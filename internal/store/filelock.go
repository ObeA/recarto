package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

// tryLockFile is lockFile without the wait.
func tryLockFile(f *os.File) (bool, error) { return lockFile(f, false) }

func openLockFile(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
}

// LockFile takes the exclusive OS lock on the file at path, creating the file
// and its directory when needed, and returns the function that releases it.
// While another holder has the lock it calls waiting (when not nil) once, then
// waits until the lock is free or ctx is done. The OS drops a lock with its
// holder, so a killed process never leaves it held. Lock files are never
// removed: removing a locked file would let two processes lock two different
// files of the same name.
func LockFile(ctx context.Context, path string, waiting func()) (release func(), err error) {
	f, err := openLockFile(path)
	if err != nil {
		return nil, err
	}
	// Closing only drops the lock, so a close error loses nothing.
	unlock := func() { _ = f.Close() }
	ok, err := lockFile(f, false)
	if err != nil {
		unlock()
		return nil, err
	}
	if ok {
		return unlock, nil
	}
	if waiting != nil {
		waiting()
	}
	type result struct {
		ok  bool
		err error
	}
	got := make(chan result, 1)
	go func() {
		ok, err := lockFile(f, true)
		got <- result{ok, err}
	}()
	select {
	case r := <-got:
		if r.err == nil && !r.ok {
			r.err = errors.New("lock wait ended without the lock")
		}
		if r.err != nil {
			unlock()
			return nil, r.err
		}
		return unlock, nil
	case <-ctx.Done():
		// The abandoned wait still holds f; closing it once the wait ends means
		// it never keeps the lock.
		go func() { <-got; unlock() }()
		return nil, ctx.Err()
	}
}

// TryLockFile is LockFile without the wait: ok is false, with nothing held,
// while another holder has the lock.
func TryLockFile(path string) (release func(), ok bool, err error) {
	f, err := openLockFile(path)
	if err != nil {
		return nil, false, err
	}
	ok, err = lockFile(f, false)
	if err != nil || !ok {
		// Nothing is held, so a close error loses nothing.
		_ = f.Close()
		return nil, false, err
	}
	return func() { _ = f.Close() }, true, nil
}
