package graph

import (
	"context"
	"strings"
	"testing"

	"github.com/gerwim/recarto/internal/store"
)

func moduleNamesOf(a Architecture) []string {
	var names []string
	for _, m := range a.Modules {
		names = append(names, m.Name)
	}
	return names
}

func TestCodeMapOnlyForFinishedIndex(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		nonGit bool
		setup  func(t *testing.T, e *Engine, wt string)
		done   bool
	}{
		{"nothing indexed", false, func(*testing.T, *Engine, string) {}, false},
		{"finished index", false, func(t *testing.T, e *Engine, wt string) {
			if err := e.ReconcileIfStale(ctx, wt, FSSource{Root: wt}, "tok"); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"reconcile cut short", false, func(t *testing.T, e *Engine, wt string) {
			if err := e.ReconcileIfStale(ctx, wt, FSSource{Root: wt}, "tok"); err != nil {
				t.Fatal(err)
			}
			if err := e.setResolvePending(ctx, wt); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"outside git", true, func(t *testing.T, e *Engine, wt string) {
			if err := e.Reconcile(ctx, wt, FSSource{Root: wt}); err != nil {
				t.Fatal(err)
			}
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEngine(t)
			wt := t.TempDir()
			writeFile(t, wt, "api/a.go", "package api\nfunc A(){}")
			tc.setup(t, e, wt)
			arch, done, err := e.CodeMap(ctx, wt, tc.nonGit)
			if err != nil {
				t.Fatal(err)
			}
			if done != tc.done {
				t.Fatalf("done = %v, want %v", done, tc.done)
			}
			if got := strings.Join(moduleNamesOf(arch), ","); (done && got != "api") || (!done && got != "") {
				t.Errorf("modules = %q with done=%v", got, done)
			}
		})
	}
}

func TestCodeMapReadsOneSnapshot(t *testing.T) {
	e := newTestEngine(t)
	ctx := context.Background()
	wt := t.TempDir()
	writeFile(t, wt, "api/a.go", "package api\nfunc A(){}")
	writeFile(t, wt, "lib/b.go", "package lib\nfunc B(){}")
	if err := e.ReconcileIfStale(ctx, wt, FSSource{Root: wt}, "tok"); err != nil {
		t.Fatal(err)
	}
	fired := false
	archHook = func() {
		if fired {
			return
		}
		fired = true
		// What a reconcile starting in another process commits.
		if err := e.setResolvePending(ctx, wt); err != nil {
			t.Error(err)
		}
		if err := e.st.WriteTx(ctx, func(c store.Conn) error {
			_, err := c.ExecContext(ctx, `DELETE FROM files WHERE worktree=? AND path='lib/b.go'`, wt)
			return err
		}); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { archHook = nil })

	arch, done, err := e.CodeMap(ctx, wt, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(moduleNamesOf(arch), ","); !done || got != "api,lib" {
		t.Errorf("CodeMap = %q, done=%v; want api,lib from the snapshot the check saw", got, done)
	}
	archHook = nil
	if _, done, err := e.CodeMap(ctx, wt, false); err != nil || done {
		t.Errorf("after the commit: done = %v, %v; want false (pending marker)", done, err)
	}
}

func TestArchitectureNotCachedFromUnfinishedIndex(t *testing.T) {
	e := newTestEngine(t)
	ctx := context.Background()
	wt := t.TempDir()
	writeFile(t, wt, "api/a.go", "package api\nfunc A(){}")
	if err := e.ReconcileIfStale(ctx, wt, FSSource{Root: wt}, "tok1"); err != nil {
		t.Fatal(err)
	}
	if err := e.setResolvePending(ctx, wt); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Architecture(ctx, wt, ""); err != nil {
		t.Fatal(err)
	}
	if err := e.clearResolvePending(ctx, wt); err != nil {
		t.Fatal(err)
	}
	if err := e.st.WriteTx(ctx, func(c store.Conn) error {
		_, err := c.ExecContext(ctx, `DELETE FROM nodes`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	arch, err := e.Architecture(ctx, wt, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range arch.Modules {
		if m.Symbols != 0 {
			t.Errorf("module %+v: a map of the unfinished index was cached", m)
		}
	}
}

func TestArchitectureQueriesReadOnlyTheWorktree(t *testing.T) {
	e := newTestEngine(t)
	ctx := context.Background()
	if err := e.Reconcile(ctx, "/wt", crossFileCallSource()); err != nil {
		t.Fatal(err)
	}
	// The reconcile's ANALYZE costs a two-file store, where a scan is cheapest.
	// Plan against the statistics of a store holding many worktrees instead.
	if err := e.st.WriteTx(ctx, func(c store.Conn) error {
		for _, row := range [][2]string{
			{"files", "idx_files_worktree"}, {"nodes", "idx_nodes_file"}, {"edges", "idx_edges_file"},
		} {
			if _, err := c.ExecContext(ctx, `DELETE FROM sqlite_stat1 WHERE tbl=?`, row[0]); err != nil {
				return err
			}
			if _, err := c.ExecContext(ctx, `INSERT INTO sqlite_stat1(tbl, idx, stat) VALUES (?, ?, ?)`,
				row[0], row[1], "1000000 20"); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// A new handle loads the statistics written above.
	st, err := store.Open(e.st.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	for _, q := range []string{archNodesQuery, archEdgesQuery} {
		for _, step := range explainPlan(t, st.DB, q, "/wt") {
			if strings.HasPrefix(step, "SCAN") {
				t.Errorf("plan of %q has a full scan: %q", q, step)
			}
		}
	}
}

func TestArchitectureIgnoresOtherWorktrees(t *testing.T) {
	e := newTestEngine(t)
	ctx := context.Background()
	index := func(wt string, files map[string]string) {
		for p, src := range files {
			if err := e.IndexFile(ctx, wt, p, []byte(src), 1, int64(len(src))); err != nil {
				t.Fatal(err)
			}
		}
		if err := e.Resolve(ctx, wt, noopProgress{}); err != nil {
			t.Fatal(err)
		}
	}
	index("/wtA", map[string]string{
		"auth/login.go":  "package auth\nfunc Login(){}",
		"api/handler.go": "package api\nfunc Handle(){ Login() }",
	})
	index("/wtB", map[string]string{
		"store/db.go":  "package store\nfunc Open(){}",
		"web/serve.go": "package web\nfunc Serve(){ Open() }",
	})
	arch, err := e.Architecture(ctx, "/wtA", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(moduleNamesOf(arch), ","); got != "api,auth" {
		t.Errorf("modules = %q, want api,auth", got)
	}
	if len(arch.Deps) != 1 || arch.Deps[0].From != "api" || arch.Deps[0].To != "auth" {
		t.Errorf("deps = %+v, want only api → auth", arch.Deps)
	}
}
