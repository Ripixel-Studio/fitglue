package pipeline

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	fs "github.com/fitglue/server/src/go/pkg/docstore"
)

func offlineFSPipeline(t *testing.T) *fs.Client {
	t.Helper()
	c, err := offlineDocstore(t)
	if err != nil {
		t.Skipf("firestore client: %v", err)
	}
	return c
}

func TestPipelineFirestoreStore(t *testing.T) {
	c := offlineFSPipeline(t)
	defer c.Close()
	s := NewFirestoreStore(c)
	if s == nil {
		t.Fatal("expected store")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := s.ListPipelines(ctx, "u1"); err == nil {
		t.Error("expected error from unreachable Firestore")
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
