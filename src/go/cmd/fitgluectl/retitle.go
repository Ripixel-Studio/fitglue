package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	firestore "cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/fitglue/server/src/go/pkg/title"
)

// `fitgluectl retitle` recomputes the canonical numbered titles of his showcase
// (pkg/title): every parkrun, oldest first, is "<Place> parkrun (#n)" with its special
// tail, and every numbered habit is "<Name> (#n)". The set of activities is his profile
// entries (a showcase without one is ignored). It writes the title on both the profile
// entry and the showcased activity, and sets each series' auto_increment counter to its
// count, so the next one numbers on. Firestore only: Strava and Hevy are not touched.
//
// Dry run by default: prints every title and counter that would change.
//
//	fitgluectl retitle --uid UID            # plan
//	fitgluectl retitle --uid UID --json     # plan as JSON
//	fitgluectl retitle --uid UID --apply    # write it

// retitleSeries is one numbered series. Adding a numbered habit is one entry.
type retitleSeries struct {
	Counter string                                          // users/{uid}/counters/{Counter}, auto_increment's key
	Match   *regexp.Regexp                                  // a title in the series
	Title   func(old string, n int, local time.Time) string // the canonical title
}

var retitleSeriesList = []retitleSeries{
	{
		Counter: "parkrun",
		Match:   regexp.MustCompile(`(?i)\bparkrun\b`),
		Title: func(old string, n int, local time.Time) string {
			return title.Parkrun(parkrunPlaceFromTitle(old), n, title.SpecialDay(local))
		},
	},
	numberedHabit("bingham_sunday_running_club", "Bingham Sunday Running Club"),
}

// numberedHabit is a series whose title is its name plus the number.
func numberedHabit(counter, name string) retitleSeries {
	return retitleSeries{
		Counter: counter,
		Match:   regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(name) + `\b`),
		Title:   func(_ string, n int, _ time.Time) string { return title.WithNumber(name, n) },
	}
}

var parkrunWord = regexp.MustCompile(`(?i)\s*\bparkrun\b`)

// parkrunPlaceFromTitle is the text before "parkrun": "Newark Parkrun - Christmas Day
// Edition (#13)" → "Newark".
func parkrunPlaceFromTitle(t string) string {
	if loc := parkrunWord.FindStringIndex(t); loc != nil {
		t = t[:loc[0]]
	}
	return strings.Trim(t, " -,:")
}

var retitleZone = func() *time.Location {
	loc, err := time.LoadLocation("Europe/London")
	if err != nil {
		return time.UTC
	}
	return loc
}()

type retitleEntry struct {
	ID    string
	Title string
	Start time.Time
}

// retitleStore is the Firestore surface retitle uses.
type retitleStore interface {
	Entries(ctx context.Context, uid string) ([]retitleEntry, error)
	// ShowcaseTitle reports the showcased activity's title; ok is false when it is gone.
	ShowcaseTitle(ctx context.Context, id string) (t string, ok bool, err error)
	SetEntryTitle(ctx context.Context, uid, id, t string) error
	SetShowcaseTitle(ctx context.Context, id, t string) error
	// Counter reports a counter's count; ok is false when it does not exist.
	Counter(ctx context.Context, uid, key string) (n int64, ok bool, err error)
	SetCounter(ctx context.Context, uid, key string, n int64) error
}

type retitleChange struct {
	ID              string `json:"id"`
	Date            string `json:"date"`
	Series          string `json:"series"`
	EntryBefore     string `json:"entry_before"`
	ShowcaseBefore  string `json:"showcase_before,omitempty"`
	ShowcaseMissing bool   `json:"showcase_missing,omitempty"`
	After           string `json:"after"`
}

type counterChange struct {
	Key     string `json:"key"`
	Before  int64  `json:"before"`
	Missing bool   `json:"missing,omitempty"`
	After   int64  `json:"after"`
}

type retitlePlan struct {
	Changes  []retitleChange `json:"changes"`
	Counters []counterChange `json:"counters"`
	Applied  bool            `json:"applied"`
}

