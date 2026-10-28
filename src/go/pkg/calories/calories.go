// Package calories decides how many calories an activity burned.
//
// The source platform's figure wins: Strava (and the Garmin / ROXFIT uploads Strava
// computes from) works calories out from heart rate, power and the athlete's own
// profile, and lands on the activity as session total_calories. FitGlue only
// estimates (MET × weight × duration) when the source gave none — Hevy workouts, or
// a Strava activity without heart rate.
//
// Before this package the calories booster always estimated, with a 70 kg default
// weight and a MET picked from the activity type as it was when the booster ran
// (ROXFIT's Hyrox races arrive as "Workout", MET 5): Birmingham's 1:29:42 solo was
// 523 kcal against Strava's 1,346.
package calories

import (
	"fmt"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
)

// Source says where a calorie figure came from.
type Source string

const (
	SourcePlatform Source = "platform" // the source activity's own total_calories
	SourceEstimate Source = "estimate" // FitGlue's MET estimate
)

// Platform returns the source platform's calories for the activity: the sum of its
// sessions' total_calories, when any session carries one.
func Platform(act *pbactivity.StandardizedActivity) (kcal float64, ok bool) {
	for _, s := range act.GetSessions() {
		if c := s.GetTotalCalories(); c > 0 {
			kcal += c
			ok = true
		}
	}
	return kcal, ok
}

// Food is a fun equivalent for a calorie figure ("≈ 4.7 slice of pizza 🍕").
type Food struct {
	Name     string
	Calories float64
	Emoji    string
}

// Foods are tried in order; the first giving a ratio between 0.5 and 10 is used.
var Foods = []Food{
	{Name: "slice of pizza", Calories: 285, Emoji: "🍕"},
	{Name: "donut", Calories: 250, Emoji: "🍩"},
	{Name: "banana", Calories: 105, Emoji: "🍌"},
	{Name: "beer", Calories: 150, Emoji: "🍺"},
	{Name: "chocolate bar", Calories: 230, Emoji: "🍫"},
	{Name: "cookie", Calories: 80, Emoji: "🍪"},
	{Name: "apple", Calories: 95, Emoji: "🍎"},
	{Name: "glass of wine", Calories: 125, Emoji: "🍷"},
	{Name: "burger", Calories: 540, Emoji: "🍔"},
	{Name: "ice cream scoop", Calories: 140, Emoji: "🍨"},
}

// FoodFor picks the food a calorie figure is compared with (pizza when none fits).
func FoodFor(kcal float64) Food {
	for _, f := range Foods {
		if r := kcal / f.Calories; r >= 0.5 && r <= 10 {
			return f
		}
	}
	return Foods[0]
}

// Comparison is the "≈ 4.7 slice of pizza 🍕" text for a figure, or "" for 50 kcal
// or less.
func Comparison(kcal float64) string {
	if kcal <= 50 {
		return ""
	}
	f := FoodFor(kcal)
	return fmt.Sprintf("≈ %.1f %s %s", kcal/f.Calories, f.Name, f.Emoji)
}
