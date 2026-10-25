package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/fitglue/server/src/go/internal/infra"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	pbpipeline "github.com/fitglue/server/src/go/pkg/types/pb/models/pipeline"
	pbuser "github.com/fitglue/server/src/go/pkg/types/pb/models/user"
	activitypb "github.com/fitglue/server/src/go/pkg/types/pb/services/activity"
	pipelinepb "github.com/fitglue/server/src/go/pkg/types/pb/services/pipeline"
	userpb "github.com/fitglue/server/src/go/pkg/types/pb/services/user"
)

// withURLParam attaches a single chi URL parameter to the request context so
// handlers that read chi.URLParam(r, key) work under direct invocation.
func withURLParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// =============================================================
// users.go — connection actions
// =============================================================

func TestHandleConnectionActions_NoToken(t *testing.T) {
	s := serverWithDeps(&APIServer{userService: &mockUserServiceClient{}, publisher: &mockPublisher{}})
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"action":"sync"}`))
	w := httptest.NewRecorder()
	s.handleConnectionActions(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestHandleConnectionActions_InvalidJSON(t *testing.T) {
	s := serverWithDeps(&APIServer{userService: &mockUserServiceClient{}, publisher: &mockPublisher{}})
	r := withToken(httptest.NewRequest(http.MethodPost, "/", strings.NewReader("bad")), "u1")
	w := httptest.NewRecorder()
	s.handleConnectionActions(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleConnectionActions_UnknownAction(t *testing.T) {
	s := serverWithDeps(&APIServer{userService: &mockUserServiceClient{}, publisher: &mockPublisher{}})
	r := withToken(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"action":"frobnicate"}`)), "u1")
	w := httptest.NewRecorder()
	s.handleConnectionActions(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleConnectionActions_SyncSuccess(t *testing.T) {
	s := serverWithDeps(&APIServer{userService: &mockUserServiceClient{}, publisher: &mockPublisher{}})
	r := withToken(withURLParam(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"action":"sync"}`)), "provider", "strava"), "u1")
	w := httptest.NewRecorder()
	s.handleConnectionActions(w, r)
	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d", w.Code)
	}
}

func TestHandleConnectionActions_SyncIntegrationNotFound(t *testing.T) {
	svc := &mockUserServiceClient{
		getIntegration: func(_ context.Context, _ *userpb.GetIntegrationRequest, _ ...grpc.CallOption) (*userpb.GetIntegrationResponse, error) {
			return nil, status.Error(codes.NotFound, "no integration")
		},
	}
	s := serverWithDeps(&APIServer{userService: svc, publisher: &mockPublisher{}})
	r := withToken(withURLParam(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"action":"sync"}`)), "provider", "strava"), "u1")
	w := httptest.NewRecorder()
	s.handleConnectionActions(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHandleConnectionActions_ClearSuccess(t *testing.T) {
	s := serverWithDeps(&APIServer{userService: &mockUserServiceClient{}, publisher: &mockPublisher{}})
	r := withToken(withURLParam(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"action":"clear"}`)), "provider", "strava"), "u1")
	w := httptest.NewRecorder()
	s.handleConnectionActions(w, r)
	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", w.Code)
	}
}

func TestHandleConnectionActions_ClearError(t *testing.T) {
	svc := &mockUserServiceClient{
		deleteIntegration: func(_ context.Context, _ *userpb.DeleteIntegrationRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
			return nil, status.Error(codes.Internal, "boom")
		},
	}
	s := serverWithDeps(&APIServer{userService: svc, publisher: &mockPublisher{}})
	r := withToken(withURLParam(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"action":"clear"}`)), "provider", "strava"), "u1")
	w := httptest.NewRecorder()
	s.handleConnectionActions(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

// =============================================================
// users.go — booster data
// =============================================================

func TestHandleGetBoosterData(t *testing.T) {
	s := serverWithDeps(&APIServer{userService: &mockUserServiceClient{}})
	t.Run("no token", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.handleGetBoosterData(w, httptest.NewRequest(http.MethodGet, "/", nil))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})
	t.Run("success", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.handleGetBoosterData(w, withToken(httptest.NewRequest(http.MethodGet, "/", nil), "u1"))
		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", w.Code)
		}
	})
}

func TestHandleSetBoosterData(t *testing.T) {
	s := serverWithDeps(&APIServer{userService: &mockUserServiceClient{}})
	t.Run("no token", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.handleSetBoosterData(w, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{}`)))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})
	t.Run("invalid body", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.handleSetBoosterData(w, withToken(httptest.NewRequest(http.MethodPut, "/", strings.NewReader("not json")), "u1"))
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", w.Code)
		}
	})
	t.Run("success", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.handleSetBoosterData(w, withToken(httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"foo":"bar"}`)), "u1"))
		if w.Code != http.StatusNoContent {
			t.Errorf("expected 204, got %d", w.Code)
		}
	})
}

func TestHandleDeleteBoosterData(t *testing.T) {
	s := serverWithDeps(&APIServer{userService: &mockUserServiceClient{}})
	t.Run("no token", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.handleDeleteBoosterData(w, httptest.NewRequest(http.MethodDelete, "/", nil))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})
	t.Run("success", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.handleDeleteBoosterData(w, withToken(httptest.NewRequest(http.MethodDelete, "/", nil), "u1"))
		if w.Code != http.StatusNoContent {
			t.Errorf("expected 204, got %d", w.Code)
		}
	})
}

