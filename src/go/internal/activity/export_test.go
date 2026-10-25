package activity

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	pbpipeline "github.com/fitglue/server/src/go/pkg/types/pb/models/pipeline"
	pbsvc "github.com/fitglue/server/src/go/pkg/types/pb/services/activity"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestExportPipelineRun_NotFound(t *testing.T) {
	svc := newTestSvc(&MockActivityStore{}, &MockBlobStore{})
	_, err := svc.ExportPipelineRun(context.Background(), &pbsvc.ExportPipelineRunRequest{UserId: "u1", RunId: "r1"})
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", err)
	}
}

func TestExportPipelineRun_BuildsAndSigns(t *testing.T) {
	store := &MockActivityStore{
		GetPipelineRunFunc: func(_ context.Context, _, _ string) (*pbpipeline.PipelineRun, error) {
			return &pbpipeline.PipelineRun{Id: "r1", Title: "Morning Run"}, nil
		},
	}
	var written []byte
	blob := &MockBlobStore{
		WriteFunc: func(_ context.Context, _, _ string, data []byte) error {
			written = data
			return nil
		},
	}
	svc := newTestSvc(store, blob)
	resp, err := svc.ExportPipelineRun(context.Background(), &pbsvc.ExportPipelineRunRequest{UserId: "u1", RunId: "r1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.DownloadUrl == "" {
		t.Error("expected a download URL")
	}
	// The written object should be a valid ZIP containing run.json.
	names := zipEntryNames(t, written)
	if !names["run.json"] || !names["README.txt"] {
		t.Errorf("expected run.json and README.txt in per-run zip, got %v", names)
	}
}

func TestRedactJSON_MasksSensitiveKeys(t *testing.T) {
	in := []byte(`{"name":"ok","access_token":"x","nested":{"refreshToken":"y","keep":"z"},"list":[{"client_secret":"s"}]}`)
	out := string(redactRawJSON(in))
	for _, leaked := range []string{`"x"`, `"y"`, `"s"`} {
		if strings.Contains(out, leaked) {
			t.Errorf("expected sensitive value removed, found in %s", out)
		}
	}
	for _, kept := range []string{`"ok"`, `"z"`} {
		if !strings.Contains(out, kept) {
			t.Errorf("expected non-sensitive value kept, missing from %s", out)
		}
	}
}

func TestExtractFitURI(t *testing.T) {
	if got := extractFitURI([]byte(`{"fit_file_uri":"gs://b/x.fit"}`)); got != "gs://b/x.fit" {
		t.Errorf("snake_case: got %q", got)
	}
	if got := extractFitURI([]byte(`{"fitFileUri":"gs://b/y.fit"}`)); got != "gs://b/y.fit" {
		t.Errorf("camelCase: got %q", got)
	}
	if got := extractFitURI([]byte(`not json`)); got != "" {
		t.Errorf("invalid json: expected empty, got %q", got)
	}
}

// zipEntryNames returns the set of entry names in a zip archive.
func zipEntryNames(t *testing.T, data []byte) map[string]bool {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("failed to read zip: %v", err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	return names
}

// zipEntry returns the bytes of a named entry.
func zipEntry(t *testing.T, data []byte, name string) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("failed to read zip: %v", err)
	}
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				t.Fatalf("open %s: %v", name, err)
			}
			defer rc.Close()
			b, _ := io.ReadAll(rc)
			return b
		}
	}
	t.Fatalf("entry %s not found", name)
	return nil
}
