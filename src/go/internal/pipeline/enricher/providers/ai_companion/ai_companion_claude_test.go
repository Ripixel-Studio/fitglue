package ai_companion

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fitglue/server/src/go/pkg/claude"
	"github.com/fitglue/server/src/go/pkg/domain/user"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	pbuser "github.com/fitglue/server/src/go/pkg/types/pb/models/user"
	"github.com/fitglue/server/src/go/pkg/voice"
)

type fakeAI struct {
	req   claude.Request
	calls int
	out   string
	err   error
}

func (f *fakeAI) Complete(_ context.Context, r claude.Request) (string, error) {
	f.req = r
	f.calls++
	return f.out, f.err
}

func run(t *testing.T, name string, f *fakeAI) (*fakeAI, map[string]string, string, string) {
	t.Helper()
	p := NewAICompanionProvider()
	p.AI = f
	act := &pbactivity.StandardizedActivity{Name: name, Type: pbactivity.ActivityType_ACTIVITY_TYPE_RUN}
	u := &user.Record{UserProfile: &pbuser.UserProfile{UserId: "u"}}
	res, err := p.Enrich(context.Background(), discardLogger(), act, u, nil, false)
	if err != nil {
		t.Fatalf("Enrich error: %v", err)
	}
	return f, res.Metadata, res.Name, res.Description
}

func TestCompanionUsesHaikuAndGuide(t *testing.T) {
	g := filepath.Join(t.TempDir(), "g.md")
	if err := os.WriteFile(g, []byte("GUIDE-MARK\nbe fun"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FITGLUE_VOICE_FILE", g)
	voice.ResetGuideCache()
	t.Cleanup(voice.ResetGuideCache)
	f, meta, name, desc := run(t, "Morning Run", &fakeAI{out: "Legs shaking, still smiling 🥵"})
	if f.req.Model != "haiku" {
		t.Errorf("model %q", f.req.Model)
	}
	if !strings.HasPrefix(f.req.System, "GUIDE-MARK") {
		t.Errorf("system should start with the guide: %q", f.req.System)
	}
	if !strings.Contains(f.req.System, "at most 280 characters") {
		t.Error("task block missing")
	}
	if name != "" {
		t.Errorf("Name must stay empty, got %q", name)
	}
	if meta["status"] != "success" || !strings.HasPrefix(desc, "✨ AI Summary:\n") {
		t.Errorf("meta %v desc %q", meta, desc)
	}
}

func TestCompanionNameScrubbedFromPrompt(t *testing.T) {
	f, _, _, _ := run(t, "Tempo w/ Tom", &fakeAI{out: "Tempo day. Cooked."})
	if strings.Contains(f.req.Prompt, "Tom") {
		t.Errorf("name leaked: %s", f.req.Prompt)
	}
}

func TestCompanionSensitiveOmitted(t *testing.T) {
	f, _, _, _ := run(t, "walk to NA meeting", &fakeAI{out: "A gentle walk."})
	if strings.Contains(f.req.Prompt, "NA meeting") {
		t.Errorf("sensitive name leaked: %s", f.req.Prompt)
	}
}

func TestCompanionDropsNamedOutput(t *testing.T) {
	_, meta, _, desc := run(t, "Morning Run", &fakeAI{out: "Sarah would be proud."})
	if desc != "" || meta["reason"] != "voice_guard" || meta["status"] != "skipped" {
		t.Errorf("meta %v desc %q", meta, desc)
	}
}

func TestCompanionClaudeErrorDoesNotFail(t *testing.T) {
	for _, e := range []error{errors.New("boom"), claude.ErrLimited} {
		_, meta, _, desc := run(t, "Morning Run", &fakeAI{err: e})
		if meta["status"] != "error" || desc != "" {
			t.Errorf("%v: meta %v desc %q", e, meta, desc)
		}
	}
}

func TestCompanionOutputCappedAt280(t *testing.T) {
	long := strings.Repeat("Legs gone. ", 60)
	_, _, _, desc := run(t, "Morning Run", &fakeAI{out: long})
	body := strings.TrimPrefix(desc, "✨ AI Summary:\n")
	if n := len([]rune(body)); n == 0 || n > 280 {
		t.Errorf("body length %d", n)
	}
}

func TestCompanionNameFromHisDescriptionIsRejected(t *testing.T) {
	p := NewAICompanionProvider()
	p.AI = &fakeAI{out: "Kev paced it, I died 🥵"}
	act := &pbactivity.StandardizedActivity{Name: "Tempo", Description: "Kev paced me the whole way",
		Type: pbactivity.ActivityType_ACTIVITY_TYPE_RUN}
	u := &user.Record{UserProfile: &pbuser.UserProfile{UserId: "u"}}
	res, _ := p.Enrich(context.Background(), discardLogger(), act, u, nil, false)
	if res.Description != "" || res.Metadata["reason"] != "voice_guard" {
		t.Errorf("name from his description must not pass: %v %q", res.Metadata, res.Description)
	}
}

func TestCompanionLabelAlwaysOn(t *testing.T) {
	p := NewAICompanionProvider()
	p.AI = &fakeAI{out: "Legs gone 🥵"}
	act := &pbactivity.StandardizedActivity{Name: "Run", Type: pbactivity.ActivityType_ACTIVITY_TYPE_RUN}
	u := &user.Record{UserProfile: &pbuser.UserProfile{UserId: "u"}}
	res, _ := p.Enrich(context.Background(), discardLogger(), act, u, map[string]string{"section_header": "false"}, false)
	if !strings.HasPrefix(res.Description, "✨ AI Summary:\n") {
		t.Errorf("label must be mandatory: %q", res.Description)
	}
}
