package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestLockFileAbandonedWaitReleases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "locks", "wt.lock")
	release, err := LockFile(context.Background(), path, nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	waiting := make(chan struct{})
	abandoned := make(chan error, 1)
	go func() {
		_, err := LockFile(ctx, path, func() { close(waiting) })
		abandoned <- err
	}()
	select {
	case <-waiting:
	case <-time.After(time.Minute):
		t.Fatal("the second LockFile never waited")
	}
	cancel()
	if err := <-abandoned; !errors.Is(err, context.Canceled) {
		t.Fatalf("abandoned wait = %v, want context.Canceled", err)
	}
	release()

	third := make(chan error, 1)
	go func() {
		r, err := LockFile(context.Background(), path, nil)
		if err == nil {
			r()
		}
		third <- err
	}()
	select {
	case err := <-third:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Minute):
		t.Fatal("the abandoned wait kept the lock")
	}
}
