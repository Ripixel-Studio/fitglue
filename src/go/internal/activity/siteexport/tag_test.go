package siteexport

import (
	"strings"
	"testing"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
)

// The classification goes out as tag/isRace, top level and on the entry; its
// raceWhy never does.
func TestBuildActivityCarriesTagAndRace(t *testing.T) {
	sc := runShowcase()
	if sc.Enrichments == nil {
		sc.Enrichments = &pbactivity.ActivityEnrichments{}
	}
	sc.Enrichments.Classification = &pbactivity.ActivityClassification{
		Tag: "run", IsRace: true, RaceWhy: "parkrun near his usual time", Model: "claude-haiku",
		RaceType: "parkrun", RaceVariant: "Newark",
	}
	b, err := BuildActivity(sc, runEntry(), fixedNow, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "raceWhy") || strings.Contains(string(b), "near his usual time") {
		t.Fatalf("raceWhy exported: %s", b)
	}
	got := decode(t, b)
	if got["tag"] != "run" || got["isRace"] != true {
		t.Errorf("top level tag/isRace = %v/%v", got["tag"], got["isRace"])
	}
	if got["raceType"] != "parkrun" || got["raceVariant"] != "Newark" {
		t.Errorf("raceType/raceVariant = %v/%v", got["raceType"], got["raceVariant"])
	}
	entry := got["entry"].(map[string]any)
	if entry["tag"] != "run" || entry["isRace"] != true {
		t.Errorf("entry tag/isRace = %v/%v", entry["tag"], entry["isRace"])
	}
	if _, ok := got["race"]; ok {
		t.Error("a run with no splits must have no race object")
	}
}

// An unclassified activity still gets a tag: the rule's.
func TestBuildActivityUnclassifiedFallsBackToRule(t *testing.T) {
	b, err := BuildActivity(liftShowcase(), liftEntry(), fixedNow, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := decode(t, b)
	if got["tag"] != "strength" || got["isRace"] != false {
		t.Errorf("tag/isRace = %v/%v", got["tag"], got["isRace"])
	}
	if got["entry"].(map[string]any)["tag"] != "strength" {
		t.Errorf("entry tag = %v", got["entry"].(map[string]any)["tag"])
	}
}

func TestBuildIndexEntriesCarryTag(t *testing.T) {
	stored := runEntry()
	stored.Tag, stored.IsRace = "run", true
	b, err := BuildIndex(testProfile(), []*pbactivity.ShowcaseProfileEntry{stored, liftEntry()}, nil, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	acts := decode(t, b)["activities"].([]any)
	first, second := acts[0].(map[string]any), acts[1].(map[string]any)
	if first["tag"] != "run" || first["isRace"] != true {
		t.Errorf("stored tag lost: %v/%v", first["tag"], first["isRace"])
	}
	if second["tag"] == "" || second["tag"] == nil || second["isRace"] != false {
		t.Errorf("untagged entry not given the rule's tag: %v/%v", second["tag"], second["isRace"])
	}
}
