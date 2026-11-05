package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultSignalFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got := defaultSignalFile(); got != "" {
		t.Errorf("no ~/.jarvis/home: no signal file, got %q", got)
	}
	if err := os.MkdirAll(filepath.Join(home, ".jarvis", "home"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, want := defaultSignalFile(), filepath.Join(home, ".jarvis", "home", "site-changed"); got != want {
		t.Errorf("got %q, want %q (what jarvis-site-rebuild.path watches)", got, want)
	}
}
