package main

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/fitglue/server/src/go/internal/activity/siteexport"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
)

func TestParseExportSiteFlags(t *testing.T) {
	t.Setenv("SHOWCASE_ASSETS_BUCKET", "")
	cases := []struct {
		name    string
		args    []string
		env     string
		wantErr bool
		check   func(t *testing.T, f *exportSiteFlags)
	}{
		{name: "slug required", args: nil, wantErr: true},
		{name: "defaults", args: []string{"--slug", "james"}, check: func(t *testing.T, f *exportSiteFlags) {
			if f.bucket != "fitglue-server-prod-showcase-assets" || f.prefix != "site/james" || f.project != defaultProject {
				t.Errorf("defaults = %+v", f)
			}
		}},
		{name: "bucket from env", args: []string{"--slug", "james"}, env: "my-bucket", check: func(t *testing.T, f *exportSiteFlags) {
			if f.bucket != "my-bucket" {
				t.Errorf("bucket = %q", f.bucket)
			}
		}},
		{name: "explicit bucket and prefix", args: []string{"--slug", "james", "--bucket", "b", "--prefix", "/x/y/"}, env: "ignored", check: func(t *testing.T, f *exportSiteFlags) {
			if f.bucket != "b" || f.prefix != "x/y" {
				t.Errorf("flags = %+v", f)
			}
		}},
		{name: "since", args: []string{"--slug", "james", "--since", "2026-10-19T00:00:00+01:00"}, check: func(t *testing.T, f *exportSiteFlags) {
			if f.since == nil || !f.since.Equal(time.Date(2026, 10, 18, 23, 0, 0, 0, time.UTC)) || f.since.Location() != time.UTC {
				t.Errorf("since = %v", f.since)
			}
		}},
		{name: "bad since", args: []string{"--slug", "james", "--since", "yesterday"}, wantErr: true},
		{name: "since and all", args: []string{"--slug", "james", "--since", "2026-10-19T00:00:00Z", "--all"}, wantErr: true},
		{name: "activity with all", args: []string{"--slug", "james", "--activity", "x", "--all"}, wantErr: true},
		{name: "dry-run needs out", args: []string{"--slug", "james", "--dry-run"}, wantErr: true},
		{name: "out needs dry-run", args: []string{"--slug", "james", "--out", "/tmp/x"}, wantErr: true},
		{name: "bad slug", args: []string{"--slug", "ja mes"}, wantErr: true},
		{name: "unknown flag", args: []string{"--slug", "james", "--bogus"}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SHOWCASE_ASSETS_BUCKET", tc.env)
			var stderr bytes.Buffer
			f, err := parseExportSiteFlags(tc.args, &stderr)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %+v", f)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.check != nil {
				tc.check(t, f)
			}
		})
	}
}

type stubSource struct {
	profile  *pbactivity.ShowcaseProfile
	entries  []*pbactivity.ShowcaseProfileEntry
	showcase *pbactivity.ShowcasedActivity
	roundup  *pbactivity.ShowcaseRoundup
}

func (s stubSource) Profile(context.Context, string) (*pbactivity.ShowcaseProfile, error) {
	return s.profile, nil
}
func (s stubSource) Entries(context.Context, string) ([]*pbactivity.ShowcaseProfileEntry, error) {
	return s.entries, nil
}
func (s stubSource) Showcase(_ context.Context, id string) (*pbactivity.ShowcasedActivity, error) {
	if s.showcase != nil && s.showcase.ShowcaseId == id {
		return s.showcase, nil
	}
	return nil, nil
}
func (s stubSource) Roundups(context.Context, string) ([]*pbactivity.ShowcaseRoundup, error) {
	if s.roundup == nil {
		return nil, nil
	}
	return []*pbactivity.ShowcaseRoundup{s.roundup}, nil
}
func (s stubSource) Roundup(_ context.Context, _, key string) (*pbactivity.ShowcaseRoundup, error) {
	if s.roundup != nil && s.roundup.PeriodKey == key {
		return s.roundup, nil
	}
	return nil, nil
}

func newStubSource() stubSource {
	start := time.Date(2026, 10, 17, 8, 0, 0, 0, time.UTC)
	return stubSource{
		profile: &pbactivity.ShowcaseProfile{Slug: "james", UserId: "uid", Visible: true},
		entries: []*pbactivity.ShowcaseProfileEntry{{ShowcaseId: "run-2026-10-17", Title: "Run", StartTime: timestamppb.New(start), CreatedAt: timestamppb.New(start)}},
		showcase: &pbactivity.ShowcasedActivity{ShowcaseId: "run-2026-10-17", Title: "Run", StartTime: timestamppb.New(start),
			ActivityType: pbactivity.ActivityType_ACTIVITY_TYPE_RUN},
		roundup: &pbactivity.ShowcaseRoundup{PeriodKey: "week-42-2026", PeriodType: pbactivity.RoundupPeriodType_ROUNDUP_PERIOD_TYPE_WEEK,
			PeriodStart: timestamppb.New(time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)), GeneratedAt: timestamppb.New(start.Add(48 * time.Hour))},
	}
}

