package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	pbactivitym "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	pbpipeline "github.com/fitglue/server/src/go/pkg/types/pb/models/pipeline"
	pipelinepb "github.com/fitglue/server/src/go/pkg/types/pb/services/pipeline"
)

// =============================================================
// Mock PipelineServiceClient
// =============================================================

type mockPipelineServiceClient struct {
	listPipelines              func(ctx context.Context, in *pipelinepb.ListPipelinesRequest, opts ...grpc.CallOption) (*pipelinepb.ListPipelinesResponse, error)
	getPipeline                func(ctx context.Context, in *pipelinepb.GetPipelineRequest, opts ...grpc.CallOption) (*pbpipeline.PipelineConfig, error)
	createPipeline             func(ctx context.Context, in *pipelinepb.CreatePipelineRequest, opts ...grpc.CallOption) (*pbpipeline.PipelineConfig, error)
	updatePipeline             func(ctx context.Context, in *pipelinepb.UpdatePipelineRequest, opts ...grpc.CallOption) (*pbpipeline.PipelineConfig, error)
	deletePipeline             func(ctx context.Context, in *pipelinepb.DeletePipelineRequest, opts ...grpc.CallOption) (*emptypb.Empty, error)
	listPipelineRuns           func(ctx context.Context, in *pipelinepb.ListPipelineRunsRequest, opts ...grpc.CallOption) (*pipelinepb.ListPipelineRunsResponse, error)
	getPipelineRun             func(ctx context.Context, in *pipelinepb.GetPipelineRunRequest, opts ...grpc.CallOption) (*pbpipeline.PipelineRun, error)
	submitInput                func(ctx context.Context, in *pipelinepb.SubmitInputRequest, opts ...grpc.CallOption) (*emptypb.Empty, error)
	repostActivity             func(ctx context.Context, in *pipelinepb.RepostActivityRequest, opts ...grpc.CallOption) (*emptypb.Empty, error)
	updateActivity             func(ctx context.Context, in *pipelinepb.UpdateActivityRequest, opts ...grpc.CallOption) (*pbactivitym.StandardizedActivity, error)
	resendActivity             func(ctx context.Context, in *pipelinepb.ResendActivityRequest, opts ...grpc.CallOption) (*emptypb.Empty, error)
	invokeEnricher             func(ctx context.Context, in *pipelinepb.InvokeEnricherRequest, opts ...grpc.CallOption) (*pipelinepb.InvokeEnricherResponse, error)
	acceptProposedEnricherRun  func(ctx context.Context, in *pipelinepb.AcceptProposedEnricherRunRequest, opts ...grpc.CallOption) (*pbactivitym.StandardizedActivity, error)
	dismissProposedEnricherRun func(ctx context.Context, in *pipelinepb.DismissProposedEnricherRunRequest, opts ...grpc.CallOption) (*emptypb.Empty, error)
}

