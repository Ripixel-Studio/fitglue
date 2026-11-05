package activity

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	pbevents "github.com/fitglue/server/src/go/pkg/types/pb/models/events"
)

// ActivityFix corrects a showcased activity's type, distance and calories
// (fitgluectl fix-activity). His own words — title, description, notes — are
// never touched.
type ActivityFix struct {
	ShowcaseID string
	// Type is the corrected type; UNSPECIFIED keeps the stored one.
	Type pbactivity.ActivityType
	// DistanceM is the corrected session distance in metres. Nil means: when the
	// (corrected) type has a distance and none is stored, use the distance the
	// source recorded on its sets (a Hevy "Running" set carries the watch's
	// figure), so the real number beats a nominal one.
	DistanceM *float64
	// CaloriesKcal, when > 0, becomes the session's platform calories (what
	// the export and the profile entry prefer over the MET estimate).
	CaloriesKcal int32
}

// FieldChange is one line of a fix plan: where, what, before → after.
type FieldChange struct {
	Where, Field, Before, After string
}

func (c FieldChange) String() string {
	return fmt.Sprintf("%-8s %-22s %s -> %s", c.Where, c.Field, c.Before, c.After)
}

// ActivityFixPlan is everything ApplyActivityFix writes, computed without writing.
type ActivityFixPlan struct {
	UserID     string
	ShowcaseID string
	Title      string
	// Doc is the Firestore showcase document as stored (not hydrated), patched.
	Doc *pbactivity.ShowcasedActivity
	// Blob is the offloaded showcase data, patched; nil when there is none.
	Blob    *pbevents.EnrichedActivityEvent
	BlobURI string
	// Entry is the profile entry, patched; nil when it is not on the profile.
	Entry *pbactivity.ShowcaseProfileEntry
	// Roundups are the stored roundups whose period holds the activity: they
	// are recomputed (numbers and story) after the fix.
	Roundups []string
	Changes  []FieldChange
	Notes    []string
}

// ErrNothingToFix means the activity already matches the fix.
var ErrNothingToFix = errors.New("nothing to change")

// PlanActivityFix reads the showcase, its data blob and its profile entry and
// returns the patched copies plus what changes. Nothing is written.
func (s *Service) PlanActivityFix(ctx context.Context, fix ActivityFix) (*ActivityFixPlan, error) {
	doc, owner, err := s.store.GetPublicShowcase(ctx, fix.ShowcaseID)
	if err != nil {
		return nil, fmt.Errorf("showcase %s: %w", fix.ShowcaseID, err)
	}
	if doc == nil {
		return nil, fmt.Errorf("showcase %s not found", fix.ShowcaseID)
	}
	plan := &ActivityFixPlan{UserID: owner, ShowcaseID: fix.ShowcaseID, Title: doc.Title, Doc: doc}

	// The activity data lives inline on older documents, otherwise in the blob.
	data := doc.ActivityData
	if doc.ActivityDataUri != "" {
		raw, err := s.blobStore.Get(ctx, "", doc.ActivityDataUri)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", doc.ActivityDataUri, err)
		}
		var ev pbevents.EnrichedActivityEvent
		if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, &ev); err != nil {
			return nil, fmt.Errorf("decode %s: %w", doc.ActivityDataUri, err)
		}
		plan.Blob, plan.BlobURI = &ev, doc.ActivityDataUri
		if data == nil {
			data = ev.ActivityData
		}
	}
	if data == nil {
		return nil, fmt.Errorf("showcase %s has no activity data", fix.ShowcaseID)
	}

	newType := fix.Type
	if newType == pbactivity.ActivityType_ACTIVITY_TYPE_UNSPECIFIED {
		newType = doc.ActivityType
	}
	dist := -1.0 // keep
	if fix.DistanceM != nil {
		dist = *fix.DistanceM
	} else if activityTypeHasDistance(newType) && sessionDistance(data) == 0 {
		if d := RecordedSetDistance(data); d > 0 {
			dist = d
			plan.Notes = append(plan.Notes, fmt.Sprintf("distance: none given, using the %.0f m the source recorded on its set", d))
		} else {
			plan.Notes = append(plan.Notes, "distance: none given and none recorded; pass --distance-m")
		}
	}

	// One patch for every copy of the activity data, so they cannot disagree.
	var changes []FieldChange
	patched := map[*pbactivity.StandardizedActivity]bool{}
	for _, d := range []*pbactivity.StandardizedActivity{doc.ActivityData, plan.Blob.GetActivityData()} {
		if d == nil || patched[d] {
			continue
		}
		patched[d] = true
		c, err := patchActivityData(d, newType, dist, fix.CaloriesKcal)
		if err != nil {
			return nil, err
		}
		if changes == nil {
			changes = c // the copies are the same activity: list its changes once
		}
	}
	if doc.ActivityType != newType {
		changes = append([]FieldChange{{"showcase", "activityType", doc.ActivityType.String(), newType.String()}}, changes...)
		doc.ActivityType = newType
	}

	entries, err := s.store.ListShowcaseProfileEntries(ctx, owner)
	if err != nil {
		return nil, fmt.Errorf("entries: %w", err)
	}
	for _, e := range entries {
		if e.ShowcaseId == fix.ShowcaseID {
			plan.Entry = e
			break
		}
	}
	if plan.Entry != nil {
		changes = append(changes, patchEntry(plan.Entry, newType, data, fix.CaloriesKcal)...)
	} else {
		plan.Notes = append(plan.Notes, "not on the public profile: no entry, totals or roundups to update")
	}
	if len(changes) == 0 {
		return plan, ErrNothingToFix
	}
	plan.Changes = changes

	if plan.Entry != nil && doc.StartTime != nil {
		if prof, err := s.store.GetShowcasePreferences(ctx, owner); err == nil && prof != nil && prof.Slug != "" {
			for _, k := range periodKeysAt(doc.StartTime.AsTime()) {
				if r, err := s.store.GetRoundup(ctx, prof.Slug, k); err == nil && r != nil {
					plan.Roundups = append(plan.Roundups, k)
				}
			}
		}
	}
	if !hasTrack(data) && activityTypeHasDistance(newType) {
		plan.Notes = append(plan.Notes, "no GPS track stored (records are bare timestamps): pace comes from distance/time; best efforts and km splits cannot be derived")
	}
	return plan, nil
}

