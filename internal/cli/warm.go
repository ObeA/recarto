package cli

import (
	"context"
	"errors"

	"github.com/gerwim/recarto/internal/app"
	"github.com/gerwim/recarto/internal/detach"
	"github.com/gerwim/recarto/internal/graph"
)

// Warm brings the code graph of every served repo up to date, caches its code
// map, and drops the graph of dead and unused worktrees. Session start runs it
// in a detached process, so the hook never waits for an index.
func Warm(ctx context.Context, a *app.App) error {
	var errs []error
	for _, rc := range a.AllRepos() {
		wt := rc.Git.Worktree
		if err := rc.Graph.ReconcileIfStale(ctx, wt, graph.NewSource(wt), graph.WorktreeToken(wt)); err != nil {
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
	return errors.Join(append(errs, err)...)
}

// startWarm starts `recarto warm` in a detached process. A var so tests can
// keep a hook from starting the test binary.
var startWarm = func() error { return detach.Start(binaryPath(), "warm") }
