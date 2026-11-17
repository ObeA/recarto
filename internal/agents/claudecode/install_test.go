package claudecode

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gerwim/recarto/internal/agents"
	"github.com/gerwim/recarto/internal/features"
)

func TestInstallRepoScopeWritesConfigAndIsIdempotent(t *testing.T) {
	setGOOS(t, "linux")
	t.Setenv("RECARTO_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	dir := t.TempDir()
	home := t.TempDir()

	// A pre-existing, unrelated hook must be preserved across install.
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	pre := `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"keep-me"}]}]}}`
	if err := os.WriteFile(filepath.Join(dir, ".claude", "settings.json"), []byte(pre), 0o644); err != nil {
		t.Fatal(err)
	}

	p := agents.Paths{Scope: agents.ScopeRepo, Dir: dir, Home: home}
	res, err := Install(p, "recarto")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Hooks, "installed") {
		t.Errorf("Hooks = %q, want installed N", res.Hooks)
	}
	if res.MCP != "configured" {
		t.Errorf("MCP = %q, want configured", res.MCP)
	}

	mcp, _ := os.ReadFile(filepath.Join(dir, ".mcp.json"))
	if !strings.Contains(string(mcp), `"recarto"`) || !strings.Contains(string(mcp), `"serve"`) {
		t.Errorf(".mcp.json missing recarto serve:\n%s", mcp)
	}

	settings, _ := os.ReadFile(filepath.Join(dir, ".claude", "settings.json"))
	for _, want := range []string{
		"keep-me",
		"recarto hook session-start --agent claude",
		"recarto hook post-read --agent claude",
		"recarto hook post-edit --agent claude",
		"recarto hook augment --agent claude",
		"recarto hook stop --agent claude",
	} {
		if !strings.Contains(string(settings), want) {
			t.Errorf("settings.json missing %q:\n%s", want, settings)
		}
	}

	agentsmd, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if !strings.Contains(string(agentsmd), "record_memory") {
		t.Errorf("AGENTS.md missing instructions:\n%s", agentsmd)
	}

	// Re-running installs nothing new.
	res2, err := Install(p, "recarto")
	if err != nil {
		t.Fatal(err)
	}
	if res2.Hooks != "already present" || res2.MCP != "already present" {
		t.Errorf("second install not idempotent: %+v", res2)
	}
}

// TestInstallInstructionsFollowConfig: the block init writes names only the MCP
// tools the user's config leaves on.
func TestInstallInstructionsFollowConfig(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.toml")
	if err := features.Save(cfg, features.Set{features.GraphTools: false}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RECARTO_CONFIG", cfg)
	dir := t.TempDir()
	if _, err := Install(agents.Paths{Scope: agents.ScopeRepo, Dir: dir, Home: t.TempDir()}, "recarto"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "record_memory") {
		t.Errorf("AGENTS.md missing the memory tools that are on:\n%s", b)
	}
	if strings.Contains(string(b), "search_graph") {
		t.Errorf("AGENTS.md names graph tools that are off:\n%s", b)
	}
}

func TestInstallReplacesStaleHooks(t *testing.T) {
	setGOOS(t, "linux")
	dir := t.TempDir()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-existing settings: a foreign hook to preserve, plus a STALE recarto hook
	// shipped by an older version (old binary path + renamed event).
	pre := `{"hooks":{` +
		`"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"keep-me"}]}],` +
		`"PostToolUse":[{"matcher":"Read","hooks":[{"type":"command","command":"/old/recarto hook post-read-LEGACY --agent claude"}]}]` +
		`}}`
	if err := os.WriteFile(filepath.Join(dir, ".claude", "settings.json"), []byte(pre), 0o644); err != nil {
		t.Fatal(err)
	}

	p := agents.Paths{Scope: agents.ScopeRepo, Dir: dir, Home: home}
	if _, err := Install(p, "recarto"); err != nil {
		t.Fatal(err)
	}

	b, _ := os.ReadFile(filepath.Join(dir, ".claude", "settings.json"))
	s := string(b)
	if strings.Contains(s, "post-read-LEGACY") {
		t.Errorf("stale recarto hook not pruned:\n%s", s)
	}
	if strings.Contains(s, "/old/recarto") {
		t.Errorf("stale recarto binary path not pruned:\n%s", s)
	}
	if !strings.Contains(s, "keep-me") {
		t.Errorf("foreign hook not preserved:\n%s", s)
	}
	for _, want := range []string{
		"recarto hook session-start --agent claude",
		"recarto hook post-read --agent claude",
		"recarto hook stop --agent claude",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("current hook %q missing:\n%s", want, s)
		}
	}
}

