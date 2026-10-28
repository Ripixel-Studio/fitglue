package siteexport

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
)

// Source is the read side of the export: the same data the public showcase
// pages are assembled from. ServiceSource adapts the activity service and
// store; tests supply fakes.
type Source interface {
	// Profile returns the public profile for slug with topPrs filled, as
	// GetPublicShowcaseProfile returns it (contract §2). It must fail when the
	// profile does not exist or is not visible.
	Profile(ctx context.Context, slug string) (*pbactivity.ShowcaseProfile, error)
	// Entries lists the user's showcase_profile_entries, newest first.
	Entries(ctx context.Context, userID string) ([]*pbactivity.ShowcaseProfileEntry, error)
	// Showcase returns the hydrated ShowcasedActivity as GetPublicShowcase
	// returns it, or nil when it no longer exists.
	Showcase(ctx context.Context, showcaseID string) (*pbactivity.ShowcasedActivity, error)
	// Roundups lists every roundup for slug, newest period first.
	Roundups(ctx context.Context, slug string) ([]*pbactivity.ShowcaseRoundup, error)
	// Roundup returns one roundup, or nil when it does not exist.
	Roundup(ctx context.Context, slug, periodKey string) (*pbactivity.ShowcaseRoundup, error)
}

// Writer is the destination: the site folder of the showcase-assets bucket, or
// a local directory for --dry-run. Names are relative to the site folder
// ("index.json", "activity/<id>.json", "roundup/<key>.json").
type Writer interface {
	Write(ctx context.Context, name string, data []byte) error
	Delete(ctx context.Context, name string) error
	// List returns the names (relative, as passed to Write) under prefix.
	List(ctx context.Context, prefix string) ([]string, error)
}

// Deps is what every export function needs.
type Deps struct {
	Source Source
	Writer Writer
	// Now is the clock for exportedAt/generatedAt; time.Now when nil.
	Now func() time.Time
	// Logf receives progress lines; discarded when nil.
	Logf func(format string, args ...any)
	// Concurrency bounds parallel activity reads/writes; 8 when 0.
	Concurrency int
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now().UTC()
	}
	return time.Now().UTC()
}

func (d Deps) logf(format string, args ...any) {
	if d.Logf != nil {
		d.Logf(format, args...)
	}
}

func (d Deps) concurrency() int {
	if d.Concurrency > 0 {
		return d.Concurrency
	}
	return 8
}

// Options control ExportAll.
type Options struct {
	// Since restricts the activity and roundup writes to entries created, and
	// roundups generated, after this instant. index.json is always rewritten.
	Since *time.Time
	// Prune deletes activity/roundup files whose id is no longer in the
	// profile (the --all repair path).
	Prune bool
}

// Result counts what an export did.
type Result struct {
	Activities int
	Roundups   int
	Skipped    int // entries without a showcase document, or expired
	Deleted    int
}

// ErrNotExportable marks an activity that exists in the profile but has no
// showcase document, or whose showcase has expired.
var ErrNotExportable = errors.New("siteexport: activity is not exportable")

const (
	indexName     = "index.json"
	activityDir   = "activity/"
	roundupDir    = "roundup/"
	fileExtension = ".json"
)

func activityName(id string) string  { return activityDir + id + fileExtension }
func roundupName(key string) string  { return roundupDir + key + fileExtension }
func idFromName(name string) string  { return strings.TrimSuffix(path.Base(name), fileExtension) }
func hasJSONExtension(n string) bool { return strings.HasSuffix(n, fileExtension) }

