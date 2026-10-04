//go:build integration

package testkit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
)

// MigrationsThrough bounds a historical migration test to its reviewed source head.
func MigrationsThrough(tb testing.TB, source string, version int64) string {
	tb.Helper()
	migrations, err := goose.CollectMigrations(source, 0, version)
	require.NoError(tb, err)
	require.NotEmpty(tb, migrations)
	require.Equal(tb, version, migrations[len(migrations)-1].Version)
	directory := tb.TempDir()
	destination, err := os.OpenRoot(directory)
	require.NoError(tb, err)
	tb.Cleanup(func() { require.NoError(tb, destination.Close()) })
	for _, migration := range migrations {
		contents, readErr := os.ReadFile(migration.Source)
		require.NoError(tb, readErr)
		require.NoError(tb, destination.WriteFile(filepath.Base(migration.Source), contents, 0o600))
	}
	return directory
}
