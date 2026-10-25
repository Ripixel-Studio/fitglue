package siteexport

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
)

// The in-memory fixtures below mirror the S0 reference set in
// jarvis-memory/specs/ripixel-website/fixtures: a parkrun with HR zones and a
// GPS track, a strength session with exercises, and a weekly roundup.

var (
	fixedNow  = time.Date(2026, 10, 19, 0, 6, 40, 0, time.UTC)
	runStart  = time.Date(2026, 10, 17, 8, 0, 0, 0, time.UTC)
	liftStart = time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)
)

func ts(t time.Time) *timestamppb.Timestamp { return timestamppb.New(t) }

func testProfile() *pbactivity.ShowcaseProfile {
	return &pbactivity.ShowcaseProfile{
		Slug:                 "james",
		UserId:               "uid-james",
		DisplayName:          "James",
		Visible:              true,
		ShowPhotoGallery:     true,
		TotalActivities:      2,
		TotalDistanceMeters:  5010,
		TotalDurationSeconds: 4612,
		CreatedAt:            ts(time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC)),
		StreakHistory: &pbactivity.WeeklyStreakHistory{
			WeeksTracked: 12,
			MissedWeeks:  2,
			WeeklyActive: []bool{true, true, true, false, true, false, true, true, true, true, true, true},
		},
		TopPrs: []*pbactivity.ShowcaseTopPR{
			{RecordType: "5k", Value: 1192, Unit: "seconds", AchievedAt: ts(runStart)},
		},
		Entries: []*pbactivity.ShowcaseProfileEntry{{ShowcaseId: "page-slice-must-be-dropped"}},
	}
}

func runEntry() *pbactivity.ShowcaseProfileEntry {
	avgHR := int32(171)
	kcal := int32(342)
	pr := "★ 5K 19:52 · −14s"
	return &pbactivity.ShowcaseProfileEntry{
		ShowcaseId:        "colwick-parkrun-2026-10-17",
		Title:             "Colwick parkrun #87",
		ActivityType:      pbactivity.ActivityType_ACTIVITY_TYPE_RUN,
		Source:            pbactivity.ActivitySource_SOURCE_STRAVA,
		StartTime:         ts(runStart),
		RouteThumbnailUrl: "https://assets.fitglue.tech/act_1/route-thumbnail.svg",
		DistanceMeters:    5010,
		DurationSeconds:   1192,
		BoosterCount:      19,
		Sparkline:         &pbactivity.EntrySparkline{Metric: "heart_rate", Values: []float64{141.5, 160, 188}},
		AvgHeartRate:      &avgHR,
		CaloriesKcal:      &kcal,
		PrLabel:           &pr,
		HrZoneMinutes:     []int32{0, 0, 2, 5, 9, 4},
		PhotoUrls:         []string{"https://storage.googleapis.com/b/activity_photos/uid/act_1/finish_p.jpg"},
		CreatedAt:         ts(runStart.Add(74 * time.Minute)),
	}
}

func liftEntry() *pbactivity.ShowcaseProfileEntry {
	return &pbactivity.ShowcaseProfileEntry{
		ShowcaseId:      "push-day-2026-10-13",
		Title:           "Push Day",
		ActivityType:    pbactivity.ActivityType_ACTIVITY_TYPE_WEIGHT_TRAINING,
		Source:          pbactivity.ActivitySource_SOURCE_HEVY,
		StartTime:       ts(liftStart),
		DurationSeconds: 3420,
		TotalSets:       4,
		TotalReps:       32,
		TotalWeightKg:   2560,
		HrZoneMinutes:   []int32{},
		CreatedAt:       ts(liftStart.Add(time.Hour)),
		PrimaryMuscles:  []string{"chest", "triceps"},
	}
}

// runRecords builds a 1 Hz track: n records with HR, speed, cadence, altitude,
// cumulative distance and a GPS position that moves north.
func runRecords(start time.Time, n int) []*pbactivity.Record {
	recs := make([]*pbactivity.Record, 0, n)
	for i := 0; i < n; i++ {
		recs = append(recs, &pbactivity.Record{
			Timestamp:    ts(start.Add(time.Duration(i) * time.Second)),
			HeartRate:    int32(140 + i%50),
			Speed:        4.2,
			Cadence:      176,
			Altitude:     28 + float64(i)/100,
			Distance:     float64(i) * 4.2,
			PositionLat:  52.9456 + float64(i)*0.00001,
			PositionLong: -1.0904,
		})
	}
	return recs
}

