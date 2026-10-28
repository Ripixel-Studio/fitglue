package providers

import (
	"sort"
	"time"
)

// LedgerField is the booster_data field that holds a "so far" booster's ledger.
const LedgerField = "ledger"

// LedgerEntry is one activity a "so far" booster (weekly goal, streak, lifetime
// distance, effort history) has counted: when it started and what it contributed.
type LedgerEntry struct {
	ID    string    // the activity's external id; "" for a carried-over legacy total
	Start time.Time // the activity's start time
	Value float64   // what it contributed (km, hours, 1, …); unused by the streak
	// Period pins a carried-over total to the period it was counted in (a goal's
	// "2026-W44"); empty for an activity, whose period follows from Start.
	Period string
}

// Ledger lets a "so far" booster compute its result as of an activity's start time.
//
// The old boosters kept one running total (accumulated km this week, current streak,
// lifetime km) and added each activity to it as it was processed. A repost or a
// late upload of an earlier activity then read a total that already contained the
// activities after it — the Birmingham doubles' weekly goal jumped from 43% to 100%
// on a repost because it counted the next day's solo. With a ledger the result is
// "everything that started before this activity, plus this activity", whatever order
// the activities are processed in, and processing one twice replaces its entry
// instead of counting it again.
//
// It is stored as an array (booster_data is written with MergeAll, which would keep
// pruned keys of a map) of {id, start, value}.
type Ledger []LedgerEntry

// ParseLedger reads the ledger from a booster_data document. ok is false when the
// document has no ledger yet (written by the running-total code), so the caller can
// carry its legacy total over once.
func ParseLedger(data map[string]interface{}) (l Ledger, ok bool) {
	raw, present := data[LedgerField]
	if !present {
		return nil, false
	}
	arr, _ := raw.([]interface{})
	for _, item := range arr {
		m, isMap := item.(map[string]interface{})
		if !isMap {
			continue
		}
		start, err := time.Parse(time.RFC3339, stringOf(m["start"]))
		if err != nil {
			continue
		}
		id := stringOf(m["id"])
		l = append(l, LedgerEntry{ID: id, Start: start, Value: ToFloat64(m["value"]), Period: stringOf(m["period"])})
	}
	return l, true
}

// Put records (or replaces) the entry for e.ID, keeping the ledger in start order.
// A carried-over entry (ID "") is never replaced by Put.
func (l Ledger) Put(e LedgerEntry) Ledger {
	out := make(Ledger, 0, len(l)+1)
	for _, x := range l {
		if e.ID != "" && x.ID == e.ID {
			continue
		}
		out = append(out, x)
	}
	out = append(out, e)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

// Before returns the entries that started strictly before t, other than the
// activity id itself — what had happened "so far" when that activity started.
func (l Ledger) Before(t time.Time, id string) Ledger {
	var out Ledger
	for _, x := range l {
		if id != "" && x.ID == id {
			continue
		}
		if x.Start.Before(t) {
			out = append(out, x)
		}
	}
	return out
}

// Sum adds up the values of the entries for which keep returns true (nil keeps all).
func (l Ledger) Sum(keep func(LedgerEntry) bool) float64 {
	var s float64
	for _, x := range l {
		if keep == nil || keep(x) {
			s += x.Value
		}
	}
	return s
}

// Since drops entries that started before t (bounds the document's size).
func (l Ledger) Since(t time.Time) Ledger {
	var out Ledger
	for _, x := range l {
		if !x.Start.Before(t) {
			out = append(out, x)
		}
	}
	return out
}

// Fold merges the entries that started before t into one carried entry (ID "")
// holding their sum, dated at the latest of them — a lifetime total keeps its
// history's sum without keeping every activity.
func (l Ledger) Fold(t time.Time) Ledger {
	var kept Ledger
	carried := LedgerEntry{}
	folded := false
	for _, x := range l {
		if x.Start.Before(t) {
			carried.Value += x.Value
			if !folded || x.Start.After(carried.Start) {
				carried.Start = x.Start
			}
			folded = true
			continue
		}
		kept = append(kept, x)
	}
	if !folded {
		return kept
	}
	return append(Ledger{carried}, kept...)
}

// LastUpdated is when booster_data was last written (SetBoosterData stamps it), or
// the zero time. A legacy running total was correct as of then, so a carried-over
// entry is dated there: activities that started after it add to the total, and a
// repost of one that started before it does not count the total again.
func LastUpdated(data map[string]interface{}) time.Time {
	if t, ok := data["last_updated"].(time.Time); ok {
		return t
	}
	return time.Time{}
}

// Latest is the entry with the latest start, or false for an empty ledger.
func (l Ledger) Latest() (LedgerEntry, bool) {
	if len(l) == 0 {
		return LedgerEntry{}, false
	}
	best := l[0]
	for _, x := range l[1:] {
		if !x.Start.Before(best.Start) {
			best = x
		}
	}
	return best, true
}

// ToFirestore is the array form SetBoosterData stores.
func (l Ledger) ToFirestore() []interface{} {
	out := make([]interface{}, 0, len(l))
	for _, x := range l {
		m := map[string]interface{}{
			"id":    x.ID,
			"start": x.Start.UTC().Format(time.RFC3339),
			"value": x.Value,
		}
		if x.Period != "" {
			m["period"] = x.Period
		}
		out = append(out, m)
	}
	return out
}

// LedgerString reads a string field of a booster_data document ("" when absent).
func LedgerString(v interface{}) string { return stringOf(v) }

func stringOf(v interface{}) string {
	s, _ := v.(string)
	return s
}
