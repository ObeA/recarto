package app

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/gerwim/recarto/internal/store"
)

// markServing marks repoID served, failing the test on an error.
func markServing(t testing.TB, repoID string) func() {
	t.Helper()
	release, err := MarkServing(repoID)
	if err != nil {
		t.Fatal(err)
	}
	return release
}

// slot is the path of repoID's serving slot n.
func slot(t testing.TB, repoID string, n int) string {
	t.Helper()
	dir, err := serveSlots(repoID)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, strconv.Itoa(n)+".lock")
}

func TestServerRunningFollowsMarks(t *testing.T) {
	t.Setenv("RECARTO_CACHE_DIR", t.TempDir())
	if ServerRunning("r1") {
		t.Fatal("running before any mark")
	}
	first, second := markServing(t, "r1"), markServing(t, "r1")
	if !ServerRunning("r1") {
		t.Error("not running with two marks")
	}
	if ServerRunning("r2") {
		t.Error("another repo's marks count for r2")
	}
	first()
	if !ServerRunning("r1") {
		t.Error("not running while the second mark is held")
	}
	second()
	if ServerRunning("r1") {
		t.Error("running after every mark is released")
	}
	for n := range 2 {
		if _, err := os.Stat(slot(t, "r1", n)); err != nil {
			t.Errorf("slot %d: %v", n, err)
		}
	}
}

// A check in flight holds the shared lock on a slot; another check must not
// take it for a server, and a server starting meanwhile must still be seen.
func TestServerRunningIgnoresOverlappingChecks(t *testing.T) {
	t.Setenv("RECARTO_CACHE_DIR", t.TempDir())
	markServing(t, "r")()
	inFlight, ok, err := store.TryShareLockFile(slot(t, "r", 0))
	if err != nil || !ok {
		t.Fatalf("shared lock on a free slot: ok=%v, %v", ok, err)
	}
	defer inFlight()
	if ServerRunning("r") {
		t.Error("a check in flight counts as a server")
	}
	release := markServing(t, "r")
	defer release()
	if _, err := os.Stat(slot(t, "r", 1)); err != nil {
		t.Errorf("a server starting during a check did not take slot 1: %v", err)
	}
	if !ServerRunning("r") {
		t.Error("a server that started during a check is not seen")
	}
}

func TestServerRunningWhenTheCheckCannotRun(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RECARTO_CACHE_DIR", file)
	if ServerRunning("r") {
		t.Error("running with an unreadable cache")
	}
}

func BenchmarkServerRunning(b *testing.B) {
	b.Setenv("RECARTO_CACHE_DIR", b.TempDir())
	var releases []func()
	for range 8 {
		releases = append(releases, markServing(b, "r"))
	}
	for _, r := range releases {
		r()
	}
	b.Run("no server, 8 slots", func(b *testing.B) {
		for b.Loop() {
			ServerRunning("r")
		}
	})
	defer markServing(b, "r")()
	b.Run("one server", func(b *testing.B) {
		for b.Loop() {
			ServerRunning("r")
		}
	})
}
