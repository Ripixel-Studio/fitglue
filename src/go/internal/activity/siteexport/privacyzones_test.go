package siteexport

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
)

// All coordinates here are invented, around (10.0, 10.0). ~0.00001 deg lat is ~1.1 m.
var (
	// farZones is a zone nowhere near any fixture, for tests that are not about zones.
	farZones = NewZones([3]float64{-40, 170, HomeZoneRadiusM})
	// homeZones is a single 500 m home at (10.0, 10.0).
	homeZones = NewZones([3]float64{10.0, 10.0, HomeZoneRadiusM})
)

// gpsRecords walks north from (lat0, 10.0) in steps of stepDeg latitude.
func gpsRecords(lat0, stepDeg float64, n int) []*pbactivity.Record {
	base := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	recs := make([]*pbactivity.Record, n)
	for i := range recs {
		recs[i] = &pbactivity.Record{
			Timestamp:    ts(base.Add(time.Duration(i) * time.Second)),
			PositionLat:  lat0 + float64(i)*stepDeg,
			PositionLong: 10.0,
		}
	}
	return recs
}

func trackData(recs []*pbactivity.Record) *pbactivity.StandardizedActivity {
	return &pbactivity.StandardizedActivity{Sessions: []*pbactivity.Session{{Laps: []*pbactivity.Lap{{Records: recs}}}}}
}

func minDistToHome(route [][]float64) float64 {
	min := 1e12
	for _, p := range route {
		if d := haversineM(p[0], p[1], 10.0, 10.0); d < min {
			min = d
		}
	}
	return min
}

func TestLoopNearHomeVanishes(t *testing.T) {
	// 300 points, all within ~330 m of home.
	_, route := DeriveTelemetry(trackData(gpsRecords(10.0, 0.00001, 300)), homeZones, false)
	if len(route) != 0 {
		t.Fatalf("a loop inside the zone must publish no route, got %d points", len(route))
	}
}

func TestStartTrimmed(t *testing.T) {
	// Starts at home, runs north ~6.6 km at ~2.2 m per point (3000 points).
	_, route := DeriveTelemetry(trackData(gpsRecords(10.0, 0.00002, 3000)), homeZones, false)
	if len(route) == 0 || len(route) > maxRoutePoints {
		t.Fatalf("route length %d", len(route))
	}
	if d := minDistToHome(route); d <= HomeZoneRadiusM {
		t.Errorf("a published point is %.0f m from home, inside the zone", d)
	}
}

func TestTrimBeforeDownsample(t *testing.T) {
	// If trimming ran after the 200-point downsample, the first kept point could sit
	// well inside the zone edge; at full resolution it is within a couple of metres.
	_, route := DeriveTelemetry(trackData(gpsRecords(10.0, 0.00002, 3000)), homeZones, false)
	d := haversineM(route[0][0], route[0][1], 10.0, 10.0)
	if d > HomeZoneRadiusM+5 {
		t.Errorf("first kept point is %.1f m out; trim must run at full resolution", d)
	}
}

func TestMidRoutePassByHomeKept(t *testing.T) {
	// Starts ~2.2 km south of home, runs north through the zone, ends ~2.2 km north.
	// Nothing is trimmed and the pass by home stays in one continuous polyline.
	_, route := DeriveTelemetry(trackData(gpsRecords(9.98, 0.00005, 800)), homeZones, false)
	if len(route) != maxRoutePoints {
		t.Fatalf("route length %d, want the whole route downsampled", len(route))
	}
	if d := minDistToHome(route); d > HomeZoneRadiusM {
		t.Errorf("the pass by home must be kept; closest point %.0f m", d)
	}
	if route[0][0] != round(9.98, routePrecision) {
		t.Error("the start must be untouched")
	}
}

