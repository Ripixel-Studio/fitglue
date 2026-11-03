package activity

import (
	"context"
	"os"
	"testing"

	"github.com/fitglue/server/src/go/internal/infra"
	"github.com/fitglue/server/src/go/pkg/claude"
)

// Opt-in: LIVE_CLAUDE=1 runs one synthetic roundup through the real CLI.
func TestRoundupLiveSmoke(t *testing.T) {
	if os.Getenv("LIVE_CLAUDE") != "1" {
		t.Skip("set LIVE_CLAUDE=1")
	}
	bin := os.Getenv("LIVE_CLAUDE_BIN")
	got := generateRoundupAISummary(context.Background(), infra.NewLogger(), claude.CLI{Bin: bin}, syntheticRoundup())
	t.Logf("OUT: %q", got)
}
