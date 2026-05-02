package main

import (
	"log"
	"os"

	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/config"
	"github.com/TakuyaYagam1/task-per-minute/internal/app"
)

func main() {
	os.Exit(run(defaultRunDeps()))
}

type runDeps struct {
	loadConfig func() (*config.Config, error)
	newLogger  func() (logkit.Logger, error)
	runApp     func(*config.Config, logkit.Logger) error
}

func defaultRunDeps() runDeps {
	return runDeps{
		loadConfig: config.Load,
		newLogger: func() (logkit.Logger, error) {
			return logkit.New(
				logkit.WithLevel(logkit.InfoLevel),
				logkit.WithServiceName("task-per-minute"),
			)
		},
		runApp: app.Run,
	}
}

func run(deps runDeps) int {
	cfg, err := deps.loadConfig()
	if err != nil {
		log.Printf("Config initialization failed: %v", err)
		return 1
	}

	l, err := deps.newLogger()
	if err != nil {
		log.Printf("Logger initialization failed: %v", err)
		return 1
	}
	defer func() {
		_ = l.Close()
	}()

	l.Info("Configuration loaded successfully")

	if err := deps.runApp(cfg, l); err != nil {
		return 1
	}
	return 0
}
