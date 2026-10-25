package sentry

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
)

func TestInit_NoDSN(t *testing.T) {
	// Empty DSN disables Sentry and returns nil.
	if err := Init(Config{}, slog.Default()); err != nil {
		t.Errorf("expected nil error for empty DSN, got %v", err)
	}
	if Enabled() {
		t.Error("Enabled() must be false after Init with an empty DSN")
	}
}

// TestNoDSN_IsSilentNoOp pins the single-user contract: with no SENTRY_DSN the
// package does nothing — captures and Error-level logs through SentryHandler
// never touch the SDK, and nothing is logged about them.
func TestNoDSN_IsSilentNoOp(t *testing.T) {
	tr := &countingTransport{}
	// Prove the transport would see events if Sentry were on...
	if err := Init(Config{DSN: "https://x@example.com/1", Transport: tr}, nil); err != nil {
		t.Fatal(err)
	}
	CaptureMessage("on", sentry.LevelError, nil, nil)
	Flush(time.Second)
	if tr.n != 1 {
		t.Fatalf("sanity: expected 1 event with DSN set, got %d", tr.n)
	}
	// ...then turn it off and check nothing more arrives.
	if err := Init(Config{}, nil); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	logger := slog.New(NewSentryHandler(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	logger.Error("boom", "error", errors.New("db down"))
	CaptureException(errors.New("direct"), nil, logger)
	CaptureMessage("msg", sentry.LevelError, nil, logger)
	if !Flush(time.Second) {
		t.Error("Flush must report success when disabled")
	}
	if tr.n != 1 {
		t.Errorf("expected no new events when disabled, transport saw %d", tr.n)
	}
	if bytes.Contains(buf.Bytes(), []byte("captured in Sentry")) {
		t.Errorf("disabled Sentry must not log capture noise, got: %s", buf.String())
	}
	if !bytes.Contains(buf.Bytes(), []byte("boom")) {
		t.Error("the wrapped handler must still receive the log record")
	}
}

type countingTransport struct{ n int }

func (t *countingTransport) Configure(_ sentry.ClientOptions) {}
func (t *countingTransport) SendEvent(_ *sentry.Event)        { t.n++ }
func (t *countingTransport) Flush(_ time.Duration) bool       { return true }

func TestCaptureException_NilError(t *testing.T) {
	// Should be a no-op and not panic.
	CaptureException(nil, nil, slog.Default())
}

func TestCaptureExceptionAndMessage(t *testing.T) {
	// Without an initialized client these are effectively no-ops, but they should
	// still execute the context-building code paths without panicking.
	CaptureException(errors.New("boom"), map[string]interface{}{"k": "v"}, slog.Default())
	CaptureMessage("hello", sentry.LevelInfo, map[string]interface{}{"k": "v"}, slog.Default())
	CaptureMessage("plain", sentry.LevelWarning, nil, nil)
}

func TestFlush(t *testing.T) {
	// Flush with no client returns quickly.
	_ = Flush(10 * time.Millisecond)
}

func TestSentryHandler(t *testing.T) {
	var buf bytes.Buffer
	base := slog.NewJSONHandler(&buf, nil)
	h := NewSentryHandler(base)

	logger := slog.New(h)

	t.Run("info passes through", func(t *testing.T) {
		buf.Reset()
		logger.Info("just info")
		if !bytes.Contains(buf.Bytes(), []byte("just info")) {
			t.Error("expected info message delegated to wrapped handler")
		}
	})

	t.Run("error with error attr", func(t *testing.T) {
		buf.Reset()
		logger.Error("failed", "error", errors.New("db down"))
		if !bytes.Contains(buf.Bytes(), []byte("failed")) {
			t.Error("expected error message delegated")
		}
	})

	t.Run("error with string error attr", func(t *testing.T) {
		buf.Reset()
		logger.Error("failed2", "error", "stringy")
		if !bytes.Contains(buf.Bytes(), []byte("failed2")) {
			t.Error("expected message delegated")
		}
	})

	t.Run("error without error attr", func(t *testing.T) {
		buf.Reset()
		logger.Error("bare error")
		if !bytes.Contains(buf.Bytes(), []byte("bare error")) {
			t.Error("expected message delegated")
		}
	})

	// Handler composition helpers.
	if h.WithGroup("g") == nil {
		t.Error("WithGroup")
	}
	if h.WithAttrs([]slog.Attr{slog.String("a", "b")}) == nil {
		t.Error("WithAttrs")
	}
	if !h.Enabled(context.Background(), slog.LevelError) {
		t.Error("Enabled should be true at error level")
	}
}

func TestRecoverAndCapture(t *testing.T) {
	// RecoverAndCapture re-panics after capturing, so we must recover again here.
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected re-panic after capture")
		}
	}()
	func() {
		defer RecoverAndCapture(slog.Default())
		panic("kaboom")
	}()
}
