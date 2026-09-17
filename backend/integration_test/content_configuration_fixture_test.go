//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func insertTournamentContentStageDefaults(
	ctx context.Context,
	tb testing.TB,
	configurationID uuid.UUID,
	bo1PoolID uuid.UUID,
	bo3PoolID uuid.UUID,
	createdAt time.Time,
) {
	tb.Helper()

	var hasCategories bool
	err := sharedPool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_schema = 'public'
				AND table_name = 'tournament_content_stage_defaults'
				AND column_name = 'categories'
		)`).Scan(&hasCategories)
	require.NoError(tb, err)
	if !hasCategories {
		_, err = sharedPool.Exec(ctx, `
			INSERT INTO tournament_content_stage_defaults (
				configuration_id, stage, format, category_mode,
				category_pool_revision_id, task_pool_kind, created_at
			)
			VALUES
				($1, 'swiss', 'bo1', 'random', $2, 'normal', $4),
				($1, 'golden', 'bo1', 'random', $2, 'golden', $4),
				($1, 'semifinal', 'bo1', 'draft', $2, 'normal', $4),
				($1, 'final', 'bo3', 'draft', $3, 'normal', $4)`,
			configurationID, bo1PoolID, bo3PoolID, createdAt)
		require.NoError(tb, err)
		return
	}

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO tournament_content_stage_defaults (
			configuration_id, stage, format, category_mode, categories,
			category_pool_revision_id, task_pool_kind, created_at
		)
		VALUES
			($1, 'swiss', 'bo1', 'random', '["web"]'::jsonb, $2, 'normal', $4),
			($1, 'golden', 'bo1', 'random', '["web"]'::jsonb, $2, 'golden', $4),
			($1, 'semifinal', 'bo1', 'draft', '["web", "crypto", "forensics"]'::jsonb, $2, 'normal', $4),
			($1, 'final', 'bo3', 'draft', '["web", "crypto", "forensics", "reverse", "pwn"]'::jsonb, $3, 'normal', $4)`,
		configurationID, bo1PoolID, bo3PoolID, createdAt)
	require.NoError(tb, err)
}
