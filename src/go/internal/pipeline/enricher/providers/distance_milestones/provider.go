package distance_milestones

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

// DistanceMilestones celebrates lifetime distance achievements.
type DistanceMilestones struct {
	Service *bootstrap.Service
}

func init() {
	providers.Register(NewDistanceMilestones())
}

func NewDistanceMilestones() *DistanceMilestones {
	return &DistanceMilestones{}
}

func (p *DistanceMilestones) SetService(service *bootstrap.Service) {
	p.Service = service
}

func (p *DistanceMilestones) Name() string {
	return "distance-milestones"
}

func (p *DistanceMilestones) ProviderType() pbplugin.EnricherProviderType {
	return pbplugin.EnricherProviderType_ENRICHER_PROVIDER_DISTANCE_MILESTONES
}

func (p *DistanceMilestones) IsIdempotent() bool { return false }

// Milestone thresholds in km
var milestones = []float64{100, 250, 500, 1000, 2500, 5000, 10000, 25000, 50000, 100000}

func (p *DistanceMilestones) Enrich(ctx context.Context, logger *slog.Logger, activity *pbactivity.StandardizedActivity, user *user.Record, inputs map[string]string, doNotRetry bool) (*providers.EnrichmentResult, error) {
	logger.Debug("distance_milestones: starting", "activity_name", activity.Name)

	// Parse config
	sport := inputs["sport"]
	if sport == "" {
		sport = "any"
	}

	// Get activity distance
	var distanceKm float64
	for _, session := range activity.Sessions {
		distanceKm += session.TotalDistance / 1000
	}

	if distanceKm == 0 {
		logger.Debug("distance_milestones: no distance in activity")
		return &providers.EnrichmentResult{
			Metadata: map[string]string{"milestone_status": "no_distance"},
		}, nil
	}

	// Check sport filter
	if sport != "any" && !matchesSport(activity.Type, sport) {
		logger.Debug("distance_milestones: activity does not match sport filter", "sport", sport, "type", activity.Type)
		return &providers.EnrichmentResult{
			Metadata: map[string]string{"milestone_status": "filtered"},
		}, nil
	}

	// The lifetime total is computed as of this activity's start: everything that
	// started before it (providers.Ledger), so a repost or a late upload neither counts
	// the activities after it nor counts itself twice.
	boosterId := fmt.Sprintf("distance_milestones_%s", sport)
	externalId := inputs["external_id"]
	activityTime := time.Now()
	if activity.StartTime != nil {
		activityTime = activity.StartTime.AsTime()
	}
	var ledger providers.Ledger
	var legacyLastID string
	if p.Service != nil && p.Service.DB != nil {
		data, err := p.Service.DB.GetBoosterData(ctx, user.UserId, boosterId)
		if err != nil {
			logger.Warn("Failed to fetch lifetime distance", "error", err)
		} else if data != nil {
			ledger = lifetimeLedgerOf(data)
			legacyLastID, _ = data["last_external_id"].(string)
		}
	}
	alreadyCarried := inputs["is_repost"] == "true" || (externalId != "" && externalId == legacyLastID)
	lifetimeDistance := ledger.Before(activityTime, externalId).Sum(nil)
	// A legacy total written after this activity started is still the lifetime so far
	// (minus this activity when it is already inside it): dropping it would restart the
	// lifetime at zero.
	for _, e := range ledger {
		if e.ID == "" && !e.Start.Before(activityTime) {
			if alreadyCarried {
				lifetimeDistance += max(0, e.Value-distanceKm)
			} else {
				lifetimeDistance += e.Value
			}
		}
	}

	// Calculate new total
	previousDistance := lifetimeDistance
	newDistance := lifetimeDistance + distanceKm

	// Check for milestone crossings
	var crossedMilestones []float64
	for _, m := range milestones {
		if previousDistance < m && newDistance >= m {
			crossedMilestones = append(crossedMilestones, m)
		}
	}

	// Persist updated lifetime distance + cached result for same-source dedup

	// Build output
	var sb strings.Builder
	resultMetadata := map[string]string{}

	if len(crossedMilestones) > 0 {
		// Celebrate milestone!
		biggest := crossedMilestones[len(crossedMilestones)-1]
		emoji := getMilestoneEmoji(biggest)
		sb.WriteString(fmt.Sprintf("%s Lifetime %s:\n", emoji, getSportLabel(sport)))
		sb.WriteString(fmt.Sprintf("🎉 MILESTONE: %.0f km reached!\n", biggest))
		sb.WriteString(fmt.Sprintf("• Total: %.1f km\n", newDistance))
		sb.WriteString(fmt.Sprintf("• This %s: +%.1f km", getSportLabel(sport), distanceKm))

		logger.Info("Distance milestone reached",
			"milestone", biggest,
			"lifetime_distance", newDistance,
			"sport", sport,
		)

		resultMetadata["milestone_reached"] = fmt.Sprintf("%.0f", biggest)
		resultMetadata["lifetime_distance"] = fmt.Sprintf("%.1f", newDistance)
		resultMetadata["activity_distance"] = fmt.Sprintf("%.1f", distanceKm)
	} else {
		// No milestone, show progress
		nextMilestone := getNextMilestone(newDistance)
		remaining := nextMilestone - newDistance

		sb.WriteString(fmt.Sprintf("📊 Lifetime %s:\n", getSportLabel(sport)))
		sb.WriteString(fmt.Sprintf("• %.1f km total\n", newDistance))
		sb.WriteString(fmt.Sprintf("• Next milestone: %.0f km (%.1f km to go)", nextMilestone, remaining))

		logger.Info("Distance milestones processed",
			"lifetime_distance", newDistance,
			"next_milestone", nextMilestone,
			"sport", sport,
		)

		resultMetadata["lifetime_distance"] = fmt.Sprintf("%.1f", newDistance)
		resultMetadata["next_milestone"] = fmt.Sprintf("%.0f", nextMilestone)
	}

	// Record this activity (replacing its entry on a repost). Entries older than a year
	// fold into one carried total. A repost of an activity a legacy total already holds
	// (it started before that total was written) is not added again.
	if p.Service != nil && p.Service.DB != nil && externalId != "" && !(alreadyCarried && carriedAfter(ledger, activityTime)) {
		ledger = ledger.Put(providers.LedgerEntry{ID: externalId, Start: activityTime, Value: distanceKm}).Fold(time.Now().AddDate(-1, 0, 0))
		updateData := map[string]interface{}{
			providers.LedgerField: ledger.ToFirestore(),
			"lifetime_distance":   ledger.Sum(nil),
		}
		if err := p.Service.DB.SetBoosterData(ctx, user.UserId, boosterId, updateData); err != nil {
			logger.Warn("Failed to save lifetime distance", "error", err)
		}
	}

	var milestoneSummary *pbactivity.DistanceMilestoneSummary
	if len(crossedMilestones) > 0 {
		biggest := crossedMilestones[len(crossedMilestones)-1]
		milestoneSummary = &pbactivity.DistanceMilestoneSummary{
			MilestoneKm:        biggest,
			LifetimeDistanceKm: newDistance,
			ActivityTypeLabel:  getSportLabel(sport),
		}
	} else {
		nextMilestoneVal := getNextMilestone(newDistance)
		milestoneSummary = &pbactivity.DistanceMilestoneSummary{
			MilestoneKm:        0,
			LifetimeDistanceKm: newDistance,
			NextMilestoneKm:    &nextMilestoneVal,
			ActivityTypeLabel:  getSportLabel(sport),
		}
	}

	return &providers.EnrichmentResult{
		Description: sb.String(),
		Metadata:    resultMetadata,
		Enrichments: &pbactivity.ActivityEnrichments{
			DistanceMilestone: milestoneSummary,
		},
	}, nil
}