func runShowcase() *pbactivity.ShowcasedActivity {
	pipelineID := "run-123"
	return &pbactivity.ShowcasedActivity{
		ShowcaseId:          "colwick-parkrun-2026-10-17",
		ActivityId:          "act_1",
		Title:               "Colwick parkrun #87",
		Description:         "Lovely morning.\n\n🏃 Pace:\nsplits...\n\nPosted via FitGlue 💪",
		ActivityType:        pbactivity.ActivityType_ACTIVITY_TYPE_RUN,
		Source:              pbactivity.ActivitySource_SOURCE_STRAVA,
		StartTime:           ts(runStart),
		ActivityDataUri:     "gs://bucket/showcase_data/uid/x_data.json",
		FitFileUri:          "gs://bucket/fit/x.fit",
		PipelineExecutionId: &pipelineID,
		AppliedEnrichments:  []string{"HEART_RATE_SUMMARY", "HEART_RATE_ZONES", "PARKRUN"},
		Tags:                []string{"parkrun"},
		CreatedAt:           ts(runStart.Add(74 * time.Minute)),
		OwnerDisplayName:    "James",
		OwnerProfileSlug:    "james",
		PhotoUrls:           []string{"https://storage.googleapis.com/b/activity_photos/uid/act_1/finish_p.jpg"},
		ActivityData: &pbactivity.StandardizedActivity{
			Name: "Colwick parkrun #87",
			Sessions: []*pbactivity.Session{{
				StartTime:        ts(runStart),
				TotalElapsedTime: 1192,
				TotalDistance:    5010,
				Laps: []*pbactivity.Lap{
					{StartTime: ts(runStart), TotalElapsedTime: 600, TotalDistance: 2500, Records: runRecords(runStart, 600)},
					{StartTime: ts(runStart.Add(600 * time.Second)), TotalElapsedTime: 592, TotalDistance: 2510, Records: runRecords(runStart.Add(600*time.Second), 592)},
				},
			}},
		},
		Enrichments: &pbactivity.ActivityEnrichments{
			HeartRate: &pbactivity.HeartRateSummary{MinBpm: 120, AvgBpm: 171, MaxBpm: 188},
			HeartRateZones: &pbactivity.HeartRateZonesSummary{
				TotalMinutes: 20,
				Zones: []*pbactivity.HeartRateZoneBucket{
					{ZoneIndex: 0, Name: "Zone 0 (Rest)"}, {ZoneIndex: 1, Name: "Zone 1"}, {ZoneIndex: 2, Name: "Zone 2", Minutes: 2, Percentage: 10},
					{ZoneIndex: 3, Name: "Zone 3", Minutes: 5, Percentage: 25}, {ZoneIndex: 4, Name: "Zone 4", Minutes: 9, Percentage: 45}, {ZoneIndex: 5, Name: "Zone 5", Minutes: 4, Percentage: 20},
				},
			},
		},
	}
}

