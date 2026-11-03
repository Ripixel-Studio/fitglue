// Package reconcile is the three-way check between Hevy, FitGlue and Strava
// behind `fitgluectl reconcile`.
//
// Every workout in any of the three is put into one Session — a real-world
// training session — and each session gets one cell per system: present,
// missing (it should be there and is not) or n/a (it does not belong there).
// The expected shape depends on where the session started:
//
//	hevy origin    Hevy workout → FitGlue run (source SOURCE_HEVY) → Strava upload
//	strava origin  Strava activity (Garmin, ROXFIT, manual) → FitGlue run
//	               (source SOURCE_STRAVA) → Hevy copy, when the pipeline made one
//
// Strava activities and Hevy workouts that FitGlue created itself are matched
// back to the session they were uploaded for, never treated as new sessions.
// Matching is by id first (the run's source id and its destination external
// ids, then the uploaded_activities loop-prevention records), then by start
// time (within MatchWindow), type and duration for a FitGlue upload whose id
// was never recorded on the run.
//
// Plan is pure: it reads a Snapshot and returns a Report. Apply carries out
// the only two fixes the report may hold — replay a workout FitGlue never
// ingested, and repost a missing Strava upload — through an Applier, and
// remembers what it did in State so a second run changes nothing.
package reconcile

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// MatchWindow is how far apart two start times may be and still be one session.
const MatchWindow = 2 * time.Minute

// Grace is how long after a workout ends reconcile leaves it alone: the
// webhook may simply not have landed yet.
const Grace = 45 * time.Minute

// StaleRun is how long a run may sit RUNNING before a missing upload counts as
// missing rather than in progress.
const StaleRun = 2 * time.Hour

// HevyWorkout is one workout from Hevy's GET /v1/workouts.
type HevyWorkout struct {
	ID    string
	Title string
	Start time.Time
	End   time.Time
}

// StravaActivity is one summary from Strava's GET /athlete/activities.
type StravaActivity struct {
	ID        string
	Name      string
	SportType string
	Start     time.Time
	Elapsed   time.Duration
	// UploadFile is the summary's external_id: the uploaded file's name.
	// FitGlue uploads "activity.fit"; Garmin's look like "garmin_ping_…".
	UploadFile string
}

// Dest is a run's outcome for one destination.
type Dest struct {
	Status     string // DESTINATION_STATUS_*
	ExternalID string
}

// Run is one FitGlue pipeline run — one row of FitGlue's activity list.
type Run struct {
	ID         string // pipeline run id
	ActivityID string // what the repost RPC takes
	PipelineID string
	Source     string // SOURCE_HEVY, SOURCE_STRAVA, …
	SourceID   string // the source's own id
	Title      string
	Type       string // ACTIVITY_TYPE_*
	Status     string // PIPELINE_RUN_STATUS_*
	Start      time.Time
	Created    time.Time
	Updated    time.Time
	Strava     *Dest // nil when the run has no Strava destination
	Hevy       *Dest // nil when the run has no Hevy destination
}

// Snapshot is everything Plan reads. The lists may reach back before Since
// (the gatherer fetches a margin so a session on the boundary still matches);
// only sessions starting at or after Since are reported.
type Snapshot struct {
	Since  time.Time
	Now    time.Time
	Hevy   []HevyWorkout
	Strava []StravaActivity
	Runs   []Run
	// Uploaded holds FitGlue's uploaded_activities records for ids no run
	// names: "hevy:<id>" or "strava:<id>" → "<SOURCE_…>:<source id>" of the
	// activity that was uploaded. A key present means FitGlue made it.
	Uploaded map[string]string
	// HevyListed / StravaListed are false when that source could not be
	// listed; its column then reads unknown and nothing is concluded from it.
	HevyListed   bool
	StravaListed bool
	// Warnings say why a source could not be listed.
	Warnings []string
}

// Cell states.
const (
	Present = "present"
	Missing = "missing"
	NA      = "n/a"
	Unknown = "unknown"
)

// Cell is one system's column for a session.
type Cell struct {
	State string   `json:"state"`
	IDs   []string `json:"ids,omitempty"`
	Note  string   `json:"note,omitempty"`
}

