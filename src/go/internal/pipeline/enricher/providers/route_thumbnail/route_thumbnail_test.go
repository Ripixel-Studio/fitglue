package route_thumbnail

import (
	"github.com/fitglue/server/src/go/internal/activity/siteexport"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	"math"
	"strings"
	"testing"
)

func TestPerpendicularDistance_PointToLine(t *testing.T) {
	// Equilateral case: point on the line should have distance 0
	lineStart := GPSPoint{Lat: 0, Long: 0}
	lineEnd := GPSPoint{Lat: 0, Long: 10}
	pointOnLine := GPSPoint{Lat: 0, Long: 5}

	dist := perpendicularDistance(pointOnLine, lineStart, lineEnd)
	if dist > 1e-9 {
		t.Errorf("perpendicularDistance on line = %.10f, want ~0", dist)
	}
}

func TestPerpendicularDistance_PointOffLine(t *testing.T) {
	// Point directly above midpoint
	lineStart := GPSPoint{Lat: 0, Long: 0}
	lineEnd := GPSPoint{Lat: 0, Long: 10}
	pointAbove := GPSPoint{Lat: 5, Long: 5}

	dist := perpendicularDistance(pointAbove, lineStart, lineEnd)
	// Distance should be approximately 5 (the lat offset)
	if math.Abs(dist-5.0) > 0.01 {
		t.Errorf("perpendicularDistance above line = %.4f, want ~5.0", dist)
	}
}

func TestPerpendicularDistance_DegenerateLineIsPoint(t *testing.T) {
	// When line start == line end, should return distance from point to that point
	lineStart := GPSPoint{Lat: 0, Long: 0}
	lineEnd := GPSPoint{Lat: 0, Long: 0}
	point := GPSPoint{Lat: 3, Long: 4}

	dist := perpendicularDistance(point, lineStart, lineEnd)
	// Distance from (3,4) to (0,0) = 5.0
	if math.Abs(dist-5.0) > 0.01 {
		t.Errorf("perpendicularDistance to degenerate line = %.4f, want ~5.0", dist)
	}
}

func TestSimplifyRoute_FewPoints(t *testing.T) {
	// Fewer than 3 points: returned unchanged
	points := []GPSPoint{
		{Lat: 0, Long: 0},
		{Lat: 1, Long: 1},
	}
	result := simplifyRoute(points, 0.001)
	if len(result) != 2 {
		t.Errorf("simplifyRoute with 2 points returned %d points, want 2", len(result))
	}
}

func TestSimplifyRoute_CollinearPoints(t *testing.T) {
	// All points on same line → should simplify to 2 endpoints
	points := []GPSPoint{
		{Lat: 0, Long: 0},
		{Lat: 0, Long: 1},
		{Lat: 0, Long: 2},
		{Lat: 0, Long: 3},
		{Lat: 0, Long: 4},
	}
	result := simplifyRoute(points, 0.001)
	if len(result) != 2 {
		t.Errorf("simplifyRoute with collinear points returned %d points, want 2", len(result))
	}
}

func TestSimplifyRoute_SignificantDeviation(t *testing.T) {
	// Points that deviate significantly, tolerance is small → should keep more points
	points := []GPSPoint{
		{Lat: 0, Long: 0},
		{Lat: 10, Long: 1}, // big deviation
		{Lat: 0, Long: 2},
	}
	result := simplifyRoute(points, 0.001)
	// With big deviation, all points should be kept
	if len(result) < 3 {
		t.Errorf("simplifyRoute with deviating point returned %d, want >= 3", len(result))
	}
}