// lifetimeLedgerOf reads the ledger. A document written by the running-total code has
// none: its lifetime total is carried over as one entry dated when it was last written.
func lifetimeLedgerOf(data map[string]interface{}) providers.Ledger {
	if l, ok := providers.ParseLedger(data); ok {
		return l
	}
	if total := providers.ToFloat64(data["lifetime_distance"]); total > 0 {
		return providers.Ledger{{Start: providers.LastUpdated(data), Value: total}}
	}
	return nil
}

// carriedAfter reports a carried total (ID "") written after start, which already
// holds an activity that started at start and is being processed again.
func carriedAfter(l providers.Ledger, start time.Time) bool {
	for _, e := range l {
		if e.ID == "" && start.Before(e.Start) {
			return true
		}
	}
	return false
}

func getMilestoneEmoji(km float64) string {
	switch {
	case km >= 10000:
		return "🏆🎉🏅"
	case km >= 5000:
		return "🏆🎉"
	case km >= 1000:
		return "🎉🏅"
	case km >= 500:
		return "🎉"
	default:
		return "✨"
	}
}

func getSportLabel(sport string) string {
	switch sport {
	case "running":
		return "running"
	case "cycling":
		return "cycling"
	case "swimming":
		return "swimming"
	default:
		return "distance"
	}
}

func getNextMilestone(current float64) float64 {
	for _, m := range milestones {
		if current < m {
			return m
		}
	}
	return milestones[len(milestones)-1] // Cap at highest
}

func matchesSport(actType pbactivity.ActivityType, sport string) bool {
	switch sport {
	case "running":
		return actType == pbactivity.ActivityType_ACTIVITY_TYPE_RUN ||
			actType == pbactivity.ActivityType_ACTIVITY_TYPE_TRAIL_RUN ||
			actType == pbactivity.ActivityType_ACTIVITY_TYPE_VIRTUAL_RUN
	case "cycling":
		return actType == pbactivity.ActivityType_ACTIVITY_TYPE_RIDE ||
			actType == pbactivity.ActivityType_ACTIVITY_TYPE_MOUNTAIN_BIKE_RIDE ||
			actType == pbactivity.ActivityType_ACTIVITY_TYPE_GRAVEL_RIDE ||
			actType == pbactivity.ActivityType_ACTIVITY_TYPE_VIRTUAL_RIDE
	case "swimming":
		return actType == pbactivity.ActivityType_ACTIVITY_TYPE_SWIM
	default:
		return true
	}
}
