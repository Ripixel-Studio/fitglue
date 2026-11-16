// nolint:proto-json
package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fitglue/server/src/go/internal/infra"
	"github.com/fitglue/server/src/go/pkg/domain/user"
	parkrunutil "github.com/fitglue/server/src/go/pkg/parkrun"
	"github.com/fitglue/server/src/go/pkg/testing/mocks"
	pbpipeline "github.com/fitglue/server/src/go/pkg/types/pb/models/pipeline"
	pbuser "github.com/fitglue/server/src/go/pkg/types/pb/models/user"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// parkrunDB wraps mocks.MockDatabase to override ListPendingInputsByEnricher,
// which has no override hook on the base mock. Everything else delegates to the
// embedded MockDatabase (GetUserFunc / UpdatePendingInputFunc etc.).
type parkrunDB struct {
	*mocks.MockDatabase
	listFn func(ctx context.Context, enricherID string, status pbpipeline.PendingInput_Status) ([]*pbpipeline.PendingInput, error)
}

func (p *parkrunDB) ListPendingInputsByEnricher(ctx context.Context, enricherID string, status pbpipeline.PendingInput_Status) ([]*pbpipeline.PendingInput, error) {
	if p.listFn != nil {
		return p.listFn(ctx, enricherID, status)
	}
	return nil, nil
}

// doCheck runs the expiry pass (POST /internal/parkrun-expire) as the box
// agent would, past the identity check, and returns its tallies.
func doCheck(t *testing.T, c *ParkrunChecker) (int, map[string]interface{}) {
	t.Helper()
	c.verifyIdentity = func(_ context.Context, _, _ string) error { return nil }
	c.fetch = func(_ context.Context, _ *slog.Logger, _, _, _ string, _ time.Time) (*parkrunutil.Result, parkrunutil.FetchDiagnostics, error) {
		t.Fatal("the expiry pass must never fetch")
		return nil, parkrunutil.FetchDiagnostics{}, nil
	}
	w := httptest.NewRecorder()
	c.HandleExpire(w, agentRequest(http.MethodPost, "/internal/parkrun-expire", "ok", nil))
	var body map[string]interface{}
	if w.Body.Len() > 0 {
		_ = json.Unmarshal(w.Body.Bytes(), &body)
	}
	return w.Code, body
}

func TestParkrunChecker_HandleExpire_ListError(t *testing.T) {
	db := &parkrunDB{
		MockDatabase: &mocks.MockDatabase{},
		listFn: func(ctx context.Context, _ string, _ pbpipeline.PendingInput_Status) ([]*pbpipeline.PendingInput, error) {
			return nil, errors.New("firestore down")
		},
	}
	c := NewParkrunChecker(db, nil, infra.NewLogger())
	code, _ := doCheck(t, c)
	if code != http.StatusInternalServerError {
		t.Fatalf("expected 500 on list error, got %d", code)
	}
}

// A past-deadline input is turned into a manual-entry prompt, NOT closed: it stays
// WAITING (so the user can still submit), auto_populated flips to false, and
// provider_metadata.parkrun_results_state becomes EXPIRED. That marker is what
// Jarvis picks up via the pending-inputs API; nothing is pushed.
func TestParkrunChecker_ProcessInput_ExpiredByDeadline(t *testing.T) {
	var gotUpdate map[string]interface{}
	base := &mocks.MockDatabase{
		UpdatePendingInputFunc: func(ctx context.Context, userID, id string, data map[string]interface{}) error {
			gotUpdate = data
			return nil
		},
	}
	past := timestamppb.New(time.Now().Add(-time.Hour))
	db := &parkrunDB{
		MockDatabase: base,
		listFn: func(ctx context.Context, _ string, _ pbpipeline.PendingInput_Status) ([]*pbpipeline.PendingInput, error) {
			return []*pbpipeline.PendingInput{{
				ActivityId:       "i1",
				UserId:           "u1",
				LinkedActivityId: "act-1",
				AutoDeadline:     past,
			}}, nil
		},
	}
	c := NewParkrunChecker(db, nil, infra.NewLogger())
	code, body := doCheck(t, c)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if body["expired"].(float64) != 1 {
		t.Errorf("expected 1 expired, got %v", body["expired"])
	}
	if gotUpdate == nil {
		t.Fatal("expected UpdatePendingInput to be called")
	}
	if got := gotUpdate["status"]; got != int32(pbpipeline.PendingInput_STATUS_WAITING) {
		t.Errorf("expected status WAITING, got %v", got)
	}
	if got := gotUpdate["auto_populated"]; got != false {
		t.Errorf("expected auto_populated=false, got %v", got)
	}
	meta, ok := gotUpdate["provider_metadata"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected provider_metadata nested map, got %T", gotUpdate["provider_metadata"])
	}
	if meta["parkrun_results_state"] != "EXPIRED" {
		t.Errorf("expected parkrun_results_state=EXPIRED, got %v", meta["parkrun_results_state"])
	}
}