// Issue kinds.
const (
	KindMissingInFitGlue    = "missing_in_fitglue"     // fixable: replay
	KindStravaUploadMissing = "strava_upload_missing"  // fixable: repost
	KindInProgress          = "in_progress"            // quiet
	KindStravaCopyDeleted   = "strava_copy_deleted"    // report
	KindHevyCopyDeleted     = "hevy_copy_deleted"      // report
	KindHevyUploadFailed    = "hevy_upload_failed"     // report
	KindSourceDeleted       = "source_deleted"         // report
	KindDuplicateSameSource = "duplicate_same_source"  // report
	KindDuplicateOverlap    = "duplicate_overlap"      // report
	KindCrossSourceDup      = "cross_source_duplicate" // report
	KindOrphanUpload        = "orphan_upload"          // report
)

// Issue statuses.
const (
	StatusFixable    = "fixable"     // dry run: --apply would fix it
	StatusApplied    = "applied"     // fixed this run
	StatusAwaiting   = "awaiting"    // fixed by an earlier run, pipeline not caught up yet
	StatusFailed     = "failed"      // the fix errored this run
	StatusUnfixed    = "unfixed"     // fixed MaxAttempts times and still wrong
	StatusReport     = "report"      // reported only; James decides
	StatusInProgress = "in_progress" // nothing to do yet
)

// Action kinds.
const (
	ActReplayHevy   = "replay_hevy"
	ActReplayStrava = "replay_strava"
	ActRepostStrava = "repost_strava"
)

// Action is a fix Apply may carry out.
type Action struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`         // Hevy workout id, Strava activity id, or FitGlue activity id
	Mode   string `json:"mode,omitempty"` // repost mode
	Title  string `json:"title,omitempty"`
	Result string `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Key identifies an action across runs (State is keyed by it).
func (a *Action) Key() string { return a.Kind + ":" + a.Target }

// Issue is one named mismatch on a session.
type Issue struct {
	Kind   string  `json:"kind"`
	Detail string  `json:"detail"`
	Status string  `json:"status"`
	Action *Action `json:"action,omitempty"`
}

// NeedsAttention is true for what James should look at.
func (i *Issue) NeedsAttention() bool {
	switch i.Status {
	case StatusReport, StatusFailed, StatusUnfixed:
		return true
	}
	return false
}

// Session is one real-world workout across the three systems.
type Session struct {
	Start   time.Time `json:"start"`
	Title   string    `json:"title"`
	Type    string    `json:"type"`
	Origin  string    `json:"origin"` // hevy | strava | other | orphan
	Hevy    Cell      `json:"hevy"`
	FitGlue Cell      `json:"fitglue"`
	Strava  Cell      `json:"strava"`
	Issues  []*Issue  `json:"issues,omitempty"`

	hevy       *HevyWorkout
	hevyMade   bool // FitGlue uploaded this Hevy workout
	strava     *StravaActivity
	stravaMade bool // FitGlue uploaded this Strava activity
	runs       []Run
}

// Report is Plan's (and Apply's) result.
type Report struct {
	GeneratedAt time.Time  `json:"generated_at"`
	Since       time.Time  `json:"since"`
	Applied     bool       `json:"applied"`
	Sessions    []*Session `json:"sessions"`
	Actions     []*Action  `json:"actions"`
	Counts      Counts     `json:"counts"`
	// NeedsAttention is one line per issue reconcile could not fix itself.
	NeedsAttention []string `json:"needs_attention"`
	// Fixed is one line per fix applied this run.
	Fixed []string `json:"fixed"`
	// Warnings are problems reading a source (the run still reports).
	Warnings []string `json:"warnings,omitempty"`
}

// Counts summarises a report.
type Counts struct {
	Sessions       int `json:"sessions"`
	OK             int `json:"ok"`
	Fixable        int `json:"fixable"`
	Applied        int `json:"applied"`
	Awaiting       int `json:"awaiting"`
	InProgress     int `json:"in_progress"`
	NeedsAttention int `json:"needs_attention"`
}

// ---------------------------------------------------------------------------
// Plan
// ---------------------------------------------------------------------------

