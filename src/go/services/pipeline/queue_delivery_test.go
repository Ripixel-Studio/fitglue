package main

import (
	"context"
	"path/filepath"
	"testing"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/fitglue/server/src/go/internal/infra"
	"github.com/fitglue/server/src/go/pkg/infrastructure/localqueue"
	infrapubsub "github.com/fitglue/server/src/go/pkg/infrastructure/pubsub"
)

// The on-box queue must hand handlePubSubPush the same CloudEvent Pub/Sub did.
func TestQueueDeliversCloudEventToPushHandler(t *testing.T) {
	ctx := context.Background()
	logger := infra.NewLoggerWithComponent("test")
	q, err := localqueue.Open(filepath.Join(t.TempDir(), "fitglue.db"), logger)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()

	ce, err := infrapubsub.NewCloudEvent("/webhook", "com.fitglue.activity.raw", map[string]string{"user_id": "u1"})
	if err != nil {
		t.Fatal(err)
	}
	ce.SetID("evt-1")
	ce.SetExtension("origin", "lag-queue")
	if _, err := q.PublishCloudEvent(ctx, "topic-raw-activity", ce); err != nil {
		t.Fatal(err)
	}

	var got cloudevents.Event
	h := handlePubSubPush(logger, func(_ context.Context, e cloudevents.Event) error { got = e; return nil })
	if ok, err := q.DeliverOne(ctx, localqueue.Subscription{Policy: localqueue.SubPipelineRaw, Handler: h}); !ok || err != nil {
		t.Fatalf("deliver: %v %v", ok, err)
	}
	if got.ID() != ce.ID() || got.Type() != ce.Type() || got.Extensions()["origin"] != "lag-queue" {
		t.Fatalf("event = %v", got)
	}
	if string(got.Data()) != `{"user_id":"u1"}` {
		t.Fatalf("data = %s", got.Data())
	}
	st, _ := q.Stats(ctx)
	if st[0].Topic != "topic-raw-activity" || st[0].Ready+st[0].Waiting+st[0].Leased != 0 {
		t.Fatalf("not acked: %+v", st[0])
	}
}
