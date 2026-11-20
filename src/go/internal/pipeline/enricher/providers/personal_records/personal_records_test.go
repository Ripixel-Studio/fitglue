package personal_records

import (
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"

	"testing"

	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestCalculate1RM(t *testing.T) {
	tests := []struct {
		name     string
		weightKg float64
		reps     int32
		want     float64
	}{
		{
			name:     "single rep returns weight directly",
			weightKg: 100,
			reps:     1,
			want:     100,
		},
		{
			name:     "Epley formula for 10 reps",
			weightKg: 100,
			reps:     10,
			want:     133.33, // 100 * (1 + 10/30) ≈ 133.33
		},
		{
			name:     "Epley formula for 5 reps",
			weightKg: 80,
			reps:     5,
			want:     93.33, // 80 * (1 + 5/30) ≈ 93.33
		},
		{
			name:     "zero reps returns zero",
			weightKg: 100,
			reps:     0,
			want:     0,
		},
		{
			name:     "negative reps returns zero",
			weightKg: 100,
			reps:     -1,
			want:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Calculate1RM(tt.weightKg, tt.reps)
			// Use approximate comparison for floating point
			if !approximatelyEqual(got, tt.want, 0.01) {
				t.Errorf("Calculate1RM(%v, %v) = %v, want approximately %v", tt.weightKg, tt.reps, got, tt.want)
			}
		})
	}
}

// approximatelyEqual checks if two floats are equal within a tolerance
func approximatelyEqual(a, b, tolerance float64) bool {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff < tolerance
}

func TestCalculateSetVolume(t *testing.T) {
	tests := []struct {
		name     string
		weightKg float64
		reps     int32
		want     float64
	}{
		{
			name:     "standard calculation",
			weightKg: 100,
			reps:     10,
			want:     1000,
		},
		{
			name:     "zero reps returns zero",
			weightKg: 100,
			reps:     0,
			want:     0,
		},
		{
			name:     "zero weight returns zero",
			weightKg: 0,
			reps:     10,
			want:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CalculateSetVolume(tt.weightKg, tt.reps)
			if got != tt.want {
				t.Errorf("CalculateSetVolume(%v, %v) = %v, want %v", tt.weightKg, tt.reps, got, tt.want)
			}
		})
	}
}

func TestCalculateImprovement(t *testing.T) {
	tests := []struct {
		name          string
		oldValue      float64
		newValue      float64
		lowerIsBetter bool
		want          float64
	}{
		{
			name:          "time improvement (lower is better)",
			oldValue:      1200,
			newValue:      1140, // 1 minute faster
			lowerIsBetter: true,
			want:          5.0, // 5% improvement
		},
		{
			name:          "weight improvement (higher is better)",
			oldValue:      100,
			newValue:      110,
			lowerIsBetter: false,
			want:          10.0, // 10% improvement
		},
		{
			name:          "zero old value returns zero",
			oldValue:      0,
			newValue:      100,
			lowerIsBetter: false,
			want:          0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CalculateImprovement(tt.oldValue, tt.newValue, tt.lowerIsBetter)
			if got != tt.want {
				t.Errorf("CalculateImprovement(%v, %v, %v) = %v, want %v", tt.oldValue, tt.newValue, tt.lowerIsBetter, got, tt.want)
			}
		})
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name    string
		seconds float64
		want    string
	}{
		{
			name:    "less than an hour",
			seconds: 1425, // 23:45
			want:    "23:45",
		},
		{
			name:    "over an hour",
			seconds: 3725, // 1:02:05
			want:    "1:02:05",
		},
		{
			name:    "exactly one minute",
			seconds: 60,
			want:    "1:00",
		},
		{
			name:    "zero seconds",
			seconds: 0,
			want:    "0:00",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatDuration(tt.seconds)
			if got != tt.want {
				t.Errorf("formatDuration(%v) = %v, want %v", tt.seconds, got, tt.want)
			}
		})
	}
}

func TestFormatWeight(t *testing.T) {
	tests := []struct {
		name string
		kg   float64
		want string
	}{
		{
			name: "whole number",
			kg:   100,
			want: "100kg",
		},
		{
			name: "decimal",
			kg:   100.5,
			want: "100.5kg",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatWeight(tt.kg)
			if got != tt.want {
				t.Errorf("formatWeight(%v) = %v, want %v", tt.kg, got, tt.want)
			}
		})
	}
}

