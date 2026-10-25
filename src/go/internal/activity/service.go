package activity

import (
	"context"

	"github.com/fitglue/server/src/go/internal/infra"
	pbsvc "github.com/fitglue/server/src/go/pkg/types/pb/services/activity"
)

// SiteExporter is told after the durable writes that change what the public
// site shows (fitness-zine S1.2): a showcase profile entry persisted by
// AddShowcaseEntry, a roundup saved by generateRoundup. Implementations must
// return at once and never fail the caller — siteexport.Hook runs the export
// in the background and logs failures. Nil means the site export is off.
type SiteExporter interface {
	ActivityChanged(ctx context.Context, showcaseID string)
	RoundupChanged(ctx context.Context, slug, periodKey string)
}

// Service implements the pbsvc.ActivityServiceServer interface.
type Service struct {
	pbsvc.UnimplementedActivityServiceServer
	store                ActivityStore
	blobStore            BlobStore
	publisher            Publisher
	bucketName           string
	showcaseAssetsBucket string
	logger               infra.Logger
	siteExporter         SiteExporter
}

func NewService(store ActivityStore, blobStore BlobStore, publisher Publisher, bucketName string, showcaseAssetsBucket string, logger infra.Logger) *Service {
	return &Service{
		store:                store,
		blobStore:            blobStore,
		publisher:            publisher,
		bucketName:           bucketName,
		showcaseAssetsBucket: showcaseAssetsBucket,
		logger:               logger,
	}
}

// SetSiteExporter installs the site-export hook. It is set once at boot by
// the activity service's main, after the Service exists, because the hook
// reads back through this same Service (siteexport.ServiceSource).
func (s *Service) SetSiteExporter(x SiteExporter) {
	s.siteExporter = x
}

func (s *Service) siteActivityChanged(ctx context.Context, showcaseID string) {
	if s.siteExporter != nil {
		s.siteExporter.ActivityChanged(ctx, showcaseID)
	}
}

func (s *Service) siteRoundupChanged(ctx context.Context, slug, periodKey string) {
	if s.siteExporter != nil {
		s.siteExporter.RoundupChanged(ctx, slug, periodKey)
	}
}
