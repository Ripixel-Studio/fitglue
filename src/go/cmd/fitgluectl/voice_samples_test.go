// nolint:proto-json // encoding/json writes plain structs (voiceSample), never a proto
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	pbpipeline "github.com/fitglue/server/src/go/pkg/types/pb/models/pipeline"
)

// Cases copied from ripixel-website tasks/fitness-hyrox.test.mjs, "his own words
// (contract §3.1)": the JS is the spec.
func TestAthleteWords(t *testing.T) {
	roxfit := "Completed via ROXFIT\n\n🏆 Division: Open Men\n📊 Overall Rank: 270\n\n⏱️ Split Times:\nRun 1: 5:39\nRoxzone: 7:24\n\n\nTwo races within 20hrs?! WHAT WAS I THINKING??\n\n✨ AI Summary:\nGrit."
	cases := []struct{ name, in, want string }{
		{"roxfit header, splits and AI summary dropped", roxfit, "Two races within 20hrs?! WHAT WAS I THINKING??"},
		{"hevy set dump", "Great one this morn 😅\n\nLogged with Hevy\n\nDeadlift (Dumbbell)\nSet 1: 20 kg x 12\nSet 2: 20 kg x 12", "Great one this morn 😅"},
		{"hevy only", "Logged with Hevy\n\nWarm Up\nSet 1: 2min 21s", ""},
		{"workout summary", "Back at it 💪\n\nWorkout Summary:\n📊 22 sets • 32340kg volume • 145 reps", "Back at it 💪"},
		{"official time", "Dad's first 5k!\n\nOfficial time: 28:31", "Dad's first 5k!"},
		{"his emoji heading stays", "🥾 Day 10\n\nRequested by the brilliant coach.\n\n🔥 Streak Tracker:\n• 3 days", "🥾 Day 10\n\nRequested by the brilliant coach."},
		{"his lists stay", "today I learned:\n* Devil's press is horrific\n* 9kg wall balls are out of reach", "today I learned:\n* Devil's press is horrific\n* 9kg wall balls are out of reach"},
		{"posted via fitglue", "Out along the Trent.\n\n🌤️ Conditions:\nClear, 11°C.\n\nPosted via FitGlue 💪", "Out along the Trent."},
		{"doubles: AI summary stops it", "Completed via ROXFIT\n\n🏆 Division: Doubles Men\n\nFirst of two races over two days!\n\n✨ AI Summary:\nA demanding effort.", "First of two races over two days!"},
		{"empty", "", ""},
		{"crlf", "Legs gone.\r\n\r\n❤️ Heart Rate:\r\nAvg 150", "Legs gone."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := athleteWords(c.in); got != c.want {
				t.Errorf("athleteWords(%q)\n got %q\nwant %q", c.in, got, c.want)
			}
		})
	}
}

type fakeRunLister []*pbpipeline.PipelineRun

func (f fakeRunLister) ListRuns(ctx context.Context, since time.Time) ([]*pbpipeline.PipelineRun, error) {
	return f, nil
}

func TestVoiceSamplesWritesPrivateFile(t *testing.T) {
	ts := func(s string) *timestamppb.Timestamp {
		tm, _ := time.Parse(time.RFC3339, s)
		return timestamppb.New(tm)
	}
	runs := fakeRunLister{
		{SourceActivityId: "a1", Title: "Leg day", Description: "Absolutely destroyed, could not walk the stairs after 😅\n\n✨ AI Summary:\nGood.", Type: pbactivity.ActivityType_ACTIVITY_TYPE_WEIGHT_TRAINING, StartTime: ts("2026-10-01T07:00:00Z")},
		{SourceActivityId: "a1", Title: "Leg day", Description: "Absolutely destroyed, could not walk the stairs after 😅", Type: pbactivity.ActivityType_ACTIVITY_TYPE_WEIGHT_TRAINING, StartTime: ts("2026-10-01T07:00:00Z")}, // a resend of the same workout
		{SourceActivityId: "a2", Title: "Run", Description: "Short one.", StartTime: ts("2026-10-02T07:00:00Z")},                                                                                                               // under 20 runes
		{SourceActivityId: "a3", Title: "Lift", Description: "Logged with Hevy\n\nSquat\nSet 1: 100 kg x 5", StartTime: ts("2026-10-03T07:00:00Z")},                                                                            // no words
	}
	dir := filepath.Join(t.TempDir(), "fitglue")
	out := filepath.Join(dir, "voice-samples.jsonl")
	var stdout, stderr bytes.Buffer
	open := func(ctx context.Context, f *voiceSamplesFlags) (runLister, func(), error) {
		return runs, func() {}, nil
	}
	if err := voiceSamplesCommand(context.Background(), []string{"--uid", "u1", "--out", out}, &stdout, &stderr, open); err != nil {
		t.Fatal(err)
	}
	if got, want := stdout.String(), "4 runs, 1 with his words\n"; got != want {
		t.Errorf("stdout = %q, want %q (counts only)", got, want)
	}
	if strings.Contains(stdout.String()+stderr.String(), "destroyed") {
		t.Error("a sample leaked to the terminal")
	}
	st, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %v, want 0600", st.Mode().Perm())
	}
	dst, _ := os.Stat(dir)
	if dst.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %v, want 0700", dst.Mode().Perm())
	}
	fh, _ := os.Open(out)
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	var lines []map[string]string
	for sc.Scan() {
		var m map[string]string
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, m)
	}
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1", len(lines))
	}
	l := lines[0]
	if l["date"] != "2026-10-01" || l["title"] != "Leg day" || l["words"] != "Absolutely destroyed, could not walk the stairs after 😅" || l["type"] == "" {
		t.Errorf("line = %v", l)
	}
}

func TestVoiceSamplesTightensExistingFile(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "v.jsonl")
	if err := os.WriteFile(out, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	open := func(ctx context.Context, f *voiceSamplesFlags) (runLister, func(), error) {
		return fakeRunLister{}, func() {}, nil
	}
	if err := voiceSamplesCommand(context.Background(), []string{"--uid", "u1", "--out", out}, &stdout, &stderr, open); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(out)
	if st.Mode().Perm() != 0o600 || st.Size() != 0 {
		t.Errorf("mode %v size %d, want 0600 and empty", st.Mode().Perm(), st.Size())
	}
	if dst, _ := os.Stat(dir); dst.Mode().Perm() != 0o755 {
		t.Errorf("existing dir mode = %v, want it left at 0755", dst.Mode().Perm())
	}
}
