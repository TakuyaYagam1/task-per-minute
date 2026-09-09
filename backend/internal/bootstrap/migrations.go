package bootstrap

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/config"
)

const migrationsDir = "db/migrations"

const (
	migrationLineage = "task-per-minute:domain-schema:v1"
	migrationTable   = "public.goose_db_version"
	// Shared by all domain migration versions and both directions.
	migrationLockID int64 = 0x74706d5f6d696772
)

// ErrIncompatibleMigrationLineage identifies a source or database history that
// cannot safely be used with the domain migrations.
var ErrIncompatibleMigrationLineage = errors.New("incompatible migration lineage")

type migrationVersion struct {
	id      int64
	version int64
	applied bool
}

type migrationHistory struct {
	exists bool
	kind   string
	marker string
	rows   []migrationVersion
}

func validateMigrationHistory(history migrationHistory, availableHead int64) error {
	if !history.exists {
		return nil
	}
	if history.kind != "r" {
		return fmt.Errorf("%w: %s must be an ordinary table", ErrIncompatibleMigrationLineage, migrationTable)
	}
	if len(history.rows) == 0 {
		return fmt.Errorf("%w: %s has no version zero", ErrIncompatibleMigrationLineage, migrationTable)
	}
	var previousID int64
	for index, row := range history.rows {
		// Goose deletes rows on Down. IDs can have gaps after reapplication, but
		// the surviving history must still be applied versions 0, 1, ..., head.
		if row.id <= previousID || !row.applied || row.version != int64(index) {
			return fmt.Errorf("%w: expected applied version %d in ordered history", ErrIncompatibleMigrationLineage, index)
		}
		previousID = row.id
	}
	head := history.rows[len(history.rows)-1].version
	if head > availableHead {
		return fmt.Errorf("%w: database head %d exceeds source head %d", ErrIncompatibleMigrationLineage, head, availableHead)
	}
	if head > 0 && history.marker != migrationLineage {
		return fmt.Errorf("%w: %s is missing the domain marker", ErrIncompatibleMigrationLineage, migrationTable)
	}
	return nil
}

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

	provider, err := newMigrationProvider(db, os.DirFS(m.dir))
	if err != nil {
		return fmt.Errorf("Migrator - Up - source: %w", err)
	}
	if err := runMigration(ctx, db, provider, "up"); err != nil && !errors.Is(err, goose.ErrNoNextVersion) {
		return fmt.Errorf("Migrator - Up - goose: %w", err)
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

	provider, err := newMigrationProvider(db, os.DirFS(m.dir))
	if err != nil {
		return fmt.Errorf("Migrator - Down - source: %w", err)
	}
	if err := runMigration(ctx, db, provider, "down"); err != nil && !errors.Is(err, goose.ErrNoNextVersion) {
		return fmt.Errorf("Migrator - Down - goose: %w", err)
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

	provider, err := newMigrationProvider(db, os.DirFS(m.dir))
	if err != nil {
		return fmt.Errorf("Migrator - Status - source: %w", err)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("Migrator - Status - connection: %w", err)
	}
	defer func() { _ = conn.Close() }()
	history, err := readMigrationHistory(ctx, conn)
	if err != nil {
		return fmt.Errorf("Migrator - Status - history: %w", err)
	}
	sources := provider.ListSources()
	availableHead := sources[len(sources)-1].Version
	log.Printf("migration status: table=%s exists=%t source_head=%d", migrationTable, history.exists, availableHead)
	for _, row := range history.rows {
		log.Printf("migration history: version=%d applied=%t", row.version, row.applied)
	}
	if err := validateMigrationHistory(history, availableHead); err != nil {
		return fmt.Errorf("Migrator - Status - history: %w", err)
	}
	for _, source := range sources {
		state := "pending"
		if source.Version < int64(len(history.rows)) {
			state = "applied"
		}
		log.Printf("migration %d: %s", source.Version, state)
	}
	return nil
}

func (m *Migrator) openDB() (*sql.DB, error) {
	config, err := pgx.ParseConfig(m.dsn)
	if err != nil {
		return nil, fmt.Errorf("parse migration connection: %w", err)
	}
	// Migration bodies contain unqualified function references. Every connection
	// must resolve them in the same schema as the qualified version table.
	config.RuntimeParams["search_path"] = "public"
	db := stdlib.OpenDB(*config)
	// One connection holds the migration lock while Goose uses the other.
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(2)
	return db, nil
}

func runMigration(ctx context.Context, db *sql.DB, provider *goose.Provider, direction string) (retErr error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("migration lock connection: %w", err)
	}
	defer func() { _ = conn.Close() }()
	sources := provider.ListSources()
	locker := migrationLocker{availableHead: sources[len(sources)-1].Version}
	if err := locker.lock(ctx, conn); err != nil {
		return err
	}
	defer func() {
		retErr = errors.Join(retErr, locker.unlock(context.WithoutCancel(ctx), conn))
	}()
	// Provider.Up calls HasPending, which can create metadata without using a
	// SessionLocker. Hold our lock across that check and the entire operation.
	if direction == "up" {
		_, err := provider.Up(ctx)
		return err
	}
	_, err = provider.Down(ctx)
	return err
}

