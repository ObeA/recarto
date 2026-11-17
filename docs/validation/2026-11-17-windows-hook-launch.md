# Hook launch on Windows

## Summary

On Windows, `recarto init` now writes Claude Code's hooks in exec form: Claude Code
starts `recarto.exe` itself, with no shell in between. Exec form is the only launch
that works with and without Git Bash and with a Recarto path that contains spaces,
and it removes the shell start. In the 1.7.0 Windows figures, stop takes 23 ms
started directly against 338 ms through PowerShell, and post-read of a small file
33 ms against 402 ms.

Exec form needs Claude Code 2.1.139 or later.

## How Claude Code starts a command hook on Windows

Sources:

- Hooks docs, <https://code.claude.com/docs/en/hooks>: the command hook fields
  `shell` and `args`, and the section "Exec form and shell form".
- Changelog, <https://raw.githubusercontent.com/anthropics/claude-code/main/CHANGELOG.md>:
  - 2.1.139 (published to npm on 2026-05-11): "Added hook `args: string[]` field
    (exec form) that spawns the command directly without a shell, so path
    placeholders never need quoting".
  - 2.1.47: "Fixed hooks … on Windows by using Git Bash instead of cmd.exe".
- The hook runner of Claude Code 2.1.326 (2026-11-16), read in the package
  `@anthropic-ai/claude-code-win32-x64@2.1.326`. Its `claude.exe` embeds the
  JavaScript as plain text. To re-check: `npm pack
  @anthropic-ai/claude-code-win32-x64@2.1.326`, extract it, and search the bytes of
  `claude.exe` for `args`, `windowsHide` and `Git Bash`. Do not run it.

Everything in this section comes from reading that code and the docs; nothing here
was run on Windows.

**Exec form** (the handler has `args`): `child_process.spawn(command, args, {env,
cwd, windowsHide: true})`. No shell runs, `shell` is ignored, and no Git Bash or
PowerShell lookup happens. An absolute `command` is used as is; the only warning is
for a bare command name that contains whitespace.

**Shell form** (no `args`): the shell is `shell` if set, else `bash` when Git Bash
is found, else `powershell`. Git Bash is looked up in this order:

1. `CLAUDE_CODE_GIT_BASH_PATH` (its file name must be `bash.exe`, `sh.exe`, `bash`
   or `sh`, and the file must exist);
2. `C:\Program Files\Git\bin\bash.exe`;
3. `C:\Program Files (x86)\Git\bin\bash.exe`;
4. `<folder of git on PATH>\..\..\bin\bash.exe`.

- `bash` runs `bash.exe -c "<command>"`. With `"shell": "bash"` and no Git Bash,
  Claude Code refuses the hook: `Hook … requires bash but Git Bash was not found`.
  Bash drops the backslashes of an unquoted `C:\…` path (`C:Users…recarto.exe:
  command not found`).
- `powershell` runs `pwsh` from PATH, else Windows PowerShell, with `-NoProfile
  -NonInteractive -ExecutionPolicy Bypass -Command "<command>` + newline + `; if
  (-not $?) { if ($LASTEXITCODE -eq 2) { exit 2 } }"`. An unquoted path with spaces
  fails (`C:\Users\Jane` is not a command); `& '<path>' …` works.

**Input and output**: Claude Code writes the hook's JSON and a newline to the
child's stdin and closes it, and reads stdout and stderr as UTF-8. A PostToolUse,
SessionStart or Stop hook never blocks the session on a failed start or an exit
code other than 2.

## Candidates

| | `settings.json` handler | With Git Bash | Without Git Bash | Path with spaces | Stdin and stdout intact | Started before recarto |
|---|---|---|---|---|---|---|
| **A** default shell, unquoted (Recarto before June 2026) | `"command": "C:\…\recarto.exe hook stop --agent claude"` | no: backslashes dropped | yes, without spaces | no | yes | Git Bash or PowerShell |
| **B** PowerShell, unquoted (Recarto 1.7–1.8) | A + `"shell": "powershell"` | yes | yes | **no** | yes | PowerShell |
| **C** PowerShell, quoted | `"command": "& 'C:\…\recarto.exe' hook stop --agent claude", "shell": "powershell"` | yes | yes | yes | yes | PowerShell (~330 ms) |
| **D** Git Bash, quoted | `"command": "'C:\…\recarto.exe' hook stop --agent claude", "shell": "bash"` | yes | **no**: Claude Code refuses the hook | yes | yes | Git Bash |
| **E** default shell, one command for both shells | — | no | no | no | — | — |
| **F** exec form | `"command": "C:\…\recarto.exe", "args": ["hook", "stop", "--agent", "claude"]` | yes | yes | yes | yes | nothing |