func TestFarRouteUntouched(t *testing.T) {
	recs := gpsRecords(10.5, 0.00002, 300)
	_, route := DeriveTelemetry(trackData(recs), homeZones, false)
	if len(route) != maxRoutePoints || route[0][0] != round(10.5, routePrecision) {
		t.Errorf("a route far from home must be untouched, got %d points", len(route))
	}
	if routeHidden(trackData(recs), homeZones, false) {
		t.Error("a far route is not hidden")
	}
}

func TestEndTrimmed(t *testing.T) {
	// Starts ~6.6 km south, runs north and ends at home.
	pts := toPts(gpsRecords(9.94, 0.00002, 3000))
	got := homeZones.trimTrack(pts)
	if len(got) == 0 || len(got) == len(pts) {
		t.Fatalf("end must be trimmed, kept %d of %d", len(got), len(pts))
	}
	if d := haversineM(got[len(got)-1][0], got[len(got)-1][1], 10.0, 10.0); d <= HomeZoneRadiusM || d > HomeZoneRadiusM+5 {
		t.Errorf("last kept point is %.1f m from home; want just outside the zone", d)
	}
	if got[0][0] != pts[0][0] {
		t.Error("the far start must be kept")
	}
}

func TestRouteHiddenFlag(t *testing.T) {
	data := trackData(gpsRecords(10.0, 0.00001, 300))
	sc := &pbactivity.ShowcasedActivity{ShowcaseId: "a", Title: "Morning Run", ActivityData: data}
	b, err := BuildActivity(sc, &pbactivity.ShowcaseProfileEntry{ShowcaseId: "a"}, fixedNow, homeZones, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"routePoints":[]`) || !strings.Contains(string(b), `"routeHidden":true`) {
		t.Error("a loop inside the zone must give routePoints [] and routeHidden true")
	}
	far := &pbactivity.ShowcasedActivity{ShowcaseId: "a", Title: "Morning Run", ActivityData: trackData(gpsRecords(10.5, 0.00002, 300))}
	b, err = BuildActivity(far, &pbactivity.ShowcaseProfileEntry{ShowcaseId: "a"}, fixedNow, homeZones, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "routeHidden") {
		t.Error("a published route must not carry routeHidden")
	}
	if routeHidden(trackData(nil), homeZones, false) {
		t.Error("an activity with no GPS is not hidden")
	}
}

func TestParkrunStaysWhole(t *testing.T) {
	// A parkrun that starts and ends inside the home zone is published in full.
	sc := distSC("Home parkrun #1", 5010, gpsRecords(10.0, 0.00001, 300))
	b, err := BuildActivity(sc, &pbactivity.ShowcaseProfileEntry{ShowcaseId: "x"}, fixedNow, homeZones, nil)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		RoutePoints [][]float64 `json:"routePoints"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.RoutePoints) != maxRoutePoints || strings.Contains(string(b), "routeHidden") {
		t.Errorf("a parkrun is kept whole, got %d points", len(out.RoutePoints))
	}
}

func toPts(recs []*pbactivity.Record) [][]float64 {
	var pts [][]float64
	for _, r := range recs {
		pts = append(pts, []float64{r.PositionLat, r.PositionLong})
	}
	return pts
}

func TestRaceWhole(t *testing.T) {
	recs := gpsRecords(10.0, 0.00001, 300) // entirely inside the home zone
	_, route := DeriveTelemetry(trackData(recs), homeZones, true)
	if len(route) != maxRoutePoints {
		t.Errorf("a race route is published untrimmed (downsampled only), got %d points", len(route))
	}
}

