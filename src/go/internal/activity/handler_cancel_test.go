package activity

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	pbpipeline "github.com/fitglue/server/src/go/pkg/types/pb/models/pipeline"
	pbsvc "github.com/fitglue/server/src/go/pkg/types/pb/services/activity"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func cancelledCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func noErrorLogged(t *testing.T, l *recordingLogger) {
	t.Helper()
	for _, e := range *l.entries {
		if e.level == "ERROR" {
			t.Fatalf("a hang-up must not log at Error (Sentry); got %v", *l.entries)
		}
	}
}

// The Jarvis gateway poller hung up on ListShowcases/ListActivities when the
// box was offline (1–3 Nov): the handler logged Error and answered Internal (a
// 500). A hang-up is Canceled, logged at Info.
func TestListShowcases_CallerGoneIsCanceledNotError(t *testing.T) {
	store := &MockActivityStore{
		ListShowcasesFunc: func(ctx context.Context, _ string) ([]*pbactivity.ShowcaseProfileEntry, error) {
			return nil, ctx.Err()
		},
	}
	l := newRecordingLogger()
	_, err := svcWithLogger(store, &MockBlobStore{}, l).ListShowcases(cancelledCtx(), &pbsvc.ListShowcasesRequest{UserId: "u"})
	if status.Code(err) != codes.Canceled {
		t.Fatalf("code = %v, want Canceled", status.Code(err))
	}
	noErrorLogged(t, l)
}

func TestListActivities_CallerGoneIsCanceledNotError(t *testing.T) {
	store := &MockActivityStore{
		ListPipelineRunsFunc: func(ctx context.Context, _ string, _ int32, _ string) ([]*pbpipeline.PipelineRun, string, error) {
			return nil, "", status.Error(codes.Canceled, "context canceled")
		},
	}
	l := newRecordingLogger()
	_, err := svcWithLogger(store, &MockBlobStore{}, l).ListActivities(cancelledCtx(), &pbsvc.ListActivitiesRequest{UserId: "u"})
	if status.Code(err) != codes.Canceled {
		t.Fatalf("code = %v, want Canceled", status.Code(err))
	}
	noErrorLogged(t, l)
}

// A real store failure is still an Error and a 500.
func TestListActivities_RealFailureStillError(t *testing.T) {
	store := &MockActivityStore{
		ListPipelineRunsFunc: func(context.Context, string, int32, string) ([]*pbpipeline.PipelineRun, string, error) {
			return nil, "", errors.New("permission denied")
		},
	}
	l := newRecordingLogger()
	_, err := svcWithLogger(store, &MockBlobStore{}, l).ListActivities(context.Background(), &pbsvc.ListActivitiesRequest{UserId: "u"})
	if status.Code(err) != codes.Internal {
		t.Fatalf("code = %v, want Internal", status.Code(err))
	}
	if lv := l.levelsFor("failed to list activities"); len(lv) != 1 || lv[0] != "ERROR" {
		t.Fatalf("want one ERROR, got %v", *l.entries)
	}
}

// Every handler read is bounded, so a store that never answers (the box
// offline) ends DeadlineExceeded, an Error, instead of hanging until the
// caller gives up.
func TestListShowcases_StoreStallIsDeadlineExceededError(t *testing.T) {
	var sawDeadline bool
	store := &MockActivityStore{
		ListShowcasesFunc: func(ctx context.Context, _ string) ([]*pbactivity.ShowcaseProfileEntry, error) {
			_, sawDeadline = ctx.Deadline()
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	// The parent's deadline is already past; the handler's own ceiling sits under it.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	l := newRecordingLogger()
	_, err := svcWithLogger(store, &MockBlobStore{}, l).ListShowcases(ctx, &pbsvc.ListShowcasesRequest{UserId: "u"})
	if !sawDeadline {
		t.Fatalf("the store read must carry a deadline")
	}
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("code = %v, want DeadlineExceeded", status.Code(err))
	}
	if lv := l.levelsFor("failed to list showcases"); len(lv) != 1 || lv[0] != "ERROR" {
		t.Fatalf("a stall is a real error; got %v", *l.entries)
	}
}

// SERVER-V: the caller hung up while GetShowcase read the activity data blob
// from GCS; the read failed with a wrapped context.Canceled, logged at Error.
func TestGetShowcase_BlobReadCallerGoneIsNotError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	store := &MockActivityStore{
		GetShowcaseFunc: func(context.Context, string, string) (*pbactivity.ShowcasedActivity, error) {
			return &pbactivity.ShowcasedActivity{ShowcaseId: "tower-pilates-2026-11-02", ActivityDataUri: "gs://b/x.json"}, nil
		},
	}
	blob := &MockBlobStore{
		GetFunc: func(context.Context, string, string) ([]byte, error) {
			cancel() // the client hangs up mid-read
			return nil, &url.Error{Op: "Get", URL: "https://storage.googleapis.com/b/x.json", Err: context.Canceled}
		},
	}
	l := newRecordingLogger()
	_, _ = svcWithLogger(store, blob, l).GetShowcase(ctx, &pbsvc.GetShowcaseRequest{UserId: "u", ShowcaseId: "tower-pilates-2026-11-02"})
	noErrorLogged(t, l)
	if lv := l.levelsFor("caller went away"); len(lv) != 1 || lv[0] != "INFO" {
		t.Fatalf("want one INFO for the hang-up, got %v", *l.entries)
	}
}

func TestGetActivity_PayloadReadCallerGoneIsCanceled(t *testing.T) {
	store := &MockActivityStore{
		GetPipelineRunFunc: func(context.Context, string, string) (*pbpipeline.PipelineRun, error) {
			return &pbpipeline.PipelineRun{Id: "r", EnrichedEventUri: "gs://b/e.json"}, nil
		},
	}
	blob := &MockBlobStore{
		GetFunc: func(ctx context.Context, _, _ string) ([]byte, error) { return nil, ctx.Err() },
	}
	l := newRecordingLogger()
	_, err := svcWithLogger(store, blob, l).GetActivity(cancelledCtx(), &pbsvc.GetActivityRequest{UserId: "u", ActivityId: "r"})
	if status.Code(err) != codes.Canceled {
		t.Fatalf("code = %v, want Canceled", status.Code(err))
	}
	noErrorLogged(t, l)
}
