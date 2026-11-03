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
// Rate limits: lookups stop for the process once Strava's X-RateLimit-Usage reaches
// the limit minus a reserve (the box shares one app limit with ingest and uploads), on
// any 429, and after MaxLookups requests; results are cached for TTL, optionally in a
// file so repeated runs make progress.
type StravaVisibility struct {
	// Token returns an access token for the owner.
	Token func(ctx context.Context, uid string) (string, error)
	// BaseURL is Strava's API root (stravamap.APIBase).
	BaseURL string
	HTTP    *http.Client
	// CachePath persists the cache as JSON; memory only when empty.
	CachePath string
	// TTL bounds how long an answer is trusted; 24h when 0.
	TTL time.Duration
	// MaxLookups caps API requests per process; 60 when 0.
	MaxLookups int
	// Reserve is how far below the 15-minute/daily limits to stop; 20 when 0.
	Reserve int
	Now     func() time.Time
	Logf    func(format string, args ...any)

	mu      sync.Mutex
	loaded  bool
	cache   map[string]visEntry
	lookups int
	blocked bool
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
	ttl := v.TTL
	if ttl == 0 {
		ttl = 24 * time.Hour
	}
	if e, ok := v.cache[stravaID]; ok && v.now().Sub(e.At) < ttl {
		return e.Public, nil
	}
	max := v.MaxLookups
	if max == 0 {
		max = 60
	}
	if v.blocked || v.lookups >= max {
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
		v.blocked = true
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
		if e1 == nil && e2 == nil && u >= l-reserve {
			v.blocked = true
			v.logf("strava rate limit near (%d/%d): visibility lookups stopped", u, l)
		}
	}
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

func (v *StravaVisibility) save() {
	if v.CachePath == "" {
		return
	}
	b, err := json.Marshal(v.cache)
	if err == nil {
		if err = os.MkdirAll(filepath.Dir(v.CachePath), 0o755); err == nil {
			err = os.WriteFile(v.CachePath, b, 0o600)
		}
	}
	if err != nil {
		v.logf("strava visibility cache not saved: %v", err)
	}
}

// NewStravaVisibility is the production checker: tokens from the user's stored
// Strava connection (tokenFor), Strava's API root, and a cache file under the user
// cache directory when one exists (the service passes "" and caches in memory).
func NewStravaVisibility(baseURL, cachePath string, tokenFor func(ctx context.Context, uid string) (string, error), logf func(string, ...any)) *StravaVisibility {
	return &StravaVisibility{Token: tokenFor, BaseURL: baseURL, CachePath: cachePath, Logf: logf}
}