func (m *mockPipelineServiceClient) ListPipelines(ctx context.Context, in *pipelinepb.ListPipelinesRequest, opts ...grpc.CallOption) (*pipelinepb.ListPipelinesResponse, error) {
	if m.listPipelines != nil {
		return m.listPipelines(ctx, in, opts...)
	}
	return &pipelinepb.ListPipelinesResponse{}, nil
}
func (m *mockPipelineServiceClient) GetPipeline(ctx context.Context, in *pipelinepb.GetPipelineRequest, opts ...grpc.CallOption) (*pbpipeline.PipelineConfig, error) {
	if m.getPipeline != nil {
		return m.getPipeline(ctx, in, opts...)
	}
	return &pbpipeline.PipelineConfig{}, nil
}
func (m *mockPipelineServiceClient) CreatePipeline(ctx context.Context, in *pipelinepb.CreatePipelineRequest, opts ...grpc.CallOption) (*pbpipeline.PipelineConfig, error) {
	if m.createPipeline != nil {
		return m.createPipeline(ctx, in, opts...)
	}
	return &pbpipeline.PipelineConfig{}, nil
}
func (m *mockPipelineServiceClient) UpdatePipeline(ctx context.Context, in *pipelinepb.UpdatePipelineRequest, opts ...grpc.CallOption) (*pbpipeline.PipelineConfig, error) {
	if m.updatePipeline != nil {
		return m.updatePipeline(ctx, in, opts...)
	}
	return &pbpipeline.PipelineConfig{}, nil
}
func (m *mockPipelineServiceClient) DeletePipeline(ctx context.Context, in *pipelinepb.DeletePipelineRequest, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	if m.deletePipeline != nil {
		return m.deletePipeline(ctx, in, opts...)
	}
	return &emptypb.Empty{}, nil
}
func (m *mockPipelineServiceClient) SubmitInput(ctx context.Context, in *pipelinepb.SubmitInputRequest, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	if m.submitInput != nil {
		return m.submitInput(ctx, in, opts...)
	}
	return &emptypb.Empty{}, nil
}
func (m *mockPipelineServiceClient) ListPendingInputs(ctx context.Context, in *pipelinepb.ListPendingInputsRequest, opts ...grpc.CallOption) (*pipelinepb.ListPendingInputsResponse, error) {
	return &pipelinepb.ListPendingInputsResponse{}, nil
}
func (m *mockPipelineServiceClient) ResolvePendingInput(ctx context.Context, in *pipelinepb.ResolvePendingInputRequest, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}
func (m *mockPipelineServiceClient) CancelPipeline(ctx context.Context, in *pipelinepb.CancelPipelineRequest, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}
func (m *mockPipelineServiceClient) CancelPipelineRun(ctx context.Context, in *pipelinepb.CancelPipelineRunRequest, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}
func (m *mockPipelineServiceClient) RepostActivity(ctx context.Context, in *pipelinepb.RepostActivityRequest, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	if m.repostActivity != nil {
		return m.repostActivity(ctx, in, opts...)
	}
	return &emptypb.Empty{}, nil
}
func (m *mockPipelineServiceClient) GetPipelineRun(ctx context.Context, in *pipelinepb.GetPipelineRunRequest, opts ...grpc.CallOption) (*pbpipeline.PipelineRun, error) {
	if m.getPipelineRun != nil {
		return m.getPipelineRun(ctx, in, opts...)
	}
	return &pbpipeline.PipelineRun{}, nil
}
func (m *mockPipelineServiceClient) ListPipelineRuns(ctx context.Context, in *pipelinepb.ListPipelineRunsRequest, opts ...grpc.CallOption) (*pipelinepb.ListPipelineRunsResponse, error) {
	if m.listPipelineRuns != nil {
		return m.listPipelineRuns(ctx, in, opts...)
	}
	return &pipelinepb.ListPipelineRunsResponse{}, nil
}
func (m *mockPipelineServiceClient) ListSourceActivities(ctx context.Context, in *pipelinepb.ListSourceActivitiesRequest, opts ...grpc.CallOption) (*pipelinepb.ListSourceActivitiesResponse, error) {
	return &pipelinepb.ListSourceActivitiesResponse{}, nil
}
func (m *mockPipelineServiceClient) BackfillActivities(ctx context.Context, in *pipelinepb.BackfillActivitiesRequest, opts ...grpc.CallOption) (*pipelinepb.BackfillActivitiesResponse, error) {
	return &pipelinepb.BackfillActivitiesResponse{}, nil
}
func (m *mockPipelineServiceClient) RefreshActivitySource(ctx context.Context, in *pipelinepb.RefreshActivitySourceRequest, opts ...grpc.CallOption) (*pbactivitym.StandardizedActivity, error) {
	return &pbactivitym.StandardizedActivity{}, nil
}
func (m *mockPipelineServiceClient) UpdateActivity(ctx context.Context, in *pipelinepb.UpdateActivityRequest, opts ...grpc.CallOption) (*pbactivitym.StandardizedActivity, error) {
	if m.updateActivity != nil {
		return m.updateActivity(ctx, in, opts...)
	}
	return &pbactivitym.StandardizedActivity{}, nil
}
func (m *mockPipelineServiceClient) ResendActivity(ctx context.Context, in *pipelinepb.ResendActivityRequest, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	if m.resendActivity != nil {
		return m.resendActivity(ctx, in, opts...)
	}
	return &emptypb.Empty{}, nil
}

// =============================================================
// Pipeline Handler Tests
// =============================================================

func buildPipelineServer(pSvc pipelinepb.PipelineServiceClient) *APIServer {
	return &APIServer{pipelineSvc: pSvc}
}