// planRetitle numbers each series oldest first and lists what differs.
func planRetitle(ctx context.Context, store retitleStore, uid string, series []retitleSeries) (*retitlePlan, error) {
	entries, err := store.Entries(ctx, uid)
	if err != nil {
		return nil, fmt.Errorf("entries: %w", err)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if !entries[i].Start.Equal(entries[j].Start) {
			return entries[i].Start.Before(entries[j].Start)
		}
		return entries[i].ID < entries[j].ID
	})
	plan := &retitlePlan{Changes: []retitleChange{}, Counters: []counterChange{}}
	counts := make([]int, len(series))
	for _, e := range entries {
		for si, s := range series {
			if !s.Match.MatchString(e.Title) {
				continue
			}
			counts[si]++
			want := s.Title(e.Title, counts[si], e.Start.In(retitleZone))
			sc, ok, err := store.ShowcaseTitle(ctx, e.ID)
			if err != nil {
				return nil, fmt.Errorf("showcase %s: %w", e.ID, err)
			}
			if e.Title != want || (ok && sc != want) {
				c := retitleChange{ID: e.ID, Date: e.Start.In(retitleZone).Format("2006-01-02"), Series: s.Counter, EntryBefore: e.Title, After: want, ShowcaseMissing: !ok}
				if ok && sc != e.Title {
					c.ShowcaseBefore = sc
				}
				plan.Changes = append(plan.Changes, c)
			}
			break // a title belongs to the first series it matches
		}
	}
	for si, s := range series {
		n, ok, err := store.Counter(ctx, uid, s.Counter)
		if err != nil {
			return nil, fmt.Errorf("counter %s: %w", s.Counter, err)
		}
		if want := int64(counts[si]); !ok || n != want {
			plan.Counters = append(plan.Counters, counterChange{Key: s.Counter, Before: n, Missing: !ok, After: want})
		}
	}
	return plan, nil
}

func applyRetitle(ctx context.Context, store retitleStore, uid string, plan *retitlePlan) error {
	for _, c := range plan.Changes {
		if err := store.SetEntryTitle(ctx, uid, c.ID, c.After); err != nil {
			return fmt.Errorf("entry %s: %w", c.ID, err)
		}
		if !c.ShowcaseMissing {
			if err := store.SetShowcaseTitle(ctx, c.ID, c.After); err != nil {
				return fmt.Errorf("showcase %s: %w", c.ID, err)
			}
		}
	}
	for _, c := range plan.Counters {
		if err := store.SetCounter(ctx, uid, c.Key, c.After); err != nil {
			return fmt.Errorf("counter %s: %w", c.Key, err)
		}
	}
	plan.Applied = true
	return nil
}

func printRetitlePlan(w io.Writer, plan *retitlePlan, apply bool) {
	for _, c := range plan.Changes {
		before := c.EntryBefore
		if c.ShowcaseBefore != "" {
			before += fmt.Sprintf(" (showcase: %q)", c.ShowcaseBefore)
		}
		if c.ShowcaseMissing {
			before += " (no showcased activity)"
		}
		fmt.Fprintf(w, "%s %s: %s → %s\n", c.Date, c.ID, before, c.After)
	}
	for _, c := range plan.Counters {
		before := fmt.Sprintf("%d", c.Before)
		if c.Missing {
			before = "missing"
		}
		fmt.Fprintf(w, "counter %s: %s → %d\n", c.Key, before, c.After)
	}
	verb := "would change (dry run; --apply writes them)"
	if apply {
		verb = "changed"
	}
	fmt.Fprintf(w, "%d titles, %d counters %s\n", len(plan.Changes), len(plan.Counters), verb)
}

type retitleFlags struct {
	project string
	uid     string
	apply   bool
	json    bool
}

