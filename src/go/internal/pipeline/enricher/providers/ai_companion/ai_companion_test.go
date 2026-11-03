package ai_companion

import (
	"testing"

	pbplugin "github.com/fitglue/server/src/go/pkg/types/pb/models/plugin"
)

func TestAICompanionProvider_Name(t *testing.T) {
	provider := NewAICompanionProvider()

	if provider.Name() != "ai-companion" {
		t.Errorf("expected name 'ai-companion', got %q", provider.Name())
	}
}

func TestAICompanionProvider_ProviderType(t *testing.T) {
	provider := NewAICompanionProvider()

	expected := pbplugin.EnricherProviderType_ENRICHER_PROVIDER_AI_COMPANION
	if provider.ProviderType() != expected {
		t.Errorf("expected provider type %v, got %v", expected, provider.ProviderType())
	}
}
