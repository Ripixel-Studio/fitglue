package localqueue

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// pushEnvelope is the body Pub/Sub push sent; the handlers parse exactly this.
type pushEnvelope struct {
	Message struct {
		Data        []byte            `json:"data"`
		Attributes  map[string]string `json:"attributes,omitempty"`
		MessageID   string            `json:"messageId"`
		PublishTime string            `json:"publishTime"`
	} `json:"message"`
	Subscription    string `json:"subscription"`
	DeliveryAttempt int    `json:"deliveryAttempt"`
}

// Run starts the workers for subs and returns; they stop when ctx is done.
// Leases left by this service's previous run are released first.
func (q *Queue) Run(ctx context.Context, subs []Subscription) error {
	topics := make([]string, 0, len(subs))
	for _, s := range subs {
		topics = append(topics, s.Topic)
	}
	if n, err := q.releaseLeases(ctx, topics); err != nil {
		return fmt.Errorf("localqueue: release leases: %w", err)
	} else if n > 0 {
		q.logger.Info(ctx, "localqueue: redelivering messages leased by the previous run", "count", n)
	}
	for _, s := range subs {
		workers := s.Workers
		if workers < 1 {
			workers = 1
		}
		for i := 0; i < workers; i++ {
			go q.worker(ctx, s, q.subscribeWake(s.Topic))
		}
		q.logger.Info(ctx, "localqueue: consuming", "subscription", s.Subscription, "topic", s.Topic, "workers", workers)
	}
	return nil
}

func (q *Queue) worker(ctx context.Context, s Subscription, wake chan struct{}) {
	ticker := time.NewTicker(q.PollInterval)
	defer ticker.Stop()
	for {
		// Drain everything due before sleeping.
		for ctx.Err() == nil {
			delivered, err := q.DeliverOne(ctx, s)
			if err != nil {
				q.logger.Error(ctx, "localqueue: delivery loop error", "subscription", s.Subscription, "error", err)
				break
			}
			if !delivered {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-wake:
		}
	}
}

// DeliverOne claims one due message on s.Topic and hands it to s.Handler.
// It reports whether a message was delivered (acked or not).
func (q *Queue) DeliverOne(ctx context.Context, s Subscription) (bool, error) {
	m, err := q.claim(ctx, s.Topic, s.AckDeadline)
	if err != nil || m == nil {
		return false, err
	}

	status, body := q.invoke(ctx, s, m)
	// Book-keeping must land even if the service is shutting down.
	bkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()

	if (status >= 200 && status < 300) || status == http.StatusProcessing {
		return true, q.ack(bkCtx, m.id)
	}
	dead := s.MaxAttempts > 0 && m.attempts >= s.MaxAttempts
	reason := fmt.Sprintf("HTTP %d: %s", status, bytes.TrimSpace(body))
	if dead {
		q.logger.Error(ctx, "localqueue: dead-lettered after max delivery attempts",
			"subscription", s.Subscription, "message_id", m.id, "attempts", m.attempts, "last_error", truncate(reason, 300))
	} else {
		q.logger.Warn(ctx, "localqueue: nacked, will redeliver",
			"subscription", s.Subscription, "message_id", m.id, "attempts", m.attempts, "backoff", s.Backoff(m.attempts).String(), "status", status)
	}
	return true, q.nack(bkCtx, m.id, s.Backoff(m.attempts), dead, reason)
}

// invoke runs the handler on a Pub/Sub-shaped push request. A panic is a nack.
func (q *Queue) invoke(ctx context.Context, s Subscription, m *message) (status int, body []byte) {
	var env pushEnvelope
	env.Message.Data = m.data
	env.Message.Attributes = m.attributes
	env.Message.MessageID = strconv.FormatInt(m.id, 10)
	env.Message.PublishTime = m.publishedAt.Format(time.RFC3339Nano)
	env.Subscription = "localqueue/" + s.Subscription
	env.DeliveryAttempt = m.attempts
	payload, err := json.Marshal(env)
	if err != nil {
		return http.StatusInternalServerError, []byte(err.Error())
	}

	hctx, cancel := context.WithTimeout(ctx, s.AckDeadline)
	defer cancel()
	req, err := http.NewRequestWithContext(hctx, http.MethodPost, "/localqueue/"+s.Subscription, bytes.NewReader(payload))
	if err != nil {
		return http.StatusInternalServerError, []byte(err.Error())
	}
	req.Header.Set("Content-Type", "application/json")

	rec := &recorder{header: http.Header{}}
	defer func() {
		if r := recover(); r != nil {
			q.logger.Error(ctx, "localqueue: handler panicked", "subscription", s.Subscription, "message_id", m.id, "panic", fmt.Sprint(r))
			status, body = http.StatusInternalServerError, []byte(fmt.Sprintf("panic: %v", r))
		}
	}()
	s.Handler.ServeHTTP(rec, req)
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	return rec.status, rec.body.Bytes()
}

// recorder is a minimal http.ResponseWriter for in-process delivery.
type recorder struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (r *recorder) Header() http.Header { return r.header }
func (r *recorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
}
func (r *recorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	if r.body.Len() < 4096 {
		r.body.Write(b)
	}
	return len(b), nil
}