// ExportAll writes every activity and roundup file for slug (filtered by
// opts.Since), prunes orphans when asked, and finally rewrites index.json so a
// reader never sees an index pointing at a missing file (contract §1).
func ExportAll(ctx context.Context, deps Deps, slug string, opts Options) (Result, error) {
	var res Result
	profile, err := deps.Source.Profile(ctx, slug)
	if err != nil {
		return res, fmt.Errorf("profile %q: %w", slug, err)
	}
	entries, err := deps.Source.Entries(ctx, profile.UserId)
	if err != nil {
		return res, fmt.Errorf("entries: %w", err)
	}
	roundups, err := deps.Source.Roundups(ctx, slug)
	if err != nil {
		return res, fmt.Errorf("roundups: %w", err)
	}
	deps.logf("profile %s: %d entries, %d roundups", slug, len(entries), len(roundups))

	// Activities, in parallel.
	var selected []*pbactivity.ShowcaseProfileEntry
	for _, e := range entries {
		if opts.Since == nil || (e.CreatedAt != nil && e.CreatedAt.AsTime().After(*opts.Since)) {
			selected = append(selected, e)
		}
	}
	written, skipped, err := exportEntries(ctx, deps, selected)
	res.Activities, res.Skipped = written, skipped
	if err != nil {
		return res, err
	}

	// Roundups.
	for _, r := range roundups {
		if opts.Since != nil && (r.GeneratedAt == nil || !r.GeneratedAt.AsTime().After(*opts.Since)) {
			continue
		}
		if err := writeRoundup(ctx, deps, r); err != nil {
			return res, err
		}
		res.Roundups++
	}

	// Prune orphans before the index is rewritten.
	if opts.Prune {
		keep := map[string]bool{}
		for _, e := range entries {
			keep[activityName(e.ShowcaseId)] = true
		}
		for _, r := range roundups {
			keep[roundupName(r.PeriodKey)] = true
		}
		for _, dir := range []string{activityDir, roundupDir} {
			existing, err := deps.Writer.List(ctx, dir)
			if err != nil {
				return res, fmt.Errorf("list %s: %w", dir, err)
			}
			for _, name := range existing {
				if keep[name] || !hasJSONExtension(name) {
					continue
				}
				deps.logf("delete orphan %s", name)
				if err := deps.Writer.Delete(ctx, name); err != nil {
					return res, fmt.Errorf("delete %s: %w", name, err)
				}
				res.Deleted++
			}
		}
	}

	if err := writeIndex(ctx, deps, profile, entries, roundups); err != nil {
		return res, err
	}
	return res, nil
}

// ExportActivity writes activity/<showcaseID>.json for one activity. It does
// not touch index.json; call RefreshIndex afterwards. Returns ErrNotExportable
// when the activity has no showcase document or has expired.
func ExportActivity(ctx context.Context, deps Deps, slug, showcaseID string) error {
	profile, err := deps.Source.Profile(ctx, slug)
	if err != nil {
		return fmt.Errorf("profile %q: %w", slug, err)
	}
	entries, err := deps.Source.Entries(ctx, profile.UserId)
	if err != nil {
		return fmt.Errorf("entries: %w", err)
	}
	for _, e := range entries {
		if e.ShowcaseId == showcaseID {
			return writeActivity(ctx, deps, e)
		}
	}
	return fmt.Errorf("%w: %s is not in the profile", ErrNotExportable, showcaseID)
}

// ExportRoundup writes roundup/<periodKey>.json for one roundup. It does not
// touch index.json; call RefreshIndex afterwards.
func ExportRoundup(ctx context.Context, deps Deps, slug, periodKey string) error {
	r, err := deps.Source.Roundup(ctx, slug, periodKey)
	if err != nil {
		return fmt.Errorf("roundup %s: %w", periodKey, err)
	}
	if r == nil {
		return fmt.Errorf("%w: roundup %s does not exist", ErrNotExportable, periodKey)
	}
	return writeRoundup(ctx, deps, r)
}

// RefreshIndex rewrites index.json from the profile, its entries and roundups,
// listing only the activities and roundups whose files exist in the
// destination (the index is the source of truth for what exists, §1).
func RefreshIndex(ctx context.Context, deps Deps, slug string) error {
	profile, err := deps.Source.Profile(ctx, slug)
	if err != nil {
		return fmt.Errorf("profile %q: %w", slug, err)
	}
	entries, err := deps.Source.Entries(ctx, profile.UserId)
	if err != nil {
		return fmt.Errorf("entries: %w", err)
	}
	roundups, err := deps.Source.Roundups(ctx, slug)
	if err != nil {
		return fmt.Errorf("roundups: %w", err)
	}
	return writeIndex(ctx, deps, profile, entries, roundups)
}

