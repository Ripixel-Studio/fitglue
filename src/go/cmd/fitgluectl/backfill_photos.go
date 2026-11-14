// nolint:proto-json -- encoding/json here is for the local state file and Strava's REST JSON, plain structs

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	firestore "cloud.google.com/go/firestore"
	gcs "cloud.google.com/go/storage"

	"github.com/fitglue/server/src/go/internal/activity"
	"github.com/fitglue/server/src/go/internal/pipeline/enricher/providers/photo_upload"
	"github.com/fitglue/server/src/go/internal/reconcile"
	stravamap "github.com/fitglue/server/src/go/pkg/domain/sourcemap/strava"
	infradb "github.com/fitglue/server/src/go/pkg/infrastructure/database"
	"github.com/fitglue/server/src/go/pkg/infrastructure/oauth"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
)

// `fitgluectl backfill-photos` gives showcased activities with no photos the
// photos he added on Strava (read only: GET /activities/{id}/photos; nothing is
// written to Strava). Each photo is copied into the showcase-assets bucket the
// way the photo-upload enricher stores one (max 1600 px, JPEG q85), in Strava's
// order, then set on the showcase and its profile entry; the activity is
// re-exported, index.json refreshed and the site rebuild signalled. The stored
// week/month/year roundups holding a changed activity get their photo mosaic
// re-collected (numbers and story untouched).
//
//	fitgluectl backfill-photos --since 2025-01-01 --until 2025-12-31          # dry run: Strava id + photo count per activity
//	fitgluectl backfill-photos --since 2025-01-01 --until 2025-12-31 --apply
//	fitgluectl backfill-photos --id some-showcase-id --apply
//
// Activities that already have photos are never touched. Strava's answers are
// kept in ~/.local/state/fitgluectl/backfill-photos.json for a week, so --apply
// after a dry run makes no second listing call and an interrupted run (Strava's
// daily limit) resumes where it stopped. Strava's limits are read from its
// response headers: the command waits out the 15-minute window and stops at
// the daily one.

const (
	stateFreshFor  = 7 * 24 * time.Hour
	stravaReserve  = 10 // requests left unused in each window (the site export's visibility lookups share it)
	maxRetries429  = 3
	photoCacheCtrl = "public, max-age=86400"
)

var errStravaDailyLimit = errors.New("strava daily rate limit reached; re-run after 00:00 UTC to resume")

type backfillPhotosFlags struct {
	export       exportSiteFlags
	since, until time.Time // until is exclusive (the day after --until)
	ids          []string
	apply        bool
	state        string
}

// photoSite is what the command reads and writes through (siteexport.ServiceSource
// joined to the activity service).
type photoSite interface {
	Profile(ctx context.Context, slug string) (*pbactivity.ShowcaseProfile, error)
	Entries(ctx context.Context, userID string) ([]*pbactivity.ShowcaseProfileEntry, error)
	Showcase(ctx context.Context, showcaseID string) (*pbactivity.ShowcasedActivity, error)
	StravaActivityID(ctx context.Context, sc *pbactivity.ShowcasedActivity) (string, error)
	SetShowcasePhotos(ctx context.Context, showcaseID string, entry *pbactivity.ShowcaseProfileEntry, urls []string) error
	RefreshRoundupPhotos(ctx context.Context, userID, slug, periodKey string) (bool, error)
}

// stravaPhoto is one photo Strava lists on an activity.
type stravaPhoto struct {
	UniqueID string `json:"uniqueId"`
	URL      string `json:"url"`
}

type photoLister interface {
	Photos(ctx context.Context, stravaID string) ([]stravaPhoto, error)
}

// stravaFinder finds his Strava activity for a session FitGlue has no Strava id
// for (a Hevy workout Hevy itself posted to Strava): the one starting within
// reconcile.MatchWindow of it with a similar duration, as reconcile pairs them.
type stravaFinder interface {
	FindByTime(ctx context.Context, start time.Time, dur time.Duration) (string, error)
}