// Once an input is EXPIRED it stays WAITING for manual submit but must leave the
// auto-poll set: a later checker run skips it entirely — no re-expire, no fetch.
func TestParkrunChecker_ProcessInput_AlreadyExpiredSkips(t *testing.T) {
	base := &mocks.MockDatabase{
		UpdatePendingInputFunc: func(ctx context.Context, userID, id string, data map[string]interface{}) error {
			t.Error("EXPIRED input must not be updated again")
			return nil
		},
		GetUserFunc: func(ctx context.Context, id string) (*user.Record, error) {
			t.Error("EXPIRED input must not trigger a user lookup / fetch")
			return parkrunUser(), nil
		},
	}
	past := timestamppb.New(time.Now().Add(-time.Hour))
	db := &parkrunDB{
		MockDatabase: base,
		listFn: func(ctx context.Context, _ string, _ pbpipeline.PendingInput_Status) ([]*pbpipeline.PendingInput, error) {
			return []*pbpipeline.PendingInput{{
				ActivityId:   "i1",
				UserId:       "u1",
				AutoDeadline: past, // past deadline, but already EXPIRED so it's skipped before that check
				ProviderMetadata: map[string]string{
					"parkrun_results_state": "EXPIRED",
				},
			}}, nil
		},
	}
	c := NewParkrunChecker(db, nil, infra.NewLogger())
	code, body := doCheck(t, c)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if body["skipped"].(float64) != 1 {
		t.Errorf("expected 1 skipped for already-EXPIRED input, got %v", body["skipped"])
	}
	if body["expired"].(float64) != 0 {
		t.Errorf("expected 0 expired for already-EXPIRED input, got %v", body["expired"])
	}
}

func TestParkrunChecker_ProcessInput_DeadlineUpdateErrorStillExpires(t *testing.T) {
	base := &mocks.MockDatabase{
		UpdatePendingInputFunc: func(ctx context.Context, userID, id string, data map[string]interface{}) error {
			return errors.New("write failed")
		},
	}
	past := timestamppb.New(time.Now().Add(-time.Hour))
	db := &parkrunDB{
		MockDatabase: base,
		listFn: func(ctx context.Context, _ string, _ pbpipeline.PendingInput_Status) ([]*pbpipeline.PendingInput, error) {
			return []*pbpipeline.PendingInput{{ActivityId: "i1", UserId: "u1", AutoDeadline: past}}, nil
		},
	}
	c := NewParkrunChecker(db, nil, infra.NewLogger())
	code, body := doCheck(t, c)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if body["expired"].(float64) != 1 {
		t.Errorf("expected expired even when update errors, got %v", body["expired"])
	}
}

func TestParkrunChecker_ProcessInput_GetUserError(t *testing.T) {
	base := &mocks.MockDatabase{
		GetUserFunc: func(ctx context.Context, id string) (*user.Record, error) {
			return nil, errors.New("user lookup failed")
		},
	}
	db := &parkrunDB{
		MockDatabase: base,
		listFn: func(ctx context.Context, _ string, _ pbpipeline.PendingInput_Status) ([]*pbpipeline.PendingInput, error) {
			return []*pbpipeline.PendingInput{{ActivityId: "i1", UserId: "u1"}}, nil
		},
	}
	c := NewParkrunChecker(db, nil, infra.NewLogger())
	code, body := doCheck(t, c)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if body["skipped"].(float64) != 1 {
		t.Errorf("expected 1 skipped on get-user error, got %v", body["skipped"])
	}
}

func TestParkrunChecker_ProcessInput_NoIntegrationExpires(t *testing.T) {
	expired := false
	base := &mocks.MockDatabase{
		GetUserFunc: func(ctx context.Context, id string) (*user.Record, error) {
			// Integrations nil → no parkrun → expire immediately.
			return &user.Record{}, nil
		},
		UpdatePendingInputFunc: func(ctx context.Context, userID, id string, data map[string]interface{}) error {
			expired = true
			return nil
		},
	}
	db := &parkrunDB{
		MockDatabase: base,
		listFn: func(ctx context.Context, _ string, _ pbpipeline.PendingInput_Status) ([]*pbpipeline.PendingInput, error) {
			return []*pbpipeline.PendingInput{{ActivityId: "i1", UserId: "u1"}}, nil
		},
	}
	c := NewParkrunChecker(db, nil, infra.NewLogger())
	code, body := doCheck(t, c)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if body["expired"].(float64) != 1 {
		t.Errorf("expected 1 expired on missing integration, got %v", body["expired"])
	}
	if !expired {
		t.Error("expected pending input to be expired")
	}
}