// =============================================================
// =============================================================

// =============================================================
// users.go — personal records
// =============================================================

func TestHandleListPersonalRecords(t *testing.T) {
	s := serverWithDeps(&APIServer{userService: &mockUserServiceClient{}})
	w := httptest.NewRecorder()
	s.handleListPersonalRecords(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
	w = httptest.NewRecorder()
	s.handleListPersonalRecords(w, withToken(httptest.NewRequest(http.MethodGet, "/", nil), "u1"))
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleSetPersonalRecord(t *testing.T) {
	s := serverWithDeps(&APIServer{userService: &mockUserServiceClient{}})
	t.Run("no token", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.handleSetPersonalRecord(w, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{}`)))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})
	t.Run("invalid body", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.handleSetPersonalRecord(w, withToken(httptest.NewRequest(http.MethodPut, "/", strings.NewReader("bad")), "u1"))
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", w.Code)
		}
	})
	t.Run("success", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.handleSetPersonalRecord(w, withToken(httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{}`)), "u1"))
		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", w.Code)
		}
	})
}

func TestHandleDeletePersonalRecord(t *testing.T) {
	s := serverWithDeps(&APIServer{userService: &mockUserServiceClient{}})
	w := httptest.NewRecorder()
	s.handleDeletePersonalRecord(w, httptest.NewRequest(http.MethodDelete, "/", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
	w = httptest.NewRecorder()
	s.handleDeletePersonalRecord(w, withToken(httptest.NewRequest(http.MethodDelete, "/", nil), "u1"))
	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", w.Code)
	}
}

// =============================================================
// users.go — plugin defaults
// =============================================================

func TestHandleListPluginDefaults(t *testing.T) {
	s := serverWithDeps(&APIServer{userService: &mockUserServiceClient{}})
	w := httptest.NewRecorder()
	s.handleListPluginDefaults(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
	w = httptest.NewRecorder()
	s.handleListPluginDefaults(w, withToken(httptest.NewRequest(http.MethodGet, "/", nil), "u1"))
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleSetPluginDefaults(t *testing.T) {
	s := serverWithDeps(&APIServer{userService: &mockUserServiceClient{}})
	t.Run("no token", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.handleSetPluginDefaults(w, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{}`)))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})
	t.Run("invalid body", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.handleSetPluginDefaults(w, withToken(httptest.NewRequest(http.MethodPut, "/", strings.NewReader("bad")), "u1"))
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", w.Code)
		}
	})
	t.Run("success", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.handleSetPluginDefaults(w, withToken(httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{}`)), "u1"))
		if w.Code != http.StatusNoContent {
			t.Errorf("expected 204, got %d", w.Code)
		}
	})
}

func TestHandleDeletePluginDefaults(t *testing.T) {
	s := serverWithDeps(&APIServer{userService: &mockUserServiceClient{}})
	w := httptest.NewRecorder()
	s.handleDeletePluginDefaults(w, httptest.NewRequest(http.MethodDelete, "/", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
	w = httptest.NewRecorder()
	s.handleDeletePluginDefaults(w, withToken(httptest.NewRequest(http.MethodDelete, "/", nil), "u1"))
	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", w.Code)
	}
}

func TestHandleDeleteCounter(t *testing.T) {
	s := serverWithDeps(&APIServer{userService: &mockUserServiceClient{}})
	w := httptest.NewRecorder()
	s.handleDeleteCounter(w, httptest.NewRequest(http.MethodDelete, "/", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
	w = httptest.NewRecorder()
	s.handleDeleteCounter(w, withToken(httptest.NewRequest(http.MethodDelete, "/", nil), "u1"))
	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", w.Code)
	}
}

// =============================================================
// activity.go — fit parse, showcase settings, slug, entries, upload urls, roundup
// =============================================================

func TestHandleParseFitFile(t *testing.T) {
	s := serverWithDeps(&APIServer{activitySvc: &mockActivityServiceClient{}})
	t.Run("no token", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.handleParseFitFile(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`)))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})
	t.Run("invalid body", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.handleParseFitFile(w, withToken(httptest.NewRequest(http.MethodPost, "/", strings.NewReader("bad")), "u1"))
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", w.Code)
		}
	})
	t.Run("success", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.handleParseFitFile(w, withToken(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`)), "u1"))
		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", w.Code)
		}
	})
}

