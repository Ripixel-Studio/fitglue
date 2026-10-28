package siteexport

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
)

// Top-level shapes from the S0 fixtures (contract §2, §3, §6).
var (
	indexKeys    = []string{"schemaVersion", "generatedAt", "siteSlug", "profile", "streaks", "activities", "roundups"}
	activityKeys = []string{"schemaVersion", "slug", "exportedAt", "category", "activity", "entry", "series", "routePoints"}
	roundupKeys  = []string{"schemaVersion", "key", "exportedAt", "previousKey", "roundup"}
	entryKeys    = []string{"showcaseId", "title", "activityType", "source", "startTime", "routeThumbnailUrl", "distanceMeters",
		"durationSeconds", "totalSets", "totalReps", "totalWeightKg", "boosterCount", "destinationCount", "hrZoneMinutes",
		"photoUrls", "createdAt", "bestEfforts", "primaryMuscles"}
	showcaseKeys = []string{"showcaseId", "title", "description", "activityType", "source", "startTime", "activityData",
		"appliedEnrichments", "tags", "createdAt", "expiresAt", "ownerDisplayName", "ownerProfilePictureUrl",
		"ownerProfileSlug", "photoUrls", "enrichments"}
	roundupSummaryKeys = []string{"key", "periodType", "periodStart", "periodEnd", "generatedAt", "totalActivities",
		"totalDurationSeconds", "totalDistanceMeters", "totalWeightKg", "hasStrength", "prCount", "hasAiSummary"}
)

