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

const (
	guideTTL = 10 * time.Minute
	guideCap = 6 * 1024
)

var (
	guideMu  sync.Mutex
	guideAt  time.Time
	guideVal string
)

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
	"PR": true, "HR": true, "HRV": true, "VO2": true, "VO₂": true, "AI": true,
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
	Louise Lou Megan Mel Nicola Nic Rachel Rach Ruth Sue Susan Tash Zoe Abi Abbie`) {
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
		if ctx[tok] || allow[tok] || (allCaps(tok) && utf8.RuneCountInString(tok) > 1) {
			continue
		}
		if i := strings.TrimRight(out[:loc[0]], " \t\n"); i == "" || strings.HasSuffix(i, `"`) ||
			strings.HasSuffix(i, "“") || strings.HasSuffix(i, ".") || strings.HasSuffix(i, "!") || strings.HasSuffix(i, "?") {
			if commonFirstNames[strings.TrimSuffix(strings.TrimSuffix(tok, "'s"), "’s")] {
				return false
			}
			continue
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
	return strings.TrimSpace(cut)
}
