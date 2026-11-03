package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fitglue/server/src/go/internal/reconcile"
)

// `fitgluectl reconcile` is the daily three-way check between Hevy, FitGlue
// and Strava (internal/reconcile has the matching rules). It lists Hevy
// workouts and Strava activities over the window, compares them with
// FitGlue's pipeline runs, prints one row per session with a cell per system,
// and names every mismatch. With --apply it fixes the two things it may fix
// on its own — a workout FitGlue never ingested (replayed as the webhook
// would have sent it) and a Hevy workout whose Strava upload is missing
// (reposted to Strava) — and only reports the rest: duplicates, deleted
// sources, copies deleted downstream. It never deletes anything.
//
//	fitgluectl reconcile [--since 14d] [--apply] [--json] [--state FILE]
//
// Jarvis runs `reconcile --since 3d --apply --json` daily
// (jarvis/systemd/user/jarvis-reconcile.timer).

const pipelineURLEnv = "PIPELINE_SERVICE_URL"

type reconcileFlags struct {
	since       time.Duration
	apply       bool
	json        bool
	project     string
	uid         string
	state       string
	pipelineURL string
}

// gatherMargin is how far before --since the sources are listed, so a
// session on the boundary still finds its other halves.
const gatherMargin = 24 * time.Hour

// reconcileBackend is what the command needs from the world: a snapshot and
// someone to apply fixes. Live: Firestore + Hevy + Strava + Pub/Sub + the
// pipeline RPC (reconcile_live.go). Tests: fakes.
type reconcileBackend interface {
	Snapshot(ctx context.Context, from, since, now time.Time) (reconcile.Snapshot, error)
	reconcile.Applier
}

type reconcileOpener func(ctx context.Context, f *reconcileFlags) (reconcileBackend, func(), error)

// parseSince accepts Go durations plus a day suffix: 14d, 36h, 90m.
func parseSince(v string) (time.Duration, error) {
	if strings.HasSuffix(v, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(v, "d"))
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("--since %q: want a positive number of days like 14d", v)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("--since %q: want 14d, 36h or similar", v)
	}
	return d, nil
}

func defaultStatePath() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "fitgluectl", "reconcile-state.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "fitgluectl", "reconcile-state.json")
}

func parseReconcileFlags(args []string, stderr io.Writer) (*reconcileFlags, error) {
	f := &reconcileFlags{}
	fs := flag.NewFlagSet("reconcile", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: fitgluectl reconcile [--since 14d] [--apply] [--json] [--state FILE]")
		fs.PrintDefaults()
	}
	since := fs.String("since", "14d", "window to check: 14d, 36h, …")
	fs.BoolVar(&f.apply, "apply", false, "replay workouts FitGlue never ingested and repost missing Strava uploads (default: dry run)")
	fs.BoolVar(&f.json, "json", false, "print the report as JSON")
	fs.StringVar(&f.project, "project", envOr(projectEnv, defaultProject), "GCP project (default $"+projectEnv+", else fitglue-server-prod)")
	fs.StringVar(&f.uid, "uid", os.Getenv(ownerUIDEnv), "owner's user id (default $"+ownerUIDEnv+")")
	fs.StringVar(&f.state, "state", defaultStatePath(), "where applied fixes are remembered (makes --apply idempotent)")
	fs.StringVar(&f.pipelineURL, "pipeline-url", envOr(pipelineURLEnv, "http://127.0.0.1:8104"), "pipeline service gRPC address, for reposts")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() > 0 {
		fs.Usage()
		return nil, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	d, err := parseSince(*since)
	if err != nil {
		return nil, err
	}
	f.since = d
	if f.uid == "" {
		return nil, errors.New("--uid is required (or set " + ownerUIDEnv + ")")
	}
	return f, nil
}

func reconcileCommand(ctx context.Context, args []string, stdout, stderr io.Writer, open reconcileOpener, now func() time.Time) error {
	f, err := parseReconcileFlags(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	backend, cleanup, err := open(ctx, f)
	if err != nil {
		return err
	}
	defer cleanup()

	t := now()
	since := t.Add(-f.since)
	snap, err := backend.Snapshot(ctx, since.Add(-gatherMargin), since, t)
	if err != nil {
		return err
	}
	rep := reconcile.Plan(snap)

	st, err := reconcile.LoadState(f.state)
	if err != nil {
		return err
	}
	reconcile.Apply(ctx, rep, backend, st, t, f.apply)
	if f.apply {
		if err := st.Save(f.state, t); err != nil {
			return fmt.Errorf("save state: %w", err)
		}
	}

	if f.json {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", " ")
		enc.SetEscapeHTML(false)
		return enc.Encode(rep)
	}
	loc, err := time.LoadLocation("Europe/London")
	if err != nil {
		loc = time.Local
	}
	reconcile.WriteText(stdout, rep, loc)
	return nil
}