func TestHandleGetShowcaseSettings(t *testing.T) {
	s := serverWithDeps(&APIServer{activitySvc: &mockActivityServiceClient{}})
	w := httptest.NewRecorder()
	s.handleGetShowcaseSettings(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
	w = httptest.NewRecorder()
	s.handleGetShowcaseSettings(w, withToken(httptest.NewRequest(http.MethodGet, "/", nil), "u1"))
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleUpdateShowcaseSettings(t *testing.T) {
	s := serverWithDeps(&APIServer{activitySvc: &mockActivityServiceClient{}})
	t.Run("no token", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.handleUpdateShowcaseSettings(w, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{}`)))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})
	t.Run("invalid proto body", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.handleUpdateShowcaseSettings(w, withToken(httptest.NewRequest(http.MethodPut, "/", strings.NewReader("not json")), "u1"))
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", w.Code)
		}
	})
	t.Run("bad link url", func(t *testing.T) {
		w := httptest.NewRecorder()
		body := `{"links":[{"url":"ftp://evil"}]}`
		s.handleUpdateShowcaseSettings(w, withToken(httptest.NewRequest(http.MethodPut, "/", strings.NewReader(body)), "u1"))
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", w.Code)
		}
	})
	t.Run("success", func(t *testing.T) {
		w := httptest.NewRecorder()
		body := `{"display_name":"Bob","links":[{"url":"https://ok"}]}`
		s.handleUpdateShowcaseSettings(w, withToken(httptest.NewRequest(http.MethodPut, "/", strings.NewReader(body)), "u1"))
		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", w.Code)
		}
	})
}

func TestHandleUpdateShowcaseSlug(t *testing.T) {
	s := serverWithDeps(&APIServer{activitySvc: &mockActivityServiceClient{}})
	t.Run("no token", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.handleUpdateShowcaseSlug(w, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{}`)))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})
	t.Run("invalid body", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.handleUpdateShowcaseSlug(w, withToken(httptest.NewRequest(http.MethodPut, "/", strings.NewReader("bad")), "u1"))
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", w.Code)
		}
	})
	t.Run("success", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.handleUpdateShowcaseSlug(w, withToken(httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{}`)), "u1"))
		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", w.Code)
		}
	})
}

func TestHandleAddShowcaseEntry(t *testing.T) {
	s := serverWithDeps(&APIServer{activitySvc: &mockActivityServiceClient{}})
	w := httptest.NewRecorder()
	s.handleAddShowcaseEntry(w, httptest.NewRequest(http.MethodPost, "/", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
	w = httptest.NewRecorder()
	s.handleAddShowcaseEntry(w, withToken(httptest.NewRequest(http.MethodPost, "/", nil), "u1"))
	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", w.Code)
	}
}

func TestHandleRemoveShowcaseEntry(t *testing.T) {
	s := serverWithDeps(&APIServer{activitySvc: &mockActivityServiceClient{}})
	w := httptest.NewRecorder()
	s.handleRemoveShowcaseEntry(w, httptest.NewRequest(http.MethodDelete, "/", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
	w = httptest.NewRecorder()
	s.handleRemoveShowcaseEntry(w, withToken(httptest.NewRequest(http.MethodDelete, "/", nil), "u1"))
	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", w.Code)
	}
}

func TestHandleGetShowcaseProfilePictureUploadUrl(t *testing.T) {
	s := serverWithDeps(&APIServer{activitySvc: &mockActivityServiceClient{}})
	t.Run("no token", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.handleGetShowcaseProfilePictureUploadUrl(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`)))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})
	t.Run("invalid body", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.handleGetShowcaseProfilePictureUploadUrl(w, withToken(httptest.NewRequest(http.MethodPost, "/", strings.NewReader("bad")), "u1"))
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", w.Code)
		}
	})
	t.Run("success", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.handleGetShowcaseProfilePictureUploadUrl(w, withToken(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`)), "u1"))
		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", w.Code)
		}
	})
}

func TestHandleGetActivityPhotoUploadUrl(t *testing.T) {
	s := serverWithDeps(&APIServer{activitySvc: &mockActivityServiceClient{}})
	t.Run("no token", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.handleGetActivityPhotoUploadUrl(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`)))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})
	t.Run("invalid body", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.handleGetActivityPhotoUploadUrl(w, withToken(httptest.NewRequest(http.MethodPost, "/", strings.NewReader("bad")), "u1"))
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", w.Code)
		}
	})
	t.Run("success", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.handleGetActivityPhotoUploadUrl(w, withToken(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`)), "u1"))
		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", w.Code)
		}
	})
}

func TestHandleUpdateRoundupSettings(t *testing.T) {
	s := serverWithDeps(&APIServer{activitySvc: &mockActivityServiceClient{}})
	t.Run("no token", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.handleUpdateRoundupSettings(w, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{}`)))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})
	t.Run("missing settings wrapper", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.handleUpdateRoundupSettings(w, withToken(httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{}`)), "u1"))
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", w.Code)
		}
	})
	t.Run("invalid json", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.handleUpdateRoundupSettings(w, withToken(httptest.NewRequest(http.MethodPut, "/", strings.NewReader("bad")), "u1"))
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", w.Code)
		}
	})
	t.Run("success", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.handleUpdateRoundupSettings(w, withToken(httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"settings":{}}`)), "u1"))
		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", w.Code)
		}
	})
}

func TestHandleRecomputeRoundup(t *testing.T) {
	s := serverWithDeps(&APIServer{activitySvc: &mockActivityServiceClient{}})
	t.Run("no token", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.handleRecomputeRoundup(w, httptest.NewRequest(http.MethodPost, "/", nil))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})
	t.Run("missing period key", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.handleRecomputeRoundup(w, withToken(httptest.NewRequest(http.MethodPost, "/", nil), "u1"))
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", w.Code)
		}
	})
	t.Run("success", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := withToken(withURLParam(httptest.NewRequest(http.MethodPost, "/", nil), "periodKey", "2026-W01"), "u1")
		s.handleRecomputeRoundup(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", w.Code)
		}
	})
}

