package siteexport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/fitglue/server/src/go/internal/infra"
)

// Env var names read by ConfigFromEnv (S1.2 "export on change").
const (
	EnvSlug     = "SITE_EXPORT_SLUG"
	EnvBucket   = "SITE_EXPORT_BUCKET"
	EnvDisabled = "SITE_EXPORT_DISABLED"
	EnvHookURL  = "SITE_EXPORT_HOOK_URL"

	// DefaultSlug is the showcase profile exported when SITE_EXPORT_SLUG is
	// unset. FitGlue has one user.
	DefaultSlug = "james"
	// DefaultExportTimeout bounds one export (activity or roundup file, or an
	// index refresh) started by the hook.
	DefaultExportTimeout = 60 * time.Second
	// DefaultIndexDebounce is the coalescing window for index.json: however
	// many files change inside it, the index is rewritten once, after it.
	DefaultIndexDebounce = 10 * time.Second
	// DefaultHookTimeout bounds the POST to SITE_EXPORT_HOOK_URL.
	DefaultHookTimeout = 2 * time.Second
	// DefaultMaxInFlight bounds concurrent file exports started by the hook,
	// so a backfill of hundreds of activities queues rather than fans out.
	DefaultMaxInFlight = 4
)

// EnvConfig is the hook's configuration as read from the environment.
type EnvConfig struct {
	// Slug is the profile to export (SITE_EXPORT_SLUG, default "james").
	Slug string
	// Bucket is the destination bucket (SITE_EXPORT_BUCKET, default the
	// showcase-assets bucket the service already has).
	Bucket string
	// HookURL, when set (SITE_EXPORT_HOOK_URL), is POSTed to after every index
	// refresh. Unset on Cloud Run; the box sets it to the gateway's
	// /hooks/fitness-updated.
	HookURL string
	// Disabled is SITE_EXPORT_DISABLED=1 (or true/yes): no hook at all.
	Disabled bool
}

// Prefix is the site folder for the slug ("site/<slug>").
func (c EnvConfig) Prefix() string { return "site/" + c.Slug }

// ConfigFromEnv reads the SITE_EXPORT_* variables through getenv (os.Getenv
// in services, a map lookup in tests). defaultBucket is used when
// SITE_EXPORT_BUCKET is unset — pass the service's showcase-assets bucket.
func ConfigFromEnv(getenv func(string) string, defaultBucket string) EnvConfig {
	cfg := EnvConfig{
		Slug:     strings.TrimSpace(getenv(EnvSlug)),
		Bucket:   strings.TrimSpace(getenv(EnvBucket)),
		HookURL:  strings.TrimSpace(getenv(EnvHookURL)),
		Disabled: isTruthy(getenv(EnvDisabled)),
	}
	if cfg.Slug == "" {
		cfg.Slug = DefaultSlug
	}
	if cfg.Bucket == "" {
		cfg.Bucket = defaultBucket
	}
	if cfg.Bucket == "" {
		// Nothing to write to: treat as disabled rather than fail every export.
		cfg.Disabled = true
	}
	return cfg
}

func isTruthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// HookConfig builds a Hook.
type HookConfig struct {
	Slug string
	Deps Deps
	// Logger receives one line per export; infra's default when nil.
	Logger infra.Logger
	// HookURL is POSTed to after each successful index refresh; "" skips it.
	HookURL string

	// Zero values take the Default* constants above.
	ExportTimeout time.Duration
	IndexDebounce time.Duration
	HookTimeout   time.Duration
	MaxInFlight   int
	// HTTPClient is used for the hook POST; http.DefaultClient when nil.
	HTTPClient *http.Client
}

// Hook keeps the site folder current after the writes that change it (S1.2):
// the activity service calls ActivityChanged after a showcase profile entry is
// persisted and RoundupChanged after a roundup is saved. Every call returns at
// once; the export runs in the background with its own timeout, failures are
// logged at Warn and never reach the caller, and index.json refreshes are
// coalesced to at most one per IndexDebounce with a refresh always following
// the last change. A nil *Hook is a no-op, so call sites need no branch.
//
// `fitgluectl export-site --all` remains the repair path for anything the hook
// missed (a crashed instance, a timed-out write, a throttled background
// goroutine on Cloud Run).
type Hook struct {
	deps          Deps
	slug          string
	logger        infra.Logger
	hookURL       string
	exportTimeout time.Duration
	indexDebounce time.Duration
	hookTimeout   time.Duration
	httpClient    *http.Client
	sem           chan struct{}

	// afterFunc schedules the index refresh; time.AfterFunc outside tests.
	afterFunc func(time.Duration, func()) *time.Timer

	mu         sync.Mutex
	indexTimer *time.Timer // pending index refresh, nil when none is scheduled
	refreshing bool        // an index refresh is running
	dirty      bool        // changes arrived while refreshing: run again after
	wg         sync.WaitGroup
}

// NewHook returns a Hook for cfg.
func NewHook(cfg HookConfig) *Hook {
	h := &Hook{
		deps:          cfg.Deps,
		slug:          cfg.Slug,
		logger:        cfg.Logger,
		hookURL:       cfg.HookURL,
		exportTimeout: cfg.ExportTimeout,
		indexDebounce: cfg.IndexDebounce,
		hookTimeout:   cfg.HookTimeout,
		httpClient:    cfg.HTTPClient,
		afterFunc:     time.AfterFunc,
	}
	if h.slug == "" {
		h.slug = DefaultSlug
	}
	if h.logger == nil {
		h.logger = infra.NewLoggerWithComponent("siteexport")
	}
	if h.exportTimeout <= 0 {
		h.exportTimeout = DefaultExportTimeout
	}
	if h.indexDebounce <= 0 {
		h.indexDebounce = DefaultIndexDebounce
	}
	if h.hookTimeout <= 0 {
		h.hookTimeout = DefaultHookTimeout
	}
	if h.httpClient == nil {
		h.httpClient = http.DefaultClient
	}
	n := cfg.MaxInFlight
	if n <= 0 {
		n = DefaultMaxInFlight
	}
	h.sem = make(chan struct{}, n)
	return h
}