// ApplyActivityFix writes a plan: the data blob, the showcase document, the
// profile entry, then the derived state — lifetime totals, every roundup that
// holds the activity (recomputed, story included) — and tells the site
// exporter, which re-exports the files, refreshes index.json and signals the
// site rebuild.
func (s *Service) ApplyActivityFix(ctx context.Context, plan *ActivityFixPlan) error {
	if plan.Blob != nil {
		raw, err := protojson.Marshal(plan.Blob)
		if err != nil {
			return fmt.Errorf("encode blob: %w", err)
		}
		if err := s.blobStore.Write(ctx, "", plan.BlobURI, raw); err != nil {
			return fmt.Errorf("write %s: %w", plan.BlobURI, err)
		}
	}
	if _, err := s.store.UpdateShowcase(ctx, plan.UserID, plan.Doc); err != nil {
		return fmt.Errorf("update showcase: %w", err)
	}
	if plan.Entry != nil {
		if err := s.store.SetShowcaseProfileEntry(ctx, plan.UserID, plan.Entry); err != nil {
			return fmt.Errorf("update entry: %w", err)
		}
		s.recomputeAndSaveProfileStats(ctx, plan.UserID)
	}
	s.siteActivityChanged(ctx, plan.ShowcaseID)
	for _, k := range plan.Roundups {
		pt, start, end, err := parsePeriodKey(k)
		if err != nil {
			return err
		}
		if _, err := s.generateRoundup(ctx, plan.UserID, pt, start, end); err != nil {
			return fmt.Errorf("recompute roundup %s: %w", k, err)
		}
	}
	return nil
}

// patchActivityData applies type, distance (dist < 0 keeps it) and calories
// (0 keeps them) to one copy of the activity data. A non-strength type drops
// strength sets that carry no lifting (no reps, no weight: a run logged as a
// Hevy "Running" set) — they would keep the site's strength layout — and
// refuses when real lifting would be lost.
func patchActivityData(d *pbactivity.StandardizedActivity, t pbactivity.ActivityType, dist float64, kcal int32) ([]FieldChange, error) {
	var out []FieldChange
	if d.Type != t {
		out = append(out, FieldChange{"data", "type", d.Type.String(), t.String()})
		d.Type = t
	}
	if len(d.Sessions) == 0 {
		return out, nil
	}
	sess := d.Sessions[0]
	if dist >= 0 && sess.TotalDistance != dist {
		out = append(out, FieldChange{"data", "session.totalDistance", metres(sess.TotalDistance), metres(dist)})
		sess.TotalDistance = dist
		if len(sess.Laps) == 1 && sess.Laps[0].TotalDistance != dist {
			sess.Laps[0].TotalDistance = dist
		}
	}
	if kcal > 0 && sess.GetTotalCalories() != float64(kcal) {
		out = append(out, FieldChange{"data", "session.totalCalories", fmt.Sprintf("%.0f", sess.GetTotalCalories()), fmt.Sprint(kcal)})
		v := float64(kcal)
		sess.TotalCalories = &v
	}
	if !IsStrengthType(t) {
		for _, s := range d.Sessions {
			if len(s.StrengthSets) == 0 {
				continue
			}
			for _, set := range s.StrengthSets {
				if set.Reps > 0 || set.WeightKg > 0 {
					return nil, fmt.Errorf("refusing: %s has a lifting set (%q %d × %.1f kg)", t, set.ExerciseName, set.Reps, set.WeightKg)
				}
			}
			names := make([]string, 0, len(s.StrengthSets))
			for _, set := range s.StrengthSets {
				names = append(names, set.ExerciseName)
			}
			out = append(out, FieldChange{"data", "strengthSets", fmt.Sprintf("%d (%s)", len(s.StrengthSets), strings.Join(names, ", ")), "0"})
			s.StrengthSets = nil
		}
	}
	return out, nil
}

