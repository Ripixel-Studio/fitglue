// nolint:proto-json
package main

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	firestore "cloud.google.com/go/firestore"
	"cloud.google.com/go/pubsub" //nolint:staticcheck // v1 client, same as the services
	"github.com/google/uuid"
	"google.golang.org/grpc"

	"github.com/fitglue/server/src/go/internal/infra"
	pipelinestore "github.com/fitglue/server/src/go/internal/pipeline"
	"github.com/fitglue/server/src/go/internal/reconcile"
	"github.com/fitglue/server/src/go/internal/user"
	hevyapi "github.com/fitglue/server/src/go/pkg/api/hevy"
	stravaapi "github.com/fitglue/server/src/go/pkg/api/strava"
	hevymap "github.com/fitglue/server/src/go/pkg/domain/sourcemap/hevy"
	stravamap "github.com/fitglue/server/src/go/pkg/domain/sourcemap/strava"
	infradb "github.com/fitglue/server/src/go/pkg/infrastructure/database"
	"github.com/fitglue/server/src/go/pkg/infrastructure/oauth"
	infrapubsub "github.com/fitglue/server/src/go/pkg/infrastructure/pubsub"
	"github.com/fitglue/server/src/go/pkg/loopprevention"
	pbevents "github.com/fitglue/server/src/go/pkg/types/pb/models/events"
	pbpipeline "github.com/fitglue/server/src/go/pkg/types/pb/models/pipeline"
	pbplugin "github.com/fitglue/server/src/go/pkg/types/pb/models/plugin"
	pipelinepb "github.com/fitglue/server/src/go/pkg/types/pb/services/pipeline"
)

// liveReconcile is reconcile's real backend: Firestore for runs, the Hevy key,
// the Strava token and the uploaded_activities records; Hevy's and Strava's
// APIs for the lists; Pub/Sub for replays (the same CloudEvent the webhook
// publishes); the pipeline service's RepostActivity RPC for reposts.
type liveReconcile struct {
	uid        string
	fs         *firestore.Client
	db         *infradb.FirestoreAdapter
	users      *user.FirestoreStore
	runs       *pipelinestore.FirestoreStore
	ps         *pubsub.Client
	publisher  *infrapubsub.PubSubAdapter
	pipeConn   *grpc.ClientConn
	pipeline   pipelinepb.PipelineServiceClient
	http       *http.Client
	hevyBase   string
	stravaBase string
}

func openLiveReconcile(ctx context.Context, f *reconcileFlags) (reconcileBackend, func(), error) {
	fs, err := firestore.NewClient(ctx, f.project)
	if err != nil {
		return nil, nil, fmt.Errorf("firestore client: %w", err)
	}
	ps, err := pubsub.NewClient(ctx, f.project)
	if err != nil {
		fs.Close()
		return nil, nil, fmt.Errorf("pubsub client: %w", err)
	}
	conn, err := infra.GRPCDial(f.pipelineURL)
	if err != nil {
		fs.Close()
		ps.Close()
		return nil, nil, fmt.Errorf("pipeline service %s: %w", f.pipelineURL, err)
	}
	l := &liveReconcile{
		uid:        f.uid,
		fs:         fs,
		db:         infradb.NewFirestoreAdapter(fs),
		users:      user.NewFirestoreStore(fs),
		runs:       pipelinestore.NewFirestoreStore(fs),
		ps:         ps,
		publisher:  &infrapubsub.PubSubAdapter{Client: ps, Logger: infra.NewLoggerWithComponent("fitgluectl")},
		pipeConn:   conn,
		pipeline:   pipelinepb.NewPipelineServiceClient(conn),
		http:       &http.Client{Timeout: 30 * time.Second},
		hevyBase:   hevymap.APIBase,
		stravaBase: stravamap.APIBase,
	}
	return l, func() { conn.Close(); ps.Close(); fs.Close() }, nil
}

