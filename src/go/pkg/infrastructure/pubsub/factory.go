package pubsub

import (
	"context"
	"fmt"

	"cloud.google.com/go/pubsub"
	"github.com/cloudevents/sdk-go/v2/event"
	"github.com/fitglue/server/src/go/internal/infra"
	"github.com/fitglue/server/src/go/pkg/infrastructure/localqueue"
)

// Publisher is what the services publish through: the on-box SQLite queue
// (localqueue, the default) or Cloud Pub/Sub (FITGLUE_QUEUE=pubsub).
type Publisher interface {
	PublishCloudEvent(ctx context.Context, topic string, e event.Event) (string, error)
	PublishJSON(ctx context.Context, topic string, data []byte) error
	Publish(ctx context.Context, topic string, data []byte, attrs map[string]string) (string, error)
}

var (
	_ Publisher = (*PubSubAdapter)(nil)
	_ Publisher = (*localqueue.Queue)(nil)
)

// NewPublisherFromEnv opens the publisher the environment selects. queue is
// non-nil only for the on-box queue, so a consuming service can Run it.
func NewPublisherFromEnv(ctx context.Context, projectID string, logger infra.Logger) (pub Publisher, queue *localqueue.Queue, closeFn func(), err error) {
	if localqueue.Enabled() {
		path := localqueue.DefaultPath()
		q, err := localqueue.Open(path, logger)
		if err != nil {
			return nil, nil, nil, err
		}
		logger.Info(ctx, "Publishing to the on-box queue", "db", path)
		return q, q, func() { _ = q.Close() }, nil
	}
	client, err := pubsub.NewClient(ctx, projectID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("pubsub client: %w", err)
	}
	logger.Info(ctx, "Publishing to Cloud Pub/Sub", "project", projectID)
	return &PubSubAdapter{Client: client, Logger: logger}, nil, func() { _ = client.Close() }, nil
}
