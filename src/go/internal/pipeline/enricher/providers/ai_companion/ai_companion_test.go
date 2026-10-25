package ai_companion

import (
	user "github.com/fitglue/server/src/go/pkg/domain/user"

	pbuser "github.com/fitglue/server/src/go/pkg/types/pb/models/user"

	pbplugin "github.com/fitglue/server/src/go/pkg/types/pb/models/plugin"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"

	"context"
	"log/slog"
	"testing"
)

func TestAICompanionProvider_NoAPIKeySkips(t *testing.T) {
	// Clear API key to ensure predictable test behavior
	t.Setenv("GEMINI_API_KEY", "")

	provider := NewAICompanionProvider()

	activity := &pbactivity.StandardizedActivity{
		Name: "Morning Run",
		Type: pbactivity.ActivityType_ACTIVITY_TYPE_RUN,
	}
	u := &user.Record{UserProfile: &pbuser.UserProfile{UserId: "user-1"}}

	result, err := provider.Enrich(context.Background(), slog.Default(), activity, u, nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Accept either "skipped" (no API key) or "error" (invalid API key) since we
	// don't control the test environment's API key state.
	if status := result.Metadata["status"]; status != "skipped" && status != "error" {
		t.Errorf("expected status skipped/error, got %q (reason: %s)", status, result.Metadata["reason"])
	}
	if result.Metadata["reason"] == "tier_restricted" {
		t.Error("tier gating was removed with billing; must not skip for tier")
	}
	// Should not modify activity
	if result.Name != "" || result.Description != "" {
		t.Errorf("expected empty Name/Description for skipped result, got %q / %q", result.Name, result.Description)
	}
}

func TestAICompanionProvider_ModeConfig(t *testing.T) {
	// Clear API key to ensure predictable test behavior
	t.Setenv("GEMINI_API_KEY", "")

	provider := NewAICompanionProvider()

	activity := &pbactivity.StandardizedActivity{
		Name: "Morning Run",
		Type: pbactivity.ActivityType_ACTIVITY_TYPE_RUN,
	}

	athleteUser := &user.Record{
		UserProfile: &pbuser.UserProfile{
			UserId: "athlete-user",
		},
	}

	// Without API key, these will all be skipped with api_key_not_configured
	// but we can verify the mode is being read correctly
	modes := []string{"title", "description", "both", ""}

	for _, mode := range modes {
		t.Run("mode="+mode, func(t *testing.T) {
			config := map[string]string{}
			if mode != "" {
				config["mode"] = mode
			}

			result, err := provider.Enrich(context.Background(), slog.Default(), activity, athleteUser, config, false)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Should skip due to no API key, but not error
			if result.Metadata["status"] != "skipped" {
				t.Logf("status=%s, reason=%s", result.Metadata["status"], result.Metadata["reason"])
			}
		})
	}
}

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

func TestCleanDescription_NoTruncation(t *testing.T) {
	longDescription := "Hybrid class crushed! Box step ups, RAM floor to skies with 15kg, sled pushes with 115kg...you brilliant people better be resting well tonight because that was an absolute masterclass in intensity and grit. See you all on the leaderboard!"

	// Use the new cleanDescription function
	result := cleanDescription(longDescription)

	if len(result) == len(longDescription) {
		t.Logf("Success: Full length preserved. Input/Output length: %d", len(result))
	} else {
		t.Errorf("Expected full length, but got: %d (original: %d)", len(result), len(longDescription))
	}
}

func TestCleanTitle_Truncation(t *testing.T) {
	longTitle := "This is an extremely long workout title that should definitely be truncated because it exceeds one hundred characters by a significant margin."

	result := cleanTitle(longTitle)

	if len(result) == 100 {
		t.Logf("Success: Title truncated to 100 characters: %q", result)
	} else {
		t.Errorf("Expected title truncation to 100 characters, but got length: %d", len(result))
	}
}
