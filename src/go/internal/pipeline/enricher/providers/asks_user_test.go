package providers_test

import (
	"testing"

	"github.com/fitglue/server/src/go/internal/pipeline/enricher/providers"
	"github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/hdrop"
	"github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/manual_workout_entry"
	"github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/photo_upload"
	"github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/weather"
)

// A run forced partial by a lagging data source must still ask the owner (2026-11-05).
func TestLagForcedRunStillAsks(t *testing.T) {
	asking := []providers.Provider{&hdrop.Provider{}, &photo_upload.Provider{}, &manual_workout_entry.Provider{}}
	for _, p := range asking {
		if providers.DoNotRetryFor(p, true) {
			t.Errorf("%s: lag-forced run passed doNotRetry=true, so it would skip its question", p.Name())
		}
	}
}

func TestDataProvidersStillStopRetrying(t *testing.T) {
	p := weather.NewWeather()
	if !providers.DoNotRetryFor(p, true) {
		t.Error("weather: doNotRetry must pass through to data providers")
	}
	if providers.DoNotRetryFor(p, false) {
		t.Error("weather: doNotRetry=false must stay false")
	}
}
