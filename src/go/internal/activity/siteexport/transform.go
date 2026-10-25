// Package siteexport turns a showcase profile into the static JSON files the
// personal site builds from, per the data contract in
// jarvis-memory/specs/ripixel-website/fitness-zine.md ("Data contract (S0)").
//
// The contract is authoritative: every path, protojson option, stripped field
// and derived value in this package is a line in that document. If this code
// and the contract disagree, this code is wrong.
//
// The protobuf messages are serialised with protojson (lowerCamel keys, enums
// as names, EmitUnpopulated) exactly as api-public's WriteJSON does today.
// encoding/json is used only on the already-serialised bytes (to drop the
// handful of internal fields the contract removes) and on the derived
// envelope structs, never on a proto message — hence the nolint below.
//
//nolint:proto-json
package siteexport

import (
	"encoding/json"
	"fmt"
	"math"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
)

// SchemaVersion is written into every file. Bump it when a field changes
// meaning or is removed; adding fields does not bump it (contract §1).
const SchemaVersion = 1

const (
	maxSeriesSamples = 120 // contract §3.3
	maxRoutePoints   = 200 // contract §3.3
	minRoutePoints   = 10  // below this, routePoints is [] (the page's map threshold)
	seriesPrecision  = 2   // decimal places kept in series values
	routePrecision   = 5   // decimal places kept in route coordinates (~1 m)
)

// marshalOpts are the options api-public's WriteJSON uses, so the site reads
// byte-for-byte what the React pages consume today (contract §1).
var marshalOpts = protojson.MarshalOptions{UseProtoNames: false, EmitUnpopulated: true}

// Category is the derived activity category (contract §3.2).
type Category string

const (
	CategoryStrength      Category = "strength"
	CategoryCardioDist    Category = "cardio-distance"
	CategoryCardioTime    Category = "cardio-time"
	CategorySport         Category = "sport"
	CategoryUntraditional Category = "untraditional"
)

// Streaks is index.json's derived weekly-streak summary (contract §2).
type Streaks struct {
	CurrentWeeks int `json:"currentWeeks"`
	LongestWeeks int `json:"longestWeeks"`
}

// RoundupSummary is the derived per-roundup entry in index.json (contract §2).
type RoundupSummary struct {
	Key                  string  `json:"key"`
	PeriodType           string  `json:"periodType"`
	PeriodStart          string  `json:"periodStart"`
	PeriodEnd            string  `json:"periodEnd"`
	GeneratedAt          string  `json:"generatedAt"`
	TotalActivities      int32   `json:"totalActivities"`
	TotalDurationSeconds float64 `json:"totalDurationSeconds"`
	TotalDistanceMeters  float64 `json:"totalDistanceMeters"`
	TotalWeightKg        float64 `json:"totalWeightKg"`
	HasStrength          bool    `json:"hasStrength"`
	PRCount              int32   `json:"prCount"`
	HasAISummary         bool    `json:"hasAiSummary"`
}

// Series is the downsampled telemetry of one activity (contract §3.3). Every
// slice has length N; a bucket with no valid sample is null. Keys are present
// only when at least one record carried the metric.
type Series struct {
	N            int        `json:"n"`
	ElapsedS     []*float64 `json:"elapsedS,omitempty"`
	DistanceM    []*float64 `json:"distanceM,omitempty"`
	HeartRate    []*float64 `json:"heartRate,omitempty"`
	SpeedMs      []*float64 `json:"speedMs,omitempty"`
	AltitudeM    []*float64 `json:"altitudeM,omitempty"`
	CadenceRpm   []*float64 `json:"cadenceRpm,omitempty"`
	PowerW       []*float64 `json:"powerW,omitempty"`
	TemperatureC []*float64 `json:"temperatureC,omitempty"`
}

