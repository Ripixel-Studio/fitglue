package siteexport

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	runID     = "colwick-parkrun-2026-10-17"
	weekKey   = "week-42-2026"
	testDebnc = 20 * time.Millisecond
)

func newTestHook(src Source, w Writer, opts ...func(*HookConfig)) *Hook {
	cfg := HookConfig{
		Slug:          "james",
		Deps:          testDeps(src, w),
		IndexDebounce: testDebnc,
		ExportTimeout: 5 * time.Second,
		HookTimeout:   time.Second,
	}
	for _, o := range opts {
		o(&cfg)
	}
	return NewHook(cfg)
}

func indexIDs(t *testing.T, w *memWriter) (activities, roundups []string) {
	t.Helper()
	w.mu.Lock()
	b, ok := w.files["index.json"]
	w.mu.Unlock()
	if !ok {
		t.Fatal("index.json was not written")
	}
	var idx struct {
		Activities []struct {
			ShowcaseID string `json:"showcaseId"`
		} `json:"activities"`
		Roundups []struct {
			Key string `json:"key"`
		} `json:"roundups"`
	}
	if err := json.Unmarshal(b, &idx); err != nil {
		t.Fatalf("index.json: %v", err)
	}
	for _, a := range idx.Activities {
		activities = append(activities, a.ShowcaseID)
	}
	for _, r := range idx.Roundups {
		roundups = append(roundups, r.Key)
	}
	return activities, roundups
}

func TestHook_ActivityChanged_WritesFileThenIndex(t *testing.T) {
	src, w := newFakeSource(), newMemWriter()
	h := newTestHook(src, w)

	h.ActivityChanged(context.Background(), runID)
	h.Wait()

	if w.writes("activity/"+runID+".json") != 1 {
		t.Fatalf("expected one activity write, order=%v", w.order)
	}
	if w.writes("index.json") != 1 {
		t.Fatalf("expected one index write, order=%v", w.order)
	}
	if w.order[0] != "activity/"+runID+".json" || w.order[1] != "index.json" {
		t.Fatalf("file must be written before the index, order=%v", w.order)
	}
	acts, _ := indexIDs(t, w)
	if len(acts) != 1 || acts[0] != runID {
		t.Fatalf("index should list only the exported activity, got %v", acts)
	}
}

func TestHook_RoundupChanged(t *testing.T) {
	t.Run("OtherSlugSkipped", func(t *testing.T) {
		src, w := newFakeSource(), newMemWriter()
		h := newTestHook(src, w)
		h.RoundupChanged(context.Background(), "someone-else", weekKey)
		h.Wait()
		if len(w.order) != 0 {
			t.Fatalf("expected no writes for another slug, got %v", w.order)
		}
	})
	t.Run("Exported", func(t *testing.T) {
		src, w := newFakeSource(), newMemWriter()
		h := newTestHook(src, w)
		h.RoundupChanged(context.Background(), "james", weekKey)
		h.Wait()
		if w.writes("roundup/"+weekKey+".json") != 1 || w.writes("index.json") != 1 {
			t.Fatalf("expected roundup then index, order=%v", w.order)
		}
		_, rs := indexIDs(t, w)
		if len(rs) != 1 || rs[0] != weekKey {
			t.Fatalf("index should list the roundup, got %v", rs)
		}
	})
	t.Run("EmptySlugMeansHookSlug", func(t *testing.T) {
		src, w := newFakeSource(), newMemWriter()
		h := newTestHook(src, w)
		h.RoundupChanged(context.Background(), "", weekKey)
		h.Wait()
		if w.writes("roundup/"+weekKey+".json") != 1 {
			t.Fatalf("expected roundup write, order=%v", w.order)
		}
	})
}

