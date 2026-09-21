package bootstrap

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
)

func TestResolveMigrationsDirFindsCanonicalSourceDirectory(t *testing.T) {
	workingDir, err := os.Getwd()
	require.NoError(t, err)
	want := filepath.Clean(filepath.Join(workingDir, "..", "..", migrationsDir))

	dir := ResolveMigrationsDir(migrationsDir)

	require.DirExists(t, dir)
	require.Equal(t, want, filepath.Clean(dir))
}

func TestResolveMigrationsDirKeepsExplicitDirectory(t *testing.T) {
	dir := t.TempDir()

	require.Equal(t, dir, ResolveMigrationsDir(dir))
}

func TestResolveMigrationsDirSupportsRuntimeLayouts(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repositoryMigrations := filepath.Join(root, "backend", migrationsDir)
	dockerMigrations := filepath.Join(root, "app", "migrations")
	require.NoError(t, os.MkdirAll(repositoryMigrations, 0o755))
	require.NoError(t, os.MkdirAll(dockerMigrations, 0o755))

	tests := []struct {
		name       string
		workingDir string
		executable string
		want       string
	}{
		{
			name:       "backend working directory",
			workingDir: filepath.Join(root, "backend"),
			want:       repositoryMigrations,
		},
		{
			name:       "repository working directory",
			workingDir: root,
			want:       repositoryMigrations,
		},
		{
			name:       "package working directory",
			workingDir: filepath.Join(root, "backend", "internal", "bootstrap"),
			want:       repositoryMigrations,
		},
		{
			name:       "docker working directory",
			workingDir: filepath.Join(root, "app"),
			executable: filepath.Join(root, "app", "server"),
			want:       dockerMigrations,
		},
		{
			name:       "executable sibling",
			workingDir: filepath.Join(root, "elsewhere"),
			executable: filepath.Join(root, "app", "server"),
			want:       dockerMigrations,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := resolveMigrationsDir(migrationsDir, tt.workingDir, tt.executable)
			require.Equal(t, filepath.Clean(tt.want), filepath.Clean(got))
		})
	}
}

func TestMigrationLineage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		history migrationHistory
		wantErr bool
	}{
		{name: "absent metadata"},
		{name: "empty table", history: migrationHistory{exists: true, kind: "r"}, wantErr: true},
		{name: "zero", history: currentMigrationHistory(0)},
		{name: "partial", history: currentMigrationHistory(6)},
		{name: "head", history: currentMigrationHistory(11)},
		{name: "missing marker", history: migrationHistory{exists: true, kind: "r", rows: currentMigrationHistory(1).rows}, wantErr: true},
		{name: "wrong marker", history: migrationHistory{exists: true, kind: "r", marker: "legacy", rows: currentMigrationHistory(1).rows}, wantErr: true},
		{name: "above source head", history: currentMigrationHistory(31), wantErr: true},
		{name: "view", history: migrationHistory{exists: true, kind: "v"}, wantErr: true},
		{name: "partitioned table", history: migrationHistory{exists: true, kind: "p"}, wantErr: true},
		{name: "missing zero", history: migrationHistory{exists: true, kind: "r", marker: migrationLineage, rows: []migrationVersion{{1, 1, true}}}, wantErr: true},
		{name: "late zero", history: migrationHistory{exists: true, kind: "r", marker: migrationLineage, rows: []migrationVersion{{1, 1, true}, {2, 0, true}}}, wantErr: true},
		{name: "gap", history: migrationHistory{exists: true, kind: "r", marker: migrationLineage, rows: []migrationVersion{{1, 0, true}, {2, 2, true}}}, wantErr: true},
		{name: "duplicate positive", history: migrationHistory{exists: true, kind: "r", marker: migrationLineage, rows: []migrationVersion{{1, 0, true}, {2, 1, true}, {3, 1, true}}}, wantErr: true},
		{name: "duplicate zero", history: migrationHistory{exists: true, kind: "r", rows: []migrationVersion{{1, 0, true}, {2, 0, true}}}, wantErr: true},
		{name: "negative", history: migrationHistory{exists: true, kind: "r", rows: []migrationVersion{{1, -1, true}, {2, 0, true}}}, wantErr: true},
		{name: "unapplied", history: migrationHistory{exists: true, kind: "r", rows: []migrationVersion{{1, 0, true}, {2, 1, false}}}, wantErr: true},
		{name: "unapplied zero", history: migrationHistory{exists: true, kind: "r", rows: []migrationVersion{{1, 0, false}}}, wantErr: true},
		{name: "latest is not head", history: migrationHistory{exists: true, kind: "r", marker: migrationLineage, rows: []migrationVersion{{1, 0, true}, {2, 2, true}, {3, 1, true}}}, wantErr: true},
		{name: "duplicate row id", history: migrationHistory{exists: true, kind: "r", marker: migrationLineage, rows: []migrationVersion{{1, 0, true}, {1, 1, true}}}, wantErr: true},
		{name: "reapplied after down", history: migrationHistory{exists: true, kind: "r", marker: migrationLineage, rows: []migrationVersion{{1, 0, true}, {8, 1, true}, {9, 2, true}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateMigrationHistory(tt.history, 11)
			if tt.wantErr {
				require.ErrorIs(t, err, ErrIncompatibleMigrationLineage)
				return
			}
			require.NoError(t, err)
		})
	}
}

