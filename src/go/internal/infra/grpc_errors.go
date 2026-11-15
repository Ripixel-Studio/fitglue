package infra

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ReadCeiling bounds one owner-API read (a Firestore list or get, a GCS blob)
// inside a gRPC handler. When the box loses its route to Google, Firestore and
// GCS calls do not fail: they wait. Without a ceiling the handler waited until
// the caller gave up (the Jarvis gateway's poller, 20 s), so a network outage
// showed up only as thousands of "context canceled" lines (1–3 Nov 2026, the
// box offline for ~39 h). With it the handler answers first, with a real
// DeadlineExceeded the caller and Sentry can see. Normal reads are well under
// it (since 8 Nov: ListShowcases p99 5.6 s, everything else p99 under 3 s).
const ReadCeiling = 12 * time.Second

// WithReadCeiling returns ctx bounded by ReadCeiling. The caller's own
// cancellation still propagates (and still reads as context.Canceled).
func WithReadCeiling(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, ReadCeiling)
}

// CallerGone reports whether err is the caller hanging up: its context was
// cancelled (a client that closed the connection, a process stopping), or a
// downstream gRPC call that ended Canceled for the same reason. A timeout
// (DeadlineExceeded) is not a hang-up: it is reported as an error.
func CallerGone(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || status.Code(err) == codes.Canceled {
		return true
	}
	return errors.Is(ctx.Err(), context.Canceled)
}

// HandlerError turns a failed read in a gRPC handler into the status it
// returns, logging it at the level it deserves:
//
//   - the caller hung up: one Info line, codes.Canceled. Not a server fault,
//     and the caller is no longer listening for the answer.
//   - the read ran out of time (ReadCeiling): Error, codes.DeadlineExceeded.
//   - Firestore/GCS unreachable: Error, codes.Unavailable.
//   - anything else: Error, codes.Internal.
//
// msg is both the log message and the status message ("failed to list
// showcases"); args are extra log attributes.
func HandlerError(ctx context.Context, logger Logger, err error, msg string, args ...any) error {
	if CallerGone(ctx, err) {
		logger.Info(ctx, "caller went away: "+msg, append([]any{"error", err.Error()}, args...)...)
		return status.Error(codes.Canceled, msg)
	}
	logger.Error(ctx, msg, append([]any{"error", err}, args...)...)
	switch {
	case errors.Is(err, context.DeadlineExceeded) || status.Code(err) == codes.DeadlineExceeded:
		return status.Error(codes.DeadlineExceeded, msg)
	case status.Code(err) == codes.Unavailable:
		return status.Error(codes.Unavailable, msg)
	default:
		return status.Error(codes.Internal, msg)
	}
}
