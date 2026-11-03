package siteexport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// StravaVisibility decides whether a Strava activity is visible to everyone, which is
// the only case a Strava link is exported. The showcase and pipeline data FitGlue
// stores carry no private/visibility flag, so it is fetched once from
// GET /activities/{id} and cached. It fails closed: anything unknown (no token,
// request error, rate limit, a response without an explicit visibility) is "not
// public" and the link is omitted.
//
// Rate limits: lookups pause once Strava's X-RateLimit-Usage reaches
// the limit minus a reserve (the box shares one app limit with ingest and uploads), on
// any 429 (until its Retry-After, else 15 minutes), and after MaxLookups requests per rolling
// 15-minute window; the service is long-lived, so all of these lift by themselves. Answers are cached in a file shared by the
// service and fitgluectl (DefaultVisibilityCachePath): "everyone" for 30 days,
// anything else for 7. An unknown result (an error) is never cached. A backfill over
// hundreds of activities therefore fills in over several runs, the entries the export
// reaches first (newest first) being linked first, and later runs skip what is cached.
type StravaVisibility struct {
	// Token returns an access token for the owner.
	Token func(ctx context.Context, uid string) (string, error)
	// BaseURL is Strava's API root (stravamap.APIBase).
	BaseURL string
	HTTP    *http.Client
	// CachePath persists the cache as JSON; memory only when empty.
	CachePath string
	// PublicTTL and OtherTTL bound how long an answer is trusted; 30 days and 7 when 0.
	PublicTTL, OtherTTL time.Duration
	// MaxLookups caps API requests per 15-minute window; 90 when 0.
	MaxLookups int
	// Reserve is how far below the 15-minute/daily limits to stop; 20 when 0.
	Reserve int
	Now     func() time.Time
	Logf    func(format string, args ...any)

	mu      sync.Mutex
	loaded  bool
	cache   map[string]visEntry
	lookups int
	// windowStart opens the current 15-minute lookup window (Strava's own window).
	windowStart  time.Time
	blockedUntil time.Time
}

type visEntry struct {
	Public bool      `json:"public"`
	At     time.Time `json:"at"`
}

var errVisibilityUnknown = errors.New("strava visibility unknown")

func (v *StravaVisibility) now() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

func (v *StravaVisibility) logf(f string, a ...any) {
	if v.Logf != nil {
		v.Logf(f, a...)
	}
}