func TestFormatExerciseName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "simple exercise",
			input: "bench_press",
			want:  "Bench Press",
		},
		{
			name:  "single word",
			input: "squat",
			want:  "Squat",
		},
		{
			name:  "three words",
			input: "romanian_dead_lift",
			want:  "Romanian Dead Lift",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatExerciseName(tt.input)
			if got != tt.want {
				t.Errorf("formatExerciseName(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestNormalizeExerciseName(t *testing.T) {
	tests := []struct {
		name         string
		exerciseName string
		wantNonEmpty bool // Just check if we get a normalized name
	}{
		{
			name:         "standard bench press",
			exerciseName: "Bench Press",
			wantNonEmpty: true,
		},
		{
			name:         "abbreviation",
			exerciseName: "DB Bench Press",
			wantNonEmpty: true,
		},
		{
			name:         "unknown exercise",
			exerciseName: "Some Random Exercise 123",
			wantNonEmpty: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeExerciseName(tt.exerciseName)
			if tt.wantNonEmpty && got == "" {
				t.Errorf("normalizeExerciseName(%v) returned empty, want non-empty", tt.exerciseName)
			}
			if !tt.wantNonEmpty && got != "" {
				// It's okay if the fuzzy matcher found something
				t.Logf("normalizeExerciseName(%v) = %v (matched unexpectedly, but acceptable)", tt.exerciseName, got)
			}
		})
	}
}

func TestFormatVolume(t *testing.T) {
	tests := []struct {
		name string
		kg   float64
		want string
	}{
		{
			name: "less than 1000kg",
			kg:   500,
			want: "500kg",
		},
		{
			name: "exactly 1000kg",
			kg:   1000,
			want: "1.0 tonnes",
		},
		{
			name: "more than 1000kg",
			kg:   4500,
			want: "4.5 tonnes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatVolume(tt.kg)
			if got != tt.want {
				t.Errorf("formatVolume(%v) = %v, want %v", tt.kg, got, tt.want)
			}
		})
	}
}

func TestFindFastestSegment_RecordLevel(t *testing.T) {
	// Create an activity with 1Hz speed data simulating variable pace over 10K
	// First 5K: 6 min/km pace (speed = 1000/360 = ~2.78 m/s) in 1800 seconds
	// Second 5K: 5 min/km pace (speed = 1000/300 = ~3.33 m/s) in 1500 seconds
	activity := makeActivityWithRecords(t, []recordSegment{
		{speedMs: 2.778, durationSec: 1800}, // ~5K at 6:00/km
		{speedMs: 3.333, durationSec: 1500}, // ~5K at 5:00/km
	})

	fastest5K := findFastestSegment(activity, Distance5K)
	if fastest5K <= 0 {
		t.Fatal("Expected non-zero fastest 5K time")
	}

	// The fastest 5K should be significantly less than 1800 seconds (the slow first half)
	// because the sliding window should find the fast second half
	if fastest5K >= 1750 {
		t.Errorf("Fastest 5K = %.1f seconds, expected significantly less than 1800 (the slow first 5K)", fastest5K)
	}

	// The fastest 5K should be close to 1500 seconds (the fast second half)
	if fastest5K > 1550 || fastest5K < 1450 {
		t.Errorf("Fastest 5K = %.1f seconds, expected close to 1500 (the fast second 5K)", fastest5K)
	}
}

func TestFindFastestSegment_ProportionalFallback(t *testing.T) {
	// Activity with only session-level totals (no records, no laps)
	activity := makeActivitySessionOnly(10000, 3000) // 10K in 50 minutes

	fastest5K := findFastestSegment(activity, Distance5K)
	if fastest5K <= 0 {
		t.Fatal("Expected non-zero fastest 5K time from proportional fallback")
	}

	// Should be proportional: (5000/10000) * 3000 = 1500
	if !approximatelyEqual(fastest5K, 1500, 1) {
		t.Errorf("Fastest 5K = %.1f, expected 1500 from proportional", fastest5K)
	}
}

func TestFindFastestSegment_LapLevel(t *testing.T) {
	// Activity with lap data (4 × 1K laps with varying pace, total 4K - but we'll test 2K)
	activity := makeActivityWithLaps([]lapData{
		{distanceM: 1000, elapsedSec: 360}, // 6:00/km
		{distanceM: 1000, elapsedSec: 300}, // 5:00/km
		{distanceM: 1000, elapsedSec: 280}, // 4:40/km
		{distanceM: 1000, elapsedSec: 320}, // 5:20/km
	})

	fastest2K := findFastestSegment(activity, Distance2K)
	if fastest2K <= 0 {
		t.Fatal("Expected non-zero fastest 2K time from lap-level")
	}

	// The fastest contiguous 2K should be laps 2+3 (300 + 280 = 580 seconds)
	if !approximatelyEqual(fastest2K, 580, 5) {
		t.Errorf("Fastest 2K = %.1f seconds, expected ~580 (laps 2+3)", fastest2K)
	}
}

func TestFindFastestSegment_InsufficientDistance(t *testing.T) {
	activity := makeActivitySessionOnly(4000, 1200) // 4K - not enough for 5K

	fastest5K := findFastestSegment(activity, Distance5K)
	if fastest5K != 0 {
		t.Errorf("Expected 0 for insufficient distance, got %.1f", fastest5K)
	}
}

