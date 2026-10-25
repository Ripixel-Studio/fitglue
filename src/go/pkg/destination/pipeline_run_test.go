package destination

import (
	"context"
	"errors"
	"testing"

	pbplugin "github.com/fitglue/server/src/go/pkg/types/pb/models/plugin"

	pbpipeline "github.com/fitglue/server/src/go/pkg/types/pb/models/pipeline"

	"github.com/fitglue/server/src/go/internal/infra"
)

// --- Mock Database ---

type MockDatabase struct {
	Outcomes       []*pbpipeline.DestinationOutcome
	SetOutcomeFunc func(ctx context.Context, userId string, pipelineRunId string, outcome *pbpipeline.DestinationOutcome) error
	UpdateRunFunc  func(ctx context.Context, userId string, id string, data map[string]interface{}) error
}

func (m *MockDatabase) SetDestinationOutcome(ctx context.Context, userId string, pipelineRunId string, outcome *pbpipeline.DestinationOutcome) error {
	if m.SetOutcomeFunc != nil {
		return m.SetOutcomeFunc(ctx, userId, pipelineRunId, outcome)
	}
	m.Outcomes = append(m.Outcomes, outcome)
	return nil
}

func (m *MockDatabase) GetDestinationOutcomes(ctx context.Context, userId string, pipelineRunId string) ([]*pbpipeline.DestinationOutcome, error) {
	return m.Outcomes, nil
}

func (m *MockDatabase) UpdatePipelineRun(ctx context.Context, userId string, id string, data map[string]interface{}) error {
	if m.UpdateRunFunc != nil {
		return m.UpdateRunFunc(ctx, userId, id, data)
	}
	return nil
}

// --- Tests ---

func TestComputePipelineRunStatus_Empty(t *testing.T) {
	status := ComputePipelineRunStatus(nil, nil)
	if status != pbpipeline.PipelineRunStatus_PIPELINE_RUN_STATUS_RUNNING {
		t.Errorf("expected RUNNING for empty outcomes, got %v", status)
	}
}

func TestComputePipelineRunStatus_AllSuccess(t *testing.T) {
	outcomes := []*pbpipeline.DestinationOutcome{
		{Destination: pbplugin.DestinationType_DESTINATION_STRAVA, Status: pbpipeline.DestinationStatus_DESTINATION_STATUS_SUCCESS},
		{Destination: pbplugin.DestinationType_DESTINATION_HEVY, Status: pbpipeline.DestinationStatus_DESTINATION_STATUS_SUCCESS},
	}
	status := ComputePipelineRunStatus(outcomes, nil)
	if status != pbpipeline.PipelineRunStatus_PIPELINE_RUN_STATUS_SYNCED {
		t.Errorf("expected SYNCED, got %v", status)
	}
}

func TestComputePipelineRunStatus_SomePending(t *testing.T) {
	outcomes := []*pbpipeline.DestinationOutcome{
		{Destination: pbplugin.DestinationType_DESTINATION_STRAVA, Status: pbpipeline.DestinationStatus_DESTINATION_STATUS_SUCCESS},
		{Destination: pbplugin.DestinationType_DESTINATION_HEVY, Status: pbpipeline.DestinationStatus_DESTINATION_STATUS_PENDING},
	}
	status := ComputePipelineRunStatus(outcomes, nil)
	if status != pbpipeline.PipelineRunStatus_PIPELINE_RUN_STATUS_RUNNING {
		t.Errorf("expected RUNNING, got %v", status)
	}
}

func TestComputePipelineRunStatus_SomeFailed(t *testing.T) {
	outcomes := []*pbpipeline.DestinationOutcome{
		{Destination: pbplugin.DestinationType_DESTINATION_STRAVA, Status: pbpipeline.DestinationStatus_DESTINATION_STATUS_SUCCESS},
		{Destination: pbplugin.DestinationType_DESTINATION_HEVY, Status: pbpipeline.DestinationStatus_DESTINATION_STATUS_FAILED},
	}
	status := ComputePipelineRunStatus(outcomes, nil)
	if status != pbpipeline.PipelineRunStatus_PIPELINE_RUN_STATUS_PARTIAL {
		t.Errorf("expected PARTIAL, got %v", status)
	}
}

