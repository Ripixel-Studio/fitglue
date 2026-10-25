package showcase

import (
	"context"
	"errors"
	"testing"

	"github.com/fitglue/server/src/go/pkg/bootstrap"
	"github.com/fitglue/server/src/go/pkg/testing/mocks"
	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	pbevents "github.com/fitglue/server/src/go/pkg/types/pb/models/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
)

func blobWith(t *testing.T, e *pbactivity.ActivityEnrichments, applied []string) []byte {
	t.Helper()
	out, err := protojson.Marshal(&pbevents.EnrichedActivityEvent{ActivityId: "act-1", Enrichments: e, AppliedEnrichments: applied})
	require.NoError(t, err)
	return out
}

func TestReadPriorEnrichments(t *testing.T) {
	logger := testLogger()

	t.Run("EmptyURI", func(t *testing.T) {
		u := New(&bootstrap.Service{Store: &mocks.MockBlobStore{}}, nil, "assets")
		assert.Nil(t, u.readPriorEnrichments(context.Background(), logger, ""))
	})

	t.Run("ReadError", func(t *testing.T) {
		store := &mocks.MockBlobStore{GetFunc: func(ctx context.Context, bucket, object string) ([]byte, error) {
			return nil, errors.New("gone")
		}}
		u := New(&bootstrap.Service{Store: store}, nil, "assets")
		assert.Nil(t, u.readPriorEnrichments(context.Background(), logger, "gs://b/o"))
	})

	t.Run("UnparseableBlob", func(t *testing.T) {
		store := &mocks.MockBlobStore{GetFunc: func(ctx context.Context, bucket, object string) ([]byte, error) {
			return []byte("not json"), nil
		}}
		u := New(&bootstrap.Service{Store: store}, nil, "assets")
		assert.Nil(t, u.readPriorEnrichments(context.Background(), logger, "gs://b/o"))
	})

	t.Run("ReturnsEnrichments", func(t *testing.T) {
		prior := &pbactivity.ActivityEnrichments{Streak: &pbactivity.StreakSummary{CurrentDays: 4}}
		store := &mocks.MockBlobStore{GetFunc: func(ctx context.Context, bucket, object string) ([]byte, error) {
			assert.Equal(t, "gs://b/o", object)
			return blobWith(t, prior, nil), nil
		}}
		u := New(&bootstrap.Service{Store: store}, nil, "assets")
		got := u.readPriorEnrichments(context.Background(), logger, "gs://b/o")
		require.NotNil(t, got)
		assert.Equal(t, int32(4), got.GetStreak().GetCurrentDays())
	})
}

// mergePriorEnrichments overlays the new run's enrichments field-wise on top of the
// prior blob's: anything the new run produced wins, anything it did not is carried
// over, and applied_enrichments is replaced with the supplied union.
func TestMergePriorEnrichments(t *testing.T) {
	logger := testLogger()

	t.Run("MalformedURIIsIgnored", func(t *testing.T) {
		var reads int
		store := &mocks.MockBlobStore{GetFunc: func(ctx context.Context, bucket, object string) ([]byte, error) {
			reads++
			return nil, nil
		}}
		u := New(&bootstrap.Service{Store: store}, nil, "assets")
		u.mergePriorEnrichments(context.Background(), logger, "not-a-gcs-uri", &pbactivity.ActivityEnrichments{}, nil)
		assert.Equal(t, 0, reads, "a URI without bucket/object must short-circuit before any read")
	})

	t.Run("ReadErrorDoesNotWrite", func(t *testing.T) {
		var wrote bool
		store := &mocks.MockBlobStore{
			GetFunc:   func(ctx context.Context, bucket, object string) ([]byte, error) { return nil, errors.New("gone") },
			WriteFunc: func(ctx context.Context, bucket, object string, data []byte) error { wrote = true; return nil },
		}
		u := New(&bootstrap.Service{Store: store}, nil, "assets")
		u.mergePriorEnrichments(context.Background(), logger, "gs://b/o", &pbactivity.ActivityEnrichments{}, nil)
		assert.False(t, wrote)
	})

	t.Run("UnparseableFreshBlobDoesNotWrite", func(t *testing.T) {
		var wrote bool
		store := &mocks.MockBlobStore{
			GetFunc:   func(ctx context.Context, bucket, object string) ([]byte, error) { return []byte("nope"), nil },
			WriteFunc: func(ctx context.Context, bucket, object string, data []byte) error { wrote = true; return nil },
		}
		u := New(&bootstrap.Service{Store: store}, nil, "assets")
		u.mergePriorEnrichments(context.Background(), logger, "gs://b/o", &pbactivity.ActivityEnrichments{}, nil)
		assert.False(t, wrote)
	})

	t.Run("NewValuesWinMissingOnesCarryOver", func(t *testing.T) {
		prior := &pbactivity.ActivityEnrichments{
			HeartRate: &pbactivity.HeartRateSummary{AvgBpm: 140},
			Streak:    &pbactivity.StreakSummary{CurrentDays: 4},
		}
		fresh := &pbactivity.ActivityEnrichments{
			HeartRate: &pbactivity.HeartRateSummary{AvgBpm: 150},
			AiSummary: &pbactivity.AiSummary{Html: "<b>nice</b>"},
		}
		var writtenBucket, writtenObject string
		var written []byte
		store := &mocks.MockBlobStore{
			GetFunc: func(ctx context.Context, bucket, object string) ([]byte, error) {
				return blobWith(t, fresh, []string{"heart-rate"}), nil
			},
			WriteFunc: func(ctx context.Context, bucket, object string, data []byte) error {
				writtenBucket, writtenObject, written = bucket, object, data
				return nil
			},
		}
		u := New(&bootstrap.Service{Store: store}, nil, "assets")
		u.mergePriorEnrichments(context.Background(), logger, "gs://assets/showcase_data/u/s_data.json", prior, []string{"heart-rate", "streak", "ai-summary"})

		assert.Equal(t, "assets", writtenBucket)
		assert.Equal(t, "showcase_data/u/s_data.json", writtenObject)
		var merged pbevents.EnrichedActivityEvent
		require.NoError(t, protojson.Unmarshal(written, &merged))
		assert.Equal(t, int32(150), merged.GetEnrichments().GetHeartRate().GetAvgBpm(), "new run's value wins")
		assert.Equal(t, int32(4), merged.GetEnrichments().GetStreak().GetCurrentDays(), "prior-only enrichment carried over")
		assert.Equal(t, "<b>nice</b>", merged.GetEnrichments().GetAiSummary().GetHtml(), "new-only enrichment kept")
		assert.Equal(t, []string{"heart-rate", "streak", "ai-summary"}, merged.AppliedEnrichments)
	})

	t.Run("WriteErrorIsLoggedNotFatal", func(t *testing.T) {
		store := &mocks.MockBlobStore{
			GetFunc:   func(ctx context.Context, bucket, object string) ([]byte, error) { return blobWith(t, nil, nil), nil },
			WriteFunc: func(ctx context.Context, bucket, object string, data []byte) error { return errors.New("write failed") },
		}
		u := New(&bootstrap.Service{Store: store}, nil, "assets")
		assert.NotPanics(t, func() {
			u.mergePriorEnrichments(context.Background(), logger, "gs://b/o", &pbactivity.ActivityEnrichments{}, nil)
		})
	})
}