// Plan matches the snapshot into sessions and names every mismatch. Fixable
// issues come back StatusFixable with their Action; Apply turns them into
// applied/awaiting/failed/unfixed.
func Plan(snap Snapshot) *Report {
	sessions := match(snap)

	// Pairwise checks first: a session that looks like a second recording of
	// another must not be "fixed" by a replay that would make a third copy.
	ambiguous := map[*Session]bool{}
	pairIssues(sessions, ambiguous)

	for _, s := range sessions {
		classify(s, snap, ambiguous[s])
	}

	rep := &Report{GeneratedAt: snap.Now, Since: snap.Since}
	for _, s := range sessions {
		if s.Start.Before(snap.Since) {
			continue
		}
		rep.Sessions = append(rep.Sessions, s)
	}
	sort.SliceStable(rep.Sessions, func(i, j int) bool { return rep.Sessions[i].Start.After(rep.Sessions[j].Start) })
	rep.Warnings = append(rep.Warnings, snap.Warnings...)
	if !snap.HevyListed {
		rep.Warnings = append(rep.Warnings, "Hevy could not be listed; its column is unknown")
	}
	if !snap.StravaListed {
		rep.Warnings = append(rep.Warnings, "Strava could not be listed; its column is unknown")
	}
	rep.finish()
	return rep
}

func runKey(source, id string) string { return source + ":" + id }

// match builds the sessions: runs grouped by source id, then Hevy and Strava
// items attached by id, by uploaded record, by time for FitGlue's own
// uploads, else as sessions of their own.
func match(snap Snapshot) []*Session {
	var sessions []*Session
	byRunKey := map[string]*Session{}
	byHevyID := map[string]*Session{}
	byStravaID := map[string]*Session{}

	runs := append([]Run(nil), snap.Runs...)
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].Created.Before(runs[j].Created) })
	for _, r := range runs {
		k := runKey(r.Source, r.SourceID)
		if r.SourceID == "" {
			k = "run:" + r.ID
		}
		s := byRunKey[k]
		if s == nil {
			s = &Session{}
			byRunKey[k] = s
			sessions = append(sessions, s)
		}
		s.runs = append(s.runs, r)
		switch r.Source {
		case "SOURCE_HEVY":
			if r.SourceID != "" {
				byHevyID[r.SourceID] = s
			}
			if r.Strava != nil && r.Strava.ExternalID != "" {
				byStravaID[r.Strava.ExternalID] = s
			}
		case "SOURCE_STRAVA":
			if r.SourceID != "" {
				byStravaID[r.SourceID] = s
			}
			if r.Hevy != nil && r.Hevy.ExternalID != "" {
				byHevyID[r.Hevy.ExternalID] = s
			}
		}
	}

	type looseItem struct {
		s   *Session
		src string
	}
	var looseHevy []looseItem
	for i := range snap.Hevy {
		w := &snap.Hevy[i]
		if s := byHevyID[w.ID]; s != nil && s.hevy == nil {
			s.hevy = w
			s.hevyMade = origin(s) == "strava"
			continue
		}
		if src, ok := snap.Uploaded["hevy:"+w.ID]; ok {
			if s := byRunKey[src]; s != nil && s.hevy == nil {
				s.hevy, s.hevyMade = w, true
				continue
			}
			s := &Session{hevy: w, hevyMade: true}
			sessions = append(sessions, s)
			looseHevy = append(looseHevy, looseItem{s, src})
			continue
		}
		s := &Session{hevy: w}
		sessions = append(sessions, s)
		byRunKey[runKey("SOURCE_HEVY", w.ID)] = s // a Hevy workout FitGlue never ingested
	}

	for i := range snap.Strava {
		a := &snap.Strava[i]
		if s := byStravaID[a.ID]; s != nil && s.strava == nil {
			s.strava = a
			s.stravaMade = origin(s) == "hevy"
			continue
		}
		if src, ok := snap.Uploaded["strava:"+a.ID]; ok {
			if s := byRunKey[src]; s != nil && s.strava == nil {
				s.strava, s.stravaMade = a, true
				continue
			}
			if s := timeMatch(sessions, a.Start, a.Elapsed, a.SportType, wantsStrava); s != nil {
				s.strava, s.stravaMade = a, true
				continue
			}
			sessions = append(sessions, &Session{strava: a, stravaMade: true})
			continue
		}
		// An upload of FitGlue's file whose id never reached the run (the
		// destination outcome is still PENDING, say): attach it by time to the
		// Hevy session waiting for its Strava copy. Anything else is a session
		// of its own — a Garmin recording of a Hevy-logged class is not matched
		// in here but flagged as a cross-source duplicate by pairIssues.
		if isFitGlueFile(a.UploadFile) {
			if s := timeMatch(sessions, a.Start, a.Elapsed, a.SportType, wantsStrava); s != nil {
				s.strava, s.stravaMade = a, true
				continue
			}
		}
		s := &Session{strava: a}
		sessions = append(sessions, s)
		byRunKey[runKey("SOURCE_STRAVA", a.ID)] = s // a Strava activity FitGlue never ingested
	}

	// A Hevy copy FitGlue made whose source run fell outside the listing:
	// try the same time match against Strava-origin sessions.
	for _, loose := range looseHevy {
		w := loose.s.hevy
		s := byRunKey[loose.src]
		if s == nil || s.hevy != nil {
			s = timeMatch(sessions, w.Start, w.End.Sub(w.Start), "weighttraining", wantsHevy)
		}
		if s != nil && s != loose.s && s.hevy == nil {
			s.hevy, s.hevyMade = w, true
			loose.s.hevy = nil
		}
	}
	out := sessions[:0]
	for _, s := range sessions {
		if s.hevy != nil || s.strava != nil || len(s.runs) > 0 {
			out = append(out, s)
		}
	}
	for _, s := range out {
		s.fillHeader()
	}
	return out
}

