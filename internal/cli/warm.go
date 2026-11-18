package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/gerwim/recarto/internal/app"
	"github.com/gerwim/recarto/internal/detach"
	"github.com/gerwim/recarto/internal/graph"
)

// Warm brings the code graph of every served repo up to date, caches its code
// map, and drops the graph of dead and unused worktrees. Session start runs it
// in a detached process, so the hook never waits for an index. A worktree that
// another process is indexing is left to that process.
func Warm(ctx context.Context, a *app.App) error {
	var errs []error
	for _, rc := range a.AllRepos() {
		wt := rc.Git.Worktree
		// The token is read under the index lock: a git call that hangs there keeps
		// the lock, so later runs leave this worktree instead of hanging in it too.
		err := rc.Graph.ReconcileIfIdle(ctx, wt, graph.NewSource(wt), func() string { return graph.WorktreeToken(wt) })
		if errors.Is(err, graph.ErrIndexing) {
			// The process indexing this worktree finishes it, and the next
			// session start caches its map.
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		// Building the map here spares the next session start from building it.
		if _, err := rc.Graph.Architecture(ctx, wt, ""); err != nil {
			errs = append(errs, err)
		}
	}
	p, err := a.PrunableWorktrees(ctx)
	if err == nil {
		_, err = a.PruneWorktrees(ctx, p)
	}
	return errors.Join(append(errs, err, p.DaysErr)...)
}

// warmLimit bounds one `recarto warm` run.
const warmLimit = 30 * time.Minute

// armWarmLimit calls f once warmLimit has passed, unless the returned stop runs
// first. A var so a test can fire the limit at a moment it picks.
var armWarmLimit = func(f func()) (stop func() bool) { return time.AfterFunc(warmLimit, f).Stop }

// endWarm ends a warm run that reached warmLimit. A git call or file read that
// hangs ignores any context, so only ending the process bounds the run. The
// store and the index lock survive this the same way they survive a crash: what
// is committed stays, and the next run continues from it.
func endWarm() {
	fmt.Fprintf(os.Stderr, "recarto warm: stopped after %v; the next session start continues the index\n", warmLimit)
	os.Exit(1)
}

// startWarm starts `recarto warm` in a detached process. A var so tests can
// keep a hook from starting the test binary.
var startWarm = func() error { return detach.Start(binaryPath(), "warm") }
