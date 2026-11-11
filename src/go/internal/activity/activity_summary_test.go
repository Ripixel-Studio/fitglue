package activity

import (
	"context"
	"fmt"
	"testing"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	pbevents "github.com/fitglue/server/src/go/pkg/types/pb/models/events"
)

const hyroxURI = "gs://assets/showcase_data/u1/hyrox_data.json"

const hyroxDesc = "OH BOY am I bad at wall balls 😭\n\nAI Summary:\nPlain word opener, third person, no name: the summary.\n\n🔥 Calories: 341 kcal\n\n❤️ Heart Rate:\n• 156 bpm avg"

type summaryWorld struct {
	store   *MockActivityStore
	blob    *MockBlobStore
	written map[string][]byte
	doc     *pbactivity.ShowcasedActivity
	setDoc  *pbactivity.ShowcasedActivity
	rec     *recordingExporter
	svc     *Service
}

// newSummaryWorld stores one showcase; inline puts its enrichments on the
// Firestore document, otherwise only the blob has them.
func newSummaryWorld(t *testing.T, inline bool, applied []string) *summaryWorld {
	t.Helper()
	w := &summaryWorld{written: map[string][]byte{}, rec: &recordingExporter{}}
	data := &pbactivity.StandardizedActivity{Name: "Half Hyrox PT", Description: hyroxDesc,
		Type:     pbactivity.ActivityType_ACTIVITY_TYPE_WEIGHT_TRAINING,
		Sessions: []*pbactivity.Session{{TotalElapsedTime: 3300}}}
	en := &pbactivity.ActivityEnrichments{AiSummary: &pbactivity.AiSummary{Html: "AI Summary:\nPlain word opener, third person, no name: the summary."}}
	w.doc = &pbactivity.ShowcasedActivity{ShowcaseId: "hyrox", Title: "Half Hyrox PT", Description: hyroxDesc,
		StartTime: timestamppb.New(time.Date(2026, 11, 11, 7, 0, 0, 0, time.UTC)), ActivityDataUri: hyroxURI,
		AppliedEnrichments: applied}
	ev := &pbevents.EnrichedActivityEvent{ActivityData: data}
	if inline {
		w.doc.Enrichments = en
	} else {
		ev.Enrichments = en
	}
	raw, err := protojson.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	w.written[hyroxURI] = raw
	w.blob = &MockBlobStore{
		GetFunc: func(_ context.Context, _, obj string) ([]byte, error) { return w.written[obj], nil },
		WriteFunc: func(_ context.Context, _, obj string, b []byte) error {
			w.written[obj] = b
			return nil
		},
	}
	w.store = &MockActivityStore{
		GetPublicShowcaseFunc: func(_ context.Context, id string) (*pbactivity.ShowcasedActivity, string, error) {
			if id != "hyrox" {
				return nil, "", nil
			}
			return w.doc, "u1", nil
		},
		UpdateShowcaseFunc: func(_ context.Context, _ string, sc *pbactivity.ShowcasedActivity) (*pbactivity.ShowcasedActivity, error) {
			w.setDoc = sc
			return sc, nil
		},
	}
	w.svc = newTestSvc(w.store, w.blob)
	w.svc.SetSiteExporter(w.rec)
	return w
}

func (w *summaryWorld) blobEvent(t *testing.T) *pbevents.EnrichedActivityEvent {
	t.Helper()
	var ev pbevents.EnrichedActivityEvent
	if err := protojson.Unmarshal(w.written[hyroxURI], &ev); err != nil {
		t.Fatal(err)
	}
	return &ev
}

func TestLoadActivitySummarySplitsHisWordsFromTheSections(t *testing.T) {
	w := newSummaryWorld(t, false, nil)
	tg, err := w.svc.LoadActivitySummary(context.Background(), "hyrox")
	if err != nil {
		t.Fatal(err)
	}
	if tg.Activity.Description != "OH BOY am I bad at wall balls 😭" {
		t.Errorf("the model sees only his words as the description, got %q", tg.Activity.Description)
	}
	if tg.OtherDescriptions != "🔥 Calories: 341 kcal\n\n❤️ Heart Rate:\n• 156 bpm avg" {
		t.Errorf("other = %q", tg.OtherDescriptions)
	}
	if tg.Old != "AI Summary:\nPlain word opener, third person, no name: the summary." || tg.Model != "" || tg.Title != "Half Hyrox PT" {
		t.Errorf("old %q model %q", tg.Old, tg.Model)
	}
	if !tg.HasData() || tg.IsCurrent("claude-sonnet-observer") {
		t.Error("has data and is stale")
	}
	if len(w.written) != 1 || w.setDoc != nil {
		t.Error("loading must not write")
	}
}

