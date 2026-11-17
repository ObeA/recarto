package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestCheckpointTruncatesWAL: after a run of committed writes grows the WAL,
// Checkpoint flushes it into the main db and truncates the WAL file, bounding
// on-disk growth during a long index.
func TestCheckpointTruncatesWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ctx := context.Background()
	for i := range 300 {
		if err := st.WriteTx(ctx, func(c Conn) error {
			_, err := c.ExecContext(ctx,
				`INSERT INTO meta(key,value) VALUES (?,?)`, fmt.Sprintf("k%d", i), "v")
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}

	walPath := path + "-wal"
	before := walSize(t, walPath)
	if before == 0 {
		t.Fatalf("expected WAL to have grown before checkpoint, got 0")
	}
	if err := st.Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	if after := walSize(t, walPath); after != 0 {
		t.Errorf("WAL not truncated: before=%d after=%d", before, after)
	}
}

func walSize(t *testing.T, p string) int64 {
	t.Helper()
	info, err := os.Stat(p)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}

// TestCheckpointDoesNotWaitForReaders: a reader on an older snapshot keeps the
// WAL from being truncated; Checkpoint returns at once instead of waiting out
// the busy timeout, leaves the connection's busy timeout as it was, and
// truncates once the reader is gone.
func TestCheckpointDoesNotWaitForReaders(t *testing.T) {
	// A checkpoint that waited for the reader would hang the test.
	const hour = 60 * 60 * 1000
	path := filepath.Join(t.TempDir(), "store.db")
	st, err := open(path, hour)
	if err != nil {
		t.Fatal(err)
	}
	// Registered before the reader's cleanups so that, on failure, the reader
	// ends first and a waiting checkpoint can finish instead of blocking Close.
	t.Cleanup(func() { _ = st.Close() })
	// The busy-timeout check must read the connection the checkpoint used.
	st.DB.SetMaxOpenConns(1)

	ctx := context.Background()
	insert := func(key string) {
		t.Helper()
		if err := st.WriteTx(ctx, func(c Conn) error {
			_, err := c.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES (?, 'v')`, key)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 50 {
		insert(fmt.Sprintf("k%d", i))
	}

	reader, err := openPool(path, 5000)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	tx, err := reader.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM meta`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	insert("after-snapshot")

	done := make(chan error, 1)
	go func() { done <- st.Checkpoint(ctx) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Minute):
		t.Fatal("Checkpoint waited for the open reader")
	}
	walPath := path + "-wal"
	if walSize(t, walPath) == 0 {
		t.Fatal("WAL truncated despite the open reader")
	}
	var timeout int
	if err := st.DB.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&timeout); err != nil {
		t.Fatal(err)
	}
	if timeout != hour {
		t.Errorf("busy_timeout after Checkpoint = %d, want %d", timeout, hour)
	}

	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := st.Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	if size := walSize(t, walPath); size != 0 {
		t.Errorf("WAL not truncated after the reader ended: %d bytes", size)
	}
}
