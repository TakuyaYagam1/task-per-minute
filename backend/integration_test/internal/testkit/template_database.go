//go:build integration

package testkit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const migrationTemplateLockID int64 = 0x74706d5f746d706c

var migrationTemplateNames sync.Map

// startTemplateClone prepares the schema once for a migration digest and
// gives each test package its own database cloned from that immutable image.
func startTemplateClone(
	ctx context.Context,
	config PostgresConfig,
) (*pgxpool.Pool, func(), error) {
	adminConfig, err := pgxpool.ParseConfig(config.DSN)
	if err != nil {
		return nil, nil, fmt.Errorf("parse postgres admin configuration: %w", err)
	}
	adminConfig.MaxConns = 10
	adminPool, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("connect postgres admin pool: %w", err)
	}
	if err := adminPool.Ping(ctx); err != nil {
		adminPool.Close()
		return nil, nil, fmt.Errorf("ping postgres admin pool: %w", err)
	}

	templateName, err := migrationTemplateDatabaseName(config.MigrationsDir)
	if err != nil {
		adminPool.Close()
		return nil, nil, err
	}
	if err := ensureMigrationTemplate(ctx, adminPool, config, templateName); err != nil {
		adminPool.Close()
		return nil, nil, err
	}

	databaseName := "tpm_package_" + uuid.NewString()[:16]
	if err := cloneDatabase(ctx, adminPool, databaseName, templateName); err != nil {
		adminPool.Close()
		return nil, nil, err
	}

	databaseDSN, err := dsnForDatabase(config.DSN, databaseName, "")
	if err != nil {
		dropDatabaseAfterFailure(ctx, adminPool, databaseName)
		adminPool.Close()
		return nil, nil, err
	}
	poolConfig, err := pgxpool.ParseConfig(databaseDSN)
	if err != nil {
		dropDatabaseAfterFailure(ctx, adminPool, databaseName)
		adminPool.Close()
		return nil, nil, fmt.Errorf("parse postgres clone configuration: %w", err)
	}
	poolConfig.MaxConns = 20
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		dropDatabaseAfterFailure(ctx, adminPool, databaseName)
		adminPool.Close()
		return nil, nil, fmt.Errorf("connect postgres clone: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		dropDatabaseAfterFailure(ctx, adminPool, databaseName)
		adminPool.Close()
		return nil, nil, fmt.Errorf("ping postgres clone: %w", err)
	}

	cleanupParent := context.WithoutCancel(ctx)
	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			pool.Close()
			cleanupCtx, cancel := context.WithTimeout(cleanupParent, 15*time.Second)
			defer cancel()
			_ = dropDatabase(cleanupCtx, adminPool, databaseName)
			adminPool.Close()
		})
	}
	return pool, cleanup, nil
}

func ensureMigrationTemplate(
	ctx context.Context,
	adminPool *pgxpool.Pool,
	config PostgresConfig,
	templateName string,
) error {
	connection, err := adminPool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire postgres template lock connection: %w", err)
	}
	defer connection.Release()

	if _, err := connection.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationTemplateLockID); err != nil {
		return fmt.Errorf("lock postgres migration template: %w", err)
	}
	defer func() {
		_, _ = connection.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", migrationTemplateLockID)
	}()

	var exists bool
	if err := connection.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)",
		templateName,
	).Scan(&exists); err != nil {
		return fmt.Errorf("inspect postgres migration template: %w", err)
	}
	if exists {
		return nil
	}

	identifier := pgx.Identifier{templateName}.Sanitize()
	if _, err := connection.Exec(ctx, "CREATE DATABASE "+identifier+" TEMPLATE template0"); err != nil {
		return fmt.Errorf("create postgres migration template: %w", err)
	}
	removeIncompleteTemplate := func() {
		_ = dropDatabase(context.WithoutCancel(ctx), adminPool, templateName)
	}

	templateDSN, err := dsnForDatabase(config.DSN, templateName, "")
	if err != nil {
		removeIncompleteTemplate()
		return err
	}
	migrationConfig := config
	migrationConfig.DSN = templateDSN
	if err := RunMigrations(ctx, migrationConfig); err != nil {
		removeIncompleteTemplate()
		return fmt.Errorf("migrate postgres template: %w", err)
	}
	if _, err := connection.Exec(ctx,
		"ALTER DATABASE "+identifier+" WITH IS_TEMPLATE true ALLOW_CONNECTIONS false",
	); err != nil {
		removeIncompleteTemplate()
		return fmt.Errorf("seal postgres migration template: %w", err)
	}
	return nil
}

