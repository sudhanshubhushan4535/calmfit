// Package backup takes pg_dump backups with an exact row-count manifest and proves them with a
// restore into a fresh database (make backup, make restore-test; README "Backups and restore").
//
// The dump and the counts come from the same snapshot: a REPEATABLE READ transaction exports its
// snapshot, pg_dump --snapshot dumps exactly that, and the counts are taken inside it. So a restore
// must reproduce the manifest exactly, even while the app keeps writing.
package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// DefaultImage provides the client tools. Matching the server's major version (16) avoids
// archive features an older server doesn't understand (D43).
const DefaultImage = "postgres:16"

// Tools runs pg_dump / pg_restore: from Bin when set (a directory of client binaries), otherwise
// in a throwaway Image container on the host network, so the same DATABASE_URL works everywhere.
type Tools struct {
	Image string
	Bin   string
}

// ToolsFromEnv reads PG_BIN and PG_IMAGE.
func ToolsFromEnv() Tools {
	t := Tools{Image: os.Getenv("PG_IMAGE"), Bin: os.Getenv("PG_BIN")}
	if t.Image == "" {
		t.Image = DefaultImage
	}
	return t
}

func (t Tools) command(ctx context.Context, tool string, args ...string) *exec.Cmd {
	if t.Bin != "" {
		return exec.CommandContext(ctx, filepath.Join(t.Bin, tool), args...)
	}
	return exec.CommandContext(ctx, "docker", append([]string{"run", "--rm", "-i", "--network", "host", t.Image, tool}, args...)...)
}

// Describe names the tool source for logs.
func (t Tools) Describe() string {
	if t.Bin != "" {
		return t.Bin
	}
	return "docker image " + t.Image
}

// Manifest describes one backup.
type Manifest struct {
	File          string           `json:"file"`
	Database      string           `json:"database"`
	ServerVersion string           `json:"server_version"`
	Tools         string           `json:"tools"`
	CreatedAt     time.Time        `json:"created_at"`
	Bytes         int64            `json:"bytes"`
	DumpSeconds   float64          `json:"dump_seconds"`
	Tables        map[string]int64 `json:"tables"` // public tables → exact row counts at the snapshot
}

// ManifestPath is where a dump's manifest lives.
func ManifestPath(dump string) string { return strings.TrimSuffix(dump, ".dump") + ".counts.json" }

// Backup dumps dsn into dir as calmfit-<UTC time>.dump and returns its manifest (also written
// next to it). A failed dump leaves no file behind.
func Backup(ctx context.Context, dsn, dir string, tools Tools) (Manifest, error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return Manifest{}, err
	}
	defer conn.Close(ctx)
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Manifest{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // read-only

	m := Manifest{CreatedAt: time.Now().UTC(), Tools: tools.Describe()}
	var snapshot string
	if err := tx.QueryRow(ctx, `SELECT pg_export_snapshot(), current_database(), current_setting('server_version')`).
		Scan(&snapshot, &m.Database, &m.ServerVersion); err != nil {
		return Manifest{}, fmt.Errorf("export snapshot: %w", err)
	}
	if m.Tables, err = countTables(ctx, tx); err != nil {
		return Manifest{}, err
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Manifest{}, err
	}
	m.File = filepath.Join(dir, "calmfit-"+m.CreatedAt.Format("20060102T150405Z")+".dump")
	tmp := m.File + ".partial"
	f, err := os.Create(tmp)
	if err != nil {
		return Manifest{}, err
	}
	start := time.Now()
	// --snapshot: exactly the data the counts above saw, while the transaction stays open.
	cmd := tools.command(ctx, "pg_dump", "--format=custom", "--snapshot="+snapshot, "--no-sync", "--dbname="+dsn)
	cmd.Stdout = f
	var stderr strings.Builder
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	closeErr := f.Close()
	if err := errors.Join(runErr, closeErr); err != nil {
		os.Remove(tmp)
		return Manifest{}, fmt.Errorf("pg_dump (%s): %w: %s", tools.Describe(), err, strings.TrimSpace(stderr.String()))
	}
	m.DumpSeconds = time.Since(start).Seconds()
	if err := os.Rename(tmp, m.File); err != nil {
		return Manifest{}, err
	}
	st, err := os.Stat(m.File)
	if err != nil {
		return Manifest{}, err
	}
	m.Bytes = st.Size()
	return m, writeJSON(ManifestPath(m.File), m)
}

