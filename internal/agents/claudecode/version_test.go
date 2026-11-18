package claudecode

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gerwim/recarto/internal/agents"
)

// setClaudeVersion fakes the output of `claude --version` until the test ends.
func setClaudeVersion(t *testing.T, out string, err error) {
	t.Helper()
	old := claudeVersionOutput
	claudeVersionOutput = func() ([]byte, error) { return []byte(out), err }
	t.Cleanup(func() { claudeVersionOutput = old })
}

func TestWindowsInstallAndNoteByClaudeCodeVersion(t *testing.T) {
	for _, tc := range []struct {
		name, out string
		err       error
		check     func(t *testing.T, note string)
	}{
		{"2.1.138", "2.1.138 (Claude Code)\n", nil, func(t *testing.T, note string) {
			for _, want := range []string{"2.1.138", "2.1.139 or later", "won't run until"} {
				if !strings.HasPrefix(note, "warning:") || !strings.Contains(note, want) {
					t.Errorf("note = %q, want a warning containing %q", note, want)
				}
			}
		}},
		{"2.1.139", "2.1.139 (Claude Code)\n", nil, func(t *testing.T, note string) {
			if note != "" {
				t.Errorf("note = %q, want none", note)
			}
		}},
		{"no claude", "", errors.New("exec: \"claude\": executable file not found"), nil},
		{"no version", "garbage\n", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setGOOS(t, "windows")
			setClaudeVersion(t, tc.out, tc.err)
			dir := t.TempDir()
			if _, err := Install(agents.Paths{Scope: agents.ScopeRepo, Dir: dir, Home: t.TempDir()}, winBinary); err != nil {
				t.Fatal(err)
			}
			if got, want := windowsHandlers(t, filepath.Join(dir, ".claude", "settings.json"), winBinary), wantWindowsHandlers(); !reflect.DeepEqual(got, want) {
				t.Errorf("handlers = %v, want %v", got, want)
			}
			note := VersionNote()
			if tc.check != nil {
				tc.check(t, note)
				return
			}
			if !strings.Contains(note, "2.1.139 or later") || strings.Contains(note, "warning") {
				t.Errorf("note = %q, want the requirement without a warning", note)
			}
		})
	}
	t.Run("linux", func(t *testing.T) {
		setGOOS(t, "linux")
		setClaudeVersion(t, "2.1.138 (Claude Code)\n", nil)
		if note := VersionNote(); note != "" {
			t.Errorf("note = %q, want none off Windows", note)
		}
	})
}

func TestVersionWarning(t *testing.T) {
	install := func(t *testing.T, os string, scope agents.Scope) agents.Paths {
		t.Helper()
		setGOOS(t, os)
		p := agents.Paths{Scope: scope, Dir: t.TempDir(), Home: t.TempDir()}
		if _, err := Install(p, winBinary); err != nil {
			t.Fatal(err)
		}
		setGOOS(t, "windows")
		return p
	}
	for _, tc := range []struct {
		name    string
		os      string
		scope   agents.Scope
		out     string
		err     error
		warning bool
	}{
		{"repo exec form, 2.1.138", "windows", agents.ScopeRepo, "2.1.138 (Claude Code)\n", nil, true},
		{"user exec form, 2.1.138", "windows", agents.ScopeUser, "2.1.138 (Claude Code)\n", nil, true},
		{"exec form, 2.1.139", "windows", agents.ScopeRepo, "2.1.139 (Claude Code)\n", nil, false},
		{"exec form, unreadable", "windows", agents.ScopeRepo, "", errors.New("not found"), false},
		{"shell form, 2.1.138", "linux", agents.ScopeRepo, "2.1.138 (Claude Code)\n", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := install(t, tc.os, tc.scope)
			setClaudeVersion(t, tc.out, tc.err)
			got := VersionWarning(p)
			if !tc.warning {
				if got != "" {
					t.Errorf("warning = %q, want none", got)
				}
				return
			}
			if !strings.Contains(got, "2.1.138") || !strings.Contains(got, "2.1.139") {
				t.Errorf("warning = %q, want one naming 2.1.138 and 2.1.139", got)
			}
		})
	}
}