// setGOOS overrides the package's OS detection for one test and restores it.
func setGOOS(t *testing.T, v string) {
	t.Helper()
	old := goos
	goos = v
	t.Cleanup(func() { goos = old })
}

func TestInstallUserScopeTargets(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	if _, err := Install(agents.Paths{Scope: agents.ScopeUser, Dir: dir, Home: home}, "recarto"); err != nil {
		t.Fatal(err)
	}
	for _, rel := range [][]string{
		{".claude.json"},
		{".claude", "settings.json"},
		{".claude", "CLAUDE.md"},
	} {
		if _, err := os.Stat(filepath.Join(append([]string{home}, rel...)...)); err != nil {
			t.Errorf("user target %v missing: %v", rel, err)
		}
	}
}

// shellGroups returns the PostToolUse groups matching Bash in a settings file.
func shellGroups(t *testing.T, path string) []map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Hooks map[string][]map[string]any `json:"hooks"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, g := range doc.Hooks["PostToolUse"] {
		if g["matcher"] == "Bash" {
			out = append(out, g)
		}
	}
	return out
}

// A Stage 1 install (no shell hooks) next to a foreign Bash hook upgrades in
// place at both scopes: one recarto Bash group with a handler per shell
// command, each filtered by its `if`, no duplicates on a re-run, and the
// foreign hook kept.
func TestInstallAddsPerCommandShellHooks(t *testing.T) {
	setGOOS(t, "linux")
	for _, scope := range []agents.Scope{agents.ScopeRepo, agents.ScopeUser} {
		p := agents.Paths{Scope: scope, Dir: t.TempDir(), Home: t.TempDir()}
		settingsPath := filepath.Join(p.Dir, ".claude", "settings.json")
		if scope == agents.ScopeUser {
			settingsPath = filepath.Join(p.Home, ".claude", "settings.json")
		}
		if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
			t.Fatal(err)
		}
		stage1 := `{"hooks":{"PostToolUse":[` +
			`{"matcher":"Bash","hooks":[{"type":"command","command":"./audit.sh"}]},` +
			`{"matcher":"Grep|Glob","hooks":[{"type":"command","command":"/old/recarto hook augment --agent claude"}]}]}}`
		if err := os.WriteFile(settingsPath, []byte(stage1), 0o644); err != nil {
			t.Fatal(err)
		}
		res, err := Install(p, "/bin/recarto")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(res.Hooks, "installed") {
			t.Errorf("%v: Hooks = %q, want installed", scope, res.Hooks)
		}
		first, _ := os.ReadFile(settingsPath)
		if res, err := Install(p, "/bin/recarto"); err != nil || res.Hooks != "already present" {
			t.Errorf("%v: re-run = %+v, %v; want already present", scope, res, err)
		}
		if again, _ := os.ReadFile(settingsPath); string(again) != string(first) {
			t.Errorf("%v: re-run changed settings:\n%s", scope, again)
		}

		groups := shellGroups(t, settingsPath)
		if len(groups) != 2 {
			t.Fatalf("%v: %d Bash groups, want the foreign one and recarto's:\n%s", scope, len(groups), first)
		}
		if h := groups[0]["hooks"].([]any); len(h) != 1 || h[0].(map[string]any)["command"] != "./audit.sh" {
			t.Errorf("%v: foreign Bash hook changed: %v", scope, h)
		}
		got := map[string]string{} // if -> command
		for _, h := range groups[1]["hooks"].([]any) {
			hm := h.(map[string]any)
			got[hm["if"].(string)] = hm["command"].(string)
		}
		if len(got) != len(agents.ShellCommands) {
			t.Errorf("%v: %d shell hooks, want %d: %v", scope, len(got), len(agents.ShellCommands), got)
		}
		for _, c := range agents.ShellCommands {
			rule := "Bash(" + c + " *)"
			if c == "git" {
				rule = "Bash(git grep *)"
			}
			if want := "/bin/recarto hook shell --agent claude --cmd " + c; got[rule] != want {
				t.Errorf("%v: if %q runs %q, want %q", scope, rule, got[rule], want)
			}
		}
		if strings.Contains(string(first), "/old/recarto") || strings.Count(string(first), "hook augment") != 1 {
			t.Errorf("%v: stale or duplicate hooks:\n%s", scope, first)
		}
	}
}

// An install whose shell hooks carry another `if` re-syncs rather than
// reporting them present.
func TestSyncHooksRefreshesChangedIf(t *testing.T) {
	settings := map[string]any{}
	for _, hs := range hookSpecs("recarto") {
		if hs.ifRule != "" {
			hs.ifRule = "Bash(*)"
		}
		appendHook(settings, hs)
	}
	if got := syncHooks(settings, "recarto"); got == "already present" {
		t.Fatalf("syncHooks = %q with stale if filters", got)
	}
	if got := syncHooks(settings, "recarto"); got != "already present" {
		t.Errorf("second syncHooks = %q, want already present", got)
	}
}

// Linux and macOS hooks are written exactly as before exec form existed: the
// golden file was produced by the installer that predates it.
func TestInstallWritesUnixHooksAsBefore(t *testing.T) {
	want, err := os.ReadFile(filepath.Join("testdata", "settings-unix.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, goosName := range []string{"linux", "darwin"} {
		setGOOS(t, goosName)
		dir := t.TempDir()
		if _, err := Install(agents.Paths{Scope: agents.ScopeRepo, Dir: dir, Home: t.TempDir()}, "/usr/local/bin/recarto"); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(dir, ".claude", "settings.json"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: settings.json differs from testdata/settings-unix.json:\n%s", goosName, got)
		}
	}
}

// winBinary has spaces, which no quoting is needed for in exec form.
const winBinary = `C:\Users\Jane Doe\AppData\Local\Programs\recarto\recarto.exe`

// windowsHandlers counts the exec-form handlers that run binary, keyed by
// event, matcher, if and args. It fails the test for a recarto handler that
// sets a shell, is still in shell form, or whose command is not exactly binary
// (exec form cannot start a quoted path).
func windowsHandlers(t *testing.T, settingsPath, binary string) map[string]int {
	t.Helper()
	b, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Hooks map[string][]struct {
			Matcher string           `json:"matcher"`
			Hooks   []map[string]any `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	out := map[string]int{}
	for event, groups := range doc.Hooks {
		for _, g := range groups {
			for _, h := range g.Hooks {
				command, _ := h["command"].(string)
				if command != binary && !isOwned(command, handlerArgs(h)) {
					continue
				}
				if _, ok := h["shell"]; ok {
					t.Errorf("recarto handler sets a shell: %v", h)
				}
				if strings.Contains(command, " hook ") {
					t.Errorf("recarto handler still in shell form: %v", h)
					continue
				}
				if command != binary {
					t.Errorf("recarto handler command = %q, want %q", command, binary)
					continue
				}
				raw, ok := h["args"].([]any)
				if !ok {
					t.Errorf("recarto handler has no args array: %v", h)
					continue
				}
				words := make([]string, len(raw))
				for i, a := range raw {
					s, ok := a.(string)
					if !ok {
						t.Errorf("non-string arg in %v", h)
					}
					words[i] = s
				}
				ifRule, _ := h["if"].(string)
				out[event+"\t"+g.Matcher+"\t"+ifRule+"\t"+strings.Join(words, " ")]++
			}
		}
	}
	return out
}

