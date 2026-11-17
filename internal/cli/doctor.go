package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gerwim/recarto/internal/app"
	"github.com/gerwim/recarto/internal/features"
)

// Doctor runs health checks: store reachable, WAL enabled, integrity ok, a schema
// version applied, and git identity resolved. It returns an error if any fail so
// the process exits non-zero. --quick runs the quick check instead of the full
// integrity check.
func Doctor(ctx context.Context, a *app.App, w io.Writer, args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	quick := fs.Bool("quick", false, "run the quick check instead of the full integrity check")
	if helped, err := parseFlags(fs, args); helped || err != nil {
		return err
	}
	allOK := true
	check := func(name string, pass bool, detail string) {
		status := "ok"
		if !pass {
			status, allOK = "FAIL", false
		}
		fmt.Fprintf(w, "[%s] %s: %s\n", status, name, detail)
	}

	var jm string
	if err := a.Store.DB.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&jm); err != nil {
		return err
	}
	check("wal", jm == "wal", jm)

	// integrity_check returns "ok" as a single row when healthy, or one row per
	// problem when corrupt — read them all so the detail isn't silently truncated.
	// A hard failure of the query or the scan is itself a common corruption
	// outcome, so it reports as a failed check with the repair hint rather than
	// aborting doctor with a bare SQLite error.
	fi, err := os.Stat(a.Store.Path())
	if err != nil {
		return err
	}
	name, pragma := "integrity", `PRAGMA integrity_check`
	notice := "checking store integrity (%s): this can take minutes on a large store; --quick runs a faster check\n"
	if *quick {
		name, pragma = "quick_check", `PRAGMA quick_check`
		notice = "running the quick check on the store (%s): this can take minutes on a large store\n"
	}
	if _, err := fmt.Fprintf(w, notice, sizeText(fi.Size())); err != nil {
		return err
	}
	integ, err := integrityDetail(ctx, a, pragma)
	if err != nil {
		integ = err.Error()
	}
	check(name, err == nil && integ == "ok", integ)
	if err != nil || integ != "ok" {
		fmt.Fprintln(w, "  hint: run 'recarto repair' to rebuild the store, salvaging your memories")
	}

	reportLastRepair(ctx, a, w)

	var uv int
	if err := a.Store.DB.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&uv); err != nil {
		return err
	}
	check("schema", uv >= 1, fmt.Sprintf("user_version=%d", uv))

	check("git", a.Git.RepoID != "", fmt.Sprintf("id=%s repo=%s branch=%s nongit=%v",
		a.Git.RepoID, a.Git.RepoName, a.Git.Branch, a.Git.NonGit))

	// A typo in the config file leaves the feature it meant on, unnoticed.
	if path, err := features.Path(); err == nil {
		s, err := features.Read(path)
		detail := strings.ReplaceAll(fmt.Sprint(err), "\n", "; ")
		if err == nil {
			var off []string
			for _, f := range features.All() {
				if !s.On(f) {
					off = append(off, f.Key)
				}
			}
			detail = "all features on"
			if len(off) > 0 {
				detail = fmt.Sprintf("%d off: %s", len(off), strings.Join(off, ", "))
			}
		}
		check("config", err == nil, detail)
	}

	// Sync reachability is diagnostic only: an unreachable server must never
	// fail doctor (offline mode is the floor), so this check never flips allOK.
	if a.Sync == nil {
		fmt.Fprintln(w, "[ok] sync: disabled")
	} else {
		pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := a.Sync.Ping(pctx)
		cancel()
		status := "ok"
		detail := fmt.Sprintf("repo_key=%s reachable", a.Sync.RepoKeyValue())
		if err != nil {
			status = "warn"
			detail = fmt.Sprintf("repo_key=%s unreachable: %v", a.Sync.RepoKeyValue(), err)
		}
		fmt.Fprintf(w, "[%s] sync: %s\n", status, detail)
	}

	if !allOK {
		return fmt.Errorf("doctor: one or more checks failed")
	}
	return nil
}

// sizeText formats a file size in GB or MB.
func sizeText(n int64) string {
	if n >= 1<<30 {
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
}

// integrityDetail runs pragma (PRAGMA integrity_check or quick_check) and joins
// its findings. A healthy store answers with the single row "ok".
func integrityDetail(ctx context.Context, a *app.App, pragma string) (string, error) {
	rows, err := a.Store.DB.QueryContext(ctx, pragma)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var msgs []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return "", err
		}
		msgs = append(msgs, s)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return strings.Join(msgs, "; "), nil
}

// reportLastRepair surfaces what an earlier automatic (or manual) repair did, so
// a store that has been silently rebuilt — and a recurring source of corruption —
// is visible rather than invisible. Absent keys simply print nothing.
func reportLastRepair(ctx context.Context, a *app.App, w io.Writer) {
	var ts, blob string
	err := a.Store.DB.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(CASE WHEN key='repair_last_ts'    THEN value END), ''),
		        COALESCE(MAX(CASE WHEN key='repair_last_stats' THEN value END), '')
		   FROM meta WHERE key IN ('repair_last_ts','repair_last_stats')`).Scan(&ts, &blob)
	if err != nil || ts == "" {
		return
	}
	when := ts
	if n, cerr := strconv.ParseInt(ts, 10, 64); cerr == nil {
		when = time.Unix(n, 0).Format(time.RFC3339)
	}
	fmt.Fprintf(w, "[warn] last repair: %s\n", when)
	if blob != "" {
		fmt.Fprintf(w, "  %s\n", blob)
	}
}