func liftShowcase() *pbactivity.ShowcasedActivity {
	set := func(name string, reps int32, kg float64, at time.Time) *pbactivity.StrengthSet {
		return &pbactivity.StrengthSet{
			ExerciseName: name, Reps: reps, WeightKg: kg, StartTime: ts(at), SetType: "normal",
			PrimaryMuscleGroup:    pbactivity.MuscleGroup_MUSCLE_GROUP_CHEST,
			SecondaryMuscleGroups: []pbactivity.MuscleGroup{pbactivity.MuscleGroup_MUSCLE_GROUP_TRICEPS},
		}
	}
	// A strength session has an HR trace but no GPS, speed or distance.
	recs := make([]*pbactivity.Record, 0, 300)
	for i := 0; i < 300; i++ {
		recs = append(recs, &pbactivity.Record{Timestamp: ts(liftStart.Add(time.Duration(i) * 10 * time.Second)), HeartRate: int32(100 + i%40)})
	}
	return &pbactivity.ShowcasedActivity{
		ShowcaseId:         "push-day-2026-10-13",
		Title:              "Push Day",
		ActivityType:       pbactivity.ActivityType_ACTIVITY_TYPE_WEIGHT_TRAINING,
		Source:             pbactivity.ActivitySource_SOURCE_HEVY,
		StartTime:          ts(liftStart),
		AppliedEnrichments: []string{"MUSCLE_HEATMAP", "HEART_RATE_SUMMARY"},
		CreatedAt:          ts(liftStart.Add(time.Hour)),
		OwnerDisplayName:   "James",
		OwnerProfileSlug:   "james",
		ActivityData: &pbactivity.StandardizedActivity{
			Sessions: []*pbactivity.Session{{
				StartTime:        ts(liftStart),
				TotalElapsedTime: 3420,
				Laps:             []*pbactivity.Lap{{StartTime: ts(liftStart), TotalElapsedTime: 3420, Records: recs}},
				StrengthSets: []*pbactivity.StrengthSet{
					set("Bench Press (Barbell)", 8, 80, liftStart.Add(5*time.Minute)),
					set("Bench Press (Barbell)", 8, 80, liftStart.Add(8*time.Minute)),
					set("Overhead Press (Barbell)", 8, 40, liftStart.Add(15*time.Minute)),
					set("Overhead Press (Barbell)", 8, 40, liftStart.Add(18*time.Minute)),
				},
			}},
		},
		Enrichments: &pbactivity.ActivityEnrichments{
			MuscleHeatmap: &pbactivity.MuscleHeatmapSummary{Primary: []string{"chest", "triceps"}, Secondary: []string{"shoulders"}},
			HeartRate:     &pbactivity.HeartRateSummary{MinBpm: 100, AvgBpm: 120, MaxBpm: 142},
		},
	}
}

func weekRoundup() *pbactivity.ShowcaseRoundup {
	return &pbactivity.ShowcaseRoundup{
		RoundupId:            "james-week-42-2026",
		Slug:                 "james",
		UserId:               "uid-james",
		PeriodType:           pbactivity.RoundupPeriodType_ROUNDUP_PERIOD_TYPE_WEEK,
		PeriodKey:            "week-42-2026",
		PeriodStart:          ts(time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)),
		PeriodEnd:            ts(time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)),
		GeneratedAt:          ts(time.Date(2026, 10, 19, 0, 5, 12, 0, time.UTC)),
		TotalActivities:      2,
		TotalDurationSeconds: 4612,
		TotalDistanceMeters:  5010,
		ActivityTypeBreakdowns: []*pbactivity.RoundupActivityTypeBreakdown{
			{ActivityType: pbactivity.ActivityType_ACTIVITY_TYPE_RUN, ActivityCount: 1, TotalDistanceMeters: 5010, TotalDurationSeconds: 1192},
			{ActivityType: pbactivity.ActivityType_ACTIVITY_TYPE_WEIGHT_TRAINING, ActivityCount: 1, TotalDurationSeconds: 3420, TotalWeightKg: 2560, TotalSets: 4, TotalReps: 32},
		},
		HrZoneMinutes: []int32{0, 0, 2, 5, 9, 4},
		PrsAchieved:   []*pbactivity.ShowcaseTopPR{{RecordType: "5k", Value: 1192, Unit: "seconds"}},
		Sources:       []pbactivity.ActivitySource{pbactivity.ActivitySource_SOURCE_STRAVA, pbactivity.ActivitySource_SOURCE_HEVY},
		AiSummary:     "A solid week.",
		DayEntries: []*pbactivity.RoundupDayEntry{
			{Date: "2026-10-13", EffortLevel: 1, ActivityCount: 1},
			{Date: "2026-10-17", EffortLevel: 4, ActivityCount: 1},
		},
		OwnerDisplayName: "James",
		OwnerProfileSlug: "james",
	}
}

