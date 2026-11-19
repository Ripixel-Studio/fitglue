package stravaexercise

import (
	"github.com/fitglue/server/src/go/pkg/activitytag"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
)

// HybridRacing is Strava's sport_type for Hyrox and other hybrid racing (launched
// 2026-11-19; not in the API docs yet, accepted by PUT /activities and /uploads).
const HybridRacing = "HybridRacing"

// hybridGymTypes are the internal types a hybrid session is filed under (the
// classifier keeps Hyrox classes as Crossfit or HIIT, PT as weight training).
var hybridGymTypes = map[pbactivity.ActivityType]bool{
	pbactivity.ActivityType_ACTIVITY_TYPE_UNSPECIFIED:                      true,
	pbactivity.ActivityType_ACTIVITY_TYPE_CROSSFIT:                         true,
	pbactivity.ActivityType_ACTIVITY_TYPE_HIGH_INTENSITY_INTERVAL_TRAINING: true,
	pbactivity.ActivityType_ACTIVITY_TYPE_WEIGHT_TRAINING:                  true,
	pbactivity.ActivityType_ACTIVITY_TYPE_WORKOUT:                          true,
}

var runTypes = map[pbactivity.ActivityType]bool{
	pbactivity.ActivityType_ACTIVITY_TYPE_RUN:         true,
	pbactivity.ActivityType_ACTIVITY_TYPE_TRAIL_RUN:   true,
	pbactivity.ActivityType_ACTIVITY_TYPE_VIRTUAL_RUN: true,
}

// SportType is the Strava sport_type for an activity: base (the type's own Strava
// name) unless the site tag is hybrid and the activity is a gym session, or a run
// carrying Hyrox race splits — then HybridRacing. A hybrid-tagged ride ("Hybrid Spin")
// or walk keeps its own type.
func SportType(t pbactivity.ActivityType, base, tag string, a *pbactivity.StandardizedActivity) string {
	if activitytag.Normalize(tag) != activitytag.Hybrid {
		return base
	}
	if hybridGymTypes[t] {
		return HybridRacing
	}
	if runTypes[t] && len(a.GetHybridRaceSummary().GetSegments()) > 0 {
		return HybridRacing
	}
	return base
}
