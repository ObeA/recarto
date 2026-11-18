package claudecode

import (
	"context"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"

	"golang.org/x/mod/semver"

	"github.com/gerwim/recarto/internal/agents"
)

// execFormMin is the first Claude Code release that runs a hook's args (exec form).
const execFormMin = "2.1.139"

// claudeVersionOutput runs `claude --version`. A var so tests can fake the Claude Code found.
var claudeVersionOutput = func() ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "claude", "--version")
	cmd.WaitDelay = time.Second // a .cmd shim's child can keep stdout open after the kill
	return cmd.Output()
}

// claudeVersion returns the version the claude CLI on PATH reports
// ("2.1.323 (Claude Code)" → "2.1.323"). It fails when there is no claude or it
// prints no version.
func claudeVersion() (string, error) {
	out, err := claudeVersionOutput()
	if err != nil {
		return "", err
	}
	for _, f := range strings.Fields(string(out)) {
		if semver.IsValid("v" + f) {
			return f, nil
		}
	}
	return "", fmt.Errorf("claude --version printed no version: %q", strings.TrimSpace(string(out)))
}

// tooOld reports whether Claude Code v (a valid version) predates exec form.
func tooOld(v string) bool { return semver.Compare("v"+v, "v"+execFormMin) < 0 }

// tooOldText is what init and doctor say about Claude Code v.
func tooOldText(v string) string {
	return fmt.Sprintf("Claude Code %s found; Recarto's hooks need Claude Code %s or later and won't run until it is updated", v, execFormMin)
}

// VersionNote is the line init prints after installing Claude Code's hooks, about
// the version that exec-form hooks need: "" off Windows and for execFormMin or later.
func VersionNote() string {
	if goos != "windows" {
		return ""
	}
	v, err := claudeVersion()
	if err != nil {
		return "note: couldn't read the Claude Code version; Recarto's hooks need Claude Code " + execFormMin + " or later."
	}
	if tooOld(v) {
		return "warning: " + tooOldText(v) + "."
	}
	return ""
}

// VersionWarning returns doctor's warning about the Claude Code version, "" when
// there is none. It warns only on Windows, when the repo's or the user's Claude
// Code settings hold Recarto's hooks in exec form and the claude on PATH reports
// a version older than execFormMin. A version it can't read gets no warning: a
// Claude Code outside PATH (the desktop app) is no sign of trouble.
func VersionWarning(p agents.Paths) string {
	if goos != "windows" {
		return ""
	}
	files := []string{p.RepoFile(".claude", "settings.json")}
	if p.Home != "" {
		files = append(files, p.UserFile(".claude", "settings.json"))
	}
	if !slices.ContainsFunc(files, execFormIn) {
		return ""
	}
	v, err := claudeVersion()
	if err != nil || !tooOld(v) {
		return ""
	}
	return tooOldText(v)
}

// execFormIn reports whether the settings file at path holds a Recarto hook in exec form.
func execFormIn(path string) bool {
	s, err := agents.ReadJSONObject(path)
	if err != nil {
		// A broken settings file is Claude Code's to report; this check only
		// looks for Recarto's hooks.
		return false
	}
	for key := range ownedCommands(s) {
		// handlerKey joins exec-form args with "\x00".
		if strings.Contains(key, "\x00") {
			return true
		}
	}
	return false
}
