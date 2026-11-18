"""Old-vs-new trial of the tool guidance Recarto gives an agent whose tools are deferred.

Each task goes to a fresh `claude -p` agent with Recarto installed as a user would
(`recarto init --agent claude --scope user` in a fresh HOME): hooks, CLAUDE.md block
and MCP server, with every built-in tool on and the MCP tools deferred
(ENABLE_TOOL_SEARCH=true). The prompt names neither Recarto nor any tool, so a load
or a call comes from Recarto's text alone. See 2026-11-17-tool-guidance.md.

Usage:
  python3 -I 2026-11-17-tool-guidance-trial.py --old BIN --new BIN --snapshot DIR --out DIR \
      [--model claude-sonnet-5-5] [--max-turns 30] [--repeats 2] [--tasks T01,T02]

Needs APP_ANTHROPIC_API_KEY; it is passed to claude only, as ANTHROPIC_API_KEY.
"""

import argparse
import importlib.util
import json
import os
import shutil
import subprocess
import sys
from concurrent.futures import ThreadPoolExecutor

_spec = importlib.util.spec_from_file_location(
    "tooldefs", os.path.join(os.path.dirname(os.path.abspath(__file__)), "2026-11-14-tool-definitions-trial.py"))
tooldefs = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(tooldefs)
clean_env, step, read_jsonl, result_of, grade = (
    tooldefs.clean_env, tooldefs.step, tooldefs.read_jsonl, tooldefs.result_of, tooldefs.grade)

# T14 needs a staged graph delta, which this trial's per-arm install does not prepare.
TASKS = [t for t in tooldefs.TASKS if t[0] != "T14"]

PROMPT = """Answer this question about the repository in the current directory. End your reply with a last line that starts with "ANSWER:" and holds only the answer.

Question: {question}"""

ARMS = ("old", "new")
PREFIX = "mcp__recarto__"
OTHER = ("Read", "Grep", "Glob", "Bash")


def install(arm, binary, snapshot, out):
    """Install Recarto for Claude Code in the arm's own HOME, as a user would."""
    home = os.path.join(out, arm, "home")
    prepared = os.path.join(out, arm, "prepared")
    if not os.path.isdir(home):
        os.makedirs(home)
        env = dict(clean_env(), HOME=home)
        env.pop("CLAUDE_CONFIG_DIR", None)
        step([binary, "init", "--agent", "claude", "--scope", "user"], snapshot, env)
        path = os.path.join(home, ".claude.json")
        with open(path) as f:
            cfg = json.load(f)
        cfg["mcpServers"]["recarto"]["env"] = {
            "RECARTO_CACHE_DIR": os.path.join(out, arm, "run-cache"),
            "RECARTO_CONFIG": os.path.join(out, "no-config.toml")}
        with open(path, "w") as f:
            json.dump(cfg, f, indent=2)
    if not os.path.isdir(prepared):
        step([binary, "index"], snapshot, tooldefs.recarto_env(out, prepared))
    return home, prepared


def run_one(task, arm, rep, home, prepared, args, key, out):
    arm_dir = os.path.join(out, arm)
    name = "%s-%d" % (task[0], rep)
    path = os.path.join(arm_dir, name + ".jsonl")
    if result_of(read_jsonl(path)) is not None:
        return
    cache = os.path.join(arm_dir, "run-cache")
    for attempt in (1, 2):
        shutil.rmtree(cache, ignore_errors=True)
        shutil.copytree(prepared, cache)
        env = dict(clean_env(), HOME=home, ANTHROPIC_API_KEY=key, ENABLE_TOOL_SEARCH="true",
                   RECARTO_CACHE_DIR=cache, RECARTO_CONFIG=os.path.join(out, "no-config.toml"))
        env.pop("CLAUDE_CONFIG_DIR", None)
        cmd = ["claude", "-p", PROMPT.format(question=task[2]), "--model", args.model,
               "--max-turns", str(args.max_turns), "--output-format", "stream-json", "--verbose",
               "--include-hook-events", "--no-session-persistence", "--allowedTools", "mcp__recarto,Bash"]
        with open(path, "w") as so, open(os.path.join(arm_dir, name + ".err"), "w") as se:
            try:
                subprocess.run(cmd, cwd=args.snapshot, env=env, stdin=subprocess.DEVNULL, stdout=so, stderr=se,
                               timeout=15 * 60)
            except subprocess.TimeoutExpired:
                se.write("\ntrial: timed out after 15 minutes\n")
        # A background `recarto warm` the hooks started may still hold the cache; a leftover is replaced next run.
        shutil.rmtree(cache, ignore_errors=True)
        init = init_of(read_jsonl(path))
        if init is not None and connected(init) or attempt == 2:
            break
        os.replace(path, path + ".attempt1")
        with open(os.path.join(arm_dir, name + ".retried"), "w") as f:
            f.write("retried once: the recarto MCP server was not connected\n")


def init_of(msgs):
    return next((m for m in msgs if m.get("type") == "system" and m.get("subtype") == "init"), None)


def connected(init):
    servers = init.get("mcp_servers", [])
    return [s.get("name") for s in servers] == ["recarto"] and servers[0].get("status") == "connected"


def blocks(msgs, role):
    for m in msgs:
        if m.get("type") != role:
            continue
        content = m.get("message", {}).get("content", [])
        if isinstance(content, list):
            for b in content:
                yield m, b


def tool_refs(value):
    """The tool names of every tool_reference block nested in value."""
    if isinstance(value, dict):
        if value.get("type") == "tool_reference":
            yield value.get("tool_name") or value.get("name") or ""
        for v in value.values():
            yield from tool_refs(v)
    elif isinstance(value, list):
        for v in value:
            yield from tool_refs(v)


