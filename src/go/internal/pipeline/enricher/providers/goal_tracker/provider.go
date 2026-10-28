package goal_tracker

import (
	"context"
	"fmt"
	"github.com/fitglue/server/src/go/pkg/domain/user"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/fitglue/server/src/go/internal/pipeline/enricher/providers"
	"github.com/fitglue/server/src/go/pkg/bootstrap"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"

	pbplugin "github.com/fitglue/server/src/go/pkg/types/pb/models/plugin"
)

// GoalTracker tracks progress toward configurable goals.
// NOTE: Full persistence will be added in a future update using the user_data service.
// For now, it outputs progress from this activity toward the goal.
type GoalTracker struct {
	Service *bootstrap.Service
}

func init() {
	providers.Register(NewGoalTracker())
}

func NewGoalTracker() *GoalTracker {
	return &GoalTracker{}
}

func (p *GoalTracker) SetService(service *bootstrap.Service) {
	p.Service = service
}

func (p *GoalTracker) Name() string {
	return "goal-tracker"
}

func (p *GoalTracker) ProviderType() pbplugin.EnricherProviderType {
	return pbplugin.EnricherProviderType_ENRICHER_PROVIDER_GOAL_TRACKER
}

func (p *GoalTracker) IsIdempotent() bool { return false }

func (p *GoalTracker) Enrich(ctx context.Context, logger *slog.Logger, activity *pbactivity.StandardizedActivity, user *user.Record, inputs map[string]string, doNotRetry bool) (*providers.EnrichmentResult, error) {
	logger.Debug("goal_tracker: starting", "activity_name", activity.Name)

	// Parse config
	period := inputs["period"]
	if period == "" {
		period = "month"
	}
	metric := inputs["metric"]
	if metric == "" {
		metric = "distance"
	}
	target, _ := strconv.ParseFloat(inputs["target"], 64)
	if target <= 0 {
		target = 100 // Default 100km goal
	}

	// Determine which period this activity belongs to using its own start time,
	// not processing time. This ensures backdated uploads count toward the correct period.
	activityTime := time.Now()
	if activity.StartTime != nil {
		activityTime = activity.StartTime.AsTime()
	}
	activityPeriod := getPeriodKey(period, activityTime)
	isCurrentPeriod := activityPeriod == getPeriodKey(period, time.Now())

	// Get current metric value from this activity
	activityValue := getMetricValue(activity, metric)
	externalId := inputs["external_id"]

	// Progress is computed as of this activity's start: the period's activities that
	// started before it, plus this one (providers.Ledger). A repost, a same-source
	// update or a late upload of an earlier activity reads the same total it read the
	// first time, instead of a running total that already holds the activities after it.
	boosterId := fmt.Sprintf("goal_tracker_%s_%s", period, metric)
	var ledger providers.Ledger
	var legacyLastID string
	if p.Service != nil && p.Service.DB != nil {
		data, err := p.Service.DB.GetBoosterData(ctx, user.UserId, boosterId)
		if err != nil {
			logger.Warn("Failed to fetch goal progress", "error", err)
		} else if data != nil {
			ledger = ledgerOf(data)
			legacyLastID, _ = data["last_external_id"].(string)
		}
	}
	inPeriod := func(e providers.LedgerEntry) bool { return entryPeriod(period, e) == activityPeriod }
	accumulatedProgress := ledger.Before(activityTime, externalId).Sum(inPeriod)

	// Calculate new total
	newTotal := accumulatedProgress + activityValue
	percentage := (newTotal / target) * 100
	if percentage > 100 {
		percentage = 100
	}

	// Days remaining is only meaningful for the current period
	daysRemaining := 0
	if isCurrentPeriod {
		daysRemaining = getDaysRemaining(period, activityTime)
	}

	// Build output
	var sb strings.Builder
	periodLabel := getPeriodLabel(period, activityTime)
	metricLabel := getMetricLabel(metric)

	// Progress bar
	progressBar := buildProgressBar(percentage)

	sb.WriteString(fmt.Sprintf("🎯 %s Goal Progress:\n", periodLabel))
	sb.WriteString(fmt.Sprintf("• %s %.1f/%.0f %s\n", progressBar, newTotal, target, metricLabel))
	sb.WriteString(fmt.Sprintf("• ➕ This activity: +%.1f %s", activityValue, metricLabel))

	if !isCurrentPeriod {
		sb.WriteString("\n• 📅 Past activity — counted toward original period")
	} else if newTotal < target {
		remaining := target - newTotal
		if daysRemaining > 0 {
			neededPerDay := remaining / float64(daysRemaining)
			sb.WriteString(fmt.Sprintf("\n• 💡 Need %.1f %s/day to hit goal", neededPerDay, metricLabel))
		}
	} else {
		sb.WriteString("\n• 🏆 Goal complete!")
	}

	logger.Info("Goal tracker processed",
		"period", period,
		"metric", metric,
		"activity_value", activityValue,
		"accumulated", newTotal,
		"target", target,
		"percentage", percentage,
		"is_current_period", isCurrentPeriod,
	)

	resultMetadata := map[string]string{
		"goal_status":      fmt.Sprintf("%.0f%%", percentage),
		"goal_accumulated": fmt.Sprintf("%.2f", newTotal),
		"goal_target":      fmt.Sprintf("%.0f", target),
		"goal_period":      period,
		"goal_metric":      metric,
	}

	// Record this activity in the ledger (replacing its earlier entry on a repost), and
	// keep the period total of the latest activity as "accumulated" for readers of the
	// booster data.
	alreadyCarried := inputs["is_repost"] == "true" || (externalId != "" && externalId == legacyLastID)
	if p.Service != nil && p.Service.DB != nil && externalId != "" && !(alreadyCarried && carriedAfter(ledger, activityPeriod, activityTime)) {
		ledger = ledger.Put(providers.LedgerEntry{ID: externalId, Start: activityTime, Value: activityValue}).Since(time.Now().AddDate(0, 0, -ledgerDays))
		latestPeriod, latestTotal := activityPeriod, newTotal
		if last, ok := ledger.Latest(); ok {
			latestPeriod = entryPeriod(period, last)
			latestTotal = ledger.Sum(func(e providers.LedgerEntry) bool { return entryPeriod(period, e) == latestPeriod })
		}
		updateData := map[string]interface{}{
			providers.LedgerField: ledger.ToFirestore(),
			"accumulated":         latestTotal,
			"period_key":          latestPeriod,
		}
		if err := p.Service.DB.SetBoosterData(ctx, user.UserId, boosterId, updateData); err != nil {
			logger.Warn("Failed to save goal progress", "error", err)
		}
	}

	return &providers.EnrichmentResult{
		Description: sb.String(),
		Metadata:    resultMetadata,
		Enrichments: &pbactivity.ActivityEnrichments{
			GoalTracker: &pbactivity.GoalTrackerSummary{
				Goals: []*pbactivity.GoalEntry{
					{
						Label:         fmt.Sprintf("%s · %.0f %s", getPeriodLabel(period, activityTime), target, getMetricLabel(metric)),
						Current:       newTotal,
						Target:        target,
						Unit:          getMetricLabel(metric),
						OnPace:        newTotal >= target,
						DaysRemaining: int32(daysRemaining),
					},
				},
			},
		},
	}, nil
}

