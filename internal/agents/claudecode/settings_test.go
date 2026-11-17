package claudecode

import "testing"

func TestEnsureMCPServerIdempotent(t *testing.T) {
	doc := map[string]any{}
	if !ensureMCPServer(doc, "recarto") {
		t.Fatal("first ensure should report a change")
	}
	if ensureMCPServer(doc, "recarto") {
		t.Error("second ensure should be a no-op")
	}
	servers := doc["mcpServers"].(map[string]any)
	rc := servers["recarto"].(map[string]any)
	if rc["command"] != "recarto" {
		t.Errorf("command = %v, want recarto", rc["command"])
	}
}

func TestEnsureMCPServerRefreshesOnBinaryDrift(t *testing.T) {
	doc := map[string]any{}
	ensureMCPServer(doc, "/old/recarto")

	// A shipped update moves the binary; re-running init must refresh command.
	if !ensureMCPServer(doc, "/new/recarto") {
		t.Fatal("ensureMCPServer should report a change when the binary path drifts")
	}
	rc := doc["mcpServers"].(map[string]any)["recarto"].(map[string]any)
	if rc["command"] != "/new/recarto" {
		t.Errorf("command = %v, want /new/recarto", rc["command"])
	}
	// No drift -> no-op.
	if ensureMCPServer(doc, "/new/recarto") {
		t.Error("ensureMCPServer should be a no-op when nothing drifts")
	}
}

func TestEnsureMCPServerPreservesUnrelatedKeys(t *testing.T) {
	doc := map[string]any{"mcpServers": map[string]any{"recarto": map[string]any{
		"command": "/old/recarto",
		"args":    []any{"serve"},
		"env":     map[string]any{"RECARTO_DEBUG": "1"},
	}}}
	if !ensureMCPServer(doc, "/new/recarto") {
		t.Fatal("expected a change for drifted command")
	}
	rc := doc["mcpServers"].(map[string]any)["recarto"].(map[string]any)
	if rc["env"] == nil {
		t.Errorf("unrelated key env was dropped: %#v", rc)
	}
}

func TestIsOwned(t *testing.T) {
	const winBin = `C:\Program Files\recarto\recarto.exe`
	for _, c := range []struct {
		command string
		args    []string
		want    bool
	}{
		{"recarto hook session-start --agent claude", nil, true},
		{"/usr/local/bin/recarto hook post-edit --agent claude", nil, true},
		{winBin, []string{"hook", "stop", "--agent", "claude"}, true},
		{"keep-me", nil, false},
		{"other-tool --flag", nil, false},
		{"recarto hook stop --agent copilot", nil, false}, // another agent's hook is not ours
		{winBin, []string{"hook", "stop", "--agent", "copilot"}, false},
	} {
		if got := isOwned(c.command, c.args); got != c.want {
			t.Errorf("isOwned(%q, %q) = %v, want %v", c.command, c.args, got, c.want)
		}
	}
}

func TestAppendHookAndHasCommandIdempotent(t *testing.T) {
	s := map[string]any{}
	cmd := "recarto hook session-start --agent claude"
	if hasCommand(s, "SessionStart", cmd) {
		t.Fatal("empty settings should not contain the command")
	}
	appendHook(s, hookSpec{event: "SessionStart", command: cmd})
	if !hasCommand(s, "SessionStart", cmd) {
		t.Fatal("command should be present after append")
	}
	// A second, unrelated hook is added as a separate group (nothing clobbered).
	appendHook(s, hookSpec{event: "SessionStart", command: "other-tool --flag"})
	groups := s["hooks"].(map[string]any)["SessionStart"].([]any)
	if len(groups) != 2 {
		t.Errorf("expected 2 hook groups, got %d", len(groups))
	}
}
