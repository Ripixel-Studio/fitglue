package pipeline

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fitglue/server/src/go/internal/infra"
	"github.com/fitglue/server/src/go/pkg/testing/mocks"
)

func TestNewParkrunChecker(t *testing.T) {
	c := NewParkrunChecker(&mocks.MockDatabase{}, nil, infra.NewLogger())
	if c == nil {
		t.Fatal("expected checker")
	}
}

func TestParkrunChecker_HandleExpire_NoPending(t *testing.T) {
	c := NewParkrunChecker(&mocks.MockDatabase{}, nil, infra.NewLogger())
	c.verifyIdentity = func(_ context.Context, _, _ string) error { return nil }
	w := httptest.NewRecorder()
	c.HandleExpire(w, agentRequest(http.MethodPost, "/internal/parkrun-expire", "ok", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response not JSON: %v", err)
	}
	if body["status"] != "ok" || body["pending"].(float64) != 0 {
		t.Errorf("unexpected body: %v", body)
	}
}
