package activity

import (
	"context"
	"errors"
	"fmt"
	"github.com/fitglue/server/src/go/pkg/paidevents"
	"slices"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/fitglue/server/src/go/pkg/description"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	pbevents "github.com/fitglue/server/src/go/pkg/types/pb/models/events"
	pbplugin "github.com/fitglue/server/src/go/pkg/types/pb/models/plugin"
)

// aiCompanionApplied is the applied-enrichments entry the site checks before it
// shows an activity's AI summary.
var aiCompanionApplied = pbplugin.EnricherProviderType_ENRICHER_PROVIDER_AI_COMPANION.String()

// ActivitySummary is one showcased activity as fitgluectl
// regen-activity-summaries sees it: the activity the AI companion would have
// seen, and the site's current AI summary.
type ActivitySummary struct {
	ShowcaseID, UserID, Title string
	StartTime                 time.Time
	// Activity is a copy of the stored activity data whose Description is only
	// the text before the AI block (the source's and his own words), as the
	// enricher saw it; nil when the showcase has no activity data.
	Activity *pbactivity.StandardizedActivity
	// OtherDescriptions is the FitGlue sections after the AI block, which the
	// enricher was given as the other enrichers' descriptions.
	OtherDescriptions string
	// Old is the site's AI summary (with its label): the typed one, else the
	// description's AI block (Gemini-era showcases); "" when there is none.
	Old string
	// Model is the stored AiSummary.model ("" for every summary before the marker).
	Model string
	// ActivityType and Tags are the showcase's.
	ActivityType pbactivity.ActivityType
	Tags         []string
	// Classification is the stored ai-activity-type verdict, nil when there is none.
	Classification *pbactivity.ActivityClassification

	doc     *pbactivity.ShowcasedActivity
	blob    *pbevents.EnrichedActivityEvent
	blobURI string
}

// HasData reports whether there is activity data to write a summary from.
func (a *ActivitySummary) HasData() bool { return a.Activity != nil }

// IsCurrent reports whether the stored summary was written by model.
func (a *ActivitySummary) IsCurrent(model string) bool {
	return a.Old != "" && a.Model == model
}

// enrichments is where this showcase's enrichments live: the document when it
// carries them (hydration then ignores the blob's), else the blob.
func (a *ActivitySummary) enrichments() *pbactivity.ActivityEnrichments {
	if e := a.doc.GetEnrichments(); e != nil {
		return e
	}
	return a.blob.GetEnrichments()
}

// LoadActivitySummary reads a showcase document and its data blob. Nothing is written.
func (s *Service) LoadActivitySummary(ctx context.Context, showcaseID string) (*ActivitySummary, error) {
	doc, owner, err := s.store.GetPublicShowcase(ctx, showcaseID)
	if err != nil {
		return nil, fmt.Errorf("showcase %s: %w", showcaseID, err)
	}
	if doc == nil {
		return nil, fmt.Errorf("showcase %s not found", showcaseID)
	}
	a := &ActivitySummary{ShowcaseID: showcaseID, UserID: owner, Title: doc.Title, doc: doc,
		ActivityType: doc.ActivityType, Tags: doc.Tags}
	if doc.StartTime != nil {
		a.StartTime = doc.StartTime.AsTime()
	}
	data := doc.ActivityData
	if doc.ActivityDataUri != "" {
		raw, err := s.blobStore.Get(ctx, "", doc.ActivityDataUri)
		switch {
		case errors.Is(err, storage.ErrObjectNotExist):
			// Older showcases point at the lifecycle-expired artifacts bucket:
			// no activity data to write from (HasData false).
		case err != nil:
			return nil, fmt.Errorf("read %s: %w", doc.ActivityDataUri, err)
		default:
			var ev pbevents.EnrichedActivityEvent
			if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, &ev); err != nil {
				return nil, fmt.Errorf("decode %s: %w", doc.ActivityDataUri, err)
			}
			a.blob, a.blobURI = &ev, doc.ActivityDataUri
			if data == nil {
				data = ev.ActivityData
			}
		}
	}
	desc := doc.Description
	if data != nil {
		if data.Description != "" {
			desc = data.Description
		}
		a.Activity = proto.Clone(data).(*pbactivity.StandardizedActivity)
	}
	own, oldAI, other := description.SplitAISummary(desc)
	if a.Activity != nil {
		a.Activity.Description = own
	}
	a.OtherDescriptions = other
	if ai := a.enrichments().GetAiSummary(); ai.GetHtml() != "" {
		a.Old, a.Model = ai.GetHtml(), ai.GetModel()
	} else if oldAI != "" {
		a.Old = "AI Summary:\n" + oldAI
	}
	a.Classification = a.enrichments().GetClassification()
	return a, nil
}