// wantWindowsHandlers is every recarto hook once, in exec form.
func wantWindowsHandlers() map[string]int {
	want := map[string]int{
		"SessionStart\t\t\thook session-start --agent claude":      1,
		"PostToolUse\tRead\t\thook post-read --agent claude":       1,
		"PostToolUse\tEdit|Write\t\thook post-edit --agent claude": 1,
		"PostToolUse\tGrep|Glob\t\thook augment --agent claude":    1,
		"Stop\t\t\thook stop --agent claude":                       1,
	}
	for _, c := range agents.ShellCommands {
		rule := c
		if c == "git" {
			rule = "git grep"
		}
		want["PostToolUse\tBash\tBash("+rule+" *)\thook shell --agent claude --cmd "+c] = 1
	}
	return want
}

func TestInstallWritesExecFormOnWindows(t *testing.T) {
	setGOOS(t, "windows")
	dir := t.TempDir()
	if _, err := Install(agents.Paths{Scope: agents.ScopeRepo, Dir: dir, Home: t.TempDir()}, winBinary); err != nil {
		t.Fatal(err)
	}
	got := windowsHandlers(t, filepath.Join(dir, ".claude", "settings.json"), winBinary)
	if want := wantWindowsHandlers(); !reflect.DeepEqual(got, want) {
		t.Errorf("handlers = %v\nwant %v", got, want)
	}
}

