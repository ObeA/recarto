//go:build !windows

package store

import (
	"errors"
	"os"
	"syscall"
)

// lockFile takes an exclusive lock on f, waiting for it when wait is set and
// otherwise reporting false when another open file holds it. Closing f or
// exiting releases it. flock binds to the open file description, so two opens
// in one process exclude each other, which POSIX fcntl locks would not.
func lockFile(f *os.File, wait bool) (bool, error) {
	how := syscall.LOCK_EX
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