func (l *liveReconcile) Snapshot(ctx context.Context, from, since, now time.Time) (reconcile.Snapshot, error) {
	snap := reconcile.Snapshot{Since: since, Now: now, Uploaded: map[string]string{}}

	// FitGlue: every pipeline run created since `from` (a run is created after
	// the workout it is for, so this covers every session in the window).
	runs, _, err := l.runs.ListPipelineRuns(ctx, l.uid, "", 1000, "", &from, nil)
	if err != nil {
		return snap, fmt.Errorf("list pipeline runs: %w", err)
	}
	named := map[string]bool{}
	for _, r := range runs {
		run := toRun(r)
		snap.Runs = append(snap.Runs, run)
		named[run.SourceID] = true
		if run.Strava != nil && run.Strava.ExternalID != "" {
			named[run.Strava.ExternalID] = true
		}
		if run.Hevy != nil && run.Hevy.ExternalID != "" {
			named[run.Hevy.ExternalID] = true
		}
	}

	integ, err := l.users.GetIntegrations(ctx, l.uid)
	if err != nil {
		return snap, fmt.Errorf("read integrations: %w", err)
	}
	if integ.GetHevy().GetApiKey() == "" {
		snap.Warnings = append(snap.Warnings, "hevy: not connected")
	} else if ws, err := l.listHevy(ctx, integ.GetHevy().GetApiKey(), from); err != nil {
		snap.Warnings = append(snap.Warnings, "hevy: "+err.Error())
	} else {
		snap.Hevy, snap.HevyListed = ws, true
	}
	if as, err := l.listStrava(ctx, from); err != nil {
		snap.Warnings = append(snap.Warnings, "strava: "+err.Error())
	} else {
		snap.Strava, snap.StravaListed = as, true
	}

	// uploaded_activities for anything no run names: is it FitGlue's own?
	lookup := func(dest pbplugin.DestinationType, prefix, id string) {
		if named[id] {
			return
		}
		rec, err := l.db.GetUploadedActivity(ctx, l.uid, dest, id)
		if err != nil || rec == nil {
			return
		}
		snap.Uploaded[prefix+id] = rec.GetSource().String() + ":" + rec.GetExternalId()
	}
	for _, w := range snap.Hevy {
		lookup(pbplugin.DestinationType_DESTINATION_HEVY, "hevy:", w.ID)
	}
	for _, a := range snap.Strava {
		lookup(pbplugin.DestinationType_DESTINATION_STRAVA, "strava:", a.ID)
	}
	return snap, nil
}

func toRun(r *pbpipeline.PipelineRun) reconcile.Run {
	ts := func(t interface{ AsTime() time.Time }, ok bool) time.Time {
		if !ok {
			return time.Time{}
		}
		return t.AsTime()
	}
	run := reconcile.Run{
		ID:         r.GetId(),
		ActivityID: r.GetActivityId(),
		PipelineID: r.GetPipelineId(),
		Source:     r.GetSource(),
		SourceID:   r.GetSourceActivityId(),
		Title:      r.GetTitle(),
		Type:       r.GetType().String(),
		Status:     r.GetStatus().String(),
		Start:      ts(r.GetStartTime(), r.GetStartTime() != nil),
		Created:    ts(r.GetCreatedAt(), r.GetCreatedAt() != nil),
		Updated:    ts(r.GetUpdatedAt(), r.GetUpdatedAt() != nil),
	}
	for _, d := range r.GetDestinations() {
		dest := &reconcile.Dest{Status: d.GetStatus().String(), ExternalID: d.GetExternalId()}
		switch d.GetDestination() {
		case pbplugin.DestinationType_DESTINATION_STRAVA:
			run.Strava = dest
		case pbplugin.DestinationType_DESTINATION_HEVY:
			run.Hevy = dest
		}
	}
	return run
}

// listHevy pages GET /v1/workouts (newest first, ten a page) back to `from`.
func (l *liveReconcile) listHevy(ctx context.Context, apiKey string, from time.Time) ([]reconcile.HevyWorkout, error) {
	client, err := hevyapi.NewClientWithResponses(l.hevyBase,
		hevyapi.WithHTTPClient(l.http),
		hevyapi.WithRequestEditorFn(func(_ context.Context, req *http.Request) error {
			req.Header.Set("api-key", apiKey)
			return nil
		}))
	if err != nil {
		return nil, err
	}
	var out []reconcile.HevyWorkout
	pageSize := 10
	for page := 1; page <= 30; page++ {
		p := page
		resp, err := client.GetV1WorkoutsWithResponse(ctx, &hevyapi.GetV1WorkoutsParams{Page: &p, PageSize: &pageSize})
		if err != nil {
			return nil, err
		}
		if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil {
			return nil, fmt.Errorf("GET /v1/workouts page %d: status %d", page, resp.StatusCode())
		}
		older := false
		if resp.JSON200.Workouts != nil {
			for _, w := range *resp.JSON200.Workouts {
				hw := reconcile.HevyWorkout{ID: deref(w.Id), Title: deref(w.Title)}
				hw.Start, _ = time.Parse(time.RFC3339, deref(w.StartTime))
				hw.End, _ = time.Parse(time.RFC3339, deref(w.EndTime))
				if hw.Start.Before(from) {
					older = true
					continue
				}
				out = append(out, hw)
			}
		}
		last := resp.JSON200.PageCount == nil || resp.JSON200.Page == nil || *resp.JSON200.Page >= *resp.JSON200.PageCount
		if older || last || resp.JSON200.Workouts == nil || len(*resp.JSON200.Workouts) == 0 {
			break
		}
	}
	return out, nil
}

func (l *liveReconcile) stravaToken(ctx context.Context) (string, error) {
	tok, err := oauth.NewFirestoreTokenSourceFromDB(l.db, l.uid, "strava").Token(ctx)
	if err != nil {
		return "", fmt.Errorf("strava token: %w", err)
	}
	return tok.AccessToken, nil
}