func isFitGlueFile(name string) bool {
	n := strings.ToLower(name)
	return n == "activity.fit" || strings.HasPrefix(n, "activity.fit")
}

// wantsStrava: a Hevy-origin session with runs and no Strava activity yet.
func wantsStrava(s *Session) bool {
	return s.strava == nil && origin(s) == "hevy" && len(s.runs) > 0
}

// wantsHevy: a Strava-origin session with no Hevy copy yet.
func wantsHevy(s *Session) bool {
	return s.hevy == nil && origin(s) == "strava" && len(s.runs) > 0
}

// timeMatch is the second-pass match: start within MatchWindow, compatible
// type, duration within a quarter (or ten minutes) when both are known.
func timeMatch(sessions []*Session, start time.Time, dur time.Duration, typ string, want func(*Session) bool) *Session {
	var best *Session
	var bestGap time.Duration
	for _, s := range sessions {
		if !want(s) {
			continue
		}
		st, sd, styp := s.when()
		gap := absDur(st.Sub(start))
		if gap > MatchWindow {
			continue
		}
		if category(styp) != "" && category(typ) != "" && category(styp) != category(typ) {
			continue
		}
		if sd > 0 && dur > 0 {
			diff := absDur(sd - dur)
			if diff > 10*time.Minute && float64(diff) > 0.25*float64(maxDur(sd, dur)) {
				continue
			}
		}
		if best == nil || gap < bestGap {
			best, bestGap = s, gap
		}
	}
	return best
}

// origin is where the session started: the first run's source, else the
// item FitGlue did not make.
func origin(s *Session) string {
	if len(s.runs) > 0 {
		switch s.runs[0].Source {
		case "SOURCE_HEVY":
			return "hevy"
		case "SOURCE_STRAVA":
			return "strava"
		default:
			return "other"
		}
	}
	if s.hevy != nil && !s.hevyMade {
		return "hevy"
	}
	if s.strava != nil && !s.stravaMade {
		return "strava"
	}
	return "orphan"
}

// when is the session's start, duration and type, from the origin item first.
func (s *Session) when() (time.Time, time.Duration, string) {
	var start time.Time
	var dur time.Duration
	var typ string
	if s.hevy != nil && !s.hevyMade {
		start, dur, typ = s.hevy.Start, s.hevy.End.Sub(s.hevy.Start), "weighttraining"
	}
	if s.strava != nil && (start.IsZero() || dur <= 0) {
		if start.IsZero() {
			start = s.strava.Start
		}
		if dur <= 0 {
			dur = s.strava.Elapsed
		}
		if typ == "" && !s.stravaMade {
			typ = s.strava.SportType
		}
	}
	if s.hevy != nil && start.IsZero() {
		start, dur = s.hevy.Start, s.hevy.End.Sub(s.hevy.Start)
	}
	if len(s.runs) > 0 {
		r := s.runs[0]
		if start.IsZero() {
			start = r.Start
		}
		if r.Type != "" {
			typ = r.Type // the type FitGlue settled on (type-mapper) wins
		}
	}
	if dur < 0 {
		dur = 0
	}
	return start, dur, typ
}