func TestHandleExportPipelineRun(t *testing.T) {
	s := serverWithDeps(&APIServer{activitySvc: &mockActivityServiceClient{}})
	w := httptest.NewRecorder()
	s.handleExportPipelineRun(w, httptest.NewRequest(http.MethodPost, "/", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
	w = httptest.NewRecorder()
	s.handleExportPipelineRun(w, withToken(httptest.NewRequest(http.MethodPost, "/", nil), "u1"))
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

// =============================================================
// pipeline.go — 0% handlers
// =============================================================

func TestHandleListSourceActivities(t *testing.T) {
	s := serverWithDeps(&APIServer{pipelineSvc: &mockPipelineServiceClient{}})
	w := httptest.NewRecorder()
	s.handleListSourceActivities(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
	w = httptest.NewRecorder()
	s.handleListSourceActivities(w, withToken(httptest.NewRequest(http.MethodGet, "/?source=strava", nil), "u1"))
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleBackfillActivities(t *testing.T) {
	s := serverWithDeps(&APIServer{pipelineSvc: &mockPipelineServiceClient{}})
	t.Run("no token", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.handleBackfillActivities(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`)))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})
	t.Run("invalid body", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.handleBackfillActivities(w, withToken(httptest.NewRequest(http.MethodPost, "/", strings.NewReader("bad")), "u1"))
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", w.Code)
		}
	})
	t.Run("success", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.handleBackfillActivities(w, withToken(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"source":"strava","sourceActivityIds":["1"]}`)), "u1"))
		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", w.Code)
		}
	})
}

func TestHandleCancelPipeline(t *testing.T) {
	s := serverWithDeps(&APIServer{pipelineSvc: &mockPipelineServiceClient{}})
	w := httptest.NewRecorder()
	s.handleCancelPipeline(w, httptest.NewRequest(http.MethodPost, "/", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
	w = httptest.NewRecorder()
	s.handleCancelPipeline(w, withToken(httptest.NewRequest(http.MethodPost, "/", nil), "u1"))
	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", w.Code)
	}
}

func TestHandleCancelPipelineRun(t *testing.T) {
	s := serverWithDeps(&APIServer{pipelineSvc: &mockPipelineServiceClient{}})
	w := httptest.NewRecorder()
	s.handleCancelPipelineRun(w, httptest.NewRequest(http.MethodPost, "/", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
	w = httptest.NewRecorder()
	s.handleCancelPipelineRun(w, withToken(httptest.NewRequest(http.MethodPost, "/", nil), "u1"))
	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", w.Code)
	}
}

func TestHandleGetPipelineRunPayload(t *testing.T) {
	t.Run("no token", func(t *testing.T) {
		s := serverWithDeps(&APIServer{pipelineSvc: &mockPipelineServiceClient{}})
		w := httptest.NewRecorder()
		s.handleGetPipelineRunPayload(w, httptest.NewRequest(http.MethodGet, "/", nil))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})
	t.Run("signer not configured", func(t *testing.T) {
		// gcsSigner is nil -> 501 Not Implemented.
		s := serverWithDeps(&APIServer{pipelineSvc: &mockPipelineServiceClient{}})
		w := httptest.NewRecorder()
		s.handleGetPipelineRunPayload(w, withToken(httptest.NewRequest(http.MethodGet, "/", nil), "u1"))
		if w.Code != http.StatusNotImplemented {
			t.Errorf("expected 501, got %d", w.Code)
		}
	})
}

func TestHandleListConnectionActivities(t *testing.T) {
	s := serverWithDeps(&APIServer{pipelineSvc: &mockPipelineServiceClient{}})
	t.Run("no token", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.handleListConnectionActivities(w, httptest.NewRequest(http.MethodGet, "/", nil))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})
	t.Run("unsupported provider", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := withToken(withURLParam(httptest.NewRequest(http.MethodGet, "/", nil), "provider", "spotify"), "u1")
		s.handleListConnectionActivities(w, r)
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", w.Code)
		}
	})
	t.Run("success", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := withToken(withURLParam(httptest.NewRequest(http.MethodGet, "/", nil), "provider", "strava"), "u1")
		s.handleListConnectionActivities(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", w.Code)
		}
	})
}

