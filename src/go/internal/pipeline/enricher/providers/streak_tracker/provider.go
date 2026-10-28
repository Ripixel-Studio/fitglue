package streak_tracker

import (
	"context"
	"fmt"
	"github.com/fitglue/server/src/go/pkg/domain/user"
	"log/slog"
	"strings"
	"time"

	"github.com/fitglue/server/src/go/internal/pipeline/enricher/providers"
	"github.com/fitglue/server/src/go/pkg/bootstrap"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"

	pbplugin "github.com/fitglue/server/src/go/pkg/types/pb/models/plugin"
)

// StreakTracker tracks consecutive activity days/weeks.
type StreakTracker struct {
	Service *bootstrap.Service
}

func init() {
	providers.Register(NewStreakTracker())
}

func NewStreakTracker() *StreakTracker {
	return &StreakTracker{}
}

func (p *StreakTracker) SetService(service *bootstrap.Service) {
	p.Service = service
}

func (p *StreakTracker) Name() string {
	return "streak-tracker"
}

func (p *StreakTracker) ProviderType() pbplugin.EnricherProviderType {
	return pbplugin.EnricherProviderType_ENRICHER_PROVIDER_STREAK_TRACKER
}

func (p *StreakTracker) IsIdempotent() bool { return false }

func (p *StreakTracker) Enrich(ctx context.Context, logger *slog.Logger, activity *pbactivity.StandardizedActivity, user *user.Record, inputs map[string]string, doNotRetry bool) (*providers.EnrichmentResult, error) {
	logger.Debug("streak_tracker: starting", "activity_name", activity.Name)

	// Parse config
	activityTypes := inputs["activity_types"]
	if activityTypes == "" {
		activityTypes = "any"
	}

	// The streak is computed as of this activity's start: the days with an activity
	// that started before it, plus its own day (providers.Ledger). A repost of Tuesday's
	// race after Wednesday's reads Tuesday's streak, not Wednesday's.
	activityTime := time.Now()
	if activity.StartTime != nil {
		activityTime = activity.StartTime.AsTime()
	}
	activityDate := dayOf(activityTime)
	externalId := inputs["external_id"]
	boosterId := fmt.Sprintf("streak_tracker_%s", activityTypes)

	var ledger providers.Ledger
	var longestBefore int
	if p.Service != nil && p.Service.DB != nil {
		data, err := p.Service.DB.GetBoosterData(ctx, user.UserId, boosterId)
		if err != nil {
			logger.Warn("Failed to fetch streak data", "error", err)
		} else if data != nil {
			ledger, longestBefore = streakLedgerOf(data)
		}
	}

	earlier := ledger.Before(activityTime, externalId)
	days := map[string]bool{}
	isNewDay := true
	for _, e := range earlier {
		d := dayOf(e.Start)
		days[d] = true
		if d == activityDate {
			isNewDay = false // a second activity that day: the streak already counts it
		}
	}
	hadEarlierDay := longestBefore > 0
	for d := range days {
		if d < activityDate {
			hadEarlierDay = true
		}
	}
	days[activityDate] = true
	currentStreak := runEndingAt(days, activityDate)
	longestStreak := max(longestBefore, longestRun(days, activityDate))
	streakBroken := currentStreak == 1 && isNewDay && hadEarlierDay

	// Build output
	activityLabel := getActivityLabel(activityTypes)
	var sb strings.Builder
	sb.WriteString("🔥 Streak Tracker:\n")

	if streakBroken {
		sb.WriteString(fmt.Sprintf("• Day 1 of your %s streak - starting fresh!", activityLabel))
	} else if currentStreak == 1 {
		sb.WriteString(fmt.Sprintf("• %s streak started - let's go!", capitalise(activityLabel)))
	} else {
		emoji := getStreakEmoji(currentStreak)
		sb.WriteString(fmt.Sprintf("• %s %d-day %s streak!", emoji, currentStreak, activityLabel))

		if currentStreak == longestStreak && currentStreak > 1 && isNewDay {
			sb.WriteString("\n• 🏆 New personal best streak!")
		} else if longestStreak > currentStreak {
			sb.WriteString(fmt.Sprintf("\n• Best: %d days", longestStreak))
		}
	}

	logger.Info("Streak tracker processed",
		"activity_types", activityTypes,
		"current_streak", currentStreak,
		"longest_streak", longestStreak,
		"streak_broken", streakBroken,
	)

	resultMetadata := map[string]string{
		"streak_current":       fmt.Sprintf("%d", currentStreak),
		"streak_longest":       fmt.Sprintf("%d", longestStreak),
		"activity_type_filter": activityTypes,
	}

	// Record this activity (replacing its entry on a repost) and keep the streak as of
	// the latest activity on the document and the user profile.
	if p.Service != nil && p.Service.DB != nil {
		ledger = ledger.Put(providers.LedgerEntry{ID: externalId, Start: activityTime})
		cutoff := time.Now().AddDate(0, 0, -ledgerDays)
		pruned := map[string]bool{}
		for _, e := range ledger {
			if e.Start.Before(cutoff) {
				pruned[dayOf(e.Start)] = true
			}
		}
		if len(pruned) > 0 {
			longestBefore = max(longestBefore, longestRun(pruned, ""))
			ledger = ledger.Since(cutoff)
		}
		all := map[string]bool{}
		for _, e := range ledger {
			all[dayOf(e.Start)] = true
		}
		latestDate := activityDate
		if last, ok := ledger.Latest(); ok {
			latestDate = dayOf(last.Start)
		}
		latestCurrent := runEndingAt(all, latestDate)
		latestLongest := max(longestBefore, longestRun(all, ""))
		updateData := map[string]interface{}{
			providers.LedgerField: ledger.ToFirestore(),
			"longest_before":      longestBefore,
			"current_streak":      latestCurrent,
			"longest_streak":      latestLongest,
			"last_activity_date":  latestDate,
		}
		if err := p.Service.DB.SetBoosterData(ctx, user.UserId, boosterId, updateData); err != nil {
			logger.Warn("Failed to save streak data", "error", err)
		}
		// Roll up current/longest streak onto UserProfile for the stats dashboard.
		if err := p.Service.DB.UpdateUser(ctx, user.UserId, map[string]interface{}{
			"current_streak_days": latestCurrent,
			"longest_streak_days": latestLongest,
		}); err != nil {
			logger.Warn("Failed to update user streak fields", "error", err)
		}
	}

	return &providers.EnrichmentResult{
		Description: sb.String(),
		Metadata:    resultMetadata,
		Enrichments: &pbactivity.ActivityEnrichments{
			Streak: &pbactivity.StreakSummary{
				CurrentDays: int32(currentStreak),
				LongestDays: int32(longestStreak),
			},
		},
	}, nil
}

