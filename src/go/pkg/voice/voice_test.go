package voice

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func resetGuide() { guideMu.Lock(); guideAt = guideAt.Truncate(0); guideVal = ""; guideMu.Unlock() }

func TestGuideFallbackWhenMissing(t *testing.T) {
	t.Setenv("FITGLUE_VOICE_FILE", filepath.Join(t.TempDir(), "nope.md"))
	resetGuide()
	if Guide() != strings.TrimSpace(fallback) {
		t.Fatalf("want fallback, got %q", Guide())
	}
}

func TestGuideCapped(t *testing.T) {
	p := filepath.Join(t.TempDir(), "g.md")
	if err := os.WriteFile(p, []byte(strings.Repeat("x", 8*1024)), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FITGLUE_VOICE_FILE", p)
	resetGuide()
	if n := len(Guide()); n == 0 || n > 6*1024 {
		t.Fatalf("len %d", n)
	}
}

func TestScrubDropsWithPhrases(t *testing.T) {
	for in, want := range map[string]string{
		"Long run w/ Tom Smith": "Long run",
		"Legs with Sarah":       "Legs",
		"Run with the club":     "Run with the club",
		"Hyrox with @jo_runs":   "Hyrox",
	} {
		if got := Scrub(in); got != want {
			t.Errorf("Scrub(%q)=%q want %q", in, got, want)
		}
	}
}

func TestSensitive(t *testing.T) {
	for _, s := range []string{"NA meeting walk", "recovery coffee", "recovery run"} {
		if !Sensitive(s) {
			t.Errorf("%q should be sensitive", s)
		}
	}
	if Sensitive("Easy 5k in the sun") {
		t.Error("false positive")
	}
}

func TestNamesFreeRejectsUnseenProperNoun(t *testing.T) {
	if !namesFree("Colwick delivered again.", "Colwick parkrun") {
		t.Error("context name should pass")
	}
	if namesFree("Sarah would be proud.", "Colwick parkrun") {
		t.Error("Sarah should fail")
	}
	if namesFree("Ran past Tom at the lights.", "Colwick parkrun") {
		t.Error("mid-sentence Tom should fail")
	}
	if !namesFree("Easy 5 KM at 140 BPM, Strava says PB.", "") {
		t.Error("units and allowlist should pass")
	}
	if !namesFree(`He said "Nice" and left.`, "") {
		t.Error("after quote should pass")
	}
}

func TestNamesFreeAllowsSentenceStart(t *testing.T) {
	if !namesFree("Legs were toast. Proper toast.", "") {
		t.Error("sentence starts should pass")
	}
}

func TestLine(t *testing.T) {
	got := Line("**Big**   day\n# out. Second sentence runs on and on", 20)
	if got != "Big day out." {
		t.Errorf("got %q", got)
	}
	if Line("\xff\xfeok", 10) != "ok" {
		t.Errorf("utf8: %q", Line("\xff\xfeok", 10))
	}
}

func TestPossessiveNames(t *testing.T) {
	for _, s := range []string{"Ran. Chris's proud.", "Ran. Jess's proud.", "Ran. Chris’s proud."} {
		if namesFree(s, "") {
			t.Errorf("%q should fail", s)
		}
	}
}

func TestScrubGaps(t *testing.T) {
	for in, want := range map[string]string{
		"Run w/Tom":                      "Run",
		"Run with Tom, Dan and Sarah":    "Run",
		"Run with Tom, Dan & Sarah Jane": "Run",
		"Run, ft Tom":                    "Run",
		"Run with Tom's mates":           "Run mates",
	} {
		if got := Scrub(in); got != want {
			t.Errorf("Scrub(%q)=%q want %q", in, got, want)
		}
	}
}

func TestCheck(t *testing.T) {
	if Check("Colwick again. Legs gone.", "Colwick parkrun") != nil {
		t.Error("clean line should pass")
	}
	for _, s := range []string{"Day 30 sober.", "Pints after.", "Sarah would be proud.", "Ran with Tom."} {
		if Check(s, "Colwick parkrun") == nil {
			t.Errorf("%q should fail", s)
		}
	}
}

// A sentence can end on an emoji in his captions; the next capital is grammar.
func TestCheckSentenceStartAfterEmoji(t *testing.T) {
	if err := Check("Legs on fire 🥵 Still smiling 💪 Bloody brutal… Cooked", "Run 8.2 km"); err != nil {
		t.Errorf("emoji-separated sentences should pass: %v", err)
	}
	if Check("Legs on fire 🥵 Sarah would be proud", "Run") == nil {
		t.Error("a first name after an emoji must still fail")
	}
	if Check("Legs on fire 🥵 and Sarah", "Run") == nil {
		t.Error("a mid-sentence name after an emoji must still fail")
	}
}

func TestCheckAllowsFirstPersonI(t *testing.T) {
	if err := Check("Legs gone 🥵 and I'm still standing, I think", "Run"); err != nil {
		t.Errorf("first person must pass: %v", err)
	}
}
