package claude

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func fake(t *testing.T, body string) CLI {
	t.Helper()
	p := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return CLI{Bin: p}
}

func TestArgsExact(t *testing.T) {
	got := Args(Request{Model: "haiku", System: "sys"})
	want := []string{"-p", "--model", "haiku", "--system-prompt", "sys", "--tools", "", "--setting-sources", "",
		"--strict-mcp-config", "--no-session-persistence", "--disable-slash-commands",
		"--output-format", "text", "--effort", "low"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q", got)
	}
}

func TestPromptOnStdin(t *testing.T) {
	out, err := fake(t, "cat").Complete(context.Background(), Request{Prompt: "hello there"})
	if err != nil || out != "hello there" {
		t.Fatalf("%q %v", out, err)
	}
}

func TestEnvPasslist(t *testing.T) {
	t.Setenv("FITGLUE_API_TOKEN", "x")
	out, err := fake(t, "env").Complete(context.Background(), Request{Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "FITGLUE_API_TOKEN") {
		t.Fatal("secret leaked into env")
	}
}

func TestRunTimeoutKillsProcess(t *testing.T) {
	start := time.Now()
	_, err := fake(t, "sleep 10").Complete(context.Background(), Request{Prompt: "p", Timeout: 200 * time.Millisecond})
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("err=%v after %v", err, time.Since(start))
	}
}

func TestRunNonZeroExit(t *testing.T) {
	_, err := fake(t, "echo boom >&2; exit 3").Complete(context.Background(), Request{Prompt: "p"})
	if err == nil || !strings.Contains(err.Error(), "exit status 3") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("%v", err)
	}
}

func TestTrimsOutput(t *testing.T) {
	out, err := fake(t, "printf '  \\n hi \\n\\n'").Complete(context.Background(), Request{Prompt: "p"})
	if err != nil || out != "hi" {
		t.Fatalf("%q %v", out, err)
	}
	_, err = fake(t, "printf '  \\n'").Complete(context.Background(), Request{Prompt: "p"})
	if err == nil || !strings.Contains(err.Error(), "claude: empty answer") {
		t.Fatalf("%v", err)
	}
}

func TestSpentWindowIsErrLimited(t *testing.T) {
	_, err := fake(t, "echo 'Claude usage limit reached. Your limit will reset at 5pm' ; exit 1").Complete(context.Background(), Request{Prompt: "p"})
	if !errors.Is(err, ErrLimited) {
		t.Fatalf("%v", err)
	}
}
