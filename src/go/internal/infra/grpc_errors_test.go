package infra

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type levelLogger struct{ levels []string }

func (l *levelLogger) Debug(context.Context, string, ...any) { l.levels = append(l.levels, "DEBUG") }
func (l *levelLogger) Info(context.Context, string, ...any)  { l.levels = append(l.levels, "INFO") }
func (l *levelLogger) Warn(context.Context, string, ...any)  { l.levels = append(l.levels, "WARN") }
func (l *levelLogger) Error(context.Context, string, ...any) { l.levels = append(l.levels, "ERROR") }
func (l *levelLogger) With(...any) Logger                    { return l }

func cancelled() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// The caller hanging up is not a server fault: Info, codes.Canceled, never
// Error (which is what reaches Sentry). Covers each shape a hang-up arrives in:
// a bare context.Canceled (Firestore), a wrapped one (GCS's *url.Error, the
// SERVER-V event), and a downstream gRPC Canceled status.
func TestHandlerError_CallerGoneIsInfoAndCanceled(t *testing.T) {
	cases := map[string]struct {
		ctx context.Context
		err error
	}{
		"context.Canceled":           {cancelled(), context.Canceled},
		"wrapped in a url.Error":     {cancelled(), &url.Error{Op: "Get", URL: "https://storage.googleapis.com/x", Err: context.Canceled}},
		"gRPC Canceled status":       {context.Background(), status.Error(codes.Canceled, "context canceled")},
		"store error, ctx cancelled": {cancelled(), errors.New("rpc error: code = Canceled desc = context canceled")},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			l := &levelLogger{}
			got := HandlerError(c.ctx, l, c.err, "failed to list showcases")
			if status.Code(got) != codes.Canceled {
				t.Fatalf("code = %v, want Canceled", status.Code(got))
			}
			if len(l.levels) != 1 || l.levels[0] != "INFO" {
				t.Fatalf("levels = %v, want one INFO", l.levels)
			}
		})
	}
}

// Real failures stay errors, with the code that says what happened.
func TestHandlerError_RealErrorsStayErrors(t *testing.T) {
	expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	cases := map[string]struct {
		ctx  context.Context
		err  error
		want codes.Code
	}{
		"internal":              {context.Background(), errors.New("permission denied"), codes.Internal},
		"read ceiling hit":      {expired, context.DeadlineExceeded, codes.DeadlineExceeded},
		"wrapped deadline":      {expired, fmt.Errorf("list: %w", context.DeadlineExceeded), codes.DeadlineExceeded},
		"firestore deadline":    {context.Background(), status.Error(codes.DeadlineExceeded, "deadline"), codes.DeadlineExceeded},
		"firestore unreachable": {context.Background(), status.Error(codes.Unavailable, "dial tcp: network is unreachable"), codes.Unavailable},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			l := &levelLogger{}
			got := HandlerError(c.ctx, l, c.err, "failed to list activities")
			if status.Code(got) != c.want {
				t.Fatalf("code = %v, want %v", status.Code(got), c.want)
			}
			if len(l.levels) != 1 || l.levels[0] != "ERROR" {
				t.Fatalf("levels = %v, want one ERROR", l.levels)
			}
		})
	}
}

func TestWithReadCeiling(t *testing.T) {
	ctx, cancel := WithReadCeiling(context.Background())
	defer cancel()
	dl, ok := ctx.Deadline()
	if !ok || time.Until(dl) > ReadCeiling || time.Until(dl) < ReadCeiling-time.Second {
		t.Fatalf("deadline %v, want ~%v from now", dl, ReadCeiling)
	}
	// Under the Jarvis gateway poller's 20 s, so the server answers first.
	if ReadCeiling >= 20*time.Second {
		t.Fatalf("ReadCeiling %v must stay under the poller's 20 s", ReadCeiling)
	}
	// The caller's own hang-up still reads as Canceled through it.
	parent, stop := context.WithCancel(context.Background())
	child, cancel2 := WithReadCeiling(parent)
	defer cancel2()
	stop()
	if !CallerGone(child, child.Err()) {
		t.Fatalf("a cancelled parent must read as the caller gone")
	}
}

type attrLogger struct {
	levelLogger
	attrs map[string][]any
}

func (l *attrLogger) Info(_ context.Context, msg string, args ...any) {
	l.attrs[msg] = args
}

// The request log names the caller (user agent, remote address) and marks a
// request whose client went away, so a hang-up can be traced to its process.
func TestLoggingMiddleware_NamesCallerAndHangUp(t *testing.T) {
	l := &attrLogger{attrs: map[string][]any{}}
	ctx, cancel := context.WithCancel(context.Background())
	h := LoggingMiddleware(l, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cancel() // the client hangs up while we work
		w.WriteHeader(499)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v2/users/me/showcases", nil).WithContext(ctx)
	req.Header.Set("User-Agent", "Python/3.14 aiohttp/3.12")
	req.RemoteAddr = "127.0.0.1:51234"
	h.ServeHTTP(httptest.NewRecorder(), req)

	got := fmt.Sprint(l.attrs["HTTP Request Received"])
	for _, want := range []string{"user_agent Python/3.14 aiohttp/3.12", "remote_addr 127.0.0.1:51234"} {
		if !strings.Contains(got, want) {
			t.Fatalf("received log %q lacks %q", got, want)
		}
	}
	if fin := fmt.Sprint(l.attrs["Request Finished"]); !strings.Contains(fin, "client_gone true") {
		t.Fatalf("finished log %q lacks client_gone", fin)
	}
}
