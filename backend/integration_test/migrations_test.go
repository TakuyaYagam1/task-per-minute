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
