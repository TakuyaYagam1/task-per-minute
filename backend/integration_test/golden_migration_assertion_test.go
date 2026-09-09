//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func assertGoldenRecoveryRevisionChain(
	ctx context.Context, tb testing.TB,
	fixture goldenMigrationFixture,
	attemptID uuid.UUID,
	createdAt time.Time,
) {
	tb.Helper()
	_, err := sharedPool.Exec(ctx, `
		UPDATE golden_attempts
		SET state = 'technical_pause', paused_at = $2
		WHERE id = $1`, attemptID, createdAt)
	require.ErrorContains(tb, err, "requires current recovery evidence")

	states := []string{"stable", "technical_pause", "recovering", "resumed"}
	var previousRevisionID any
	var lastRevisionID uuid.UUID
	for index, state := range states {
		lastRevisionID = uuid.New()
		recordedAt := createdAt.Add(time.Duration(index+1) * time.Second)
		_, err = sharedPool.Exec(
			ctx, `
			INSERT INTO golden_recovery_revisions (
				id, attempt_id, tournament_id, roster_id,
				revision_number, previous_revision_id, state,
				recovery_evidence, recorded_at, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8::JSONB, $9, $9)`,
			lastRevisionID,
			attemptID,
			fixture.tournamentID,
			fixture.rosterID,
			index+1,
			previousRevisionID,
			state,
			`{"source":"synthetic migration test"}`,
			recordedAt,
		)
		require.NoError(tb, err)
		previousRevisionID = lastRevisionID

		switch state {
		case "technical_pause":
			_, err = sharedPool.Exec(ctx, `
				UPDATE golden_attempts
				SET state = 'technical_pause', paused_at = $2
				WHERE id = $1`, attemptID, recordedAt)
			require.NoError(tb, err)
		case "resumed":
			_, err = sharedPool.Exec(ctx, `
				UPDATE golden_attempts
				SET state = 'active', paused_at = NULL
				WHERE id = $1`, attemptID)
			require.NoError(tb, err)
		}
	}

	_, err = sharedPool.Exec(ctx, `
		UPDATE golden_recovery_revisions
		SET recovery_evidence = '{"source":"rewritten"}'::JSONB
		WHERE id = $1`, lastRevisionID)
	require.ErrorContains(tb, err, "immutable evidence")
}
