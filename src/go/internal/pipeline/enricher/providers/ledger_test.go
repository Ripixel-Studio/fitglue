package providers

import (
	"testing"
	"time"
)

func TestLedger_PutBeforeFoldRoundTrip(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 10, d, 8, 0, 0, 0, time.UTC) }
	var l Ledger
	l = l.Put(LedgerEntry{ID: "b", Start: day(27), Value: 8.6})
	l = l.Put(LedgerEntry{ID: "a", Start: day(26), Value: 5})
	l = l.Put(LedgerEntry{ID: "c", Start: day(28), Value: 8.7})
	l = l.Put(LedgerEntry{ID: "b", Start: day(27), Value: 8.8}) // repost replaces

	if len(l) != 3 || l[0].ID != "a" || l[1].Value != 8.8 {
		t.Fatalf("Put: %+v", l)
	}
	if got := l.Before(day(27), "b").Sum(nil); got != 5 {
		t.Errorf("Before(27, b) = %v, want 5 (not itself, not the 28th)", got)
	}
	if last, _ := l.Latest(); last.ID != "c" {
		t.Errorf("Latest = %+v", last)
	}

	folded := l.Fold(day(28))
	if len(folded) != 2 || folded[0].ID != "" || folded[0].Value != 13.8 || !folded[0].Start.Equal(day(27)) {
		t.Errorf("Fold: %+v", folded)
	}
	// A carried entry is never replaced by Put.
	if got := folded.Put(LedgerEntry{Start: day(29), Value: 1}); len(got) != 3 {
		t.Errorf("Put of an id-less entry replaced the carried one: %+v", got)
	}

	back, ok := ParseLedger(map[string]interface{}{LedgerField: folded.ToFirestore()})
	if !ok || len(back) != 2 || back[0].Value != 13.8 || back[1].ID != "c" || !back[1].Start.Equal(day(28)) {
		t.Errorf("round trip: %+v %v", back, ok)
	}
	if _, ok := ParseLedger(map[string]interface{}{"accumulated": 3.0}); ok {
		t.Error("a document without a ledger must report ok=false")
	}
	if got := l.Since(day(27)); len(got) != 2 {
		t.Errorf("Since: %+v", got)
	}
}
