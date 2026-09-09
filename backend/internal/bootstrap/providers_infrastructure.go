package bootstrap

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	"github.com/TakuyaYagam1/task-per-minute/config"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/objectstorage"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	redisadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/redis"
)

func providePostgresConfig(cfg *config.Config) postgres.Config {
	return postgres.Config{
		DSN:      cfg.DB.DSN,
		MaxConns: cfg.DB.MaxConns,
	}
}

func providePostgres(ctx context.Context, cfg postgres.Config) (*pgxpool.Pool, func(), error) {
	pool, err := postgres.New(ctx, cfg)
	if err != nil {
		return nil, func() {}, err
	}
	return pool, pool.Close, nil
}

func provideRedisConfig(cfg *config.Config) redisadapter.Config {
	return redisadapter.Config{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	}
}

func provideRedis(ctx context.Context, cfg redisadapter.Config) (*goredis.Client, func(), error) {
	client, err := redisadapter.New(ctx, cfg)
	if err != nil {
		return nil, func() {}, err
	}
	return client, func() { _ = client.Close() }, nil
}

func provideSeaweedConfig(cfg *config.Config) objectstorage.Config {
	return objectstorage.Config{
		Endpoint:       cfg.SeaweedFS.Endpoint,
		PublicEndpoint: cfg.SeaweedFS.PublicEndpoint,
		AccessKey:      cfg.SeaweedFS.AccessKey,
		SecretKey:      cfg.SeaweedFS.SecretKey,
		Bucket:         cfg.SeaweedFS.Bucket,
		Secure:         cfg.SeaweedFS.Secure,
		PublicSecure:   cfg.SeaweedFS.PublicSecure,
	}
}

func provideSeaweedStorage(cfg objectstorage.Config) (*objectstorage.SeaweedStorage, error) {
	return objectstorage.New(cfg)
}

func provideRevocationRedis(client *goredis.Client) *redisadapter.RevocationRedis {
	return redisadapter.NewRevocationRedis(client, redisadapter.DefaultRevocationKeyPrefix)
}
