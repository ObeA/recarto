#!/usr/bin/env bash
# Session-start benchmark behind 2026-11-17-session-start.md.
#
#   docs/validation/2026-11-17-session-start-bench.sh <base-recarto> <new-recarto> <graphql-platform-clone> <out-dir>
#
# Needs hyperfine, taskset, python3 and git. The clone is a git repo of
# ChilliCream/graphql-platform at 6d25fa3; every worktree is made from it with
# `git worktree add`, so the worktrees of one binary share one store. Every
# command is pinned to CPUs 0-1 (the background `warm` inherits the pinning),
# remote sync is off, and each binary gets its own cache dir and an empty config
# file (all features on). Results go to <out-dir>/results.txt.
set -euo pipefail
BASE=$(realpath "$1") NEW=$(realpath "$2") GP=$(realpath "$3") OUT=$(realpath -m "$4")
W=$(mktemp -d)
mkdir -p "$OUT"
RES=$OUT/results.txt
: >"$RES"
unset RECARTO_REPOS RECARTO_WORKSPACE RECARTO_SYNC_URL RECARTO_SYNC_KEY RECARTO_SYNC_REPO
: >"$W/config.toml"
export RECARTO_CONFIG=$W/config.toml

bin() { if [ "$1" = base ]; then echo "$BASE"; else echo "$NEW"; fi; }
use() { export RECARTO_CACHE_DIR=$W/cache-$1; }
store() { ls -d "$RECARTO_CACHE_DIR"/[0-9a-f]*/store.db; }
wt() { git -C "$GP" worktree add -q --detach "$1"; }
drop() { git -C "$GP" worktree remove --force "$1"; }
log() { echo "$*" | tee -a "$RES"; }
ms() { date +%s%3N; }
# await_warm <recarto>: wait until no `<recarto> warm` process is left.
await_warm() { while pgrep -f "^$1 warm" >/dev/null; do sleep 0.2; done; }
payload() { printf '{"session_id":"%s","source":"startup"}' "$1"; }
INDEXING="The code graph is still being indexed"

# 4. Code-map build time, store with 1 vs 5 indexed worktrees, cache emptied.
# 5. Session start in a fully indexed, unchanged worktree (map cache filled).
for b in base new; do
	use scale-$b
	R=$(bin $b)
	for i in 1 2 3 4 5; do [ -d "$W/scale-$i" ] || wt "$W/scale-$i"; done
	(cd "$W/scale-1" && taskset -c 0,1 "$R" index >/dev/null)
	DB=$(store)
	# Empties the code-map cache before each timed run.
	CLEAR=$W/clear-$b
	printf '%s\n' '#!/usr/bin/env bash' \
		"exec python3 -c \"import sqlite3,sys; c=sqlite3.connect(sys.argv[1]); c.execute(\\\"DELETE FROM meta WHERE key LIKE 'arch_cache:%'\\\"); c.commit()\" $DB" >"$CLEAR"
	chmod +x "$CLEAR"
	(cd "$W/scale-1" && hyperfine -N --runs 5 --prepare "$CLEAR" --export-json "$OUT/arch-$b-1.json" \
		"taskset -c 0,1 $R graph arch")
	for i in 2 3 4 5; do (cd "$W/scale-$i" && taskset -c 0,1 "$R" index >/dev/null); done
	(cd "$W/scale-1" && hyperfine -N --runs 5 --prepare "$CLEAR" --export-json "$OUT/arch-$b-5.json" \
		"taskset -c 0,1 $R graph arch")

	(cd "$W/scale-1" && payload fill-$b | taskset -c 0,1 "$R" hook session-start >/dev/null)
	await_warm "$R"
	cat >"$W/prep-$b" <<EOF
#!/usr/bin/env bash
printf '{"session_id":"u-%s","source":"startup"}' "\$(date +%s%N)" >"$W/payload-$b.json"
while pgrep -f "^$R warm" >/dev/null; do sleep 0.2; done
EOF
	chmod +x "$W/prep-$b"
	(cd "$W/scale-1" && hyperfine --runs 10 --prepare "$W/prep-$b" --export-json "$OUT/unchanged-$b.json" \
		"taskset -c 0,1 $R hook session-start <$W/payload-$b.json")
	await_warm "$R"
	rm -rf "$RECARTO_CACHE_DIR"
done
for i in 1 2 3 4 5; do drop "$W/scale-$i"; done

# 1. Session start in a fresh, never-indexed worktree (5 runs, a new worktree each).
# 2. With the new binary: time until the background index is done, and the next
#    session start's block.
for i in 1 2 3 4 5; do
	for b in base new; do
		use fresh-$b
		R=$(bin $b)
		wt "$W/fresh-$b-$i"
		cd "$W/fresh-$b-$i"
		t0=$(ms)
		payload "f-$b-$i" | taskset -c 0,1 "$R" hook session-start >"$OUT/fresh-$b-$i.out"
		t1=$(ms)
		log "fresh $b run $i: hook ${t1}-${t0} = $((t1 - t0)) ms"
		if [ $b = new ]; then
			grep -q "$INDEXING" "$OUT/fresh-$b-$i.out" && log "  indexing line: yes" || log "  indexing line: NO"
			grep -q "Code map" "$OUT/fresh-$b-$i.out" && log "  code map: YES" || log "  code map: no"
			await_warm "$R"
			t2=$(ms)
			log "  background index done after $((t2 - t0)) ms"
			payload "f2-$b-$i" | taskset -c 0,1 "$R" hook session-start >"$OUT/fresh-$b-$i-next.out"
			await_warm "$R"
			grep -q "Code map (modules)" "$OUT/fresh-$b-$i-next.out" && log "  next block code map: yes" || log "  next block code map: NO"
		fi
		cd "$W"
		drop "$W/fresh-$b-$i"
	done
	# A store of fully indexed worktrees would fill the disk; keep one run's.
	rm -rf "$W/cache-fresh-new"
done

# 3. Two session starts at the same moment in one fresh worktree.
use conc
wt "$W/conc"
cd "$W/conc"
payload c1 | taskset -c 0,1 "$NEW" hook session-start >/dev/null &
payload c2 | taskset -c 0,1 "$NEW" hook session-start >/dev/null &
wait
await_warm "$NEW"
log "concurrent: $(taskset -c 0,1 "$NEW" status | grep graph_index_runs)"
cd "$W"
drop "$W/conc"

python3 - "$OUT" <<'EOF' | tee -a "$RES"
import json, statistics, sys
out = sys.argv[1]
def med(name):
    return statistics.median(json.load(open(f"{out}/{name}.json"))["results"][0]["times"])
for b in ("base", "new"):
    one, five = med(f"arch-{b}-1"), med(f"arch-{b}-5")
    print(f"code map {b}: 1 worktree {one:.2f} s, 5 worktrees {five:.2f} s, ratio {five/one:.2f}")
for b in ("base", "new"):
    print(f"unchanged session start {b}: median {med(f'unchanged-{b}')*1000:.0f} ms")
EOF
rm -rf "$W"