func (s *Session) fillHeader() {
	s.Origin = origin(s)
	start, _, typ := s.when()
	s.Start = start
	s.Type = strings.TrimPrefix(typ, "ACTIVITY_TYPE_")
	switch {
	case len(s.runs) > 0 && s.runs[len(s.runs)-1].Title != "":
		s.Title = s.runs[len(s.runs)-1].Title
	case s.hevy != nil && s.Origin == "hevy":
		s.Title = s.hevy.Title
	case s.strava != nil:
		s.Title = s.strava.Name
	case s.hevy != nil:
		s.Title = s.hevy.Title
	}
}

// category folds Hevy, FitGlue and Strava type names into comparable groups:
// every gym/class type is "strength"; anything else is its own name.
func category(t string) string {
	n := strings.ToLower(strings.TrimPrefix(t, "ACTIVITY_TYPE_"))
	n = strings.ReplaceAll(n, "_", "")
	switch n {
	case "":
		return ""
	case "weighttraining", "workout", "crossfit", "pilates", "yoga", "hiit",
		"highintensityintervaltraining", "strengthtraining", "functionaltraining", "stairstepper", "elliptical":
		return "strength"
	case "run", "trailrun", "virtualrun", "running", "treadmill":
		return "run"
	case "ride", "virtualride", "ebikeride", "cycling", "mountainbikeride", "gravelride":
		return "ride"
	}
	return n
}

func isStrengthOrClass(t string) bool { return category(t) == "strength" }

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