// fakeSource is an in-memory Source.
type fakeSource struct {
	profile   *pbactivity.ShowcaseProfile
	entries   []*pbactivity.ShowcaseProfileEntry
	showcases map[string]*pbactivity.ShowcasedActivity
	roundups  []*pbactivity.ShowcaseRoundup
	errs      map[string]error
}

func newFakeSource() *fakeSource {
	return &fakeSource{
		profile: testProfile(),
		entries: []*pbactivity.ShowcaseProfileEntry{runEntry(), liftEntry()},
		showcases: map[string]*pbactivity.ShowcasedActivity{
			"colwick-parkrun-2026-10-17": runShowcase(),
			"push-day-2026-10-13":        liftShowcase(),
		},
		roundups: []*pbactivity.ShowcaseRoundup{weekRoundup()},
		errs:     map[string]error{},
	}
}

func (f *fakeSource) Profile(_ context.Context, slug string) (*pbactivity.ShowcaseProfile, error) {
	if err := f.errs["profile"]; err != nil {
		return nil, err
	}
	if slug != f.profile.Slug {
		return nil, fmt.Errorf("profile %q not found", slug)
	}
	return proto.Clone(f.profile).(*pbactivity.ShowcaseProfile), nil
}

func (f *fakeSource) Entries(_ context.Context, userID string) ([]*pbactivity.ShowcaseProfileEntry, error) {
	if err := f.errs["entries"]; err != nil {
		return nil, err
	}
	if userID != f.profile.UserId {
		return nil, fmt.Errorf("unexpected user %q", userID)
	}
	return f.entries, nil
}

func (f *fakeSource) Showcase(_ context.Context, id string) (*pbactivity.ShowcasedActivity, error) {
	if err := f.errs["showcase:"+id]; err != nil {
		return nil, err
	}
	sc, ok := f.showcases[id]
	if !ok {
		return nil, nil
	}
	return proto.Clone(sc).(*pbactivity.ShowcasedActivity), nil
}

func (f *fakeSource) Roundups(_ context.Context, slug string) ([]*pbactivity.ShowcaseRoundup, error) {
	if err := f.errs["roundups"]; err != nil {
		return nil, err
	}
	return f.roundups, nil
}

func (f *fakeSource) Roundup(_ context.Context, slug, key string) (*pbactivity.ShowcaseRoundup, error) {
	for _, r := range f.roundups {
		if r.PeriodKey == key {
			return r, nil
		}
	}
	return nil, nil
}

// memWriter records writes in memory. It is safe for concurrent use: the
// hook (hook_test.go) writes from several goroutines.
type memWriter struct {
	mu    sync.Mutex
	files map[string][]byte
	order []string
	// failing, when set, makes Write fail for names with that prefix.
	failing string
}

func newMemWriter() *memWriter { return &memWriter{files: map[string][]byte{}} }

func (w *memWriter) Write(_ context.Context, name string, data []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failing != "" && strings.HasPrefix(name, w.failing) {
		return fmt.Errorf("write %s: injected failure", name)
	}
	w.files[name] = append([]byte(nil), data...)
	w.order = append(w.order, name)
	return nil
}

func (w *memWriter) Delete(_ context.Context, name string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.files, name)
	w.order = append(w.order, "-"+name)
	return nil
}

// writes counts how many times a name was written.
func (w *memWriter) writes(name string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := 0
	for _, o := range w.order {
		if o == name {
			n++
		}
	}
	return n
}

func (w *memWriter) List(_ context.Context, prefix string) ([]string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	var names []string
	for n := range w.files {
		if len(n) >= len(prefix) && n[:len(prefix)] == prefix {
			names = append(names, n)
		}
	}
	return names, nil
}

func decode(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, b)
	}
	return m
}

func keysOf(m map[string]any) map[string]bool {
	out := map[string]bool{}
	for k := range m {
		out[k] = true
	}
	return out
}

func requireKeys(t *testing.T, what string, m map[string]any, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			t.Errorf("%s: missing key %q (have %v)", what, k, keysOf(m))
		}
	}
}

func forbidKeys(t *testing.T, what string, m map[string]any, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if _, ok := m[k]; ok {
			t.Errorf("%s: key %q must be stripped", what, k)
		}
	}
}
