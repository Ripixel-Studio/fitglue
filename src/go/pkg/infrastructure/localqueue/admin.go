package localqueue

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// TopicStats counts one topic's messages.
type TopicStats struct {
	Topic    string     `json:"topic"`
	Ready    int        `json:"ready"`   // due now, not leased
	Waiting  int        `json:"waiting"` // backing off
	Leased   int        `json:"leased"`  // being delivered
	Dead     int        `json:"dead"`    // dead-lettered
	OldestAt *time.Time `json:"oldest_at,omitempty"`
}

// Stats returns per-topic counts, every subscribed topic included.
func (q *Queue) Stats(ctx context.Context) ([]TopicStats, error) {
	now := q.now().UnixMilli()
	rows, err := q.db.QueryContext(ctx, `
SELECT topic,
	SUM(CASE WHEN dead_at IS NULL AND lease_until <= ? AND next_at <= ? THEN 1 ELSE 0 END),
	SUM(CASE WHEN dead_at IS NULL AND lease_until <= ? AND next_at > ? THEN 1 ELSE 0 END),
	SUM(CASE WHEN dead_at IS NULL AND lease_until > ? THEN 1 ELSE 0 END),
	SUM(CASE WHEN dead_at IS NOT NULL THEN 1 ELSE 0 END),
	MIN(published_at)
FROM queue_messages GROUP BY topic`, now, now, now, now, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byTopic := map[string]TopicStats{}
	for rows.Next() {
		var s TopicStats
		var oldest sql.NullInt64
		if err := rows.Scan(&s.Topic, &s.Ready, &s.Waiting, &s.Leased, &s.Dead, &oldest); err != nil {
			return nil, err
		}
		if oldest.Valid {
			t := time.UnixMilli(oldest.Int64).UTC()
			s.OldestAt = &t
		}
		byTopic[s.Topic] = s
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]TopicStats, 0, len(AllPolicies))
	for _, p := range AllPolicies {
		s, ok := byTopic[p.Topic]
		if !ok {
			s = TopicStats{Topic: p.Topic}
		}
		out = append(out, s)
		delete(byTopic, p.Topic)
	}
	for _, s := range byTopic {
		out = append(out, s)
	}
	return out, nil
}

// DeadMessage is one dead-lettered message.
type DeadMessage struct {
	ID          int64     `json:"id"`
	Topic       string    `json:"topic"`
	Attempts    int       `json:"attempts"`
	PublishedAt time.Time `json:"published_at"`
	DeadAt      time.Time `json:"dead_at"`
	LastError   string    `json:"last_error"`
	SizeBytes   int       `json:"size_bytes"`
}

// Dead lists dead-lettered messages, newest first.
func (q *Queue) Dead(ctx context.Context, limit int) ([]DeadMessage, error) {
	rows, err := q.db.QueryContext(ctx, `
SELECT id, topic, attempts, published_at, dead_at, COALESCE(last_error, ''), length(data)
FROM queue_messages WHERE dead_at IS NOT NULL ORDER BY dead_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeadMessage
	for rows.Next() {
		var d DeadMessage
		var pub, dead int64
		if err := rows.Scan(&d.ID, &d.Topic, &d.Attempts, &pub, &dead, &d.LastError, &d.SizeBytes); err != nil {
			return nil, err
		}
		d.PublishedAt = time.UnixMilli(pub).UTC()
		d.DeadAt = time.UnixMilli(dead).UTC()
		out = append(out, d)
	}
	return out, rows.Err()
}

// Retry puts a dead (or backing-off) message back on its topic, due now, with
// its attempt count reset.
func (q *Queue) Retry(ctx context.Context, id int64) error {
	res, err := q.db.ExecContext(ctx,
		`UPDATE queue_messages SET dead_at = NULL, attempts = 0, next_at = ?, lease_until = 0 WHERE id = ?`,
		q.now().UnixMilli(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no queued message %d", id)
	}
	return nil
}
