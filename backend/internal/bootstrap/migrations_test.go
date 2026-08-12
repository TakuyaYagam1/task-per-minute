package bootstrap

import (
	"os"
	"path/filepath"
	"testing"

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
