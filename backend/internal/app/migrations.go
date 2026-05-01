package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	_ "github.com/jackc/pgx/v5/stdlib" // register pgx database/sql driver for goose
	"github.com/pressly/goose/v3"
)

type Migrator struct {
	dsn string
	dir string
}

func NewMigrator(dsn, dir string) *Migrator {
	return &Migrator{dsn: dsn, dir: dir}
}

func ResolveMigrationsDir(dir string) string {
	for _, candidate := range []string{
		dir,
		filepath.Join("..", dir),
		sourceMigrationsDir(),
	} {
		if candidate == "" {
			continue
		}
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return dir
}

func (m *Migrator) Up(ctx context.Context) error {
	if m == nil {
		return nil
	}

	db, err := m.openDB()
	if err != nil {
		return fmt.Errorf("Migrator - Up - openDB: %w", err)
	}
	defer func() {
		_ = db.Close()
	}()

	if err := goose.UpContext(ctx, db, m.dir); err != nil && !errors.Is(err, goose.ErrNoNextVersion) {
		return fmt.Errorf("Migrator - Up - goose.UpContext: %w", err)
	}
	return nil
}

func (m *Migrator) Down(ctx context.Context) error {
	if m == nil {
		return nil
	}

	db, err := m.openDB()
	if err != nil {
		return fmt.Errorf("Migrator - Down - openDB: %w", err)
	}
	defer func() {
		_ = db.Close()
	}()

	if err := goose.DownContext(ctx, db, m.dir); err != nil && !errors.Is(err, goose.ErrNoCurrentVersion) {
		return fmt.Errorf("Migrator - Down - goose.DownContext: %w", err)
	}
	return nil
}

func (m *Migrator) Status(ctx context.Context) error {
	if m == nil {
		return nil
	}

	db, err := m.openDB()
	if err != nil {
		return fmt.Errorf("Migrator - Status - openDB: %w", err)
	}
	defer func() {
		_ = db.Close()
	}()

	if err := goose.StatusContext(ctx, db, m.dir); err != nil {
		return fmt.Errorf("Migrator - Status - goose.StatusContext: %w", err)
	}
	return nil
}

func (m *Migrator) openDB() (*sql.DB, error) {
	if err := goose.SetDialect("postgres"); err != nil {
		return nil, fmt.Errorf("goose.SetDialect: %w", err)
	}
	db, err := sql.Open("pgx", m.dsn)
	if err != nil {
		return nil, fmt.Errorf("sql.Open: %w", err)
	}
	return db, nil
}

func sourceMigrationsDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "migrations")
}
