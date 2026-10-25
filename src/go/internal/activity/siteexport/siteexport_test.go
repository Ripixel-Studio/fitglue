package siteexport

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
)

func testDeps(src Source, w Writer) Deps {
	return Deps{Source: src, Writer: w, Now: func() time.Time { return fixedNow }, Concurrency: 2}
}

func TestExportAll_WritesEverythingIndexLast(t *testing.T) {
	src := newFakeSource()
	w := newMemWriter()
	res, err := ExportAll(context.Background(), testDeps(src, w), "james", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Activities != 2 || res.Roundups != 1 || res.Skipped != 0 || res.Deleted != 0 {
		t.Errorf("result = %+v", res)
	}
	for _, name := range []string{"index.json", "activity/colwick-parkrun-2026-10-17.json", "activity/push-day-2026-10-13.json", "roundup/week-42-2026.json"} {
		if _, ok := w.files[name]; !ok {
			t.Errorf("missing %s (have %v)", name, w.order)
		}
	}
	if last := w.order[len(w.order)-1]; last != "index.json" {
		t.Errorf("index.json must be written last, order was %v", w.order)
	}
	idx := decode(t, w.files["index.json"])
	if n := len(idx["activities"].([]any)); n != 2 {
		t.Errorf("index lists %d activities", n)
	}
	if n := len(idx["roundups"].([]any)); n != 1 {
		t.Errorf("index lists %d roundups", n)
	}
}

func TestExportAll_IsIdempotent(t *testing.T) {
	src := newFakeSource()
	w := newMemWriter()
	deps := testDeps(src, w)
	if _, err := ExportAll(context.Background(), deps, "james", Options{}); err != nil {
		t.Fatal(err)
	}
	first := map[string][]byte{}
	for k, v := range w.files {
		first[k] = v
	}
	if _, err := ExportAll(context.Background(), deps, "james", Options{Prune: true}); err != nil {
		t.Fatal(err)
	}
	if len(w.files) != len(first) {
		t.Errorf("second run changed the file set: %d vs %d", len(w.files), len(first))
	}
	for k, v := range first {
		if string(w.files[k]) != string(v) {
			t.Errorf("%s differs between identical runs", k)
		}
	}
}

func TestExportAll_SkipsMissingAndExpired(t *testing.T) {
	src := newFakeSource()
	delete(src.showcases, "push-day-2026-10-13")
	expired := src.showcases["colwick-parkrun-2026-10-17"]
	expired.ExpiresAt = timestamppb.New(fixedNow.Add(-time.Hour))
	w := newMemWriter()
	res, err := ExportAll(context.Background(), testDeps(src, w), "james", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Activities != 0 || res.Skipped != 2 {
		t.Errorf("result = %+v", res)
	}
	idx := decode(t, w.files["index.json"])
	if n := len(idx["activities"].([]any)); n != 0 {
		t.Errorf("index must not list activities without a file, got %d", n)
	}
	if len(w.files) != 2 { // index + roundup
		t.Errorf("files = %v", w.order)
	}
}

func TestExportAll_SinceFiltersWritesButRewritesIndex(t *testing.T) {
	src := newFakeSource()
	w := newMemWriter()
	deps := testDeps(src, w)
	if _, err := ExportAll(context.Background(), deps, "james", Options{}); err != nil {
		t.Fatal(err)
	}
	w.order = nil
	since := liftStart.Add(2 * time.Hour) // after push-day was showcased, before the parkrun
	res, err := ExportAll(context.Background(), deps, "james", Options{Since: &since})
	if err != nil {
		t.Fatal(err)
	}
	if res.Activities != 1 || res.Roundups != 1 {
		t.Errorf("result = %+v", res)
	}
	want := []string{"activity/colwick-parkrun-2026-10-17.json", "roundup/week-42-2026.json", "index.json"}
	if len(w.order) != len(want) {
		t.Fatalf("writes = %v, want %v", w.order, want)
	}
	for i := range want {
		if w.order[i] != want[i] {
			t.Errorf("writes = %v, want %v", w.order, want)
		}
	}
	idx := decode(t, w.files["index.json"])
	if n := len(idx["activities"].([]any)); n != 2 {
		t.Errorf("index must still list every existing activity, got %d", n)
	}

	// Nothing newer than the roundup: only the index is rewritten.
	w.order = nil
	since = fixedNow
	if _, err := ExportAll(context.Background(), deps, "james", Options{Since: &since}); err != nil {
		t.Fatal(err)
	}
	if len(w.order) != 1 || w.order[0] != "index.json" {
		t.Errorf("writes = %v", w.order)
	}
}

func TestExportAll_PruneDeletesOrphans(t *testing.T) {
	src := newFakeSource()
	w := newMemWriter()
	_ = w.Write(context.Background(), "activity/un-showcased-2025-01-01.json", []byte("{}"))
	_ = w.Write(context.Background(), "roundup/week-01-2025.json", []byte("{}"))
	_ = w.Write(context.Background(), "activity/notes.txt", []byte("keep"))
	deps := testDeps(src, w)

	res, err := ExportAll(context.Background(), deps, "james", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Deleted != 0 || len(w.files) != 7 {
		t.Errorf("without Prune nothing is deleted: %+v %v", res, w.order)
	}
	res, err = ExportAll(context.Background(), deps, "james", Options{Prune: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Deleted != 2 {
		t.Errorf("deleted = %d", res.Deleted)
	}
	for _, gone := range []string{"activity/un-showcased-2025-01-01.json", "roundup/week-01-2025.json"} {
		if _, ok := w.files[gone]; ok {
			t.Errorf("%s should have been pruned", gone)
		}
	}
	if _, ok := w.files["activity/notes.txt"]; !ok {
		t.Error("non-JSON objects are left alone")
	}
}

func TestExportAll_Errors(t *testing.T) {
	cases := []struct {
		name string
		key  string
	}{
		{"profile", "profile"},
		{"entries", "entries"},
		{"roundups", "roundups"},
		{"showcase read", "showcase:colwick-parkrun-2026-10-17"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := newFakeSource()
			src.errs[tc.key] = errors.New("boom")
			w := newMemWriter()
			if _, err := ExportAll(context.Background(), testDeps(src, w), "james", Options{}); err == nil || !errors.Is(err, src.errs[tc.key]) {
				t.Errorf("err = %v", err)
			}
			if _, ok := w.files["index.json"]; ok {
				t.Error("index must not be written after a failure")
			}
		})
	}
	if _, err := ExportAll(context.Background(), testDeps(newFakeSource(), newMemWriter()), "nobody", Options{}); err == nil {
		t.Error("unknown slug must fail")
	}
}

func TestExportActivity_And_RefreshIndex(t *testing.T) {
	src := newFakeSource()
	w := newMemWriter()
	deps := testDeps(src, w)

	if err := ExportActivity(context.Background(), deps, "james", "push-day-2026-10-13"); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.files["activity/push-day-2026-10-13.json"]; !ok || len(w.files) != 1 {
		t.Errorf("files = %v", w.order)
	}
	if err := ExportActivity(context.Background(), deps, "james", "nope"); !errors.Is(err, ErrNotExportable) {
		t.Errorf("unknown id: %v", err)
	}
	delete(src.showcases, "colwick-parkrun-2026-10-17")
	if err := ExportActivity(context.Background(), deps, "james", "colwick-parkrun-2026-10-17"); !errors.Is(err, ErrNotExportable) {
		t.Errorf("missing showcase doc: %v", err)
	}

	if err := ExportRoundup(context.Background(), deps, "james", "week-42-2026"); err != nil {
		t.Fatal(err)
	}
	if err := ExportRoundup(context.Background(), deps, "james", "week-01-2020"); !errors.Is(err, ErrNotExportable) {
		t.Errorf("missing roundup: %v", err)
	}

	if err := RefreshIndex(context.Background(), deps, "james"); err != nil {
		t.Fatal(err)
	}
	idx := decode(t, w.files["index.json"])
	acts := idx["activities"].([]any)
	if len(acts) != 1 || acts[0].(map[string]any)["slug"] != "push-day-2026-10-13" {
		t.Errorf("index lists only activities whose file exists: %v", acts)
	}
	if n := len(idx["roundups"].([]any)); n != 1 {
		t.Errorf("roundups = %d", n)
	}
}

func TestDepsDefaults(t *testing.T) {
	var d Deps
	if d.concurrency() != 8 {
		t.Errorf("default concurrency = %d", d.concurrency())
	}
	if time.Since(d.now()) > time.Minute {
		t.Error("default clock is now")
	}
	d.logf("discarded %d", 1)
}