// SaveActivitySummary replaces only the site's AI summary (text, model, time)
// where the showcase keeps its enrichments, marks the AI companion applied (the
// site shows the summary only then), saves the document and tells the site
// exporter. His title, description and the activity data are untouched, and
// nothing goes to Strava or any other destination. Empty text is refused: a
// failed rewrite must leave the old summary in place.
func (s *Service) SaveActivitySummary(ctx context.Context, a *ActivitySummary, text, model string, now time.Time) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("empty summary")
	}
	ai := &pbactivity.AiSummary{Html: text, Model: model, GeneratedAt: timestamppb.New(now)}
	switch {
	case a.doc.Enrichments != nil:
		a.doc.Enrichments.AiSummary = ai
	case a.blob != nil && a.blob.Enrichments != nil:
		a.blob.Enrichments.AiSummary = ai
		raw, err := protojson.Marshal(a.blob)
		if err != nil {
			return fmt.Errorf("encode blob: %w", err)
		}
		if err := s.blobStore.Write(ctx, "", a.blobURI, raw); err != nil {
			return fmt.Errorf("write %s: %w", a.blobURI, err)
		}
	default:
		a.doc.Enrichments = &pbactivity.ActivityEnrichments{AiSummary: ai}
	}
	if !slices.Contains(a.doc.AppliedEnrichments, aiCompanionApplied) {
		a.doc.AppliedEnrichments = append(a.doc.AppliedEnrichments, aiCompanionApplied)
	}
	if _, err := s.store.UpdateShowcase(ctx, a.UserID, a.doc); err != nil {
		return fmt.Errorf("update showcase %s: %w", a.ShowcaseID, err)
	}
	a.Old, a.Model = text, model
	s.siteActivityChanged(ctx, a.ShowcaseID)
	return nil
}

// Parkrun is the stored parkrun result, nil when there is none.
func (a *ActivitySummary) Parkrun() *pbactivity.ParkrunSummary {
	return a.enrichments().GetParkrun()
}

// SaveClassification stores the site's tag, race flag and reason where the
// showcase keeps its enrichments (the document when it carries them, else the
// data blob), copies the tag and race flag onto the profile entry and tells the
// site exporter. The title, description, activity type and data are untouched,
// and nothing goes to Strava or any other destination.
func (s *Service) SaveClassification(ctx context.Context, a *ActivitySummary, c *pbactivity.ActivityClassification, entry *pbactivity.ShowcaseProfileEntry) error {
	if c == nil || c.Tag == "" {
		return errors.New("empty classification")
	}
	docChanged := true
	switch {
	case a.doc.Enrichments != nil:
		a.doc.Enrichments.Classification = c
	case a.blob != nil:
		docChanged = false
		if a.blob.Enrichments == nil {
			a.blob.Enrichments = &pbactivity.ActivityEnrichments{}
		}
		a.blob.Enrichments.Classification = c
		raw, err := protojson.Marshal(a.blob)
		if err != nil {
			return fmt.Errorf("encode blob: %w", err)
		}
		if err := s.blobStore.Write(ctx, "", a.blobURI, raw); err != nil {
			return fmt.Errorf("write %s: %w", a.blobURI, err)
		}
	default:
		a.doc.Enrichments = &pbactivity.ActivityEnrichments{Classification: c}
	}
	if docChanged {
		if _, err := s.store.UpdateShowcase(ctx, a.UserID, a.doc); err != nil {
			return fmt.Errorf("update showcase %s: %w", a.ShowcaseID, err)
		}
	}
	if entry != nil {
		entry.Tag, entry.IsRace = c.Tag, c.IsRace
		if err := s.store.SetShowcaseProfileEntry(ctx, a.UserID, entry); err != nil {
			return fmt.Errorf("update entry %s: %w", a.ShowcaseID, err)
		}
	}
	a.Classification = c
	s.siteActivityChanged(ctx, a.ShowcaseID)
	return nil
}

// PaidEvents reads his paid-for events sheet as the box copied it to the
// artifacts bucket (paidevents.Object).
func (s *Service) PaidEvents(ctx context.Context) ([]paidevents.Event, error) {
	return paidevents.Load(ctx, s.blobStore, s.bucketName)
}