- **B** is what `recarto init` wrote until now, so a Windows install whose
  `recarto.exe` path has spaces did not work.
- **E** cannot be written: `'<path>'` is a parse error in PowerShell when followed
  by arguments, and `& '<path>'` is a syntax error in bash, so no quoting works in
  both.
- For A–D, the shell's child inherits Claude Code's pipes; the 1.7.0 report shows
  replies arriving through PowerShell. For F, stdin and stdout are Claude Code's
  pipes to recarto itself.
- cmd is not a `shell` value, and Claude Code moved hooks off cmd.exe in 2.1.47.
  Started in exec form it would only add a process start, so it is not a candidate.

## Console window

Claude Code passes `windowsHide: true` for every form. With F, `recarto.exe` gets
the hidden console that `powershell.exe` used to get. The git processes and the
detached `recarto warm` share it, as before (see
[`internal/detach/detach_windows.go`](../../internal/detach/detach_windows.go)).

## Never blocks

- Recarto's Claude Code hooks are PostToolUse, SessionStart and Stop hooks. Claude
  Code never blocks on a failed start or an exit code other than 2 for these.
- `recarto hook` always exits 0.
- A moved or deleted binary is a "failed to spawn" notice, which does not block,
  the same as a shell's "not found" before.

## Cost

From the 1.7.0 report (Windows 11, large repo, median of five runs). "Alone" is
`recarto.exe` started directly, which is what F does; "through pwsh" is B or C.

| Hook | Alone (F) | Through pwsh (B, C) | Saved |
|---|---|---|---|
| post-edit, `.cs` file | 166 ms | 561 ms | 395 ms |
| post-edit, file outside the repo | 21 ms | 368 ms | 347 ms |
| augment, identifier pattern | 683 ms | 912 ms | 229 ms |
| augment, regex pattern | 388 ms | 686 ms | 298 ms |
| post-read, large file | 66 ms | 316 ms | 250 ms |
| post-read, small file | 33 ms | 402 ms | 369 ms |
| shell, `grep` | 617 ms | 974 ms | 357 ms |
| stop | 23 ms | 338 ms | 315 ms |

Git Bash (D) was not measured there; the bench below measures it. Its figure does
not change the choice, because D fails without Git Bash.

## Choice and its limits

- Exec form (F) on Windows. Linux and macOS hooks are written exactly as before.
- Claude Code 2.1.139 or later is needed. An older version most likely drops the
  unknown `args` field and runs the bare `command` through its default shell. This
  is inferred from the hook schema, not verified. With Git Bash that is "command not
  found"; without it, PowerShell runs `recarto.exe` alone, which prints its usage
  and exits 0 (at SessionStart that text is added to the context), or exits 1 when
  the path has spaces. Neither blocks a tool call.
- Re-running `recarto init` moves hooks written in shell form (with or without
  `"shell": "powershell"`) to exec form. Each hook ends up there once, and other
  tools' hooks are kept as they are.
- Copilot and OpenCode hooks are unchanged.

## Reproduce

On Windows, from a Recarto checkout:

```
powershell -NoProfile -ExecutionPolicy Bypass -File docs\validation\2026-11-17-windows-hook-launch-bench.ps1 [-Recarto <path to recarto.exe>]
```

[`2026-11-17-windows-hook-launch-bench.ps1`](2026-11-17-windows-hook-launch-bench.ps1)
copies the binary into a temp folder whose name has spaces, seeds a store there, and
times each hook through each launch, started as Claude Code 2.1.326 starts it.
Columns:

- `PowerShell`, `Git Bash`, `direct`: median of 10 runs after 3 warm-ups, in ms, for
  launches C, D and F. `-` means Git Bash was not found; `failed` means a run exited
  non-zero or wrote to stderr (listed below the table).
- `direct vs PowerShell`: time saved by exec form, in ms and percent.
- `reply`: `same` when each launch's first reply matches the direct start's, else
  `differs:` and the launches that differ.

The script was checked with pwsh 7 on Linux, the Git Bash column through
`CLAUDE_CODE_GIT_BASH_PATH=/bin/bash`.
