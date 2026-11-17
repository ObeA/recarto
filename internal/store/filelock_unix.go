//go:build !windows

package store

import (
	"errors"
	"os"
	"syscall"
)

// tryLockFile takes an exclusive lock on f without waiting, reporting false
// when another open file holds it. Closing f or exiting releases it. flock
// binds to the open file description, so two opens in one process exclude
// each other, which POSIX fcntl locks would not.
func tryLockFile(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return false, nil
	}
	return err == nil, err
}
