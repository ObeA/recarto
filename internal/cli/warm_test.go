package cli

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gerwim/recarto/internal/app"
	"github.com/gerwim/recarto/internal/clock"
	"github.com/gerwim/recarto/internal/graph"
	"github.com/gerwim/recarto/internal/store"
)

// filesIn counts the graph files a's store holds for wt.
func filesIn(t *testing.T, a *app.App, wt string) int {
	t.Helper()
	var n int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM files WHERE worktree=?`, wt).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestWarmPrunesDeadWorktrees: deleting a git worktree must eventually delete
// its graph copy from the shared store; the work session start hands off is
// the opportunistic moment.
func TestWarmPrunesDeadWorktrees(t *testing.T) {
	liveDir := t.TempDir()
	a := newPruneApp(t, liveDir)
	a.Git.Worktree = t.TempDir()
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(liveDir, "a.go"), []byte("package p\nfunc A(){}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.Graph.Reconcile(ctx, liveDir, graph.FSSource{Root: liveDir}); err != nil {
		t.Fatal(err)
	}
	if err := a.Graph.Reconcile(ctx, "/definitely/gone-wt", graph.FSSource{Root: liveDir}); err != nil {
		t.Fatal(err)
	}

	if err := Warm(ctx, a); err != nil {
		t.Fatal(err)
	}

	if got := filesIn(t, a, "/definitely/gone-wt"); got != 0 {
		t.Errorf("dead worktree still has %d files after warm", got)
	}
	if got := filesIn(t, a, liveDir); got != 1 {
		t.Errorf("live worktree files = %d, want 1 (prune must be scoped)", got)
	}
}

// TestWarmPrunesUnusedWorktrees: warm drops the graph of a worktree not used
// for N days and keeps a recent one and its own.
func TestWarmPrunesUnusedWorktrees(t *testing.T) {
	wtOld, wtRecent, own := t.TempDir(), t.TempDir(), t.TempDir()
	a := newPruneApp(t, wtOld, wtRecent, own)
	a.Git.Worktree = own
	fc := a.Clock.(*clock.Fake)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(own, "a.go"), []byte("package p\nfunc A(){}"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := graph.FSSource{Root: own}
	for _, wt := range []string{wtOld, wtRecent, own} {
		if err := a.Graph.Reconcile(ctx, wt, src); err != nil {
			t.Fatal(err)
		}
	}
	fc.Advance(6 * 24 * time.Hour)
	if err := a.Graph.MarkUsed(ctx, wtRecent); err != nil {
		t.Fatal(err)
	}
	fc.Advance(4 * 24 * time.Hour)

	if err := Warm(ctx, a); err != nil {
		t.Fatal(err)
	}

	if got := filesIn(t, a, wtOld); got != 0 {
		t.Errorf("unused worktree still has %d files after warm", got)
	}
	if got := filesIn(t, a, wtRecent); got != 1 {
		t.Errorf("recent worktree files = %d, want 1", got)
	}
	if got := filesIn(t, a, own); got == 0 {
		t.Error("warm pruned its own worktree")
	}
}

// initGitRepo creates a real git repo in dir with files (path → content)
// committed. Duplicated per test package (no shared testutil package yet).
func initGitRepo(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for p, body := range files {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"commit", "-q", "-m", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func TestWarmIndexesServedReposAndCachesTheirMap(t *testing.T) {
	primary, extra := t.TempDir(), t.TempDir()
	initGitRepo(t, primary, map[string]string{"api/a.go": "package api\n\nfunc A() {}\n"})
	initGitRepo(t, extra, map[string]string{"lib/b.go": "package lib\n\nfunc B() {}\n"})
	for _, k := range []string{"RECARTO_WORKSPACE", "RECARTO_SYNC_URL", "RECARTO_SYNC_KEY", "RECARTO_SYNC_REPO"} {
		t.Setenv(k, "")
	}
	t.Setenv("RECARTO_REPOS", extra)
	t.Setenv("RECARTO_CACHE_DIR", t.TempDir())
	t.Setenv("RECARTO_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	a, err := app.Open(app.Config{CWD: primary, Clock: clock.NewFake(time.UnixMilli(1_700_000_000_000))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	ctx := context.Background()

	if err := Warm(ctx, a); err != nil {
		t.Fatal(err)
	}

	repos := a.AllRepos()
	if len(repos) != 2 {
		t.Fatalf("served repos = %d, want 2", len(repos))
	}
	for i, mod := range []string{"api", "lib"} {
		rc := repos[i]
		wt := rc.Git.Worktree
		// Nothing reads the map before the nodes go: a read of a finished index caches the map itself.
		if err := rc.Store.WriteTx(ctx, func(c store.Conn) error {
			_, err := c.ExecContext(ctx, `DELETE FROM nodes`)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		arch, err := rc.Graph.Architecture(ctx, wt, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(arch.Modules) != 1 || arch.Modules[0].Name != mod || arch.Modules[0].Symbols == 0 {
			t.Errorf("%s: map = %+v, want module %s with its symbols from the cache warm wrote", wt, arch.Modules, mod)
		}
	}
}

// heldSource lists only once gate closes, so its reconcile holds the
// worktree's index lock until then.
type heldSource struct {
	graph.Source
	listing chan struct{}
	gate    chan struct{}
}

func (h heldSource) List() ([]graph.FileEntry, error) {
	close(h.listing)
	<-h.gate
	return h.Source.List()
}

// TestWarmLeavesAWorktreeAnotherProcessIndexes: while another process indexes
// the primary worktree and holds the store's write lock, warm neither waits
// nor writes there, and still indexes the served repos and runs its sweep.
func TestWarmLeavesAWorktreeAnotherProcessIndexes(t *testing.T) {
	primary, extra := t.TempDir(), t.TempDir()
	initGitRepo(t, primary, map[string]string{"api/a.go": "package api\n\nfunc A() {}\n"})
	initGitRepo(t, extra, map[string]string{"lib/b.go": "package lib\n\nfunc B() {}\n"})
	for _, k := range []string{"RECARTO_WORKSPACE", "RECARTO_SYNC_URL", "RECARTO_SYNC_KEY", "RECARTO_SYNC_REPO"} {
		t.Setenv(k, "")
	}
	t.Setenv("RECARTO_REPOS", extra)
	t.Setenv("RECARTO_CACHE_DIR", t.TempDir())
	t.Setenv("RECARTO_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	open := func() *app.App {
		a, err := app.Open(app.Config{CWD: primary, Clock: clock.NewFake(time.UnixMilli(1_700_000_000_000))})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = a.Close() })
		return a
	}
	a, a2 := open(), open()
	ctx := context.Background()
	wt := a.Git.Worktree

	held := heldSource{Source: graph.NewSource(wt), listing: make(chan struct{}), gate: make(chan struct{})}
	indexed := make(chan error, 1)
	go func() { indexed <- a2.Graph.ReconcileIfStale(ctx, wt, held, "tok") }()
	var gateOnce sync.Once
	openGate := func() { gateOnce.Do(func() { close(held.gate) }) }
	t.Cleanup(openGate)
	awaitSignal(t, "the other process never began indexing", held.listing)

	blocker, err := sql.Open("sqlite", "file:"+a.Store.Path()+"?_pragma=busy_timeout(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = blocker.Close() })
	bconn, err := blocker.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bconn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("blocker BEGIN IMMEDIATE: %v", err)
	}
	awaitNil(t, "warm with the worktree busy", func() error { return Warm(ctx, a) })
	repos := a.AllRepos()
	if len(repos) != 2 {
		t.Fatalf("served repos = %d, want 2", len(repos))
	}
	var served int
	if err := repos[1].Store.DB.QueryRow(`SELECT COUNT(*) FROM files WHERE worktree=?`, repos[1].Git.Worktree).Scan(&served); err != nil {
		t.Fatal(err)
	}
	if served != 1 {
		t.Errorf("served repo files = %d, want 1", served)
	}
	if got := filesIn(t, a, wt); got != 0 {
		t.Errorf("busy worktree files = %d, want 0: warm must leave it", got)
	}

	if _, err := bconn.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	if err := a.Graph.Reconcile(ctx, "/definitely/gone-wt", graph.FSSource{Root: primary}); err != nil {
		t.Fatal(err)
	}
	awaitNil(t, "warm after the store lock is free", func() error { return Warm(ctx, a) })
	if got := filesIn(t, a, "/definitely/gone-wt"); got != 0 {
		t.Errorf("dead worktree files = %d, want 0: warm's sweep must run", got)
	}
	if got := filesIn(t, a, wt); got != 0 {
		t.Errorf("busy worktree files = %d, want 0: warm must leave it", got)
	}

	openGate()
	awaitNil(t, "the other process's index", func() error { return <-indexed })
}

// awaitSignal waits for ch to close, failing only on a hang.
func awaitSignal(t *testing.T, msg string, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Minute):
		t.Fatal(msg)
	}
}

// awaitNil runs f in a goroutine and fails on an error or a hang.
func awaitNil(t *testing.T, what string, f func() error) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- f() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	case <-time.After(time.Minute):
		t.Fatalf("%s hung", what)
	}
}

func TestWarmBadUnusedDaysStillPrunesDead(t *testing.T) {
	wtOld, own := t.TempDir(), t.TempDir()
	a := newPruneApp(t, wtOld, own)
	a.Git.Worktree = own
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(own, "a.go"), []byte("package p\nfunc A(){}"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := graph.FSSource{Root: own}
	for _, wt := range []string{own, wtOld, "/definitely/gone-wt"} {
		if err := a.Graph.Reconcile(ctx, wt, src); err != nil {
			t.Fatal(err)
		}
	}
	a.Clock.(*clock.Fake).Advance(10 * 24 * time.Hour)
	t.Setenv("RECARTO_UNUSED_WORKTREE_DAYS", "7d")

	if err := Warm(ctx, a); err == nil || !strings.Contains(err.Error(), "RECARTO_UNUSED_WORKTREE_DAYS") {
		t.Fatalf("Warm error = %v, want one naming RECARTO_UNUSED_WORKTREE_DAYS", err)
	}
	if got := filesIn(t, a, "/definitely/gone-wt"); got != 0 {
		t.Errorf("dead worktree files = %d, want 0", got)
	}
	if got := filesIn(t, a, wtOld); got != 1 {
		t.Errorf("unused worktree files = %d, want 1", got)
	}
}