type indexFile struct {
	SchemaVersion int               `json:"schemaVersion"`
	GeneratedAt   string            `json:"generatedAt"`
	SiteSlug      string            `json:"siteSlug"`
	Profile       json.RawMessage   `json:"profile"`
	Streaks       Streaks           `json:"streaks"`
	Activities    []json.RawMessage `json:"activities"`
	Roundups      []RoundupSummary  `json:"roundups"`
}

type activityFile struct {
	SchemaVersion int             `json:"schemaVersion"`
	Slug          string          `json:"slug"`
	ExportedAt    string          `json:"exportedAt"`
	Category      Category        `json:"category"`
	Activity      json.RawMessage `json:"activity"`
	Entry         json.RawMessage `json:"entry"`
	Series        *Series         `json:"series"`
	RoutePoints   [][]float64     `json:"routePoints"`
}

type roundupFile struct {
	SchemaVersion int             `json:"schemaVersion"`
	Key           string          `json:"key"`
	ExportedAt    string          `json:"exportedAt"`
	PreviousKey   string          `json:"previousKey"`
	Roundup       json.RawMessage `json:"roundup"`
}

// BuildIndex renders index.json (contract §2). entries and roundups must be
// newest first, as the store returns them; only the entries and roundups the
// caller has actually written should be passed (the index is authoritative).
func BuildIndex(profile *pbactivity.ShowcaseProfile, entries []*pbactivity.ShowcaseProfileEntry, roundups []*pbactivity.ShowcaseRoundup, now time.Time) ([]byte, error) {
	profileJSON, err := marshalStripped(profile, "entries", "userId")
	if err != nil {
		return nil, fmt.Errorf("profile: %w", err)
	}
	out := indexFile{
		SchemaVersion: SchemaVersion,
		GeneratedAt:   formatTime(now),
		SiteSlug:      profile.Slug,
		Profile:       profileJSON,
		Streaks:       ComputeStreaks(profile),
		Activities:    make([]json.RawMessage, 0, len(entries)),
		Roundups:      make([]RoundupSummary, 0, len(roundups)),
	}
	for _, e := range entries {
		b, err := marshalWith(e, map[string]any{"slug": e.ShowcaseId})
		if err != nil {
			return nil, fmt.Errorf("entry %s: %w", e.ShowcaseId, err)
		}
		out.Activities = append(out.Activities, b)
	}
	for _, r := range roundups {
		out.Roundups = append(out.Roundups, SummarizeRoundup(r))
	}
	return encode(out)
}

// BuildActivity renders activity/<showcaseId>.json (contract §3). showcase must
// be the hydrated ShowcasedActivity GetPublicShowcase returns; entry the
// matching ShowcaseProfileEntry.
func BuildActivity(showcase *pbactivity.ShowcasedActivity, entry *pbactivity.ShowcaseProfileEntry, now time.Time) ([]byte, error) {
	category := ResolveCategory(showcase)
	series, route := DeriveTelemetry(showcase.ActivityData)

	// Strip the records after deriving series/route from them (§3.1). Work on
	// a clone so the caller's message is untouched.
	stripped := proto.Clone(showcase).(*pbactivity.ShowcasedActivity)
	if stripped.ActivityData != nil {
		for _, sess := range stripped.ActivityData.Sessions {
			for _, lap := range sess.Laps {
				lap.Records = nil
			}
		}
	}
	activityJSON, err := marshalStripped(stripped, "activityDataUri", "fitFileUri", "pipelineExecutionId", "activityId")
	if err != nil {
		return nil, fmt.Errorf("activity: %w", err)
	}
	entryJSON, err := marshalOpts.Marshal(entry)
	if err != nil {
		return nil, fmt.Errorf("entry: %w", err)
	}
	return encode(activityFile{
		SchemaVersion: SchemaVersion,
		Slug:          showcase.ShowcaseId,
		ExportedAt:    formatTime(now),
		Category:      category,
		Activity:      activityJSON,
		Entry:         entryJSON,
		Series:        series,
		RoutePoints:   route,
	})
}

