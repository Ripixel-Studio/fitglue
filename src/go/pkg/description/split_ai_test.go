package description

import "testing"

func TestSplitAISummaryAroundTheAIBlock(t *testing.T) {
	desc := "Completed via ROXFIT\n\n🏆 Division: Open Men\n\nTwo races within 20hrs?!\n\n✨ AI Summary:\nGrit and more grit.\n\nSecond Gemini paragraph.\n\n🔥 Streak Tracker:\n• 2-day streak\n\n❤️ Heart Rate:\n• 156 bpm avg\n\nPosted via FitGlue"
	own, ai, other := SplitAISummary(desc)
	if own != "Completed via ROXFIT\n\nTwo races within 20hrs?!" {
		t.Errorf("own = %q", own)
	}
	if ai != "Grit and more grit.\n\nSecond Gemini paragraph." {
		t.Errorf("ai = %q", ai)
	}
	if other != "🏆 Division: Open Men\n\n🔥 Streak Tracker:\n• 2-day streak\n\n❤️ Heart Rate:\n• 156 bpm avg" {
		t.Errorf("other = %q", other)
	}
}

func TestSplitAISummaryPlainLabel(t *testing.T) {
	own, ai, other := SplitAISummary("Wall balls 😭\n\nAI Summary:\nPlain word opener.\n\n🔥 Calories: 341 kcal")
	if own != "Wall balls 😭" || ai != "Plain word opener." || other != "🔥 Calories: 341 kcal" {
		t.Errorf("got %q / %q / %q", own, ai, other)
	}
}

func TestSplitAISummaryWithoutAIBlock(t *testing.T) {
	own, ai, other := SplitAISummary("Easy one.\n\n🔥 Calories: 300 kcal\n\nMore of his words.\n\nPosted via FitGlue")
	if ai != "" {
		t.Errorf("ai = %q", ai)
	}
	if own != "Easy one.\n\nMore of his words." {
		t.Errorf("own = %q", own)
	}
	if other != "🔥 Calories: 300 kcal" {
		t.Errorf("other = %q", other)
	}
	if o, a, x := SplitAISummary(""); o != "" || a != "" || x != "" {
		t.Error("empty description")
	}
}

func TestSplitAISummaryInlineLabel(t *testing.T) {
	if _, ai, _ := SplitAISummary("Run.\n\n✨ AI Summary: A steady one."); ai != "A steady one." {
		t.Errorf("ai = %q", ai)
	}
}

// A section before the AI block (Personal Records) is a section, not his words.
func TestSplitAISummarySectionBeforeAI(t *testing.T) {
	own, ai, other := SplitAISummary("Very sweaty 🥵\n\n🏆 Personal Records:\n• Squat: 60kg (previous: 50kg)\n\nAI Summary:\nOld line.\n\n🔥 Calories: 362 kcal")
	if own != "Very sweaty 🥵" || ai != "Old line." || other != "🏆 Personal Records:\n• Squat: 60kg (previous: 50kg)\n\n🔥 Calories: 362 kcal" {
		t.Errorf("got %q / %q / %q", own, ai, other)
	}
}
