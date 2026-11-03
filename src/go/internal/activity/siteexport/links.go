package siteexport

import (
	"regexp"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
)

// Links are the public pages an activity lives on elsewhere (contract C4). Only
// the two URLs below can be built, from ids that match strict patterns; nothing
// here carries a coordinate or a place.
type Links struct {
	Strava string `json:"strava,omitempty"`
	Hevy   string `json:"hevy,omitempty"`
}

var (
	stravaIDRe = regexp.MustCompile(`^[0-9]+$`)
	hevyIDRe   = regexp.MustCompile(`^[A-Za-z0-9-]+$`)
)

// linkSource is the activity's source, from the showcase or else its activity data.
func linkSource(sc *pbactivity.ShowcasedActivity) pbactivity.ActivitySource {
	if src := sc.GetSource(); src != pbactivity.ActivitySource_SOURCE_UNSPECIFIED {
		return src
	}
	return sc.GetActivityData().GetSource()
}

// BuildLinks derives the links for sc. stravaID is the Strava page to link, already
// confirmed visible to everyone by Source.StravaID ("" when private, followers-only
// or unknown): the activity's own id for a Strava source, the pipeline's Strava
// upload for a Hevy source, which also links its Hevy workout. It returns nil when
// there is nothing valid to link.
func BuildLinks(sc *pbactivity.ShowcasedActivity, stravaID string) *Links {
	if sc == nil {
		return nil
	}
	src := linkSource(sc)
	ext := sc.GetActivityData().GetExternalId()
	var l Links
	switch src {
	case pbactivity.ActivitySource_SOURCE_STRAVA:
		if stravaIDRe.MatchString(stravaID) {
			l.Strava = "https://www.strava.com/activities/" + stravaID
		}
	case pbactivity.ActivitySource_SOURCE_HEVY:
		if hevyIDRe.MatchString(ext) {
			l.Hevy = "https://hevy.com/workout/" + ext
		}
		if stravaIDRe.MatchString(stravaID) {
			l.Strava = "https://www.strava.com/activities/" + stravaID
		}
	}
	if l == (Links{}) {
		return nil
	}
	return &l
}