// BuildRoundup renders roundup/<periodKey>.json (contract §6).
func BuildRoundup(roundup *pbactivity.ShowcaseRoundup, now time.Time) ([]byte, error) {
	roundupJSON, err := marshalStripped(roundup, "userId")
	if err != nil {
		return nil, fmt.Errorf("roundup: %w", err)
	}
	return encode(roundupFile{
		SchemaVersion: SchemaVersion,
		Key:           roundup.PeriodKey,
		ExportedAt:    formatTime(now),
		PreviousKey:   PreviousPeriodKey(roundup),
		Roundup:       roundupJSON,
	})
}

// SummarizeRoundup derives the index.json roundup tile (contract §2).
func SummarizeRoundup(r *pbactivity.ShowcaseRoundup) RoundupSummary {
	s := RoundupSummary{
		Key:                  r.PeriodKey,
		PeriodType:           r.PeriodType.String(),
		PeriodStart:          timestampString(r.PeriodStart),
		PeriodEnd:            timestampString(r.PeriodEnd),
		GeneratedAt:          timestampString(r.GeneratedAt),
		TotalActivities:      r.TotalActivities,
		TotalDurationSeconds: r.TotalDurationSeconds,
		TotalDistanceMeters:  r.TotalDistanceMeters,
		PRCount:              int32(len(r.PrsAchieved)),
		HasAISummary:         r.AiSummary != "",
	}
	for _, b := range r.ActivityTypeBreakdowns {
		s.TotalWeightKg += b.TotalWeightKg
		if b.TotalSets > 0 {
			s.HasStrength = true
		}
	}
	return s
}

// ComputeStreaks derives the current and longest run of active weeks from
// profile.streakHistory.weeklyActive (most-recent-last); both 0 when absent.
func ComputeStreaks(profile *pbactivity.ShowcaseProfile) Streaks {
	var s Streaks
	if profile == nil || profile.StreakHistory == nil {
		return s
	}
	run := 0
	for _, active := range profile.StreakHistory.WeeklyActive {
		if active {
			run++
			if run > s.LongestWeeks {
				s.LongestWeeks = run
			}
		} else {
			run = 0
		}
	}
	s.CurrentWeeks = run
	return s
}

// PreviousPeriodKey is the key of the period immediately before the roundup's
// (contract §6): week → ISO week of periodStart − 7d; month → previous month;
// year → year − 1. It is always set, whether or not that roundup exists.
func PreviousPeriodKey(r *pbactivity.ShowcaseRoundup) string {
	if r.PeriodStart == nil {
		return ""
	}
	start := r.PeriodStart.AsTime().UTC()
	switch r.PeriodType {
	case pbactivity.RoundupPeriodType_ROUNDUP_PERIOD_TYPE_WEEK:
		return PeriodKey(r.PeriodType, start.AddDate(0, 0, -7))
	case pbactivity.RoundupPeriodType_ROUNDUP_PERIOD_TYPE_MONTH:
		return PeriodKey(r.PeriodType, start.AddDate(0, -1, 0))
	case pbactivity.RoundupPeriodType_ROUNDUP_PERIOD_TYPE_YEAR:
		return PeriodKey(r.PeriodType, start.AddDate(-1, 0, 0))
	default:
		return ""
	}
}

// PeriodKey mirrors periodKey() in roundup_generator.go: week-WW-YYYY (ISO),
// month-MM-YYYY, year-YYYY.
func PeriodKey(t pbactivity.RoundupPeriodType, start time.Time) string {
	switch t {
	case pbactivity.RoundupPeriodType_ROUNDUP_PERIOD_TYPE_WEEK:
		year, week := start.ISOWeek()
		return fmt.Sprintf("week-%02d-%d", week, year)
	case pbactivity.RoundupPeriodType_ROUNDUP_PERIOD_TYPE_MONTH:
		return fmt.Sprintf("month-%02d-%d", int(start.Month()), start.Year())
	case pbactivity.RoundupPeriodType_ROUNDUP_PERIOD_TYPE_YEAR:
		return fmt.Sprintf("year-%d", start.Year())
	default:
		return "unknown"
	}
}

