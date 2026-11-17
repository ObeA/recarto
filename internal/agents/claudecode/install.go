package claudecode

import (
	"runtime"
	"strings"

	"github.com/gerwim/recarto/internal/agents"
	"github.com/gerwim/recarto/internal/features"
)

// goos is the target OS Recarto installs for. It indirects runtime.GOOS so tests
// can exercise the Windows exec-form hooks without running on Windows.
var goos = runtime.GOOS

// hookSpec is one hook the installer registers: its event, matcher, `if`
// permission-rule filter ("" runs it for every matched tool call), and the
// handler it runs. On Windows the handler is in exec form: command is the
// binary and args its arguments. Elsewhere command is the whole command line
// and args is nil.
type hookSpec struct {
	event, matcher, command string
	args                    []string
	ifRule                  string
}

// hookSpecs are the advisory hooks. SessionStart and Stop run unconditionally
// (no matcher); post-read, post-edit, and augment are scoped to their tools. Each
// command carries `--agent claude` so the dispatcher selects the Claude adapter.
//
// Claude Code's native builds have no Grep/Glob tools: the agent searches and
// views files with the Bash tool. One shell hook per agents.ShellCommands key
// runs only when the command line runs that command (its `if`), so any other
// Bash call starts no Recarto process; --cmd tells the hook which key started
// it, so a line matching several keys is answered once.
//
// On Windows Claude Code runs a shell-form command through Git Bash, which
// drops the backslashes of an unquoted Windows path, or through PowerShell,
// whose start costs several times the hook's own run. Exec form (Claude Code
// 2.1.139+) starts the binary directly, with no shell and no quoting. See
// docs/validation/2026-11-17-windows-hook-launch.md.
func hookSpecs(binary string) []hookSpec {
	hook := func(event, matcher, ifRule string, words ...string) hookSpec {
		args := append([]string{"hook"}, words...)
		if goos == "windows" {
			return hookSpec{event: event, matcher: matcher, command: binary, args: args, ifRule: ifRule}
		}
		return hookSpec{event: event, matcher: matcher, command: binary + " " + strings.Join(args, " "), ifRule: ifRule}
	}
	specs := []hookSpec{
		hook("SessionStart", "", "", "session-start", "--agent", "claude"),
		hook("PostToolUse", "Read", "", "post-read", "--agent", "claude"),
		hook("PostToolUse", "Edit|Write", "", "post-edit", "--agent", "claude"),
		hook("PostToolUse", "Grep|Glob", "", "augment", "--agent", "claude"),
	}
	for _, c := range agents.ShellCommands {
		rule := c
		if c == "git" {
			rule = "git grep"
		}
		specs = append(specs, hook("PostToolUse", "Bash", "Bash("+rule+" *)", "shell", "--agent", "claude", "--cmd", c))
	}
	return append(specs, hook("Stop", "", "", "stop", "--agent", "claude"))
}

// Install sets up Claude Code integration at the given scope. It is idempotent:
// re-running adds nothing already present and preserves unrelated config.
//
// Repo scope writes .mcp.json, .claude/settings.json, and AGENTS.md. User scope
// writes ~/.claude.json (mcpServers), ~/.claude/settings.json (hooks), and
// ~/.claude/CLAUDE.md (Claude's global instructions file).
func Install(p agents.Paths, binary string) (agents.InstallResult, error) {
	res := agents.InstallResult{Agent: "claude"}

	var mcpPath, settingsPath, docPath string
	switch p.Scope {
	case agents.ScopeUser:
		mcpPath = p.UserFile(".claude.json")
		settingsPath = p.UserFile(".claude", "settings.json")
		docPath = p.UserFile(".claude", "CLAUDE.md")
	default:
		mcpPath = p.RepoFile(".mcp.json")
		settingsPath = p.RepoFile(".claude", "settings.json")
		docPath = p.RepoFile("AGENTS.md")
	}

	// 1. Register the MCP server.
	mcpDoc, err := agents.ReadJSONObject(mcpPath)
	if err != nil {
		return res, err
	}
	if ensureMCPServer(mcpDoc, binary) {
		res.MCP = "configured"
	} else {
		res.MCP = "already present"
	}
	if err := agents.WriteJSONObject(mcpPath, mcpDoc); err != nil {
		return res, err
	}

	// 2. Install lifecycle hooks.
	settings, err := agents.ReadJSONObject(settingsPath)
	if err != nil {
		return res, err
	}
	res.Hooks = syncHooks(settings, binary)
	if err := agents.WriteJSONObject(settingsPath, settings); err != nil {
		return res, err
	}

	// 3. Write the instructions block.
	if err := agents.WriteInstructionsBlock(docPath, features.Load()); err != nil {
		return res, err
	}
	res.Docs = "written"

	return res, nil
}