func parkrunUser() *user.Record {
	return &user.Record{
		Integrations: &pbuser.UserIntegrations{
			Parkrun: &pbuser.ParkrunIntegration{
				Enabled:    true,
				AthleteId:  "12345",
				CountryUrl: "parkrun.org.uk",
			},
		},
	}
}

func TestParkrunChecker_ProcessInput_MissingMetadataSkips(t *testing.T) {
	base := &mocks.MockDatabase{
		GetUserFunc: func(ctx context.Context, id string) (*user.Record, error) { return parkrunUser(), nil },
	}
	db := &parkrunDB{
		MockDatabase: base,
		listFn: func(ctx context.Context, _ string, _ pbpipeline.PendingInput_Status) ([]*pbpipeline.PendingInput, error) {
			return []*pbpipeline.PendingInput{{
				ActivityId:       "i1",
				UserId:           "u1",
				ProviderMetadata: map[string]string{}, // no slug/date
			}}, nil
		},
	}
	c := NewParkrunChecker(db, nil, infra.NewLogger())
	code, body := doCheck(t, c)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if body["skipped"].(float64) != 1 {
		t.Errorf("expected 1 skipped on missing metadata, got %v", body["skipped"])
	}
}

func TestParkrunChecker_ProcessInput_BadDateSkips(t *testing.T) {
	base := &mocks.MockDatabase{
		GetUserFunc: func(ctx context.Context, id string) (*user.Record, error) { return parkrunUser(), nil },
	}
	db := &parkrunDB{
		MockDatabase: base,
		listFn: func(ctx context.Context, _ string, _ pbpipeline.PendingInput_Status) ([]*pbpipeline.PendingInput, error) {
			return []*pbpipeline.PendingInput{{
				ActivityId: "i1",
				UserId:     "u1",
				ProviderMetadata: map[string]string{
					"parkrun_event_slug": "bushy",
					"expected_date":      "not-a-date",
				},
			}}, nil
		},
	}
	c := NewParkrunChecker(db, nil, infra.NewLogger())
	code, body := doCheck(t, c)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if body["skipped"].(float64) != 1 {
		t.Errorf("expected 1 skipped on bad date, got %v", body["skipped"])
	}
}

// An input still inside its deadline is left to the agent's fetch: the expiry
// pass counts it as polling and neither updates nor fetches it.
func TestParkrunChecker_HandleExpire_InsideDeadlineLeftPolling(t *testing.T) {
	base := &mocks.MockDatabase{
		GetUserFunc: func(ctx context.Context, id string) (*user.Record, error) { return parkrunUser(), nil },
		UpdatePendingInputFunc: func(ctx context.Context, userID, id string, data map[string]interface{}) error {
			t.Error("an input inside its deadline must not be updated")
			return nil
		},
	}
	db := &parkrunDB{
		MockDatabase: base,
		listFn: func(ctx context.Context, _ string, _ pbpipeline.PendingInput_Status) ([]*pbpipeline.PendingInput, error) {
			return []*pbpipeline.PendingInput{{
				ActivityId:   "i1",
				UserId:       "u1",
				AutoDeadline: timestamppb.New(time.Now().Add(time.Hour)),
				ProviderMetadata: map[string]string{
					"parkrun_event_slug": "bushy",
					"expected_date":      "26/07/2025",
				},
			}}, nil
		},
	}
	c := NewParkrunChecker(db, nil, infra.NewLogger())
	code, body := doCheck(t, c)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if body["polling"].(float64) != 1 || body["expired"].(float64) != 0 || body["skipped"].(float64) != 0 {
		t.Errorf("want polling=1 expired=0 skipped=0, got %v", body)
	}
}

func TestParkrunChecker_HandleExpire_Auth(t *testing.T) {
	c := NewParkrunChecker(&mocks.MockDatabase{}, nil, infra.NewLogger())
	w := httptest.NewRecorder()
	c.HandleExpire(w, agentRequest(http.MethodPost, "/internal/parkrun-expire", "", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expire without token: got %d, want 401", w.Code)
	}
	c.verifyIdentity = func(_ context.Context, _, _ string) error { return errors.New("bad") }
	w = httptest.NewRecorder()
	c.HandleExpire(w, agentRequest(http.MethodPost, "/internal/parkrun-expire", "x.y.z", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("expire with rejected token: got %d, want 403", w.Code)
	}
	c.verifyIdentity = func(_ context.Context, _, _ string) error { return nil }
	w = httptest.NewRecorder()
	c.HandleExpire(w, agentRequest(http.MethodGet, "/internal/parkrun-expire", "ok", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expire by GET: got %d, want 405", w.Code)
	}
}
