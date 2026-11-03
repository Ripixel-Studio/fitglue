package activity

import (
	"os"
	"testing"
)

// Service tests that reach generateRoundup must never spawn the real claude CLI
// (it would spend the shared usage window). /bin/false fails at once, so the
// roundup keeps its non-AI text.
func TestMain(m *testing.M) {
	os.Setenv("CLAUDE_BIN", "/bin/false")
	os.Exit(m.Run())
}
