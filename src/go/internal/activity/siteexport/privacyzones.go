package siteexport

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
)

// Privacy zones (life hub spec, "Route privacy, revised"): the public site must never
// show where James lives, but like Strava it only hides the START and END of a route
// when they are within HomeZoneRadiusM of a home. Everything between is published,
// including mid-route passes near home. A route that never leaves the zone is hidden
// entirely (routePoints [] and routeHidden true).
//
// The zones are the homes in places.json ({"homes":[{"lat":..,"lon":..}]}), never
// committed and never in the bucket. places.json missing or unreadable fails closed:
// routePoints is [] and routeHidden true for every activity not kept whole.

// EnvPlacesFile names the env var overriding the places.json path.
const EnvPlacesFile = "SITE_EXPORT_PLACES_FILE"

// HomeZoneRadiusM is the radius of the zone around each home in places.json.
const HomeZoneRadiusM = 500.0

type zone struct {
	lat, lng, radiusM float64
}

// Zones is a loaded set of privacy zones. A nil *Zones means "not available":
// callers fail closed.
type Zones struct {
	zones []zone
}

// ZoneSource loads the zones, called once per activity built so edits to the
// files apply to the next export without a restart.
type ZoneSource func() (*Zones, error)

// NewZones builds a zone set from explicit circles (lat, lng, radiusM).
func NewZones(circles ...[3]float64) *Zones {
	z := &Zones{}
	for _, c := range circles {
		z.zones = append(z.zones, zone{c[0], c[1], c[2]})
	}
	return z
}

// StaticZones is a ZoneSource returning a fixed set (tests).
func StaticZones(z *Zones) ZoneSource { return func() (*Zones, error) { return z, nil } }

// ZonesFromEnv reads SITE_EXPORT_PLACES_FILE through getenv, defaulting to
// ~/.jarvis/home/places.json.
func ZonesFromEnv(getenv func(string) string) ZoneSource {
	places := strings.TrimSpace(getenv(EnvPlacesFile))
	if places == "" {
		if home, err := os.UserHomeDir(); err == nil {
			places = filepath.Join(home, ".jarvis", "home", "places.json")
		}
	}
	return func() (*Zones, error) { return LoadZones(places) }
}

// LoadZones reads the homes (required: no homes is an error, never an empty set).
// Errors never include coordinates.
func LoadZones(placesPath string) (*Zones, error) {
	b, err := os.ReadFile(placesPath)
	if err != nil {
		return nil, fmt.Errorf("places file unreadable: %w", errors.Unwrap(err))
	}
	var places struct {
		Homes []struct {
			Lat *float64 `json:"lat"`
			Lon *float64 `json:"lon"`
		} `json:"homes"`
	}
	if err := json.Unmarshal(b, &places); err != nil {
		return nil, errors.New("places file is not valid JSON")
	}
	z := &Zones{}
	for _, h := range places.Homes {
		if h.Lat == nil || h.Lon == nil {
			return nil, errors.New("places file has a home without lat/lon")
		}
		z.zones = append(z.zones, zone{*h.Lat, *h.Lon, HomeZoneRadiusM})
	}
	if len(z.zones) == 0 {
		return nil, errors.New("places file lists no homes")
	}
	return z, nil
}

// Contains reports whether the point is inside any zone.
func (z *Zones) Contains(lat, lng float64) bool {
	for _, c := range z.zones {
		if haversineM(lat, lng, c.lat, c.lng) <= c.radiusM {
			return true
		}
	}
	return false
}

func haversineM(lat1, lng1, lat2, lng2 float64) float64 {
	const earthM = 6371000.0
	rad := math.Pi / 180
	dLat := (lat2 - lat1) * rad
	dLng := (lng2 - lng1) * rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * earthM * math.Asin(math.Min(1, math.Sqrt(a)))
}