// countTables returns exact row counts of every table in the public schema.
func countTables(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}) (map[string]int64, error) {
	rows, err := q.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname = 'public' ORDER BY tablename`)
	if err != nil {
		return nil, err
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(names))
	for _, t := range names {
		var n int64
		if err := q.QueryRow(ctx, `SELECT count(*) FROM public.`+pgx.Identifier{t}.Sanitize()).Scan(&n); err != nil {
			return nil, fmt.Errorf("count %s: %w", t, err)
		}
		out[t] = n
	}
	return out, nil
}

// Diff is one table's comparison.
type Diff struct {
	Table      string
	Want, Got  int64
	InManifest bool
	InRestore  bool
}

// OK reports whether the table matches.
func (d Diff) OK() bool { return d.InManifest && d.InRestore && d.Want == d.Got }

// Compare lines up the manifest with the restored counts, table by table (sorted). A table
// missing on either side is a failure.
func Compare(want, got map[string]int64) (diffs []Diff, ok bool) {
	names := map[string]bool{}
	for t := range want {
		names[t] = true
	}
	for t := range got {
		names[t] = true
	}
	sorted := make([]string, 0, len(names))
	for t := range names {
		sorted = append(sorted, t)
	}
	sort.Strings(sorted)
	ok = len(sorted) > 0
	for _, t := range sorted {
		w, inW := want[t]
		g, inG := got[t]
		d := Diff{Table: t, Want: w, Got: g, InManifest: inW, InRestore: inG}
		ok = ok && d.OK()
		diffs = append(diffs, d)
	}
	return diffs, ok
}

// WithDatabase returns dsn (a postgres:// URL) pointing at another database on the same server.
func WithDatabase(dsn, name string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return "", fmt.Errorf("DATABASE_URL must be a postgres:// URL")
	}
	u.Path = "/" + name
	return u.String(), nil
}

// DatabaseName returns the database a postgres:// URL points at.
func DatabaseName(dsn string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(u.Path, "/"), nil
}

// RestoreResult is what RestoreTest found.
type RestoreResult struct {
	Target         string
	RestoreSeconds float64
	Diffs          []Diff
	OK             bool
}

// RestoreTest drops and recreates target on dsn's server, restores dump into it, and compares
// every table's row count with the manifest. target must differ from dsn's own database; only
// target is ever dropped. On success target is dropped again unless keep is set.
func RestoreTest(ctx context.Context, dsn, dump, target string, keep bool, tools Tools, log io.Writer) (RestoreResult, error) {
	res := RestoreResult{Target: target}
	var m Manifest
	if err := readJSON(ManifestPath(dump), &m); err != nil {
		return res, fmt.Errorf("manifest: %w", err)
	}
	src, err := DatabaseName(dsn)
	if err != nil {
		return res, err
	}
	if target == "" || target == src || target == m.Database {
		return res, fmt.Errorf("refusing: the restore target %q must be a scratch database, not %q", target, src)
	}
	admin, err := WithDatabase(dsn, "postgres")
	if err != nil {
		return res, err
	}
	targetDSN, err := WithDatabase(dsn, target)
	if err != nil {
		return res, err
	}

	ac, err := pgx.Connect(ctx, admin)
	if err != nil {
		return res, err
	}
	defer ac.Close(ctx)
	ident := pgx.Identifier{target}.Sanitize()
	if _, err := ac.Exec(ctx, `DROP DATABASE IF EXISTS `+ident+` WITH (FORCE)`); err != nil {
		return res, err
	}
	if _, err := ac.Exec(ctx, `CREATE DATABASE `+ident); err != nil {
		return res, err
	}
	fmt.Fprintf(log, "created fresh database %s; restoring %s (%s)…\n", target, filepath.Base(dump), tools.Describe())

	in, err := os.Open(dump)
	if err != nil {
		return res, err
	}
	defer in.Close()
	start := time.Now()
	cmd := tools.command(ctx, "pg_restore", "--no-owner", "--no-privileges", "--exit-on-error", "--dbname="+targetDSN)
	cmd.Stdin = in
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return res, fmt.Errorf("pg_restore: %w: %s (database %s kept for inspection)", err, strings.TrimSpace(stderr.String()), target)
	}
	res.RestoreSeconds = time.Since(start).Seconds()

	tc, err := pgx.Connect(ctx, targetDSN)
	if err != nil {
		return res, err
	}
	got, err := countTables(ctx, tc)
	tc.Close(ctx)
	if err != nil {
		return res, err
	}
	res.Diffs, res.OK = Compare(m.Tables, got)
	if res.OK && !keep {
		if _, err := ac.Exec(ctx, `DROP DATABASE `+ident+` WITH (FORCE)`); err != nil {
			return res, fmt.Errorf("drop %s after a passing check: %w", target, err)
		}
	}
	return res, nil
}
