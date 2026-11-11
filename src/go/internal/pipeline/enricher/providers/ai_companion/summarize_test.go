package ai_companion

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/fitglue/server/src/go/pkg/claude"
	"github.com/fitglue/server/src/go/pkg/domain/user"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	pbuser "github.com/fitglue/server/src/go/pkg/types/pb/models/user"
)

// Summarize is the enricher's own path for a stored activity (fitgluectl
// regen-activity-summaries): same prompt, same model, same guard, same label.
func TestSummarizeIsTheEnrichersPath(t *testing.T) {
	act := &pbactivity.StandardizedActivity{Name: "Half Hyrox PT", Description: "Bad at wall balls",
		Type: pbactivity.ActivityType_ACTIVITY_TYPE_WEIGHT_TRAINING}
	other := "🔥 Calories: 341 kcal"

	live := &fakeAI{out: "Hard going, and the session shows why."}
	p := NewAICompanionProvider()
	p.AI = live
	u := &user.Record{UserProfile: &pbuser.UserProfile{UserId: "u"}}
	res, err := p.Enrich(context.Background(), discardLogger(), act, u, map[string]string{"enriched_description": other}, false)
	if err != nil {
		t.Fatal(err)
	}

	regen := &fakeAI{out: "Hard going, and the session shows why."}
	text, err := Summarize(context.Background(), regen, act, other)
	if err != nil {
		t.Fatal(err)
	}
	if regen.req.System != live.req.System || regen.req.Prompt != live.req.Prompt || regen.req.Model != live.req.Model {
		t.Errorf("Summarize must send the enricher's request:\n%+v\n%+v", regen.req, live.req)
	}
	if text != res.Description || !strings.HasPrefix(text, "AI Summary:\n") {
		t.Errorf("text %q, enricher %q", text, res.Description)
	}
	sys, prompt := SummaryPrompt(act, other)
	if sys != live.req.System || prompt != live.req.Prompt {
		t.Error("SummaryPrompt must be the prompt Summarize sends")
	}
	if !strings.Contains(prompt, "Calories: 341") {
		t.Error("other enrichers' descriptions belong in the context")
	}
}

func TestSummarizeGuardAndErrors(t *testing.T) {
	act := &pbactivity.StandardizedActivity{Name: "Run", Type: pbactivity.ActivityType_ACTIVITY_TYPE_RUN}
	if text, err := Summarize(context.Background(), &fakeAI{out: "Sarah would be proud."}, act, ""); err != nil || text != "" {
		t.Errorf("guard drop: %q %v", text, err)
	}
	if _, err := Summarize(context.Background(), &fakeAI{err: claude.ErrLimited}, act, ""); !errors.Is(err, claude.ErrLimited) {
		t.Errorf("ErrLimited must come back: %v", err)
	}
}

// New summaries carry the marker regen-activity-summaries skips on.
func TestEnrichStampsSummaryModel(t *testing.T) {
	p := NewAICompanionProvider()
	p.AI = &fakeAI{out: "A steady one."}
	act := &pbactivity.StandardizedActivity{Name: "Run", Type: pbactivity.ActivityType_ACTIVITY_TYPE_RUN}
	u := &user.Record{UserProfile: &pbuser.UserProfile{UserId: "u"}}
	res, _ := p.Enrich(context.Background(), discardLogger(), act, u, nil, false)
	ai := res.Enrichments.GetAiSummary()
	if ai.GetModel() != SummaryModel || ai.GetGeneratedAt() == nil {
		t.Errorf("model %q generatedAt %v", ai.GetModel(), ai.GetGeneratedAt())
	}
	if !strings.Contains(SummaryModel, "claude") {
		t.Error("the site shows a summary only when its model names claude")
	}
}