func TestGenerateRouteSVG_Basic(t *testing.T) {
	// A simple square route
	points := []GPSPoint{
		{Lat: 0, Long: 0},
		{Lat: 0, Long: 1},
		{Lat: 1, Long: 1},
		{Lat: 1, Long: 0},
		{Lat: 0, Long: 0},
	}
	svg := generateRouteSVG(points)

	if svg == "" {
		t.Fatal("generateRouteSVG returned empty string")
	}
	if !strings.Contains(svg, "<svg") {
		t.Errorf("generateRouteSVG output doesn't contain '<svg': %q", svg[:min(100, len(svg))])
	}
	if !strings.Contains(svg, "M") {
		t.Errorf("generateRouteSVG output missing path M command: %q", svg[:min(100, len(svg))])
	}
}

func TestGenerateRouteSVG_TwoPoints(t *testing.T) {
	// Minimum case: 2 points
	points := []GPSPoint{
		{Lat: 51.5, Long: -0.1},
		{Lat: 51.6, Long: -0.2},
	}
	svg := generateRouteSVG(points)
	if svg == "" {
		t.Fatal("generateRouteSVG returned empty string for 2 points")
	}
	if !strings.Contains(svg, "<svg") {
		t.Error("Expected SVG markup in output")
	}
}

func TestRouteThumbnailProviderName(t *testing.T) {
	p := NewRouteThumbnailProvider()
	if p.Name() != "route_thumbnail" {
		t.Errorf("unexpected name: %s", p.Name())
	}
}

func TestRouteThumbnailProviderType(t *testing.T) {
	p := NewRouteThumbnailProvider()
	if p.ProviderType() == 0 {
		t.Error("expected non-zero provider type")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Invented coordinates around (10.0, 10.0); ~0.00002 deg of latitude is ~2.2 m.
func walkNorth(n int) *pbactivity.StandardizedActivity {
	recs := make([]*pbactivity.Record, n)
	for i := range recs {
		recs[i] = &pbactivity.Record{PositionLat: 10.0 + float64(i)*0.00002, PositionLong: 10.0}
	}
	return &pbactivity.StandardizedActivity{Name: "Morning Run", Sessions: []*pbactivity.Session{{Laps: []*pbactivity.Lap{{Records: recs}}}}}
}

func TestThumbnailForActivity_FailsClosedWithoutZones(t *testing.T) {
	got := string(ThumbnailForActivity(walkNorth(3000), nil))
	if got != PlaceholderSVG {
		t.Error("a non-whole route must not be drawn without zones")
	}
}

func TestThumbnailForActivity_TrimmedFromHome(t *testing.T) {
	zones := siteexport.NewZones([3]float64{10.0, 10.0, siteexport.HomeZoneRadiusM})
	// North ~3 km out of the zone, then a 3 km dogleg east: the part inside the zone
	// changes the drawn shape (the drawing is normalised to its bounding box).
	act := walkNorth(1500)
	recs := act.Sessions[0].Laps[0].Records
	for i := 1; i <= 1500; i++ {
		recs = append(recs, &pbactivity.Record{PositionLat: 10.03, PositionLong: 10.0 + float64(i)*0.00002})
	}
	act.Sessions[0].Laps[0].Records = recs
	trimmed := string(ThumbnailForActivity(act, zones))
	if trimmed == PlaceholderSVG {
		t.Fatal("a route leaving the zone should still be drawn")
	}
	full := string(RenderTrack(siteexport.ThumbnailTrack(act, nil, true)))
	if trimmed == full {
		t.Error("the trimmed thumbnail must differ from the untrimmed one")
	}
}

func TestThumbnailForActivity_LoopInsideZoneIsPlaceholder(t *testing.T) {
	zones := siteexport.NewZones([3]float64{10.0, 10.0, siteexport.HomeZoneRadiusM})
	if string(ThumbnailForActivity(walkNorth(300), zones)) != PlaceholderSVG {
		t.Error("a loop inside the zone must give the placeholder")
	}
}

func TestRenderTrack_NilIsPlaceholder(t *testing.T) {
	if string(RenderTrack(nil)) != PlaceholderSVG || !strings.Contains(PlaceholderSVG, "<svg") {
		t.Error("nil track must render the placeholder")
	}
}