func TestHandleBackfillConnectionActivities(t *testing.T) {
	s := serverWithDeps(&APIServer{pipelineSvc: &mockPipelineServiceClient{}})
	t.Run("no token", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.handleBackfillConnectionActivities(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`)))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})
	t.Run("unsupported provider", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := withToken(withURLParam(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`)), "provider", "spotify"), "u1")
		s.handleBackfillConnectionActivities(w, r)
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", w.Code)
		}
	})
	t.Run("invalid body", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := withToken(withURLParam(httptest.NewRequest(http.MethodPost, "/", strings.NewReader("bad")), "provider", "strava"), "u1")
		s.handleBackfillConnectionActivities(w, r)
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", w.Code)
		}
	})
	t.Run("success", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := withToken(withURLParam(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"sourceActivityIds":["1"]}`)), "provider", "strava"), "u1")
		s.handleBackfillConnectionActivities(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", w.Code)
		}
	})
}

// =============================================================
// helpers.go — decodeProto read error
// =============================================================

func TestDecodeProto_ReadError(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", errReader{})
	var msg userpb.SetPersonalRecordRequest
	if err := decodeProto(r, &msg); err == nil {
		t.Error("expected error reading body")
	}
}

// =============================================================
// middleware.go — SentryRecoveryMiddleware
// =============================================================

func TestSentryRecoveryMiddleware_RecoversPanic(t *testing.T) {
	logger := infra.NewLogger()
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("boom")
	})
	handler := SentryRecoveryMiddleware(logger)(next)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestSentryRecoveryMiddleware_NoPanic(t *testing.T) {
	logger := infra.NewLogger()
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := SentryRecoveryMiddleware(logger)(next)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

// =============================================================
// Service-error branches for handlers otherwise only success-tested
// =============================================================

func TestActivityHandlers_ServiceErrors(t *testing.T) {
	t.Run("getActivity error", func(t *testing.T) {
		svc := &mockActivityServiceClient{
			getActivity: func(_ context.Context, _ *activitypb.GetActivityRequest, _ ...grpc.CallOption) (*pbactivity.StandardizedActivity, error) {
				return nil, status.Error(codes.NotFound, "x")
			},
		}
		s := serverWithDeps(&APIServer{activitySvc: svc})
		w := httptest.NewRecorder()
		s.handleGetActivity(w, withToken(httptest.NewRequest(http.MethodGet, "/", nil), "u1"))
		if w.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", w.Code)
		}
	})

	t.Run("deleteActivity error", func(t *testing.T) {
		svc := &mockActivityServiceClient{
			deleteActivity: func(_ context.Context, _ *activitypb.DeleteActivityRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
				return nil, status.Error(codes.Internal, "x")
			},
		}
		s := serverWithDeps(&APIServer{activitySvc: svc})
		w := httptest.NewRecorder()
		s.handleDeleteActivity(w, withToken(httptest.NewRequest(http.MethodDelete, "/", nil), "u1"))
		if w.Code != http.StatusInternalServerError {
			t.Errorf("expected 500, got %d", w.Code)
		}
	})

	t.Run("listShowcases error", func(t *testing.T) {
		svc := &mockActivityServiceClient{
			listShowcases: func(_ context.Context, _ *activitypb.ListShowcasesRequest, _ ...grpc.CallOption) (*activitypb.ListShowcasesResponse, error) {
				return nil, status.Error(codes.Internal, "x")
			},
		}
		s := serverWithDeps(&APIServer{activitySvc: svc})
		w := httptest.NewRecorder()
		s.handleListShowcases(w, withToken(httptest.NewRequest(http.MethodGet, "/", nil), "u1"))
		if w.Code != http.StatusInternalServerError {
			t.Errorf("expected 500, got %d", w.Code)
		}
	})

	t.Run("getShowcase error", func(t *testing.T) {
		svc := &mockActivityServiceClient{
			getShowcase: func(_ context.Context, _ *activitypb.GetShowcaseRequest, _ ...grpc.CallOption) (*pbactivity.ShowcasedActivity, error) {
				return nil, status.Error(codes.NotFound, "x")
			},
		}
		s := serverWithDeps(&APIServer{activitySvc: svc})
		w := httptest.NewRecorder()
		s.handleGetShowcase(w, withToken(httptest.NewRequest(http.MethodGet, "/", nil), "u1"))
		if w.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", w.Code)
		}
	})

	t.Run("deleteShowcase error", func(t *testing.T) {
		svc := &mockActivityServiceClient{
			deleteShowcase: func(_ context.Context, _ *activitypb.DeleteShowcaseRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
				return nil, status.Error(codes.Internal, "x")
			},
		}
		s := serverWithDeps(&APIServer{activitySvc: svc})
		w := httptest.NewRecorder()
		s.handleDeleteShowcase(w, withToken(httptest.NewRequest(http.MethodDelete, "/", nil), "u1"))
		if w.Code != http.StatusInternalServerError {
			t.Errorf("expected 500, got %d", w.Code)
		}
	})

	t.Run("getShowcasePreferences error", func(t *testing.T) {
		svc := &mockActivityServiceClient{
			getShowcasePreferences: func(_ context.Context, _ *activitypb.GetShowcasePreferencesRequest, _ ...grpc.CallOption) (*pbactivity.ShowcaseProfile, error) {
				return nil, status.Error(codes.Internal, "x")
			},
		}
		s := serverWithDeps(&APIServer{activitySvc: svc})
		w := httptest.NewRecorder()
		s.handleGetShowcasePreferences(w, withToken(httptest.NewRequest(http.MethodGet, "/", nil), "u1"))
		if w.Code != http.StatusInternalServerError {
			t.Errorf("expected 500, got %d", w.Code)
		}
	})

	t.Run("updateShowcasePreferences error", func(t *testing.T) {
		svc := &mockActivityServiceClient{
			updateShowcasePreferences: func(_ context.Context, _ *activitypb.UpdateShowcasePreferencesRequest, _ ...grpc.CallOption) (*pbactivity.ShowcaseProfile, error) {
				return nil, status.Error(codes.Internal, "x")
			},
		}
		s := serverWithDeps(&APIServer{activitySvc: svc})
		w := httptest.NewRecorder()
		s.handleUpdateShowcasePreferences(w, withToken(httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{}`)), "u1"))
		if w.Code != http.StatusInternalServerError {
			t.Errorf("expected 500, got %d", w.Code)
		}
	})
}

