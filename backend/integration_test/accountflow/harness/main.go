//go:build integration && account_e2e

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/accountflow"
)

const (
	defaultBackendPort = 4319
	defaultFrontendURL = "http://127.0.0.1:3101"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "account browser harness failed: %v\n", err)
		os.Exit(1)
	}
}

func run() (resultErr error) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	frontendOrigin := os.Getenv("E2E_ACCOUNT_FRONTEND_ORIGIN")
	if frontendOrigin == "" {
		frontendOrigin = defaultFrontendURL
	}
	port, err := backendPort(os.Getenv("E2E_ACCOUNT_BACKEND_PORT"))
	if err != nil {
		return err
	}
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("listen on account browser harness port: %w", err)
	}
	defer func() {
		if closeErr := listener.Close(); closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
			resultErr = errors.Join(resultErr, fmt.Errorf("close account browser harness listener: %w", closeErr))
		}
	}()

	fixture, err := accountflow.New(ctx, frontendOrigin)
	if err != nil {
		return err
	}
	defer fixture.Close()

	server := &http.Server{
		Handler:           fixture.Handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- server.Serve(listener)
	}()
	if _, err := fmt.Fprintln(os.Stdout, "account browser harness ready"); err != nil {
		return fmt.Errorf("write account browser harness readiness: %w", err)
	}

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("stop account browser harness: %w", err)
		}
		err := <-serveDone
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve account browser harness: %w", err)
		}
		return nil
	case err := <-serveDone:
		if err == nil || errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve account browser harness: %w", err)
	}
}

func backendPort(value string) (int, error) {
	if value == "" {
		return defaultBackendPort, nil
	}
	port, err := strconv.Atoi(value)
	if err != nil || port < 1024 || port > 65535 {
		return 0, errors.New("E2E_ACCOUNT_BACKEND_PORT must be between 1024 and 65535")
	}
	return port, nil
}