func exportEntries(ctx context.Context, deps Deps, entries []*pbactivity.ShowcaseProfileEntry) (written, skipped int, err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		firstErr error
		sem      = make(chan struct{}, deps.concurrency())
	)
	for _, e := range entries {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return written, skipped, firstErr
		}
		wg.Add(1)
		go func(e *pbactivity.ShowcaseProfileEntry) {
			defer wg.Done()
			defer func() { <-sem }()
			err := writeActivity(ctx, deps, e)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				written++
			case errors.Is(err, ErrNotExportable):
				deps.logf("skip %s: %v", e.ShowcaseId, err)
				skipped++
			default:
				if firstErr == nil {
					firstErr = err
					cancel()
				}
			}
		}(e)
	}
	wg.Wait()
	return written, skipped, firstErr
}

func writeActivity(ctx context.Context, deps Deps, entry *pbactivity.ShowcaseProfileEntry) error {
	sc, err := deps.Source.Showcase(ctx, entry.ShowcaseId)
	if err != nil {
		return fmt.Errorf("showcase %s: %w", entry.ShowcaseId, err)
	}
	if sc == nil {
		return fmt.Errorf("%w: no showcase document for %s", ErrNotExportable, entry.ShowcaseId)
	}
	now := deps.now()
	if sc.ExpiresAt != nil && sc.ExpiresAt.AsTime().Before(now) {
		return fmt.Errorf("%w: %s expired at %s", ErrNotExportable, entry.ShowcaseId, sc.ExpiresAt.AsTime().Format(time.RFC3339))
	}
	// Strava's calories over the MET estimate older runs stored (mutates entry, so the
	// index ExportAll writes next carries the same figure).
	ApplyPlatformCalories(sc, entry)
	b, err := BuildActivity(sc, entry, now)
	if err != nil {
		return fmt.Errorf("build %s: %w", entry.ShowcaseId, err)
	}
	if err := deps.Writer.Write(ctx, activityName(entry.ShowcaseId), b); err != nil {
		return fmt.Errorf("write %s: %w", entry.ShowcaseId, err)
	}
	deps.logf("wrote %s (%d bytes)", activityName(entry.ShowcaseId), len(b))
	return nil
}

func writeRoundup(ctx context.Context, deps Deps, r *pbactivity.ShowcaseRoundup) error {
	b, err := BuildRoundup(r, deps.now())
	if err != nil {
		return fmt.Errorf("build roundup %s: %w", r.PeriodKey, err)
	}
	if err := deps.Writer.Write(ctx, roundupName(r.PeriodKey), b); err != nil {
		return fmt.Errorf("write roundup %s: %w", r.PeriodKey, err)
	}
	deps.logf("wrote %s (%d bytes)", roundupName(r.PeriodKey), len(b))
	return nil
}

func writeIndex(ctx context.Context, deps Deps, profile *pbactivity.ShowcaseProfile, entries []*pbactivity.ShowcaseProfileEntry, roundups []*pbactivity.ShowcaseRoundup) error {
	existing := map[string]bool{}
	for _, dir := range []string{activityDir, roundupDir} {
		names, err := deps.Writer.List(ctx, dir)
		if err != nil {
			return fmt.Errorf("list %s: %w", dir, err)
		}
		for _, n := range names {
			existing[n] = true
		}
	}
	var listedEntries []*pbactivity.ShowcaseProfileEntry
	for _, e := range entries {
		if existing[activityName(e.ShowcaseId)] {
			listedEntries = append(listedEntries, e)
		}
	}
	var listedRoundups []*pbactivity.ShowcaseRoundup
	for _, r := range roundups {
		if existing[roundupName(r.PeriodKey)] {
			listedRoundups = append(listedRoundups, r)
		}
	}
	b, err := BuildIndex(profile, listedEntries, listedRoundups, deps.now())
	if err != nil {
		return fmt.Errorf("build index: %w", err)
	}
	if err := deps.Writer.Write(ctx, indexName, b); err != nil {
		return fmt.Errorf("write index: %w", err)
	}
	deps.logf("wrote %s: %d activities, %d roundups (%d bytes)", indexName, len(listedEntries), len(listedRoundups), len(b))
	return nil
}
