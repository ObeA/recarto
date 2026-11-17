package app

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gerwim/recarto/internal/gitctx"
	"github.com/gerwim/recarto/internal/graph"
)

// Prunable is what a worktree sweep drops the graph of.
type Prunable struct {
	Dead   []string            // stored worktrees whose directory is gone
	Unused []graph.WorktreeUse // stored worktrees last used more than Days days ago, oldest first
	Days   int                 // RECARTO_UNUSED_WORKTREE_DAYS; 0 = unused worktrees are kept
	at     time.Time           // when the lists were taken
	cutoff time.Time           // at − Days days
}

// maxUnusedWorktreeDays bounds RECARTO_UNUSED_WORKTREE_DAYS so the cutoff
// computation stays within time.Duration's range.
const maxUnusedWorktreeDays = 36500

// PrunableWorktrees returns the stored worktrees whose graph a sweep drops.
//
// A worktree is dead only when BOTH hold: git's authoritative list
// (`git worktree list --porcelain`) does not name it, AND its path is gone
// from disk — so a path-form mismatch (symlinked roots) or a temporarily
// unavailable list can never condemn a live worktree. A worktree is unused when
// its own use mark (or, before any mark, its last index time) is older than the
// cutoff; activity marks the same path string it is stored under, so a path-form
// mismatch cannot make a used worktree look unused. With no authoritative list
// (non-git, or the command fails) it reports none of either: pruning is an
// optimization, guessing is not. The current worktree is never in either list.
func (a *App) PrunableWorktrees(ctx context.Context) (Prunable, error) {
	days, err := unusedWorktreeDays()
	if err != nil {
		return Prunable{}, err
	}
	p := Prunable{Days: days, at: a.Clock.Now()}
	p.cutoff = p.at.Add(-time.Duration(days) * 24 * time.Hour)
	if a.Git.NonGit {
		return p, nil
	}
	livePaths, err := gitctx.LiveWorktrees(a.Runner)
	if err != nil || len(livePaths) == 0 {
		return p, nil
	}
	live := make(map[string]bool, len(livePaths))
	for _, lp := range livePaths {
		live[lp] = true
	}
	stored, err := a.Graph.Worktrees(ctx)
	if err != nil {
		return p, err
	}
	dead := map[string]bool{}
	for _, wt := range stored {
		if live[wt] || wt == a.Git.Worktree {
			continue
		}
		if _, statErr := os.Stat(wt); os.IsNotExist(statErr) {
			p.Dead = append(p.Dead, wt)
			dead[wt] = true
		}
	}
	if days == 0 {
		return p, nil
	}
	unused, err := a.Graph.WorktreesUsedBefore(ctx, p.cutoff)
	if err != nil {
		return p, err
	}
	for _, u := range unused {
		if u.Worktree != a.Git.Worktree && !dead[u.Worktree] {
			p.Unused = append(p.Unused, u)
		}
	}
	return p, nil
}

// PruneWorktrees drops the graph of every worktree in p and returns how many it
// pruned. A worktree used again since p was taken keeps the graph it still has.
func (a *App) PruneWorktrees(ctx context.Context, p Prunable) (int, error) {
	n := 0
	prune := func(wt string, usedBefore time.Time) error {
		ok, err := a.Graph.PruneWorktree(ctx, wt, usedBefore)
		if ok {
			n++
		}
		return err
	}
	var err error
	for _, wt := range p.Dead {
		if err = prune(wt, p.at); err != nil {
			break
		}
	}
	if err == nil {
		for _, u := range p.Unused {
			if err = prune(u.Worktree, p.cutoff); err != nil {
				break
			}
		}
	}
	if n > 0 {
		// The prune is committed; a failed checkpoint only leaves the WAL for the next one.
		_ = a.Store.Checkpoint(ctx)
	}
	return n, err
}

// unusedWorktreeDays reads RECARTO_UNUSED_WORKTREE_DAYS: unset or blank → 7;
// a whole number from 0 to maxUnusedWorktreeDays → that; anything else → error.
func unusedWorktreeDays() (int, error) {
	v := strings.TrimSpace(os.Getenv("RECARTO_UNUSED_WORKTREE_DAYS"))
	if v == "" {
		return 7, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 || n > maxUnusedWorktreeDays {
		return 0, fmt.Errorf("RECARTO_UNUSED_WORKTREE_DAYS=%q: want a whole number of days from 0 to %d (0 keeps unused worktrees)", v, maxUnusedWorktreeDays)
	}
	return n, nil
}
