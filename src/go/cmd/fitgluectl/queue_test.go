package main

import (
	"bytes"
	"context"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/fitglue/server/src/go/internal/infra"
	"github.com/fitglue/server/src/go/pkg/infrastructure/localqueue"
)

func TestQueueCommand_CountsDeadAndRetry(t *testing.T) {
	ctx := context.Background()
	q, err := localqueue.Open(filepath.Join(t.TempDir(), "fitglue.db"), infra.NewLoggerWithComponent("test"))
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()

	if _, err := q.Publish(ctx, "topic-roundup-trigger", []byte(`{"period_type":"week"}`), nil); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := runQueueCommand(ctx, q, nil, &out, &errb); err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`topic-roundup-trigger\s+1\s+0\s+0\s+0`).MatchString(out.String()) {
		t.Fatalf("counts:\n%s", out.String())
	}

	// Fail it once with a one-attempt policy: dead.
	p := localqueue.SubRoundupTrigger
	p.MaxAttempts = 1
	fail := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "nope", 500) })
	if ok, err := q.DeliverOne(ctx, localqueue.Subscription{Policy: p, Handler: fail}); !ok || err != nil {
		t.Fatalf("%v %v", ok, err)
	}
	out.Reset()
	if err := runQueueCommand(ctx, q, []string{"--dead"}, &out, &errb); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "HTTP 500: nope") {
		t.Fatalf("dead:\n%s", out.String())
	}
	out.Reset()
	if err := runQueueCommand(ctx, q, []string{"retry", "1"}, &out, &errb); err != nil {
		t.Fatal(err)
	}
	if err := runQueueCommand(ctx, q, []string{"retry", "99"}, &out, &errb); err == nil {
		t.Fatal("retry of a missing id succeeded")
	}
}