func TestHandleListPipelines_Success(t *testing.T) {
	s := buildPipelineServer(&mockPipelineServiceClient{})
	r := httptest.NewRequest(http.MethodGet, "/api/v2/users/me/pipelines", nil)
	r = withToken(r, "user1")
	w := httptest.NewRecorder()
	s.handleListPipelines(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleListPipelines_NoToken(t *testing.T) {
	s := buildPipelineServer(&mockPipelineServiceClient{})
	r := httptest.NewRequest(http.MethodGet, "/api/v2/users/me/pipelines", nil)
	w := httptest.NewRecorder()
	s.handleListPipelines(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestHandleListPipelines_ServiceError(t *testing.T) {
	svc := &mockPipelineServiceClient{
		listPipelines: func(_ context.Context, _ *pipelinepb.ListPipelinesRequest, _ ...grpc.CallOption) (*pipelinepb.ListPipelinesResponse, error) {
			return nil, status.Error(codes.Internal, "db error")
		},
	}
	s := buildPipelineServer(svc)
	r := httptest.NewRequest(http.MethodGet, "/api/v2/users/me/pipelines", nil)
	r = withToken(r, "user1")
	w := httptest.NewRecorder()
	s.handleListPipelines(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestHandleGetPipeline_Success(t *testing.T) {
	s := buildPipelineServer(&mockPipelineServiceClient{})
	r := httptest.NewRequest(http.MethodGet, "/api/v2/users/me/pipelines/pipe1", nil)
	r = withToken(r, "user1")
	w := httptest.NewRecorder()
	s.handleGetPipeline(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleGetPipeline_NoToken(t *testing.T) {
	s := buildPipelineServer(&mockPipelineServiceClient{})
	r := httptest.NewRequest(http.MethodGet, "/api/v2/users/me/pipelines/pipe1", nil)
	w := httptest.NewRecorder()
	s.handleGetPipeline(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestHandleCreatePipeline_Success(t *testing.T) {
	s := buildPipelineServer(&mockPipelineServiceClient{})
	body, _ := json.Marshal(map[string]string{"name": "My Pipeline"})
	r := httptest.NewRequest(http.MethodPost, "/api/v2/users/me/pipelines", bytes.NewReader(body))
	r = withToken(r, "user1")
	w := httptest.NewRecorder()
	s.handleCreatePipeline(w, r)
	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d", w.Code)
	}
}

func TestHandleCreatePipeline_InvalidJSON(t *testing.T) {
	s := buildPipelineServer(&mockPipelineServiceClient{})
	r := httptest.NewRequest(http.MethodPost, "/api/v2/users/me/pipelines", strings.NewReader("not json"))
	r = withToken(r, "user1")
	w := httptest.NewRecorder()
	s.handleCreatePipeline(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleCreatePipeline_NoToken(t *testing.T) {
	s := buildPipelineServer(&mockPipelineServiceClient{})
	body, _ := json.Marshal(map[string]string{"name": "Test"})
	r := httptest.NewRequest(http.MethodPost, "/api/v2/users/me/pipelines", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleCreatePipeline(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestHandleUpdatePipeline_Success(t *testing.T) {
	s := buildPipelineServer(&mockPipelineServiceClient{})
	body, _ := json.Marshal(map[string]string{"name": "Updated"})
	r := httptest.NewRequest(http.MethodPut, "/api/v2/users/me/pipelines/pipe1", bytes.NewReader(body))
	r = withToken(r, "user1")
	w := httptest.NewRecorder()
	s.handleUpdatePipeline(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleUpdatePipeline_InvalidJSON(t *testing.T) {
	s := buildPipelineServer(&mockPipelineServiceClient{})
	r := httptest.NewRequest(http.MethodPut, "/api/v2/users/me/pipelines/pipe1", strings.NewReader("bad"))
	r = withToken(r, "user1")
	w := httptest.NewRecorder()
	s.handleUpdatePipeline(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleDeletePipeline_Success(t *testing.T) {
	s := buildPipelineServer(&mockPipelineServiceClient{})
	r := httptest.NewRequest(http.MethodDelete, "/api/v2/users/me/pipelines/pipe1", nil)
	r = withToken(r, "user1")
	w := httptest.NewRecorder()
	s.handleDeletePipeline(w, r)
	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", w.Code)
	}
}

func TestHandleDeletePipeline_NoToken(t *testing.T) {
	s := buildPipelineServer(&mockPipelineServiceClient{})
	r := httptest.NewRequest(http.MethodDelete, "/api/v2/users/me/pipelines/pipe1", nil)
	w := httptest.NewRecorder()
	s.handleDeletePipeline(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestHandleListPipelineRuns_Success(t *testing.T) {
	s := buildPipelineServer(&mockPipelineServiceClient{})
	r := httptest.NewRequest(http.MethodGet, "/api/v2/users/me/pipelines/pipe1/runs?limit=10", nil)
	r = withToken(r, "user1")
	w := httptest.NewRecorder()
	s.handleListPipelineRuns(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleListPipelineRuns_NoToken(t *testing.T) {
	s := buildPipelineServer(&mockPipelineServiceClient{})
	r := httptest.NewRequest(http.MethodGet, "/api/v2/users/me/pipelines/pipe1/runs", nil)
	w := httptest.NewRecorder()
	s.handleListPipelineRuns(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestHandleGetPipelineRun_Success(t *testing.T) {
	s := buildPipelineServer(&mockPipelineServiceClient{})
	r := httptest.NewRequest(http.MethodGet, "/api/v2/users/me/pipelines/pipe1/runs/run1", nil)
	r = withToken(r, "user1")
	w := httptest.NewRecorder()
	s.handleGetPipelineRun(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleGetPipelineRun_NoToken(t *testing.T) {
	s := buildPipelineServer(&mockPipelineServiceClient{})
	r := httptest.NewRequest(http.MethodGet, "/api/v2/users/me/pipelines/pipe1/runs/run1", nil)
	w := httptest.NewRecorder()
	s.handleGetPipelineRun(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestHandleSubmitInput_Success(t *testing.T) {
	s := buildPipelineServer(&mockPipelineServiceClient{})
	body, _ := json.Marshal(map[string]string{"value": "abc"})
	r := httptest.NewRequest(http.MethodPost, "/api/v2/users/me/pending-inputs/inp1/submit", bytes.NewReader(body))
	r = withToken(r, "user1")
	w := httptest.NewRecorder()
	s.handleSubmitInput(w, r)
	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", w.Code)
	}
}

func TestHandleSubmitInput_InvalidJSON(t *testing.T) {
	s := buildPipelineServer(&mockPipelineServiceClient{})
	r := httptest.NewRequest(http.MethodPost, "/api/v2/users/me/pending-inputs/inp1/submit", strings.NewReader("bad"))
	r = withToken(r, "user1")
	w := httptest.NewRecorder()
	s.handleSubmitInput(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleSubmitInput_NoToken(t *testing.T) {
	s := buildPipelineServer(&mockPipelineServiceClient{})
	body, _ := json.Marshal(map[string]string{})
	r := httptest.NewRequest(http.MethodPost, "/api/v2/users/me/pending-inputs/inp1/submit", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleSubmitInput(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestHandleRepostActivity_Success(t *testing.T) {
	s := buildPipelineServer(&mockPipelineServiceClient{})
	r := httptest.NewRequest(http.MethodPost, "/api/v2/users/me/activities/act1/repost", nil)
	r = withToken(r, "user1")
	w := httptest.NewRecorder()
	s.handleRepostActivity(w, r)
	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", w.Code)
	}
}

func TestHandleRepostActivity_ServiceError(t *testing.T) {
	svc := &mockPipelineServiceClient{
		repostActivity: func(_ context.Context, _ *pipelinepb.RepostActivityRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
			return nil, status.Error(codes.NotFound, "activity not found")
		},
	}
	s := buildPipelineServer(svc)
	r := httptest.NewRequest(http.MethodPost, "/api/v2/users/me/activities/act1/repost", nil)
	r = withToken(r, "user1")
	w := httptest.NewRecorder()
	s.handleRepostActivity(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func (m *mockPipelineServiceClient) InvokeEnricher(ctx context.Context, in *pipelinepb.InvokeEnricherRequest, opts ...grpc.CallOption) (*pipelinepb.InvokeEnricherResponse, error) {
	if m.invokeEnricher != nil {
		return m.invokeEnricher(ctx, in, opts...)
	}
	return &pipelinepb.InvokeEnricherResponse{}, nil
}
func (m *mockPipelineServiceClient) AcceptProposedEnricherRun(ctx context.Context, in *pipelinepb.AcceptProposedEnricherRunRequest, opts ...grpc.CallOption) (*pbactivitym.StandardizedActivity, error) {
	if m.acceptProposedEnricherRun != nil {
		return m.acceptProposedEnricherRun(ctx, in, opts...)
	}
	return &pbactivitym.StandardizedActivity{}, nil
}
func (m *mockPipelineServiceClient) DismissProposedEnricherRun(ctx context.Context, in *pipelinepb.DismissProposedEnricherRunRequest, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	if m.dismissProposedEnricherRun != nil {
		return m.dismissProposedEnricherRun(ctx, in, opts...)
	}
	return &emptypb.Empty{}, nil
}
