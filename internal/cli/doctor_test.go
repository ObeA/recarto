package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/gerwim/recarto/internal/agents"
)

func TestDoctorPasses(t *testing.T) {
	t.Setenv("RECARTO_UNUSED_WORKTREE_DAYS", "")
	a := newTestApp(t, "main")
	for _, tc := range []struct {
		args  []string
		check string
	}{{nil, "[ok] integrity"}, {[]string{"--quick"}, "[ok] quick_check"}} {
		var buf bytes.Buffer
		if err := Doctor(context.Background(), a, &buf, tc.args); err != nil {
			t.Fatalf("doctor %v returned error: %v\n%s", tc.args, err, buf.String())
		}
		out := buf.String()
		for _, want := range []string{"[ok] wal", tc.check, "[ok] schema", "[ok] git", "[ok] unused_worktree_days: 7"} {
			if !strings.Contains(out, want) {
				t.Errorf("doctor %v missing %q:\n%s", tc.args, want, out)
			}
		}
		notice := strings.Index(out, "can take minutes on a large store")
		if notice < 0 || !strings.Contains(out[:notice], " MB)") || notice > strings.Index(out, tc.check) {
			t.Errorf("doctor %v must print the notice with the store size before %q:\n%s", tc.args, tc.check, out)
		}
	}
}

// TestDoctorFailsOnUnresolvedGit exercises the FAIL path + non-nil error return:
// an App whose git identity didn't resolve (empty RepoID) must fail the git check.
func TestDoctorFailsOnUnresolvedGit(t *testing.T) {
	a := newTestApp(t, "main")
	a.Git.RepoID = "" // simulate unresolved identity

	var buf bytes.Buffer
	err := Doctor(context.Background(), a, &buf, nil)
	if err == nil {
		t.Fatalf("expected doctor to fail, got nil\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "[FAIL] git") {
		t.Errorf("expected [FAIL] git line:\n%s", buf.String())
	}
}

func TestDoctorFailsOnBadUnusedDays(t *testing.T) {
	a := newTestApp(t, "main")
	t.Setenv("RECARTO_UNUSED_WORKTREE_DAYS", "7d")
	var buf bytes.Buffer
	if err := Doctor(context.Background(), a, &buf, []string{"--quick"}); err == nil {
		t.Fatalf("doctor passed with an invalid RECARTO_UNUSED_WORKTREE_DAYS:\n%s", buf.String())
	}
	var line string
	for _, l := range strings.Split(buf.String(), "\n") {
		if strings.HasPrefix(l, "[FAIL] unused_worktree_days:") {
			line = l
		}
	}
	if !strings.Contains(line, "RECARTO_UNUSED_WORKTREE_DAYS") || !strings.Contains(line, "0 to 36500") {
		t.Errorf("want a [FAIL] unused_worktree_days line naming the variable and the range:\n%s", buf.String())
	}
}

func TestDoctorWarnsAboutClaudeCodeWithoutFailing(t *testing.T) {
	t.Setenv("RECARTO_UNUSED_WORKTREE_DAYS", "")
	a := newTestApp(t, "main")
	old := claudeCodeWarning
	t.Cleanup(func() { claudeCodeWarning = old })
	for _, msg := range []string{"Claude Code 2.1.138 found; too old", ""} {
		claudeCodeWarning = func(agents.Paths) string { return msg }
		var buf bytes.Buffer
		if err := Doctor(context.Background(), a, &buf, []string{"--quick"}); err != nil {
			t.Fatalf("doctor with warning %q: %v\n%s", msg, err, buf.String())
		}
		hasLine := strings.Contains(buf.String(), "claude code")
		if msg != "" && !strings.Contains(buf.String(), "[warn] claude code: "+msg+"\n") {
			t.Errorf("want the [warn] claude code line:\n%s", buf.String())
		}
		if msg == "" && hasLine {
			t.Errorf("want no claude code line:\n%s", buf.String())
		}
	}
}
