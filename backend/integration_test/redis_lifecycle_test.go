//go:build integration

package integration_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

type redisFx struct {
	client *goredis.Client
}

var (
	redisOnce     sync.Once
	redisFixture  *redisFx
	errRedisInit  error
	redisTeardown func()
)

func sharedRedis(t *testing.T) *redisFx {
	t.Helper()
	redisOnce.Do(func() {
		redisFixture, redisTeardown, errRedisInit = startRedis()
	})
	if errRedisInit != nil {
		t.Fatalf("redis setup: %v", errRedisInit)
	}
	return redisFixture
}

func startRedis() (*redisFx, func(), error) {
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        "redis:8-alpine",
		ExposedPorts: []string{"6379/tcp"},
		WaitingFor: wait.ForLog("Ready to accept connections").
			WithStartupTimeout(containerStartupTimeout),
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
	return &redisFx{client: client}, teardown, nil
}