func TestLoadActivitySummaryOldTextFromTheDescription(t *testing.T) {
	w := newSummaryWorld(t, true, nil)
	w.doc.Enrichments.AiSummary = nil
	tg, err := w.svc.LoadActivitySummary(context.Background(), "hyrox")
	if err != nil {
		t.Fatal(err)
	}
	if tg.Old != "AI Summary:\nPlain word opener, third person, no name: the summary." {
		t.Errorf("an untyped (Gemini-era) summary is read from the description: %q", tg.Old)
	}
}

func TestSaveActivitySummaryInBlobLeavesHisWords(t *testing.T) {
	w := newSummaryWorld(t, false, nil)
	tg, _ := w.svc.LoadActivitySummary(context.Background(), "hyrox")
	now := time.Date(2026, 11, 11, 20, 0, 0, 0, time.UTC)
	if err := w.svc.SaveActivitySummary(context.Background(), tg, "AI Summary:\nA hard session.", "claude-sonnet-observer", now); err != nil {
		t.Fatal(err)
	}
	ev := w.blobEvent(t)
	ai := ev.Enrichments.GetAiSummary()
	if ai.GetHtml() != "AI Summary:\nA hard session." || ai.GetModel() != "claude-sonnet-observer" || !ai.GetGeneratedAt().AsTime().Equal(now) {
		t.Errorf("blob summary = %v", ai)
	}
	if ev.ActivityData.Description != hyroxDesc || ev.ActivityData.Name != "Half Hyrox PT" {
		t.Error("the stored activity data must be untouched")
	}
	if w.setDoc == nil || w.setDoc.Enrichments != nil {
		t.Fatal("the document is saved (applied list) without inline enrichments, which would hide the blob's")
	}
	if w.setDoc.Description != hyroxDesc || w.setDoc.Title != "Half Hyrox PT" {
		t.Error("his title and description must be untouched")
	}
	if len(w.setDoc.AppliedEnrichments) != 1 || w.setDoc.AppliedEnrichments[0] != "ENRICHER_PROVIDER_AI_COMPANION" {
		t.Errorf("the site shows the summary only with AI_COMPANION applied: %v", w.setDoc.AppliedEnrichments)
	}
	if len(w.rec.activities) != 1 || w.rec.activities[0] != "hyrox" {
		t.Errorf("site export not told: %v", w.rec.activities)
	}
	again, _ := w.svc.LoadActivitySummary(context.Background(), "hyrox")
	if !again.IsCurrent("claude-sonnet-observer") {
		t.Error("a saved summary is current on the next load")
	}
}

func TestSaveActivitySummaryInlineDocument(t *testing.T) {
	applied := []string{"ENRICHER_PROVIDER_AI_COMPANION"}
	w := newSummaryWorld(t, true, applied)
	before := string(w.written[hyroxURI])
	tg, _ := w.svc.LoadActivitySummary(context.Background(), "hyrox")
	if err := w.svc.SaveActivitySummary(context.Background(), tg, "AI Summary:\nA hard session.", "m", time.Now()); err != nil {
		t.Fatal(err)
	}
	if string(w.written[hyroxURI]) != before {
		t.Error("the blob is untouched when the document carries the enrichments")
	}
	if w.setDoc.Enrichments.GetAiSummary().GetHtml() != "AI Summary:\nA hard session." || len(w.setDoc.AppliedEnrichments) != 1 {
		t.Errorf("doc = %v", w.setDoc)
	}
	if err := w.svc.SaveActivitySummary(context.Background(), tg, "", "m", time.Now()); err == nil {
		t.Error("empty text must be refused: a failed rewrite keeps the old one")
	}
}

func TestLoadActivitySummaryMissingBlobIsNoData(t *testing.T) {
	w := newSummaryWorld(t, true, nil)
	w.blob.GetFunc = func(context.Context, string, string) ([]byte, error) {
		return nil, fmt.Errorf("read: %w", storage.ErrObjectNotExist)
	}
	tg, err := w.svc.LoadActivitySummary(context.Background(), "hyrox")
	if err != nil {
		t.Fatal(err)
	}
	if tg.HasData() {
		t.Error("a missing blob means no data to write from")
	}
}