func TestBuildActivity(t *testing.T) {
	cases := []struct {
		name         string
		showcase     *pbactivity.ShowcasedActivity
		entry        *pbactivity.ShowcaseProfileEntry
		category     Category
		seriesKeys   []string
		noSeriesKeys []string
		routePoints  int
		enrichments  []string
	}{
		{
			name:        "run with HR zones, GPS and sparkline",
			showcase:    runShowcase(),
			entry:       runEntry(),
			category:    CategoryCardioDist,
			seriesKeys:  []string{"n", "elapsedS", "distanceM", "heartRate", "speedMs", "altitudeM", "cadenceRpm"},
			routePoints: maxRoutePoints,
			enrichments: []string{"heartRate", "heartRateZones"},
		},
		{
			name:         "strength session with exercises",
			showcase:     liftShowcase(),
			entry:        liftEntry(),
			category:     CategoryStrength,
			seriesKeys:   []string{"n", "elapsedS", "heartRate"},
			noSeriesKeys: []string{"distanceM", "speedMs", "powerW", "cadenceRpm", "altitudeM", "temperatureC"},
			routePoints:  0,
			enrichments:  []string{"muscleHeatmap", "heartRate"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := BuildActivity(tc.showcase, tc.entry, fixedNow)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(b), "gs://") {
				t.Error("internal gs:// pointers leaked into the export")
			}
			file := decode(t, b)
			requireKeys(t, "file", file, activityKeys...)
			if file["schemaVersion"].(float64) != SchemaVersion {
				t.Errorf("schemaVersion = %v", file["schemaVersion"])
			}
			if file["slug"] != tc.showcase.ShowcaseId {
				t.Errorf("slug = %v", file["slug"])
			}
			if file["exportedAt"] != "2026-10-19T00:06:40Z" {
				t.Errorf("exportedAt = %v", file["exportedAt"])
			}
			if file["category"] != string(tc.category) {
				t.Errorf("category = %v, want %s", file["category"], tc.category)
			}

			activity := file["activity"].(map[string]any)
			requireKeys(t, "activity", activity, showcaseKeys...)
			forbidKeys(t, "activity", activity, "activityDataUri", "fitFileUri", "pipelineExecutionId", "activityId", "userId")
			if activity["activityType"] != tc.showcase.ActivityType.String() {
				t.Errorf("enum not rendered as name: %v", activity["activityType"])
			}
			if activity["expiresAt"] != nil {
				t.Errorf("unset non-optional message must be null, got %v", activity["expiresAt"])
			}
			sessions := activity["activityData"].(map[string]any)["sessions"].([]any)
			for _, s := range sessions {
				for _, l := range s.(map[string]any)["laps"].([]any) {
					lap := l.(map[string]any)
					if recs, ok := lap["records"].([]any); !ok || len(recs) != 0 {
						t.Errorf("laps[].records must be [], got %v", lap["records"])
					}
					requireKeys(t, "lap", lap, "startTime", "totalElapsedTime", "totalDistance", "exerciseName", "intensity")
				}
				if sets, ok := s.(map[string]any)["strengthSets"].([]any); ok && tc.category == CategoryStrength {
					if len(sets) != 4 {
						t.Errorf("strengthSets kept in full: got %d", len(sets))
					}
					requireKeys(t, "set", sets[0].(map[string]any), "exerciseName", "reps", "weightKg", "startTime", "primaryMuscleGroup", "secondaryMuscleGroups", "setType")
				}
			}
			enr := activity["enrichments"].(map[string]any)
			requireKeys(t, "enrichments", enr, tc.enrichments...)
			if _, ok := enr["pace"]; ok {
				t.Error("unset optional enrichment must be omitted, not emitted")
			}

			entry := file["entry"].(map[string]any)
			requireKeys(t, "entry", entry, entryKeys...)
			forbidKeys(t, "entry", entry, "slug")
			if zones := entry["hrZoneMinutes"].([]any); len(zones) != 6 && len(zones) != 0 {
				t.Errorf("hrZoneMinutes must be length 6 or empty, got %d", len(zones))
			}
			if tc.entry.Sparkline != nil {
				sp := entry["sparkline"].(map[string]any)
				requireKeys(t, "sparkline", sp, "metric", "values")
			} else if _, ok := entry["sparkline"]; ok {
				t.Error("absent sparkline must be omitted")
			}

			series := file["series"].(map[string]any)
			requireKeys(t, "series", series, tc.seriesKeys...)
			forbidKeys(t, "series", series, tc.noSeriesKeys...)
			n := int(series["n"].(float64))
			if n > maxSeriesSamples || n <= 0 {
				t.Errorf("series.n = %d", n)
			}
			for k, v := range series {
				if k == "n" {
					continue
				}
				if got := len(v.([]any)); got != n {
					t.Errorf("series.%s has %d samples, want n=%d", k, got, n)
				}
			}
			elapsed := series["elapsedS"].([]any)
			if elapsed[0].(float64) < 0 || elapsed[n-1].(float64) <= elapsed[0].(float64) {
				t.Errorf("elapsedS not increasing: %v … %v", elapsed[0], elapsed[n-1])
			}

			route := file["routePoints"].([]any)
			if len(route) != tc.routePoints {
				t.Errorf("routePoints = %d, want %d", len(route), tc.routePoints)
			}
			if len(route) > 0 {
				first := route[0].([]any)
				if len(first) != 2 || first[0].(float64) < 52 || first[1].(float64) > 0 {
					t.Errorf("routePoints are [lat, lng]: %v", first)
				}
				last := route[len(route)-1].([]any)
				wantLast := round(52.9456+591*0.00001, routePrecision)
				if last[0].(float64) != wantLast {
					t.Errorf("last route point must be the final record: %v want %v", last[0], wantLast)
				}
			}
		})
	}
}

