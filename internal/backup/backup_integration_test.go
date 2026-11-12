package backup

import (
	"context"
	"io"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/sudhanshubhushan4535/calmfit/internal/testutil"
)

// A real backup of calmfit_test, restored into a scratch database, must match its manifest.
func TestBackupRestoreRoundTrip(t *testing.T) {
	db := testutil.DB(t) // fails, never skips, without TEST_DATABASE_URL
	dsn := os.Getenv("TEST_DATABASE_URL")
	ctx := context.Background()
	const target = "calmfit_test_restore_check"

	m, err := Backup(ctx, dsn, t.TempDir(), ToolsFromEnv())
	if err != nil {
		t.Fatal(err)
	}
	if m.Bytes == 0 || m.Tables["exercises"] == 0 || m.Tables["program_templates"] == 0 {
		t.Fatalf("manifest looks empty: %+v", m)
	}

	// The source must never be a valid target.
	if _, err := RestoreTest(ctx, dsn, m.File, m.Database, false, ToolsFromEnv(), io.Discard); err == nil {
		t.Fatal("restoring over the source database was not refused")
	}

	res, err := RestoreTest(ctx, dsn, m.File, target, false, ToolsFromEnv(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("restore mismatch: %+v", res.Diffs)
	}
	var exists bool
	if err := db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, target).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Errorf("%s still exists after a passing check", target)
		admin, _ := WithDatabase(dsn, "postgres")
		if c, err := pgx.Connect(ctx, admin); err == nil {
			_, _ = c.Exec(ctx, `DROP DATABASE IF EXISTS `+target+` WITH (FORCE)`)
			c.Close(ctx)
		}
	}
}
