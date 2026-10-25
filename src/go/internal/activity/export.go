package activity

import (
	"context"
	"fmt"
	"time"

	pbsvc "github.com/fitglue/server/src/go/pkg/types/pb/services/activity"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// exportSignedURLExpiry is how long whole-account and per-run download links live.
const exportSignedURLExpiry = 24 * time.Hour

// ExportPipelineRun synchronously builds a per-run ZIP and returns a signed URL.
func (s *Service) ExportPipelineRun(ctx context.Context, req *pbsvc.ExportPipelineRunRequest) (*pbsvc.ExportPipelineRunResponse, error) {
	if req.UserId == "" || req.RunId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and run_id are required")
	}

	run, err := s.store.GetPipelineRun(ctx, req.UserId, req.RunId)
	if err != nil {
		s.logger.Error(ctx, "ExportPipelineRun: failed to get run", "userId", req.UserId, "runId", req.RunId, "error", err)
		return nil, status.Error(codes.Internal, "failed to load pipeline run")
	}
	if run == nil {
		return nil, status.Error(codes.NotFound, "pipeline run not found")
	}

	data, err := s.buildRunArchive(ctx, run)
	if err != nil {
		s.logger.Error(ctx, "ExportPipelineRun: failed to build archive", "userId", req.UserId, "runId", req.RunId, "error", err)
		return nil, status.Error(codes.Internal, "failed to build export")
	}

	objectPath := fmt.Sprintf("exports/%s/runs/%s-%d.zip", req.UserId, sanitizeDir(req.RunId), time.Now().UnixMilli())
	if err := s.blobStore.Write(ctx, s.bucketName, objectPath, data); err != nil {
		s.logger.Error(ctx, "ExportPipelineRun: failed to write archive", "userId", req.UserId, "error", err)
		return nil, status.Error(codes.Internal, "failed to store export")
	}

	url, err := s.blobStore.SignedURL(ctx, s.bucketName, objectPath, "", 0, exportSignedURLExpiry)
	if err != nil {
		s.logger.Error(ctx, "ExportPipelineRun: failed to sign URL", "userId", req.UserId, "error", err)
		return nil, status.Error(codes.Internal, "failed to generate download link")
	}

	return &pbsvc.ExportPipelineRunResponse{
		DownloadUrl: url,
		SizeBytes:   int64(len(data)),
		ExpiresAt:   time.Now().UTC().Add(exportSignedURLExpiry).Format(time.RFC3339),
	}, nil
}
