// Command reminders sends today's workout-day Web Push reminders once (D47). Cron runs it daily at
// 07:30 Asia/Kolkata. -dry-run prints who would get what and sends nothing; -user limits the run
// to one user id (a manual test send); -keygen prints a new VAPID key pair for .env and exits.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sudhanshubhushan4535/calmfit/internal/config"
	"github.com/sudhanshubhushan4535/calmfit/internal/reminder"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	dryRun := flag.Bool("dry-run", false, "print who is due and the text; send and record nothing")
	user := flag.String("user", "", "only this user id")
	keygen := flag.Bool("keygen", false, "print a new VAPID key pair and exit")
	flag.Parse()

	if *keygen {
		priv, pub, err := webpush.GenerateVAPIDKeys()
		if err != nil {
			return err
		}
		// Printed for the operator to paste into .env; never written to a file.
		fmt.Printf("VAPID_PUBLIC_KEY=%s\nVAPID_PRIVATE_KEY=%s\n", pub, priv)
		return nil
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL is not set (see .env.example)")
	}
	sender := reminder.WebPush{
		PublicKey:  os.Getenv("VAPID_PUBLIC_KEY"),
		PrivateKey: os.Getenv("VAPID_PRIVATE_KEY"),
		Subject:    os.Getenv("VAPID_SUBJECT"),
	}
	if !*dryRun && (sender.PublicKey == "" || sender.PrivateKey == "" || sender.Subject == "") {
		return errors.New("VAPID_PUBLIC_KEY, VAPID_PRIVATE_KEY and VAPID_SUBJECT must be set (make vapid-keys)")
	}
	cfgPath := os.Getenv("APP_CONFIG")
	if cfgPath == "" {
		cfgPath = "config/app.json"
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("db pool: %w", err)
	}
	defer db.Close()

	opts := reminder.Options{DryRun: *dryRun}
	if *user != "" {
		opts.Users = []string{*user}
	}
	res, err := reminder.Run(ctx, db, sender, cfg.Location, time.Now(), opts)
	if *dryRun {
		for _, m := range res.Messages {
			fmt.Printf("%s\t%s\n", m.UserID, m.Text)
		}
	}
	for _, e := range res.Errors {
		log.Printf("send failed: %s", e)
	}
	log.Printf("reminders: candidates=%d due=%d sent=%d gone=%d failed=%d dry_run=%v",
		res.Candidates, res.Due, res.Sent, res.Gone, res.Failed, *dryRun)
	return err
}
