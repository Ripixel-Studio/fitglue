// Package voice holds the one voice guide (spec 6, contract C3) and the two
// guards around AI text: Scrub on the way in, NamesFree on the way out.
package voice

import (
	_ "embed"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

//go:embed fallback.md
var fallback string

// words.txt: lowercase English words of 2-10 letters, from SCOWL (Spell Checker
// Oriented Word Lists, Kevin Atkinson; permissive MIT-style licence), the
// british-english + american-english lists shipped as Debian wbritish/wamerican
// 2020.12.07, proper nouns excluded. No frequency data was available, so it is
// the plain ~53k list rather than a 10-20k frequency cut.
//
//go:embed words.txt
var wordsTxt string

var commonWords = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(wordsTxt) {
		m[w] = true
	}
	return m
}()

// commonWord: the lowercase form is an ordinary English word. Callers must also
// check the first-names list; a name that is a word (Will, Grace) stays blocked.
func commonWord(tok string) bool {
	return commonWords[strings.ToLower(tok)]
}

const (
	guideTTL = 10 * time.Minute
	guideCap = 6 * 1024
)

var (
	guideMu  sync.Mutex
	guideAt  time.Time
	guideVal string
)

// ResetGuideCache drops the cached guide so a test can point FITGLUE_VOICE_FILE
// at its own file regardless of test order.
func ResetGuideCache() { guideMu.Lock(); guideAt = time.Time{}; guideVal = ""; guideMu.Unlock() }

// Guide returns the voice guide, re-read at most every 10 minutes, capped at 6 KB.
// Missing or empty file gives the embedded fallback.
func Guide() string {
	guideMu.Lock()
	defer guideMu.Unlock()
	if guideVal != "" && time.Since(guideAt) < guideTTL {
		return guideVal
	}
	p := os.Getenv("FITGLUE_VOICE_FILE")
	if p == "" {
		p = filepath.Join(os.Getenv("HOME"), "jarvis-memory", "voice", "james.md")
	}
	v := ""
	if b, err := os.ReadFile(p); err == nil {
		if len(b) > guideCap {
			b = b[:guideCap]
		}
		v = strings.ToValidUTF8(strings.TrimSpace(string(b)), "")
	}
	if v == "" {
		v = strings.TrimSpace(fallback)
	}
	guideVal, guideAt = v, time.Now()
	return v
}