func currentMigrationHistory(head int64) migrationHistory {
	history := migrationHistory{exists: true, kind: "r"}
	if head > 0 {
		history.marker = migrationLineage
	}
	for version := int64(0); version <= head; version++ {
		history.rows = append(history.rows, migrationVersion{id: version + 1, version: version, applied: true})
	}
	return history
}

func TestMigrationSources(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		files   []string
		wantErr bool
	}{
		{name: "empty", wantErr: true},
		{name: "partial", files: []string{"000001_player.sql", "000002_task.sql"}},
		{name: "missing first", files: []string{"000002_task.sql"}, wantErr: true},
		{name: "gap", files: []string{"000001_player.sql", "000003_tournament.sql"}, wantErr: true},
		{name: "duplicate", files: []string{"000001_player.sql", "000001_other.sql"}, wantErr: true},
		{name: "zero file", files: []string{"000000_zero.sql", "000001_player.sql"}, wantErr: true},
		{name: "unversioned SQL", files: []string{"player.sql", "000001_player.sql"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			source := fstest.MapFS{}
			for _, name := range tt.files {
				source[name] = &fstest.MapFile{Data: []byte("-- +goose Up\nSELECT 1;\n-- +goose Down\nSELECT 1;\n")}
			}
			// Provider construction must inspect source without using the database.
			provider, err := newMigrationProvider(new(sql.DB), source)
			if tt.wantErr {
				require.ErrorIs(t, err, ErrIncompatibleMigrationLineage)
				return
			}
			require.NoError(t, err)
			require.Len(t, provider.ListSources(), len(tt.files))
		})
	}
}

func TestMigrationSourcesHaveTwentySixDomainVersions(t *testing.T) {
	t.Parallel()
	provider, err := newMigrationProvider(new(sql.DB), os.DirFS(ResolveMigrationsDir(migrationsDir)))
	require.NoError(t, err)
	sources := provider.ListSources()
	require.Len(t, sources, 26)
	for index, source := range sources {
		require.Equal(t, int64(index+1), source.Version)
		require.Equal(t, goose.TypeSQL, source.Type)
	}
}

func TestReadyWindowPauseMigrationKeepsGuardedResumeEvidence(t *testing.T) {
	t.Parallel()
	source, err := os.ReadFile(filepath.Join(
		ResolveMigrationsDir(migrationsDir),
		"000020_ready_window_pause_clock.sql",
	))
	require.NoError(t, err)
	sql := string(source)
	for _, fragment := range []string{
		"CREATE TABLE public.ready_window_pause_clocks",
		"original_deadline timestamp with time zone NOT NULL",
		"frozen_remaining interval NOT NULL",
		"NEW.revision <> OLD.revision + 1",
		"OR OLD.resumed_at IS NOT NULL",
		"resumed_deadline = resumed_at + frozen_remaining",
		"clock.original_deadline = OLD.deadline",
		"clock.resumed_deadline = NEW.deadline",
		"pause.state = 'active'",
	} {
		require.Contains(t, sql, fragment)
	}
}

