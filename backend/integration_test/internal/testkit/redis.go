//go:build integration

package testkit

import (
	"context"
	"errors"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// RedisFixture owns the client for one shared integration Redis container.
// Callers must invoke the returned teardown function from StartRedis when the
// process-level fixture is no longer needed.
type RedisFixture struct {
	Client *goredis.Client
}

// StartRedis starts the integration Redis container and verifies the client
// before returning. The timeout is supplied by the integration package so the
// testkit stays reusable without duplicating repository constants.
func StartRedis(startupTimeout time.Duration) (*RedisFixture, func(), error) {
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        "redis:8-alpine",
		ExposedPorts: []string{"6379/tcp"},
		WaitingFor: wait.ForLog("Ready to accept connections").
			WithStartupTimeout(startupTimeout),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("start redis container: %w", err)
	}

	host, err := c.Host(ctx)
	if err != nil {
		return nil, nil, errors.Join(err, c.Terminate(ctx))
	}
	port, err := c.MappedPort(ctx, "6379/tcp")
	if err != nil {
		return nil, nil, errors.Join(err, c.Terminate(ctx))
	}

	client := goredis.NewClient(&goredis.Options{Addr: fmt.Sprintf("%s:%s", host, port.Port())})
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, nil, errors.Join(err, c.Terminate(ctx))
	}

	teardown := func() {
		_ = client.Close()
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Terminate(termCtx)
	}
	return &RedisFixture{Client: client}, teardown, nil
}