// An install from Recarto 1.7/1.8 (PowerShell pinned) with one hook from
// before the pin moves to exec form, keeping other tools' hooks.
func TestInstallMovesPowershellHooksToExecForm(t *testing.T) {
	setGOOS(t, "windows")
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	ps := func(words string, extra string) map[string]any {
		h := map[string]any{"type": "command", "command": winBinary + " hook " + words + " --agent claude", "shell": "powershell"}
		if extra != "" {
			h["if"] = extra
		}
		return h
	}
	group := func(matcher string, hs ...map[string]any) map[string]any {
		g := map[string]any{"hooks": hs}
		if matcher != "" {
			g["matcher"] = matcher
		}
		return g
	}
	shellHook := ps("shell", "Bash(grep *)")
	shellHook["command"] = winBinary + " hook shell --agent claude --cmd grep"
	seed := map[string]any{"hooks": map[string]any{
		"PreToolUse":   []any{group("Bash", map[string]any{"type": "command", "command": "keep-me"})},
		"SessionStart": []any{group("", ps("session-start", ""))},
		"PostToolUse": []any{
			group("Read", ps("post-read", ""), map[string]any{"type": "command", "command": "Write-Output read", "shell": "powershell"}),
			group("Edit|Write", ps("post-edit", "")),
			group("Grep|Glob", ps("augment", "")),
			group("Bash", shellHook),
		},
		"Stop": []any{group("", map[string]any{"type": "command", "command": winBinary + " hook stop --agent claude"})},
	}}
	b, err := json.Marshal(seed)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, b, 0o644); err != nil {
		t.Fatal(err)
	}

	p := agents.Paths{Scope: agents.ScopeRepo, Dir: dir, Home: t.TempDir()}
	res, err := Install(p, winBinary)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Hooks, "installed") {
		t.Errorf("Hooks = %q, want installed N", res.Hooks)
	}
	if got, want := windowsHandlers(t, settingsPath, winBinary), wantWindowsHandlers(); !reflect.DeepEqual(got, want) {
		t.Errorf("handlers = %v\nwant %v", got, want)
	}
	first, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Hooks map[string][]struct {
			Hooks []map[string]any `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(first, &doc); err != nil {
		t.Fatal(err)
	}
	foreign := map[string]bool{}
	for _, groups := range doc.Hooks {
		for _, g := range groups {
			for _, h := range g.Hooks {
				if h["command"] == "keep-me" && h["shell"] == nil {
					foreign["keep-me"] = true
				}
				if h["command"] == "Write-Output read" && h["shell"] == "powershell" {
					foreign["Write-Output read"] = true
				}
			}
		}
	}
	if !foreign["keep-me"] || !foreign["Write-Output read"] {
		t.Errorf("foreign hooks not kept as they were: %v\n%s", foreign, first)
	}

	res2, err := Install(p, winBinary)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Hooks != "already present" {
		t.Errorf("second Hooks = %q, want already present", res2.Hooks)
	}
	if again, _ := os.ReadFile(settingsPath); !bytes.Equal(again, first) {
		t.Errorf("re-run changed settings:\n%s", again)
	}
}
