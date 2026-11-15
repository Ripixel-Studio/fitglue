// nolint:proto-json
package strava_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fitglue/server/src/go/services/api-webhook/internal/webhook/sources/strava"
	"github.com/stretchr/testify/assert"
)

func TestVerifySubscription(t *testing.T) {
	provider := strava.NewProvider("secret-token", nil)

	t.Run("success", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?hub.mode=subscribe&hub.verify_token=secret-token&hub.challenge=12345", nil)
		rec := httptest.NewRecorder()

		provider.VerifySubscription(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "{\"hub.challenge\":\"12345\"}\n", rec.Body.String())
	})

	t.Run("invalid token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?hub.mode=subscribe&hub.verify_token=wrong-token&hub.challenge=12345", nil)
		rec := httptest.NewRecorder()

		provider.VerifySubscription(rec, req)

		assert.Equal(t, http.StatusForbidden, rec.Code)
	})
}

func TestParseEvent(t *testing.T) {
	provider := strava.NewProvider("secret-token", nil)

	t.Run("valid activity create", func(t *testing.T) {
		payload := map[string]interface{}{
			"object_type": "activity",
			"object_id":   123456,
			"aspect_type": "create",
			"owner_id":    98765,
		}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBuffer(body))

		events, err := provider.ParseEvent(req)

		assert.NoError(t, err)
		assert.Len(t, events, 1)
		assert.Equal(t, "strava", events[0].Provider)
		assert.Equal(t, "98765", events[0].ProviderUID)
		assert.Equal(t, "123456", events[0].ActivityID)
		assert.Equal(t, "create", events[0].Event)
	})

	t.Run("ignore non-activity", func(t *testing.T) {
		payload := map[string]interface{}{
			"object_type": "athlete",
			"object_id":   123456,
			"aspect_type": "create",
			"owner_id":    98765,
		}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBuffer(body))

		events, err := provider.ParseEvent(req)

		assert.NoError(t, err)
		assert.Empty(t, events)
	})
}

// Only "create" runs the pipeline: an update (a rename, a type or privacy change) or a
// delete returns no event, so the processor acknowledges it and does nothing.
func TestParseEvent_OnlyCreateRunsThePipeline(t *testing.T) {
	provider := strava.NewProvider("secret-token", nil)
	cases := []struct {
		name    string
		payload map[string]interface{}
		events  int
	}{
		{"create", map[string]interface{}{"object_type": "activity", "object_id": 1, "aspect_type": "create", "owner_id": 2}, 1},
		{"update title", map[string]interface{}{"object_type": "activity", "object_id": 1, "aspect_type": "update", "owner_id": 2, "updates": map[string]interface{}{"title": "Newark parkrun (#13)"}}, 0},
		{"update type and privacy", map[string]interface{}{"object_type": "activity", "object_id": 1, "aspect_type": "update", "owner_id": 2, "updates": map[string]interface{}{"type": "Run", "sport_type": "Run", "private": "true", "visibility": "only_me"}}, 0},
		{"update without updates", map[string]interface{}{"object_type": "activity", "object_id": 1, "aspect_type": "update", "owner_id": 2}, 0},
		{"delete", map[string]interface{}{"object_type": "activity", "object_id": 1, "aspect_type": "delete", "owner_id": 2}, 0},
		{"athlete deauthorize", map[string]interface{}{"object_type": "athlete", "object_id": 2, "aspect_type": "update", "owner_id": 2, "updates": map[string]interface{}{"authorized": "false"}}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(tc.payload)
			events, err := provider.ParseEvent(httptest.NewRequest(http.MethodPost, "/", bytes.NewBuffer(body)))
			assert.NoError(t, err)
			assert.Len(t, events, tc.events)
		})
	}
}