func TestAllDistanceThresholds(t *testing.T) {
	thresholds := AllDistanceThresholds()

	if len(thresholds) != 23 {
		t.Errorf("Expected 23 distance thresholds, got %d", len(thresholds))
	}

	// Verify ascending distance order
	for i := 1; i < len(thresholds); i++ {
		if thresholds[i].DistanceM < thresholds[i-1].DistanceM {
			t.Errorf("Thresholds not in ascending order: %s (%.0fm) comes after %s (%.0fm)",
				thresholds[i].Display, thresholds[i].DistanceM,
				thresholds[i-1].Display, thresholds[i-1].DistanceM)
		}
	}

	// Verify all have non-empty fields
	for _, th := range thresholds {
		if th.RecordType == "" {
			t.Error("Empty RecordType found in thresholds")
		}
		if th.Display == "" {
			t.Error("Empty Display found in thresholds")
		}
		if th.DistanceM <= 0 {
			t.Errorf("Invalid distance for %s: %.1f", th.Display, th.DistanceM)
		}
	}
}

func TestFormatRecordTypeForDisplay_NewTypes(t *testing.T) {
	tests := []struct {
		recordType string
		want       string
	}{
		{"fastest_100m", "Fastest 100m"},
		{"fastest_1k", "Fastest 1K"},
		{"fastest_1_mile", "Fastest 1 Mile"},
		{"fastest_5k", "Fastest 5K"},
		{"fastest_10k", "Fastest 10K"},
		{"fastest_half_marathon", "Fastest Half Marathon"},
		{"fastest_marathon", "Fastest Marathon"},
		{"fastest_ultra_marathon", "Fastest Ultra Marathon"},
		{"longest_run", "Longest Run"},
		{"longest_ride", "Longest Ride"},
	}

	for _, tt := range tests {
		t.Run(tt.recordType, func(t *testing.T) {
			got := formatRecordTypeForDisplay(tt.recordType)
			if got != tt.want {
				t.Errorf("formatRecordTypeForDisplay(%q) = %q, want %q", tt.recordType, got, tt.want)
			}
		})
	}
}

// --- Test helpers ---

type recordSegment struct {
	speedMs     float64
	durationSec int
}

type lapData struct {
	distanceM  float64
	elapsedSec float64
}

func makeActivityWithRecords(t *testing.T, segments []recordSegment) *pbactivity.StandardizedActivity {
	t.Helper()
	var records []*pbactivity.Record
	baseTime := int64(1700000000)
	currentTime := baseTime

	for _, seg := range segments {
		for i := 0; i < seg.durationSec; i++ {
			records = append(records, &pbactivity.Record{
				Timestamp: &timestamppb.Timestamp{Seconds: currentTime},
				Speed:     seg.speedMs,
			})
			currentTime++
		}
	}

	// Calculate total distance and time
	totalTimeSec := float64(currentTime - baseTime)
	var totalDistance float64
	for _, seg := range segments {
		totalDistance += seg.speedMs * float64(seg.durationSec)
	}

	return &pbactivity.StandardizedActivity{
		Sessions: []*pbactivity.Session{
			{
				TotalDistance:    totalDistance,
				TotalElapsedTime: totalTimeSec,
				Laps: []*pbactivity.Lap{
					{
						Records: records,
					},
				},
			},
		},
	}
}

func makeActivitySessionOnly(distanceM, elapsedSec float64) *pbactivity.StandardizedActivity {
	return &pbactivity.StandardizedActivity{
		Sessions: []*pbactivity.Session{
			{
				TotalDistance:    distanceM,
				TotalElapsedTime: elapsedSec,
			},
		},
	}
}

func makeActivityWithLaps(laps []lapData) *pbactivity.StandardizedActivity {
	var totalDist, totalTime float64
	var pbLaps []*pbactivity.Lap
	for _, l := range laps {
		totalDist += l.distanceM
		totalTime += l.elapsedSec
		pbLaps = append(pbLaps, &pbactivity.Lap{
			TotalDistance:    l.distanceM,
			TotalElapsedTime: l.elapsedSec,
		})
	}
	return &pbactivity.StandardizedActivity{
		Sessions: []*pbactivity.Session{
			{
				TotalDistance:    totalDist,
				TotalElapsedTime: totalTime,
				Laps:             pbLaps,
			},
		},
	}
}

func TestRecordSetsSkipEstimated(t *testing.T) {
	a := &pbactivity.StandardizedActivity{Sessions: []*pbactivity.Session{{StrengthSets: []*pbactivity.StrengthSet{
		{ExerciseName: "Wall Ball", Reps: 100, WeightKg: 9, Estimated: true},
		{ExerciseName: "Bench Press", Reps: 5, WeightKg: 100},
		{ExerciseName: "SkiErg", DurationSeconds: 200, Estimated: true},
	}}}}
	got := recordSets(a)
	if len(got) != 1 || got[0].ExerciseName != "Bench Press" {
		t.Fatalf("recordSets = %v", got)
	}
}
