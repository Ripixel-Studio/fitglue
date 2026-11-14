package activity

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"google.golang.org/protobuf/proto"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
)

// ErrHasPhotos is returned by SetShowcasePhotos for a showcase that already
// carries photos: a backfill never adds to or reorders what is there.
var ErrHasPhotos = errors.New("showcase already has photos")

// SetShowcasePhotos gives a showcase with no photos its photo URLs, on both the
// showcase document and its profile entry (the site's index reads the entry),
// then tells the site exporter. entry is the stored profile entry (nil when the
// activity is not on the public profile); it is written back whole, as
// SaveClassification does.
func (s *Service) SetShowcasePhotos(ctx context.Context, showcaseID string, entry *pbactivity.ShowcaseProfileEntry, urls []string) error {
	if len(urls) == 0 {
		return errors.New("no photo urls")
	}
	doc, owner, err := s.store.GetPublicShowcase(ctx, showcaseID)
	if err != nil {
		return fmt.Errorf("showcase %s: %w", showcaseID, err)
	}
	if doc == nil {
		return fmt.Errorf("showcase %s not found", showcaseID)
	}
	if len(doc.PhotoUrls) > 0 {
		return ErrHasPhotos
	}
	doc.PhotoUrls = slices.Clone(urls)
	if _, err := s.store.UpdateShowcase(ctx, owner, doc); err != nil {
		return fmt.Errorf("update showcase %s: %w", showcaseID, err)
	}
	if entry != nil && len(entry.PhotoUrls) == 0 {
		entry.PhotoUrls = slices.Clone(urls)
		if err := s.store.SetShowcaseProfileEntry(ctx, owner, entry); err != nil {
			return fmt.Errorf("update entry %s: %w", showcaseID, err)
		}
	}
	s.siteActivityChanged(ctx, showcaseID)
	return nil
}

// RefreshRoundupPhotos re-collects a stored roundup's photo mosaic from its
// period's entries (collectRoundupMedia, as the generator does) and saves it
// when it changed. Nothing else in the roundup is recomputed: numbers and story
// stay as they are. It reports whether the roundup was rewritten.
func (s *Service) RefreshRoundupPhotos(ctx context.Context, userID, slug, periodKey string) (bool, error) {
	r, err := s.store.GetRoundup(ctx, slug, periodKey)
	if err != nil {
		return false, fmt.Errorf("roundup %s: %w", periodKey, err)
	}
	if r == nil {
		return false, nil
	}
	_, start, end, err := parsePeriodKey(periodKey)
	if err != nil {
		return false, err
	}
	profile, err := s.store.GetShowcasePreferences(ctx, userID)
	if err != nil {
		return false, fmt.Errorf("load profile: %w", err)
	}
	entries, err := s.store.ListShowcaseEntriesInRange(ctx, userID, start, end)
	if err != nil {
		return false, fmt.Errorf("list entries: %w", err)
	}
	photos, _ := collectRoundupMedia(entries, profile.GetShowPhotoGallery())
	if samePhotos(r.Photos, photos) {
		return false, nil
	}
	r.Photos = photos
	if err := s.store.SetRoundup(ctx, r); err != nil {
		return false, fmt.Errorf("save roundup %s: %w", periodKey, err)
	}
	s.siteRoundupChanged(ctx, slug, periodKey)
	return true, nil
}

// RoundupKeysAt is the week, month and year roundup keys whose period holds t.
var RoundupKeysAt = periodKeysAt

func samePhotos(a, b []*pbactivity.RoundupPhoto) bool {
	return slices.EqualFunc(a, b, func(x, y *pbactivity.RoundupPhoto) bool { return proto.Equal(x, y) })
}

// RemoveShowcasePhotos drops the given URLs from a showcase's photos and from its
// profile entry's (keeping the order of the rest), then tells the site exporter.
// It reports how many URLs the showcase lost; 0 means nothing was written.
func (s *Service) RemoveShowcasePhotos(ctx context.Context, showcaseID string, entry *pbactivity.ShowcaseProfileEntry, drop []string) (int, error) {
	doc, owner, err := s.store.GetPublicShowcase(ctx, showcaseID)
	if err != nil {
		return 0, fmt.Errorf("showcase %s: %w", showcaseID, err)
	}
	if doc == nil {
		return 0, fmt.Errorf("showcase %s not found", showcaseID)
	}
	keep := func(urls []string) []string {
		return slices.DeleteFunc(slices.Clone(urls), func(u string) bool { return slices.Contains(drop, u) })
	}
	kept := keep(doc.PhotoUrls)
	removed := len(doc.PhotoUrls) - len(kept)
	entryKept := keep(entry.GetPhotoUrls())
	entryChanged := entry != nil && len(entryKept) != len(entry.PhotoUrls)
	if removed == 0 && !entryChanged {
		return 0, nil
	}
	if removed > 0 {
		doc.PhotoUrls = kept
		if _, err := s.store.UpdateShowcase(ctx, owner, doc); err != nil {
			return 0, fmt.Errorf("update showcase %s: %w", showcaseID, err)
		}
	}
	if entryChanged {
		entry.PhotoUrls = entryKept
		if err := s.store.SetShowcaseProfileEntry(ctx, owner, entry); err != nil {
			return removed, fmt.Errorf("update entry %s: %w", showcaseID, err)
		}
	}
	s.siteActivityChanged(ctx, showcaseID)
	return removed, nil
}