// photoCopier downloads a Strava photo, resizes it and stores it, returning its public URL.
type photoCopier func(ctx context.Context, uid, stravaID string, p stravaPhoto) (string, error)

// photoState is one activity's last answer from Strava, and what was applied.
type photoState struct {
	StravaID  string        `json:"stravaId"`
	CheckedAt time.Time     `json:"checkedAt"`
	Photos    []stravaPhoto `json:"photos"`
	Applied   []string      `json:"applied,omitempty"`
}

type photoStateFile struct {
	path string
	m    map[string]*photoState
}

func loadPhotoState(path string) *photoStateFile {
	s := &photoStateFile{path: path, m: map[string]*photoState{}}
	if path != "" {
		if raw, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(raw, &s.m)
		}
	}
	return s
}

func (s *photoStateFile) save() error {
	if s.path == "" {
		return nil
	}
	raw, err := json.MarshalIndent(s.m, "", " ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func defaultPhotoState() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "fitgluectl", "backfill-photos.json")
}

func parseDay(name, v string) (time.Time, error) {
	t, err := time.Parse("2006-01-02", v)
	if err != nil {
		return time.Time{}, fmt.Errorf("--%s: want YYYY-MM-DD, got %q", name, v)
	}
	return t, nil
}

func parseBackfillPhotosFlags(args []string, stderr io.Writer) (*backfillPhotosFlags, error) {
	f := &backfillPhotosFlags{}
	fs := flag.NewFlagSet("backfill-photos", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var since, until, ids string
	fs.StringVar(&f.export.slug, "slug", "james", "showcase profile slug")
	fs.StringVar(&f.export.project, "project", defaultProject, "GCP project holding Firestore")
	fs.StringVar(&f.export.bucket, "bucket", "", "showcase-assets bucket (default <project>-showcase-assets)")
	fs.StringVar(&since, "since", "2025-01-01", "first activity date (YYYY-MM-DD)")
	fs.StringVar(&until, "until", "", "last activity date, inclusive (YYYY-MM-DD; default: no end)")
	fs.StringVar(&ids, "id", "", "only these activities (showcase ids, comma-separated); ignores --since/--until")
	fs.BoolVar(&f.apply, "apply", false, "copy the photos and save them (default: dry run)")
	fs.StringVar(&f.state, "state", defaultPhotoState(), "local state file (\"\" for none)")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	var err error
	if f.since, err = parseDay("since", since); err != nil {
		return nil, err
	}
	if until != "" {
		u, err := parseDay("until", until)
		if err != nil {
			return nil, err
		}
		f.until = u.AddDate(0, 0, 1)
		if !f.until.After(f.since) {
			return nil, errors.New("--until is before --since")
		}
	}
	for _, id := range strings.Split(ids, ",") {
		if id = strings.TrimSpace(id); id != "" {
			f.ids = append(f.ids, id)
		}
	}
	if f.export.bucket == "" {
		f.export.bucket = f.export.project + "-showcase-assets"
	}
	f.export.prefix = "site/" + f.export.slug
	f.export.dryRun = !f.apply
	f.export.concurrency = 4
	return f, nil
}

type backfillCounts struct {
	checked, noStrava, withPhotos, photosFound, copied, applied, skippedHas, failed int
	failures                                                                        []string
}

func (f *backfillPhotosFlags) wants(e *pbactivity.ShowcaseProfileEntry) bool {
	if len(f.ids) > 0 {
		for _, id := range f.ids {
			if id == e.ShowcaseId {
				return true
			}
		}
		return false
	}
	if e.StartTime == nil {
		return false
	}
	t := e.StartTime.AsTime()
	return !t.Before(f.since) && (f.until.IsZero() || t.Before(f.until))
}

func runBackfillPhotos(ctx context.Context, f *backfillPhotosFlags, site photoSite, strava photoLister, copyPhoto photoCopier, out io.Writer, now func() time.Time) error {
	prof, err := site.Profile(ctx, f.export.slug)
	if err != nil {
		return fmt.Errorf("profile %q: %w", f.export.slug, err)
	}
	uid := prof.GetUserId()
	entries, err := site.Entries(ctx, uid)
	if err != nil {
		return fmt.Errorf("entries: %w", err)
	}
	var todo []*pbactivity.ShowcaseProfileEntry
	for _, e := range entries {
		if f.wants(e) {
			todo = append(todo, e)
		}
	}
	sort.SliceStable(todo, func(i, j int) bool {
		return todo[i].GetStartTime().AsTime().Before(todo[j].GetStartTime().AsTime())
	})
	state := loadPhotoState(f.state)
	defer func() { _ = state.save() }()

	var c backfillCounts
	roundupKeys := map[string]bool{}
	fail := func(e *pbactivity.ShowcaseProfileEntry, why string) {
		c.failed++
		c.failures = append(c.failures, e.ShowcaseId+": "+why)
		fmt.Fprintf(out, "%s  %s  FAILED: %s\n", day(e), e.ShowcaseId, why)
	}
	var stopErr error
	for _, e := range todo {
		if len(e.PhotoUrls) > 0 {
			c.skippedHas++
			continue
		}
		c.checked++
		st := state.m[e.ShowcaseId]
		if st != nil && len(st.Applied) > 0 {
			// Applied by an earlier run; the entry above would normally show it.
			c.withPhotos++
			c.photosFound += len(st.Photos)
			fmt.Fprintf(out, "%s  %s  strava %s  %d photos (applied earlier)\n", day(e), e.ShowcaseId, st.StravaID, len(st.Photos))
			continue
		}
		if st == nil || now().Sub(st.CheckedAt) > stateFreshFor {
			sc, err := site.Showcase(ctx, e.ShowcaseId)
			if err != nil {
				fail(e, "read showcase: "+err.Error())
				continue
			}
			if sc == nil {
				fail(e, "showcase not found")
				continue
			}
			if len(sc.PhotoUrls) > 0 {
				c.checked--
				c.skippedHas++
				continue
			}
			sid, err := site.StravaActivityID(ctx, sc)
			if err != nil {
				fail(e, "strava id: "+err.Error())
				continue
			}
			if sid == "" && sc.GetSource() == pbactivity.ActivitySource_SOURCE_HEVY {
				if fd, ok := strava.(stravaFinder); ok {
					sid, err = fd.FindByTime(ctx, e.GetStartTime().AsTime(), time.Duration(e.DurationSeconds*float64(time.Second)))
					if errors.Is(err, errStravaDailyLimit) {
						stopErr = err
						break
					}
					if err != nil {
						fail(e, "strava match: "+err.Error())
						continue
					}
				}
			}
			if sid == "" {
				c.noStrava++
				fmt.Fprintf(out, "%s  %s  no Strava copy (%s)\n", day(e), e.ShowcaseId, strings.TrimPrefix(sc.GetSource().String(), "SOURCE_"))
				continue
			}
			photos, err := strava.Photos(ctx, sid)
			if errors.Is(err, errStravaDailyLimit) {
				stopErr = err
				break
			}
			if err != nil {
				fail(e, "strava photos: "+err.Error())
				continue
			}
			st = &photoState{StravaID: sid, CheckedAt: now(), Photos: photos}
			state.m[e.ShowcaseId] = st
			_ = state.save()
		}
		if len(st.Photos) == 0 {
			fmt.Fprintf(out, "%s  %s  strava %s  no photos\n", day(e), e.ShowcaseId, st.StravaID)
			continue
		}
		c.withPhotos++
		c.photosFound += len(st.Photos)
		fmt.Fprintf(out, "%s  %s  strava %s  %d photos  %q\n", day(e), e.ShowcaseId, st.StravaID, len(st.Photos), e.Title)
		if !f.apply {
			for _, k := range activity.RoundupKeysAt(e.GetStartTime().AsTime()) {
				roundupKeys[k] = true
			}
			continue
		}
		var urls []string
		var copyErr error
		for _, p := range st.Photos {
			u, err := copyPhoto(ctx, uid, st.StravaID, p)
			if err != nil {
				copyErr = fmt.Errorf("photo %s: %w", p.UniqueID, err)
				break
			}
			urls = append(urls, u)
		}
		if copyErr != nil {
			// Strava's CDN links can go stale: list again next run.
			st.CheckedAt = time.Time{}
			fail(e, "copy "+copyErr.Error())
			continue
		}
		c.copied += len(urls)
		err := site.SetShowcasePhotos(ctx, e.ShowcaseId, e, urls)
		if errors.Is(err, activity.ErrHasPhotos) {
			c.skippedHas++
			continue
		}
		if err != nil {
			fail(e, "save: "+err.Error())
			continue
		}
		st.Applied = urls
		_ = state.save()
		c.applied++
		for _, k := range activity.RoundupKeysAt(e.GetStartTime().AsTime()) {
			roundupKeys[k] = true
		}
	}

	keys := make([]string, 0, len(roundupKeys))
	for k := range roundupKeys {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	refreshed := 0
	if f.apply {
		for _, k := range keys {
			changed, err := site.RefreshRoundupPhotos(ctx, uid, f.export.slug, k)
			if err != nil {
				c.failures = append(c.failures, "roundup "+k+": "+err.Error())
				continue
			}
			if changed {
				refreshed++
			}
		}
	}

	fmt.Fprintf(out, "\n%d activities without photos checked (%d already had photos): %d have Strava photos (%d photos), %d no Strava copy, %d failed\n",
		c.checked, c.skippedHas, c.withPhotos, c.photosFound, c.noStrava, c.failed)
	if f.apply {
		fmt.Fprintf(out, "applied: %d activities, %d photos copied; roundup photo mosaics refreshed: %d of %d (stories untouched)\n",
			c.applied, c.copied, refreshed, len(keys))
	} else {
		fmt.Fprintf(out, "dry run: nothing written; --apply copies the photos and refreshes %d roundup mosaics\n", len(keys))
	}
	for _, fl := range c.failures {
		fmt.Fprintf(out, "failure: %s\n", fl)
	}
	return stopErr
}

func day(e *pbactivity.ShowcaseProfileEntry) string {
	return e.GetStartTime().AsTime().UTC().Format("2006-01-02")
}

// stravaPhotoClient lists an activity's photos, pacing itself on Strava's
// rate-limit headers.
type stravaPhotoClient struct {
	BaseURL      string
	Token        func(ctx context.Context) (string, error)
	HTTP         *http.Client
	Now          func() time.Time
	Sleep        func(time.Duration)
	Logf         func(string, ...any)
	blockedUntil time.Time
	daily        bool
}

// nextQuarter is the start of Strava's next 15-minute window (they begin on the
// quarter hour), plus a little slack.
func nextQuarter(t time.Time) time.Time {
	return t.Truncate(15 * time.Minute).Add(15*time.Minute + 10*time.Second)
}

func (c *stravaPhotoClient) wait() error {
	if c.daily {
		return errStravaDailyLimit
	}
	if d := c.blockedUntil.Sub(c.Now()); d > 0 {
		c.Logf("strava 15-minute limit: waiting %s", d.Round(time.Second))
		c.Sleep(d)
	}
	return nil
}

// noteUsage reads "15min,daily" usage against the limits, for both the overall
// and the read limit headers.
func (c *stravaPhotoClient) noteUsage(h http.Header) {
	for _, pre := range []string{"X-RateLimit-", "X-ReadRateLimit-"} {
		use, lim := strings.Split(h.Get(pre+"Usage"), ","), strings.Split(h.Get(pre+"Limit"), ",")
		for i := 0; i < len(use) && i < len(lim) && i < 2; i++ {
			u, e1 := strconv.Atoi(strings.TrimSpace(use[i]))
			l, e2 := strconv.Atoi(strings.TrimSpace(lim[i]))
			if e1 != nil || e2 != nil || u < l-stravaReserve {
				continue
			}
			if i == 1 {
				c.daily = true
			} else if q := nextQuarter(c.Now()); q.After(c.blockedUntil) {
				c.blockedUntil = q
			}
		}
	}
}

func (c *stravaPhotoClient) Photos(ctx context.Context, stravaID string) ([]stravaPhoto, error) {
	body, err := c.get(ctx, "/activities/"+stravaID+"/photos?size=2048&photo_sources=true")
	if err != nil {
		return nil, err
	}
	return parseStravaPhotos(body)
}

// get makes one paced GET against the Strava API and returns the body of a 200.
func (c *stravaPhotoClient) get(ctx context.Context, path string) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		if err := c.wait(); err != nil {
			return nil, err
		}
		tok, err := c.Token(ctx)
		if err != nil {
			return nil, fmt.Errorf("token: %w", err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.BaseURL, "/")+path, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := c.HTTP.Do(req)
		if err != nil {
			return nil, err
		}
		c.noteUsage(resp.Header)
		body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		_ = resp.Body.Close()
		if err != nil {
			return nil, err
		}
		switch {
		case resp.StatusCode == http.StatusTooManyRequests:
			if attempt >= maxRetries429 {
				return nil, errors.New("rate limited")
			}
			if q := nextQuarter(c.Now()); q.After(c.blockedUntil) {
				c.blockedUntil = q
			}
			continue
		case resp.StatusCode == http.StatusNotFound:
			return nil, errors.New("strava activity not found (404)")
		case resp.StatusCode != http.StatusOK:
			return nil, fmt.Errorf("status %d", resp.StatusCode)
		}
		return body, nil
	}
}

func (c *stravaPhotoClient) FindByTime(ctx context.Context, start time.Time, dur time.Duration) (string, error) {
	q := fmt.Sprintf("/athlete/activities?after=%d&before=%d&per_page=30",
		start.Add(-reconcile.MatchWindow-time.Second).Unix(), start.Add(reconcile.MatchWindow+time.Second).Unix())
	body, err := c.get(ctx, q)
	if err != nil {
		return "", err
	}
	var acts []struct {
		ID          int64     `json:"id"`
		StartDate   time.Time `json:"start_date"`
		ElapsedTime int64     `json:"elapsed_time"`
	}
	if err := json.Unmarshal(body, &acts); err != nil {
		return "", fmt.Errorf("decode activities: %w", err)
	}
	best, bestGap := int64(0), time.Duration(-1)
	for _, a := range acts {
		gap := a.StartDate.Sub(start).Abs()
		if gap > reconcile.MatchWindow {
			continue
		}
		if d := time.Duration(a.ElapsedTime) * time.Second; d > 0 && dur > 0 {
			diff := (d - dur).Abs()
			if diff > 10*time.Minute && float64(diff) > 0.25*float64(max(d, dur)) {
				continue
			}
		}
		if bestGap < 0 || gap < bestGap {
			best, bestGap = a.ID, gap
		}
	}
	if best == 0 {
		return "", nil
	}
	return strconv.FormatInt(best, 10), nil
}

// parseStravaPhotos keeps Strava's order and drops videos; each photo's URL is
// the largest size Strava returned.
func parseStravaPhotos(body []byte) ([]stravaPhoto, error) {
	var raw []struct {
		UniqueID string            `json:"unique_id"`
		URLs     map[string]string `json:"urls"`
		VideoURL string            `json:"video_url"`
		MediaTyp int               `json:"media_type"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decode photos: %w", err)
	}
	photos := []stravaPhoto{}
	for _, r := range raw {
		if r.VideoURL != "" || r.MediaTyp == 2 {
			continue
		}
		best, bestSize := "", -1
		for k, u := range r.URLs {
			n, err := strconv.Atoi(k)
			if err != nil {
				n = 0
			}
			if u != "" && n > bestSize {
				best, bestSize = u, n
			}
		}
		if best == "" || r.UniqueID == "" {
			continue
		}
		photos = append(photos, stravaPhoto{UniqueID: r.UniqueID, URL: best})
	}
	return photos, nil
}

// photoObject is where a Strava photo is stored: stable, so a re-run overwrites
// the same object at the same URL.
func photoObject(uid, stravaID, uniqueID string) string {
	safe := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, uniqueID)
	return fmt.Sprintf("activity_photos/%s/strava-%s/%s.jpg", uid, stravaID, safe)
}

// newPhotoCopier downloads from Strava's CDN, resizes like the photo-upload
// enricher and writes the JPEG with put.
func newPhotoCopier(hc *http.Client, bucket string, put func(ctx context.Context, obj string, data []byte) error) photoCopier {
	return func(ctx context.Context, uid, stravaID string, p stravaPhoto) (string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.URL, nil)
		if err != nil {
			return "", err
		}
		resp, err := hc.Do(req)
		if err != nil {
			return "", err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("download status %d", resp.StatusCode)
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		if err != nil {
			return "", err
		}
		jpg, err := photo_upload.ResizeForShowcase(data)
		if err != nil {
			return "", err
		}
		obj := photoObject(uid, stravaID, p.UniqueID)
		if err := put(ctx, obj, jpg); err != nil {
			return "", fmt.Errorf("upload: %w", err)
		}
		return "https://storage.googleapis.com/" + bucket + "/" + obj, nil
	}
}

// photoSiteAdapter joins the site source to the service's photo halves.
type photoSiteAdapter struct{ summaryAdapter }

func (a photoSiteAdapter) SetShowcasePhotos(ctx context.Context, id string, e *pbactivity.ShowcaseProfileEntry, urls []string) error {
	return a.Service.SetShowcasePhotos(ctx, id, e, urls)
}

func (a photoSiteAdapter) RefreshRoundupPhotos(ctx context.Context, uid, slug, key string) (bool, error) {
	return a.Service.RefreshRoundupPhotos(ctx, uid, slug, key)
}

func backfillPhotosCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	f, err := parseBackfillPhotosFlags(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	site, err := openLiveSite(ctx, &f.export, stderr)
	if err != nil {
		return err
	}
	defer site.close()

	fs, err := firestore.NewClient(ctx, f.export.project)
	if err != nil {
		return fmt.Errorf("firestore client: %w", err)
	}
	defer fs.Close()
	gc, err := gcs.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("gcs client: %w", err)
	}
	defer gc.Close()

	prof, err := site.source.Profile(ctx, f.export.slug)
	if err != nil {
		return fmt.Errorf("profile %q: %w", f.export.slug, err)
	}
	db := infradb.NewFirestoreAdapter(fs)
	ts := oauth.NewFirestoreTokenSourceFromDB(db, prof.GetUserId(), "strava")
	logf := func(format string, a ...any) { fmt.Fprintf(stderr, format+"\n", a...) }
	strava := &stravaPhotoClient{
		BaseURL: stravamap.APIBase,
		Token: func(ctx context.Context) (string, error) {
			tok, err := ts.Token(ctx)
			if err != nil {
				return "", err
			}
			return tok.AccessToken, nil
		},
		HTTP:  &http.Client{Timeout: 30 * time.Second},
		Now:   time.Now,
		Sleep: time.Sleep,
		Logf:  logf,
	}
	put := func(ctx context.Context, obj string, data []byte) error {
		wc := gc.Bucket(f.export.bucket).Object(obj).NewWriter(ctx)
		wc.ContentType = "image/jpeg"
		wc.CacheControl = photoCacheCtrl
		if _, err := wc.Write(data); err != nil {
			_ = wc.Close()
			return err
		}
		return wc.Close()
	}
	cp := newPhotoCopier(&http.Client{Timeout: 60 * time.Second}, f.export.bucket, put)
	return runBackfillPhotos(ctx, f, photoSiteAdapter{summaryAdapter{site.source}}, strava, cp, stdout, time.Now)
}
