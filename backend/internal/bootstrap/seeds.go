package bootstrap

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/pressly/goose/v3"
	logkit "github.com/wahrwelt-kit/go-logkit"
)

const (
	seedTable   = "public.goose_seed_version"
	seedLineage = "task-per-minute:demo-seed:v1"
)

// RunSeedsDSN applies explicitly requested demonstration data independently of
// the schema history. Normal application startup never calls this function.
func RunSeedsDSN(ctx context.Context, dsn string, logger logkit.Logger, command string) (retErr error) {
	if ctx == nil || dsn == "" {
		return errors.New("seed: context and database connection are required")
	}
	if command != "seed-up" && command != "seed-status" {
		return fmt.Errorf("seed: unknown command %q", command)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	migrator := NewMigrator(dsn, ResolveMigrationsDir(migrationsDir))
	db, err := migrator.openDB()
	if err != nil {
		return fmt.Errorf("seed: open database: %w", err)
	}
	defer func() { _ = db.Close() }()
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("seed: lock connection: %w", err)
	}
	defer func() { _ = conn.Close() }()
	locker, err := lockSeedSchema(ctx, db, conn, migrator.dir)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, locker.unlock(context.WithoutCancel(ctx), conn)) }()
	provider, head, err := newSeedProvider(db)
	if err != nil {
		return err
	}
	version, err := readSeedVersion(ctx, conn, head)
	if err != nil {
		return err
	}
	if command == "seed-up" {
		if _, err := provider.Up(ctx); err != nil {
			return fmt.Errorf("seed: apply demonstration data: %w", err)
		}
		version, err = readSeedVersion(ctx, conn, head)
		if err != nil {
			return err
		}
	}
	if logger != nil {
		logger.Info("demonstration seed", logkit.Fields{"command": command, "version": version})
	}
	return nil
}

func lockSeedSchema(ctx context.Context, db *sql.DB, conn *sql.Conn, dir string) (*migrationLocker, error) {
	schema, err := newMigrationProvider(db, os.DirFS(dir))
	if err != nil {
		return nil, err
	}
	sources := schema.ListSources()
	if len(sources) == 0 {
		return nil, errors.New("seed: schema migrations are missing")
	}
	locker := &migrationLocker{availableHead: sources[len(sources)-1].Version}
	if err := locker.lock(ctx, conn); err != nil {
		return nil, err
	}
	history, err := readMigrationHistory(ctx, conn)
	if err == nil && (len(history.rows) == 0 || history.rows[len(history.rows)-1].version != locker.availableHead) {
		err = errors.New("seed: apply all schema migrations before loading demonstration data")
	}
	if err != nil {
		return nil, errors.Join(err, locker.unlock(context.WithoutCancel(ctx), conn))
	}
	return locker, nil
}

func newSeedProvider(db *sql.DB) (*goose.Provider, int64, error) {
	provider, err := goose.NewProvider(goose.DialectPostgres, db,
		os.DirFS(ResolveMigrationsDir("db/seeds")),
		goose.WithTableName(seedTable), goose.WithDisableGlobalRegistry(true))
	if err != nil {
		return nil, 0, fmt.Errorf("seed: migration source: %w", err)
	}
	seedSources := provider.ListSources()
	if len(seedSources) == 0 {
		return nil, 0, errors.New("seed: demonstration migrations are missing")
	}
	for index, source := range seedSources {
		if source.Type != goose.TypeSQL || source.Version != int64(index+1) {
			return nil, 0, errors.New("seed: source must contain consecutive SQL versions starting at one")
		}
	}
	return provider, seedSources[len(seedSources)-1].Version, nil
}

// Status must not create Goose metadata. An existing history must belong to
// this seed and may not contain gaps, down records or unknown future versions.
func readSeedVersion(ctx context.Context, conn *sql.Conn, availableHead int64) (int64, error) {
	var kind, marker string
	err := conn.QueryRowContext(ctx, `
		SELECT relation.relkind::text,
			COALESCE(pg_catalog.obj_description(relation.oid, 'pg_class'), '')
		FROM pg_catalog.pg_class AS relation
		JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace
		WHERE namespace.nspname = 'public' AND relation.relname = 'goose_seed_version'
	`).Scan(&kind, &marker)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("seed: read metadata: %w", err)
	}
	if kind != "r" {
		return 0, errors.New("seed: history must be an ordinary table")
	}
	rows, err := conn.QueryContext(ctx, `SELECT version_id, is_applied FROM public.goose_seed_version ORDER BY id`)
	if err != nil {
		return 0, fmt.Errorf("seed: read history: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var next int64
	for rows.Next() {
		var version int64
		var applied bool
		if err := rows.Scan(&version, &applied); err != nil {
			return 0, fmt.Errorf("seed: decode history: %w", err)
		}
		if version != next || !applied || version > availableHead {
			return 0, errors.New("seed: incompatible version history")
		}
		next++
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("seed: iterate history: %w", err)
	}
	if next == 0 || !validSeedLineage(marker, next-1) {
		return 0, errors.New("seed: missing history or demonstration lineage marker")
	}
	return next - 1, nil
}

func validSeedLineage(marker string, version int64) bool {
	return marker == seedLineage || (marker == "" && version == 0)
}
