package reconcile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Applier carries out the fixes. The CLI's implementation sends exactly what
// the webhook would have (replays) and calls the pipeline's RepostActivity
// RPC (reposts); tests use a fake.
type Applier interface {
	ReplayHevy(ctx context.Context, workoutID string) error
	ReplayStrava(ctx context.Context, activityID string) error
	RepostStrava(ctx context.Context, fitglueActivityID, mode string) error
}

// Cooldown is how long after a fix reconcile waits for the pipeline before
// it would fix the same thing again.
const Cooldown = 6 * time.Hour

// MaxAttempts is how many times one fix is tried before it is reported as
// unfixed instead.
const MaxAttempts = 2

// stateTTL drops remembered fixes nobody has needed for a month.
const stateTTL = 30 * 24 * time.Hour

// Attempt is what State remembers about one action.
type Attempt struct {
	Count int       `json:"count"`
	Last  time.Time `json:"last"`
	Error string    `json:"error,omitempty"`
}

// State is the memory that makes --apply idempotent: a replay publishes an
// event and the run appears a few seconds to minutes later, so a second
// reconcile in between must not publish it again.
type State struct {
	Attempts map[string]*Attempt `json:"attempts"`
}

// LoadState reads the state file; a missing file is an empty state.
func LoadState(path string) (*State, error) {
	st := &State{Attempts: map[string]*Attempt{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, st); err != nil {
		return nil, fmt.Errorf("state %s: %w", path, err)
	}
	if st.Attempts == nil {
		st.Attempts = map[string]*Attempt{}
	}
	return st, nil
}

// Save writes the state atomically.
func (s *State) Save(path string, now time.Time) error {
	for k, a := range s.Attempts {
		if now.Sub(a.Last) > stateTTL {
			delete(s.Attempts, k)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", " ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Apply resolves every fixable issue. With apply false it only marks fixes
// an earlier run already made (awaiting/unfixed); with apply true it carries
// out the rest through ap and records them in st.
func Apply(ctx context.Context, rep *Report, ap Applier, st *State, now time.Time, apply bool) {
	rep.Applied = apply
	for _, s := range rep.Sessions {
		for _, i := range s.Issues {
			if i.Action == nil || i.Status != StatusFixable {
				continue
			}
			a := i.Action
			prev := st.Attempts[a.Key()]
			switch {
			case prev != nil && now.Sub(prev.Last) < Cooldown:
				i.Status = StatusAwaiting
				a.Result = fmt.Sprintf("already %s at %s; waiting for the pipeline", verb(a.Kind), prev.Last.Local().Format("Mon 15:04"))
				continue
			case prev != nil && prev.Count >= MaxAttempts:
				i.Status = StatusUnfixed
				a.Result = fmt.Sprintf("%s %d times, still wrong", verb(a.Kind), prev.Count)
				i.Detail += " — " + a.Result
				continue
			}
			if !apply {
				a.Result = "would " + wouldVerb(a.Kind)
				continue
			}
			err := run(ctx, ap, a)
			if prev == nil {
				prev = &Attempt{}
				st.Attempts[a.Key()] = prev
			}
			prev.Count++
			prev.Last = now
			prev.Error = ""
			if err != nil {
				prev.Error = err.Error()
				i.Status = StatusFailed
				a.Error = err.Error()
				a.Result = "failed"
				i.Detail += " — fix failed: " + err.Error()
				continue
			}
			i.Status = StatusApplied
			a.Result = verb(a.Kind)
		}
	}
	rep.finish()
}

func run(ctx context.Context, ap Applier, a *Action) error {
	switch a.Kind {
	case ActReplayHevy:
		return ap.ReplayHevy(ctx, a.Target)
	case ActReplayStrava:
		return ap.ReplayStrava(ctx, a.Target)
	case ActRepostStrava:
		return ap.RepostStrava(ctx, a.Target, a.Mode)
	}
	return fmt.Errorf("unknown action %q", a.Kind)
}

func verb(kind string) string {
	switch kind {
	case ActReplayHevy:
		return "replayed from Hevy"
	case ActReplayStrava:
		return "replayed from Strava"
	case ActRepostStrava:
		return "reposted to Strava"
	}
	return kind
}

func wouldVerb(kind string) string {
	switch kind {
	case ActReplayHevy:
		return "replay from Hevy"
	case ActReplayStrava:
		return "replay from Strava"
	case ActRepostStrava:
		return "repost to Strava"
	}
	return kind
}

// ---------------------------------------------------------------------------
// Text output
// ---------------------------------------------------------------------------

func mark(c Cell) string {
	switch c.State {
	case Present:
		return "✓"
	case Missing:
		return "✗"
	case Unknown:
		return "?"
	}
	return "–"
}

func cellText(c Cell, width int) string {
	t := mark(c)
	if len(c.IDs) > 0 {
		id := c.IDs[0]
		if len(id) > 13 {
			id = id[:8]
		}
		t += " " + id
		if len(c.IDs) > 1 {
			t += fmt.Sprintf(" +%d", len(c.IDs)-1)
		}
	}
	if c.Note != "" {
		t += " (" + c.Note + ")"
	}
	return pad(t, width)
}

func pad(s string, width int) string {
	n := len([]rune(s))
	if n >= width {
		r := []rune(s)
		return string(r[:width-1]) + " "
	}
	return s + strings.Repeat(" ", width-n)
}

// WriteText prints the per-session three-column table, then the issues and
// the actions.
func WriteText(w io.Writer, rep *Report, loc *time.Location) {
	mode := "dry run"
	if rep.Applied {
		mode = "applied"
	}
	fmt.Fprintf(w, "reconcile %s → %s (%s)\n\n", rep.Since.In(loc).Format("Mon 2 Jan 15:04"), rep.GeneratedAt.In(loc).Format("Mon 2 Jan 15:04"), mode)
	fmt.Fprintf(w, "%s%s%s%s%s\n", pad("START", 17), pad("SESSION", 34), pad("HEVY", 26), pad("FITGLUE", 26), "STRAVA")
	for _, s := range rep.Sessions {
		fmt.Fprintf(w, "%s%s%s%s%s\n", pad(s.Start.In(loc).Format("Mon 02 Jan 15:04"), 17), pad(s.Title, 34),
			cellText(s.Hevy, 26), cellText(s.FitGlue, 26), strings.TrimRight(cellText(s.Strava, 40), " "))
	}
	fmt.Fprintln(w)
	var issues []string
	for _, s := range rep.Sessions {
		for _, i := range s.Issues {
			line := fmt.Sprintf("  [%s] %s %s: %s", i.Status, s.Start.In(loc).Format("Mon 2 Jan"), i.Kind, i.Detail)
			if i.Action != nil && i.Action.Result != "" && i.Status != StatusUnfixed {
				line += " → " + i.Action.Result
			}
			issues = append(issues, line)
		}
	}
	if len(issues) == 0 {
		fmt.Fprintln(w, "no mismatches: Hevy, FitGlue and Strava agree")
	} else {
		fmt.Fprintln(w, "issues:")
		for _, l := range issues {
			fmt.Fprintln(w, l)
		}
	}
	for _, warn := range rep.Warnings {
		fmt.Fprintln(w, "warning: "+warn)
	}
	c := rep.Counts
	fmt.Fprintf(w, "\n%d sessions: %d ok, %d fixable, %d applied, %d awaiting, %d in progress, %d need attention\n",
		c.Sessions, c.OK, c.Fixable, c.Applied, c.Awaiting, c.InProgress, c.NeedsAttention)
}
