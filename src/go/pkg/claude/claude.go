// Package claude calls the box's `claude` CLI as a tool-less model. It replaces
// Gemini/Vertex for FitGlue's AI text. The usage window is shared with Jarvis, so
// a spent window surfaces as ErrLimited and the caller skips its AI line; there
// is no retry here.
package claude

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

const (
	ModelHaiku  = "haiku"
	ModelSonnet = "sonnet"

	DefaultTimeout = 90 * time.Second
	maxStderr      = 200
)

// ErrLimited means the shared Claude usage window is spent. Skip, do not retry.
var ErrLimited = errors.New("claude: usage limit reached")

// Same phrases as Jarvis gateway/usagegate.py LIMIT_TEXT.
var limitText = regexp.MustCompile(`(?i)usage limit reached|hit your (?:session|usage|weekly) limit|limit will reset|out of (?:extra )?usage|session limit .{0,20}resets`)

type Request struct {
	Model, System, Prompt string
	Timeout               time.Duration
}

type Completer interface {
	Complete(ctx context.Context, r Request) (string, error)
}

type CLI struct{ Bin string }

// at most 2 claude processes at once
var sem = make(chan struct{}, 2)

func Default() Completer {
	bin := os.Getenv("CLAUDE_BIN")
	if bin == "" {
		bin = filepath.Join(os.Getenv("HOME"), ".local", "bin", "claude")
	}
	return CLI{Bin: bin}
}

func Args(r Request) []string {
	return []string{"-p", "--model", r.Model, "--system-prompt", r.System, "--tools", "",
		"--setting-sources", "", "--strict-mcp-config", "--no-session-persistence",
		"--disable-slash-commands", "--output-format", "text", "--effort", "low"}
}

func passEnv() []string {
	var env []string
	for _, k := range []string{"PATH", "HOME", "USER", "LANG", "TZ"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

func (c CLI) Complete(ctx context.Context, r Request) (string, error) {
	to := r.Timeout
	if to <= 0 {
		to = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, to)
	defer cancel()

	select {
	case sem <- struct{}{}:
		defer func() { <-sem }()
	case <-ctx.Done():
		return "", fmt.Errorf("claude: %w", ctx.Err())
	}

	cmd := exec.CommandContext(ctx, c.Bin, Args(r)...)
	cmd.Dir = os.TempDir()
	cmd.Env = passEnv()
	cmd.Stdin = strings.NewReader(r.Prompt)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 500 * time.Millisecond
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	if err := cmd.Run(); err != nil {
		// The CLI prints the limit notice on stdout; inspect it, never log it.
		if limitText.Match(stdout.Bytes()) || limitText.Match(stderr.Bytes()) {
			return "", ErrLimited
		}
		if ctx.Err() != nil {
			return "", fmt.Errorf("claude: %w (%v)", ctx.Err(), err)
		}
		se := stderr.Bytes()
		if len(se) > maxStderr {
			se = se[:maxStderr]
		}
		return "", fmt.Errorf("claude: %w: %s", err, strings.TrimSpace(string(se)))
	}
	out := strings.TrimSpace(stdout.String())
	if out == "" {
		return "", errors.New("claude: empty answer")
	}
	return out, nil
}