func TestHook_FailuresAreSwallowed(t *testing.T) {
	t.Run("ActivityWriteFails_IndexStillRefreshed", func(t *testing.T) {
		src, w := newFakeSource(), newMemWriter()
		w.failing = "activity/"
		h := newTestHook(src, w)
		h.ActivityChanged(context.Background(), runID) // must not panic or block
		h.Wait()
		if w.writes("activity/"+runID+".json") != 0 {
			t.Fatal("failed write must not be recorded")
		}
		if w.writes("index.json") != 1 {
			t.Fatalf("index refresh should still run, order=%v", w.order)
		}
		acts, _ := indexIDs(t, w)
		if len(acts) != 0 {
			t.Fatalf("index must not list an activity whose file is missing, got %v", acts)
		}
	})
	t.Run("IndexWriteFails", func(t *testing.T) {
		src, w := newFakeSource(), newMemWriter()
		w.failing = "index.json"
		posts := int32(0)
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { atomic.AddInt32(&posts, 1) }))
		defer srv.Close()
		h := newTestHook(src, w, func(c *HookConfig) { c.HookURL = srv.URL })
		h.ActivityChanged(context.Background(), runID)
		h.Wait()
		if w.writes("activity/"+runID+".json") != 1 {
			t.Fatalf("activity should be written, order=%v", w.order)
		}
		if atomic.LoadInt32(&posts) != 0 {
			t.Fatal("hook must not be notified when the index refresh failed")
		}
	})
	t.Run("SourceFails", func(t *testing.T) {
		src, w := newFakeSource(), newMemWriter()
		src.errs["profile"] = io.ErrUnexpectedEOF
		h := newTestHook(src, w)
		h.ActivityChanged(context.Background(), runID)
		h.RoundupChanged(context.Background(), "james", weekKey)
		h.Wait()
		if w.writes("index.json") != 0 || w.writes("activity/"+runID+".json") != 0 {
			t.Fatalf("nothing should be written when the source fails, order=%v", w.order)
		}
	})
	t.Run("NotExportable_IndexRefreshed", func(t *testing.T) {
		// A removed showcase: ExportActivity reports ErrNotExportable, and the
		// index is refreshed so it drops out of the listing.
		src, w := newFakeSource(), newMemWriter()
		h := newTestHook(src, w)
		h.ActivityChanged(context.Background(), "gone-2026-01-01")
		h.Wait()
		if len(w.order) != 1 || w.order[0] != "index.json" {
			t.Fatalf("expected only an index refresh, order=%v", w.order)
		}
	})
}

func TestHook_DebounceCoalescesBurst(t *testing.T) {
	const debounce = 200 * time.Millisecond
	src, w := newFakeSource(), newMemWriter()
	h := newTestHook(src, w, func(c *HookConfig) { c.IndexDebounce = debounce })

	const n = 60
	started := time.Now()
	for i := 0; i < n; i++ {
		id := runID
		if i%2 == 1 {
			id = "push-day-2026-10-13"
		}
		h.ActivityChanged(context.Background(), id)
	}
	h.Wait()
	elapsed := time.Since(started)

	if got := w.writes("activity/"+runID+".json") + w.writes("activity/push-day-2026-10-13.json"); got != n {
		t.Fatalf("every activity change must be exported: got %d writes, want %d", got, n)
	}
	// At most one index write per debounce window that elapsed (plus the
	// trailing one), and far fewer than one per change.
	got := w.writes("index.json")
	maxWindows := int(elapsed/debounce) + 2
	if got < 1 || got > maxWindows || got > n/10 {
		t.Fatalf("burst of %d changes over %s should coalesce to <= %d index writes, got %d", n, elapsed, maxWindows, got)
	}
	acts, _ := indexIDs(t, w)
	if len(acts) != 2 {
		t.Fatalf("index should list both activities, got %v", acts)
	}
}

// blockingWriter holds the first index write until released, so a test can
// inject a change while a refresh is in flight.
type blockingWriter struct {
	*memWriter
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingWriter) Write(ctx context.Context, name string, data []byte) error {
	if name == "index.json" {
		b.once.Do(func() {
			close(b.started)
			<-b.release
		})
	}
	return b.memWriter.Write(ctx, name, data)
}

