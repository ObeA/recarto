//go:build !windows

package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/gerwim/recarto/internal/app"
)

// TestWarmLimitHelper is the `recarto warm` process TestWarmStopsAtItsLimit
// runs. Its limit fires when SIGUSR1 arrives, unless Run stopped it first.
func TestWarmLimitHelper(t *testing.T) {
	if os.Getenv("RECARTO_WARM_LIMIT_HELPER") != "1" {
		t.Skip("run by TestWarmStopsAtItsLimit")
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGUSR1)
	armWarmLimit = func(f func()) func() bool {
		// 0 armed, 1 stopped, 2 fired: whichever of stop and the signal claims
		// it first wins, as with time.Timer.
		var state atomic.Int32
		go func() {
			<-sig
			if state.CompareAndSwap(0, 2) {
				f()
			}
		}()
		return func() bool { return state.CompareAndSwap(0, 1) }
	}
	_ = Run([]string{"warm"}, io.Discard)
	t.Fatal("warm returned instead of being stopped")
}

// TestWarmStopsAtItsLimit: a warm run stuck in git ends at its limit, and the
// next run completes the index.
func TestWarmStopsAtItsLimit(t *testing.T) {
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	initGitRepo(t, repo, map[string]string{"a.go": "package p\n\nfunc A() {}\n"})
	for _, k := range []string{"RECARTO_WORKSPACE", "RECARTO_SYNC_URL", "RECARTO_SYNC_KEY", "RECARTO_SYNC_REPO", "RECARTO_REPOS"} {
		t.Setenv(k, "")
	}
	t.Setenv("RECARTO_CACHE_DIR", t.TempDir())
	t.Setenv("RECARTO_CONFIG", filepath.Join(t.TempDir(), "config.toml"))

	// The fake git fires the limit once the run is inside the index, then hangs.
	fakeDir := t.TempDir()
	script := "#!/bin/sh\nfor a in \"$@\"; do\n  if [ \"$a\" = ls-files ]; then\n    kill -USR1 $PPID\n    exec sleep 3600\n  fi\ndone\nexec " + realGit + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(fakeDir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestWarmLimitHelper$")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "RECARTO_WARM_LIMIT_HELPER=1", "PATH="+fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Reaps the orphaned sleep along with the helper.
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(stderr.String(), "stopped after") {
			t.Fatalf("helper = %v, stderr %q; want a non-zero exit saying it stopped", err, stderr.String())
		}
	case <-time.After(time.Minute):
		t.Fatal("warm was not stopped at its limit")
	}

	a, err := app.Open(app.Config{CWD: repo})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	ctx := context.Background()
	if err := Warm(ctx, a); err != nil {
		t.Fatal(err)
	}
	_, finished, err := a.Graph.CodeMap(ctx, a.Git.Worktree, false)
	if err != nil {
		t.Fatal(err)
	}
	if !finished {
		t.Error("index unfinished after the next warm")
	}
	if got := filesIn(t, a, a.Git.Worktree); got != 1 {
		t.Errorf("indexed files = %d, want 1", got)
	}
}