func TestProjectionEvidencePayloadsPreserveSerialization(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile(filepath.Join(
		ResolveMigrationsDir(migrationsDir),
		"000011_projection_schema.sql",
	))
	require.NoError(t, err)

	tablePattern := regexp.MustCompile(`(?ms)^CREATE TABLE public\.([a-z_]+) \(\n(.*?)^\);$`)
	payloadDigestPattern := regexp.MustCompile(`(?m)^    payload (?:json|jsonb) NOT NULL,\n    payload_digest bytea NOT NULL,`)
	serializationPreservingPattern := regexp.MustCompile(`(?m)^    payload json NOT NULL,\n    payload_digest bytea NOT NULL,`)

	var (
		evidenceTables           []string
		normalizingPayloadTables []string
	)
	for _, match := range tablePattern.FindAllSubmatch(source, -1) {
		if !payloadDigestPattern.Match(match[2]) {
			continue
		}

		table := string(match[1])
		evidenceTables = append(evidenceTables, table)
		if !serializationPreservingPattern.Match(match[2]) {
			normalizingPayloadTables = append(normalizingPayloadTables, table)
		}
	}
	sort.Strings(evidenceTables)
	sort.Strings(normalizingPayloadTables)
	require.Equal(t, []string{
		"correction_projection_decisions",
		"projection_artifacts",
		"result_projection_nodes",
	}, evidenceTables)
	require.Empty(t, normalizingPayloadTables,
		"payload columns paired with byte digests must preserve their exact server-owned serialization")
}

func TestMigrationRunRejectsFullForeignHistory(t *testing.T) {
	for _, direction := range []string{"up", "down"} {
		t.Run(direction, func(t *testing.T) {
			state := &migrationTestState{history: currentMigrationHistory(12)}
			state.history.marker = ""
			db, provider := migrationTestProvider(t, state)

			err := runMigration(t.Context(), db, provider, direction)

			require.ErrorIs(t, err, ErrIncompatibleMigrationLineage)
			require.Zero(t, state.writes)
			require.False(t, state.readBeforeLock, "Goose must not inspect metadata before the lineage lock")
			require.False(t, state.locked, "rejection must release the lock")
		})
	}
}

func TestMigrationRunWaitsBeforeCreatingMetadata(t *testing.T) {
	for _, direction := range []string{"up", "down"} {
		t.Run(direction, func(t *testing.T) {
			lockErr := errors.New("migration lock unavailable")
			state := &migrationTestState{lockErr: lockErr}
			db, provider := migrationTestProvider(t, state)

			err := runMigration(t.Context(), db, provider, direction)

			require.ErrorIs(t, err, lockErr)
			require.Zero(t, state.writes, "blocked migration must not create metadata")
			require.False(t, state.history.exists)
			require.False(t, state.readBeforeLock)
		})
	}
}

func TestMigrationRunChecksCurrentHeadUnderLock(t *testing.T) {
	state := &migrationTestState{history: currentMigrationHistory(26)}
	db, provider := migrationTestProvider(t, state)

	require.NoError(t, runMigration(t.Context(), db, provider, "up"))

	require.False(t, state.readBeforeLock)
	require.Zero(t, state.writes)
	require.False(t, state.locked)
}

func migrationTestProvider(t *testing.T, state *migrationTestState) (*sql.DB, *goose.Provider) {
	t.Helper()
	db := sql.OpenDB(migrationTestConnector{state})
	db.SetMaxOpenConns(2)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	provider, err := newMigrationProvider(db, os.DirFS(ResolveMigrationsDir(migrationsDir)))
	require.NoError(t, err)
	return db, provider
}

// This driver exercises Goose's actual control flow without a PostgreSQL server.
// It accepts only metadata operations needed by the regression scenarios.
type migrationTestState struct {
	history        migrationHistory
	lockErr        error
	locked         bool
	readBeforeLock bool
	writes         int
}

