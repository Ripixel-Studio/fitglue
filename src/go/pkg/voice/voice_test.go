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
	if err != nil || got != "Nobody saw that coming" || n != 2 {
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

func TestCheckBlocksDrinkAndDrugs(t *testing.T) {
	for _, s := range []string{
		"Beer legs today.", "Ran off last night's hangover.", "Felt a bit drunk by the last rep.",
		"Earned a glass of wine.", "Stoned legs.", "Pub after.", "BEER legs.", "Two pints later.",
		"Gin o'clock.", "Totally wasted.", "Prosecco pace.", "Smelled of weed.", "Day one, rehab.",
		"Booze-free.", "Sober October.", "Vodka legs.", "AA tonight.",
	} {
		if Check(s, "Type: ACTIVITY_TYPE_RUN") == nil {
			t.Errorf("should block %q", s)
		}
	}
	// Word boundaries: no substring hits; "high" is left alone.
	for _, s := range []string{"Ginormous legs.", "Pubic bone fine.", "High reps, heavy day.", "Cooked the bench."} {
		if err := Check(s, "Type: ACTIVITY_TYPE_RUN"); err != nil && strings.Contains(err.Error(), "drink") {
			t.Errorf("over-blocked %q: %v", s, err)
		}
	}
}

// Regression: lines the guard wrongly rejected on James's box (round 2), with
// the context the enrichers really pass. The activity type is the raw enum.
func TestCheckFitnessVocabulary(t *testing.T) {
	pilates := "Type: ACTIVITY_TYPE_PILATES\nDuration: 50 mins"
	cross := "Type: ACTIVITY_TYPE_CROSSFIT"
	roundup := "week Monday Tuesday run crossfit weight training pilates"
	for _, c := range []struct{ out, ctx string }{
		{"Pilates for 45 mins, and my core is still wobbling like a jelly 🥴", pilates},
		{"Plenty of Pilates today, 50 mins with my heart rate at 76 avg. Max of 97 so not flat out 💪", pilates},
		{"Max 179 bpm and 373 W on average, legs doing their bit 💪", ""},
		{"Suffering is the whole point of CrossFit 🥵 Knackered but back for more", cross},
		{"Bloody hell, 100 wall balls on the Sled then SkiErg and Rowing. Kettlebell swings and Burpees after 💪", ""},
		{"Hyrox prep with Garmin and Hevy logging it all, Zone 2 and a PB on the Erg. HIIT, AMRAP, EMOM, Tabata.", ""},
		{"Ooh, a big week. Weights, then a hard session. Legs: gone", ""},
		{"Five PBs landed in one week, and two PRs on the Bench and Deadlift 💪", roundup},
		{"Two PBs landed, a 5K in 24:03 and a 1K in 4:38. Pilates twice, CrossFit once.", roundup},
	} {
		if err := Check(c.out, c.ctx); err != nil {
			t.Errorf("%q should pass: %v (offender %q)", c.out, err, Offender(c.out, c.ctx))
		}
	}
}

func TestContextTokensCaseInsensitive(t *testing.T) {
	m := contextTokens("Type: ACTIVITY_TYPE_WEIGHT_TRAINING")
	for _, w := range []string{"weight", "training", "activity"} {
		if !m[w] {
			t.Errorf("missing %q in %v", w, m)
		}
	}
	if !namesFree("Weight sessions are rough.", "ACTIVITY_TYPE_WEIGHT_TRAINING") {
		t.Error("normalised type word should pass")
	}
}

// Names must still fail with the wider vocabulary and the looser context.
func TestNamesStillFailWithFitnessVocabulary(t *testing.T) {
	ctx := "Type: ACTIVITY_TYPE_PILATES\nMax HR: 120"
	for _, s := range []string{
		"Pilates today with Priya, and my core is wobbling.",
		"Pilates for 45 mins. Priya would be proud.",
		"Great CrossFit class, thanks Marcus.",
		"Max would be proud of that Pilates class.",
		"Pilates, then Hyrox, then a coffee with Beatrix.",
		"Strava says Dorothea beat my PB.",
		"Sled push and a chat with Colm about the PBs.",
		"Squats were rough, Sarah laughed at me.",
	} {
		if Check(s, ctx) == nil {
			t.Errorf("%q should fail", s)
		}
	}
	// Context never whitelists a first name, even in the activity type.
	if namesFree("Lovely Sarah session.", "Type: ACTIVITY_TYPE_SARAH") {
		t.Error("context must not whitelist a known first name")
	}
	for _, s := range []string{"Ran with Pilates Priya.", "Max would be proud.", "Maxine did well."} {
		if namesFree(s, "") {
			t.Errorf("%q should fail", s)
		}
	}
}

func TestStripEmoji(t *testing.T) {
	cases := map[string]string{
		"brutal 🥵 Still smiling":             "brutal Still smiling",
		"Legs gone 💀.":                       "Legs gone.",
		"✨ AI Summary:\nFast one 🏃‍♂️ today": "AI Summary:\nFast one today",
		"Heart ❤️ and 👍🏽 done":               "Heart and done",
		"1️⃣ rep":                            "1 rep",
		"5 km → 10 km, £5 + 3 = 8":           "5 km → 10 km, £5 + 3 = 8",
		"plain text":                         "plain text",
		"🔥🔥🔥":                                "",
	}
	for in, want := range cases {
		if got := StripEmoji(in); got != want {
			t.Errorf("StripEmoji(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLineAndCheckStripEmoji(t *testing.T) {
	if got := Line("Easy run 😅 legs fine", 100); got != "Easy run legs fine" {
		t.Errorf("Line = %q", got)
	}
	if err := Check("Easy run 😅.", ""); err != nil {
		t.Errorf("Check: %v", err)
	}
}

// A roundup for May may say "May" at a sentence start (2026-11-05: April, May and June
// roundups were dropped every time); without its period token "May" is still a name.
func TestPeriodMonthAllowsThatMonthOnly(t *testing.T) {
	line := "May was a proper mixed bag, seven sessions in all."
	if Check(line, "month") == nil {
		t.Error("a bare May with no period context must still fail")
	}
	if err := Check(line, "month "+PeriodMonthToken("May")); err != nil {
		t.Errorf("May in a May roundup should pass: %v", err)
	}
	if Check("June was next, and so was May.", "month "+PeriodMonthToken("April")) == nil {
		t.Error("an April roundup does not clear May or June")
	}
}

// 2026-11-11: Haiku answered the retry with its brief ("Plain word opener, third person,
// no name: the summary."); Check refuses an echo, keeps ordinary fitness prose.
func TestCheckRefusesEchoedInstructions(t *testing.T) {
	for _, bad := range []string{
		"Plain word opener, third person, no name: the summary. Hard going, but the session shows the point of it.",
		"Here is the summary: a solid tempo run.",
		"The summary: six rounds, steady pacing.",
	} {
		if Check(bad, "") == nil {
			t.Errorf("echo passed: %q", bad)
		}
	}
	for _, ok := range []string{
		"Fifty-eight minutes with most of it in zone four says this was a threshold session, and the wall balls at the end came on tired legs.",
		"A steady hour that kept the heart rate honest without ever tipping into a race effort.",
		"A 50-minute session, and the heart rate tells the story: an average of 88 bpm.",
	} {
		if err := Check(ok, "wall balls threshold zone"); err != nil {
			t.Errorf("good line refused: %q: %v", ok, err)
		}
	}
}

// Words the guard refused in the November 2026 regen, none of them names.
func TestOffenderPassesFitnessCompoundsAndUnits(t *testing.T) {
	for _, ok := range []string{
		"A Hyrox-style hour that leaned toward skill over strain.",
		"A Saturday-morning parkrun that came in at just over 23 minutes.",
		"Sub-24 for a 5k is a proper marker.",
		"Skin temperature climbed from 28.1°C to 42.5 °C in the heat.",
		"Week three of a Couch to 5k plan, and the intervals did their job.",
		"Fifty minutes of Tower Pilates, then a Reformer session the next day.",
	} {
		if tok := Offender(ok, ""); tok != "" {
			t.Errorf("refused %q on %q", ok, tok)
		}
	}
	for _, bad := range []string{
		"A run alongside Sarah-Jane at an easy pace.",
		"A Rome-bound week of training.",
		"The C team turned up.",
	} {
		if Offender(bad, "") == "" {
			t.Errorf("passed %q", bad)
		}
	}
}
