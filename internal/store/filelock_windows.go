//go:build windows

package store

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockFile takes an exclusive lock on f, or a shared one when shared is set;
// shared holders coexist, and any holder excludes an exclusive lock. It waits
// for the lock when wait is set and otherwise reports false when another
// handle holds it. Closing f or exiting
// releases it. The locked byte sits at 4 GiB, past the content: Windows locks
// are mandatory, and the trace must stay readable by processes that do not
// hold the lock.
func lockFile(f *os.File, wait, shared bool) (bool, error) {
	var flags uint32
	if !shared {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	if !wait {
		flags |= windows.LOCKFILE_FAIL_IMMEDIATELY
	}
	err := windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, 1, 0, &windows.Overlapped{OffsetHigh: 1})
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
		return false, nil
	}
	return err == nil, err
}
