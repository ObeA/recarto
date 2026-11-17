package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// warmRunning reports whether a process runs bin with the argument warm.
func warmRunning(t *testing.T, bin string) bool {
	t.Helper()
	cmdlines, err := filepath.Glob("/proc/[0-9]*/cmdline")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range cmdlines {
		b, err := os.ReadFile(p)
		if err != nil {
			continue // the process ended since the glob
		}
		args := strings.Split(string(b), "\x00")
		if len(args) > 1 && args[0] == bin && args[1] == "warm" {
			return true
		}
	}
	return false
}

// awaitNoWarm polls until no worker of bin runs.
func awaitNoWarm(t *testing.T, bin string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for warmRunning(t, bin) {
		if time.Now().After(deadline) {
			t.Fatal("the background index did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSessionStartIndexesInTheBackground(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the recarto binary (CGO); skipped under -short")
	}
	bin := filepath.Join(t.TempDir(), "recarto")
	build := exec.Command("go", "build", "-o", bin, "github.com/gerwim/recarto/cmd/recarto")
	build.Env = append(os.Environ(), "CGO_ENABLED=1")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build recarto: %v\n%s", err, out)
	}
	repo := t.TempDir()
	initGitRepo(t, repo, map[string]string{
		"api/a.go": "package api\n\nimport \"example.com/m/lib\"\n\nfunc A() { lib.B() }\n",
		"lib/b.go": "package lib\n\nfunc B() {}\n",
	})
	tmp := t.TempDir()
	// Runs before the temp dirs go, so a worker never writes into a removed dir.
	t.Cleanup(func() { awaitNoWarm(t, bin) })
	env := append(os.Environ(),
		"RECARTO_CACHE_DIR="+filepath.Join(tmp, "cache"),
		"RECARTO_CONFIG="+filepath.Join(tmp, "config.toml"),
		"RECARTO_WORKSPACE=", "RECARTO_REPOS=", "RECARTO_SYNC_URL=", "RECARTO_SYNC_KEY=")
	run := func(stdin string, args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Dir, cmd.Env, cmd.Stdin = repo, env, strings.NewReader(stdin)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("recarto %v: %v\n%s", args, err, stderr.String())
		}
		return string(out)
	}

	const (
		codeMap      = "Code map (modules)"
		indexingLine = "The code graph is still being indexed; graph results may be incomplete."
	)
	got := run(`{"session_id":"s1","source":"startup"}`, "hook", "session-start")
	if !strings.Contains(got, indexingLine) || strings.Contains(got, codeMap) {
		t.Fatalf("first session start: want the indexing line and no map:\n%s", got)
	}
	awaitNoWarm(t, bin)

	got = run(`{"session_id":"s2","source":"startup"}`, "hook", "session-start")
	if !strings.Contains(got, codeMap) || strings.Contains(got, indexingLine) {
		t.Fatalf("after the background index: want the map and no indexing line:\n%s", got)
	}
	awaitNoWarm(t, bin)

	status := run("", "status")
	m := regexp.MustCompile(`graph_index_runs\s+(\d+)`).FindStringSubmatch(status)
	if m == nil || m[1] != "1" {
		t.Errorf("graph_index_runs = %v, want 1:\n%s", m, status)
	}
}
