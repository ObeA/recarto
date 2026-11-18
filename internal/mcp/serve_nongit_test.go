package mcp

import (
	"context"
	"errors"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gerwim/recarto/internal/app"
)

// TestServeNonGitCwdWithWorkspaceSync is the regression test for the 1.2.0
// startup SIGSEGV: with remote sync fully configured
// (RECARTO_SYNC_URL + RECARTO_SYNC_KEY + RECARTO_WORKSPACE) and a cwd that is
// NOT a git repository, the primary repo syncer is nil (no cross-machine
// identity) while HasSync is true (the workspace syncer exists), and the
// background sync goroutine dereferenced a.Sync — killing the process before
// the MCP handshake. The serve path must instead answer initialize and
// tools/list. The sync URL is a .invalid domain on purpose: the crash (and
// this test) involves no network I/O, and even a failing sync cycle must stay
// non-fatal.
func TestServeNonGitCwdWithWorkspaceSync(t *testing.T) {
	clearRecartoEnv(t)
	t.Setenv("RECARTO_CACHE_DIR", t.TempDir())
	t.Setenv("RECARTO_SYNC_URL", "https://x.invalid")
	t.Setenv("RECARTO_SYNC_KEY", "k")
	t.Setenv("RECARTO_WORKSPACE", "w")
	t.Setenv("RECARTO_SYNC_INTERVAL", "3600") // no periodic tick during the test

	// Real git resolution against a plain temp dir → the NonGit fallback.
	cfg := app.Config{CWD: t.TempDir()}
	a, err := app.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// The crash preconditions, asserted so the test fails loudly if wiring
	// changes ever stop reproducing them.
	if !a.Git.NonGit {
		t.Fatalf("cwd resolved as a git repo, want NonGit: %+v", a.Git)
	}
	if a.Sync != nil {
		t.Fatal("primary syncer must be nil in a non-git cwd")
	}
	if !a.HasSync() {
		t.Fatal("workspace syncer must be configured (HasSync=true is the crash precondition)")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	sv, err := app.Describe(cfg)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ct, st := sdk.NewInMemoryTransports()
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, sv, func(ctx context.Context) (*app.App, error) { return app.OpenContext(ctx, cfg) }, st)
	}()

	client := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "v0"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("initialize failed (the 1.2.0 crash died before this handshake): %v", err)
	}
	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	if len(tools.Tools) == 0 {
		t.Fatal("tools/list returned no tools")
	}
	// A tool call waits for the background open, which starts the sync loop.
	res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: "list_memory", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("list_memory: %v", err)
	}
	if res.IsError {
		t.Fatalf("list_memory: %s", resultText(res))
	}
	_ = cs.Close()
	cancel()
	// cancel ends the session, so serve reports exactly that.
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("serve: %v", err)
	}
}