func TestHook_ChangeDuringRefreshRunsAgain(t *testing.T) {
	src := newFakeSource()
	w := &blockingWriter{memWriter: newMemWriter(), started: make(chan struct{}), release: make(chan struct{})}
	h := newTestHook(src, w)

	h.ActivityChanged(context.Background(), runID)
	select {
	case <-w.started:
	case <-time.After(5 * time.Second):
		t.Fatal("index refresh did not start")
	}
	// The refresh is mid-write: this change must not be lost.
	h.ActivityChanged(context.Background(), "push-day-2026-10-13")
	// Give the second export time to finish and mark the refresh dirty.
	deadline := time.Now().Add(5 * time.Second)
	for w.writes("activity/push-day-2026-10-13.json") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("second activity was not exported")
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(w.release)
	h.Wait()

	if got := w.writes("index.json"); got != 2 {
		t.Fatalf("a change during a refresh must schedule another refresh: got %d index writes, order=%v", got, w.order)
	}
	acts, _ := indexIDs(t, w.memWriter)
	if len(acts) != 2 {
		t.Fatalf("final index should list both activities, got %v", acts)
	}
}

func TestHook_NotifiesHookURL(t *testing.T) {
	t.Run("Posted", func(t *testing.T) {
		var mu sync.Mutex
		var bodies []string
		var ctypes []string
		srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				rw.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			bodies = append(bodies, string(b))
			ctypes = append(ctypes, r.Header.Get("Content-Type"))
			mu.Unlock()
			rw.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		src, w := newFakeSource(), newMemWriter()
		h := newTestHook(src, w, func(c *HookConfig) { c.HookURL = srv.URL + "/hooks/fitness-updated" })
		h.ActivityChanged(context.Background(), runID)
		h.ActivityChanged(context.Background(), "push-day-2026-10-13")
		h.Wait()

		mu.Lock()
		defer mu.Unlock()
		if len(bodies) != w.writes("index.json") {
			t.Fatalf("one POST per index refresh: %d posts, %d index writes", len(bodies), w.writes("index.json"))
		}
		if len(bodies) == 0 {
			t.Fatal("expected at least one POST")
		}
		var payload map[string]string
		if err := json.Unmarshal([]byte(bodies[0]), &payload); err != nil || payload["slug"] != "james" {
			t.Fatalf("body should be JSON with the slug, got %q (%v)", bodies[0], err)
		}
		if ctypes[0] != "application/json" {
			t.Fatalf("content-type %q", ctypes[0])
		}
	})
	t.Run("UnsetSkipsPost", func(t *testing.T) {
		src, w := newFakeSource(), newMemWriter()
		called := int32(0)
		h := newTestHook(src, w, func(c *HookConfig) {
			c.HTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				atomic.AddInt32(&called, 1)
				return nil, io.ErrClosedPipe
			})}
		})
		h.ActivityChanged(context.Background(), runID)
		h.Wait()
		if atomic.LoadInt32(&called) != 0 {
			t.Fatal("no hook URL: no request should be made")
		}
	})
	t.Run("UnreachableAndRejectedAreSwallowed", func(t *testing.T) {
		src, w := newFakeSource(), newMemWriter()
		h := newTestHook(src, w, func(c *HookConfig) { c.HookURL = "http://127.0.0.1:1/hooks/fitness-updated" })
		h.ActivityChanged(context.Background(), runID)
		h.Wait()
		if w.writes("index.json") != 1 {
			t.Fatal("index still written when the hook is unreachable")
		}

		srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) { rw.WriteHeader(http.StatusForbidden) }))
		defer srv.Close()
		h2 := newTestHook(src, newMemWriter(), func(c *HookConfig) { c.HookURL = srv.URL })
		h2.RoundupChanged(context.Background(), "james", weekKey)
		h2.Wait()
	})
	t.Run("TimesOut", func(t *testing.T) {
		// The handler never answers until the test lets it: the client must
		// give up on its own. (httptest's Close waits for handlers, so the
		// release happens before the deferred Close.)
		release := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			<-release
		}))
		defer srv.Close()
		defer close(release)
		src, w := newFakeSource(), newMemWriter()
		h := newTestHook(src, w, func(c *HookConfig) {
			c.HookURL = srv.URL
			c.HookTimeout = 30 * time.Millisecond
		})
		start := time.Now()
		h.ActivityChanged(context.Background(), runID)
		h.Wait()
		if time.Since(start) > 3*time.Second {
			t.Fatal("hook POST must give up at its timeout")
		}
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHook_NilAndEmptyAreNoOps(t *testing.T) {
	var h *Hook
	h.ActivityChanged(context.Background(), runID)
	h.RoundupChanged(context.Background(), "james", weekKey)
	h.Wait()
	if h.Slug() != "" {
		t.Fatal("nil hook has no slug")
	}

	src, w := newFakeSource(), newMemWriter()
	live := newTestHook(src, w)
	live.ActivityChanged(context.Background(), "")
	live.RoundupChanged(context.Background(), "james", "")
	live.Wait()
	if len(w.order) != 0 {
		t.Fatalf("empty ids must not export, order=%v", w.order)
	}
	if live.Slug() != "james" {
		t.Fatalf("slug %q", live.Slug())
	}
}

