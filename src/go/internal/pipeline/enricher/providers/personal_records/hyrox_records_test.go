package personal_records

import (
	"context"
	"strings"
	"testing"

	"github.com/fitglue/server/src/go/pkg/testing/mocks"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	pbuser "github.com/fitglue/server/src/go/pkg/types/pb/models/user"
)

const roxfitSoloDesc = "Completed via ROXFIT\n\n🏆 Division: Open Men\n📊 Overall Rank: 270\n\n⏱️ Split Times:\nRun 1: 5:39\nSki Erg: 4:30\nRun 2: 5:12\nSled Push: 3:34\nRun 3: 5:16\nSled Pull: 4:48\nRun 4: 5:28\nBurpee Broad Jump: 6:13\nRun 5: 5:37\nRow Erg: 4:44\nRun 6: 5:15\nFarmers Carry: 1:37\nRun 7: 5:16\nSandbag Lunge: 4:51\nRun 8: 5:27\nWall Ball: 8:59\nRoxzone: 7:24\n\nTwo races within 20hrs"

func hyroxActivity(name, desc string, elapsed float64) *pbactivity.StandardizedActivity {
	return &pbactivity.StandardizedActivity{
		Name:        name,
		Description: desc,
		ExternalId:  "this-race",
		Type:        pbactivity.ActivityType_ACTIVITY_TYPE_WORKOUT,
		Tags:        []string{"HYROX"},
		Sessions:    []*pbactivity.Session{{TotalElapsedTime: elapsed}},
	}
}

func TestHyroxRaceValues_OfficialSplits(t *testing.T) {
	race, ok := HyroxRaceValues(hyroxActivity("Hyrox Birmingham - Open Men's Solo", roxfitSoloDesc, 5382))
	if !ok || race.Division.Key != "men_open" {
		t.Fatalf("ok=%v division=%q", ok, race.Division.Key)
	}
	want := map[string]float64{"total_time": 5382, "run_1": 339, "skierg": 270, "run_8": 327, "wall_balls": 539, "roxzone": 444}
	for k, v := range want {
		if race.Values[k] != v {
			t.Errorf("%s = %v, want %v", k, race.Values[k], v)
		}
	}
	if len(race.Values) != 18 {
		t.Errorf("values = %d, want 18 (total + 8 runs + 8 stations + roxzone)", len(race.Values))
	}
}

func TestHyroxRaceValues_TitleTimeAndSets(t *testing.T) {
	a := hyroxActivity("Hyrox - London Olympia 25/26 - 1:23:47", "🏁 Hyrox - Men's Open:\n🏃 Run 1: 4:49", 5043)
	a.Sessions[0].StrengthSets = []*pbactivity.StrengthSet{
		{ExerciseName: "Running", DurationSeconds: 289},
		{ExerciseName: "SkiErg", DurationSeconds: 258},
		{ExerciseName: "Running", DurationSeconds: 335},
	}
	race, ok := HyroxRaceValues(a)
	if !ok || race.Division.Key != "men_open" {
		t.Fatalf("ok=%v %q", ok, race.Division.Key)
	}
	if race.Values["total_time"] != 5027 || race.Values["run_2"] != 335 || race.Values["skierg"] != 258 {
		t.Errorf("values = %v", race.Values)
	}
}

func TestHyroxRaceValues_SkipsSimulationsAndUnknownDivision(t *testing.T) {
	if _, ok := HyroxRaceValues(hyroxActivity("Hyrox Sim", "🏁 Hyrox - Male Single:\n🏃 Run 1: 5:54", 4756)); ok {
		t.Error("simulation must not set records")
	}
	if _, ok := HyroxRaceValues(hyroxActivity("Hyrox London", "", 4500)); ok {
		t.Error("no division, no records")
	}
}

// A doubles time is checked against the doubles bucket only: an existing Open Men's Solo
// record (Olympia's 1:23:47) is never compared with the doubles 1:15:29.
func TestCheckHyroxRecords_PerDivisionKeys(t *testing.T) {
	var asked []string
	db := &prDB{
		MockDatabase: &mocks.MockDatabase{},
		getRecord: func(rt string) (*pbuser.PersonalRecord, error) {
			asked = append(asked, rt)
			if rt == "hybrid_race_hyrox_men_open_total_time" {
				return &pbuser.PersonalRecord{Value: 5027, Unit: "seconds", ActivityId: "olympia"}, nil
			}
			return nil, nil
		},
	}
	p := newProviderWithDB(db)

	doubles := strings.Replace(roxfitSoloDesc, "Division: Open Men", "Division: Doubles Men", 1)
	prs := p.checkHyroxRecords(context.Background(), discardLogger(), hyroxActivity("Hyrox Birmingham - Open Men's Doubles", doubles, 4529), "u1")
	if len(prs) != 18 {
		t.Errorf("first doubles race: %d PRs, want 18", len(prs))
	}
	for _, rt := range asked {
		if !strings.HasPrefix(rt, "hybrid_race_hyrox_men_doubles_") {
			t.Errorf("doubles race read %s", rt)
		}
	}

	// The solo at 1:29:42 is slower than the stored 1:23:47: no total-time PR.
	asked = nil
	prs = p.checkHyroxRecords(context.Background(), discardLogger(), hyroxActivity("Hyrox Birmingham - Open Men's Solo", roxfitSoloDesc, 5382), "u1")
	for _, pr := range prs {
		if pr.RecordType == "hybrid_race_hyrox_men_open_total_time" {
			t.Errorf("solo 1:29:42 claimed a PB over 1:23:47")
		}
	}
	if len(prs) != 17 {
		t.Errorf("solo: %d PRs, want 17 (every category but total)", len(prs))
	}
}

func TestHyroxRecordDisplay(t *testing.T) {
	cases := map[string]string{
		"hybrid_race_hyrox_men_open_total_time":    "HYROX Open Men's Solo Total Time",
		"hybrid_race_hyrox_men_doubles_run_3":      "HYROX Open Men's Doubles Run 3",
		"hybrid_race_hyrox_men_pro_doubles_rowing": "HYROX Pro Men's Doubles Row",
		"hybrid_race_hyrox_mixed_doubles_roxzone":  "HYROX Mixed Doubles (Open) Roxzone",
	}
	for in, want := range cases {
		if got := formatRecordTypeForDisplay(in); got != want {
			t.Errorf("%s → %q, want %q", in, got, want)
		}
	}
	// Legacy division-less keys still render.
	if got := formatRecordTypeForDisplay("hybrid_race_hyrox_total_time"); got != "HYROX Total Time" {
		t.Errorf("legacy = %q", got)
	}
}
