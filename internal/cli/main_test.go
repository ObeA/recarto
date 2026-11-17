package cli

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	// A hook run by a test would otherwise start `<test binary> warm`, which
	// reruns the package's tests.
	startWarm = func() error { return nil }
	os.Exit(m.Run())
}
