package voice

import (
	"errors"
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

// A sentence can end on an emoji; the next capital must still be a known non-name.
func TestCheckSentenceStartAfterEmoji(t *testing.T) {
	if err := Check("Legs on fire 🥵 Still smiling 💪 Bloody brutal… Cooked", "Run 8.2 km"); err != nil {
		t.Errorf("emoji-separated sentences should pass: %v", err)
	}
}

func TestCheckRejectsNamesAfterBoundaries(t *testing.T) {
	for _, s := range []string{
		"Legs on fire 🥵 Sarah would be proud", "Legs on fire 🥵 and Sarah",
		"🔥 Priya smashed it", "Brutal… Kev came too", "Legs gone 🥵 Priya paced it",
		"PB 🎉 Gav was there", "Cooked ❤️ Becca", "8.2 km + Kev on the hills",
		"5k → Priya pacing", "Sub-25 = Kev buying coffee", "Hills £ Kev", "Kev paced it.",
		"Done. Priya smashed it", "Priya paced it",
	} {
		if Check(s, "Type: ACTIVITY_TYPE_RUN") == nil {
			t.Errorf("%q should fail", s)
		}
	}
}

func TestContextNeverWhitelistsFirstName(t *testing.T) {
	if Check("Tom paced it", "Original Name: Tom") == nil {
		t.Error("a known first name in context must still fail")
	}
}

func TestCheckAllowsFirstPersonI(t *testing.T) {
	if err := Check("Legs gone 🥵 and I'm still standing, I think", "Run"); err != nil {
		t.Errorf("first person must pass: %v", err)
	}
}

func TestEndsInEmoji(t *testing.T) {
	for _, s := range []string{"x 🔥", "x ❤️", "x 💪🏽", "x 🥵"} {
		if !endsInEmoji(s) {
			t.Errorf("%q should end in emoji", s)
		}
	}
	for _, s := range []string{"x +", "x =", "x →", "x £", "x ^", "x ~", "x <", "x"} {
		if endsInEmoji(s) {
			t.Errorf("%q must not count", s)
		}
	}
}

func TestLineCutsOnWordBoundary(t *testing.T) {
	got := Line(strings.Repeat("legs gone ", 40), 25)
	if got != "legs gone legs gone legs" {
		t.Errorf("got %q", got)
	}
}

func TestCommonWordsPassAtBoundary(t *testing.T) {
	for _, s := range []string{"Nobody saw that coming", "Apparently my legs work", "Four runs. Honestly, knackered 💪"} {
		if err := Check(s, ""); err != nil {
			t.Errorf("%q should pass: %v", s, err)
		}
	}
}

func TestNamesStillFailAtBoundary(t *testing.T) {
	for _, s := range []string{"Priya smashed it", "🔥 Zoltan", "Four runs. Kev kept up", "Will smashed it", "Grace was there"} {
		if Check(s, "") == nil {
			t.Errorf("%q should fail", s)
		}
	}
}

func TestGenerateRetry(t *testing.T) {
	var extras []string
	seq := []string{"Priya smashed it", "Nobody saw that coming 💪"}
	got, n, err := Generate(func(extra string) (string, error) {
		extras = append(extras, extra)
		o := seq[0]
		seq = seq[1:]
		return o, nil
	}, 280, "")
	if err != nil || got != "Nobody saw that coming 💪" || n != 2 {
		t.Fatalf("got %q n=%d err=%v", got, n, err)
	}
	if extras[0] != "" || extras[1] != RetryNote {
		t.Errorf("extras = %q", extras)
	}
	// Both fail: empty, never the raw output.
	calls := 0
	got, n, _ = Generate(func(string) (string, error) { calls++; return "Priya smashed it", nil }, 280, "")
	if got != "" || calls != 2 || n != 2 {
		t.Errorf("both-fail: %q calls=%d", got, calls)
	}
	// First passes: no retry. Error: no retry.
	calls = 0
	got, n, _ = Generate(func(string) (string, error) { calls++; return "Legs shaking 💪", nil }, 280, "")
	if got == "" || calls != 1 || n != 1 {
		t.Errorf("first-pass: %q calls=%d", got, calls)
	}
	calls = 0
	_, _, err = Generate(func(string) (string, error) { calls++; return "", errors.New("limited") }, 280, "")
	if err == nil || calls != 1 {
		t.Errorf("error path: err=%v calls=%d", err, calls)
	}
}

func TestWordNamesFailAtBoundary(t *testing.T) {
	for _, n := range strings.Fields("Dawn Ruby Amber Robin Hazel Penny Heather Daisy Ken Victor Colleen Terry Smith Iris Jasmine Olive Violet Pearl Rick Carol Sandy Drew Roger Martin Wade Dale") {
		if Check(n+" smashed it", "") == nil {
			t.Errorf("%s smashed it should fail", n)
		}
		if Check("Four runs. "+n+" kept up", "") == nil {
			t.Errorf("Four runs. %s kept up should fail", n)
		}
	}
}

func TestSafeStartersPass(t *testing.T) {
	for _, w := range strings.Fields("Nobody Apparently Somehow Legs Turns Honestly Still Proper Absolute Somewhere Everyone") {
		if err := Check(w+" worked", ""); err != nil {
			t.Errorf("%s should pass: %v", w, err)
		}
	}
}

func TestAmbiguousMonths(t *testing.T) {
	for _, s := range []string{"Last ran in May 💪", "Since June I'm faster", "Great week. 3 May was brutal", "May 3 was brutal", "Best since April"} {
		if err := Check(s, ""); err != nil {
			t.Errorf("%q should pass: %v", s, err)
		}
	}
	for _, s := range []string{"May smashed it", "Ran with May", "April was cold", "Done. June joined me", "Ran with April and it hurt"} {
		if Check(s, "") == nil {
			t.Errorf("%q should fail", s)
		}
	}
	if Check("January was cold", "") != nil {
		t.Errorf("January stays allowed")
	}
}

func TestJadeAndMiles(t *testing.T) {
	for _, s := range []string{"Jade smashed it", "Four runs. Jade kept up", "Four runs. Miles kept up", "Miles smashed it", "Ran with Miles", "Miles"} {
		if Check(s, "") == nil {
			t.Errorf("%q should fail", s)
		}
	}
	for _, s := range []string{"Miles: 6.2", "Miles 6.2 today", "Miles - 6", "Ran 6 miles 💪", "Four runs. Miles: 20"} {
		if err := Check(s, ""); err != nil {
			t.Errorf("%q should pass: %v", s, err)
		}
	}
}