var (
	cardioDistanceTypes = typeSet(
		pbactivity.ActivityType_ACTIVITY_TYPE_RUN, pbactivity.ActivityType_ACTIVITY_TYPE_TRAIL_RUN,
		pbactivity.ActivityType_ACTIVITY_TYPE_VIRTUAL_RUN, pbactivity.ActivityType_ACTIVITY_TYPE_RIDE,
		pbactivity.ActivityType_ACTIVITY_TYPE_GRAVEL_RIDE, pbactivity.ActivityType_ACTIVITY_TYPE_MOUNTAIN_BIKE_RIDE,
		pbactivity.ActivityType_ACTIVITY_TYPE_EMOUNTAIN_BIKE_RIDE, pbactivity.ActivityType_ACTIVITY_TYPE_EBIKE_RIDE,
		pbactivity.ActivityType_ACTIVITY_TYPE_VIRTUAL_RIDE, pbactivity.ActivityType_ACTIVITY_TYPE_HANDCYCLE,
		pbactivity.ActivityType_ACTIVITY_TYPE_VELOMOBILE, pbactivity.ActivityType_ACTIVITY_TYPE_ROLLER_SKI,
		pbactivity.ActivityType_ACTIVITY_TYPE_NORDIC_SKI, pbactivity.ActivityType_ACTIVITY_TYPE_BACKCOUNTRY_SKI,
		pbactivity.ActivityType_ACTIVITY_TYPE_ALPINE_SKI, pbactivity.ActivityType_ACTIVITY_TYPE_SNOWSHOE,
		pbactivity.ActivityType_ACTIVITY_TYPE_ICE_SKATE, pbactivity.ActivityType_ACTIVITY_TYPE_INLINE_SKATE,
		pbactivity.ActivityType_ACTIVITY_TYPE_WALK, pbactivity.ActivityType_ACTIVITY_TYPE_HIKE,
		pbactivity.ActivityType_ACTIVITY_TYPE_WHEELCHAIR, pbactivity.ActivityType_ACTIVITY_TYPE_KAYAKING,
		pbactivity.ActivityType_ACTIVITY_TYPE_CANOEING, pbactivity.ActivityType_ACTIVITY_TYPE_ROWING,
		pbactivity.ActivityType_ACTIVITY_TYPE_VIRTUAL_ROW, pbactivity.ActivityType_ACTIVITY_TYPE_STAND_UP_PADDLING,
		pbactivity.ActivityType_ACTIVITY_TYPE_SWIM, pbactivity.ActivityType_ACTIVITY_TYPE_WINDSURF,
		pbactivity.ActivityType_ACTIVITY_TYPE_KITESURF, pbactivity.ActivityType_ACTIVITY_TYPE_SAIL,
		pbactivity.ActivityType_ACTIVITY_TYPE_SURFING, pbactivity.ActivityType_ACTIVITY_TYPE_SKATEBOARD,
	)
	cardioTimeTypes = typeSet(
		pbactivity.ActivityType_ACTIVITY_TYPE_ELLIPTICAL, pbactivity.ActivityType_ACTIVITY_TYPE_STAIR_STEPPER,
		pbactivity.ActivityType_ACTIVITY_TYPE_HIGH_INTENSITY_INTERVAL_TRAINING, pbactivity.ActivityType_ACTIVITY_TYPE_PILATES,
		pbactivity.ActivityType_ACTIVITY_TYPE_YOGA, pbactivity.ActivityType_ACTIVITY_TYPE_WORKOUT,
		pbactivity.ActivityType_ACTIVITY_TYPE_CROSSFIT,
	)
	sportTypes = typeSet(
		pbactivity.ActivityType_ACTIVITY_TYPE_TENNIS, pbactivity.ActivityType_ACTIVITY_TYPE_BADMINTON,
		pbactivity.ActivityType_ACTIVITY_TYPE_PICKLEBALL, pbactivity.ActivityType_ACTIVITY_TYPE_RACQUETBALL,
		pbactivity.ActivityType_ACTIVITY_TYPE_SQUASH, pbactivity.ActivityType_ACTIVITY_TYPE_TABLE_TENNIS,
		pbactivity.ActivityType_ACTIVITY_TYPE_SOCCER, pbactivity.ActivityType_ACTIVITY_TYPE_GOLF,
		pbactivity.ActivityType_ACTIVITY_TYPE_ROCK_CLIMBING,
	)
)