func migrationTemplateDatabaseName(migrationsDir string) (string, error) {
	absDir, err := filepath.Abs(migrationsDir)
	if err != nil {
		return "", fmt.Errorf("resolve migrations directory: %w", err)
	}
	if cached, ok := migrationTemplateNames.Load(absDir); ok {
		return cached.(string), nil
	}

	entries, err := os.ReadDir(absDir)
	if err != nil {
		return "", fmt.Errorf("read migrations directory: %w", err)
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			files = append(files, entry.Name())
		}
	}
	if len(files) == 0 {
		return "", errors.New("migration template requires at least one SQL migration")
	}
	sort.Strings(files)

	digest := sha256.New()
	for _, name := range files {
		contents, err := os.ReadFile(filepath.Join(absDir, name))
		if err != nil {
			return "", fmt.Errorf("read migration %s: %w", name, err)
		}
		_, _ = digest.Write([]byte(name))
		_, _ = digest.Write([]byte{0})
		_, _ = digest.Write(contents)
		_, _ = digest.Write([]byte{0})
	}
	sum := digest.Sum(nil)
	name := "tpm_template_" + hex.EncodeToString(sum[:8])
	migrationTemplateNames.Store(absDir, name)
	return name, nil
}

func cloneDatabase(
	ctx context.Context,
	adminPool *pgxpool.Pool,
	databaseName string,
	templateName string,
) error {
	databaseIdentifier := pgx.Identifier{databaseName}.Sanitize()
	templateIdentifier := pgx.Identifier{templateName}.Sanitize()
	if _, err := adminPool.Exec(ctx,
		"CREATE DATABASE "+databaseIdentifier+" TEMPLATE "+templateIdentifier,
	); err != nil {
		return fmt.Errorf("clone postgres test database: %w", err)
	}
	return nil
}

func dropDatabase(ctx context.Context, adminPool *pgxpool.Pool, databaseName string) error {
	identifier := pgx.Identifier{databaseName}.Sanitize()
	if _, err := adminPool.Exec(ctx, "DROP DATABASE "+identifier+" WITH (FORCE)"); err != nil {
		return fmt.Errorf("drop postgres test database: %w", err)
	}
	return nil
}

func dropDatabaseAfterFailure(ctx context.Context, adminPool *pgxpool.Pool, databaseName string) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	_ = dropDatabase(cleanupCtx, adminPool, databaseName)
}

func dsnForDatabase(dsn string, databaseName string, searchPath string) (string, error) {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		parsed, err := url.Parse(dsn)
		if err != nil {
			return "", fmt.Errorf("parse postgres DSN: %w", err)
		}
		parsed.Path = "/" + databaseName
		query := parsed.Query()
		query.Del("dbname")
		query.Del("database")
		if searchPath == "" {
			query.Del("search_path")
		} else {
			query.Set("search_path", searchPath)
		}
		parsed.RawQuery = query.Encode()
		return parsed.String(), nil
	}

	quote := func(value string) string {
		return "'" + strings.NewReplacer(`\`, `\\`, "'", `\'`).Replace(value) + "'"
	}
	result := dsn + " dbname=" + quote(databaseName)
	if searchPath != "" {
		result += " search_path=" + quote(searchPath)
	}
	return result, nil
}