func TestPipelineHandlers_ServiceErrors(t *testing.T) {
	t.Run("getPipeline error", func(t *testing.T) {
		svc := &mockPipelineServiceClient{
			getPipeline: func(_ context.Context, _ *pipelinepb.GetPipelineRequest, _ ...grpc.CallOption) (*pbpipeline.PipelineConfig, error) {
				return nil, status.Error(codes.NotFound, "x")
			},
		}
		s := serverWithDeps(&APIServer{pipelineSvc: svc})
		w := httptest.NewRecorder()
		s.handleGetPipeline(w, withToken(httptest.NewRequest(http.MethodGet, "/", nil), "u1"))
		if w.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", w.Code)
		}
	})

	t.Run("deletePipeline error", func(t *testing.T) {
		svc := &mockPipelineServiceClient{
			deletePipeline: func(_ context.Context, _ *pipelinepb.DeletePipelineRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
				return nil, status.Error(codes.Internal, "x")
			},
		}
		s := serverWithDeps(&APIServer{pipelineSvc: svc})
		w := httptest.NewRecorder()
		s.handleDeletePipeline(w, withToken(httptest.NewRequest(http.MethodDelete, "/", nil), "u1"))
		if w.Code != http.StatusInternalServerError {
			t.Errorf("expected 500, got %d", w.Code)
		}
	})

	t.Run("getPipelineRun error", func(t *testing.T) {
		svc := &mockPipelineServiceClient{
			getPipelineRun: func(_ context.Context, _ *pipelinepb.GetPipelineRunRequest, _ ...grpc.CallOption) (*pbpipeline.PipelineRun, error) {
				return nil, status.Error(codes.NotFound, "x")
			},
		}
		s := serverWithDeps(&APIServer{pipelineSvc: svc})
		w := httptest.NewRecorder()
		s.handleGetPipelineRun(w, withToken(httptest.NewRequest(http.MethodGet, "/", nil), "u1"))
		if w.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", w.Code)
		}
	})
}

func TestUserHandlers_ServiceErrors(t *testing.T) {
	t.Run("getBoosterData error", func(t *testing.T) {
		svc := &errUserClient{}
		s := serverWithDeps(&APIServer{userService: svc})
		w := httptest.NewRecorder()
		s.handleGetBoosterData(w, withToken(httptest.NewRequest(http.MethodGet, "/", nil), "u1"))
		if w.Code != http.StatusInternalServerError {
			t.Errorf("expected 500, got %d", w.Code)
		}
	})
}

func TestHandleSetIntegration_Branches(t *testing.T) {
	t.Run("unsupported provider", func(t *testing.T) {
		s := serverWithDeps(&APIServer{userService: &mockUserServiceClient{}})
		r := withToken(withURLParam(httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{}`)), "provider", "myspace"), "u1")
		w := httptest.NewRecorder()
		s.handleSetIntegration(w, r)
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", w.Code)
		}
	})
	t.Run("invalid body", func(t *testing.T) {
		s := serverWithDeps(&APIServer{userService: &mockUserServiceClient{}})
		r := withToken(withURLParam(httptest.NewRequest(http.MethodPut, "/", strings.NewReader("bad")), "provider", "strava"), "u1")
		w := httptest.NewRecorder()
		s.handleSetIntegration(w, r)
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", w.Code)
		}
	})
	t.Run("intervals missing fields", func(t *testing.T) {
		s := serverWithDeps(&APIServer{userService: &mockUserServiceClient{}})
		r := withToken(withURLParam(httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"apiKey":""}`)), "provider", "intervals"), "u1")
		w := httptest.NewRecorder()
		s.handleSetIntegration(w, r)
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", w.Code)
		}
	})
	t.Run("setIntegration service error", func(t *testing.T) {
		svc := &mockUserServiceClient{
			setIntegration: func(_ context.Context, _ *userpb.SetIntegrationRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
				return nil, status.Error(codes.Internal, "boom")
			},
		}
		s := serverWithDeps(&APIServer{userService: svc})
		r := withToken(withURLParam(httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"access_token":"a"}`)), "provider", "strava"), "u1")
		w := httptest.NewRecorder()
		s.handleSetIntegration(w, r)
		if w.Code != http.StatusInternalServerError {
			t.Errorf("expected 500, got %d", w.Code)
		}
	})
}

// errUserClient embeds the base mock and overrides a couple of methods to error.
type errUserClient struct {
	mockUserServiceClient
}

func (e *errUserClient) GetBoosterData(_ context.Context, _ *userpb.GetBoosterDataRequest, _ ...grpc.CallOption) (*userpb.GetBoosterDataResponse, error) {
	return nil, status.Error(codes.Internal, "boom")
}

func (e *errUserClient) SetBoosterData(_ context.Context, _ *userpb.SetBoosterDataRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	return nil, status.Error(codes.Internal, "boom")
}

func (e *errUserClient) DeleteBoosterData(_ context.Context, _ *userpb.DeleteBoosterDataRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	return nil, status.Error(codes.Internal, "boom")
}

func (e *errUserClient) ListPersonalRecords(_ context.Context, _ *userpb.ListPersonalRecordsRequest, _ ...grpc.CallOption) (*userpb.ListPersonalRecordsResponse, error) {
	return nil, status.Error(codes.Internal, "boom")
}

func (e *errUserClient) SetPersonalRecord(_ context.Context, _ *userpb.SetPersonalRecordRequest, _ ...grpc.CallOption) (*pbuser.PersonalRecord, error) {
	return nil, status.Error(codes.Internal, "boom")
}

func (e *errUserClient) DeletePersonalRecord(_ context.Context, _ *userpb.DeletePersonalRecordRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	return nil, status.Error(codes.Internal, "boom")
}

func (e *errUserClient) ListPluginDefaults(_ context.Context, _ *userpb.ListPluginDefaultsRequest, _ ...grpc.CallOption) (*userpb.ListPluginDefaultsResponse, error) {
	return nil, status.Error(codes.Internal, "boom")
}

func (e *errUserClient) SetPluginDefaults(_ context.Context, _ *userpb.SetPluginDefaultsRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	return nil, status.Error(codes.Internal, "boom")
}

func (e *errUserClient) DeletePluginDefaults(_ context.Context, _ *userpb.DeletePluginDefaultsRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	return nil, status.Error(codes.Internal, "boom")
}

func (e *errUserClient) DeleteCounter(_ context.Context, _ *userpb.DeleteCounterRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	return nil, status.Error(codes.Internal, "boom")
}

// TestUserHandlers_MoreServiceErrors drives the error branch of each remaining
// user handler through errUserClient.
func TestUserHandlers_MoreServiceErrors(t *testing.T) {
	s := serverWithDeps(&APIServer{userService: &errUserClient{}})

	cases := []struct {
		name string
		fn   func(http.ResponseWriter, *http.Request)
		req  *http.Request
	}{
		{"setBooster", s.handleSetBoosterData, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"a":"b"}`))},
		{"deleteBooster", s.handleDeleteBoosterData, httptest.NewRequest(http.MethodDelete, "/", nil)},
		{"listPRs", s.handleListPersonalRecords, httptest.NewRequest(http.MethodGet, "/", nil)},
		{"setPR", s.handleSetPersonalRecord, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{}`))},
		{"deletePR", s.handleDeletePersonalRecord, httptest.NewRequest(http.MethodDelete, "/", nil)},
		{"listPlugins", s.handleListPluginDefaults, httptest.NewRequest(http.MethodGet, "/", nil)},
		{"setPlugins", s.handleSetPluginDefaults, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{}`))},
		{"deletePlugins", s.handleDeletePluginDefaults, httptest.NewRequest(http.MethodDelete, "/", nil)},
		{"deleteCounter", s.handleDeleteCounter, httptest.NewRequest(http.MethodDelete, "/", nil)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c.fn(w, withToken(c.req, "u1"))
			if w.Code != http.StatusInternalServerError {
				t.Errorf("%s: expected 500, got %d", c.name, w.Code)
			}
		})
	}
}