var (
	capWord  = `\p{Lu}[\p{L}'’-]*`
	nameRun  = capWord + `(?:\s+` + capWord + `){0,2}(?:['’]s)?`
	personRe = regexp.MustCompile(`(?:^|\s)(?:w/\s*|(?:with|ft\.?|feat\.?|&|and)\s+)` + nameRun +
		`(?:\s*(?:,|,?\s*(?:and|&))\s*` + nameRun + `)*`)
	trailConn = regexp.MustCompile(`(?i)\s+(?:with|w/|ft\.?|feat\.?|&|and)\s*$`)
	handleRe  = regexp.MustCompile(`@\w+`)
	spacesRe  = regexp.MustCompile(`\s+`)
	mdRe      = regexp.MustCompile("[*#`]")
	recoveryR = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:` + strings.Join([]string{
		// Keep in step with RECOVERY_WORDS in jarvis gateway/lifehub/screen.py.
		`anonymous`, `sober(?:ly)?`, `sobriety`, `recovery`, `recovering`, `12[- ]?steps?`,
		`twelve[- ]?steps?`, `addict(?:ion|ions|s|ed)?`, `clean (?:days?|time)`, `days? clean`,
		`days? sober`, `relapse[sd]?`, `rehab`, `detox`, `narcotics`, `alcoholics?`,
		`hungover`, `spliff`, `pints?`, `aa`, `na`, `the program(?:me)?`,
	}, "|") + `)(?:$|[^a-z0-9])`)
)

// Scrub removes person phrases ("with Tom Smith", "w/ Sarah", "ft. X", "& X",
// "and X" followed by 1-3 capitalised words) and @handles. Lowercase
// continuations ("with the club") are left alone.
func Scrub(s string) string {
	s = handleRe.ReplaceAllString(s, "")
	// A bare trailing connector left by a removed handle ("Hyrox with ").
	s = personRe.ReplaceAllString(s, "")
	s = trailConn.ReplaceAllString(s, "")
	s = strings.TrimSpace(spacesRe.ReplaceAllString(s, " "))
	return strings.TrimRight(s, " ,;")
}

// Sensitive reports whether s touches recovery vocabulary. Fails closed: it
// also catches "recovery run". Source: jarvis gateway/lifehub/screen.py RECOVERY_WORDS.
func Sensitive(s string) bool { return recoveryR.MatchString(s) }

var allow = map[string]bool{
	"January": true, "February": true, "March": true, "April": true, "May": true, "June": true,
	"July": true, "August": true, "September": true, "October": true, "November": true, "December": true,
	"Monday": true, "Tuesday": true, "Wednesday": true, "Thursday": true, "Friday": true,
	"Saturday": true, "Sunday": true,
	"Strava": true, "Hevy": true, "Hyrox": true, "parkrun": true, "Zone": true, "PB": true,
	"PR": true, "I": true, "I'm": true, "I’m": true, "I'll": true, "I’ll": true, "I've": true, "I’ve": true, "I'd": true, "I’d": true, "HR": true, "HRV": true, "VO2": true, "VO₂": true, "AI": true,
}

// commonFirstNames: a sentence-initial capital is normally just grammar, but
// "Sarah would be proud." must still fail, so sentence starters are checked
// against this list (plus the context rule). Not exhaustive; a backstop.
var commonFirstNames = func() map[string]bool {
	m := map[string]bool{}
	for _, n := range strings.Fields(`James Jim Jimmy John Jon Jack Joe Jo Joey Tom Tommy Thomas Tim Dan Danny
	Daniel Dave David Dad Mum Mark Matt Matthew Mike Michael Chris Steve Stephen Paul Pete Peter Rob Robert Rich
	Richard Ben Sam Andy Andrew Adam Alex Nick Luke Will Harry Henry Ed Ian Gary Gareth Simon Sean Phil Tony
	Sarah Sara Sally Sophie Sophia Emma Emily Em Kate Katie Kath Claire Clare Laura Lucy Lisa Liz Beth Anna
	Anne Annie Amy Amanda Becky Rebecca Charlotte Hannah Helen Holly Jen Jenny Jess Jessica Julie Karen Kim
	Louise Lou Megan Mel Nicola Nic Rachel Rach Ruth Sue Susan Tash Zoe Abi Abbie
	Grace Rose Bill Billy Bob Bobby Joy Hope Faith Max Ray Rory Kev Kevin Neil Ross Craig Chloe Ella Eve Eva
	Ivy Jane Joan Josh Joshua Lee Leo Liam Lily Lucas Mia Nina Olivia Oliver Ollie Pat Priya Ryan Sophie Tess
	Toby Tina Vicky Victoria Wendy April May June August Mary Maria Jordan Jamie Charlie Frank Fred George
	Hugh Jake Jay Jeff Jerry Kyle Larry Mick Nathan Ned Nigel Owen Pam Pippa Rod Roy Sid Stan Stu Ted Tara`) {
		m[n] = true
	}
	return m
}()

var tokenRe = regexp.MustCompile(`[\p{L}\p{N}₂][\p{L}\p{N}₂'’-]*`)

func contextTokens(c string) map[string]bool {
	m := map[string]bool{}
	for _, t := range tokenRe.FindAllString(c, -1) {
		m[t] = true
	}
	return m
}

// endsInEmoji: a real emoji (Extended_Pictographic ranges, VS16, ZWJ, skin
// tones) ends a caption sentence in James's voice ("brutal 🥵 Still smiling").
// Math, currency and arrow symbols (+ = → £) are never a boundary.
func endsInEmoji(s string) bool {
	r, _ := utf8.DecodeLastRuneInString(s)
	switch {
	case r == 0xFE0F, r == 0x200D:
		return true
	case r >= 0x1F000 && r <= 0x1FAFF, r >= 0x2600 && r <= 0x27BF, r >= 0x2B00 && r <= 0x2BFF,
		r >= 0x2300 && r <= 0x23FF, r == 0x203C, r == 0x2049, r == 0x2122, r == 0x2139:
		return true
	}
	return false
}

// starters are capitalised words that are not names: sentence openers,
// numbers, and activity words. After a sentence boundary a capital word must be
// one of these (or fitness context / allow); anything else fails the check.
var starters = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(`a an the this that these those it its there here then than so but and or
	nor yet if when while what why how who where which whose because since until after before again also
	almost another any all both each either every few more most much many some such no not nothing none
	one two three four five six seven eight nine ten eleven twelve fifteen twenty thirty forty fifty sixty
	seventy eighty ninety hundred first second third last next half quarter whole
	i me my mine we us our you your he him his she her they them their let lets
	is was were be been being am are do does did done doing have has had having can could will would shall
	should may might must get got gets getting go goes going gone went come came coming make made makes
	take took taken keep kept give gave put set ran run running runs rode ride riding walk walked walking
	swam swim swimming lifted lift lifting hit hits hitting push pushed pushing pull pulled pulling
	felt feel feels feeling think thought know knew see saw look looked say said still just only even ever
	never always often sometimes now today tonight tomorrow yesterday later soon already finally actually
	honestly basically literally properly proper really very quite pretty absolutely totally completely
	well yes yeah yep nope no ok okay oh ah ha haha hah hmm um uh ugh urgh eek phew wow whoa blimey
	cheers right sure fine good great nice lovely proper decent solid strong steady easy hard tough
	brutal brutally horrific horrifically killer savage savagely cooked knackered shattered wrecked
	destroyed dead dying died spent done smashed crushed nailed bossed cheeky cheekily sweaty sweat
	hot cold warm wet dry windy rainy sunny muggy freezing boiling chilly grim mad mental unhinged
	wild proper legs leg quads quad calves glutes glute hamstrings hams hammies lungs lung heart hr
	arms arm shoulders shoulder back core abs chest feet foot knees knee hips hip ankles lats traps
	cardio zone zones pace paces pacing tempo intervals interval reps rep sets set squats squat deadlift
	deadlifts bench press presses row rows rowing curl curls lunges lunge plank planks burpees burpee
	sled sleds sleds wall balls ball box jumps jump skier skiers hyrox parkrun strava hevy
	km mile miles minutes minute mins min hours hour seconds second secs sec sesh session sessions
	workout workouts training train trained trains effort efforts hills hill hilly flat trail trails
	road roads track treadmill gym garage turbo spin bike cycling cycle swimming pool lake sea
	morning afternoon evening night lunch lunchtime dinner breakfast early late
	mondays tuesdays wednesdays thursdays fridays saturdays sundays weekend weekends week weeks
	month months year years day days time times lap laps mile miles kilometre kilometres
	big small little lil huge tiny long short quick slow fast faster slower better worse best worst
	new old different same other another next previous recent once twice thrice
	lessgo lessgooo thoughts note notes ps pb pr pbs prs hrs bpm watts watt kg kgs lb lbs
	oof ouch yikes gah argh eh meh bit bits lot lots loads load plenty enough
	somehow someday somewhere nowhere everywhere anywhere everything anything something
	nobody everybody everyone anyone someone whoever whatever whenever wherever
	not nah yup yay woohoo hooray hurrah boom bam pow zoom whoosh
	bloody bloomin flipping holy chuffed proud gutted buzzing blown wiped hurting hurts aching sore stiff
	grinding grind grinded sweating suffering suffered suffer pain painful agony misery fun funny
	forty-five twenty-five`) {
		m[w] = true
	}
	return m
}()

