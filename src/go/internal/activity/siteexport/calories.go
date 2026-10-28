package siteexport

import (
	"math"

	"github.com/fitglue/server/src/go/pkg/calories"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
)

// ApplyPlatformCalories puts the source platform's calories (the activity's session
// total_calories, Strava's figure) on the showcase's calories enrichment and on its
// profile entry, replacing the MET estimate older boosters stored, and reports whether
// either changed. Activities without platform calories are left alone (their stored
// figure is FitGlue's estimate, the only one there is).
//
// The export applies it to every activity file and to the index entries it writes, so
// a re-export corrects history without re-running any pipeline; `fitgluectl
// fix-calories` applies it to the stored entries, so roundups and RefreshIndex agree.
func ApplyPlatformCalories(sc *pbactivity.ShowcasedActivity, entry *pbactivity.ShowcaseProfileEntry) bool {
	kcal, ok := calories.Platform(sc.GetActivityData())
	if !ok {
		return false
	}
	v := int32(math.Round(kcal))
	changed := false
	if sc != nil {
		if sc.Enrichments == nil {
			sc.Enrichments = &pbactivity.ActivityEnrichments{}
		}
		c := sc.Enrichments.Calories
		if c == nil || c.Kcal != v {
			text := ""
			if c != nil && c.ComparisonText != "" {
				text = calories.Comparison(kcal) // keep fun mode on only where it was on
			}
			sc.Enrichments.Calories = &pbactivity.CaloriesSummary{Kcal: v, ComparisonText: text}
			changed = true
		}
	}
	if entry != nil && entry.GetCaloriesKcal() != v {
		entry.CaloriesKcal = &v
		changed = true
	}
	return changed
}
