package firestore

import (
	"path/filepath"
	"testing"

	fs "github.com/fitglue/server/src/go/pkg/docstore"
)

// offlineClient constructs a Firestore client that never connects. Building
// document/collection references is pure path construction and needs no network.
func offlineClient(t *testing.T) *fs.Client {
	t.Helper()
	c, err := offlineDocstore(t)
	if err != nil {
		t.Skipf("could not build firestore client: %v", err)
	}
	return c
}

func TestClient_CollectionAccessors(t *testing.T) {
	raw := offlineClient(t)
	defer raw.Close()
	c := NewClient(raw)

	// Each accessor builds a typed Collection without touching the network.
	if c.Users().Ref == nil {
		t.Error("Users")
	}
	if c.Executions().Ref == nil {
		t.Error("Executions")
	}
	if c.UserExecutions("u1").Ref == nil {
		t.Error("UserExecutions")
	}
	if c.OrphanedExecutions().Ref == nil {
		t.Error("OrphanedExecutions")
	}
	if c.PendingInputs().Ref == nil {
		t.Error("PendingInputs")
	}
	if c.UserPendingInputs("u1").Ref == nil {
		t.Error("UserPendingInputs")
	}
	if c.Counters("u1").Ref == nil {
		t.Error("Counters")
	}
	if c.PersonalRecords("u1").Ref == nil {
		t.Error("PersonalRecords")
	}
	if c.ShowcasedActivities().Ref == nil {
		t.Error("ShowcasedActivities")
	}
	if c.ShowcaseProfiles().Ref == nil {
		t.Error("ShowcaseProfiles")
	}
	if c.ShowcaseProfileEntries("u1").Ref == nil {
		t.Error("ShowcaseProfileEntries")
	}
	if c.UploadedActivities("u1").Ref == nil {
		t.Error("UploadedActivities")
	}
	if c.PipelineRuns("u1").Ref == nil {
		t.Error("PipelineRuns")
	}
	if c.PluginDefaults("u1").Ref == nil {
		t.Error("PluginDefaults")
	}
}

func TestCollection_DocRefs(t *testing.T) {
	raw := offlineClient(t)
	defer raw.Close()
	c := NewClient(raw)

	users := c.Users()
	doc := users.Doc("user-123")
	if doc.ID() != "user-123" {
		t.Errorf("Doc ID = %q", doc.ID())
	}
	if users.NewDoc().Ref == nil {
		t.Error("NewDoc should produce a ref")
	}
}

// offlineDocstore is a store whose database is already closed, so every read
// and write fails fast, as the unreachable Firestore emulator used to.
func offlineDocstore(t *testing.T) (*fs.Client, error) {
	t.Helper()
	c, err := fs.Open(filepath.Join(t.TempDir(), "fitglue.db"))
	if err != nil {
		return nil, err
	}
	_ = c.Close()
	return c, nil
}