func TestMoreServiceErrors_ActivityPipeline(t *testing.T) {
	t.Run("listSourceActivities error", func(t *testing.T) {
		svc := &errPipelineClient{}
		s := serverWithDeps(&APIServer{pipelineSvc: svc})
		w := httptest.NewRecorder()
		s.handleListSourceActivities(w, withToken(httptest.NewRequest(http.MethodGet, "/", nil), "u1"))
		if w.Code != http.StatusInternalServerError {
			t.Errorf("expected 500, got %d", w.Code)
		}
	})
	t.Run("backfillActivities error", func(t *testing.T) {
		svc := &errPipelineClient{}
		s := serverWithDeps(&APIServer{pipelineSvc: svc})
		w := httptest.NewRecorder()
		s.handleBackfillActivities(w, withToken(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"source":"x"}`)), "u1"))
		if w.Code != http.StatusInternalServerError {
			t.Errorf("expected 500, got %d", w.Code)
		}
	})
	t.Run("cancelPipeline error", func(t *testing.T) {
		svc := &errPipelineClient{}
		s := serverWithDeps(&APIServer{pipelineSvc: svc})
		w := httptest.NewRecorder()
		s.handleCancelPipeline(w, withToken(httptest.NewRequest(http.MethodPost, "/", nil), "u1"))
		if w.Code != http.StatusInternalServerError {
			t.Errorf("expected 500, got %d", w.Code)
		}
	})
	t.Run("cancelPipelineRun error", func(t *testing.T) {
		svc := &errPipelineClient{}
		s := serverWithDeps(&APIServer{pipelineSvc: svc})
		w := httptest.NewRecorder()
		s.handleCancelPipelineRun(w, withToken(httptest.NewRequest(http.MethodPost, "/", nil), "u1"))
		if w.Code != http.StatusInternalServerError {
			t.Errorf("expected 500, got %d", w.Code)
		}
	})
}

type errPipelineClient struct {
	mockPipelineServiceClient
}

func (e *errPipelineClient) ListSourceActivities(_ context.Context, _ *pipelinepb.ListSourceActivitiesRequest, _ ...grpc.CallOption) (*pipelinepb.ListSourceActivitiesResponse, error) {
	return nil, status.Error(codes.Internal, "boom")
}
func (e *errPipelineClient) BackfillActivities(_ context.Context, _ *pipelinepb.BackfillActivitiesRequest, _ ...grpc.CallOption) (*pipelinepb.BackfillActivitiesResponse, error) {
	return nil, status.Error(codes.Internal, "boom")
}
func (e *errPipelineClient) CancelPipeline(_ context.Context, _ *pipelinepb.CancelPipelineRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	return nil, status.Error(codes.Internal, "boom")
}
func (e *errPipelineClient) CancelPipelineRun(_ context.Context, _ *pipelinepb.CancelPipelineRunRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	return nil, status.Error(codes.Internal, "boom")
}

func TestActivityHandlers_MoreServiceErrors(t *testing.T) {
	s := serverWithDeps(&APIServer{activitySvc: &errActivityClient{}})
	cases := []struct {
		name string
		fn   func(http.ResponseWriter, *http.Request)
		req  *http.Request
	}{
		{"parseFit", s.handleParseFitFile, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))},
		{"getSettings", s.handleGetShowcaseSettings, httptest.NewRequest(http.MethodGet, "/", nil)},
		{"updateSlug", s.handleUpdateShowcaseSlug, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{}`))},
		{"addEntry", s.handleAddShowcaseEntry, httptest.NewRequest(http.MethodPost, "/", nil)},
		{"removeEntry", s.handleRemoveShowcaseEntry, httptest.NewRequest(http.MethodDelete, "/", nil)},
		{"profilePicURL", s.handleGetShowcaseProfilePictureUploadUrl, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))},
		{"photoURL", s.handleGetActivityPhotoUploadUrl, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))},
		{"roundupSettings", s.handleUpdateRoundupSettings, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"settings":{}}`))},
		{"generateImages", s.handleGenerateShowcaseImages, httptest.NewRequest(http.MethodPost, "/", nil)},
		{"createShowcase", s.handleCreateShowcase, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))},
		{"updateShowcase", s.handleUpdateShowcase, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{}`))},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c.fn(w, withToken(c.req, "u1"))
			if w.Code != http.StatusInternalServerError {
				t.Errorf("%s: expected 500, got %d", c.name, w.Code)
			}
		})
	}

	t.Run("recomputeRoundup error", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := withToken(withURLParam(httptest.NewRequest(http.MethodPost, "/", nil), "periodKey", "2026-W01"), "u1")
		s.handleRecomputeRoundup(w, r)
		if w.Code != http.StatusInternalServerError {
			t.Errorf("expected 500, got %d", w.Code)
		}
	})
}

