// Package localqueue replaces Cloud Pub/Sub on the box: a message table in the
// on-box SQLite file (FITGLUE_DB) that every service opens, plus a dispatcher
// that delivers due messages to the same push handlers Pub/Sub used to call.
//
// It keeps Pub/Sub's contract so no handler changes:
//
//   - the handler receives the push envelope Pub/Sub sent
//     ({"message":{"data","attributes","messageId","publishTime"},"subscription"});
//   - a 2xx (or 102) response acks: the row is deleted;
//   - anything else nacks: the row comes back after an exponential backoff
//     between the subscription's minimum and maximum (terraform/pubsub.tf's
//     retry_policy), and after MaxAttempts deliveries it is dead-lettered
//     (kept, with dead_at set) instead of retried;
//   - a delivery is leased for the subscription's ack deadline, so a crash or a
//     restart mid-delivery redelivers it.
//
// Publishers in one process and consumers in another meet in the file: each
// worker polls its topic every PollInterval (a publish in the same process
// wakes them at once).
package localqueue

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/cloudevents/sdk-go/v2/event"
	"github.com/fitglue/server/src/go/internal/infra"
	_ "modernc.org/sqlite" // pure-Go driver: build-box stays CGO_ENABLED=0
)

// EnvDB names the SQLite file; EnvBackend picks "sqlite" (default) or "pubsub".
const (
	EnvDB      = "FITGLUE_DB"
	EnvBackend = "FITGLUE_QUEUE"
)

// DefaultPath is where the box keeps the file when FITGLUE_DB is unset.
func DefaultPath() string {
	if p := os.Getenv(EnvDB); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = "."
	}
	return filepath.Join(home, ".local", "share", "fitglue", "fitglue.db")
}

// Enabled reports whether the services should use this queue rather than
// Cloud Pub/Sub. Only FITGLUE_QUEUE=pubsub turns it off (docker-compose, the
// retired Cloud Run deployment).
func Enabled() bool { return os.Getenv(EnvBackend) != "pubsub" }

const schema = `
CREATE TABLE IF NOT EXISTS queue_messages (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	topic        TEXT    NOT NULL,
	data         BLOB    NOT NULL,
	attributes   TEXT,
	published_at INTEGER NOT NULL,
	attempts     INTEGER NOT NULL DEFAULT 0,
	next_at      INTEGER NOT NULL,
	lease_until  INTEGER NOT NULL DEFAULT 0,
	dead_at      INTEGER,
	last_error   TEXT
);
CREATE INDEX IF NOT EXISTS queue_messages_due ON queue_messages (topic, dead_at, next_at);
`

// Queue is the publisher half (shared.Publisher) and the dispatcher half.
type Queue struct {
	db     *sql.DB
	logger infra.Logger
	now    func() time.Time

	mu    sync.Mutex
	wakes map[string][]chan struct{}

	// PollInterval is how often an idle worker looks for due messages.
	PollInterval time.Duration
}

// Open opens (creating if needed) the SQLite file at path in WAL mode.
func Open(path string, logger infra.Logger) (*Queue, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("localqueue: create %s: %w", dir, err)
		}
	}
	dsn := "file:" + path + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("localqueue: open %s: %w", path, err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("localqueue: schema: %w", err)
	}
	return &Queue{db: db, logger: logger, now: time.Now, wakes: map[string][]chan struct{}{}, PollInterval: time.Second}, nil
}

// Close closes the database.
func (q *Queue) Close() error { return q.db.Close() }