func buildProgressBar(percentage float64) string {
	filled := int(percentage / 10)
	empty := 10 - filled
	bar := strings.Repeat("█", filled) + strings.Repeat("░", empty)
	return fmt.Sprintf("[%s] %.0f%%", bar, percentage)
}

// ledgerDays bounds the ledger: a year period needs the year so far.
const ledgerDays = 400

// ledgerOf reads the goal's ledger. A document written by the running-total code has
// none: its total (accumulated in period_key) is carried over as one entry dated when
// it was last written.
func ledgerOf(data map[string]interface{}) providers.Ledger {
	if l, ok := providers.ParseLedger(data); ok {
		return l
	}
	key, _ := data["period_key"].(string)
	total := providers.ToFloat64(data["accumulated"])
	if key == "" || total <= 0 {
		return nil
	}
	return providers.Ledger{{Start: providers.LastUpdated(data), Value: total, Period: key}}
}

// carriedAfter reports a carried-over legacy total in the activity's period that was
// written after the activity started. A repost of such an activity is already inside
// that total, so it is not added to the ledger a second time.
func carriedAfter(l providers.Ledger, periodKey string, start time.Time) bool {
	for _, e := range l {
		if e.ID == "" && e.Period == periodKey && start.Before(e.Start) {
			return true
		}
	}
	return false
}

// entryPeriod is the period an entry counts toward.
func entryPeriod(period string, e providers.LedgerEntry) string {
	if e.Period != "" {
		return e.Period
	}
	return getPeriodKey(period, e.Start)
}

func getMetricValue(activity *pbactivity.StandardizedActivity, metric string) float64 {
	var total float64
	for _, session := range activity.Sessions {
		switch metric {
		case "distance":
			total += session.TotalDistance / 1000 // Convert to km
		case "duration":
			total += session.TotalElapsedTime / 3600 // Convert to hours
		case "activities":
			total = 1
		}
	}
	return total
}

func getPeriodLabel(period string, t time.Time) string {
	switch period {
	case "week":
		return "Weekly"
	case "year":
		return fmt.Sprintf("%d", t.Year())
	default:
		return t.Format("January")
	}
}

// getPeriodKey returns a unique key for the given time's period to track resets
func getPeriodKey(period string, t time.Time) string {
	switch period {
	case "week":
		year, week := t.ISOWeek()
		return fmt.Sprintf("%d-W%02d", year, week)
	case "year":
		return fmt.Sprintf("%d", t.Year())
	default: // month
		return t.Format("2006-01")
	}
}

func getMetricLabel(metric string) string {
	switch metric {
	case "duration":
		return "hours"
	case "activities":
		return "activities"
	case "elevation":
		return "m elevation"
	default:
		return "km"
	}
}

func getDaysRemaining(period string, t time.Time) int {
	switch period {
	case "week":
		return 7 - int(t.Weekday())
	case "year":
		endOfYear := time.Date(t.Year(), 12, 31, 0, 0, 0, 0, t.Location())
		return int(endOfYear.Sub(t).Hours() / 24)
	default: // month
		endOfMonth := time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, t.Location())
		return endOfMonth.Day() - t.Day()
	}
}
