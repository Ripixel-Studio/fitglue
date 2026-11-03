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

// BuildLinks derives the links for sc: a Strava-sourced activity links its own
// Strava page; a Hevy-sourced one links its Hevy workout and, when stravaID is
// set (the pipeline's successful Strava upload), the Strava copy. It returns nil
// when there is nothing valid to link.
func BuildLinks(sc *pbactivity.ShowcasedActivity, stravaID string) *Links {
	if sc == nil {
		return nil
	}
	src := sc.GetSource()
	if src == pbactivity.ActivitySource_SOURCE_UNSPECIFIED {
		src = sc.GetActivityData().GetSource()
	}
	ext := sc.GetActivityData().GetExternalId()
	var l Links
	switch src {
	case pbactivity.ActivitySource_SOURCE_STRAVA:
		if stravaIDRe.MatchString(ext) {
			l.Strava = "https://www.strava.com/activities/" + ext
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
