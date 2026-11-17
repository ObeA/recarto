//go:build !windows

package store

import (
	"errors"
	"os"
	"syscall"
)

// lockFile takes an exclusive lock on f, or a shared one when shared is set;
// shared holders coexist, and any holder excludes an exclusive lock. It waits
// for the lock when wait is set and otherwise reports false when another open
// file holds it. Closing f or
// exiting releases it. flock binds to the open file description, so two opens
// in one process exclude each other, which POSIX fcntl locks would not.
func lockFile(f *os.File, wait, shared bool) (bool, error) {
	how := syscall.LOCK_EX
	if shared {
		how = syscall.LOCK_SH
	}
	if !wait {
		how |= syscall.LOCK_NB
	}
	for {
		err := syscall.Flock(int(f.Fd()), how)
		// A signal can cut a wait short.
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return false, nil
		}
		return err == nil, err
	}
}