func TestNewHook_Defaults(t *testing.T) {
	h := NewHook(HookConfig{Deps: testDeps(newFakeSource(), newMemWriter())})
	if h.slug != DefaultSlug || h.exportTimeout != DefaultExportTimeout || h.indexDebounce != DefaultIndexDebounce ||
		h.hookTimeout != DefaultHookTimeout || cap(h.sem) != DefaultMaxInFlight || h.logger == nil || h.httpClient == nil {
		t.Fatalf("defaults not applied: %+v", h)
	}
}

func TestConfigFromEnv(t *testing.T) {
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	t.Run("Defaults", func(t *testing.T) {
		c := ConfigFromEnv(env(nil), "fitglue-server-prod-showcase-assets")
		if c.Slug != "james" || c.Bucket != "fitglue-server-prod-showcase-assets" || c.HookURL != "" || c.Disabled {
			t.Fatalf("%+v", c)
		}
		if c.Prefix() != "site/james" {
			t.Fatalf("prefix %q", c.Prefix())
		}
	})
	t.Run("Overrides", func(t *testing.T) {
		c := ConfigFromEnv(env(map[string]string{
			EnvSlug:    " other ",
			EnvBucket:  "my-bucket",
			EnvHookURL: "http://127.0.0.1:8790/hooks/fitness-updated",
		}), "default-bucket")
		if c.Slug != "other" || c.Bucket != "my-bucket" || c.HookURL != "http://127.0.0.1:8790/hooks/fitness-updated" || c.Disabled {
			t.Fatalf("%+v", c)
		}
	})
	t.Run("Disabled", func(t *testing.T) {
		for _, v := range []string{"1", "true", "TRUE", "yes", "on"} {
			if c := ConfigFromEnv(env(map[string]string{EnvDisabled: v}), "b"); !c.Disabled {
				t.Fatalf("%q should disable", v)
			}
		}
		for _, v := range []string{"", "0", "false", "no"} {
			if c := ConfigFromEnv(env(map[string]string{EnvDisabled: v}), "b"); c.Disabled {
				t.Fatalf("%q should not disable", v)
			}
		}
	})
	t.Run("NoBucketDisables", func(t *testing.T) {
		if c := ConfigFromEnv(env(nil), ""); !c.Disabled {
			t.Fatal("no bucket anywhere: must be disabled")
		}
	})
}