func typeSet(ts ...pbactivity.ActivityType) map[pbactivity.ActivityType]bool {
	m := make(map[pbactivity.ActivityType]bool, len(ts))
	for _, t := range ts {
		m[t] = true
	}
	return m
}

// ResolveCategory mirrors utils/activityCategory.ts: strength sets override the
// type; otherwise the type's family; anything else is untraditional (§3.2).
func ResolveCategory(sc *pbactivity.ShowcasedActivity) Category {
	if sc.ActivityData != nil {
		for _, sess := range sc.ActivityData.Sessions {
			if len(sess.StrengthSets) > 0 {
				return CategoryStrength
			}
		}
	}
	switch {
	case cardioDistanceTypes[sc.ActivityType]:
		return CategoryCardioDist
	case cardioTimeTypes[sc.ActivityType]:
		return CategoryCardioTime
	case sportTypes[sc.ActivityType]:
		return CategorySport
	default:
		return CategoryUntraditional
	}
}

// DeriveTelemetry downsamples the records of every lap into Series (≤ 120
// bucket-averaged samples, null where a bucket has no valid value) and the GPS
// track into ≤ 200 [lat, lng] pairs (§3.3). routePoints is empty when fewer
// than 10 records carry a position.
func DeriveTelemetry(data *pbactivity.StandardizedActivity) (*Series, [][]float64) {
	var records []*pbactivity.Record
	var base time.Time
	if data != nil {
		for _, sess := range data.Sessions {
			if base.IsZero() && sess.StartTime != nil {
				base = sess.StartTime.AsTime()
			}
			for _, lap := range sess.Laps {
				records = append(records, lap.Records...)
			}
		}
	}
	series := &Series{N: 0}
	if len(records) == 0 {
		return series, [][]float64{}
	}
	if base.IsZero() {
		for _, r := range records {
			if r.Timestamp != nil {
				base = r.Timestamp.AsTime()
				break
			}
		}
	}

	n := len(records)
	if n > maxSeriesSamples {
		n = maxSeriesSamples
	}
	series.N = n
	series.ElapsedS = bucketAverage(records, n, func(r *pbactivity.Record) (float64, bool) {
		if r.Timestamp == nil {
			return 0, false
		}
		return r.Timestamp.AsTime().Sub(base).Seconds(), true
	})
	// distanceM is cumulative, so 0 is a real sample once any record has moved;
	// an activity whose records never carry distance omits the key.
	hasDistance := false
	for _, r := range records {
		if r.Distance > 0 {
			hasDistance = true
			break
		}
	}
	series.DistanceM = bucketAverage(records, n, func(r *pbactivity.Record) (float64, bool) {
		return r.Distance, hasDistance
	})
	series.HeartRate = bucketAverage(records, n, func(r *pbactivity.Record) (float64, bool) {
		return float64(r.HeartRate), r.HeartRate > 0
	})
	series.SpeedMs = bucketAverage(records, n, func(r *pbactivity.Record) (float64, bool) {
		return r.Speed, r.Speed > 0
	})
	series.AltitudeM = bucketAverage(records, n, func(r *pbactivity.Record) (float64, bool) {
		return r.Altitude, r.Altitude != 0
	})
	series.CadenceRpm = bucketAverage(records, n, func(r *pbactivity.Record) (float64, bool) {
		return float64(r.Cadence), r.Cadence > 0
	})
	series.PowerW = bucketAverage(records, n, func(r *pbactivity.Record) (float64, bool) {
		return float64(r.Power), r.Power > 0
	})
	series.TemperatureC = bucketAverage(records, n, func(r *pbactivity.Record) (float64, bool) {
		return float64(r.GetTemperature()), r.Temperature != nil
	})
	return series, routePoints(records)
}

