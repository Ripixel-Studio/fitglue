package ai_companion

import (
	"strings"
	"testing"

	"github.com/fitglue/server/src/go/pkg/voice"
)

// James, 2026-11-11: the activity summary is told by a knowledgeable observer, not in his first person.
func TestCompanionPromptIsTheObserver(t *testing.T) {
	sys, _ := buildPrompt("Type: RUN")
	if !strings.HasPrefix(sys, voice.Observer()) || strings.Contains(sys, voice.Guide()) {
		t.Fatal("companion system prompt must be the observer guide, not his voice guide")
	}
	if !strings.Contains(sys, "Third person") || !strings.Contains(sys, "Never I, me, my or we") {
		t.Fatal("companion task block must ask for the third person")
	}
}