func TestBuildActivity_NoActivityData(t *testing.T) {
	sc := runShowcase()
	sc.ActivityData = nil
	sc.Enrichments = nil
	b, err := BuildActivity(sc, runEntry(), fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	file := decode(t, b)
	activity := file["activity"].(map[string]any)
	if activity["activityData"] != nil || activity["enrichments"] != nil {
		t.Errorf("missing blob must render activityData/enrichments as null")
	}
	if file["category"] != string(CategoryCardioDist) {
		t.Errorf("category falls back to the type: %v", file["category"])
	}
	series := file["series"].(map[string]any)
	if series["n"].(float64) != 0 || len(series) != 1 {
		t.Errorf("series without records = %v", series)
	}
	if len(file["routePoints"].([]any)) != 0 {
		t.Error("routePoints must be [] without records")
	}
}

func TestBuildIndex(t *testing.T) {
	b, err := BuildIndex(testProfile(), []*pbactivity.ShowcaseProfileEntry{runEntry(), liftEntry()}, []*pbactivity.ShowcaseRoundup{weekRoundup()}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	idx := decode(t, b)
	requireKeys(t, "index", idx, indexKeys...)
	if idx["siteSlug"] != "james" || idx["generatedAt"] != "2026-10-19T00:06:40Z" {
		t.Errorf("siteSlug/generatedAt = %v/%v", idx["siteSlug"], idx["generatedAt"])
	}
	profile := idx["profile"].(map[string]any)
	requireKeys(t, "profile", profile, "slug", "displayName", "visible", "showPhotoGallery", "totalActivities", "streakHistory", "topPrs", "links", "callouts", "theme", "defaultDestination")
	forbidKeys(t, "profile", profile, "entries", "userId")
	if _, ok := profile["zoneSplit"]; ok {
		t.Error("absent optional zoneSplit must be omitted")
	}
	streaks := idx["streaks"].(map[string]any)
	if streaks["currentWeeks"].(float64) != 6 || streaks["longestWeeks"].(float64) != 6 {
		t.Errorf("streaks = %v", streaks)
	}
	acts := idx["activities"].([]any)
	if len(acts) != 2 {
		t.Fatalf("activities = %d", len(acts))
	}
	first := acts[0].(map[string]any)
	requireKeys(t, "activities[0]", first, append([]string{"slug"}, entryKeys...)...)
	if first["slug"] != first["showcaseId"] || first["slug"] != "colwick-parkrun-2026-10-17" {
		t.Errorf("slug must equal showcaseId, newest first: %v", first["slug"])
	}
	if first["sparkline"].(map[string]any)["metric"] != "heart_rate" {
		t.Errorf("sparkline lost: %v", first["sparkline"])
	}
	rs := idx["roundups"].([]any)
	if len(rs) != 1 {
		t.Fatalf("roundups = %d", len(rs))
	}
	r := rs[0].(map[string]any)
	requireKeys(t, "roundups[0]", r, roundupSummaryKeys...)
	if r["key"] != "week-42-2026" || r["periodType"] != "ROUNDUP_PERIOD_TYPE_WEEK" || r["periodEnd"] != "2026-10-19T00:00:00Z" {
		t.Errorf("roundup summary = %v", r)
	}
	if r["totalWeightKg"].(float64) != 2560 || r["hasStrength"] != true || r["prCount"].(float64) != 1 || r["hasAiSummary"] != true {
		t.Errorf("derived roundup fields = %v", r)
	}
}

func TestBuildRoundup(t *testing.T) {
	b, err := BuildRoundup(weekRoundup(), fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	file := decode(t, b)
	requireKeys(t, "file", file, roundupKeys...)
	if file["key"] != "week-42-2026" || file["previousKey"] != "week-41-2026" {
		t.Errorf("key/previousKey = %v/%v", file["key"], file["previousKey"])
	}
	r := file["roundup"].(map[string]any)
	forbidKeys(t, "roundup", r, "userId")
	requireKeys(t, "roundup", r, "roundupId", "slug", "periodType", "periodKey", "periodStart", "periodEnd", "generatedAt",
		"totalActivities", "activityTypeBreakdowns", "hrZoneMinutes", "prsAchieved", "sources", "aiSummary",
		"calloutActivities", "dayEntries", "photos", "routes", "places", "bestEfforts", "muscles",
		"effortEasyCount", "longestSessionShowcaseId")
	if _, ok := r["weather"]; ok {
		t.Error("absent optional weather must be omitted")
	}
	days := r["dayEntries"].([]any)
	if len(days) != 2 || days[1].(map[string]any)["effortLevel"].(float64) != 4 {
		t.Errorf("dayEntries = %v", days)
	}
	if src := r["sources"].([]any); src[0] != "SOURCE_STRAVA" {
		t.Errorf("sources must be enum names: %v", src)
	}
}

func TestPreviousPeriodKey(t *testing.T) {
	mk := func(pt pbactivity.RoundupPeriodType, start time.Time) *pbactivity.ShowcaseRoundup {
		return &pbactivity.ShowcaseRoundup{PeriodType: pt, PeriodStart: timestamppb.New(start)}
	}
	week, month, year := pbactivity.RoundupPeriodType_ROUNDUP_PERIOD_TYPE_WEEK, pbactivity.RoundupPeriodType_ROUNDUP_PERIOD_TYPE_MONTH, pbactivity.RoundupPeriodType_ROUNDUP_PERIOD_TYPE_YEAR
	cases := []struct {
		r    *pbactivity.ShowcaseRoundup
		want string
	}{
		{mk(week, time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)), "week-41-2026"},
		{mk(week, time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)), "week-01-2026"},
		{mk(week, time.Date(2025, 12, 29, 0, 0, 0, 0, time.UTC)), "week-52-2025"}, // ISO week 1 of 2026 → week 52 of 2025
		{mk(month, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)), "month-09-2026"},
		{mk(month, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)), "month-12-2025"},
		{mk(year, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)), "year-2025"},
		{&pbactivity.ShowcaseRoundup{PeriodType: week}, ""},
	}
	for _, tc := range cases {
		if got := PreviousPeriodKey(tc.r); got != tc.want {
			t.Errorf("PreviousPeriodKey(%s %v) = %q, want %q", tc.r.PeriodType, tc.r.PeriodStart, got, tc.want)
		}
	}
	if got := PeriodKey(week, time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)); got != "week-42-2026" {
		t.Errorf("PeriodKey = %q", got)
	}
}

