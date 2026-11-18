package graph

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/gerwim/recarto/internal/store"
)

// pruneBatchFiles is how many files each prune transaction tears down. Batched
// commits keep the write lock short and make the prune resumable: a worktree
// holds a full graph copy (easily 100k+ rows), and a single transaction would
// both stall readers for its whole duration and lose everything to one
// cancellation — the same trap the per-index rebuild fix closed.
const pruneBatchFiles = 256

// usedKeyPrefix prefixes the meta key holding when Recarto last used a worktree (unix ms).
const usedKeyPrefix = "worktree_used:"

// errWorktreeUsed stops a prune whose worktree was used again since the sweep chose it.
var errWorktreeUsed = errors.New("worktree used since the prune began")

// WorktreeUse is a worktree with indexed files and when Recarto last used it.
type WorktreeUse struct {
	Worktree string
	LastUsed time.Time
}

// Worktrees returns the distinct worktrees with indexed files in this store.
func (e *Engine) Worktrees(ctx context.Context) ([]string, error) {
	rows, err := e.st.DB.QueryContext(ctx, `SELECT DISTINCT worktree FROM files`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var wt string
		if err := rows.Scan(&wt); err != nil {
			return nil, err
		}
		out = append(out, wt)
	}
	return out, rows.Err()
}

// MarkUsed records that Recarto is working in worktree now (e.clk.Now()).
// The mark never moves backwards: a write landing late from a process whose
// clock read was earlier must not hide a newer use from the prune guard.
func (e *Engine) MarkUsed(ctx context.Context, worktree string) error {
	now := e.clk.Now().UnixMilli()
	return e.st.WriteTx(ctx, func(c store.Conn) error {
		_, err := c.ExecContext(ctx,
			`INSERT INTO meta(key, value) VALUES (?, ?)
			 ON CONFLICT(key) DO UPDATE SET value = excluded.value
			 WHERE CAST(meta.value AS INTEGER) < CAST(excluded.value AS INTEGER)`,
			usedKeyPrefix+worktree, strconv.FormatInt(now, 10))
		return err
	})
}

// usedSince reports whether worktree's use mark is at or after t. It is the
// one rule both the prune guard and WorktreesUsedBefore (its negation) apply.
func usedSince(ctx context.Context, c store.Conn, worktree string, t time.Time) (bool, error) {
	var v int64
	err := c.QueryRowContext(ctx,
		`SELECT CAST(value AS INTEGER) FROM meta WHERE key=?`, usedKeyPrefix+worktree).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return v >= t.UnixMilli(), nil
}

// WorktreesUsedBefore returns the worktrees with indexed files last used before
// cutoff, oldest first. A worktree never marked counts from its newest file index time.
func (e *Engine) WorktreesUsedBefore(ctx context.Context, cutoff time.Time) ([]WorktreeUse, error) {
	// last_used < cutoff is the negation of usedSince: a worktree used exactly
	// at the cutoff is kept, one used a millisecond earlier is selected.
	rows, err := e.st.DB.QueryContext(ctx,
		`SELECT worktree, last_used FROM (
		   SELECT w.worktree,
		          COALESCE((SELECT CAST(value AS INTEGER) FROM meta WHERE key = ?1 || w.worktree),
		                   (SELECT MAX(last_indexed_at) FROM files WHERE worktree = w.worktree)) AS last_used
		   FROM (SELECT DISTINCT worktree FROM files) w)
		 WHERE last_used < ?2 ORDER BY last_used`,
		usedKeyPrefix, cutoff.UnixMilli())
	if err != nil {
		return nil, err
	}
	// rows.Err reports any read failure; closing a read-only cursor has nothing to lose.
	defer func() { _ = rows.Close() }()
	var out []WorktreeUse
	for rows.Next() {
		var u WorktreeUse
		var ms int64
		if err := rows.Scan(&u.Worktree, &ms); err != nil {
			return nil, err
		}
		u.LastUsed = time.UnixMilli(ms)
		out = append(out, u)
	}
	return out, rows.Err()
}

// PruneWorktree removes worktree's graph from the store. It stops, reporting
// false, once Recarto marks the worktree used at or after usedBefore.
//
// The prune is a series of self-contained transactions (pruneStep), so it stays
// batched and safe to interrupt: every committed step leaves a worktree that
// re-indexes cleanly, and one with files left is still listed, so the next
// sweep resumes it. Memories, sessions and every non-graph table stay.
func (e *Engine) PruneWorktree(ctx context.Context, worktree string, usedBefore time.Time) (bool, error) {
	for {
		done, err := e.pruneStep(ctx, worktree, usedBefore, pruneBatchFiles)
		if errors.Is(err, errWorktreeUsed) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if done {
			return true, nil
		}
	}
}

// pruneStep runs one prune transaction and reports done once worktree has no files left.
//
// It first checks nobody came back to the worktree, so a returning session's
// mark stops the prune before it writes anything. It then drops the freshness
// token, cached code map and change journal before any file: a prune cut short
// leaves a worktree that re-indexes instead of one that looks fresh, and whose
// re-created files journal nothing. The last batch drops the remaining meta in
// the same transaction, so no committed state has the files gone and the meta
// pending.
func (e *Engine) pruneStep(ctx context.Context, worktree string, usedBefore time.Time, batch int) (bool, error) {
	done := false
	err := e.st.WriteTx(ctx, func(c store.Conn) error {
		used, err := usedSince(ctx, c, worktree, usedBefore)
		if err != nil {
			return err
		}
		if used {
			return errWorktreeUsed
		}
		if _, err := c.ExecContext(ctx, `DELETE FROM meta WHERE key IN (?, ?)`,
			graphTokenKey+worktree, archCacheKey+worktree); err != nil {
			return err
		}
		if _, err := c.ExecContext(ctx, `DELETE FROM graph_deltas WHERE worktree=?`, worktree); err != nil {
			return err
		}
		ids, err := fileIDBatch(ctx, c, worktree, batch)
		if err != nil {
			return err
		}
		if len(ids) > 0 {
			args := make([]any, len(ids))
			ph := make([]string, len(ids))
			for i, id := range ids {
				args[i], ph[i] = id, "?"
			}
			in := strings.Join(ph, ",")
			for _, fts := range []string{"nodes_fts", "nodes_fts_stem", "nodes_fts_tri"} {
				if _, err := c.ExecContext(ctx, `DELETE FROM `+fts+` WHERE rowid IN (
					SELECT fts_rowid FROM node_fts_rows
					WHERE node_id IN (SELECT id FROM nodes WHERE file_id IN (`+in+`)))`, args...); err != nil {
					return err
				}
			}
			if _, err := c.ExecContext(ctx, `DELETE FROM files WHERE id IN (`+in+`)`, args...); err != nil {
				return err
			}
		}
		var left bool
		if err := c.QueryRowContext(ctx,
			`SELECT EXISTS (SELECT 1 FROM files WHERE worktree=?)`, worktree).Scan(&left); err != nil {
			return err
		}
		if left {
			return nil
		}
		if _, err := c.ExecContext(ctx, `DELETE FROM meta WHERE key IN (?, ?)`,
			resolvePendingKey(worktree), usedKeyPrefix+worktree); err != nil {
			return err
		}
		done = true
		return nil
	})
	return done, err
}

// fileIDBatch returns up to batch file ids of worktree.
func fileIDBatch(ctx context.Context, c store.Conn, worktree string, batch int) ([]string, error) {
	rows, err := c.QueryContext(ctx,
		`SELECT id FROM files WHERE worktree=? LIMIT ?`, worktree, batch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