// patchEntry re-derives the entry fields that depend on type, distance and
// calories, the way AddShowcaseEntry builds them. d is already patched.
func patchEntry(e *pbactivity.ShowcaseProfileEntry, t pbactivity.ActivityType, d *pbactivity.StandardizedActivity, kcal int32) []FieldChange {
	var out []FieldChange
	if e.ActivityType != t {
		out = append(out, FieldChange{"entry", "activityType", e.ActivityType.String(), t.String()})
		e.ActivityType = t
	}
	dist := 0.0
	if activityTypeHasDistance(t) {
		dist = sessionDistance(d)
	}
	if e.DistanceMeters != dist {
		out = append(out, FieldChange{"entry", "distanceMeters", metres(e.DistanceMeters), metres(dist)})
		e.DistanceMeters = dist
	}
	sets := int32(0)
	if len(d.Sessions) > 0 {
		sets = int32(len(d.Sessions[0].StrengthSets))
	}
	if e.TotalSets != sets {
		out = append(out, FieldChange{"entry", "totalSets", fmt.Sprint(e.TotalSets), fmt.Sprint(sets)})
		e.TotalSets = sets
	}
	if kcal > 0 && e.GetCaloriesKcal() != kcal {
		out = append(out, FieldChange{"entry", "caloriesKcal", fmt.Sprint(e.GetCaloriesKcal()), fmt.Sprint(kcal)})
		v := kcal
		e.CaloriesKcal = &v
	}
	return out
}

// RecordedSetDistance sums the distance recorded on the activity's strength
// sets: Hevy logs a run as a "Running" set carrying the watch's distance.
func RecordedSetDistance(d *pbactivity.StandardizedActivity) float64 {
	var m float64
	for _, s := range d.GetSessions() {
		for _, set := range s.StrengthSets {
			m += set.DistanceMeters
		}
	}
	return m
}

// MislabelledRun reports whether an activity looks like a run stored as a
// strength session: a strength type, no distance on the session, and only
// running sets (named run/jog, no reps or weight).
func MislabelledRun(t pbactivity.ActivityType, d *pbactivity.StandardizedActivity) bool {
	if !IsStrengthType(t) || sessionDistance(d) > 0 {
		return false
	}
	n := 0
	for _, s := range d.GetSessions() {
		for _, set := range s.StrengthSets {
			name := strings.ToLower(set.ExerciseName)
			if set.Reps > 0 || set.WeightKg > 0 || !(strings.Contains(name, "run") || strings.Contains(name, "jog")) {
				return false
			}
			n++
		}
	}
	return n > 0
}

// IsStrengthType is the types a lifting session is stored as.
func IsStrengthType(t pbactivity.ActivityType) bool {
	switch t {
	case pbactivity.ActivityType_ACTIVITY_TYPE_WEIGHT_TRAINING,
		pbactivity.ActivityType_ACTIVITY_TYPE_WORKOUT,
		pbactivity.ActivityType_ACTIVITY_TYPE_CROSSFIT,
		pbactivity.ActivityType_ACTIVITY_TYPE_HIGH_INTENSITY_INTERVAL_TRAINING:
		return true
	}
	return false
}

func sessionDistance(d *pbactivity.StandardizedActivity) float64 {
	if len(d.GetSessions()) == 0 {
		return 0
	}
	return d.Sessions[0].TotalDistance
}

// hasTrack reports whether any record carries distance or position: bare
// timestamps (what a Hevy workout's single lap holds) are not a track.
func hasTrack(d *pbactivity.StandardizedActivity) bool {
	for _, s := range d.GetSessions() {
		for _, l := range s.Laps {
			for _, r := range l.Records {
				if r.Distance > 0 || r.PositionLat != 0 || r.PositionLong != 0 {
					return true
				}
			}
		}
	}
	return false
}

// periodKeysAt is the week, month and year keys of the periods holding t.
func periodKeysAt(t time.Time) []string {
	t = t.UTC()
	y, w := t.ISOWeek()
	keys := []string{
		fmt.Sprintf("week-%02d-%d", w, y),
		fmt.Sprintf("month-%02d-%d", int(t.Month()), t.Year()),
		fmt.Sprintf("year-%d", t.Year()),
	}
	sort.Strings(keys)
	return keys
}

func metres(m float64) string {
	return fmt.Sprintf("%.0f m", math.Round(m))
}
