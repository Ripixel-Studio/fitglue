package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// A caller that hung up is answered 499 (client closed request), not 500: the
// request log then says what happened instead of reporting a server fault.
// A real failure is still a 5xx.
func TestWriteError_StatusCodes(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"gRPC Canceled from a domain service", status.Error(codes.Canceled, "failed to list showcases"), StatusClientClosedRequest},
		{"local context.Canceled", fmt.Errorf("read: %w", context.Canceled), StatusClientClosedRequest},
		{"Internal", status.Error(codes.Internal, "failed to list showcases"), http.StatusInternalServerError},
		{"DeadlineExceeded", status.Error(codes.DeadlineExceeded, "failed to list showcases"), http.StatusGatewayTimeout},
		{"Unavailable", status.Error(codes.Unavailable, "failed to list showcases"), http.StatusServiceUnavailable},
		{"plain error", errors.New("boom"), http.StatusInternalServerError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			WriteError(rec, c.err)
			if rec.Code != c.want {
				t.Fatalf("status = %d, want %d", rec.Code, c.want)
			}
		})
	}
}
