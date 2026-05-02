package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/config"
	"github.com/TakuyaYagam1/task-per-minute/internal/app"
)

func main() {
	os.Exit(run())
}

func run() int {
	command := "up"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	if len(os.Args) > 2 {
		log.Fatalf("Usage: migrate [up|down|status]")
	}

	cfg, err := config.LoadMigration()
	if err != nil {
		log.Fatalf("Migration config initialization failed: %v", err)
	}

	l, err := logkit.New(
		logkit.WithLevel(logkit.InfoLevel),
		logkit.WithServiceName("task-per-minute-migrate"),
	)
	if err != nil {
		log.Fatalf("Logger initialization failed: %v", err)
	}
	defer func() {
		_ = l.Close()
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := app.RunMigrationsDSN(ctx, cfg.DB.DSN, l, command); err != nil {
		l.WithError(err).Error("migration failed")
		return 1
	}
	return 0
}
