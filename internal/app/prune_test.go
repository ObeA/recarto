package app

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gerwim/recarto/internal/clock"
	"github.com/gerwim/recarto/internal/graph"
)

// worktreeListGit extends fakeGit with a `git worktree list --porcelain`
// answer naming the given live paths.
type worktreeListGit struct {
	fakeGit
	live []string
}

func (g worktreeListGit) Run(args ...string) (string, error) {
	if args[0] == "worktree" {
		var b strings.Builder
		for _, p := range g.live {
			b.WriteString("worktree " + p + "\nHEAD abc\n\n")
		}
		return b.String(), nil
	}
	return g.fakeGit.Run(args...)
}

func openPruneApp(t *testing.T, live ...string) (*App, *clock.Fake) {
	t.Helper()
	t.Setenv("RECARTO_CACHE_DIR", t.TempDir())
	fc := clock.NewFake(time.UnixMilli(1_700_000_000_000))
	a, err := Open(Config{
		CWD:    "/repo",
		Runner: worktreeListGit{fakeGit: fakeGit{branch: "main"}, live: live},
		Clock:  fc,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a, fc
}

// goSrc returns a source directory holding one Go file.
func goSrc(t *testing.T) graph.FSSource {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package p\nfunc A(){}"), 0o644); err != nil {
		t.Fatal(err)
	}
	return graph.FSSource{Root: dir}
}

func unusedNames(p Prunable) []string {
	var out []string
	for _, u := range p.Unused {
		out = append(out, u.Worktree)
	}
	return out
}

// TestDeadWorktrees: a stored worktree is dead only when git no longer lists
// it AND its path is gone from disk — both, so a porcelain/path-form mismatch
// (symlinks) can never condemn a live worktree.
func TestDeadWorktrees(t *testing.T) {
	liveDir := t.TempDir()
	t.Setenv("RECARTO_UNUSED_WORKTREE_DAYS", "")
	a, _ := openPruneApp(t, liveDir)

	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(liveDir, "a.go"), []byte("package p\nfunc A(){}"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Index the live worktree, a deleted one (rows labeled with a path that no
	// longer exists), and an unlisted-but-present one (e.g. porcelain printed a
	// different path form for it).
	if err := a.Graph.Reconcile(ctx, liveDir, graph.FSSource{Root: liveDir}); err != nil {
		t.Fatal(err)
	}
	if err := a.Graph.Reconcile(ctx, "/definitely/gone-wt", graph.FSSource{Root: liveDir}); err != nil {
		t.Fatal(err)
	}
	present := t.TempDir()
	if err := a.Graph.Reconcile(ctx, present, graph.FSSource{Root: liveDir}); err != nil {
		t.Fatal(err)
	}

	p, err := a.PrunableWorktrees(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Dead) != 1 || p.Dead[0] != "/definitely/gone-wt" {
		t.Fatalf("Dead = %v, want exactly [/definitely/gone-wt]", p.Dead)
	}
	if len(p.Unused) != 0 {
		t.Fatalf("Unused = %v, want none", p.Unused)
	}
}

// TestPrunableWorktreesUnavailableListIsNoop: with no authoritative worktree
// list (git errors), nothing may be condemned, dead or unused.
func TestPrunableWorktreesUnavailableListIsNoop(t *testing.T) {
	t.Setenv("RECARTO_UNUSED_WORKTREE_DAYS", "")
	a := newTestApp(t, "main") // fakeGit errors on `worktree list`
	ctx := context.Background()
	src := goSrc(t)
	if err := a.Graph.Reconcile(ctx, "/definitely/gone-wt", src); err != nil {
		t.Fatal(err)
	}
	if err := a.Graph.Reconcile(ctx, t.TempDir(), src); err != nil {
		t.Fatal(err)
	}
	a.Clock.(*clock.Fake).Advance(10 * 24 * time.Hour)
	p, err := a.PrunableWorktrees(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Dead) != 0 || len(p.Unused) != 0 {
		t.Fatalf("Prunable = %+v, want none when the live list is unavailable", p)
	}
}

// TestPrunableWorktreesUnused: a worktree last used (or, unmarked, last
// indexed) more than N days ago is unused; a recently used one and the current
// worktree are not.
func TestPrunableWorktreesUnused(t *testing.T) {
	wtOld, wtRecent, wtLegacy := t.TempDir(), t.TempDir(), t.TempDir()
	a, fc := openPruneApp(t, wtOld, wtRecent, wtLegacy, "/repo")
	ctx := context.Background()
	t0 := fc.Now()
	src := goSrc(t)
	if err := a.Graph.Reconcile(ctx, "/repo", src); err != nil {
		t.Fatal(err)
	}
	for _, wt := range []string{wtOld, wtRecent} {
		if err := a.Graph.ReconcileIfStale(ctx, wt, src, "tok"); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Graph.Reconcile(ctx, wtLegacy, src); err != nil {
		t.Fatal(err)
	}
	fc.Advance(6 * 24 * time.Hour)
	if err := a.Graph.MarkUsed(ctx, wtRecent); err != nil {
		t.Fatal(err)
	}
	fc.Advance(4 * 24 * time.Hour)

	for _, tc := range []struct {
		days string
		want []string
	}{
		{"", []string{wtOld, wtLegacy}},
		{"0", nil},
		{"36500", nil},
	} {
		t.Run("days="+tc.days, func(t *testing.T) {
			t.Setenv("RECARTO_UNUSED_WORKTREE_DAYS", tc.days)
			p, err := a.PrunableWorktrees(ctx)
			if err != nil {
				t.Fatal(err)
			}
			got := unusedNames(p)
			if !sameSet(got, tc.want) {
				t.Fatalf("Unused = %v, want %v", got, tc.want)
			}
			for _, u := range p.Unused {
				if !u.LastUsed.Equal(t0) {
					t.Errorf("%s LastUsed = %v, want %v", u.Worktree, u.LastUsed, t0)
				}
			}
		})
	}
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]bool{}
	for _, x := range a {
		m[x] = true
	}
	for _, x := range b {
		if !m[x] {
			return false
		}
	}
	return true
}

// TestPrunableWorktreesNDayBoundary: a worktree used exactly N days ago is
// kept and one used a millisecond earlier is unused, for marked and unmarked
// worktrees alike.
func TestPrunableWorktreesNDayBoundary(t *testing.T) {
	t.Setenv("RECARTO_UNUSED_WORKTREE_DAYS", "7")
	wtMarked, wtLegacy := t.TempDir(), t.TempDir()
	a, fc := openPruneApp(t, wtMarked, wtLegacy, "/repo")
	ctx := context.Background()
	t0 := fc.Now()
	src := goSrc(t)
	if err := a.Graph.ReconcileIfStale(ctx, wtMarked, src, "tok"); err != nil {
		t.Fatal(err)
	}
	if err := a.Graph.Reconcile(ctx, wtLegacy, src); err != nil {
		t.Fatal(err)
	}
	fc.Advance(30 * time.Minute)
	if err := a.Graph.MarkUsed(ctx, wtMarked); err != nil {
		t.Fatal(err)
	}
	week := 7 * 24 * time.Hour
	for _, tc := range []struct {
		at   time.Duration
		want []string
	}{
		{week, nil},
		{week + time.Millisecond, []string{wtLegacy}},
		{week + 30*time.Minute, []string{wtLegacy}},
		{week + 30*time.Minute + time.Millisecond, []string{wtLegacy, wtMarked}},
	} {
		fc.Advance(t0.Add(tc.at).Sub(fc.Now()))
		p, err := a.PrunableWorktrees(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got := unusedNames(p); !slices.Equal(got, tc.want) {
			t.Errorf("at T0+%v: Unused = %v, want %v", tc.at, got, tc.want)
		}
	}
}

func fileCount(t *testing.T, a *App, wt string) int {
	t.Helper()
	var n int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM files WHERE worktree=?`, wt).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestHookDuringSweepStopsPrune: a session that comes back to an unused
// worktree after the sweep chose it writes the mark that stops the prune.
func TestHookDuringSweepStopsPrune(t *testing.T) {
	t.Setenv("RECARTO_UNUSED_WORKTREE_DAYS", "")
	wtOld := t.TempDir()
	a, fc := openPruneApp(t, wtOld, "/repo")
	ctx := context.Background()
	if err := a.Graph.ReconcileIfStale(ctx, wtOld, goSrc(t), "tok"); err != nil {
		t.Fatal(err)
	}
	fc.Advance(8 * 24 * time.Hour)
	p, err := a.PrunableWorktrees(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := unusedNames(p); !sameSet(got, []string{wtOld}) {
		t.Fatalf("Unused = %v, want [%s]", got, wtOld)
	}
	if err := a.Graph.NoteUsed(ctx, wtOld); err != nil {
		t.Fatal(err)
	}
	n, err := a.PruneWorktrees(ctx, p)
	if err != nil || n != 0 {
		t.Fatalf("PruneWorktrees = %d, %v; want 0, nil", n, err)
	}
	if got := fileCount(t, a, wtOld); got != 1 {
		t.Errorf("files of the used worktree = %d, want 1", got)
	}
}

// TestRecreatedDeadWorktreeKeepsItsGraph: the returning session's mark is too
// recent to rewrite, so the re-created directory alone must stop the prune.
func TestRecreatedDeadWorktreeKeepsItsGraph(t *testing.T) {
	t.Setenv("RECARTO_UNUSED_WORKTREE_DAYS", "")
	a, fc := openPruneApp(t, "/repo")
	ctx := context.Background()
	wt := filepath.Join(t.TempDir(), "wt")
	if err := a.Graph.Reconcile(ctx, wt, goSrc(t)); err != nil {
		t.Fatal(err)
	}
	if err := a.Graph.MarkUsed(ctx, wt); err != nil {
		t.Fatal(err)
	}
	fc.Advance(30 * time.Minute)
	p, err := a.PrunableWorktrees(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.Dead, []string{wt}) {
		t.Fatalf("Dead = %v, want [%s]", p.Dead, wt)
	}
	if err := os.Mkdir(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := a.Graph.NoteUsed(ctx, wt); err != nil {
		t.Fatal(err)
	}
	n, err := a.PruneWorktrees(ctx, p)
	if err != nil || n != 0 {
		t.Fatalf("PruneWorktrees = %d, %v; want 0, nil", n, err)
	}
	if got := fileCount(t, a, wt); got != 1 {
		t.Errorf("files of the re-created worktree = %d, want 1", got)
	}
}
