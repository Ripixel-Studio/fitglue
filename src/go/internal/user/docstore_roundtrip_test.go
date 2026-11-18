package user

import (
	"context"
	"path/filepath"
	"testing"

	firestore "github.com/fitglue/server/src/go/pkg/docstore"
)

// The user store against a real (SQLite) docstore: owner bootstrap, an
// integration written by the OAuth path, lookup by provider id, counters.
func TestFirestoreStore_OnDocstore(t *testing.T) {
	c, err := firestore.Open(filepath.Join(t.TempDir(), "fitglue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s := NewFirestoreStore(c)
	ctx := context.Background()

	if _, err := s.BootstrapOwner(ctx, "owner", "o@example.com", "Owner"); err != nil {
		t.Fatalf("BootstrapOwner: %v", err)
	}
	p, err := s.GetProfile(ctx, "owner")
	if err != nil || p.GetUserId() != "owner" {
		t.Fatalf("GetProfile: %v %v", p, err)
	}
	if err := s.SetIntegration(ctx, "owner", "strava", map[string]interface{}{"enabled": true, "athlete_id": int64(42), "access_token": "t"}); err != nil {
		t.Fatalf("SetIntegration: %v", err)
	}
	ints, err := s.GetIntegrations(ctx, "owner")
	if err != nil || !ints.GetStrava().GetEnabled() || ints.GetStrava().GetAthleteId() != 42 {
		t.Fatalf("GetIntegrations: %v %v", ints, err)
	}
	found, err := s.FindUserByIntegration(ctx, "strava", "42")
	if err != nil || found.GetUserId() != "owner" {
		t.Fatalf("FindUserByIntegration: %v %v", found, err)
	}
	if _, err := s.UpdateCounter(ctx, "owner", "parkrun", 7); err != nil {
		t.Fatalf("UpdateCounter: %v", err)
	}
	counters, err := s.ListCounters(ctx, "owner")
	if err != nil || len(counters) != 1 || counters[0].GetCount() != 7 {
		t.Fatalf("ListCounters: %v %v", counters, err)
	}
	if err := s.DeleteIntegration(ctx, "owner", "strava"); err != nil {
		t.Fatalf("DeleteIntegration: %v", err)
	}
	ints, _ = s.GetIntegrations(ctx, "owner")
	if ints.GetStrava().GetEnabled() {
		t.Fatal("strava still enabled after delete")
	}
}
