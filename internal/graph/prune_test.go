package graph

import (
	"context"
	"testing"
	"time"

	"github.com/gerwim/recarto/internal/clock"
)

// orphanFTSRows counts FTS rows whose node no longer exists — must be zero
// after a prune, since FTS5 tables are not FK-linked and need explicit teardown.
func orphanFTSRows(t *testing.T, e *Engine) int {
	t.Helper()
	total := 0
	for _, tbl := range []string{"nodes_fts", "nodes_fts_stem", "nodes_fts_tri"} {
		var n int
		if err := e.st.DB.QueryRow(
			`SELECT COUNT(*) FROM ` + tbl + ` WHERE node_id NOT IN (SELECT id FROM nodes)`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		total += n
	}
	return total
}

func metaKeyExists(t *testing.T, e *Engine, key string) bool {
	t.Helper()
	var v string
	err := e.st.DB.QueryRow(`SELECT value FROM meta WHERE key=?`, key).Scan(&v)
	return err == nil
}

// TestPruneWorktreeRemovesAllRows: pruning a worktree must remove its files
// (cascading nodes/edges/candidates), its FTS rows, and its meta keys — and
// leave every other worktree fully intact and queryable.
func TestPruneWorktreeRemovesAllRows(t *testing.T) {
	e := newTestEngine(t)
	ctx := context.Background()
	if err := e.ReconcileIfStale(ctx, "/wtA", crossFileCallSource(), "tok-a"); err != nil {
		t.Fatal(err)
	}
	if err := e.ReconcileIfStale(ctx, "/wtB", crossFileCallSource(), "tok-b"); err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, e, "files", "/wtB"); got != 2 {
		t.Fatalf("setup: wtB files = %d, want 2", got)
	}
	if _, err := e.Architecture(ctx, "/wtB", ""); err != nil {
		t.Fatal(err)
	}
	seedJournal(t, e, "/wtB")
	insertMeta(t, e, "graph_resolve_pending:/wtB", "1")
	e.clk.(*clock.Fake).Advance(time.Hour)

	if ok, err := e.PruneWorktree(ctx, "/wtB", e.clk.Now()); err != nil || !ok {
		t.Fatalf("PruneWorktree = %v, %v; want true, nil", ok, err)
	}

	for _, tbl := range []string{"files", "nodes", "edges"} {
		if got := countRows(t, e, tbl, "/wtB"); got != 0 {
			t.Errorf("%s rows for pruned worktree = %d, want 0", tbl, got)
		}
	}
	if got := orphanFTSRows(t, e); got != 0 {
		t.Errorf("orphaned FTS rows after prune = %d, want 0", got)
	}
	var mapped int
	if err := e.st.DB.QueryRow(`SELECT COUNT(*) FROM node_fts_rows r
		JOIN nodes n ON n.id=r.node_id JOIN files f ON f.id=n.file_id
		WHERE f.worktree='/wtB'`).Scan(&mapped); err != nil {
		t.Fatal(err)
	}
	if mapped != 0 {
		t.Errorf("FTS row mappings for pruned worktree = %d, want 0", mapped)
	}
	for _, k := range []string{"graph_token:/wtB", "arch_cache:/wtB", "worktree_used:/wtB", "graph_resolve_pending:/wtB"} {
		if metaKeyExists(t, e, k) {
			t.Errorf("meta key %s survived the prune", k)
		}
	}
	if got := journalRows(t, e, "/wtB"); got != 0 {
		t.Errorf("journal rows for pruned worktree = %d, want 0", got)
	}
	if wts, err := e.Worktrees(ctx); err != nil || len(wts) != 1 || wts[0] != "/wtA" {
		t.Errorf("Worktrees = %v, %v; want [/wtA]", wts, err)
	}

	// The surviving worktree is untouched and still resolved.
	if got := countRows(t, e, "files", "/wtA"); got != 2 {
		t.Errorf("wtA files = %d, want 2 (prune must be scoped)", got)
	}
	if got := resolvedCallEdges(t, e, "/wtA"); got == 0 {
		t.Error("wtA lost its resolved edges")
	}
	if !metaKeyExists(t, e, "graph_token:/wtA") {
		t.Error("wtA freshness token lost")
	}
	if !metaKeyExists(t, e, "worktree_used:/wtA") {
		t.Error("wtA use mark lost")
	}
}

