package siteexport

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/fitglue/server/src/go/internal/activity"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	pbpipeline "github.com/fitglue/server/src/go/pkg/types/pb/models/pipeline"
	pbplugin "github.com/fitglue/server/src/go/pkg/types/pb/models/plugin"
	pbsvc "github.com/fitglue/server/src/go/pkg/types/pb/services/activity"
)

// ServiceSource reads through the activity service — the same code path the
// public showcase pages use — so hydration, owner metadata and topPrs come out
// exactly as api-public serves them (contract §2, §3.1).
type ServiceSource struct {
	Service *activity.Service
	Store   activity.ActivityStore
}

// profilePage is the page requested from GetPublicShowcaseProfile. The export
// discards profile.entries (it lists every entry itself), so the page does not
// matter for content; page 1 additionally kicks off a background Firestore
// write (the lazy streak-history backfill) which a read-only export — and a
// --dry-run in particular — must not trigger.
const profilePage = 2

func (s ServiceSource) Profile(ctx context.Context, slug string) (*pbactivity.ShowcaseProfile, error) {
	res, err := s.Service.GetPublicShowcaseProfile(ctx, &pbsvc.GetPublicShowcaseProfileRequest{Slug: slug, Page: profilePage})
	if err != nil {
		return nil, err
	}
	return res.Profile, nil
}

func (s ServiceSource) Entries(ctx context.Context, userID string) ([]*pbactivity.ShowcaseProfileEntry, error) {
	return s.Store.ListShowcaseProfileEntries(ctx, userID)
}

func (s ServiceSource) Showcase(ctx context.Context, showcaseID string) (*pbactivity.ShowcasedActivity, error) {
	sc, err := s.Service.GetPublicShowcase(ctx, &pbsvc.GetPublicShowcaseRequest{ShowcaseId: showcaseID})
	if status.Code(err) == codes.NotFound {
		return nil, nil
	}
	return sc, err
}

func (s ServiceSource) Roundups(ctx context.Context, slug string) ([]*pbactivity.ShowcaseRoundup, error) {
	return s.Store.ListAllRoundups(ctx, slug)
}

func (s ServiceSource) Roundup(ctx context.Context, slug, periodKey string) (*pbactivity.ShowcaseRoundup, error) {
	return s.Store.GetRoundup(ctx, slug, periodKey)
}

// StravaID reads the activity's pipeline run and returns the external id of its
// successful Strava upload, else "". The owner comes from the showcase document
// (the public showcase carries no user id).
func (s ServiceSource) StravaID(ctx context.Context, sc *pbactivity.ShowcasedActivity) (string, error) {
	if sc == nil || sc.GetPipelineExecutionId() == "" {
		return "", nil
	}
	_, owner, err := s.Store.GetPublicShowcase(ctx, sc.ShowcaseId)
	if err != nil || owner == "" {
		return "", err
	}
	run, err := s.Store.GetPipelineRun(ctx, owner, sc.GetPipelineExecutionId())
	if err != nil || run == nil {
		return "", err
	}
	for _, d := range run.GetDestinations() {
		if d.GetDestination() == pbplugin.DestinationType_DESTINATION_STRAVA &&
			d.GetStatus() == pbpipeline.DestinationStatus_DESTINATION_STATUS_SUCCESS {
			return d.GetExternalId(), nil
		}
	}
	return "", nil
}
