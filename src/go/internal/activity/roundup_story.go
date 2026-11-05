package activity

import (
	"context"
	"errors"
	"fmt"
	"time"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// RoundupStoryModel is recorded in ShowcaseRoundup.ai_summary_model when the
// story ("THE STORY" on the site) was written by the Claude path in James's
// voice (roundupAISummary: voice guide, voice.Check, no emoji). Summaries from
// before it was recorded, the old Gemini third-person ones, carry "".
// Change it when the path changes enough that every story should be rewritten:
// fitgluectl regen-roundup-stories then treats the old ones as stale.
const RoundupStoryModel = "claude-sonnet"

// RoundupStoryIsCurrent reports whether r's story was written by the current path.
func RoundupStoryIsCurrent(r *pbactivity.ShowcaseRoundup) bool {
	return r.GetAiSummary() != "" && r.GetAiSummaryModel() == RoundupStoryModel
}

// stampRoundupStory sets the story and who wrote it; empty text clears all three.
func stampRoundupStory(r *pbactivity.ShowcaseRoundup, text string, now time.Time) {
	r.AiSummary = text
	if text == "" {
		r.AiSummaryModel, r.AiSummaryGeneratedAt = "", nil
		return
	}
	r.AiSummaryModel = RoundupStoryModel
	r.AiSummaryGeneratedAt = timestamppb.New(now)
}

// WriteRoundupStory asks Claude for r's story from r's stored numbers; nothing
// is saved. "" with a nil error means the output failed the voice guard twice;
// a Claude error (claude.ErrLimited when the shared window is spent) is returned.
func (s *Service) WriteRoundupStory(ctx context.Context, r *pbactivity.ShowcaseRoundup) (string, error) {
	text, _, err := roundupAISummary(ctx, s.AI, r)
	return text, err
}

// SaveRoundupStory replaces only r's story (text, model, time), saves r and
// tells the site exporter, which re-exports roundup/<key>.json, refreshes
// index.json and signals the site rebuild. Empty text is refused: a failed
// rewrite must leave the old story in place.
func (s *Service) SaveRoundupStory(ctx context.Context, r *pbactivity.ShowcaseRoundup, text string, now time.Time) error {
	if text == "" {
		return errors.New("empty story")
	}
	stampRoundupStory(r, text, now)
	if err := s.store.SetRoundup(ctx, r); err != nil {
		return fmt.Errorf("save roundup %s: %w", r.PeriodKey, err)
	}
	s.siteRoundupChanged(ctx, r.Slug, r.PeriodKey)
	return nil
}