func TestExportSiteCommand_DryRun(t *testing.T) {
	out := filepath.Join(t.TempDir(), "site")
	var stderr bytes.Buffer
	var gotFlags *exportSiteFlags
	cleaned := false
	connect := func(_ context.Context, f *exportSiteFlags) (siteexport.Source, siteexport.Writer, func(), error) {
		gotFlags = f
		// A dry run never gets a bucket writer.
		return newStubSource(), nil, func() { cleaned = true }, nil
	}
	err := exportSiteCommand(context.Background(), []string{"--slug", "james", "--dry-run", "--out", out, "--all"}, &stderr, connect)
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr.String())
	}
	if !cleaned || gotFlags == nil || !gotFlags.dryRun {
		t.Errorf("connector not used as expected: cleaned=%v flags=%+v", cleaned, gotFlags)
	}
	for _, name := range []string{"index.json", "activity/run-2026-10-17.json", "roundup/week-42-2026.json"} {
		if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(name))); err != nil {
			t.Errorf("%s not written: %v", name, err)
		}
	}
	if !bytes.Contains(stderr.Bytes(), []byte("dry run")) || !bytes.Contains(stderr.Bytes(), []byte("done: 1 activities, 1 roundups")) {
		t.Errorf("log = %s", stderr.String())
	}
}

func TestExportSiteCommand_SingleItemsAndErrors(t *testing.T) {
	out := filepath.Join(t.TempDir(), "site")
	var stderr bytes.Buffer
	connect := func(context.Context, *exportSiteFlags) (siteexport.Source, siteexport.Writer, func(), error) {
		return newStubSource(), nil, nil, nil
	}
	if err := exportSiteCommand(context.Background(), []string{"--slug", "james", "--dry-run", "--out", out, "--activity", "run-2026-10-17"}, &stderr, connect); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "index.json")); err != nil {
		t.Error("index must be refreshed after a single export")
	}
	if err := exportSiteCommand(context.Background(), []string{"--slug", "james", "--dry-run", "--out", out, "--roundup", "week-42-2026"}, &stderr, connect); err != nil {
		t.Fatal(err)
	}
	if err := exportSiteCommand(context.Background(), []string{"--slug", "james", "--dry-run", "--out", out, "--roundup", "week-01-2020"}, &stderr, connect); !errors.Is(err, siteexport.ErrNotExportable) {
		t.Errorf("missing roundup: %v", err)
	}
	if err := exportSiteCommand(context.Background(), []string{"--slug", "james", "--dry-run", "--out", out, "--activity", "nope"}, &stderr, connect); !errors.Is(err, siteexport.ErrNotExportable) {
		t.Errorf("missing activity: %v", err)
	}

	boom := errors.New("no credentials")
	failing := func(context.Context, *exportSiteFlags) (siteexport.Source, siteexport.Writer, func(), error) {
		return nil, nil, nil, boom
	}
	if err := exportSiteCommand(context.Background(), []string{"--slug", "james"}, &stderr, failing); !errors.Is(err, boom) {
		t.Errorf("connector error must propagate: %v", err)
	}
	if err := exportSiteCommand(context.Background(), nil, &stderr, failing); err == nil {
		t.Error("flag errors must propagate")
	}
}

func TestRunExportSite_BucketWriterUsed(t *testing.T) {
	// Without --dry-run the writer the connector returned is used as-is.
	w := &recordingWriter{}
	f := &exportSiteFlags{slug: "james", bucket: "b", prefix: "site/james", concurrency: 1}
	if err := runExportSite(context.Background(), f, newStubSource(), w, log.New(&bytes.Buffer{}, "", 0)); err != nil {
		t.Fatal(err)
	}
	if len(w.names) != 3 || w.names[2] != "index.json" {
		t.Errorf("writes = %v", w.names)
	}
}

type recordingWriter struct{ names []string }

func (w *recordingWriter) Write(_ context.Context, name string, _ []byte) error {
	w.names = append(w.names, name)
	return nil
}
func (w *recordingWriter) Delete(context.Context, string) error { return nil }
func (w *recordingWriter) List(_ context.Context, prefix string) ([]string, error) {
	var out []string
	for _, n := range w.names {
		if len(n) >= len(prefix) && n[:len(prefix)] == prefix {
			out = append(out, n)
		}
	}
	return out, nil
}