// bucketAverage splits records into n evenly sized buckets (like downsample()
// in showcase_settings.go) and averages the valid values in each. It returns
// nil when no record carried the metric at all, so the key is omitted.
func bucketAverage(records []*pbactivity.Record, n int, value func(*pbactivity.Record) (float64, bool)) []*float64 {
	out := make([]*float64, n)
	any := false
	step := float64(len(records)) / float64(n)
	for i := 0; i < n; i++ {
		start := int(float64(i) * step)
		end := int(float64(i+1) * step)
		if i == n-1 || end > len(records) {
			end = len(records)
		}
		var sum float64
		count := 0
		for _, r := range records[start:end] {
			if v, ok := value(r); ok {
				sum += v
				count++
			}
		}
		if count > 0 {
			v := round(sum/float64(count), seriesPrecision)
			out[i] = &v
			any = true
		}
	}
	if !any {
		return nil
	}
	return out
}

func routePoints(records []*pbactivity.Record) [][]float64 {
	var pts [][]float64
	for _, r := range records {
		if r.PositionLat != 0 && r.PositionLong != 0 {
			pts = append(pts, []float64{round(r.PositionLat, routePrecision), round(r.PositionLong, routePrecision)})
		}
	}
	if len(pts) < minRoutePoints {
		return [][]float64{}
	}
	if len(pts) <= maxRoutePoints {
		return pts
	}
	// Evenly spaced picks; index 0 and len-1 are always chosen.
	out := make([][]float64, 0, maxRoutePoints)
	last := len(pts) - 1
	for i := 0; i < maxRoutePoints; i++ {
		idx := int(math.Round(float64(i) * float64(last) / float64(maxRoutePoints-1)))
		out = append(out, pts[idx])
	}
	return out
}

func round(v float64, places int) float64 {
	p := math.Pow10(places)
	return math.Round(v*p) / p
}

// marshalStripped serialises a proto with the contract's protojson options and
// removes the named top-level keys. Only the top level is decoded (into raw
// messages), so nested content is byte-for-byte protojson output.
func marshalStripped(msg proto.Message, drop ...string) (json.RawMessage, error) {
	return marshalEdit(msg, drop, nil)
}

// marshalWith serialises a proto and adds the given top-level keys.
func marshalWith(msg proto.Message, add map[string]any) (json.RawMessage, error) {
	return marshalEdit(msg, nil, add)
}

func marshalEdit(msg proto.Message, drop []string, add map[string]any) (json.RawMessage, error) {
	b, err := marshalOpts.Marshal(msg)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		return nil, err
	}
	for _, k := range drop {
		delete(fields, k)
	}
	for k, v := range add {
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		fields[k] = raw
	}
	return json.Marshal(fields)
}

// encode renders the envelope compactly. protojson deliberately injects random
// whitespace into its output; compacting makes successive exports of unchanged
// data byte-identical, which keeps the files diffable.
func encode(v any) ([]byte, error) {
	return json.Marshal(v)
}

// formatTime renders a derived timestamp exactly as protojson renders proto
// timestamps (RFC 3339 UTC, "Z"), so derived and proto fields look alike.
func formatTime(t time.Time) string {
	return timestampString(timestamppb.New(t.UTC()))
}

func timestampString(ts *timestamppb.Timestamp) string {
	if ts == nil {
		return ""
	}
	b, err := protojson.Marshal(ts)
	if err != nil {
		return ""
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return ""
	}
	return s
}
