package bootstrap

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/jackc/pgx/v5/stdlib" // register pgx database/sql driver for goose
	"github.com/pressly/goose/v3"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/config"
)

const migrationsDir = "db/migrations"

type Migrator struct {
	dsn string
	dir string
}

func NewMigrator(dsn, dir string) *Migrator {
	return &Migrator{dsn: dsn, dir: dir}
}

func Migrate(ctx context.Context, cfg *config.Config, log logkit.Logger) error {
	return RunMigrations(ctx, cfg, log, "up")
}

func RunMigrations(ctx context.Context, cfg *config.Config, log logkit.Logger, command string) error {
	if ctx == nil {
		return errors.New("app migrate: nil context")
	}
	if cfg == nil {
		return errors.New("app migrate: nil config")
	}
	return RunMigrationsDSN(ctx, cfg.DB.DSN, log, command)
}

func RunMigrationsDSN(ctx context.Context, dsn string, log logkit.Logger, command string) error {
	if ctx == nil {
		return errors.New("app migrate: nil context")
	}
	if dsn == "" {
		return errors.New("app migrate: empty DB_DSN")
	}
	if command == "" {
		command = "up"
	}

	if log != nil {
		log.Info("database migrations starting", logkit.Fields{"command": command})
	}
	migrator := NewMigrator(dsn, ResolveMigrationsDir(migrationsDir))

	var err error
	switch command {
	case "up":
		err = migrator.Up(ctx)
	case "down":
		err = migrator.Down(ctx)
	case "status":
		err = migrator.Status(ctx)
	default:
		err = fmt.Errorf("unknown migration command %q", command)
	}
	if err != nil {
		return fmt.Errorf("app migrate: %w", err)
	}
	if log != nil {
		log.Info("database migrations finished", logkit.Fields{"command": command})
	}
	return nil
}

func ResolveMigrationsDir(dir string) string {
	workingDir, _ := os.Getwd()
	executable, _ := os.Executable()
	return resolveMigrationsDir(dir, workingDir, executable)
}

func resolveMigrationsDir(dir, workingDir, executable string) string {
	for _, candidate := range migrationDirCandidates(dir, workingDir, executable) {
		if candidate == "" {
			continue
		}
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return dir
}

func migrationDirCandidates(dir, workingDir, executable string) []string {
	if dir == "" {
		return nil
	}
	if filepath.IsAbs(dir) {
		return []string{filepath.Clean(dir)}
	}

	base := filepath.Base(dir)
	candidates := make([]string, 0, 7)
	if workingDir != "" {
		candidates = append(candidates,
			filepath.Join(workingDir, dir),
			filepath.Join(workingDir, base),
			filepath.Join(workingDir, "backend", dir),
			filepath.Join(workingDir, "..", dir),
			filepath.Join(workingDir, "..", "..", dir),
		)
	}
	if executable != "" {
		executableDir := filepath.Dir(executable)
		candidates = append(candidates,
			filepath.Join(executableDir, base),
			filepath.Join(executableDir, "..", dir),
		)
	}
	return candidates
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
