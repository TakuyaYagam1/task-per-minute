//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	goredis "github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

const containerStartupTimeout = 90 * time.Second

var sharedPool *pgxpool.Pool

func TestMain(m *testing.M) {
	pool, teardown, err := startPostgres()
	if err != nil {
		fmt.Fprintf(os.Stderr, "integration_test: failed to start postgres: %v\n", err)
		os.Exit(1)
	}
	sharedPool = pool

	code := m.Run()

	teardown()
	if redisTeardown != nil {
		redisTeardown()
	}
	if seaweedTeardown != nil {
		seaweedTeardown()
	}
	os.Exit(code)
}

type redisFx struct {
	client *goredis.Client
}

var (
	redisOnce     sync.Once
	redisFixture  *redisFx
	redisInitErr  error
	redisTeardown func()
)

func sharedRedis(t *testing.T) *redisFx {
	t.Helper()
	redisOnce.Do(func() {
		redisFixture, redisTeardown, redisInitErr = startRedis()
	})
	if redisInitErr != nil {
		t.Fatalf("redis setup: %v", redisInitErr)
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

// SeaweedFS is started lazily on the first call to sharedSeaweed(t) so PG-only
// test runs do not pay the ~5s startup cost. The same container is reused
// across all tests in the package; per-test isolation is achieved through
// unique object keys.
type seaweedFx struct {
	endpoint string
	bucket   string
}

var (
	seaweedOnce     sync.Once
	seaweedFixture  *seaweedFx
	seaweedInitErr  error
	seaweedTeardown func()
)

func sharedSeaweed(t *testing.T) *seaweedFx {
	t.Helper()
	seaweedOnce.Do(func() {
		seaweedFixture, seaweedTeardown, seaweedInitErr = startSeaweedFS()
	})
	if seaweedInitErr != nil {
		t.Fatalf("seaweedfs setup: %v", seaweedInitErr)
	}
	return seaweedFixture
}

const seaweedIdentitiesJSON = `{
  "identities": [
    {
      "name": "tpm",
      "credentials": [{"accessKey": "tpm", "secretKey": "tpm-secret"}],
      "actions": ["Admin"]
    }
  ]
}`

func startSeaweedFS() (*seaweedFx, func(), error) {
	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		Image: "chrislusf/seaweedfs:3.71",
		Cmd: []string{
			"server",
			"-dir=/data",
			"-s3",
			"-s3.port=8333",
			"-s3.config=/etc/seaweedfs/s3.json",
		},
		ExposedPorts: []string{"8333/tcp"},
		Files: []testcontainers.ContainerFile{
			{
				Reader:            strings.NewReader(seaweedIdentitiesJSON),
				ContainerFilePath: "/etc/seaweedfs/s3.json",
				FileMode:          0o644,
			},
		},
		WaitingFor: wait.ForListeningPort("8333/tcp").
			WithStartupTimeout(containerStartupTimeout),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("start seaweedfs container: %w", err)
	}

	host, err := c.Host(ctx)
	if err != nil {
		return nil, nil, errors.Join(err, c.Terminate(ctx))
	}
	port, err := c.MappedPort(ctx, "8333/tcp")
	if err != nil {
		return nil, nil, errors.Join(err, c.Terminate(ctx))
	}

	teardown := func() {
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Terminate(termCtx)
	}
	return &seaweedFx{
		endpoint: fmt.Sprintf("%s:%s", host, port.Port()),
		bucket:   "tpm-test",
	}, teardown, nil
}

func startPostgres() (*pgxpool.Pool, func(), error) {
	ctx := context.Background()

	pgC, err := postgres.Run(ctx, "postgres:18-alpine",
		postgres.WithDatabase("tpm_test"),
		postgres.WithUsername("tpm"),
		postgres.WithPassword("tpm"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(containerStartupTimeout),
		),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("start container: %w", err)
	}

	dsn, err := pgC.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return nil, nil, errors.Join(err, pgC.Terminate(ctx))
	}

	if err := runMigrations(ctx, dsn); err != nil {
		return nil, nil, errors.Join(err, pgC.Terminate(ctx))
	}

	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, nil, errors.Join(err, pgC.Terminate(ctx))
	}
	poolCfg.MaxConns = 50
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, nil, errors.Join(err, pgC.Terminate(ctx))
	}

	if err := truncateTables(ctx, pool); err != nil {
		pool.Close()
		return nil, nil, errors.Join(err, pgC.Terminate(ctx))
	}

	teardown := func() {
		pool.Close()
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = pgC.Terminate(termCtx)
	}
	return pool, teardown, nil
}

func runMigrations(ctx context.Context, dsn string) error {
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open sql.DB: %w", err)
	}
	defer sqlDB.Close()

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("goose dialect: %w", err)
	}
	if err := goose.UpContext(ctx, sqlDB, migrationsDirAbs()); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	return nil
}

func migrationsDirAbs() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "migrations")
}

// uniq builds a unique-per-call identifier suffixed with 8 hex chars.
// Tests use it to scope their entities (usernames, task titles, file keys) so
// parallel tests do not collide on UNIQUE constraints or shared bucket counts.
func uniq(prefix string) string {
	return prefix + "_" + uuid.NewString()[:8]
}
