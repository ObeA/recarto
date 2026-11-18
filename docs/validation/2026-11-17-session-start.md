# Session start without waiting for the index

Session start now hands the graph work to a background `recarto warm` and shows
"The code graph is still being indexed; graph results may be incomplete." instead
of the code map while the worktree's index is unfinished. This report compares it
with 1.8.0 on a large repo.

## Method

- Machine: 4-CPU Intel Xeon @ 3.80 GHz VM, 4 GB RAM, Linux 6.12. Every command,
  the background `recarto warm` included, is pinned to CPUs 0-1 (a 2-CPU budget).
- Binaries: base is tag `1.8.0` (`81ab5b7`), new is `ed01433`. Both built with Go 1.26.4.
- Repo: `ChilliCream/graphql-platform` at `6d25fa3` (GitHub's archive of that
  commit, committed into a local repo): 10,113 indexed files, 130,433 symbols.
  Every worktree is made with `git worktree add` from it, so the worktrees of one
  binary share one store.
- Remote sync off, `RECARTO_REPOS` and `RECARTO_WORKSPACE` unset, an empty config
  file (all features on), one cache dir per binary.
- Script: [`2026-11-17-session-start-bench.sh`](2026-11-17-session-start-bench.sh)
  (hyperfine 1.19.0).

## Results

### Fresh, never-indexed worktree

One `recarto hook session-start` per run, each in a new worktree; the next run
starts once the previous background index is done.

| Run | 1.8.0 | new |
|---|---|---|
| 1 | 146.7 s | 0.56 s |
| 2 | 234.3 s | 0.52 s |
| 3 | 346.8 s | 0.42 s |
| 4 | 400.9 s | 0.34 s |
| 5 | 571.0 s | 0.52 s |
| **Median** | **346.8 s** | **0.52 s** |

1.8.0 gets slower with each run: each run leaves one more partly indexed worktree
in the store, and its code map reads every worktree. Its block had a partial map:
in run 1, 8 modules, no `website` module and no dependency lines. Every new block
had the indexing line and no code map.

Verdict: **pass** (new ≤ 3 s, with the indexing line and no map).

### The background index completes

| Run | Index done after the hook started | Next session start |
|---|---|---|
| 1 | 57.7 s | full code map |
| 2 | 58.1 s | full code map |
| 3 | 59.2 s | full code map |
| 4 | 57.3 s | full code map |
| 5 | 57.1 s | full code map |

The full map has 12 modules (`website` included) and 8 dependency lines.

Verdict: **pass**.

### Two session starts at the same moment

Two new-binary session starts in one fresh worktree, started together. After both
background processes ended, `recarto status` showed `graph_index_runs 1`.

Verdict: **pass** (one index run).

### Code map build time, 1 vs 5 indexed worktrees

`recarto graph arch` in a fully indexed worktree, code-map cache emptied before
each of 5 runs. Medians:

| Store holds | 1.8.0 | new |
|---|---|---|
| 1 worktree | 0.86 s | 1.02 s |
| 5 worktrees | 3.06 s | 1.02 s |
| Ratio | 3.56 | 1.00 |

With one worktree the new map takes 0.16 s longer: it looks rows up by file
instead of scanning the tables. It is built in the background after each index,
so a session start reads it from the cache.

Verdict: **pass** (new 5-worktree time is 1.00 × the 1-worktree time, limit 1.2).

### Fully indexed, unchanged worktree

`recarto hook session-start` in a fully indexed, unchanged worktree of the
5-worktree store, code-map cache filled, 10 runs, a fresh session id each.

| | 1.8.0 | new |
|---|---|---|
| Median | 120 ms | 76 ms |

The new time does not include the background `recarto warm` it starts, which
finds the graph fresh and exits.

Verdict: **pass** (new ≤ 1.8.0).