func TestMissingZonesFailsClosed(t *testing.T) {
	_, route := DeriveTelemetry(trackData(gpsRecords(10.5, 0.00002, 300)), nil, false)
	if route == nil || len(route) != 0 {
		t.Errorf("no zones must give [], got %v", route)
	}
	_, route = DeriveTelemetry(trackData(gpsRecords(10.5, 0.00002, 300)), nil, true)
	if len(route) != maxRoutePoints {
		t.Errorf("whole activities are still published without zones, got %d", len(route))
	}
	b, err := BuildActivity(&pbactivity.ShowcasedActivity{
		ShowcaseId: "a", Title: "Morning Run", ActivityData: trackData(gpsRecords(10.5, 0.00002, 300)),
		Enrichments: &pbactivity.ActivityEnrichments{Location: &pbactivity.LocationSummary{LocationName: "X", Latitude: 10.5, Longitude: 10.0}},
	}, &pbactivity.ShowcaseProfileEntry{ShowcaseId: "a"}, fixedNow, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"routePoints":[]`) {
		t.Error("routePoints must be [] in the file")
	}
	if strings.Contains(string(b), `"latitude":10.5`) {
		t.Error("location coordinates must not be published when zones are unavailable")
	}
}

func TestLocationInZoneDropped(t *testing.T) {
	sc := &pbactivity.ShowcasedActivity{
		ShowcaseId: "a", Title: "Morning Run",
		Enrichments: &pbactivity.ActivityEnrichments{Location: &pbactivity.LocationSummary{LocationName: "Home Town", Latitude: 10.001, Longitude: 10.0}},
	}
	b, err := BuildActivity(sc, &pbactivity.ShowcaseProfileEntry{ShowcaseId: "a"}, fixedNow, homeZones, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "Home Town") {
		t.Error("a location inside a zone must be dropped")
	}
	if sc.Enrichments.Location == nil {
		t.Error("the caller's message must be untouched")
	}
}

func TestLoadZones(t *testing.T) {
	dir := t.TempDir()
	places := filepath.Join(dir, "places.json")
	write := func(p, s string) {
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := LoadZones(places); err == nil {
		t.Error("missing places.json must be an error")
	}
	write(places, `{"homes":[{"lat":10.0,"lon":10.0,"name":"x"}]}`)
	z, err := LoadZones(places)
	if err != nil || !z.Contains(10.004, 10.0) || z.Contains(10.006, 10.0) {
		t.Fatalf("homes only, 500 m: %v", err)
	}
	write(places, `{"homes":[]}`)
	if _, err := LoadZones(places); err == nil {
		t.Error("no homes must be an error")
	}
	write(places, `{not json`)
	if _, err := LoadZones(places); err == nil || strings.Contains(err.Error(), dir) {
		t.Errorf("error must exist and not leak paths/content: %v", err)
	}
}

func TestDepsZonesFailClosedLogsOnce(t *testing.T) {
	var lines []string
	d := Deps{Logf: func(f string, a ...any) { lines = append(lines, f) }}
	if d.zones() != nil || len(lines) != 1 {
		t.Errorf("nil source: lines=%v", lines)
	}
	lines = nil
	d.Zones = ZonesFromEnv(func(k string) string { return filepath.Join(t.TempDir(), "absent.json") })
	if d.zones() != nil || len(lines) != 1 {
		t.Errorf("missing file: lines=%v", lines)
	}
}

func distSC(title string, meters float64, recs []*pbactivity.Record) *pbactivity.ShowcasedActivity {
	return &pbactivity.ShowcasedActivity{ShowcaseId: "x", Title: title, ActivityData: &pbactivity.StandardizedActivity{
		Name: title, Sessions: []*pbactivity.Session{{TotalDistance: meters, Laps: []*pbactivity.Lap{{Records: recs}}}}}}
}

func TestIsWhole(t *testing.T) {
	cases := []struct {
		name string
		sc   *pbactivity.ShowcasedActivity
		want bool
	}{
		{"real parkrun", distSC("Colwick parkrun #87", 5010, nil), true},
		{"parkrun at 4.8 km", distSC("Parkrun", 4800, nil), true},
		{"run to parkrun from home", distSC("Run to parkrun", 9200, nil), false},
		{"parkrun plus jog home", distSC("parkrun + jog home", 7800, nil), false},
		{"parkrun title but 2 km", distSC("parkrun warm-up", 2000, nil), false},
		{"parkrun tag alone", func() *pbactivity.ShowcasedActivity {
			s := distSC("Morning Run", 5000, nil)
			s.Tags = []string{"parkrun"}
			return s
		}(), false},
		{"hyrox training, no race result", showcase("Hyrox training", "", nil, 3000), false},
		{"hyrox race with splits", showcase("Hyrox Birmingham - Open Men's Doubles", roxfitDoubles, hrRecords(4529, func(int) int32 { return 160 }), 4529), true},
		{"easy run", distSC("Morning Run", 5000, nil), false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		if got := IsWhole(c.sc); got != c.want {
			t.Errorf("%s: IsWhole = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestRunToParkrunFromHomeIsTrimmed(t *testing.T) {
	// 9 km out of the front door, titled "parkrun": not whole, so the start is cut.
	sc := distSC("Run to parkrun", 9000, gpsRecords(10.0, 0.00003, 3000))
	b, err := BuildActivity(sc, &pbactivity.ShowcaseProfileEntry{ShowcaseId: "x"}, fixedNow, homeZones, nil)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		RoutePoints [][]float64 `json:"routePoints"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.RoutePoints) == 0 || minDistToHome(out.RoutePoints) <= HomeZoneRadiusM {
		t.Errorf("route must be non-empty and outside the zone (n=%d)", len(out.RoutePoints))
	}
}

func TestThumbnailTrack(t *testing.T) {
	data := trackData(gpsRecords(10.0, 0.00002, 3000))
	if got := ThumbnailTrack(data, nil, false); got != nil {
		t.Error("non-whole without zones must give no track")
	}
	got := ThumbnailTrack(data, homeZones, false)
	if len(got) < minRoutePoints || minDistToHome(got) <= HomeZoneRadiusM {
		t.Errorf("trimmed track must be outside the zone (n=%d)", len(got))
	}
	if got := ThumbnailTrack(trackData(gpsRecords(10.0, 0.00001, 300)), homeZones, false); got != nil {
		t.Error("a track entirely inside the zone gives no track (placeholder)")
	}
	if got := ThumbnailTrack(trackData(gpsRecords(10.0, 0.00001, 300)), nil, true); len(got) != 300 {
		t.Errorf("whole is kept in full, got %d", len(got))
	}
}

func TestThumbnailObject(t *testing.T) {
	cases := map[string]string{
		"https://assets.fitglue.tech/pipe-1/route-thumbnail.svg":                "pipe-1/route-thumbnail.svg",
		"https://storage.googleapis.com/some-bucket/pipe-1/route-thumbnail.svg": "pipe-1/route-thumbnail.svg",
		"https://assets.fitglue.tech/photos/a.jpg":                              "",
		"https://assets.fitglue.tech/../x/route-thumbnail.svg":                  "",
		"": "",
	}
	for in, want := range cases {
		got, ok := ThumbnailObject(in)
		if got != want || ok != (want != "") {
			t.Errorf("%q -> %q,%v want %q", in, got, ok, want)
		}
	}
}

func TestRewriteThumbnails(t *testing.T) {
	mk := func(id, title string, meters float64, recs []*pbactivity.Record) (*pbactivity.ShowcaseProfileEntry, *pbactivity.ShowcasedActivity) {
		sc := distSC(title, meters, recs)
		sc.ShowcaseId = id
		return &pbactivity.ShowcaseProfileEntry{ShowcaseId: id, Title: title, RouteThumbnailUrl: "https://assets.invalid/" + id + "/route-thumbnail.svg"}, sc
	}
	e1, s1 := mk("a", "Morning Run", 6000, gpsRecords(10.0, 0.00002, 3000))    // trimmed
	e2, s2 := mk("b", "Colwick parkrun", 5000, gpsRecords(10.0, 0.00001, 300)) // whole
	e3, s3 := mk("c", "Short loop", 400, gpsRecords(10.0, 0.00001, 300))       // placeholder
	e4, _ := mk("d", "Expired", 0, nil)                                        // no showcase: placeholder
	noThumb := &pbactivity.ShowcaseProfileEntry{ShowcaseId: "e"}
	src := &fakeSource{profile: testProfile(), entries: []*pbactivity.ShowcaseProfileEntry{e1, e2, e3, e4, noThumb},
		showcases: map[string]*pbactivity.ShowcasedActivity{"a": s1, "b": s2, "c": s3}, errs: map[string]error{}}
	var mu sync.Mutex
	got := map[string]int{} // object -> number of track points handed to the renderer
	deps := ThumbsDeps{
		Source: src, Zones: StaticZones(homeZones),
		Render: func(track [][]float64) []byte {
			if track != nil && len(track) > 0 && ThumbsTestHook != nil {
				ThumbsTestHook(track)
			}
			return []byte{byte(len(track) % 251)}
		},
		Put: func(_ context.Context, obj string, svg []byte) error {
			mu.Lock()
			defer mu.Unlock()
			got[obj] = int(svg[0])
			return nil
		},
	}
	var rendered [][][]float64
	ThumbsTestHook = func(tr [][]float64) { mu.Lock(); rendered = append(rendered, tr); mu.Unlock() }
	defer func() { ThumbsTestHook = nil }()
	res, err := RewriteThumbnails(context.Background(), deps, "james")
	if err != nil {
		t.Fatal(err)
	}
	if res.Trimmed != 1 || res.Whole != 1 || res.Placeholder != 2 || res.Skipped != 1 {
		t.Errorf("result = %+v", res)
	}
	if len(got) != 4 {
		t.Errorf("objects written = %v", got)
	}
	for _, tr := range rendered {
		if len(tr) != 300 && minDistToHome(tr) <= HomeZoneRadiusM {
			t.Error("a non-whole track handed to the renderer lies inside the zone")
		}
	}

	// Fail closed: no zones, nothing non-whole is rendered.
	rendered = nil
	deps.Zones = func() (*Zones, error) { return nil, os.ErrNotExist }
	res, err = RewriteThumbnails(context.Background(), deps, "james")
	if err != nil || res.Trimmed != 0 || res.Whole != 1 || res.Placeholder != 3 {
		t.Errorf("without zones: %+v %v", res, err)
	}
}

func TestPruneDropsSkippedEntriesFiles(t *testing.T) {
	src := newFakeSource()
	src.showcases["push-day-2026-10-13"].ExpiresAt = ts(fixedNow.Add(-time.Hour))
	w := newMemWriter()
	_ = w.Write(context.Background(), "activity/push-day-2026-10-13.json", []byte(`{"old":"untrimmed"}`))
	res, err := ExportAll(context.Background(), testDeps(src, w), "james", Options{Prune: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := w.files["activity/push-day-2026-10-13.json"]; ok {
		t.Error("the expired activity's old file must be gone")
	}
	if res.Skipped != 1 {
		t.Errorf("result = %+v", res)
	}
}

func TestSingleExportDeletesStaleFileWhenNoLongerExportable(t *testing.T) {
	src := newFakeSource()
	delete(src.showcases, "push-day-2026-10-13")
	w := newMemWriter()
	_ = w.Write(context.Background(), "activity/push-day-2026-10-13.json", []byte(`{"old":"untrimmed"}`))
	_ = ExportActivity(context.Background(), testDeps(src, w), "james", "push-day-2026-10-13")
	if _, ok := w.files["activity/push-day-2026-10-13.json"]; ok {
		t.Error("stale file must be deleted")
	}
}

// ThumbsTestHook lets a test observe the tracks handed to the renderer.
var ThumbsTestHook func([][]float64)
