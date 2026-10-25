package siteexport

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestDirWriter(t *testing.T) {
	root := t.TempDir()
	w := &DirWriter{Root: root}
	ctx := context.Background()

	if err := w.Write(ctx, "activity/a.json", []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := w.Write(ctx, "roundup/week-01-2026.json", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := w.Write(ctx, "index.json", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "activity", "a.json"))
	if err != nil || string(b) != `{"a":1}` {
		t.Fatalf("read back: %q %v", b, err)
	}

	names, err := w.List(ctx, "activity/")
	if err != nil || len(names) != 1 || names[0] != "activity/a.json" {
		t.Errorf("List = %v %v", names, err)
	}
	names, _ = w.List(ctx, "")
	sort.Strings(names)
	if len(names) != 3 {
		t.Errorf("List all = %v", names)
	}
	if names, err := w.List(ctx, "missing/"); err != nil || len(names) != 0 {
		t.Errorf("List missing dir = %v %v", names, err)
	}

	if err := w.Delete(ctx, "activity/a.json"); err != nil {
		t.Fatal(err)
	}
	if err := w.Delete(ctx, "activity/a.json"); err != nil {
		t.Errorf("deleting a missing file is not an error: %v", err)
	}
	if names, _ := w.List(ctx, "activity/"); len(names) != 0 {
		t.Errorf("after delete: %v", names)
	}
}

func TestGCSWriterPaths(t *testing.T) {
	w := &GCSWriter{Bucket: "b", Prefix: "site/james"}
	if got := w.object("index.json"); got != "site/james/index.json" {
		t.Errorf("object = %q", got)
	}
	if got := w.object("activity/x.json"); got != "site/james/activity/x.json" {
		t.Errorf("object = %q", got)
	}
	if ContentType != "application/json; charset=utf-8" || CacheControl != "public, max-age=300" {
		t.Error("object metadata drifted from the contract")
	}
}
