// Command backup writes a pg_dump (custom format) of DATABASE_URL into ./backups with an exact
// row-count manifest from the same snapshot (make backup). Tools: postgres:16 image by default (D43).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"

	"github.com/sudhanshubhushan4535/calmfit/internal/backup"
)

func main() {
	dir := flag.String("dir", "backups", "output directory")
	flag.Parse()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is not set (see .env.example)")
	}
	m, err := backup.Backup(context.Background(), dsn, *dir, backup.ToolsFromEnv())
	if err != nil {
		log.Fatal(err)
	}
	var rows int64
	names := make([]string, 0, len(m.Tables))
	for t, n := range m.Tables {
		names = append(names, t)
		rows += n
	}
	sort.Strings(names)
	fmt.Printf("backup: %s\n  database %s (server %s), tools: %s\n", m.File, m.Database, m.ServerVersion, m.Tools)
	fmt.Printf("  %.1f MB in %.1f s; %d tables, %d rows (manifest %s)\n", float64(m.Bytes)/(1<<20), m.DumpSeconds,
		len(m.Tables), rows, backup.ManifestPath(m.File))
	for _, t := range names {
		fmt.Printf("    %-20s %10d\n", t, m.Tables[t])
	}
}
