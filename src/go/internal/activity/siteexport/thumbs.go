package siteexport

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
)

// ThumbnailRenderer draws a route thumbnail from a (trimmed) track, or a neutral
// placeholder when track is nil. Injected so this package does not depend on the
// enricher that owns the SVG template.
type ThumbnailRenderer func(track [][]float64) []byte

// ThumbnailPut stores the SVG at an object path inside the showcase-assets bucket.
type ThumbnailPut func(ctx context.Context, object string, svg []byte) error

// ThumbsDeps is the wiring of RewriteThumbnails.
type ThumbsDeps struct {
	Source      Source
	Zones       ZoneSource
	Render      ThumbnailRenderer
	Put         ThumbnailPut
	Logf        func(format string, args ...any)
	Concurrency int
	Now         func() time.Time
}

// ThumbsResult counts what RewriteThumbnails did.
type ThumbsResult struct {
	Trimmed, Whole, Placeholder, Skipped int
}

// ThumbnailObject maps a route thumbnail URL (custom assets domain or raw
// storage.googleapis.com/<bucket>/...) to its object path in the bucket.
func ThumbnailObject(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", false
	}
	p := strings.TrimPrefix(u.Path, "/")
	if u.Host == "storage.googleapis.com" {
		i := strings.Index(p, "/")
		if i < 0 {
			return "", false
		}
		p = p[i+1:]
	}
	if p == "" || strings.Contains(p, "..") || !strings.HasSuffix(p, "route-thumbnail.svg") {
		return "", false
	}
	return p, true
}

// RewriteThumbnails re-renders every profile entry's route thumbnail from the trimmed
// track (same zones and whole rule as routePoints) and overwrites the same object, so
// every URL already published now shows the trimmed shape. An entry whose showcase can
// no longer be read gets the placeholder; without zones every non-whole entry does.
func RewriteThumbnails(ctx context.Context, d ThumbsDeps, slug string) (ThumbsResult, error) {
	var res ThumbsResult
	logf := func(f string, a ...any) {
		if d.Logf != nil {
			d.Logf(f, a...)
		}
	}
	now := time.Now
	if d.Now != nil {
		now = d.Now
	}
	profile, err := d.Source.Profile(ctx, slug)
	if err != nil {
		return res, fmt.Errorf("profile %q: %w", slug, err)
	}
	entries, err := d.Source.Entries(ctx, profile.UserId)
	if err != nil {
		return res, fmt.Errorf("entries: %w", err)
	}
	var zones *Zones
	if d.Zones == nil {
		logf("privacy zones: no source configured; non-whole thumbnails become placeholders")
	} else if zones, err = d.Zones(); err != nil {
		zones = nil
		logf("privacy zones unavailable (%v); non-whole thumbnails become placeholders", err)
	}
	conc := d.Concurrency
	if conc <= 0 {
		conc = 8
	}
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		firstErr error
		sem      = make(chan struct{}, conc)
	)
	for _, e := range entries {
		obj, ok := ThumbnailObject(e.RouteThumbnailUrl)
		if !ok {
			mu.Lock()
			res.Skipped++
			mu.Unlock()
			continue
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(id, obj string) {
			defer wg.Done()
			defer func() { <-sem }()
			var track [][]float64
			whole := false
			sc, err := d.Source.Showcase(ctx, id)
			if err == nil && sc != nil && (sc.ExpiresAt == nil || sc.ExpiresAt.AsTime().After(now())) {
				whole = IsWhole(sc)
				track = ThumbnailTrack(sc.ActivityData, zones, whole)
			}
			err2 := d.Put(ctx, obj, d.Render(track))
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err2 != nil:
				if firstErr == nil {
					firstErr = fmt.Errorf("put %s: %w", obj, err2)
				}
			case track == nil:
				res.Placeholder++
			case whole:
				res.Whole++
			default:
				res.Trimmed++
			}
		}(e.ShowcaseId, obj)
	}
	wg.Wait()
	if firstErr != nil {
		return res, firstErr
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return res, ctx.Err()
	}
	return res, nil
}