// Slug is the profile this hook exports.
func (h *Hook) Slug() string {
	if h == nil {
		return ""
	}
	return h.slug
}

// ActivityChanged exports activity/<showcaseID>.json in the background and
// schedules an index refresh. It returns immediately. The caller's ctx is not
// used: the export runs on its own context so it survives the request that
// triggered it.
func (h *Hook) ActivityChanged(_ context.Context, showcaseID string) {
	if h == nil || showcaseID == "" {
		return
	}
	h.spawn(func(ctx context.Context) error {
		return ExportActivity(ctx, h.deps, h.slug, showcaseID)
	}, "activity", showcaseID)
}

// RoundupChanged exports roundup/<periodKey>.json in the background and
// schedules an index refresh. Roundups for a slug other than the hook's are
// ignored: they are not on the site.
func (h *Hook) RoundupChanged(_ context.Context, slug, periodKey string) {
	if h == nil || periodKey == "" {
		return
	}
	if slug != "" && slug != h.slug {
		h.logger.Debug(context.Background(), "site export: roundup for another slug, skipped",
			"slug", slug, "period_key", periodKey, "export_slug", h.slug)
		return
	}
	h.spawn(func(ctx context.Context) error {
		return ExportRoundup(ctx, h.deps, h.slug, periodKey)
	}, "roundup", periodKey)
}

// Wait blocks until every export and index refresh started so far, including
// a scheduled-but-not-yet-run index refresh, has finished. For tests and for
// a graceful shutdown; services never call it on the request path.
func (h *Hook) Wait() {
	if h == nil {
		return
	}
	h.wg.Wait()
}

func (h *Hook) spawn(run func(ctx context.Context) error, kind, id string) {
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		h.sem <- struct{}{}
		defer func() { <-h.sem }()

		ctx, cancel := context.WithTimeout(context.Background(), h.exportTimeout)
		defer cancel()
		started := time.Now()
		err := run(ctx)
		switch {
		case err == nil:
			h.logger.Info(ctx, "site export: wrote "+kind, "id", id, "slug", h.slug, "took", time.Since(started).String())
		case errors.Is(err, ErrNotExportable):
			// Not in the exported profile, no showcase document, or expired:
			// nothing to write, but the index may need to drop it.
			h.logger.Info(ctx, "site export: "+kind+" not exportable, skipped", "id", id, "slug", h.slug, "error", err)
		default:
			h.logger.Warn(ctx, "site export: "+kind+" export failed", "id", id, "slug", h.slug, "error", err)
		}
		h.requestIndex()
	}()
}

// requestIndex asks for index.json to be rewritten. The first request after a
// quiet period starts the debounce timer; requests while the timer is pending
// are covered by that run (it reads the state at run time, after their file
// writes); requests during a run mark it dirty so another run follows, again
// one debounce window later. Thus at most one refresh per window, and always
// one after the last change.
func (h *Hook) requestIndex() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.refreshing {
		h.dirty = true
		return
	}
	if h.indexTimer != nil {
		return
	}
	h.scheduleIndexLocked()
}

func (h *Hook) scheduleIndexLocked() {
	h.wg.Add(1)
	h.indexTimer = h.afterFunc(h.indexDebounce, h.refreshIndex)
}

func (h *Hook) refreshIndex() {
	defer h.wg.Done()
	h.mu.Lock()
	h.indexTimer = nil
	h.refreshing = true
	h.dirty = false
	h.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), h.exportTimeout)
	started := time.Now()
	err := RefreshIndex(ctx, h.deps, h.slug)
	if err != nil {
		h.logger.Warn(ctx, "site export: index refresh failed", "slug", h.slug, "error", err)
	} else {
		h.logger.Info(ctx, "site export: wrote index", "slug", h.slug, "took", time.Since(started).String())
		h.notify()
	}
	cancel()

	h.mu.Lock()
	defer h.mu.Unlock()
	h.refreshing = false
	if h.dirty {
		h.dirty = false
		h.scheduleIndexLocked()
	}
}

// notify POSTs to the hook URL (the box's gateway, S3.2) so the site rebuild
// can start. Best-effort: a failure is one Warn line.
func (h *Hook) notify() {
	if h.hookURL == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), h.hookTimeout)
	defer cancel()
	body := []byte(fmt.Sprintf(`{"slug":%q,"what":"index"}`, h.slug))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.hookURL, bytes.NewReader(body))
	if err != nil {
		h.logger.Warn(ctx, "site export: hook request invalid", "url", h.hookURL, "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := h.httpClient.Do(req)
	if err != nil {
		h.logger.Warn(ctx, "site export: hook POST failed", "url", h.hookURL, "error", err)
		return
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		h.logger.Warn(ctx, "site export: hook POST rejected", "url", h.hookURL, "status", res.StatusCode)
		return
	}
	h.logger.Info(ctx, "site export: hook notified", "url", h.hookURL)
}