// startsOK reports whether a capitalised word at a sentence boundary is a known
// non-name: starters, or the first hyphen/apostrophe segment of one.
func startsOK(tok string) bool {
	l := strings.ToLower(tok)
	if starters[l] {
		return true
	}
	if i := strings.IndexAny(l, "'’"); i > 0 && starters[l[:i]] {
		return true
	}
	if i := strings.Index(l, "-"); i > 0 && starters[l[:i]] {
		return true
	}
	return false
}

func allCaps(t string) bool {
	has := false
	for _, r := range t {
		if unicode.IsLower(r) {
			return false
		}
		if unicode.IsUpper(r) {
			has = true
		}
	}
	return has
}

// namesFree is false when out contains a capitalised word that is not at a
// sentence start (or just after a quote), not in context, not allowlisted and
// not an all-caps unit. Sentence-initial words fail only if they are a known
// first name and absent from context.
func namesFree(out, context string) bool {
	ctx := contextTokens(context)
	for _, loc := range tokenRe.FindAllStringIndex(out, -1) {
		tok := out[loc[0]:loc[1]]
		r, _ := utf8.DecodeRuneInString(tok)
		if !unicode.IsUpper(r) {
			continue
		}
		base := strings.TrimSuffix(strings.TrimSuffix(tok, "'s"), "’s")
		known := commonFirstNames[base]
		// Context never whitelists a known first name, and callers pass only
		// fitness vocabulary (never his free text) as context.
		if (ctx[tok] && !known) || allow[tok] || (allCaps(tok) && utf8.RuneCountInString(tok) > 1) {
			continue
		}
		if known {
			return false
		}
		if i := strings.TrimRight(out[:loc[0]], " \t\n"); i == "" || strings.HasSuffix(i, `"`) ||
			strings.HasSuffix(i, "“") || strings.HasSuffix(i, ".") || strings.HasSuffix(i, "!") || strings.HasSuffix(i, "?") ||
			strings.HasSuffix(i, "…") || endsInEmoji(i) {
			if startsOK(tok) || commonWord(tok) {
				continue
			}
			return false
		}
		return false
	}
	return true
}

