package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/testbots"
)

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if os.Getenv("TEST_BOTS_CONTROLLER_PLAYER_ID") == "" {
		_, _ = fmt.Fprintln(os.Stdout, "test bots disabled: configure TEST_BOTS_CONTROLLER_PLAYER_ID")
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		<-ctx.Done()
		return nil
	}
	cfg := testbots.Config{BackendURL: os.Getenv("TEST_BOTS_BACKEND_URL"), Origin: os.Getenv("TEST_BOTS_ORIGIN"), ControllerID: os.Getenv("TEST_BOTS_CONTROLLER_PLAYER_ID"), AccountsFile: "/accounts/accounts.json", CatalogFile: "/catalog/tasks.json", KeyFile: "/control/control.key", StateDir: "/state"}
	if !validAddress(cfg.BackendURL) {
		return errors.New("invalid test backend URL")
	}
	if !validAddress(cfg.Origin) {
		return errors.New("invalid test origin")
	}
	runner, err := testbots.New(cfg)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	done := make(chan struct{})
	go func() { runner.Start(ctx); close(done) }()
	server := &http.Server{Addr: ":8090", Handler: runner, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 40 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	go func() {
		<-ctx.Done()
		shutdown, stop := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer stop()
		_ = server.Shutdown(shutdown)
	}()
	if err = server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		cancel()
	}
	<-done
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return errors.New("test bot server failed")
	}
	return nil
}

func validAddress(raw string) bool {
	address, err := url.Parse(raw)
	return err == nil && address.Host != "" && address.User == nil && address.Path == "" && address.RawQuery == "" && address.Fragment == "" && (address.Scheme == "http" || address.Scheme == "https")
}
