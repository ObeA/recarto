package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestDoctorPasses(t *testing.T) {
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
		for _, want := range []string{"[ok] wal", tc.check, "[ok] schema", "[ok] git"} {
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
