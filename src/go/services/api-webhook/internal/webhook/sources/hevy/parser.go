package hevy

import (
	hevymap "github.com/fitglue/server/src/go/pkg/domain/sourcemap/hevy"
	activitypb "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
)

// The mapping lives in pkg/domain/sourcemap/hevy so `fitgluectl reconcile` replays
// a missed workout exactly as this webhook would have. This wrapper keeps this
// package's call sites and tests unchanged.

func mapToStandardizedActivity(rawJSON []byte, userID string, source activitypb.ActivitySource) (*activitypb.StandardizedActivity, error) {
	return hevymap.MapToStandardizedActivity(rawJSON, userID, source)
}
