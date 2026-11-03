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

	"google.golang.org/protobuf/proto"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
)

// Privacy zones (life hub spec §5b): the public site must never show where James
// lives. Every coordinate the export publishes is trimmed against a set of circular
// zones at the source, so the bucket never holds an untrimmed route.
//
// The zones come from two files on the box, never committed and never in the bucket:
//
//	places.json         {"homes":[{"lat":..,"lon":..}]}            each home is an 800 m zone
//	privacy-zones.json  {"zones":[{"lat":..,"lng":..,"radiusM":..}]}  learned spots (optional)
//
// places.json missing or unreadable fails closed: routePoints is [] for every
// activity that is not kept whole.

// Env var names for the zone files; the defaults are the box paths.
const (
	EnvPlacesFile = "SITE_EXPORT_PLACES_FILE"
	EnvZonesFile  = "SITE_EXPORT_ZONES_FILE"

	// HomeZoneRadiusM is the radius of the zone around each home in places.json.
	HomeZoneRadiusM = 800.0
	// minSegmentPoints: a stretch of route shorter than this after trimming is dropped.
	minSegmentPoints = 5
)

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

// ZonesFromEnv reads SITE_EXPORT_PLACES_FILE / SITE_EXPORT_ZONES_FILE through getenv,
// defaulting to ~/.jarvis/home/places.json and ~/.jarvis/home/privacy-zones.json.
func ZonesFromEnv(getenv func(string) string) ZoneSource {
	dir := ""
	if home, err := os.UserHomeDir(); err == nil {
		dir = filepath.Join(home, ".jarvis", "home")
	}
	places := strings.TrimSpace(getenv(EnvPlacesFile))
	if places == "" {
		places = filepath.Join(dir, "places.json")
	}
	learned := strings.TrimSpace(getenv(EnvZonesFile))
	if learned == "" {
		learned = filepath.Join(dir, "privacy-zones.json")
	}
	return func() (*Zones, error) { return LoadZones(places, learned) }
}

// LoadZones reads the homes (required: no homes is an error, never an empty set) and
// the learned zones (optional when the file does not exist; present but corrupt is an
// error). Errors never include coordinates.
func LoadZones(placesPath, learnedPath string) (*Zones, error) {
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
	lb, err := os.ReadFile(learnedPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return z, nil
	case err != nil:
		return nil, errors.New("privacy zones file unreadable")
	}
	var learned struct {
		Zones []struct {
			Lat     *float64 `json:"lat"`
			Lng     *float64 `json:"lng"`
			RadiusM *float64 `json:"radiusM"`
		} `json:"zones"`
	}
	if err := json.Unmarshal(lb, &learned); err != nil {
		return nil, errors.New("privacy zones file is not valid JSON")
	}
	for _, l := range learned.Zones {
		if l.Lat == nil || l.Lng == nil || l.RadiusM == nil {
			return nil, errors.New("privacy zones file has an incomplete zone")
		}
		z.zones = append(z.zones, zone{*l.Lat, *l.Lng, *l.RadiusM})
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

// trimTrack drops every point inside a zone, splits the track where points were
// dropped, discards segments shorter than minSegmentPoints and returns the longest
// remaining one (never joining across a zone). nil when nothing survives.
func (z *Zones) trimTrack(pts [][]float64) [][]float64 {
	var best, cur [][]float64
	flush := func() {
		if len(cur) >= minSegmentPoints && len(cur) > len(best) {
			best = cur
		}
		cur = nil
	}
	for _, p := range pts {
		if z.Contains(p[0], p[1]) {
			flush()
			continue
		}
		cur = append(cur, p)
	}
	flush()
	return best
}

var parkrunRE = regexp.MustCompile(`(?i)parkrun`)

// keptWhole reports whether an activity's route is published untrimmed: a race
// (Hyrox, the one race marker the export has) or a parkrun.
func keptWhole(sc *pbactivity.ShowcasedActivity) bool {
	if sc == nil {
		return false
	}
	if isHyroxActivity(sc) || parkrunRE.MatchString(sc.Title) {
		return true
	}
	if sc.ActivityData != nil && parkrunRE.MatchString(sc.ActivityData.Name) {
		return true
	}
	for _, t := range sc.Tags {
		if parkrunRE.MatchString(t) {
			return true
		}
	}
	return false
}

// titleKeptWhole is keptWhole for the index and roundups, which carry a title only.
func titleKeptWhole(title string) bool {
	t := strings.ToLower(title)
	return strings.Contains(t, "hyrox") || parkrunRE.MatchString(title)
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

// scrubEntry blanks the route thumbnail of an entry that is not kept whole. The
// thumbnail is an image of the full track, which cannot be trimmed; the site draws
// its maps from the trimmed routePoints instead. Works on a copy.
func scrubEntry(e *pbactivity.ShowcaseProfileEntry) *pbactivity.ShowcaseProfileEntry {
	if e == nil || e.RouteThumbnailUrl == "" || titleKeptWhole(e.Title) {
		return e
	}
	c := proto.Clone(e).(*pbactivity.ShowcaseProfileEntry)
	c.RouteThumbnailUrl = ""
	return c
}

// scrubEnrichments
// applies the zones to the location. Mutates the (cloned) enrichments.
func scrubEnrichments(en *pbactivity.ActivityEnrichments, zones *Zones) *pbactivity.ActivityEnrichments {
	if en == nil {
		return nil
	}
	en.Location = scrubLocation(en.Location, zones)
	return en
}

// scrubRoundup drops the roundup's route thumbnails of activities not kept whole.
func scrubRoundup(r *pbactivity.ShowcaseRoundup) *pbactivity.ShowcaseRoundup {
	if r == nil || len(r.Routes) == 0 {
		return r
	}
	c := proto.Clone(r).(*pbactivity.ShowcaseRoundup)
	kept := c.Routes[:0]
	for _, rt := range c.Routes {
		if titleKeptWhole(rt.ActivityTitle) {
			kept = append(kept, rt)
		}
	}
	c.Routes = kept
	return c
}