def measure(task, msgs):
    g = grade(task, msgs)
    init = init_of(msgs) or {}
    uses = {b["id"]: (m, b) for m, b in blocks(msgs, "assistant") if b.get("type") == "tool_use"}
    calls = {"root": {"recarto": 0, "other": 0}, "sub": {"recarto": 0, "other": 0}}
    for m, b in uses.values():
        who = "sub" if m.get("parent_tool_use_id") else "root"
        if b["name"].startswith(PREFIX):
            calls[who]["recarto"] += 1
        elif b["name"] in OTHER:
            calls[who]["other"] += 1
    loaded, first_query = set(), None
    for _, b in blocks(msgs, "user"):
        if b.get("type") != "tool_result" or b.get("tool_use_id") not in uses:
            continue
        _, use = uses[b["tool_use_id"]]
        if use["name"] != "ToolSearch":
            continue
        got = {n for n in tool_refs(b.get("content")) if n.startswith(PREFIX)}
        if got and first_query is None:
            first_query = use.get("input", {}).get("query")
        loaded |= got
    start_out = " ".join(json.dumps(m) for m in msgs
                         if m.get("type") == "system" and m.get("subtype") == "hook_response"
                         and "SessionStart" in json.dumps(m))
    tools = init.get("tools", [])
    return {
        "solved": g["solved"], "answer": g["answer"], "turns": g["num_turns"], "cost_usd": g["cost_usd"],
        "connected": bool(init) and connected(init),
        "deferred": "ToolSearch" in tools,
        "loaded": len(loaded), "first_load_query": first_query,
        "recarto_calls": calls["root"]["recarto"] + calls["sub"]["recarto"],
        "other_calls": calls["root"]["other"] + calls["sub"]["other"],
        "calls": calls,
        "session_start_pointers": "record_memory" in start_out,
    }


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--old", required=True)
    ap.add_argument("--new", required=True)
    ap.add_argument("--snapshot", required=True)
    ap.add_argument("--out", required=True)
    ap.add_argument("--model", default="claude-sonnet-5-5")
    ap.add_argument("--max-turns", type=int, default=30)
    ap.add_argument("--repeats", type=int, default=2)
    ap.add_argument("--tasks")
    args = ap.parse_args()
    key = os.environ.get("APP_ANTHROPIC_API_KEY")
    if not key:
        sys.exit("APP_ANTHROPIC_API_KEY is not set")
    args.snapshot = os.path.abspath(args.snapshot)
    out = os.path.abspath(args.out)
    bins = {"old": os.path.abspath(args.old), "new": os.path.abspath(args.new)}
    tasks = TASKS
    if args.tasks:
        want = set(args.tasks.split(","))
        tasks = [t for t in TASKS if t[0] in want]
    installed = {arm: install(arm, bins[arm], args.snapshot, out) for arm in ARMS}

    # The arms share nothing, so a task's two arms run side by side.
    with ThreadPoolExecutor(len(ARMS)) as pool:
        for t in tasks:
            for rep in range(1, args.repeats + 1):
                print("running %s-%d" % (t[0], rep), flush=True)
                list(pool.map(lambda arm: run_one(t, arm, rep, *installed[arm], args, key, out), ARMS))

    results = {arm: {} for arm in ARMS}
    for arm in ARMS:
        for t in tasks:
            for rep in range(1, args.repeats + 1):
                name = "%s-%d" % (t[0], rep)
                r = measure(t, read_jsonl(os.path.join(out, arm, name + ".jsonl")))
                r["retried"] = os.path.exists(os.path.join(out, arm, name + ".retried"))
                results[arm][name] = r
    with open(os.path.join(out, "results.json"), "w") as f:
        json.dump(results, f, indent=1)

    print("| arm | runs | connected | deferred | runs with a load | runs with a Recarto call | Recarto calls "
          "(root/sub) | Read/Grep/Glob/Bash calls (root/sub) | solved | session start points at tools | cost (USD) |")
    print("|---|---|---|---|---|---|---|---|---|---|---|")
    for arm in ARMS:
        rs = list(results[arm].values())
        root_r = sum(r["calls"]["root"]["recarto"] for r in rs)
        sub_r = sum(r["calls"]["sub"]["recarto"] for r in rs)
        root_o = sum(r["calls"]["root"]["other"] for r in rs)
        sub_o = sum(r["calls"]["sub"]["other"] for r in rs)
        print("| %s | %d | %d | %d | %d | %d | %d (%d/%d) | %d (%d/%d) | %d | %d | %.2f |" % (
            arm, len(rs), sum(r["connected"] for r in rs), sum(r["deferred"] for r in rs),
            sum(r["loaded"] > 0 for r in rs), sum(r["recarto_calls"] > 0 for r in rs),
            root_r + sub_r, root_r, sub_r, root_o + sub_o, root_o, sub_o,
            sum(r["solved"] for r in rs), sum(r["session_start_pointers"] for r in rs),
            sum(r["cost_usd"] or 0 for r in rs)))
    print()
    print("| run | " + " | ".join("%s: loaded / Recarto calls / other calls / solved" % a for a in ARMS) + " |")
    print("|---|" + "---|" * len(ARMS))
    for name in results["old"]:
        print("| %s | %s |" % (name, " | ".join("%d / %d / %d / %s" % (
            results[a][name]["loaded"], results[a][name]["recarto_calls"], results[a][name]["other_calls"],
            "yes" if results[a][name]["solved"] else "no") for a in ARMS)))
    print()
    for arm in ARMS:
        print("%s first-load queries: %s" % (arm, sorted({str(r["first_load_query"]) for r in results[arm].values()
                                                          if r["first_load_query"]})))


if __name__ == "__main__":
    main()
