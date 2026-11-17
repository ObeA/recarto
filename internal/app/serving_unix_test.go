//go:build !windows

package app

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"testing"
)

// TestServeMarkHelper is the server process of TestKilledServerStopsCounting.
func TestServeMarkHelper(t *testing.T) {
	if os.Getenv("RECARTO_SERVE_MARK_HELPER") == "" {
		t.Skip("helper process")
	}
	if _, err := MarkServing("r"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stdout.WriteString("marked\n"); err != nil {
		t.Fatal(err)
	}
	select {} // holds the mark until killed
}

func TestKilledServerStopsCounting(t *testing.T) {
	t.Setenv("RECARTO_CACHE_DIR", t.TempDir())
	cmd := exec.Command(os.Args[0], "-test.run=^TestServeMarkHelper$")
	cmd.Env = append(os.Environ(), "RECARTO_SERVE_MARK_HELPER=1")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil || line != "marked\n" {
		_ = cmd.Process.Kill() // the test fails either way
		t.Fatalf("helper said %q, %v", line, err)
	}
	if !ServerRunning("r") {
		t.Error("a live server is not counted")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	var exit *exec.ExitError
	if err := cmd.Wait(); !errors.As(err, &exit) || exit.Success() {
		t.Fatalf("wait = %v, want the kill", err)
	}
	if ServerRunning("r") {
		t.Error("a killed server still counts")
	}
}