func TestComputePipelineRunStatus_SuccessAndSkipped(t *testing.T) {
	outcomes := []*pbpipeline.DestinationOutcome{
		{Destination: pbplugin.DestinationType_DESTINATION_STRAVA, Status: pbpipeline.DestinationStatus_DESTINATION_STATUS_SUCCESS},
		{Destination: pbplugin.DestinationType_DESTINATION_HEVY, Status: pbpipeline.DestinationStatus_DESTINATION_STATUS_SKIPPED},
	}
	status := ComputePipelineRunStatus(outcomes, nil)
	if status != pbpipeline.PipelineRunStatus_PIPELINE_RUN_STATUS_SYNCED {
		t.Errorf("expected SYNCED (skipped doesn't count as failure), got %v", status)
	}
}

// UpdateStatus writes the outcome and recomputes the parent run's status. Nothing is
// pushed to the user — Jarvis polls the API for run state.
func TestUpdateStatus_WritesOutcomeAndRecomputesStatus(t *testing.T) {
	var gotStatus int32
	db := &MockDatabase{
		Outcomes: []*pbpipeline.DestinationOutcome{
			{Destination: pbplugin.DestinationType_DESTINATION_STRAVA, Status: pbpipeline.DestinationStatus_DESTINATION_STATUS_SUCCESS},
		},
		UpdateRunFunc: func(ctx context.Context, userId string, id string, data map[string]interface{}) error {
			gotStatus = data["status"].(int32)
			return nil
		},
	}
	logger := infra.NewLogger()

	UpdateStatus(context.Background(), db, "user1", "run1",
		pbplugin.DestinationType_DESTINATION_HEVY, pbpipeline.DestinationStatus_DESTINATION_STATUS_SUCCESS,
		"hevy-123", "", logger, nil)

	if len(db.Outcomes) != 2 {
		t.Fatalf("expected the Hevy outcome to be appended, got %d outcomes", len(db.Outcomes))
	}
	if got := db.Outcomes[1]; got.ExternalId == nil || *got.ExternalId != "hevy-123" {
		t.Errorf("expected external id to be recorded, got %v", got.ExternalId)
	}
	if gotStatus != int32(pbpipeline.PipelineRunStatus_PIPELINE_RUN_STATUS_SYNCED) {
		t.Errorf("expected SYNCED, got %v", pbpipeline.PipelineRunStatus(gotStatus))
	}
}

func TestUpdateStatus_FailedOutcomeYieldsPartial(t *testing.T) {
	var gotStatus int32
	db := &MockDatabase{
		Outcomes: []*pbpipeline.DestinationOutcome{
			{Destination: pbplugin.DestinationType_DESTINATION_STRAVA, Status: pbpipeline.DestinationStatus_DESTINATION_STATUS_SUCCESS},
		},
		UpdateRunFunc: func(ctx context.Context, userId string, id string, data map[string]interface{}) error {
			gotStatus = data["status"].(int32)
			return nil
		},
	}
	logger := infra.NewLogger()

	UpdateStatus(context.Background(), db, "user1", "run1",
		pbplugin.DestinationType_DESTINATION_HEVY, pbpipeline.DestinationStatus_DESTINATION_STATUS_FAILED,
		"", "API error", logger, nil)

	if got := db.Outcomes[1]; got.Error == nil || *got.Error != "API error" {
		t.Errorf("expected error message to be recorded, got %v", got.Error)
	}
	if gotStatus != int32(pbpipeline.PipelineRunStatus_PIPELINE_RUN_STATUS_PARTIAL) {
		t.Errorf("expected PARTIAL, got %v", pbpipeline.PipelineRunStatus(gotStatus))
	}
}

// All destinations succeed but a non-blocking input is still pending → SYNCED_WITH_PENDING,
// and the pending IDs are written back so resolution can clear them.
func TestUpdateStatus_SyncedWithPendingWritesPendingIDs(t *testing.T) {
	var gotData map[string]interface{}
	db := &MockDatabase{
		Outcomes: []*pbpipeline.DestinationOutcome{
			{Destination: pbplugin.DestinationType_DESTINATION_STRAVA, Status: pbpipeline.DestinationStatus_DESTINATION_STATUS_SUCCESS},
		},
		UpdateRunFunc: func(ctx context.Context, userId string, id string, data map[string]interface{}) error {
			gotData = data
			return nil
		},
	}
	logger := infra.NewLogger()

	UpdateStatus(context.Background(), db, "user1", "run1",
		pbplugin.DestinationType_DESTINATION_HEVY, pbpipeline.DestinationStatus_DESTINATION_STATUS_SUCCESS,
		"hevy-123", "", logger, []string{"pending-1"})

	if gotData["status"].(int32) != int32(pbpipeline.PipelineRunStatus_PIPELINE_RUN_STATUS_SYNCED_WITH_PENDING) {
		t.Errorf("expected SYNCED_WITH_PENDING, got %v", gotData["status"])
	}
	ids, _ := gotData["non_blocking_pending_input_ids"].([]string)
	if len(ids) != 1 || ids[0] != "pending-1" {
		t.Errorf("expected pending ids to be written back, got %v", gotData["non_blocking_pending_input_ids"])
	}
}

