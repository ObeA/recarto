# Tool guidance with deferred tools: a Claude Code trial

## Question

Claude Code defers MCP tools when many are configured: the agent sees their names and must load one (`ToolSearch`) before calling it. Does the agent load and call Recarto's tools more often when Recarto's text calls them tools of the `recarto` MCP server, says how to load them, and the server returns `instructions` in its `initialize` reply?

## Setup

- Arms: `old` is tag 1.8.0; `new` is this branch (`f3d374d`). Both built with `go build ./cmd/recarto`.
- Repository: a standalone clone at `2bec1b8`, the commit the tasks' expected answers are about. It has no `CLAUDE.md`, `AGENTS.md` or `.mcp.json` of its own.
- Install per arm, as a user would: a fresh `HOME`, then `recarto init --agent claude --scope user`. That writes the MCP server to `~/.claude.json`, the hooks to `~/.claude/settings.json` and the instructions block to `~/.claude/CLAUDE.md`. The script adds `RECARTO_CACHE_DIR` and a missing `RECARTO_CONFIG` to the server entry, so every feature is on. No other MCP server, hook or instructions file is configured.
- Each run starts from a copy of an index the arm built beforehand (`recarto index`), and the copy is removed afterwards.
- Claude Code 2.1.323, `claude -p` with `--model claude-sonnet-5-5 --max-turns 30 --output-format stream-json --verbose --include-hook-events --no-session-persistence --allowedTools mcp__recarto,Bash`, `ENABLE_TOOL_SEARCH=true`. No `--bare`, so hooks and `CLAUDE.md` load; built-in tools stay on.
- Prompt: the question, and a request to end with an `ANSWER:` line. It names neither Recarto nor any tool.
- Tasks: T01–T13 of the [tool-definition trial](2026-11-14-tool-definitions-trial.py), graded the same way. T14 needs a staged graph delta and is left out. Two runs per task and arm: 52 runs.

Checked in every run's `init` message: the only MCP server is `recarto`, connected, and the tools include `ToolSearch` and the 20 `mcp__recarto__*` names. No run needed a retry. The new arm's `CLAUDE.md` holds the new "tools of the `recarto` MCP server" sentence, and its hook output in all 26 runs calls the tools "recarto MCP tool"; the old arm's does neither.

## Measures

From the stream-json of each run:

- load: a `ToolSearch` result holding a `tool_reference` to an `mcp__recarto__*` tool;
- Recarto calls: `tool_use` blocks named `mcp__recarto__*`;
- other calls: `tool_use` blocks named `Read`, `Grep`, `Glob` or `Bash`;
- root and subagent calls apart, by `parent_tool_use_id`;
- session start points at tools: the SessionStart hook's output contains `record_memory`.

## Results

| Arm | Runs | Runs with a load | Runs with a Recarto call | Recarto calls (root/sub) | Read/Grep/Glob/Bash calls (root/sub) | Solved | Session start points at tools | Cost (USD) |
|---|---|---|---|---|---|---|---|---|
| old (1.8.0) | 26 | 2 | 2 | 2 (2/0) | 41 (41/0) | 26 | 26 | 0.62 |
| new | 26 | 2 | 2 | 2 (2/0) | 42 (42/0) | 25 | 26 | 0.67 |

Per run, loaded tools / Recarto calls / other calls / solved:

| Run | old | new |
|---|---|---|
| T01-1 | 0 / 0 / 4 / yes | 0 / 0 / 4 / yes |
| T01-2 | 0 / 0 / 3 / yes | 0 / 0 / 4 / yes |
| T02-1 | 0 / 0 / 4 / yes | 0 / 0 / 2 / yes |
| T02-2 | 0 / 0 / 3 / yes | 0 / 0 / 2 / yes |
| T03-1 | 0 / 0 / 1 / yes | 0 / 0 / 1 / yes |
| T03-2 | 1 / 1 / 0 / yes | 0 / 0 / 1 / yes |
| T04-1 | 0 / 0 / 2 / yes | 0 / 0 / 2 / yes |
| T04-2 | 0 / 0 / 2 / yes | 0 / 0 / 2 / yes |
| T05-1 | 0 / 0 / 1 / yes | 1 / 1 / 1 / yes |
| T05-2 | 1 / 1 / 0 / yes | 1 / 1 / 1 / yes |
| T06-1 | 0 / 0 / 2 / yes | 0 / 0 / 2 / yes |
| T06-2 | 0 / 0 / 2 / yes | 0 / 0 / 2 / yes |
| T07-1 | 0 / 0 / 2 / yes | 0 / 0 / 2 / yes |
| T07-2 | 0 / 0 / 2 / yes | 0 / 0 / 2 / yes |
| T08-1 | 0 / 0 / 1 / yes | 0 / 0 / 1 / yes |
| T08-2 | 0 / 0 / 1 / yes | 0 / 0 / 1 / yes |
| T09-1 | 0 / 0 / 1 / yes | 0 / 0 / 1 / yes |
| T09-2 | 0 / 0 / 1 / yes | 0 / 0 / 1 / yes |
| T10-1 | 0 / 0 / 1 / yes | 0 / 0 / 1 / yes |
| T10-2 | 0 / 0 / 1 / yes | 0 / 0 / 1 / no |
| T11-1 | 0 / 0 / 1 / yes | 0 / 0 / 2 / yes |
| T11-2 | 0 / 0 / 2 / yes | 0 / 0 / 2 / yes |
| T12-1 | 0 / 0 / 2 / yes | 0 / 0 / 2 / yes |
| T12-2 | 0 / 0 / 2 / yes | 0 / 0 / 2 / yes |
| T13-1 | 0 / 0 / 0 / yes | 0 / 0 / 0 / yes |
| T13-2 | 0 / 0 / 0 / yes | 0 / 0 / 0 / yes |

First-load queries: old `select:mcp__recarto__get_file_outline` and `select:mcp__recarto__trace_call_path`; new `select:mcp__recarto__trace_call_path`. Every load was followed by a call of the loaded tool. T13 needs no tool call in either arm: the session-start code map holds the answer. The one unsolved run (new T10-2) left `SyncReport` out of a list it found with a shell `grep`.

## Reading

The new text did not make the agent load or call Recarto's tools more often on these tasks: 2 of 26 runs in each arm. The tasks are small lookups the agent finished in 0–4 calls, mostly with one `Grep` or `Bash` grep, so there are few moments where a pointer could change its choice. The trial does not measure longer sessions or subagents, where the 1.7.0 report found the gap.

Session start pointed at the tools in all 26 new-arm runs: the session's own `recarto serve` had marked the repo by the time the SessionStart hook formatted its block, so the start-up race did not drop a pointer here.

## Reproduce

```sh
git worktree add /tmp/old-src 1.8.0 && (cd /tmp/old-src && go build -o /tmp/recarto-old ./cmd/recarto)
go build -o /tmp/recarto-new ./cmd/recarto
git clone . /tmp/snap && git -C /tmp/snap checkout 2bec1b8
APP_ANTHROPIC_API_KEY=… python3 -I docs/validation/2026-11-17-tool-guidance-trial.py \
    --old /tmp/recarto-old --new /tmp/recarto-new --snapshot /tmp/snap --out /tmp/tool-guidance
```

The script prints both tables and writes every run's stream-json and `results.json` under `--out`.
