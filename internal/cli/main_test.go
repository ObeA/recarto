package cli

import (
	"os"
	"testing"

	"github.com/gerwim/recarto/internal/agents"
)

func TestMain(m *testing.M) {
	// A hook run by a test would otherwise start `<test binary> warm`, which
	// reruns the package's tests.
	startWarm = func() error { return nil }
	// Tests must not start the host's own `claude`.
	claudeCodeNote = func() string { return "" }
	claudeCodeWarning = func(agents.Paths) string { return "" }
	os.Exit(m.Run())
}
