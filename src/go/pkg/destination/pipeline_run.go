package destination

import (
	"context"

	"github.com/fitglue/server/src/go/internal/infra"

	pbplugin "github.com/fitglue/server/src/go/pkg/types/pb/models/plugin"

	"github.com/fitglue/server/src/go/pkg/types/formatters"

	pbpipeline "github.com/fitglue/server/src/go/pkg/types/pb/models/pipeline"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Database interface subset needed for destination updates
type Database interface {
	UpdatePipelineRun(ctx context.Context, userId string, id string, data map[string]interface{}) error
	SetDestinationOutcome(ctx context.Context, userId string, pipelineRunId string, outcome *pbpipeline.DestinationOutcome) error
	GetDestinationOutcomes(ctx context.Context, userId string, pipelineRunId string) ([]*pbpipeline.DestinationOutcome, error)
}

// UpdateStatus updates a single destination's status using the subcollection pattern.
// Each destination is written as a separate document in the destination_outcomes subcollection,
// eliminating race conditions between parallel uploaders.
// The overall pipeline run status is recomputed from every destination outcome and
// written back to the parent run; nothing is pushed to the user — Jarvis polls the API.
// Parameters:
//   - db: the Database interface for Firestore operations
//   - userId: the user's ID
//   - pipelineRunId: the ID of the pipeline run (same as pipelineExecutionId)
//   - dest: the destination enum value (e.g., DESTINATION_STRAVA)
//   - status: the new status (e.g., DESTINATION_STATUS_SUCCESS, DESTINATION_STATUS_FAILED)
//   - externalId: optional external ID from the destination (e.g., Strava activity ID)
//   - errMsg: optional error message if status is FAILED
//   - logger: logger for debugging
//   - nonBlockingPendingIDs: IDs of non-blocking pending inputs still awaiting user input
func UpdateStatus(ctx context.Context, db Database, userId string, pipelineRunId string, dest pbplugin.DestinationType, status pbpipeline.DestinationStatus, externalId string, errMsg string, logger infra.Logger, nonBlockingPendingIDs []string) {
	if pipelineRunId == "" {
		return // No pipeline run to update - legacy flow
	}

	// Build the outcome
	outcome := &pbpipeline.DestinationOutcome{
		Destination: dest,
		Status:      status,
		CompletedAt: timestamppb.Now(),
	}
	if externalId != "" {
		outcome.ExternalId = &externalId
	}
	if errMsg != "" {
		outcome.Error = &errMsg
	}

	// Write directly to subcollection - each destination has its own document
	// No read-modify-write needed, eliminating race conditions
	if err := db.SetDestinationOutcome(ctx, userId, pipelineRunId, outcome); err != nil {
		logger.Error(ctx, "Failed to set destination outcome", "error", err, "pipeline_run_id", pipelineRunId, "destination", dest.String())
		return
	}

	logger.Debug(ctx, "Set destination outcome in subcollection", "pipeline_run_id", pipelineRunId, "destination", dest.String(), "status", status.String())

	// Now compute and update the overall pipeline status
	// Read all destination outcomes from subcollection
	outcomes, err := db.GetDestinationOutcomes(ctx, userId, pipelineRunId)
	if err != nil {
		logger.Warn(ctx, "Failed to get destination outcomes for status computation", "error", err, "pipeline_run_id", pipelineRunId)
		return
	}

	newStatus := ComputePipelineRunStatus(outcomes, nonBlockingPendingIDs)

	// Convert outcomes to Firestore-compatible format for the inline destinations array
	// This keeps the inline array in sync with the subcollection for UI consumers
	destinationsData := make([]map[string]interface{}, len(outcomes))
	for i, o := range outcomes {
		destData := map[string]interface{}{
			"destination": int32(o.Destination),
			"status":      int32(o.Status),
		}
		if o.ExternalId != nil {
			destData["external_id"] = *o.ExternalId
		}
		if o.Error != nil {
			destData["error"] = *o.Error
		}
		if o.CompletedAt != nil {
			destData["completed_at"] = o.CompletedAt.AsTime()
		}
		destinationsData[i] = destData
	}

	// Update the parent pipeline run's overall status AND inline destinations array.
	// Always write non_blocking_pending_input_ids so resolution clears them correctly.
	updateData := map[string]interface{}{
		"status":                         int32(newStatus),
		"updated_at":                     timestamppb.Now(),
		"destinations":                   destinationsData,
		"non_blocking_pending_input_ids": nonBlockingPendingIDs,
	}

	if err := db.UpdatePipelineRun(ctx, userId, pipelineRunId, updateData); err != nil {
		logger.Error(ctx, "Failed to update pipeline run status", "error", err, "pipeline_run_id", pipelineRunId)
	} else {
		logger.Debug(ctx, "Updated pipeline run status and destinations", "pipeline_run_id", pipelineRunId, "status", newStatus.String(), "destinations_count", len(destinationsData))
	}

}

// FormatDestinationName returns a human-readable name for a destination.
// Delegates to the generated formatters package for consistency across Go and TypeScript.
func FormatDestinationName(dest pbplugin.DestinationType) string {
	return formatters.FormatDestination(dest)
}

// ComputePipelineRunStatus determines overall status from destination outcomes and any
// non-blocking pending inputs still awaiting user input.
func ComputePipelineRunStatus(destinations []*pbpipeline.DestinationOutcome, nonBlockingPendingIDs []string) pbpipeline.PipelineRunStatus {
	if len(destinations) == 0 {
		return pbpipeline.PipelineRunStatus_PIPELINE_RUN_STATUS_RUNNING
	}

	anyFailed := false
	allComplete := true

	for _, d := range destinations {
		switch d.Status {
		case pbpipeline.DestinationStatus_DESTINATION_STATUS_PENDING:
			allComplete = false
		case pbpipeline.DestinationStatus_DESTINATION_STATUS_FAILED:
			anyFailed = true
		case pbpipeline.DestinationStatus_DESTINATION_STATUS_SUCCESS:
			// Good
		case pbpipeline.DestinationStatus_DESTINATION_STATUS_SKIPPED:
			// Skipped doesn't count as failure
		}
	}

	if !allComplete {
		return pbpipeline.PipelineRunStatus_PIPELINE_RUN_STATUS_RUNNING
	}
	if anyFailed {
		return pbpipeline.PipelineRunStatus_PIPELINE_RUN_STATUS_PARTIAL
	}
	if len(nonBlockingPendingIDs) > 0 {
		return pbpipeline.PipelineRunStatus_PIPELINE_RUN_STATUS_SYNCED_WITH_PENDING
	}
	return pbpipeline.PipelineRunStatus_PIPELINE_RUN_STATUS_SYNCED
}
