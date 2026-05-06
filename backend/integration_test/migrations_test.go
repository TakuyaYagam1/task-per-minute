//go:build integration

package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigrations_TaskHintsInInitialSchema(t *testing.T) {
	pool, cleanup := SetupTestDB(t)
	t.Cleanup(cleanup)

	_, err := pool.Exec(context.Background(), `
		INSERT INTO tasks (title, description, category, difficulty, time_limit, flag, hint_1, hint_2, hint_3)
		VALUES ('migration hints', 'ok', 'web', 'easy', 60, 'FLAG{ok}', 'h1', 'h2', 'h3')`)
	require.NoError(t, err)
}

func TestMigrations_TaskAssetsAreCategoryAgnostic(t *testing.T) {
	pool, cleanup := SetupTestDB(t)
	t.Cleanup(cleanup)

	_, err := pool.Exec(context.Background(), `
		INSERT INTO tasks (title, description, category, difficulty, time_limit, flag, hint_1, hint_2, hint_3, task_url)
		VALUES ('forensics url', 'ok', 'forensics', 'easy', 60, 'FLAG{ok}', 'h1', 'h2', 'h3', 'https://tasks.example')`)
	require.NoError(t, err)

	_, err = pool.Exec(context.Background(), `
		INSERT INTO tasks (title, description, category, difficulty, time_limit, flag, hint_1, hint_2, hint_3, source_file_url)
		VALUES ('web source', 'ok', 'web', 'easy', 60, 'FLAG{ok}', 'h1', 'h2', 'h3', 'https://files.example/source.zip')`)
	require.NoError(t, err)

	_, err = pool.Exec(context.Background(), `
		INSERT INTO tasks (title, description, category, difficulty, time_limit, flag, hint_1, hint_2, hint_3, task_url, source_file_url)
		VALUES ('pwn with source', 'ok', 'pwn', 'easy', 60, 'FLAG{ok}', 'h1', 'h2', 'h3', 'pwn.example:31337', 'https://files.example/source.zip')`)
	require.NoError(t, err)

	_, err = pool.Exec(context.Background(), `
		INSERT INTO tasks (title, description, category, difficulty, time_limit, flag, hint_1, hint_2, hint_3)
		VALUES ('stego category', 'ok', 'steganography', 'easy', 60, 'FLAG{ok}', 'h1', 'h2', 'h3')`)
	require.NoError(t, err)

	_, err = pool.Exec(context.Background(), `
		INSERT INTO tasks (title, description, category, difficulty, time_limit, flag, hint_1, hint_2, hint_3)
		VALUES ('ppc category', 'ok', 'ppc', 'easy', 60, 'FLAG{ok}', 'h1', 'h2', 'h3')`)
	require.NoError(t, err)

	_, err = pool.Exec(context.Background(), `
		INSERT INTO tasks (title, description, category, difficulty, time_limit, flag, hint_1, hint_2, hint_3)
		VALUES ('unknown category', 'bad', 'network', 'easy', 60, 'FLAG{bad}', 'h1', 'h2', 'h3')`)
	require.Error(t, err)
}
