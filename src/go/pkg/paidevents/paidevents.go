// nolint:proto-json -- a plain JSON file the box writes, not a proto
// Package paidevents reads the events he paid for, copied from his "Fitness Paid
// For Events" sheet by the box (jarvis scripts/fitness_paid_events.py) to the
// artifacts bucket. The ai-activity-type enricher shows the classifier the
// events on an activity's date: strong evidence for isRace, still judged
// (a Hyrox sim or a fun run with the kids is paid for and not a race).
//
// The file holds only dates and event names, never who he went with.
package paidevents

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// Object is the file's path in the artifacts bucket.
const Object = "config/paid-events.json"

// Event is one row of the sheet.
type Event struct {
	Date string `json:"date"` // YYYY-MM-DD, his local date
	Name string `json:"name"`
}

type file struct {
	Events []Event `json:"events"`
}

// Getter reads one object from a bucket.
type Getter interface {
	Get(ctx context.Context, bucket, path string) ([]byte, error)
}

// Load reads the file. A missing or unreadable file is no events and an error.
func Load(ctx context.Context, g Getter, bucket string) ([]Event, error) {
	raw, err := g.Get(ctx, bucket, Object)
	if err != nil {
		return nil, err
	}
	return Parse(raw)
}

// Parse reads the file's JSON.
func Parse(raw []byte) ([]Event, error) {
	var f file
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, err
	}
	return f.Events, nil
}

// weekLong is an event that runs for days from its sheet date ("Running Week").
func weekLong(name string) bool {
	return strings.Contains(strings.ToLower(name), "week")
}

// On returns the names of the events on day (his local date). An event whose name
// says it is a week long also counts for the six days after its date.
func On(events []Event, day time.Time) []string {
	d := day.Format("2006-01-02")
	var out []string
	for _, e := range events {
		if e.Date == d {
			out = append(out, e.Name)
			continue
		}
		if weekLong(e.Name) {
			start, err := time.Parse("2006-01-02", e.Date)
			if err != nil {
				continue
			}
			day0, _ := time.Parse("2006-01-02", d)
			if diff := day0.Sub(start); diff > 0 && diff < 7*24*time.Hour {
				out = append(out, e.Name)
			}
		}
	}
	return out
}