// ledgerDays bounds the ledger; the longest streak among pruned days is kept in
// longest_before.
const ledgerDays = 800

const dayLayout = "2006-01-02"

func dayOf(t time.Time) string { return t.UTC().Format(dayLayout) }

// streakLedgerOf reads the ledger and the longest streak older than it. A document
// written by the running-total code has no ledger: its current streak is rebuilt as
// the run of days ending at last_activity_date, and its longest streak carried over.
func streakLedgerOf(data map[string]interface{}) (providers.Ledger, int) {
	if l, ok := providers.ParseLedger(data); ok {
		return l, int(providers.ToFloat64(data["longest_before"]))
	}
	longest := int(providers.ToFloat64(data["longest_streak"]))
	current := int(providers.ToFloat64(data["current_streak"]))
	last, err := time.Parse(dayLayout, providers.LedgerString(data["last_activity_date"]))
	if err != nil || current <= 0 {
		return nil, longest
	}
	var l providers.Ledger
	for i := current - 1; i >= 0; i-- {
		l = append(l, providers.LedgerEntry{Start: last.AddDate(0, 0, -i)})
	}
	return l, longest
}

// runEndingAt counts the consecutive active days ending at day.
func runEndingAt(days map[string]bool, day string) int {
	t, err := time.Parse(dayLayout, day)
	if err != nil || !days[day] {
		return 0
	}
	n := 0
	for days[t.Format(dayLayout)] {
		n++
		t = t.AddDate(0, 0, -1)
	}
	return n
}

// longestRun is the longest run of consecutive active days, among days up to and
// including through ("" for all).
func longestRun(days map[string]bool, through string) int {
	best := 0
	for d := range days {
		if through != "" && d > through {
			continue
		}
		prev, err := time.Parse(dayLayout, d)
		if err != nil || days[prev.AddDate(0, 0, -1).Format(dayLayout)] {
			continue // not the first day of a run
		}
		n := 0
		for t := prev; days[t.Format(dayLayout)] && (through == "" || t.Format(dayLayout) <= through); t = t.AddDate(0, 0, 1) {
			n++
		}
		best = max(best, n)
	}
	return best
}

func getStreakEmoji(streak int) string {
	switch {
	case streak >= 30:
		return "🏆🔥"
	case streak >= 14:
		return "💪🔥"
	case streak >= 7:
		return "🔥🔥"
	default:
		return "🔥"
	}
}

func getActivityLabel(filter string) string {
	switch filter {
	case "running":
		return "running"
	case "cycling":
		return "cycling"
	case "swimming":
		return "swimming"
	case "strength":
		return "strength training"
	default:
		return "activity"
	}
}

func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
