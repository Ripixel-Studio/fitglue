package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fitglue/server/src/go/internal/infra"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	activitypb "github.com/fitglue/server/src/go/pkg/types/pb/services/activity"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type pubActivityClient struct {
	activitypb.ActivityServiceClient
	err error
}

func (m *pubActivityClient) GetPublicShowcase(_ context.Context, _ *activitypb.GetPublicShowcaseRequest, _ ...grpc.CallOption) (*pbactivity.ShowcasedActivity, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &pbactivity.ShowcasedActivity{}, nil
}
func (m *pubActivityClient) GetPublicShowcaseProfile(_ context.Context, _ *activitypb.GetPublicShowcaseProfileRequest, _ ...grpc.CallOption) (*activitypb.GetPublicShowcaseProfileResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &activitypb.GetPublicShowcaseProfileResponse{}, nil
}
func (m *pubActivityClient) GetPublicRoundup(_ context.Context, _ *activitypb.GetPublicRoundupRequest, _ ...grpc.CallOption) (*pbactivity.ShowcaseRoundup, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &pbactivity.ShowcaseRoundup{}, nil
}
func (m *pubActivityClient) GetRecentPublicRoundups(_ context.Context, _ *activitypb.GetRecentPublicRoundupsRequest, _ ...grpc.CallOption) (*activitypb.GetRecentPublicRoundupsResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &activitypb.GetRecentPublicRoundupsResponse{}, nil
}

func pubServer(act *pubActivityClient) *APIServer {
	return &APIServer{logger: infra.NewLogger(), activitySvc: act}
}

func TestPublicShowcaseHandlers(t *testing.T) {
	okAct := &pubActivityClient{}
	errAct := &pubActivityClient{err: status.Error(codes.NotFound, "nope")}

	cases := []struct {
		name string
		call func(s *APIServer, w http.ResponseWriter, r *http.Request)
	}{
		{"getShowcase", (*APIServer).handleGetPublicShowcase},
		{"getProfile", (*APIServer).handleGetPublicShowcaseProfile},
		{"getRoundup", (*APIServer).handleGetPublicRoundup},
		{"recentRoundups", (*APIServer).handleGetRecentPublicRoundups},
	}
	for _, c := range cases {
		t.Run(c.name+"_ok", func(t *testing.T) {
			s := pubServer(okAct)
			w := httptest.NewRecorder()
			c.call(s, w, httptest.NewRequest(http.MethodGet, "/x", nil))
			if w.Code != http.StatusOK {
				t.Errorf("expected 200, got %d", w.Code)
			}
		})
		t.Run(c.name+"_err", func(t *testing.T) {
			s := pubServer(errAct)
			w := httptest.NewRecorder()
			c.call(s, w, httptest.NewRequest(http.MethodGet, "/x", nil))
			if w.Code != http.StatusNotFound {
				t.Errorf("expected 404, got %d", w.Code)
			}
		})
	}
}