func TestComputeStreaks(t *testing.T) {
	cases := []struct {
		name   string
		active []bool
		want   Streaks
	}{
		{"absent", nil, Streaks{}},
		{"all active", []bool{true, true, true}, Streaks{3, 3}},
		{"ends inactive", []bool{true, true, false}, Streaks{0, 2}},
		{"longest earlier", []bool{true, true, true, false, true}, Streaks{1, 3}},
	}
	for _, tc := range cases {
		p := &pbactivity.ShowcaseProfile{}
		if tc.active != nil {
			p.StreakHistory = &pbactivity.WeeklyStreakHistory{WeeklyActive: tc.active}
		}
		if got := ComputeStreaks(p); got != tc.want {
			t.Errorf("%s: got %+v want %+v", tc.name, got, tc.want)
		}
	}
	if got := ComputeStreaks(nil); got != (Streaks{}) {
		t.Errorf("nil profile: %+v", got)
	}
}

func TestResolveCategory(t *testing.T) {
	withSets := &pbactivity.ShowcasedActivity{
		ActivityType: pbactivity.ActivityType_ACTIVITY_TYPE_RUN,
		ActivityData: &pbactivity.StandardizedActivity{Sessions: []*pbactivity.Session{{StrengthSets: []*pbactivity.StrengthSet{{ExerciseName: "Squat"}}}}},
	}
	typed := func(t pbactivity.ActivityType) *pbactivity.ShowcasedActivity {
		return &pbactivity.ShowcasedActivity{ActivityType: t, ActivityData: &pbactivity.StandardizedActivity{Sessions: []*pbactivity.Session{{}}}}
	}
	cases := []struct {
		sc   *pbactivity.ShowcasedActivity
		want Category
	}{
		{withSets, CategoryStrength},
		{typed(pbactivity.ActivityType_ACTIVITY_TYPE_RUN), CategoryCardioDist},
		{typed(pbactivity.ActivityType_ACTIVITY_TYPE_SWIM), CategoryCardioDist},
		{typed(pbactivity.ActivityType_ACTIVITY_TYPE_YOGA), CategoryCardioTime},
		{typed(pbactivity.ActivityType_ACTIVITY_TYPE_WORKOUT), CategoryCardioTime},
		{typed(pbactivity.ActivityType_ACTIVITY_TYPE_CROSSFIT), CategoryCardioTime},
		{typed(pbactivity.ActivityType_ACTIVITY_TYPE_TENNIS), CategorySport},
		{typed(pbactivity.ActivityType_ACTIVITY_TYPE_GOLF), CategorySport},
		{typed(pbactivity.ActivityType_ACTIVITY_TYPE_WEIGHT_TRAINING), CategoryUntraditional},
		{typed(pbactivity.ActivityType_ACTIVITY_TYPE_SNOWBOARD), CategoryUntraditional},
		{typed(pbactivity.ActivityType_ACTIVITY_TYPE_UNSPECIFIED), CategoryUntraditional},
	}
	for _, tc := range cases {
		if got := ResolveCategory(tc.sc); got != tc.want {
			t.Errorf("%s: got %s want %s", tc.sc.ActivityType, got, tc.want)
		}
	}
}