type migrationTestConnector struct{ state *migrationTestState }

func (c migrationTestConnector) Connect(context.Context) (driver.Conn, error) {
	return &migrationTestConn{c.state}, nil
}

func (c migrationTestConnector) Driver() driver.Driver { return migrationTestDriver(c) }

type migrationTestDriver struct{ state *migrationTestState }

func (d migrationTestDriver) Open(string) (driver.Conn, error) {
	return &migrationTestConn{d.state}, nil
}

type migrationTestConn struct{ state *migrationTestState }

func (*migrationTestConn) Prepare(string) (driver.Stmt, error) { return nil, errors.ErrUnsupported }
func (*migrationTestConn) Close() error                        { return nil }
func (*migrationTestConn) Begin() (driver.Tx, error)           { return migrationTestTx{}, nil }
func (*migrationTestConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return migrationTestTx{}, nil
}

func (c *migrationTestConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if strings.Contains(query, "pg_advisory_lock(") {
		if c.state.lockErr != nil {
			return nil, c.state.lockErr
		}
		c.state.locked = true
		return driver.RowsAffected(1), nil
	}
	if strings.HasPrefix(query, "CREATE TABLE public.goose_db_version") {
		c.state.history.exists = true
		c.state.history.kind = "r"
		c.state.writes++
		return driver.RowsAffected(1), nil
	}
	if strings.HasPrefix(query, "INSERT INTO public.goose_db_version") {
		c.state.history.rows = append(c.state.history.rows, migrationVersion{
			id: int64(len(c.state.history.rows) + 1), version: args[0].Value.(int64), applied: true,
		})
		c.state.writes++
		return driver.RowsAffected(1), nil
	}
	return nil, fmt.Errorf("unexpected migration test exec: %s", query)
}

func (c *migrationTestConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if strings.Contains(query, "pg_advisory_unlock(") {
		locked := c.state.locked
		c.state.locked = false
		return &migrationTestRows{columns: []string{"unlocked"}, values: [][]driver.Value{{locked}}}, nil
	}
	if !c.state.locked {
		c.state.readBeforeLock = true
	}
	if strings.Contains(query, "FROM pg_tables") {
		return &migrationTestRows{columns: []string{"exists"}, values: [][]driver.Value{{c.state.history.exists}}}, nil
	}
	if strings.Contains(query, "FROM pg_catalog.pg_class") {
		rows := &migrationTestRows{columns: []string{"relkind", "marker"}}
		if c.state.history.exists {
			rows.values = [][]driver.Value{{c.state.history.kind, c.state.history.marker}}
		}
		return rows, nil
	}
	if strings.Contains(query, "FROM public.goose_db_version ORDER BY id") {
		rows := &migrationTestRows{columns: []string{"id", "version_id", "is_applied"}}
		for _, row := range c.state.history.rows {
			rows.values = append(rows.values, []driver.Value{row.id, row.version, row.applied})
		}
		return rows, nil
	}
	if strings.Contains(query, "from public.goose_db_version ORDER BY id DESC") {
		rows := &migrationTestRows{columns: []string{"version_id", "is_applied"}}
		for index := len(c.state.history.rows) - 1; index >= 0; index-- {
			row := c.state.history.rows[index]
			rows.values = append(rows.values, []driver.Value{row.version, row.applied})
		}
		return rows, nil
	}
	return nil, fmt.Errorf("unexpected migration test query: %s", query)
}

type migrationTestTx struct{}

func (migrationTestTx) Commit() error   { return nil }
func (migrationTestTx) Rollback() error { return nil }

type migrationTestRows struct {
	columns []string
	values  [][]driver.Value
	index   int
}

func (r *migrationTestRows) Columns() []string { return r.columns }
func (*migrationTestRows) Close() error        { return nil }
func (r *migrationTestRows) Next(values []driver.Value) error {
	if r.index == len(r.values) {
		return io.EOF
	}
	copy(values, r.values[r.index])
	r.index++
	return nil
}
