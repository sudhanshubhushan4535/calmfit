// Command seed-synthetic bulk-loads deterministic synthetic training history for load tests
// (make seed-synthetic). It writes with COPY in one transaction and bypasses the outbox (D40).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sudhanshubhushan4535/calmfit/internal/config"
	"github.com/sudhanshubhushan4535/calmfit/internal/synthetic"
)

// budgetBytes is the size guard from the Day 6 brief: keep the DB under ~3 GB.
const budgetBytes = 3 << 30

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	users := flag.Int("users", 4000, "number of synthetic users")
	seed := flag.Uint64("seed", 42, "generator seed (same seed + anchor = same rows)")
	anchor := flag.String("anchor", "", "last day +1 of the history, YYYY-MM-DD (default: today, local)")
	reset := flag.Bool("reset", false, "delete existing synthetic users first (synthetic-NNNNN only)")
	flag.Parse()
	if *users < 1 || *users > synthetic.MaxUsers {
		return fmt.Errorf("-users must be 1–%d", synthetic.MaxUsers)
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL is not set (see .env.example)")
	}
	cfgPath := os.Getenv("APP_CONFIG")
	if cfgPath == "" {
		cfgPath = "config/app.json"
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	p := synthetic.Params{Users: *users, Seed: *seed, Loc: cfg.Location, Anchor: cfg.Today(time.Now())}
	if *anchor != "" {
		if p.Anchor, err = time.Parse(time.DateOnly, *anchor); err != nil {
			return fmt.Errorf("-anchor: %w", err)
		}
	}

	ctx := context.Background()
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer db.Close()

	n, err := synthetic.Existing(ctx, db)
	if err != nil {
		return err
	}
	if n > 0 && !*reset {
		return fmt.Errorf("%d synthetic users already exist; rerun with -reset (make seed-synthetic ARGS=-reset) to replace them", n)
	}
	if n > 0 {
		start := time.Now()
		deleted, err := synthetic.Reset(ctx, db)
		if err != nil {
			return fmt.Errorf("reset: %w", err)
		}
		fmt.Printf("reset: deleted %d synthetic users in %s\n", deleted, synthetic.Elapsed(start))
	}

	tpl, err := synthetic.LoadTemplate(ctx, db, "ppl")
	if err != nil {
		return err
	}
	start := time.Now()
	est, err := synthetic.Count(p, tpl)
	if err != nil {
		return err
	}
	_, before, err := synthetic.Sizes(ctx, db)
	if err != nil {
		return err
	}
	fmt.Printf("plan: %d users, seed %d, history up to %s: %d workouts, %d exercise rows, %d sets, %d plate checks (generated in %s)\n",
		p.Users, p.Seed, p.Anchor.AddDate(0, 0, -1).Format(time.DateOnly), est.Workouts, est.WorkoutExercises, est.WorkoutSets, est.PlateChecks, synthetic.Elapsed(start))
	fmt.Printf("estimate: +%s on top of %s now (budget %s)\n", mb(est.EstimatedBytes()), mb(before), mb(budgetBytes))
	if before+est.EstimatedBytes() > budgetBytes {
		return fmt.Errorf("refusing: the estimate exceeds the %s budget; lower -users", mb(budgetBytes))
	}

	start = time.Now()
	got, err := synthetic.Write(ctx, db, p, tpl, os.Stdout)
	if err != nil {
		return err
	}
	if got != est {
		return fmt.Errorf("internal: wrote %+v, planned %+v", got, est)
	}
	fmt.Printf("written and analyzed in %s\n\n", synthetic.Elapsed(start))

	sizes, dbBytes, err := synthetic.Sizes(ctx, db)
	if err != nil {
		return err
	}
	fmt.Printf("%-18s %12s %10s\n", "table", "rows", "size")
	for _, s := range sizes {
		fmt.Printf("%-18s %12d %10s\n", s.Table, s.Rows, mb(s.Bytes))
	}
	fmt.Printf("\ndatabase size: %s\n", mb(dbBytes))
	return nil
}

func mb(b int64) string { return fmt.Sprintf("%.1f MB", float64(b)/(1<<20)) }
