package enricher

import (
	"testing"

	pbplugin "github.com/fitglue/server/src/go/pkg/types/pb/models/plugin"
)

// His answer to an ask leads, even when a section-writing enricher (parkrun) sits before it.
func TestHisWordsFirst(t *testing.T) {
	configs := []configuredEnricher{
		{ProviderType: pbplugin.EnricherProviderType_ENRICHER_PROVIDER_PARKRUN},
		{ProviderType: pbplugin.EnricherProviderType_ENRICHER_PROVIDER_AI_COMPANION},
		{ProviderType: pbplugin.EnricherProviderType_ENRICHER_PROVIDER_USER_INPUT},
	}
	slots := []string{"source text", "parkrun section", "AI Summary:\nai", "his answer"}
	got := buildDescriptionFromSlots(hisWordsFirst(slots, configs))
	want := "source text\n\nhis answer\n\nparkrun section\n\nAI Summary:\nai"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if got := buildDescriptionFromSlots(hisWordsFirst([]string{"", "p", "", "his"}, configs)); got != "his\n\np" {
		t.Fatalf("no source text: got %q", got)
	}
}
