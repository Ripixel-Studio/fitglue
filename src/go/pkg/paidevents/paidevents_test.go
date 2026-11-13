package paidevents

import (
	"testing"
	"time"
)

func TestOn(t *testing.T) {
	evs, err := Parse([]byte(`{"events":[{"date":"2026-07-17","name":"5.30 5k"},{"date":"2026-06-01","name":"Gordon Whelbourn Running Week"},{"date":"2026-07-11","name":"Spartan Sprint"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	day := func(s string) time.Time { d, _ := time.Parse("2006-01-02", s); return d }
	if got := On(evs, day("2026-07-17")); len(got) != 1 || got[0] != "5.30 5k" {
		t.Errorf("same day: %v", got)
	}
	if got := On(evs, day("2026-06-04")); len(got) != 1 {
		t.Errorf("a running week counts for its days: %v", got)
	}
	if got := On(evs, day("2026-06-08")); len(got) != 0 {
		t.Errorf("not after the week: %v", got)
	}
	if got := On(evs, day("2026-07-18")); len(got) != 0 {
		t.Errorf("next day: %v", got)
	}
}
