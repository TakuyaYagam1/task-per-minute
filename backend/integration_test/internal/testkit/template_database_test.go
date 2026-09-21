//go:build integration

package testkit

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigrationTemplateDatabaseNameTracksMigrationContents(t *testing.T) {
	t.Parallel()

	firstDir := t.TempDir()
	secondDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(firstDir, "000001_schema.sql"), []byte("SELECT 1;\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(secondDir, "000001_schema.sql"), []byte("SELECT 2;\n"), 0o600))

	firstName, err := migrationTemplateDatabaseName(firstDir)
	require.NoError(t, err)
	secondName, err := migrationTemplateDatabaseName(secondDir)
	require.NoError(t, err)

	require.Regexp(t, `^tpm_template_[0-9a-f]{16}$`, firstName)
	require.NotEqual(t, firstName, secondName)
}

func TestDSNForDatabasePreservesOptionsAndChangesScope(t *testing.T) {
	t.Parallel()

	dsn, err := dsnForDatabase(
		"postgres://user:password@127.0.0.1:5432/original?application_name=integration&search_path=old",
		"replacement",
		"public",
	)
	require.NoError(t, err)

	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	require.Equal(t, "/replacement", parsed.Path)
	require.Equal(t, "integration", parsed.Query().Get("application_name"))
	require.Equal(t, "public", parsed.Query().Get("search_path"))
}
