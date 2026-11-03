// nolint:proto-json // encoding/json writes plain structs (voiceSample), never a proto
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	firestore "cloud.google.com/go/firestore"

	pipelinestore "github.com/fitglue/server/src/go/internal/pipeline"
	pbpipeline "github.com/fitglue/server/src/go/pkg/types/pb/models/pipeline"
)

// voice-samples pulls James's own words out of every pipeline run, for the voice
// guide (life hub round 2, R2a.5). A run's title and description are written at
// ingest, before any enricher, so they hold what he typed on Strava or Hevy. The
// output is private: mode 0600 in a 0700 directory, and the command prints counts
// only, never a sample.

const minSampleRunes = 20

type runLister interface {
	ListRuns(ctx context.Context, since time.Time) ([]*pbpipeline.PipelineRun, error)
}

type voiceSamplesFlags struct {
	project string
	uid     string
	since   time.Time
	out     string
}

type voiceSample struct {
	Date  string `json:"date"`
	Type  string `json:"type"`
	Title string `json:"title"`
	Words string `json:"words"`
}

func defaultVoiceSamplesPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "voice-samples.jsonl"
	}
	return filepath.Join(home, ".jarvis", "fitglue", "voice-samples.jsonl")
}

func voiceSamplesCommand(ctx context.Context, args []string, stdout, stderr io.Writer, open func(ctx context.Context, f *voiceSamplesFlags) (runLister, func(), error)) error {
	f := &voiceSamplesFlags{}
	fs := flag.NewFlagSet("voice-samples", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&f.project, "project", envOr(projectEnv, defaultProject), "GCP project holding Firestore (default $"+projectEnv+", else fitglue-server-prod)")
	fs.StringVar(&f.uid, "uid", os.Getenv(ownerUIDEnv), "owner's user id (default $"+ownerUIDEnv+")")
	since := fs.String("since", "2025-01-01", "only runs created on or after this date (YYYY-MM-DD)")
	fs.StringVar(&f.out, "out", defaultVoiceSamplesPath(), "JSONL output (written 0600)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	t, err := time.Parse("2006-01-02", *since)
	if err != nil {
		return fmt.Errorf("--since: want YYYY-MM-DD: %w", err)
	}
	f.since = t
	if f.uid == "" {
		return errors.New("--uid is required (or set " + ownerUIDEnv + ")")
	}

	lister, cleanup, err := open(ctx, f)
	if err != nil {
		return err
	}
	defer cleanup()
	runs, err := lister.ListRuns(ctx, f.since)
	if err != nil {
		return fmt.Errorf("list pipeline runs: %w", err)
	}

	// Resends and reposts make several runs for one workout: keep one sample each.
	seen := map[string]bool{}
	var samples []voiceSample
	for _, r := range runs {
		words := athleteWords(r.GetDescription())
		if utf8.RuneCountInString(words) < minSampleRunes {
			continue
		}
		key := r.GetSource() + "/" + r.GetSourceActivityId()
		if r.GetSourceActivityId() == "" {
			key = "words/" + words
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		ts := r.GetStartTime()
		if ts == nil {
			ts = r.GetCreatedAt()
		}
		date := ""
		if ts != nil {
			date = ts.AsTime().UTC().Format("2006-01-02")
		}
		samples = append(samples, voiceSample{
			Date:  date,
			Type:  strings.ToLower(strings.TrimPrefix(r.GetType().String(), "ACTIVITY_TYPE_")),
			Title: r.GetTitle(),
			Words: words,
		})
	}

	if err := writePrivateJSONL(f.out, samples); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%d runs, %d with his words\n", len(runs), len(samples))
	return nil
}

// writePrivateJSONL writes the samples 0600, tightening a file that already
// exists with a looser mode. A directory it creates is 0700 (chmod'd past the
// umask); an existing directory is left as its owner set it.
func writePrivateJSONL(path string, samples []voiceSample) error {
	dir := filepath.Dir(path)
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("chmod %s: %w", dir, err)
		}
	} else if err != nil {
		return fmt.Errorf("stat %s: %w", dir, err)
	}
	fh, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	if err := fh.Chmod(0o600); err != nil {
		fh.Close()
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	enc := json.NewEncoder(fh)
	enc.SetEscapeHTML(false)
	for _, s := range samples {
		if err := enc.Encode(s); err != nil {
			fh.Close()
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	return fh.Close()
}

// firestoreRuns lists the owner's pipeline runs through the pipeline service's own
// store, as reconcile does.
type firestoreRuns struct {
	uid   string
	store *pipelinestore.FirestoreStore
}

func (l firestoreRuns) ListRuns(ctx context.Context, since time.Time) ([]*pbpipeline.PipelineRun, error) {
	runs, _, err := l.store.ListPipelineRuns(ctx, l.uid, "", 100000, "", &since, nil)
	return runs, err
}

func openLiveVoiceSamples(ctx context.Context, f *voiceSamplesFlags) (runLister, func(), error) {
	fs, err := firestore.NewClient(ctx, f.project)
	if err != nil {
		return nil, nil, fmt.Errorf("firestore client: %w", err)
	}
	return firestoreRuns{uid: f.uid, store: pipelinestore.NewFirestoreStore(fs)}, func() { fs.Close() }, nil
}

// --- athleteWords: a port of ripixel-website tasks/fitness-words.mjs (contract §3.1).
// The JS is the spec: same regexes, same paragraph rule.

var (
	reMachineNA      = regexp.MustCompile(`(?i)^(n/a|-|—)$`)
	reNumberLed      = regexp.MustCompile(`^[#+−-]?\d`)
	reRealWord       = regexp.MustCompile(`(?i)[a-z]{6,}`)
	reParens         = regexp.MustCompile(`\(.*\)`)
	reViaLine        = regexp.MustCompile(`(?i)^(completed|posted|uploaded|synced|recorded|logged|tracked) (via|with|by|on) \S+`)
	reBullet         = regexp.MustCompile(`^[•·]\s`)
	reSymbolLabel    = regexp.MustCompile(`^\S+\s+[^:]{1,40}:\s*\S`)
	reTitleHeader    = regexp.MustCompile(`^(?:[A-Z][\w'&()-]*\s?){1,3}:$`)
	reKeyValue       = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9 ()'/&-]{0,40}):\s*(.+)$`)
	rePostedFitGlue  = regexp.MustCompile(`\n?\n?Posted via FitGlue\s*💪`)
	reParagraphBreak = regexp.MustCompile(`\n\s*\n`)
)

// isMachineValue: a value that reads like machine output — starts with a number and
// carries no real words — or is N/A.
func isMachineValue(v string) bool {
	t := strings.TrimSpace(v)
	if reMachineNA.MatchString(t) {
		return true
	}
	// JS .length counts UTF-16 code units.
	return reNumberLed.MatchString(t) && len(utf16.Encode([]rune(t))) <= 32 && !reRealWord.MatchString(reParens.ReplaceAllString(t, ""))
}

func startsWithSymbol(line string) bool {
	r, size := utf8.DecodeRuneInString(line)
	return size > 0 && r > 0xff
}

// isMachineLine: emoji-led section headers or "label: value" lines, a plain stat
// line, a FitGlue bullet, "Completed via …", "Posted via FitGlue".
func isMachineLine(raw string) bool {
	line := strings.TrimSpace(raw)
	if line == "" {
		return false
	}
	if reViaLine.MatchString(line) {
		return true
	}
	if reBullet.MatchString(line) {
		return true
	}
	if startsWithSymbol(line) && (strings.HasSuffix(line, ":") || strings.Contains(line, " • ") || reSymbolLabel.MatchString(line)) {
		return true
	}
	if reTitleHeader.MatchString(line) {
		return true
	}
	if kv := reKeyValue.FindStringSubmatch(line); kv != nil && isMachineValue(kv[2]) {
		return true
	}
	return false
}

// athleteWords returns his paragraphs, in order, joined by blank lines. A paragraph
// is his when its first line is not machine-written and most of its lines are not.
// Scanning stops at the first machine paragraph after his words have started.
func athleteWords(text string) string {
	if text == "" {
		return ""
	}
	cleaned := strings.ReplaceAll(text, "\r\n", "\n")
	cleaned = strings.ReplaceAll(cleaned, "\r", "\n")
	cleaned = strings.TrimSpace(rePostedFitGlue.ReplaceAllString(cleaned, ""))
	if cleaned == "" {
		return ""
	}
	var kept []string
	for _, p := range reParagraphBreak.Split(cleaned, -1) {
		para := strings.TrimSpace(p)
		if para == "" {
			continue
		}
		var lines []string
		for _, l := range strings.Split(para, "\n") {
			if strings.TrimSpace(l) != "" {
				lines = append(lines, l)
			}
		}
		machine := 0
		for _, l := range lines {
			if isMachineLine(l) {
				machine++
			}
		}
		isHis := !isMachineLine(lines[0]) && machine*2 < len(lines)
		if isHis {
			var his []string
			for _, l := range lines {
				if !isMachineLine(l) {
					his = append(his, l)
				}
			}
			kept = append(kept, strings.Join(his, "\n"))
		} else if len(kept) > 0 {
			break
		}
	}
	return strings.TrimSpace(strings.Join(kept, "\n\n"))
}