func TestUpdateStatus_NoPipelineRunId(t *testing.T) {
	var updated bool
	db := &MockDatabase{UpdateRunFunc: func(ctx context.Context, userId string, id string, data map[string]interface{}) error {
		updated = true
		return nil
	}}
	logger := infra.NewLogger()

	// Empty pipelineRunId → early return (legacy flow)
	UpdateStatus(context.Background(), db, "user1", "",
		pbplugin.DestinationType_DESTINATION_STRAVA, pbpipeline.DestinationStatus_DESTINATION_STATUS_SUCCESS,
		"strava-123", "", logger, nil)

	if updated || len(db.Outcomes) != 0 {
		t.Fatal("expected no writes for empty pipelineRunId")
	}
}

func TestUpdateStatus_OutcomeWriteErrorStopsBeforeRunUpdate(t *testing.T) {
	var updated bool
	db := &MockDatabase{
		SetOutcomeFunc: func(ctx context.Context, userId string, pipelineRunId string, outcome *pbpipeline.DestinationOutcome) error {
			return errors.New("firestore down")
		},
		UpdateRunFunc: func(ctx context.Context, userId string, id string, data map[string]interface{}) error {
			updated = true
			return nil
		},
	}

	UpdateStatus(context.Background(), db, "user1", "run1",
		pbplugin.DestinationType_DESTINATION_STRAVA, pbpipeline.DestinationStatus_DESTINATION_STATUS_SUCCESS,
		"strava-1", "", infra.NewLogger(), nil)

	if updated {
		t.Fatal("run status must not be recomputed when the outcome write failed")
	}
}

func TestUpdateStatus_RunUpdateErrorIsLoggedNotFatal(t *testing.T) {
	db := &MockDatabase{
		Outcomes: []*pbpipeline.DestinationOutcome{
			{Destination: pbplugin.DestinationType_DESTINATION_STRAVA, Status: pbpipeline.DestinationStatus_DESTINATION_STATUS_SUCCESS},
		},
		UpdateRunFunc: func(ctx context.Context, userId string, id string, data map[string]interface{}) error {
			return errors.New("firestore down")
		},
	}

	// Must not panic; the outcome itself is still recorded.
	UpdateStatus(context.Background(), db, "user1", "run1",
		pbplugin.DestinationType_DESTINATION_HEVY, pbpipeline.DestinationStatus_DESTINATION_STATUS_SUCCESS,
		"hevy-1", "", infra.NewLogger(), nil)

	if len(db.Outcomes) != 2 {
		t.Fatalf("expected outcome to be recorded even when the run update fails, got %d", len(db.Outcomes))
	}
}

func TestFormatDestinationName(t *testing.T) {
	tests := []struct {
		dest     pbplugin.DestinationType
		expected string
	}{
		{pbplugin.DestinationType_DESTINATION_STRAVA, "Strava"},
		{pbplugin.DestinationType_DESTINATION_HEVY, "Hevy"},
		{pbplugin.DestinationType_DESTINATION_SHOWCASE, "Showcase"},
		{pbplugin.DestinationType_DESTINATION_TRAININGPEAKS, "TrainingPeaks"},
		{pbplugin.DestinationType_DESTINATION_INTERVALS, "Intervals.icu"},
		{pbplugin.DestinationType_DESTINATION_GOOGLESHEETS, "Google Sheets"},
		{pbplugin.DestinationType_DESTINATION_GITHUB, "GitHub"},
		{pbplugin.DestinationType_DESTINATION_MOCK, "Mock"},
	}
	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			got := FormatDestinationName(tt.dest)
			if got != tt.expected {
				t.Errorf("FormatDestinationName(%v) = %q, want %q", tt.dest, got, tt.expected)
			}
		})
	}
}