// Public reports whether the Strava activity is visible to everyone. owner is only
// called on a cache miss. An error means unknown; callers must treat it as not public.
func (v *StravaVisibility) Public(ctx context.Context, stravaID string, owner func(context.Context) (string, error)) (bool, error) {
	if v == nil || !stravaIDRe.MatchString(stravaID) {
		return false, errVisibilityUnknown
	}
	v.mu.Lock()
	defer v.mu.Unlock() // serialises lookups, which also keeps requests under the rate limit
	v.load()
	if e, ok := v.cache[stravaID]; ok && v.now().Sub(e.At) < v.ttl(e.Public) {
		return e.Public, nil
	}
	max := v.MaxLookups
	if max == 0 {
		max = 90
	}
	now := v.now()
	if now.Sub(v.windowStart) >= 15*time.Minute {
		v.windowStart, v.lookups = now, 0
	}
	if now.Before(v.blockedUntil) || v.lookups >= max {
		return false, fmt.Errorf("%w: lookup budget spent", errVisibilityUnknown)
	}
	uid, err := owner(ctx)
	if err != nil || uid == "" {
		return false, fmt.Errorf("%w: owner: %v", errVisibilityUnknown, err)
	}
	tok, err := v.Token(ctx, uid)
	if err != nil {
		return false, fmt.Errorf("%w: token: %v", errVisibilityUnknown, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(v.BaseURL, "/")+"/activities/"+stravaID, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	hc := v.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	v.lookups++
	resp, err := hc.Do(req)
	if err != nil {
		return false, fmt.Errorf("%w: %v", errVisibilityUnknown, err)
	}
	defer func() { _ = resp.Body.Close() }()
	v.noteUsage(resp)
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		wait := 15 * time.Minute
		if n, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); err == nil && n > 0 {
			wait = time.Duration(n) * time.Second
		}
		v.blockedUntil = v.now().Add(wait)
		return false, fmt.Errorf("%w: rate limited", errVisibilityUnknown)
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusForbidden:
		// Gone, or not ours to read: nothing public to link.
		v.put(stravaID, false)
		return false, nil
	case resp.StatusCode != http.StatusOK:
		return false, fmt.Errorf("%w: status %d", errVisibilityUnknown, resp.StatusCode)
	}
	var a struct {
		Private    *bool   `json:"private"`
		Visibility *string `json:"visibility"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&a); err != nil {
		return false, fmt.Errorf("%w: decode: %v", errVisibilityUnknown, err)
	}
	if a.Private == nil && a.Visibility == nil {
		return false, fmt.Errorf("%w: response has neither private nor visibility", errVisibilityUnknown)
	}
	pub := a.Visibility != nil && *a.Visibility == "everyone" && (a.Private == nil || !*a.Private)
	v.put(stravaID, pub)
	return pub, nil
}

// noteUsage blocks further lookups when Strava reports the 15-minute or daily
// usage within Reserve of the limit ("15min,day" in both headers).
func (v *StravaVisibility) noteUsage(resp *http.Response) {
	reserve := v.Reserve
	if reserve == 0 {
		reserve = 20
	}
	use, lim := strings.Split(resp.Header.Get("X-RateLimit-Usage"), ","), strings.Split(resp.Header.Get("X-RateLimit-Limit"), ",")
	for i := 0; i < len(use) && i < len(lim); i++ {
		u, e1 := strconv.Atoi(strings.TrimSpace(use[i]))
		l, e2 := strconv.Atoi(strings.TrimSpace(lim[i]))
		if e1 != nil || e2 != nil || u < l-reserve {
			continue
		}
		// Index 0 is the 15-minute window, 1 the daily one (resets at UTC midnight).
		now := v.now().UTC()
		until := now.Add(15 * time.Minute)
		if i == 1 {
			until = time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
		}
		if until.After(v.blockedUntil) {
			v.blockedUntil = until
		}
		v.logf("strava rate limit near (%d/%d): visibility lookups paused until %s", u, l, until.Format(time.RFC3339))
	}
}

func (v *StravaVisibility) ttl(public bool) time.Duration {
	if public {
		if v.PublicTTL > 0 {
			return v.PublicTTL
		}
		return 30 * 24 * time.Hour
	}
	if v.OtherTTL > 0 {
		return v.OtherTTL
	}
	return 7 * 24 * time.Hour
}

func (v *StravaVisibility) put(id string, pub bool) {
	v.cache[id] = visEntry{Public: pub, At: v.now()}
	v.save()
}

func (v *StravaVisibility) load() {
	if v.loaded {
		return
	}
	v.loaded = true
	v.cache = map[string]visEntry{}
	if v.CachePath == "" {
		return
	}
	if b, err := os.ReadFile(v.CachePath); err == nil {
		_ = json.Unmarshal(b, &v.cache)
		if v.cache == nil {
			v.cache = map[string]visEntry{}
		}
	}
}

// save merges with what another process wrote since we loaded (newer entry wins) and
// replaces the file atomically, so the service and fitgluectl can share it.
func (v *StravaVisibility) save() {
	if v.CachePath == "" {
		return
	}
	var onDisk map[string]visEntry
	if b, err := os.ReadFile(v.CachePath); err == nil {
		_ = json.Unmarshal(b, &onDisk)
	}
	for k, e := range onDisk {
		if cur, ok := v.cache[k]; !ok || e.At.After(cur.At) {
			v.cache[k] = e
		}
	}
	b, err := json.Marshal(v.cache)
	if err == nil {
		if err = os.MkdirAll(filepath.Dir(v.CachePath), 0o755); err == nil {
			tmp := v.CachePath + "." + strconv.Itoa(os.Getpid()) + ".tmp"
			if err = os.WriteFile(tmp, b, 0o600); err == nil {
				err = os.Rename(tmp, v.CachePath)
			}
		}
	}
	if err != nil {
		v.logf("strava visibility cache not saved: %v", err)
	}
}

// DefaultVisibilityCachePath is the cache file the service and fitgluectl share (both
// run as the box user): STRAVA_VISIBILITY_CACHE, else <user cache dir>/fitglue/strava-visibility.json,
// else "" (memory only).
func DefaultVisibilityCachePath(getenv func(string) string) string {
	if p := getenv("STRAVA_VISIBILITY_CACHE"); p != "" {
		return p
	}
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, "fitglue", "strava-visibility.json")
	}
	return ""
}

// NewStravaVisibility is the production checker: tokens from the user's stored
// Strava connection (tokenFor), Strava's API root, and a shared cache file (DefaultVisibilityCachePath).
func NewStravaVisibility(baseURL, cachePath string, tokenFor func(ctx context.Context, uid string) (string, error), logf func(string, ...any)) *StravaVisibility {
	return &StravaVisibility{Token: tokenFor, BaseURL: baseURL, CachePath: cachePath, Logf: logf}
}
