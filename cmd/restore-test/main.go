// Command restore-test restores a backup into a fresh scratch database on the same server and
// compares every table's row count with the backup's manifest (make restore-test). It prints
// PASS/FAIL and exits non-zero on FAIL. The scratch database is dropped on PASS, kept on FAIL.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"

	"github.com/sudhanshubhushan4535/calmfit/internal/backup"
)

func main() {
	file := flag.String("file", "", "dump to restore (default: newest backups/*.dump)")
	target := flag.String("target", "calmfit_restore_check", "scratch database to (re)create")
	keep := flag.Bool("keep", false, "keep the scratch database after a pass")
	flag.Parse()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is not set (see .env.example)")
	}
	if *file == "" {
		dumps, _ := filepath.Glob("backups/*.dump")
		if len(dumps) == 0 {
			log.Fatal("no backups/*.dump: run make backup first")
		}
		sort.Strings(dumps) // names carry a UTC timestamp, so the last is the newest
		*file = dumps[len(dumps)-1]
	}

	res, err := backup.RestoreTest(context.Background(), dsn, *file, *target, *keep, backup.ToolsFromEnv(), os.Stdout)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("restored in %.1f s; row counts vs manifest:\n", res.RestoreSeconds)
	fmt.Printf("  %-20s %10s %10s  %s\n", "table", "backup", "restored", "")
	for _, d := range res.Diffs {
		status := "PASS"
		if !d.OK() {
			status = "FAIL"
		}
		fmt.Printf("  %-20s %10s %10s  %s\n", d.Table, count(d.Want, d.InManifest), count(d.Got, d.InRestore), status)
	}
	if !res.OK {
		fmt.Printf("RESULT: FAIL (database %s kept for inspection)\n", res.Target)
		os.Exit(1)
	}
	kept := "dropped"
	if *keep {
		kept = "kept"
	}
	fmt.Printf("RESULT: PASS (%d tables match; %s %s)\n", len(res.Diffs), res.Target, kept)
}

func count(n int64, present bool) string {
	if !present {
		return "missing"
	}
	return fmt.Sprint(n)
}
