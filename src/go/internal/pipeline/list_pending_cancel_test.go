package pipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/fitglue/server/src/go/internal/infra"
	pbsvc "github.com/fitglue/server/src/go/pkg/types/pb/services/pipeline"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type levelsLogger struct{ levels *[]string }

func (l levelsLogger) add(s string)                          { *l.levels = append(*l.levels, s) }
func (l levelsLogger) Debug(context.Context, string, ...any) { l.add("DEBUG") }
func (l levelsLogger) Info(context.Context, string, ...any)  { l.add("INFO") }
func (l levelsLogger) Warn(context.Context, string, ...any)  { l.add("WARN") }
func (l levelsLogger) Error(context.Context, string, ...any) { l.add("ERROR") }
func (l levelsLogger) With(...any) infra.Logger              { return l }

// The Jarvis gateway polls pending inputs every 60 s and gives up after 20 s.
// While the box was offline (1–3 Nov 2026) every poll was hung up on and this
// handler logged ~2,600 Errors and answered Internal. A hang-up is Canceled at
// Info; a real failure is still an Error.
func TestListPendingInputs_CallerGoneVersusRealFailure(t *testing.T) {
	cases := []struct {
		name      string
		ctx       func() context.Context
		storeErr  error
		wantCode  codes.Code
		wantLevel string
	}{
		{"caller hung up", func() context.Context {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx
		}, context.Canceled, codes.Canceled, "INFO"},
		{"store failure", context.Background, errors.New("permission denied"), codes.Internal, "ERROR"},
		{"firestore unreachable", context.Background, status.Error(codes.Unavailable, "network is unreachable"), codes.Unavailable, "ERROR"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := NewMockStore()
			var sawDeadline bool
			store.ListPendingErr = func(ctx context.Context) error {
				_, sawDeadline = ctx.Deadline()
				return c.storeErr
			}
			var levels []string
			svc := NewService(store, &MockPublisher{}, &MockBlobStore{}, levelsLogger{&levels}, nil)
			_, err := svc.ListPendingInputs(c.ctx(), &pbsvc.ListPendingInputsRequest{UserId: "u"})
			if status.Code(err) != c.wantCode {
				t.Fatalf("code = %v, want %v", status.Code(err), c.wantCode)
			}
			if len(levels) != 1 || levels[0] != c.wantLevel {
				t.Fatalf("levels = %v, want [%s]", levels, c.wantLevel)
			}
			if !sawDeadline {
				t.Fatalf("the store read must carry the read ceiling")
			}
		})
	}
}