// Publish stores one message and returns its id.
func (q *Queue) Publish(ctx context.Context, topic string, data []byte, attrs map[string]string) (string, error) {
	if !KnownTopic(topic) {
		// Pub/Sub dropped messages on a topic without a subscription; keep doing
		// that rather than filling the table with rows nobody reads.
		q.logger.Warn(ctx, "localqueue: no subscriber for topic, dropping message", "topic", topic, "size_bytes", len(data))
		return "", nil
	}
	var attrJSON any
	if len(attrs) > 0 {
		b, err := json.Marshal(attrs)
		if err != nil {
			return "", err
		}
		attrJSON = string(b)
	}
	now := q.now().UnixMilli()
	// Detached like the Pub/Sub adapter: the event is completed work that must
	// reach downstream even if the caller's request is about to time out.
	pubCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	res, err := q.db.ExecContext(pubCtx,
		`INSERT INTO queue_messages (topic, data, attributes, published_at, next_at) VALUES (?, ?, ?, ?, ?)`,
		topic, data, attrJSON, now, now)
	if err != nil {
		q.logger.Error(ctx, "localqueue: publish failed", "topic", topic, "error", err)
		return "", fmt.Errorf("localqueue: publish to %s: %w", topic, err)
	}
	id, _ := res.LastInsertId()
	q.wake(topic)
	q.logger.Info(ctx, "Message published successfully", "topic", topic, "message_id", id, "size_bytes", len(data))
	return strconv.FormatInt(id, 10), nil
}

// PublishCloudEvent publishes e as JSON, as the Pub/Sub adapter did.
func (q *Queue) PublishCloudEvent(ctx context.Context, topic string, e event.Event) (string, error) {
	b, err := json.Marshal(e)
	if err != nil {
		return "", err
	}
	return q.Publish(ctx, topic, b, nil)
}

// PublishJSON publishes raw bytes.
func (q *Queue) PublishJSON(ctx context.Context, topic string, data []byte) error {
	_, err := q.Publish(ctx, topic, data, nil)
	return err
}

func (q *Queue) wake(topic string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, ch := range q.wakes[topic] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (q *Queue) subscribeWake(topic string) chan struct{} {
	ch := make(chan struct{}, 1)
	q.mu.Lock()
	q.wakes[topic] = append(q.wakes[topic], ch)
	q.mu.Unlock()
	return ch
}

// message is one claimed delivery.
type message struct {
	id          int64
	data        []byte
	attributes  map[string]string
	publishedAt time.Time
	attempts    int
}

// claim leases the oldest due message on topic, or returns (nil, nil).
func (q *Queue) claim(ctx context.Context, topic string, lease time.Duration) (*message, error) {
	now := q.now().UnixMilli()
	row := q.db.QueryRowContext(ctx, `
UPDATE queue_messages SET attempts = attempts + 1, lease_until = ?
WHERE id = (
	SELECT id FROM queue_messages
	WHERE topic = ? AND dead_at IS NULL AND next_at <= ? AND lease_until <= ?
	ORDER BY next_at, id LIMIT 1)
RETURNING id, data, attributes, published_at, attempts`,
		now+lease.Milliseconds(), topic, now, now)
	var (
		m     message
		attrs sql.NullString
		pub   int64
	)
	if err := row.Scan(&m.id, &m.data, &attrs, &pub, &m.attempts); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	m.publishedAt = time.UnixMilli(pub).UTC()
	if attrs.Valid && attrs.String != "" {
		_ = json.Unmarshal([]byte(attrs.String), &m.attributes)
	}
	return &m, nil
}

func (q *Queue) ack(ctx context.Context, id int64) error {
	_, err := q.db.ExecContext(ctx, `DELETE FROM queue_messages WHERE id = ?`, id)
	return err
}

// nack schedules a redelivery, or dead-letters the message when dead is true.
func (q *Queue) nack(ctx context.Context, id int64, backoff time.Duration, dead bool, reason string) error {
	now := q.now().UnixMilli()
	var deadAt any
	if dead {
		deadAt = now
	}
	_, err := q.db.ExecContext(ctx,
		`UPDATE queue_messages SET lease_until = 0, next_at = ?, dead_at = ?, last_error = ? WHERE id = ?`,
		now+backoff.Milliseconds(), deadAt, truncate(reason, 2000), id)
	return err
}

// releaseLeases makes every leased, live message on topics due now. A service
// calls it at start: it is the only consumer of its topics, so any lease it
// finds belonged to its previous run (a deploy or a crash).
func (q *Queue) releaseLeases(ctx context.Context, topics []string) (int64, error) {
	var total int64
	for _, t := range topics {
		res, err := q.db.ExecContext(ctx,
			`UPDATE queue_messages SET lease_until = 0 WHERE topic = ? AND dead_at IS NULL AND lease_until > 0`, t)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