// TestWorktreesListsDistinct: Engine.Worktrees feeds the GC's liveness check.
func TestWorktreesListsDistinct(t *testing.T) {
	e := newTestEngine(t)
	ctx := context.Background()
	if err := e.Reconcile(ctx, "/wtA", crossFileCallSource()); err != nil {
		t.Fatal(err)
	}
	if err := e.Reconcile(ctx, "/wtB", crossFileCallSource()); err != nil {
		t.Fatal(err)
	}
	wts, err := e.Worktrees(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(wts) != 2 {
		t.Fatalf("Worktrees = %v, want 2 entries", wts)
	}
}

func insertMeta(t *testing.T, e *Engine, key, value string) {
	t.Helper()
	if _, err := e.st.DB.Exec(`INSERT OR REPLACE INTO meta(key, value) VALUES (?, ?)`, key, value); err != nil {
		t.Fatal(err)
	}
}

func journalRows(t *testing.T, e *Engine, worktree string) int {
	t.Helper()
	var n int
	if err := e.st.DB.QueryRow(`SELECT COUNT(*) FROM graph_deltas WHERE worktree=?`, worktree).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// seedJournal gives worktree change-journal rows, as an edit would.
func seedJournal(t *testing.T, e *Engine, worktree string) {
	t.Helper()
	if _, err := e.st.DB.Exec(`INSERT INTO graph_deltas(id, worktree, path, qualified_name, kind, change, at)
		VALUES ('d1', ?, 'b.go', 'p.B', 'function', 'changed', 0)`, worktree); err != nil {
		t.Fatal(err)
	}
}

func usedMark(t *testing.T, e *Engine, worktree string) int64 {
	t.Helper()
	var v int64
	if err := e.st.DB.QueryRow(`SELECT CAST(value AS INTEGER) FROM meta WHERE key=?`,
		"worktree_used:"+worktree).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// TestPruneStepLeavesRecoverableState: every committed prune step leaves a
// worktree that re-indexes (no token, no code map, no journal) and is still
// listed while it has files, so the next sweep resumes it.
func TestPruneStepLeavesRecoverableState(t *testing.T) {
	e := newTestEngine(t)
	ctx := context.Background()
	if err := e.ReconcileIfStale(ctx, "/wtB", crossFileCallSource(), "tok-b"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Architecture(ctx, "/wtB", ""); err != nil {
		t.Fatal(err)
	}
	seedJournal(t, e, "/wtB")
	insertMeta(t, e, "graph_resolve_pending:/wtB", "1")
	e.clk.(*clock.Fake).Advance(time.Hour)

	done, err := e.pruneStep(ctx, "/wtB", e.clk.Now(), 1)
	if err != nil || done {
		t.Fatalf("first step = %v, %v; want false, nil", done, err)
	}
	if got := countRows(t, e, "files", "/wtB"); got != 1 {
		t.Fatalf("files after one step = %d, want 1", got)
	}
	if wts, _ := e.Worktrees(ctx); len(wts) != 1 {
		t.Errorf("Worktrees after one step = %v, want /wtB still listed", wts)
	}
	for _, k := range []string{"graph_token:/wtB", "arch_cache:/wtB"} {
		if metaKeyExists(t, e, k) {
			t.Errorf("%s survived the first step", k)
		}
	}
	if got := journalRows(t, e, "/wtB"); got != 0 {
		t.Errorf("journal rows after first step = %d, want 0", got)
	}

	done, err = e.pruneStep(ctx, "/wtB", e.clk.Now(), 1)
	if err != nil || !done {
		t.Fatalf("second step = %v, %v; want true, nil", done, err)
	}
	if got := countRows(t, e, "files", "/wtB"); got != 0 {
		t.Errorf("files after last step = %d, want 0", got)
	}
	for _, k := range []string{"graph_token:/wtB", "arch_cache:/wtB", "graph_resolve_pending:/wtB", "worktree_used:/wtB"} {
		if metaKeyExists(t, e, k) {
			t.Errorf("%s survived the prune", k)
		}
	}
}

// TestPruneWorktreeStopsWhenUsedSince: a use at or after usedBefore stops the
// prune before it writes anything; an older use does not.
func TestPruneWorktreeStopsWhenUsedSince(t *testing.T) {
	e := newTestEngine(t)
	ctx := context.Background()
	if err := e.ReconcileIfStale(ctx, "/wtB", crossFileCallSource(), "tok-b"); err != nil {
		t.Fatal(err)
	}
	t0 := e.clk.Now()
	ok, err := e.PruneWorktree(ctx, "/wtB", t0)
	if err != nil || ok {
		t.Fatalf("PruneWorktree(used at usedBefore) = %v, %v; want false, nil", ok, err)
	}
	if got := countRows(t, e, "files", "/wtB"); got != 2 {
		t.Errorf("files after stopped prune = %d, want 2", got)
	}
	if !metaKeyExists(t, e, "graph_token:/wtB") {
		t.Error("stopped prune dropped the token")
	}
	ok, err = e.PruneWorktree(ctx, "/wtB", t0.Add(time.Millisecond))
	if err != nil || !ok {
		t.Fatalf("PruneWorktree(used before usedBefore) = %v, %v; want true, nil", ok, err)
	}
}

// TestPrunedWorktreeReindexes: coming back to a fully or partly pruned
// worktree re-indexes it under its old, unchanged token, serves its symbols,
// and journals nothing for the re-created files.
func TestPrunedWorktreeReindexes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		prune func(e *Engine) error
	}{
		{"after one prune step", func(e *Engine) error {
			_, err := e.pruneStep(context.Background(), "/wtB", e.clk.Now(), 1)
			return err
		}},
		{"after the whole prune", func(e *Engine) error {
			_, err := e.PruneWorktree(context.Background(), "/wtB", e.clk.Now())
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEngine(t)
			ctx := context.Background()
			src := &mutMemSource{files: map[string]string{
				"a.go": "package p\n\nfunc Alpha() {\n\tBravo()\n}\n",
				"b.go": "package p\n\nfunc Bravo() {}\n",
			}}
			if err := e.ReconcileIfStale(ctx, "/wtB", src, "tok-b"); err != nil {
				t.Fatal(err)
			}
			seedJournal(t, e, "/wtB")
			e.clk.(*clock.Fake).Advance(time.Hour)
			if err := tc.prune(e); err != nil {
				t.Fatal(err)
			}

			if f := e.EnsureFresh(ctx, "/wtB", src, "tok-b", 10*time.Second); f.State != Fresh {
				t.Fatalf("EnsureFresh = %+v, want Fresh", f)
			}
			if got := countRows(t, e, "files", "/wtB"); got != 2 {
				t.Errorf("files after re-index = %d, want 2", got)
			}
			for _, sym := range []string{"Alpha", "Bravo"} {
				res, err := e.Search(ctx, "/wtB", sym, "", "", 10)
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, r := range res {
					found = found || r.Name == sym
				}
				if !found {
					t.Errorf("Search(%s) = %v, want the re-indexed symbol", sym, res)
				}
			}
			if _, n, err := e.DeltasSince(ctx, "/wtB", 0, 100); err != nil || n != 0 {
				t.Errorf("DeltasSince = %d, %v; want 0 changes from the re-index", n, err)
			}
		})
	}
}

// TestWorktreesUsedBefore: a worktree is selected iff its last use (its mark,
// else its newest index time) is strictly before the cutoff.
func TestWorktreesUsedBefore(t *testing.T) {
	e := newTestEngine(t)
	ctx := context.Background()
	fc := e.clk.(*clock.Fake)
	t0 := e.clk.Now()
	if err := e.ReconcileIfStale(ctx, "/wtMarked", crossFileCallSource(), "tok"); err != nil {
		t.Fatal(err)
	}
	if err := e.Reconcile(ctx, "/wtLegacy", crossFileCallSource()); err != nil {
		t.Fatal(err)
	}
	if err := e.Reconcile(ctx, "/wtNew", crossFileCallSource()); err != nil {
		t.Fatal(err)
	}
	fc.Advance(5 * 24 * time.Hour)
	if err := e.MarkUsed(ctx, "/wtNew"); err != nil {
		t.Fatal(err)
	}

	got, err := e.WorktreesUsedBefore(ctx, t0)
	if err != nil || len(got) != 0 {
		t.Fatalf("WorktreesUsedBefore(T0) = %v, %v; want none", got, err)
	}
	got, err = e.WorktreesUsedBefore(ctx, t0.Add(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, u := range got {
		names[u.Worktree] = true
		if !u.LastUsed.Equal(t0) {
			t.Errorf("%s LastUsed = %v, want %v", u.Worktree, u.LastUsed, t0)
		}
	}
	if len(got) != 2 || !names["/wtMarked"] || !names["/wtLegacy"] {
		t.Errorf("WorktreesUsedBefore(T0+1ms) = %v, want /wtMarked and /wtLegacy", got)
	}
}

// TestMarkUsedNeverMovesBack: a mark from an earlier clock read cannot
// overwrite a newer one.
func TestMarkUsedNeverMovesBack(t *testing.T) {
	e := newTestEngine(t)
	ctx := context.Background()
	fc := e.clk.(*clock.Fake)
	t0 := e.clk.Now()
	mark := func() {
		t.Helper()
		if err := e.MarkUsed(ctx, "/wt"); err != nil {
			t.Fatal(err)
		}
	}
	fc.Advance(time.Hour)
	mark()
	fc.Advance(-time.Hour)
	mark()
	if got := usedMark(t, e, "/wt"); got != t0.Add(time.Hour).UnixMilli() {
		t.Errorf("mark after an earlier write = %d, want T0+1h", got)
	}
	fc.Advance(2 * time.Hour)
	mark()
	if got := usedMark(t, e, "/wt"); got != t0.Add(2*time.Hour).UnixMilli() {
		t.Errorf("mark after a later write = %d, want T0+2h", got)
	}
}