func maxDur(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

// overlap is the share of the shorter session covered by the other.
func overlap(aStart time.Time, aDur time.Duration, bStart time.Time, bDur time.Duration) float64 {
	if aDur <= 0 || bDur <= 0 {
		return 0
	}
	aEnd, bEnd := aStart.Add(aDur), bStart.Add(bDur)
	lo, hi := aStart, aEnd
	if bStart.After(lo) {
		lo = bStart
	}
	if bEnd.Before(hi) {
		hi = bEnd
	}
	if !hi.After(lo) {
		return 0
	}
	shorter := aDur
	if bDur < shorter {
		shorter = bDur
	}
	return float64(hi.Sub(lo)) / float64(shorter)
}

// pairIssues names two sessions that look like one real session: a Garmin
// (third-party) Strava recording of a class James logged in Hevy, or two
// FitGlue activities for the same time.
func pairIssues(sessions []*Session, ambiguous map[*Session]bool) {
	for i := 0; i < len(sessions); i++ {
		for j := i + 1; j < len(sessions); j++ {
			a, b := sessions[i], sessions[j]
			as, ad, at := a.when()
			bs, bd, bt := b.when()
			near := absDur(as.Sub(bs)) <= MatchWindow
			ov := overlap(as, ad, bs, bd)
			if !near && ov <= 0.8 {
				continue
			}
			if a.Origin == "orphan" || b.Origin == "orphan" {
				continue
			}
			h, st := a, b
			if a.Origin == "strava" && b.Origin == "hevy" {
				h, st = b, a
			}
			if h.Origin == "hevy" && st.Origin == "strava" && st.strava != nil && !st.stravaMade &&
				(isStrengthOrClass(st.strava.SportType) || isStrengthOrClass(firstRunType(st))) {
				ambiguous[st] = true
				if len(h.runs) == 0 {
					ambiguous[h] = true
				}
				st.add(&Issue{Kind: KindCrossSourceDup, Status: StatusReport, Detail: fmt.Sprintf(
					"Strava %s %q (%s) looks like a second recording of Hevy workout %q at the same time — probably a watch recording; keep one",
					st.strava.ID, st.strava.Name, st.strava.SportType, h.Title)})
				continue
			}
			if !near && category(at) != category(bt) {
				continue
			}
			if len(a.runs) == 0 || len(b.runs) == 0 {
				// One of them never reached FitGlue: replaying it would put a
				// second activity for this session into FitGlue. Report instead.
				for _, x := range []*Session{a, b} {
					if len(x.runs) == 0 {
						ambiguous[x] = true
					}
				}
				continue
			}
			// Name it on the less complete one so the recommendation reads "drop this".
			keep, drop := a, b
			if rank(b) > rank(a) {
				keep, drop = b, a
			}
			drop.add(&Issue{Kind: KindDuplicateOverlap, Status: StatusReport, Detail: fmt.Sprintf(
				"FitGlue has %q (%s, %s) and %q (%s, %s) for the same time; recommend deleting %q (run %s) and keeping %q",
				drop.Title, drop.runs[0].SourceID, shortStatus(drop.runs[0].Status),
				keep.Title, keep.runs[0].SourceID, shortStatus(keep.runs[0].Status),
				drop.Title, drop.runs[0].ID, keep.Title)})
		}
	}
}

func firstRunType(s *Session) string {
	if len(s.runs) > 0 {
		return s.runs[0].Type
	}
	return ""
}

// rank orders sessions by how complete they are, for "keep this one".
func rank(s *Session) int {
	r := 0
	if s.strava != nil {
		r += 2
	}
	if s.hevy != nil {
		r++
	}
	for _, run := range s.runs {
		switch run.Status {
		case "PIPELINE_RUN_STATUS_SYNCED":
			r += 4
		case "PIPELINE_RUN_STATUS_PARTIAL":
			r += 2
		case "PIPELINE_RUN_STATUS_CANCELLED", "PIPELINE_RUN_STATUS_ARCHIVED", "PIPELINE_RUN_STATUS_FAILED":
			r -= 2
		}
	}
	return r
}

func shortStatus(s string) string {
	return strings.ToLower(strings.TrimPrefix(s, "PIPELINE_RUN_STATUS_"))
}

func (s *Session) add(i *Issue) { s.Issues = append(s.Issues, i) }

// ended is when the session finished, for the Grace check.
func (s *Session) ended() time.Time {
	st, d, _ := s.when()
	if s.hevy != nil && !s.hevy.End.IsZero() {
		return s.hevy.End
	}
	return st.Add(d)
}

func runIDs(runs []Run) []string {
	ids := make([]string, 0, len(runs))
	for _, r := range runs {
		ids = append(ids, r.ID)
	}
	return ids
}

// classify fills the three cells and the per-session issues.
func classify(s *Session, snap Snapshot, ambiguous bool) {
	recent := snap.Now.Sub(s.ended()) < Grace

	// FitGlue column is the same for every origin.
	if len(s.runs) > 0 {
		s.FitGlue = Cell{State: Present, IDs: runIDs(s.runs), Note: shortStatus(s.runs[len(s.runs)-1].Status)}
		if len(s.runs) > 1 {
			var parts []string
			keep := s.runs[0]
			for _, r := range s.runs {
				parts = append(parts, fmt.Sprintf("%s (%s)", r.ID, shortStatus(r.Status)))
				if runRank(r) > runRank(keep) {
					keep = r
				}
			}
			s.add(&Issue{Kind: KindDuplicateSameSource, Status: StatusReport, Detail: fmt.Sprintf(
				"FitGlue has %d activities for one %s %s: %s; recommend keeping %s and deleting the rest",
				len(s.runs), sourceName(s.runs[0].Source), s.runs[0].SourceID, strings.Join(parts, ", "), keep.ID)})
		}
	} else {
		s.FitGlue = Cell{State: Missing}
	}

	hevyCell := func() Cell {
		if !snap.HevyListed {
			return Cell{State: Unknown}
		}
		if s.hevy != nil {
			c := Cell{State: Present, IDs: []string{s.hevy.ID}}
			if s.hevyMade {
				c.Note = "FitGlue copy"
			}
			return c
		}
		return Cell{State: Missing}
	}
	stravaCell := func() Cell {
		if !snap.StravaListed {
			return Cell{State: Unknown}
		}
		if s.strava != nil {
			c := Cell{State: Present, IDs: []string{s.strava.ID}}
			if s.stravaMade {
				c.Note = "FitGlue upload"
			}
			return c
		}
		return Cell{State: Missing}
	}

	switch s.Origin {
	case "hevy":
		s.Hevy = hevyCell()
		s.Strava = stravaCell()
		if s.Hevy.State == Missing && len(s.runs) > 0 {
			s.add(&Issue{Kind: KindSourceDeleted, Status: StatusReport, Detail: fmt.Sprintf(
				"Hevy workout %s is gone from Hevy but FitGlue still lists %q (run %s)", s.runs[0].SourceID, s.Title, s.runs[0].ID)})
		}
		if len(s.runs) == 0 {
			if s.Strava.State == Missing {
				s.Strava.Note = "uploads after ingest"
			}
			s.missingInFitGlue(ActReplayHevy, s.hevy.ID, "Hevy workout", recent, ambiguous)
			return
		}
		if s.Strava.State != Missing {
			return
		}
		run, dest := latestDest(s.runs, func(r Run) *Dest { return r.Strava })
		switch {
		case dest == nil:
			s.Strava = Cell{State: NA, Note: "pipeline has no Strava destination"}
		case dest.ExternalID != "":
			s.Strava.IDs = []string{dest.ExternalID}
			s.add(&Issue{Kind: KindStravaCopyDeleted, Status: StatusReport, Detail: fmt.Sprintf(
				"FitGlue uploaded %q to Strava as %s but it is no longer on Strava (deleted there?)", s.Title, dest.ExternalID)})
		case runInProgress(run, snap.Now) || recent:
			s.Strava.Note = "upload in progress"
			s.add(&Issue{Kind: KindInProgress, Status: StatusInProgress, Detail: fmt.Sprintf(
				"%q: run %s is %s; the Strava upload has not happened yet", s.Title, run.ID, shortStatus(run.Status))})
		default:
			s.add(&Issue{Kind: KindStravaUploadMissing, Status: StatusFixable, Detail: fmt.Sprintf(
				"%q is in FitGlue (%s) but its Strava upload is %s", s.Title, shortStatus(run.Status), destStatus(dest)),
				Action: &Action{Kind: ActRepostStrava, Target: run.ActivityID, Mode: "retry-destination", Title: s.Title}})
		}

	case "strava":
		s.Strava = stravaCell()
		s.Hevy = Cell{State: NA}
		if s.hevy != nil {
			s.Hevy = hevyCell()
		}
		if s.Strava.State == Missing && len(s.runs) > 0 {
			s.add(&Issue{Kind: KindSourceDeleted, Status: StatusReport, Detail: fmt.Sprintf(
				"Strava activity %s is gone from Strava but FitGlue still lists %q (run %s, %s)",
				s.runs[0].SourceID, s.Title, s.runs[0].ID, shortStatus(s.runs[0].Status))})
		}
		if len(s.runs) == 0 {
			s.missingInFitGlue(ActReplayStrava, s.strava.ID, "Strava activity", recent, ambiguous)
			return
		}
		// The Hevy copy FitGlue makes of a Strava activity.
		_, dest := latestDest(s.runs, func(r Run) *Dest { return r.Hevy })
		switch {
		case s.hevy != nil:
			s.Hevy = hevyCell()
		case dest == nil:
		case dest.ExternalID != "" && snap.HevyListed:
			s.Hevy = Cell{State: Missing, IDs: []string{dest.ExternalID}}
			s.add(&Issue{Kind: KindHevyCopyDeleted, Status: StatusReport, Detail: fmt.Sprintf(
				"FitGlue copied %q to Hevy as %s but it is no longer in Hevy (deleted there?)", s.Title, dest.ExternalID)})
		case dest.Status == "DESTINATION_STATUS_FAILED":
			s.Hevy = Cell{State: Missing, Note: "Hevy copy failed"}
			s.add(&Issue{Kind: KindHevyUploadFailed, Status: StatusReport, Detail: fmt.Sprintf(
				"%q: FitGlue's Hevy copy failed", s.Title)})
		}

	case "orphan":
		s.Hevy, s.Strava = hevyCell(), stravaCell()
		s.FitGlue = Cell{State: Missing}
		what := ""
		switch {
		case s.strava != nil:
			what = "Strava activity " + s.strava.ID
		case s.hevy != nil:
			what = "Hevy workout " + s.hevy.ID
		}
		s.add(&Issue{Kind: KindOrphanUpload, Status: StatusReport, Detail: fmt.Sprintf(
			"%s %q was uploaded by FitGlue but FitGlue has no activity for it in the window", what, s.Title)})

	default: // parkrun results, file uploads: FitGlue only
		s.Hevy, s.Strava = Cell{State: NA}, Cell{State: NA}
	}
}

func (s *Session) missingInFitGlue(kind, id, what string, recent, ambiguous bool) {
	switch {
	case recent:
		s.add(&Issue{Kind: KindInProgress, Status: StatusInProgress, Detail: fmt.Sprintf(
			"%s %s %q finished under %s ago; leaving it to the webhook", what, id, s.Title, Grace)})
	case ambiguous:
		s.add(&Issue{Kind: KindMissingInFitGlue, Status: StatusReport, Detail: fmt.Sprintf(
			"%s %s %q never reached FitGlue, but it overlaps another recording of the same session; not replaying it until one is deleted",
			what, id, s.Title)})
	default:
		s.add(&Issue{Kind: KindMissingInFitGlue, Status: StatusFixable, Detail: fmt.Sprintf(
			"%s %s %q never reached FitGlue", what, id, s.Title),
			Action: &Action{Kind: kind, Target: id, Title: s.Title}})
	}
}

func sourceName(src string) string {
	switch src {
	case "SOURCE_HEVY":
		return "Hevy workout"
	case "SOURCE_STRAVA":
		return "Strava activity"
	}
	return strings.ToLower(strings.TrimPrefix(src, "SOURCE_")) + " id"
}

func runRank(r Run) int {
	switch r.Status {
	case "PIPELINE_RUN_STATUS_SYNCED":
		return 3
	case "PIPELINE_RUN_STATUS_PARTIAL", "PIPELINE_RUN_STATUS_PENDING", "PIPELINE_RUN_STATUS_RUNNING":
		return 2
	case "PIPELINE_RUN_STATUS_CANCELLED", "PIPELINE_RUN_STATUS_ARCHIVED", "PIPELINE_RUN_STATUS_FAILED":
		return 0
	}
	return 1
}

// latestDest is the newest run's outcome for one destination, preferring a
// run that recorded an external id.
func latestDest(runs []Run, pick func(Run) *Dest) (Run, *Dest) {
	var run Run
	var dest *Dest
	for _, r := range runs {
		d := pick(r)
		if d == nil {
			continue
		}
		if dest == nil || d.ExternalID != "" || dest.ExternalID == "" {
			run, dest = r, d
		}
	}
	if dest == nil && len(runs) > 0 {
		run = runs[len(runs)-1]
	}
	return run, dest
}

func runInProgress(r Run, now time.Time) bool {
	switch r.Status {
	case "PIPELINE_RUN_STATUS_PENDING":
		return true // waiting on James's input; the upload follows it
	case "PIPELINE_RUN_STATUS_RUNNING":
		last := r.Updated
		if last.IsZero() {
			last = r.Created
		}
		return now.Sub(last) < StaleRun
	}
	return false
}

func destStatus(d *Dest) string {
	if d == nil {
		return "not attempted"
	}
	return strings.ToLower(strings.TrimPrefix(d.Status, "DESTINATION_STATUS_"))
}

// finish recomputes counts and the one-line lists.
func (r *Report) finish() {
	r.Counts = Counts{Sessions: len(r.Sessions)}
	r.Actions = nil
	r.NeedsAttention = []string{}
	r.Fixed = []string{}
	for _, s := range r.Sessions {
		if len(s.Issues) == 0 {
			r.Counts.OK++
		}
		for _, i := range s.Issues {
			if i.Action != nil {
				r.Actions = append(r.Actions, i.Action)
			}
			switch i.Status {
			case StatusFixable:
				r.Counts.Fixable++
			case StatusApplied:
				r.Counts.Applied++
				r.Fixed = append(r.Fixed, fmt.Sprintf("%s %s: %s", s.Start.Format("Mon 2 Jan"), s.Title, i.Action.Result))
			case StatusAwaiting:
				r.Counts.Awaiting++
			case StatusInProgress:
				r.Counts.InProgress++
			}
			if i.NeedsAttention() {
				r.Counts.NeedsAttention++
				r.NeedsAttention = append(r.NeedsAttention, fmt.Sprintf("%s: %s", s.Start.Format("Mon 2 Jan"), i.Detail))
			}
		}
	}
}
