package main

import (
	"log"

	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/config"
	"github.com/TakuyaYagam1/task-per-minute/internal/app"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Config initialization failed: %v", err)
	}

	l, err := logkit.New(
		logkit.WithLevel(logkit.InfoLevel),
		logkit.WithServiceName("task-per-minute"),
	)
	if err != nil {
		log.Fatalf("Logger initialization failed: %v", err)
	}
	defer func() {
		_ = l.Close()
	}()

	l.Info("Configuration loaded successfully")

	app.Run(cfg, l)
}