func TestDeriveTelemetry(t *testing.T) {
	t.Run("fewer records than buckets keeps every sample", func(t *testing.T) {
		data := &pbactivity.StandardizedActivity{Sessions: []*pbactivity.Session{{
			StartTime: ts(runStart),
			Laps:      []*pbactivity.Lap{{Records: runRecords(runStart, 30)}},
		}}}
		s, route := DeriveTelemetry(data)
		if s.N != 30 || len(s.HeartRate) != 30 || *s.ElapsedS[29] != 29 || *s.DistanceM[0] != 0 {
			t.Errorf("series = n=%d hr=%d elapsed[29]=%v dist[0]=%v", s.N, len(s.HeartRate), *s.ElapsedS[29], *s.DistanceM[0])
		}
		if len(route) != 30 {
			t.Errorf("route kept in full below the cap: %d", len(route))
		}
	})
	t.Run("gaps become null, power and temperature only when present", func(t *testing.T) {
		temp := int32(9)
		recs := []*pbactivity.Record{
			{Timestamp: ts(runStart), HeartRate: 100, Power: 200, Temperature: &temp},
			{Timestamp: ts(runStart.Add(time.Second))},
			{Timestamp: ts(runStart.Add(2 * time.Second)), HeartRate: 120},
		}
		data := &pbactivity.StandardizedActivity{Sessions: []*pbactivity.Session{{Laps: []*pbactivity.Lap{{Records: recs}}}}}
		s, route := DeriveTelemetry(data)
		if s.N != 3 || s.HeartRate[1] != nil || *s.HeartRate[2] != 120 {
			t.Errorf("heartRate = %v", s.HeartRate)
		}
		if s.PowerW == nil || *s.PowerW[0] != 200 || s.PowerW[1] != nil {
			t.Errorf("powerW = %v", s.PowerW)
		}
		if s.TemperatureC == nil || *s.TemperatureC[0] != 9 {
			t.Errorf("temperatureC = %v", s.TemperatureC)
		}
		if s.DistanceM != nil || s.SpeedMs != nil || s.AltitudeM != nil || s.CadenceRpm != nil {
			t.Error("metrics no record carried must be omitted")
		}
		if *s.ElapsedS[0] != 0 {
			t.Errorf("elapsed base falls back to the first record: %v", *s.ElapsedS[0])
		}
		if len(route) != 0 {
			t.Errorf("fewer than %d GPS points must give []", minRoutePoints)
		}
	})
	t.Run("under ten GPS points is no route", func(t *testing.T) {
		data := &pbactivity.StandardizedActivity{Sessions: []*pbactivity.Session{{Laps: []*pbactivity.Lap{{Records: runRecords(runStart, 9)}}}}}
		if _, route := DeriveTelemetry(data); len(route) != 0 {
			t.Errorf("got %d route points", len(route))
		}
	})
	t.Run("nil data", func(t *testing.T) {
		s, route := DeriveTelemetry(nil)
		if s.N != 0 || route == nil || len(route) != 0 {
			t.Errorf("nil data: %+v %v", s, route)
		}
	})
}

func TestApplyPlatformCalories(t *testing.T) {
	strava := 1346.0
	old := int32(523)
	sc := &pbactivity.ShowcasedActivity{
		ActivityData: &pbactivity.StandardizedActivity{Sessions: []*pbactivity.Session{{TotalCalories: &strava}}},
		Enrichments:  &pbactivity.ActivityEnrichments{Calories: &pbactivity.CaloriesSummary{Kcal: 523, ComparisonText: "≈ 1.8 slice of pizza 🍕"}},
	}
	entry := &pbactivity.ShowcaseProfileEntry{CaloriesKcal: &old}
	if !ApplyPlatformCalories(sc, entry) {
		t.Fatal("expected a change")
	}
	if sc.Enrichments.Calories.Kcal != 1346 || sc.Enrichments.Calories.ComparisonText != "≈ 4.7 slice of pizza 🍕" || entry.GetCaloriesKcal() != 1346 {
		t.Errorf("got %+v / %d", sc.Enrichments.Calories, entry.GetCaloriesKcal())
	}
	if ApplyPlatformCalories(sc, entry) {
		t.Error("second pass should change nothing")
	}
	// No platform figure: the estimate stays.
	hevy := &pbactivity.ShowcasedActivity{ActivityData: &pbactivity.StandardizedActivity{Sessions: []*pbactivity.Session{{}}},
		Enrichments: &pbactivity.ActivityEnrichments{Calories: &pbactivity.CaloriesSummary{Kcal: 300}}}
	if ApplyPlatformCalories(hevy, &pbactivity.ShowcaseProfileEntry{}) || hevy.Enrichments.Calories.Kcal != 300 {
		t.Error("an activity without platform calories must be left alone")
	}
}
