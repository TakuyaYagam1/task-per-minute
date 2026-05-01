package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

const (
	defaultDialTimeout  = 3 * time.Second
	defaultReadTimeout  = 3 * time.Second
	defaultWriteTimeout = 3 * time.Second
	defaultPingTimeout  = 2 * time.Second
)

var ErrNilClient = errors.New("redis: nil client")

type Config struct {
	Addr     string
	Password string
	DB       int
}

func New(ctx context.Context, cfg Config) (*goredis.Client, error) {
	client := goredis.NewClient(&goredis.Options{
		Addr:         cfg.Addr,
		Password:     cfg.Password,
		DB:           cfg.DB,
		DialTimeout:  defaultDialTimeout,
		ReadTimeout:  defaultReadTimeout,
		WriteTimeout: defaultWriteTimeout,
	})

	pingCtx, cancel := context.WithTimeout(ctx, defaultPingTimeout)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis - New - Client.Ping: %w", err)
	}

	return client, nil
}

func HealthCheck(ctx context.Context, client *goredis.Client) error {
	if client == nil {
		return ErrNilClient
	}
	if err := client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis - HealthCheck - Client.Ping: %w", err)
	}
	return nil
}