func retitleCommand(ctx context.Context, args []string, stdout, stderr io.Writer, open func(ctx context.Context, project string) (retitleStore, func(), error)) error {
	f := &retitleFlags{}
	fs := flag.NewFlagSet("retitle", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&f.project, "project", envOr(projectEnv, defaultProject), "GCP project holding Firestore (default $"+projectEnv+", else fitglue-server-prod)")
	fs.StringVar(&f.uid, "uid", os.Getenv(ownerUIDEnv), "owner's user id (default $"+ownerUIDEnv+")")
	fs.BoolVar(&f.apply, "apply", false, "write the titles and counters (default: print the plan only)")
	fs.BoolVar(&f.json, "json", false, "print the plan as JSON")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if f.uid == "" {
		return errors.New("--uid is required (or set " + ownerUIDEnv + ")")
	}
	store, cleanup, err := open(ctx, f.project)
	if err != nil {
		return err
	}
	defer cleanup()
	plan, err := planRetitle(ctx, store, f.uid, retitleSeriesList)
	if err != nil {
		return err
	}
	if f.apply {
		if err := applyRetitle(ctx, store, f.uid, plan); err != nil {
			return err
		}
	}
	if f.json {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(plan)
	}
	printRetitlePlan(stdout, plan, f.apply)
	return nil
}

// firestoreRetitle is the live store.
type firestoreRetitle struct{ fs *firestore.Client }

func openLiveRetitle(ctx context.Context, project string) (retitleStore, func(), error) {
	fs, err := firestore.NewClient(ctx, project)
	if err != nil {
		return nil, nil, fmt.Errorf("firestore client: %w", err)
	}
	return &firestoreRetitle{fs: fs}, func() { fs.Close() }, nil
}

func (s *firestoreRetitle) entries(uid string) *firestore.CollectionRef {
	return s.fs.Collection("users").Doc(uid).Collection("showcase_profile_entries")
}

func (s *firestoreRetitle) Entries(ctx context.Context, uid string) ([]retitleEntry, error) {
	it := s.entries(uid).Documents(ctx)
	defer it.Stop()
	var out []retitleEntry
	for {
		doc, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		e := retitleEntry{ID: doc.Ref.ID}
		if v, ok := doc.Data()["title"].(string); ok {
			e.Title = v
		}
		if v, ok := doc.Data()["start_time"].(time.Time); ok {
			e.Start = v
		}
		out = append(out, e)
	}
}

func (s *firestoreRetitle) ShowcaseTitle(ctx context.Context, id string) (string, bool, error) {
	doc, err := s.fs.Collection("showcased_activities").Doc(id).Get(ctx)
	if status.Code(err) == codes.NotFound {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	t, _ := doc.Data()["title"].(string)
	return t, true, nil
}

func (s *firestoreRetitle) SetEntryTitle(ctx context.Context, uid, id, t string) error {
	_, err := s.entries(uid).Doc(id).Update(ctx, []firestore.Update{{Path: "title", Value: t}})
	return err
}

func (s *firestoreRetitle) SetShowcaseTitle(ctx context.Context, id, t string) error {
	_, err := s.fs.Collection("showcased_activities").Doc(id).Update(ctx, []firestore.Update{{Path: "title", Value: t}})
	return err
}

func (s *firestoreRetitle) counter(uid, key string) *firestore.DocumentRef {
	return s.fs.Collection("users").Doc(uid).Collection("counters").Doc(key)
}

func (s *firestoreRetitle) Counter(ctx context.Context, uid, key string) (int64, bool, error) {
	doc, err := s.counter(uid, key).Get(ctx)
	if status.Code(err) == codes.NotFound {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	switch n := doc.Data()["count"].(type) {
	case int64:
		return n, true, nil
	case float64:
		return int64(n), true, nil
	}
	return 0, true, nil
}

func (s *firestoreRetitle) SetCounter(ctx context.Context, uid, key string, n int64) error {
	_, err := s.counter(uid, key).Set(ctx, map[string]interface{}{
		"id":           key,
		"count":        n,
		"last_updated": time.Now(),
	}, firestore.MergeAll)
	return err
}
