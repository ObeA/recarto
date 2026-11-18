package app

import (
	"os"
	"path/filepath"
	"strconv"

	"github.com/gerwim/recarto/internal/cache"
	"github.com/gerwim/recarto/internal/store"
)

// serveSlots is the directory of repoID's serving slots, next to its store:
// one lock file per slot, each held exclusively by one `recarto serve`.
func serveSlots(repoID string) (string, error) {
	path, err := cache.StorePath(repoID)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(path), "serve"), nil
}

// MarkServing marks a Recarto MCP server as running for repoID on this
// machine until release is called, by locking the lowest slot no one holds.
// The OS drops the lock with its holder, so a crashed server stops counting
// once it has exited. A slot a ServerRunning check is reading counts as held
// and is skipped. Slot files are never removed (see store.LockFile).
func MarkServing(repoID string) (release func(), err error) {
	dir, err := serveSlots(repoID)
	if err != nil {
		return nil, err
	}
	// The loop ends: a slot past every existing file is new, so only a server
	// racing for that same slot can hold it, and then the next one is tried.
	for n := 0; ; n++ {
		release, ok, err := store.TryLockFile(filepath.Join(dir, strconv.Itoa(n)+".lock"))
		if err != nil {
			return nil, err
		}
		if ok {
			return release, nil
		}
	}
}

// ServerRunning reports whether a Recarto MCP server marks repoID on this
// machine (MarkServing).
//
// A slot or directory that cannot be read counts as no server: hooks then
// leave out their tool pointers rather than fail or point at tools that may be
// gone. Checks take the shared lock, which other checks share, so concurrent
// checks never count each other as a server.
func ServerRunning(repoID string) bool {
	dir, err := serveSlots(repoID)
	if err != nil {
		return false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		release, ok, err := store.TryShareLockFile(filepath.Join(dir, e.Name()))
		if err != nil {
			// An unreadable slot counts as no server held there (see above).
			continue
		}
		if !ok {
			return true
		}
		release()
	}
	return false
}
