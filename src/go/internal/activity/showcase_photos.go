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
