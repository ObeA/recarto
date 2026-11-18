package agents

import (
	"context"
	"os"
	"testing"
)

// TestStopCheckpointsWAL: the Stop hook is the natural turn boundary to flush
// the WAL — the agent is idle, so the TRUNCATE checkpoint can actually win.
// Without it the WAL only ever shrinks mid-index, which silently fails under
// concurrent readers.
func TestStopCheckpointsWAL(t *testing.T) {
	a := newTestApp(t, "main")
	ctx := context.Background()
	if _, err := Handle(ctx, a, Event{Kind: KindSessionStart, Agent: "opencode", SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Handle(ctx, a, Event{Kind: KindStop, SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	wal := a.Store.Path() + "-wal"
	if info, err := os.Stat(wal); err == nil && info.Size() != 0 {
		t.Fatalf("WAL is %d bytes after Stop hook, want 0 (truncated)", info.Size())
	}
}
