package strava

import (
	"context"
	"testing"

	"github.com/fitglue/server/src/go/pkg/bootstrap"
	"github.com/fitglue/server/src/go/pkg/testing/mocks"
	"github.com/fitglue/server/src/go/pkg/title"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	pbevents "github.com/fitglue/server/src/go/pkg/types/pb/models/events"
	pbplugin "github.com/fitglue/server/src/go/pkg/types/pb/models/plugin"
	"github.com/stretchr/testify/assert"
)

const (
	roxfitTitle   = "HYROX - Birmingham - 01:15:29"
	calendarTitle = "Hyrox Birmingham - Open Men's Doubles"
	doublesID     = "20751310619"
)

// recordDB keeps strava:<id> loop-prevention records in memory.
func recordDB(records map[string]*pbactivity.UploadedActivityRecord) *mocks.MockDatabase {
	return &mocks.MockDatabase{
		GetUploadedActivityFunc: func(ctx context.Context, userId string, d pbplugin.DestinationType, id string) (*pbactivity.UploadedActivityRecord, error) {
			return records[id], nil
		},
		SetUploadedActivityFunc: func(ctx context.Context, userId string, r *pbactivity.UploadedActivityRecord) error {
			records[r.DestinationId] = r
			return nil
		},
	}
}

func doublesPayload() *pbevents.ActivityPayload {
	return &pbevents.ActivityPayload{
		UserId: "u1",
		Metadata: map[string]string{
			"activity_name":           calendarTitle,
			title.MetadataSourceTitle: roxfitTitle, // the original payload a repost replays
		},
		StandardizedActivity: &pbactivity.StandardizedActivity{ExternalId: doublesID},
	}
}

// The repost of the Birmingham doubles (Strava 20751310619): FitGlue had written the
// description on the first run but kept ROXFIT's title, so the record holds no written
// title. The repost must keep ROXFIT's title — it replaced it with the calendar name.
func TestSameSourceTitle_RepostKeepsThirdPartyTitle(t *testing.T) {
	records := map[string]*pbactivity.UploadedActivityRecord{doublesID: {DestinationId: doublesID}}
	u := New(&bootstrap.Service{DB: recordDB(records)})

	got := u.sameSourceTitle(context.Background(), doublesPayload(), doublesID, roxfitTitle, true)
	assert.Equal(t, roxfitTitle, got)
}

// After the buggy repost the activity carries the calendar name and the record no
// written title: the next repost gives ROXFIT's title back and records it as written.
func TestSameSourceTitle_RepostRestoresTitleTakenByEarlierRepost(t *testing.T) {
	records := map[string]*pbactivity.UploadedActivityRecord{doublesID: {DestinationId: doublesID}}
	u := New(&bootstrap.Service{DB: recordDB(records)})
	p := doublesPayload()

	got := u.sameSourceTitle(context.Background(), p, doublesID, calendarTitle, true)
	assert.Equal(t, roxfitTitle, got)

	u.recordWrittenTitle(context.Background(), p, doublesID, got)
	assert.Equal(t, roxfitTitle, records[doublesID].WrittenTitle)
	// And from then on the restored title is FitGlue's own write, kept on every repost.
	assert.Equal(t, roxfitTitle, u.sameSourceTitle(context.Background(), p, doublesID, roxfitTitle, true))
}

// A title FitGlue did write (a Strava default it renamed) is still FitGlue's own and
// takes the new proposal.
func TestSameSourceTitle_ReplacesFitGluesOwnWrittenTitle(t *testing.T) {
	records := map[string]*pbactivity.UploadedActivityRecord{"9": {DestinationId: "9", WrittenTitle: "Newark parkrun #41"}}
	u := New(&bootstrap.Service{DB: recordDB(records)})
	p := &pbevents.ActivityPayload{UserId: "u1", Metadata: map[string]string{"activity_name": "Newark parkrun #42", title.MetadataSourceTitle: "Morning Run"}}

	assert.Equal(t, "Newark parkrun #42", u.sameSourceTitle(context.Background(), p, "9", "Newark parkrun #41", true))
}
