package strava

import (
	"context"
	"testing"

	"github.com/fitglue/server/src/go/pkg/bootstrap"
	"github.com/fitglue/server/src/go/pkg/description"
	pbevents "github.com/fitglue/server/src/go/pkg/types/pb/models/events"
	pbpipeline "github.com/fitglue/server/src/go/pkg/types/pb/models/pipeline"
	pbplugin "github.com/fitglue/server/src/go/pkg/types/pb/models/plugin"
	"github.com/stretchr/testify/assert"
)

const roxfit = "Completed via ROXFIT\n\n⏱️ Split Times:\nRun 1: 4:54\nRoxzone: 6:14\n"

func TestKeepForeignSourceDescription(t *testing.T) {
	fitglue := "Brutal sleds.\n\n🔥 Calories: 1132 kcal"

	// Never written, payload lacks the existing text (slot 0 was empty): keep it first.
	assert.Equal(t, "Completed via ROXFIT\n\n⏱️ Split Times:\nRun 1: 4:54\nRoxzone: 6:14\n\n"+fitglue,
		keepForeignSourceDescription(roxfit, fitglue, false))

	// Never written, payload already carries it (the enricher refreshed slot 0): verbatim.
	withSlot0 := roxfit + "\n\n" + fitglue
	assert.Equal(t, withSlot0, keepForeignSourceDescription(roxfit, withSlot0, false))

	// Already written: the existing text is FitGlue's own previous output — replace.
	assert.Equal(t, fitglue, keepForeignSourceDescription("old FitGlue text", fitglue, true))

	// Nothing there: the payload stands.
	assert.Equal(t, fitglue, keepForeignSourceDescription("  \n", fitglue, false))

	// Never written and FitGlue has nothing to say: leave the existing text alone.
	assert.Equal(t, roxfit, keepForeignSourceDescription(roxfit, "", false))
}

func TestFitglueWroteActivity(t *testing.T) {
	u := New(&bootstrap.Service{})
	ctx := context.Background()
	success := &pbpipeline.DestinationOutcome{
		Destination: pbplugin.DestinationType_DESTINATION_STRAVA,
		Status:      pbpipeline.DestinationStatus_DESTINATION_STATUS_SUCCESS,
	}
	failed := &pbpipeline.DestinationOutcome{
		Destination: pbplugin.DestinationType_DESTINATION_STRAVA,
		Status:      pbpipeline.DestinationStatus_DESTINATION_STATUS_FAILED,
	}
	key := description.MetadataSourceWrittenByFitGlue

	never := &pbevents.ActivityPayload{Metadata: map[string]string{key: "false"}}
	assert.False(t, u.fitglueWroteActivity(ctx, never, &pbpipeline.PipelineRun{}))
	assert.False(t, u.fitglueWroteActivity(ctx, never, &pbpipeline.PipelineRun{Destinations: []*pbpipeline.DestinationOutcome{failed}}))
	assert.False(t, u.fitglueWroteActivity(ctx, never, nil))

	// Enricher saw an earlier write (any pipeline/run).
	assert.True(t, u.fitglueWroteActivity(ctx, &pbevents.ActivityPayload{Metadata: map[string]string{key: "true"}}, nil))

	// This run already succeeded on Strava (a resume after the first sync).
	assert.True(t, u.fitglueWroteActivity(ctx, never, &pbpipeline.PipelineRun{Destinations: []*pbpipeline.DestinationOutcome{success}}))

	// No metadata (event enriched before the flag existed): falls back to the run outcome.
	legacy := &pbevents.ActivityPayload{Metadata: map[string]string{}}
	assert.False(t, u.fitglueWroteActivity(ctx, legacy, &pbpipeline.PipelineRun{}))
	assert.True(t, u.fitglueWroteActivity(ctx, legacy, &pbpipeline.PipelineRun{Destinations: []*pbpipeline.DestinationOutcome{success}}))
}