// Check is the one output guard: nil only if out names nobody (given the
// context it was written from) and touches no recovery vocabulary. Callers
// must use this, never the halves.
func Check(out, context string) error {
	if Sensitive(out) {
		return errors.New("voice: output touches sensitive vocabulary")
	}
	if !namesFree(out, context) {
		return errors.New("voice: output names someone")
	}
	return nil
}

// Line tidies AI text to one line: whitespace collapsed, Markdown stripped,
// cut at the last sentence end within max runes, valid UTF-8.
func Line(s string, max int) string {
	s = strings.ToValidUTF8(s, "")
	s = mdRe.ReplaceAllString(s, "")
	s = strings.TrimSpace(spacesRe.ReplaceAllString(s, " "))
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	cut := string([]rune(s)[:max])
	if i := strings.LastIndexAny(cut, ".!?"); i >= 0 {
		return cut[:i+1]
	}
	// No sentence end: cut on a word boundary unless the next rune already is one.
	if next := []rune(s)[max]; next != ' ' {
		if i := strings.LastIndex(cut, " "); i > 0 {
			cut = cut[:i]
		}
	}
	return strings.TrimSpace(cut)
}

// RetryNote is appended to the system prompt on the one retry after a failed Check.
const RetryNote = "Your previous attempt was rejected. Do not name or refer to anyone. Open with a plain word."

// Generate runs gen (extra is "" first, RetryNote on the retry), tidies the
// output with Line and checks it with Check. One retry on a failed check; if the
// second attempt fails too it returns "" so the caller keeps its non-AI text.
// A gen error (including claude.ErrLimited) is returned at once, no retry.
// attempts is 1 or 2.
func Generate(gen func(extra string) (string, error), max int, checkContext string) (line string, attempts int, err error) {
	for attempts = 1; attempts <= 2; attempts++ {
		extra := ""
		if attempts == 2 {
			extra = RetryNote
		}
		out, gerr := gen(extra)
		if gerr != nil {
			return "", attempts, gerr
		}
		l := Line(out, max)
		if l != "" && Check(l, checkContext) == nil {
			return l, attempts, nil
		}
	}
	return "", 2, nil
}
