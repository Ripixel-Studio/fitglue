package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fitglue/server/src/go/internal/reconcile"
)

type fakeReconcile struct {
	snap  reconcile.Snapshot
	calls []string
	from  time.Time
	since time.Time
}

func (f *fakeReconcile) Snapshot(_ context.Context, from, since, now time.Time) (reconcile.Snapshot, error) {
	f.from, f.since = from, since
	s := f.snap
	s.Since, s.Now = since, now
	return s, nil
}
func (f *fakeReconcile) ReplayHevy(_ context.Context, id string) error {
	f.calls = append(f.calls, "hevy:"+id)
	return nil
}
func (f *fakeReconcile) ReplayStrava(_ context.Context, id string) error {
	f.calls = append(f.calls, "strava:"+id)
	return nil
}
func (f *fakeReconcile) RepostStrava(_ context.Context, id, mode string) error {
	f.calls = append(f.calls, "repost:"+id)
	return nil
}

var reconcileNow = time.Date(2026, 11, 3, 21, 30, 0, 0, time.UTC)

func fakeOpen(f *fakeReconcile) reconcileOpener {
	return func(context.Context, *reconcileFlags) (reconcileBackend, func(), error) { return f, func() {}, nil }
}

func missedMondayPilates() *fakeReconcile {
	st := time.Date(2026, 11, 2, 8, 0, 13, 0, time.UTC)
	return &fakeReconcile{snap: reconcile.Snapshot{
		HevyListed: true, StravaListed: true, Uploaded: map[string]string{},
		Hevy: []reconcile.HevyWorkout{{ID: "8c413d69", Title: "Tower Pilates", Start: st, End: st.Add(50 * time.Minute)}},
	}}
}

func TestReconcileCommand_JSONDryRun(t *testing.T) {
	f := missedMondayPilates()
	var out, errb bytes.Buffer
	state := filepath.Join(t.TempDir(), "state.json")
	err := reconcileCommand(context.Background(), []string{"--since", "3d", "--json", "--uid", "owner", "--state", state},
		&out, &errb, fakeOpen(f), func() time.Time { return reconcileNow })
	if err != nil {
		t.Fatal(err, errb.String())
	}
	var rep reconcile.Report
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("not json: %v\n%s", err, out.String())
	}
	if rep.Applied || rep.Counts.Fixable != 1 || len(f.calls) != 0 {
		t.Errorf("dry run: applied=%v counts=%+v calls=%v", rep.Applied, rep.Counts, f.calls)
	}
	if !f.since.Equal(reconcileNow.Add(-72*time.Hour)) || !f.from.Equal(f.since.Add(-gatherMargin)) {
		t.Errorf("window from=%v since=%v", f.from, f.since)
	}
	s := rep.Sessions[0]
	if s.Hevy.State != reconcile.Present || s.FitGlue.State != reconcile.Missing || s.Strava.State != reconcile.Missing {
		t.Errorf("cells %+v %+v %+v", s.Hevy, s.FitGlue, s.Strava)
	}
}

func TestReconcileCommand_ApplyIsIdempotent(t *testing.T) {
	f := missedMondayPilates()
	state := filepath.Join(t.TempDir(), "state.json")
	args := []string{"--since", "10d", "--apply", "--uid", "owner", "--state", state}
	var out bytes.Buffer
	if err := reconcileCommand(context.Background(), args, &out, &out, fakeOpen(f), func() time.Time { return reconcileNow }); err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.calls, ",") != "hevy:8c413d69" {
		t.Fatalf("calls %v", f.calls)
	}
	if !strings.Contains(out.String(), "replayed from Hevy") || !strings.Contains(out.String(), "(applied)") {
		t.Errorf("text output:\n%s", out.String())
	}
	out.Reset()
	if err := reconcileCommand(context.Background(), args, &out, &out, fakeOpen(f), func() time.Time { return reconcileNow.Add(time.Minute) }); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 {
		t.Fatalf("second run acted: %v", f.calls)
	}
	if !strings.Contains(out.String(), "awaiting") {
		t.Errorf("second run output:\n%s", out.String())
	}
}

func TestParseSince(t *testing.T) {
	for in, want := range map[string]time.Duration{"14d": 14 * 24 * time.Hour, "36h": 36 * time.Hour, "90m": 90 * time.Minute} {
		if got, err := parseSince(in); err != nil || got != want {
			t.Errorf("%s: %v %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "0d", "-3d", "x", "-1h"} {
		if _, err := parseSince(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestReconcileCommand_NeedsUID(t *testing.T) {
	t.Setenv(ownerUIDEnv, "")
	var out bytes.Buffer
	err := reconcileCommand(context.Background(), []string{"--since", "3d"}, &out, &out, fakeOpen(&fakeReconcile{}), time.Now)
	if err == nil || !strings.Contains(err.Error(), "--uid") {
		t.Errorf("err %v", err)
	}
}
