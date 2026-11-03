package siteexport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
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
	EnvSignal   = "SITE_EXPORT_SIGNAL_FILE"

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
	// /hooks/fitness-updated (the kiosk's fitness panels refetch).
	HookURL string
	// SignalFile, when set (SITE_EXPORT_SIGNAL_FILE), gets one line appended
	// after every index refresh: "<unix seconds> fitglue <reasons>". On the box
	// it is ~/.jarvis/home/site-changed, which a systemd path unit watches to
	// rebuild james.ripixel.co.uk (jarvis docs/deploys.md, S6).
	SignalFile string
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
		Slug:       strings.TrimSpace(getenv(EnvSlug)),
		Bucket:     strings.TrimSpace(getenv(EnvBucket)),
		HookURL:    strings.TrimSpace(getenv(EnvHookURL)),
		SignalFile: strings.TrimSpace(getenv(EnvSignal)),
		Disabled:   isTruthy(getenv(EnvDisabled)),
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
	// SignalFile gets a line appended after each successful index refresh;
	// "" skips it.
	SignalFile string

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
// persisted, updated or removed, RoundupChanged after a roundup is saved and
// ProfileChanged after the public profile's settings change. Every call returns at
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
	signalFile    string
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
	reasons    []string    // what changed since the last good refresh, for the notify
	wg         sync.WaitGroup
}

// NewHook returns a Hook for cfg.
func NewHook(cfg HookConfig) *Hook {
	h := &Hook{
		deps:          cfg.Deps,
		slug:          cfg.Slug,
		logger:        cfg.Logger,
		hookURL:       cfg.HookURL,
		signalFile:    cfg.SignalFile,
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
	if h.deps.Logf == nil {
		h.deps.Logf = func(format string, args ...any) {
			h.logger.Info(context.Background(), fmt.Sprintf(format, args...))
		}
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

// ProfileChanged schedules an index refresh (the profile's name, bio, links
// and totals live in index.json). It returns immediately.
func (h *Hook) ProfileChanged(_ context.Context) {
	if h == nil {
		return
	}
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		h.addReason("profile")
		h.requestIndex()
	}()
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
			h.addReason(kind + " " + id)
		case errors.Is(err, ErrNotExportable):
			// Not in the exported profile, no showcase document, or expired:
			// nothing to write, but the index may need to drop it.
			h.logger.Info(ctx, "site export: "+kind+" not exportable, skipped", "id", id, "slug", h.slug, "error", err)
			h.addReason("removed " + kind + " " + id)
		default:
			h.logger.Warn(ctx, "site export: "+kind+" export failed", "id", id, "slug", h.slug, "error", err)
		}
		h.requestIndex()
	}()
}

// maxReasons caps the reasons carried by one notify (a backfill of hundreds
// of activities says "+N more" instead of listing them).
const maxReasons = 20

func (h *Hook) addReason(r string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, x := range h.reasons {
		if x == r {
			return
		}
	}
	h.reasons = append(h.reasons, r)
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
	reasons := h.reasons
	h.reasons = nil
	h.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), h.exportTimeout)
	started := time.Now()
	err := RefreshIndex(ctx, h.deps, h.slug)
	if err != nil {
		h.logger.Warn(ctx, "site export: index refresh failed", "slug", h.slug, "error", err)
		// Carry them to the next good refresh: the site has not seen them yet.
		h.mu.Lock()
		h.reasons = append(reasons, h.reasons...)
		h.mu.Unlock()
	} else {
		h.logger.Info(ctx, "site export: wrote index", "slug", h.slug, "took", time.Since(started).String())
		h.notify(reasons)
		h.signal(reasons)
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

// reasonText is the reasons as one line: "activity a, roundup w (+3 more)",
// or "index" when there are none (a refresh nothing named, e.g. a retry).
func reasonText(reasons []string) string {
	if len(reasons) == 0 {
		return "index"
	}
	shown := reasons
	more := ""
	if len(shown) > maxReasons {
		shown = shown[:maxReasons]
		more = fmt.Sprintf(" (+%d more)", len(reasons)-maxReasons)
	}
	return strings.Join(shown, ", ") + more
}

// signal appends one line to the signal file, the box's "public site data
// changed" queue (S6): a systemd path unit starts the debounced site rebuild
// while the file exists, and the rebuild consumes it. Best-effort: a failure
// is one Warn line, and the nightly rebuild is the safety net.
func (h *Hook) signal(reasons []string) {
	if h.signalFile == "" {
		return
	}
	line := fmt.Sprintf("%d fitglue %s\n", time.Now().Unix(), strings.ReplaceAll(reasonText(reasons), "\n", " "))
	f, err := os.OpenFile(h.signalFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		_, err = f.WriteString(line)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		h.logger.Warn(context.Background(), "site export: signal file write failed", "file", h.signalFile, "error", err)
		return
	}
	h.logger.Info(context.Background(), "site export: site rebuild signalled", "file", h.signalFile, "reasons", reasonText(reasons))
}

// notify POSTs to the hook URL (the box's gateway) so the kiosk's fitness
// panels refetch. Best-effort: a failure is one Warn line.
func (h *Hook) notify(reasons []string) {
	if h.hookURL == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), h.hookTimeout)
	defer cancel()
	body, _ := json.Marshal(map[string]any{"slug": h.slug, "what": "index", "reasons": reasonText(reasons)})
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
