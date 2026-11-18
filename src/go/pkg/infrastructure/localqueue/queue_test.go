package localqueue

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/fitglue/server/src/go/internal/infra"
)

func openTest(t *testing.T) (*Queue, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sub", "fitglue.db")
	q, err := Open(path, infra.NewLoggerWithComponent("test"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { q.Close() })
	return q, path
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func sub(p Policy, h http.HandlerFunc) Subscription { return Subscription{Policy: p, Handler: h} }

func TestPublishDeliversPubSubEnvelopeAndAcks(t *testing.T) {
	q, _ := openTest(t)
	ctx := context.Background()
	id, err := q.Publish(ctx, "topic-roundup-trigger", []byte(`{"period_type":"week"}`), map[string]string{"k": "v"})
	if err != nil || id == "" {
		t.Fatalf("publish: %q %v", id, err)
	}

	var got struct {
		Message struct {
			Data        []byte            `json:"data"`
			Attributes  map[string]string `json:"attributes"`
			MessageID   string            `json:"messageId"`
			PublishTime string            `json:"publishTime"`
		} `json:"message"`
		Subscription string `json:"subscription"`
	}
	s := sub(SubRoundupTrigger, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content-type %q", r.Header.Get("Content-Type"))
		}
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	ok, err := q.DeliverOne(ctx, s)
	if !ok || err != nil {
		t.Fatalf("deliver: %v %v", ok, err)
	}
	if string(got.Message.Data) != `{"period_type":"week"}` || got.Message.Attributes["k"] != "v" || got.Message.MessageID != id {
		t.Fatalf("envelope: %+v", got)
	}
	if _, err := time.Parse(time.RFC3339Nano, got.Message.PublishTime); err != nil {
		t.Fatalf("publishTime %q: %v", got.Message.PublishTime, err)
	}
	if ok, _ := q.DeliverOne(ctx, s); ok {
		t.Fatal("acked message delivered again")
	}
}

func TestCloudEventRoundTrip(t *testing.T) {
	q, _ := openTest(t)
	ctx := context.Background()
	e := cloudevents.NewEvent()
	e.SetID("e1")
	e.SetType("com.fitglue.activity.enriched")
	e.SetSource("/enricher")
	e.SetExtension("origin", "lag-queue")
	_ = e.SetData(cloudevents.ApplicationJSON, map[string]string{"user_id": "u"})
	if _, err := q.PublishCloudEvent(ctx, "topic-enrichment-lag", e); err != nil {
		t.Fatal(err)
	}
	var back cloudevents.Event
	_, err := q.DeliverOne(ctx, sub(SubEnrichmentLag, func(w http.ResponseWriter, r *http.Request) {
		var env pushEnvelope
		_ = json.NewDecoder(r.Body).Decode(&env)
		if err := json.Unmarshal(env.Message.Data, &back); err != nil {
			t.Fatal(err)
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	if back.Type() != e.Type() || back.Extensions()["origin"] != "lag-queue" {
		t.Fatalf("event: %v", back)
	}
}

func TestNackBacksOffThenDeadLetters(t *testing.T) {
	q, _ := openTest(t)
	clk := &fakeClock{t: time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)}
	q.now = clk.now
	ctx := context.Background()
	if _, err := q.Publish(ctx, "topic-pipeline-activity", []byte(`{}`), nil); err != nil {
		t.Fatal(err)
	}
	calls := 0
	s := sub(SubPipelineRun, func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	for attempt := 1; attempt <= SubPipelineRun.MaxAttempts; attempt++ {
		if ok, err := q.DeliverOne(ctx, s); !ok || err != nil {
			t.Fatalf("attempt %d: %v %v", attempt, ok, err)
		}
		// Not due again until the backoff has passed.
		if ok, _ := q.DeliverOne(ctx, s); ok {
			t.Fatalf("attempt %d redelivered before backoff", attempt)
		}
		clk.t = clk.t.Add(SubPipelineRun.Backoff(attempt))
	}
	if calls != 5 {
		t.Fatalf("calls = %d, want 5", calls)
	}
	if ok, _ := q.DeliverOne(ctx, s); ok {
		t.Fatal("dead message delivered")
	}
	dead, err := q.Dead(ctx, 10)
	if err != nil || len(dead) != 1 || dead[0].Attempts != 5 || dead[0].LastError == "" {
		t.Fatalf("dead: %+v %v", dead, err)
	}
	if err := q.Retry(ctx, dead[0].ID); err != nil {
		t.Fatal(err)
	}
	if ok, _ := q.DeliverOne(ctx, s); !ok {
		t.Fatal("retried message not delivered")
	}
}

func TestBackoffDoublesAndCaps(t *testing.T) {
	p := SubPipelineRun // 30s..600s
	want := []time.Duration{30 * time.Second, 60 * time.Second, 120 * time.Second, 240 * time.Second, 480 * time.Second, 600 * time.Second, 600 * time.Second}
	for i, w := range want {
		if got := p.Backoff(i + 1); got != w {
			t.Errorf("Backoff(%d) = %s, want %s", i+1, got, w)
		}
	}
}

func TestLeaseHidesInFlightAndReleaseRedelivers(t *testing.T) {
	q, _ := openTest(t)
	ctx := context.Background()
	_, _ = q.Publish(ctx, "topic-destination-upload", []byte(`{}`), nil)
	m, err := q.claim(ctx, "topic-destination-upload", time.Minute)
	if err != nil || m == nil {
		t.Fatalf("claim: %v %v", m, err)
	}
	if m2, _ := q.claim(ctx, "topic-destination-upload", time.Minute); m2 != nil {
		t.Fatal("leased message claimed twice")
	}
	// A restart: the new process releases its topics' leases.
	if n, err := q.releaseLeases(ctx, []string{"topic-destination-upload"}); err != nil || n != 1 {
		t.Fatalf("release: %d %v", n, err)
	}
	if m3, _ := q.claim(ctx, "topic-destination-upload", time.Minute); m3 == nil || m3.attempts != 2 {
		t.Fatalf("redelivery: %+v", m3)
	}
}

func TestPanicIsANack(t *testing.T) {
	q, _ := openTest(t)
	ctx := context.Background()
	_, _ = q.Publish(ctx, "topic-raw-activity", []byte(`{}`), nil)
	if ok, err := q.DeliverOne(ctx, sub(SubPipelineRaw, func(http.ResponseWriter, *http.Request) { panic("x") })); !ok || err != nil {
		t.Fatalf("%v %v", ok, err)
	}
	st, _ := q.Stats(ctx)
	for _, s := range st {
		if s.Topic == "topic-raw-activity" && s.Waiting != 1 {
			t.Fatalf("stats: %+v", s)
		}
	}
}

func TestUnknownTopicIsDropped(t *testing.T) {
	q, _ := openTest(t)
	id, err := q.Publish(context.Background(), "topic-fitbit-updates", []byte(`{}`), nil)
	if err != nil || id != "" {
		t.Fatalf("%q %v", id, err)
	}
	st, _ := q.Stats(context.Background())
	for _, s := range st {
		if s.Topic == "topic-fitbit-updates" {
			t.Fatalf("dropped topic stored: %+v", s)
		}
	}
}

func TestRunDeliversEachMessageOnceAcrossWorkers(t *testing.T) {
	q, _ := openTest(t)
	q.PollInterval = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	seen := map[string]int{}
	var total atomic.Int32
	p := SubDestinationUpload
	p.Workers = 4
	if err := q.Run(ctx, []Subscription{sub(p, func(w http.ResponseWriter, r *http.Request) {
		var env pushEnvelope
		_ = json.NewDecoder(r.Body).Decode(&env)
		mu.Lock()
		seen[string(env.Message.Data)]++
		mu.Unlock()
		total.Add(1)
	})}); err != nil {
		t.Fatal(err)
	}
	const n = 60
	for i := 0; i < n; i++ {
		if _, err := q.Publish(ctx, p.Topic, []byte(strconv.Itoa(i)), nil); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(10 * time.Second)
	for total.Load() < n && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != n || int(total.Load()) != n {
		t.Fatalf("delivered %d distinct, %d total; want %d", len(seen), total.Load(), n)
	}
}

// TestCrossProcessPublish runs a second process that publishes into the same
// file while this one consumes: the box's api-webhook → pipeline path.
func TestCrossProcessPublish(t *testing.T) {
	if os.Getenv("LOCALQUEUE_CHILD_DB") != "" {
		q, err := Open(os.Getenv("LOCALQUEUE_CHILD_DB"), infra.NewLoggerWithComponent("child"))
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 50; i++ {
			if _, err := q.Publish(context.Background(), "topic-raw-activity", []byte(strconv.Itoa(i)), nil); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	if testing.Short() {
		t.Skip("spawns child processes")
	}
	q, path := openTest(t)
	q.PollInterval = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var total atomic.Int32
	p := SubPipelineRaw
	p.Workers = 3
	_ = q.Run(ctx, []Subscription{sub(p, func(http.ResponseWriter, *http.Request) { total.Add(1) })})

	var wg sync.WaitGroup
	for c := 0; c < 3; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.Command(os.Args[0], "-test.run=^TestCrossProcessPublish$")
			cmd.Env = append(os.Environ(), "LOCALQUEUE_CHILD_DB="+path)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("child: %v\n%s", err, out)
			}
		}()
	}
	wg.Wait()
	deadline := time.Now().Add(10 * time.Second)
	for total.Load() < 150 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if total.Load() != 150 {
		t.Fatalf("delivered %d, want 150", total.Load())
	}
}