// listStrava is GET /athlete/activities?after=…, a hundred a page — one call
// for a normal window, well inside Strava's per-app limits.
func (l *liveReconcile) listStrava(ctx context.Context, from time.Time) ([]reconcile.StravaActivity, error) {
	token, err := l.stravaToken(ctx)
	if err != nil {
		return nil, err
	}
	client, err := stravaapi.NewClientWithResponses(l.stravaBase,
		stravaapi.WithHTTPClient(l.http),
		stravaapi.WithRequestEditorFn(func(_ context.Context, req *http.Request) error {
			req.Header.Set("Authorization", "Bearer "+token)
			return nil
		}))
	if err != nil {
		return nil, err
	}
	after := int(from.Unix())
	perPage := stravaapi.PerPage(100)
	var out []reconcile.StravaActivity
	for page := 1; page <= 10; page++ {
		pg := stravaapi.Page(page)
		resp, err := client.GetLoggedInAthleteActivitiesWithResponse(ctx, &stravaapi.GetLoggedInAthleteActivitiesParams{After: &after, Page: &pg, PerPage: &perPage})
		if err != nil {
			return nil, err
		}
		if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil {
			return nil, fmt.Errorf("GET /athlete/activities: status %d", resp.StatusCode())
		}
		for _, a := range *resp.JSON200 {
			sa := reconcile.StravaActivity{Name: deref(a.Name), UploadFile: deref(a.ExternalId)}
			if a.Id != nil {
				sa.ID = strconv.FormatInt(*a.Id, 10)
			}
			if a.SportType != nil {
				sa.SportType = string(*a.SportType)
			}
			if a.StartDate != nil {
				sa.Start = a.StartDate.UTC()
			}
			if a.ElapsedTime != nil {
				sa.Elapsed = time.Duration(*a.ElapsedTime) * time.Second
			}
			out = append(out, sa)
		}
		if len(*resp.JSON200) < int(perPage) {
			break
		}
	}
	return out, nil
}

// ReplayHevy fetches the workout and publishes it exactly as the Hevy webhook
// does: same mapping, same bounceback check, same CloudEvent on
// topic-raw-activity, so enrichers, destinations and pending inputs all run.
func (l *liveReconcile) ReplayHevy(ctx context.Context, workoutID string) error {
	integ, err := l.users.GetIntegrations(ctx, l.uid)
	if err != nil {
		return fmt.Errorf("read integrations: %w", err)
	}
	key := integ.GetHevy().GetApiKey()
	if key == "" {
		return fmt.Errorf("hevy is not connected")
	}
	payload, err := hevymap.FetchPayload(ctx, l.http, l.hevyBase, key, workoutID, l.uid)
	if err != nil {
		return err
	}
	return l.publishLikeWebhook(ctx, "hevy", payload)
}

// ReplayStrava is ReplayHevy for a Strava activity (summary + streams).
func (l *liveReconcile) ReplayStrava(ctx context.Context, activityID string) error {
	token, err := l.stravaToken(ctx)
	if err != nil {
		return err
	}
	payload, err := stravamap.FetchPayload(ctx, l.stravaBase, token, activityID, l.uid)
	if err != nil {
		return err
	}
	return l.publishLikeWebhook(ctx, "strava", payload)
}

// publishLikeWebhook is api-webhook's Processor.processEvent from step 3 on.
func (l *liveReconcile) publishLikeWebhook(ctx context.Context, provider string, payload *pbevents.ActivityPayload) error {
	if std := payload.GetStandardizedActivity(); std != nil {
		var start int64
		if st := std.GetStartTime(); st != nil {
			start = st.AsTime().Unix()
		}
		bounce, err := loopprevention.IsBounceback(ctx, l.db, l.uid, payload.GetSource(), std.GetExternalId(), start)
		if err != nil {
			return fmt.Errorf("bounceback check: %w", err)
		}
		if bounce {
			return fmt.Errorf("%s %s is FitGlue's own upload; not replaying it", provider, payload.GetActivityId())
		}
	}
	execID := uuid.NewString()
	payload.PipelineExecutionId = &execID
	ce, err := infrapubsub.NewCloudEvent(fmt.Sprintf("/integrations/%s/webhook", provider), "com.fitglue.activity.created", payload)
	if err != nil {
		return fmt.Errorf("pack cloud event: %w", err)
	}
	if _, err := l.publisher.PublishCloudEvent(ctx, "topic-raw-activity", ce); err != nil {
		return fmt.Errorf("publish: %w", err)
	}
	return nil
}

// RepostStrava is what POST /api/v2/repost/retry-destination does: the
// pipeline re-runs the Strava upload on the existing run.
func (l *liveReconcile) RepostStrava(ctx context.Context, fitglueActivityID, mode string) error {
	_, err := l.pipeline.RepostActivity(ctx, &pipelinepb.RepostActivityRequest{
		UserId:      l.uid,
		ActivityId:  fitglueActivityID,
		Mode:        mode,
		Destination: pbplugin.DestinationType_DESTINATION_STRAVA.String(),
	})
	return err
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