func newMigrationProvider(db *sql.DB, source fs.FS) (*goose.Provider, error) {
	entries, err := fs.ReadDir(source, ".")
	if err != nil {
		return nil, fmt.Errorf("%w: read migration source: %w", ErrIncompatibleMigrationLineage, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".sql" {
			if _, err := goose.NumericComponent(entry.Name()); err != nil {
				return nil, fmt.Errorf("%w: invalid SQL migration name: %w", ErrIncompatibleMigrationLineage, err)
			}
		}
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, source,
		goose.WithTableName(migrationTable),
		goose.WithDisableGlobalRegistry(true),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: collect migration source: %w", ErrIncompatibleMigrationLineage, err)
	}
	for index, migration := range provider.ListSources() {
		if migration.Type != goose.TypeSQL || migration.Version != int64(index+1) {
			return nil, fmt.Errorf("%w: source must contain SQL versions 1 through head without gaps", ErrIncompatibleMigrationLineage)
		}
	}
	return provider, nil
}

type migrationLocker struct {
	availableHead int64
}

func (l *migrationLocker) lock(ctx context.Context, conn *sql.Conn) error {
	if _, err := conn.ExecContext(ctx, "SELECT pg_catalog.pg_advisory_lock($1)", migrationLockID); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	history, err := readMigrationHistory(ctx, conn)
	if err == nil {
		err = validateMigrationHistory(history, l.availableHead)
	}
	if err != nil {
		return errors.Join(err, l.unlock(context.WithoutCancel(ctx), conn))
	}
	return nil
}

func (*migrationLocker) unlock(ctx context.Context, conn *sql.Conn) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var unlocked bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_catalog.pg_advisory_unlock($1)", migrationLockID).Scan(&unlocked); err != nil {
		return fmt.Errorf("release migration lock: %w", err)
	}
	if !unlocked {
		return errors.New("migration lock was not held")
	}
	return nil
}

func readMigrationHistory(ctx context.Context, conn *sql.Conn) (history migrationHistory, retErr error) {
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return history, fmt.Errorf("%w: begin history read: %w", ErrIncompatibleMigrationLineage, err)
	}
	defer func() { _ = tx.Rollback() }()
	err = tx.QueryRowContext(ctx, `
		SELECT relation.relkind::text,
			COALESCE(pg_catalog.obj_description(relation.oid, 'pg_class'), '')
		FROM pg_catalog.pg_class AS relation
		JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace
		WHERE namespace.nspname = 'public' AND relation.relname = 'goose_db_version'
	`).Scan(&history.kind, &history.marker)
	if errors.Is(err, sql.ErrNoRows) {
		return history, nil
	}
	if err != nil {
		return history, fmt.Errorf("%w: read metadata catalog: %w", ErrIncompatibleMigrationLineage, err)
	}
	history.exists = true
	if history.kind != "r" {
		return history, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, version_id, is_applied FROM public.goose_db_version ORDER BY id`)
	if err != nil {
		return history, fmt.Errorf("%w: read metadata history: %w", ErrIncompatibleMigrationLineage, err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && retErr == nil {
			retErr = fmt.Errorf("%w: close metadata history: %w", ErrIncompatibleMigrationLineage, closeErr)
		}
	}()
	for rows.Next() {
		var row migrationVersion
		if err := rows.Scan(&row.id, &row.version, &row.applied); err != nil {
			return history, fmt.Errorf("%w: decode metadata history: %w", ErrIncompatibleMigrationLineage, err)
		}
		history.rows = append(history.rows, row)
	}
	if err := rows.Err(); err != nil {
		return history, fmt.Errorf("%w: iterate metadata history: %w", ErrIncompatibleMigrationLineage, err)
	}
	return history, nil
}