// trimTrack hides the start and end like Strava: it drops the leading points while
// they are inside a zone and the trailing points the same way, and keeps everything
// between as one polyline. Empty when the whole track stays inside the zones.
func (z *Zones) trimTrack(pts [][]float64) [][]float64 {
	start := 0
	for start < len(pts) && z.Contains(pts[start][0], pts[start][1]) {
		start++
	}
	if start == len(pts) {
		return nil
	}
	end := len(pts)
	for end > start && z.Contains(pts[end-1][0], pts[end-1][1]) {
		end--
	}
	return pts[start:end]
}

// routeHidden reports whether a non-whole activity's route was withheld for privacy:
// it has a GPS track, but zones are unavailable or the trimmed track is too short to
// publish. Whole activities and activities with no usable GPS are never hidden.
func routeHidden(data *pbactivity.StandardizedActivity, zones *Zones, whole bool) bool {
	if whole {
		return false
	}
	pts := trackPoints(trackRecords(data))
	if len(pts) < minRoutePoints {
		return false
	}
	return zones == nil || len(zones.trimTrack(pts)) < minRoutePoints
}

var parkrunRE = regexp.MustCompile(`(?i)parkrun`)

// Distance window of a real parkrun (5 km course, GPS slack).
const (
	parkrunMinM = 4800.0
	parkrunMaxM = 5300.0
)

// IsWhole is the one definition of "kept whole" for every public coordinate the
// export publishes (routePoints, route thumbnails, locations): the activity is a race
// (DeriveRace finds one) or a parkrun (parkrun in the title and 4.8-5.3 km). A bare
// "hyrox" or "parkrun" in a title, name or tag never makes an activity whole by itself:
// "Run to parkrun" and "Hyrox training" start at the front door.
func IsWhole(sc *pbactivity.ShowcasedActivity) bool {
	if sc == nil {
		return false
	}
	if DeriveRace(sc) != nil {
		return true
	}
	title := sc.Title
	if title == "" && sc.ActivityData != nil {
		title = sc.ActivityData.Name
	}
	if !parkrunRE.MatchString(title) {
		return false
	}
	d := activityDistanceM(sc.ActivityData)
	return d >= parkrunMinM && d <= parkrunMaxM
}

// activityDistanceM is the sessions' total distance, else the last cumulative record distance.
func activityDistanceM(data *pbactivity.StandardizedActivity) float64 {
	if data == nil {
		return 0
	}
	total, last := 0.0, 0.0
	for _, sess := range data.Sessions {
		total += sess.TotalDistance
		for _, lap := range sess.Laps {
			for _, r := range lap.Records {
				if r.Distance > last {
					last = r.Distance
				}
			}
		}
	}
	if total > 0 {
		return total
	}
	return last
}

// ThumbnailTrack is the full-resolution track a route thumbnail may be drawn from:
// the whole track for a whole activity, the track with its start and end
// trimmed otherwise, and nil (fail closed) for a non-whole activity without zones. Fewer than
// minRoutePoints points also gives nil: draw a placeholder.
func ThumbnailTrack(data *pbactivity.StandardizedActivity, zones *Zones, whole bool) [][]float64 {
	pts := trackPoints(trackRecords(data))
	if !whole {
		if zones == nil {
			return nil
		}
		pts = zones.trimTrack(pts)
	}
	if len(pts) < minRoutePoints {
		return nil
	}
	return pts
}

// scrubLocation applies the zones to a LocationSummary: unavailable zones drop its
// coordinates, a place inside a zone is dropped whole. Returns the value to keep.
func scrubLocation(loc *pbactivity.LocationSummary, zones *Zones) *pbactivity.LocationSummary {
	if loc == nil {
		return nil
	}
	if zones == nil {
		loc.Latitude, loc.Longitude = 0, 0
		return loc
	}
	if (loc.Latitude != 0 || loc.Longitude != 0) && zones.Contains(loc.Latitude, loc.Longitude) {
		return nil
	}
	return loc
}