type errActivityClient struct {
	mockActivityServiceClient
}

func (e *errActivityClient) ParseFitFile(_ context.Context, _ *activitypb.ParseFitFileRequest, _ ...grpc.CallOption) (*pbactivity.StandardizedActivity, error) {
	return nil, status.Error(codes.Internal, "boom")
}
func (e *errActivityClient) GetShowcaseSettings(_ context.Context, _ *activitypb.GetShowcaseSettingsRequest, _ ...grpc.CallOption) (*activitypb.GetShowcaseSettingsResponse, error) {
	return nil, status.Error(codes.Internal, "boom")
}
func (e *errActivityClient) UpdateShowcaseSlug(_ context.Context, _ *activitypb.UpdateShowcaseSlugRequest, _ ...grpc.CallOption) (*activitypb.UpdateShowcaseSlugResponse, error) {
	return nil, status.Error(codes.Internal, "boom")
}
func (e *errActivityClient) AddShowcaseEntry(_ context.Context, _ *activitypb.AddShowcaseEntryRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	return nil, status.Error(codes.Internal, "boom")
}
func (e *errActivityClient) RemoveShowcaseEntry(_ context.Context, _ *activitypb.RemoveShowcaseEntryRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	return nil, status.Error(codes.Internal, "boom")
}
func (e *errActivityClient) GetShowcaseProfilePictureUploadUrl(_ context.Context, _ *activitypb.GetShowcaseProfilePictureUploadUrlRequest, _ ...grpc.CallOption) (*activitypb.GetShowcaseProfilePictureUploadUrlResponse, error) {
	return nil, status.Error(codes.Internal, "boom")
}
func (e *errActivityClient) GetActivityPhotoUploadUrl(_ context.Context, _ *activitypb.GetActivityPhotoUploadUrlRequest, _ ...grpc.CallOption) (*activitypb.GetActivityPhotoUploadUrlResponse, error) {
	return nil, status.Error(codes.Internal, "boom")
}
func (e *errActivityClient) UpdateRoundupSettings(_ context.Context, _ *activitypb.UpdateRoundupSettingsRequest, _ ...grpc.CallOption) (*pbactivity.ShowcaseProfile, error) {
	return nil, status.Error(codes.Internal, "boom")
}
func (e *errActivityClient) RecomputeRoundup(_ context.Context, _ *activitypb.RecomputeRoundupRequest, _ ...grpc.CallOption) (*pbactivity.ShowcaseRoundup, error) {
	return nil, status.Error(codes.Internal, "boom")
}
func (e *errActivityClient) GenerateShowcaseImages(_ context.Context, _ *activitypb.GenerateShowcaseImagesRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	return nil, status.Error(codes.Internal, "boom")
}
func (e *errActivityClient) CreateShowcase(_ context.Context, _ *activitypb.CreateShowcaseRequest, _ ...grpc.CallOption) (*pbactivity.ShowcasedActivity, error) {
	return nil, status.Error(codes.Internal, "boom")
}
func (e *errActivityClient) UpdateShowcase(_ context.Context, _ *activitypb.UpdateShowcaseRequest, _ ...grpc.CallOption) (*pbactivity.ShowcasedActivity, error) {
	return nil, status.Error(codes.Internal, "boom")
}

// errReader fails on Read; used to exercise decode read-error branches.
type errReader struct{}

func (errReader) Read(_ []byte) (int, error) { return 0, io.ErrClosedPipe }
