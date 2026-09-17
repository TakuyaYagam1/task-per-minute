//go:build integration

package reconnect

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func RunReconnectMigration(t *testing.T, pool *pgxpool.Pool) {
	runReconnectMigration(t, pool, func(ctx context.Context, t *testing.T) {
		resetMigrationTables(ctx, t)
		defer resetMigrationTables(ctx, t)

		fixture := createReconnectMigrationFixture(ctx, t)
		insertResumeDecision(ctx, t, fixture, fixture.rootPauseID, 1, nil, nil, "resume", fixture.pausedAt)
		insertResumeDecision(ctx, t, fixture, fixture.gamePauseID, 1, nil, nil, "resume", fixture.pausedAt)
		assertParentPauseWaitsForChild(ctx, t, fixture)

		fixture.firstIntervalID, fixture.firstDeadline = disconnectParticipant(
			ctx, t, fixture, fixture.draft.participantIDs[0], fixture.pausedAt.Add(time.Second), 2*time.Minute,
		)
		insertResumeDecision(ctx, t, fixture, fixture.gamePauseID, 2, &fixture.firstIntervalID, nil, "wait_first", fixture.pausedAt.Add(time.Second))

		fixture.secondIntervalID, fixture.secondDeadline = disconnectParticipant(
			ctx, t, fixture, fixture.draft.participantIDs[1], fixture.pausedAt.Add(2*time.Second), 3*time.Minute,
		)
		insertResumeDecision(ctx, t, fixture, fixture.gamePauseID, 3, &fixture.firstIntervalID, &fixture.secondIntervalID, "wait_both", fixture.pausedAt.Add(2*time.Second))
		require.NotEqual(t, fixture.firstDeadline, fixture.secondDeadline)

		reconnectParticipant(ctx, t, fixture, fixture.draft.participantIDs[0], fixture.firstIntervalID, fixture.pausedAt.Add(3*time.Second))
		insertResumeDecision(ctx, t, fixture, fixture.gamePauseID, 4, nil, &fixture.secondIntervalID, "wait_second", fixture.pausedAt.Add(3*time.Second))
		reconnectParticipant(ctx, t, fixture, fixture.draft.participantIDs[1], fixture.secondIntervalID, fixture.pausedAt.Add(4*time.Second))
		insertResumeDecision(ctx, t, fixture, fixture.gamePauseID, 5, nil, nil, "resume", fixture.pausedAt.Add(4*time.Second))
		resumeMigrationPause(ctx, t, fixture, true, fixture.pausedAt.Add(5*time.Second))
		insertResumeDecision(ctx, t, fixture, fixture.rootPauseID, 2, nil, nil, "resume", fixture.pausedAt.Add(5*time.Second))
		resumeMigrationPause(ctx, t, fixture, false, fixture.pausedAt.Add(6*time.Second))

		assertReconnectPersistence(ctx, t, fixture)
		assertReconnectCAS(ctx, t, fixture)
	})
}

func RunReconnectMigrationResumeCAS(t *testing.T, pool *pgxpool.Pool) {
	runReconnectMigration(t, pool, func(ctx context.Context, t *testing.T) {
		resetMigrationTables(ctx, t)
		defer resetMigrationTables(ctx, t)

		fixture := createReconnectMigrationFixture(ctx, t)
		assertPauseResumeRejectedForMigration(ctx, t, fixture.gamePauseID, fixture.gamePauseRevisionID, fixture.pausedAt.Add(time.Second), "current resume decision")
		insertResumeDecision(ctx, t, fixture, fixture.gamePauseID, 1, nil, nil, "resume", fixture.pausedAt.Add(time.Second))
		intervalID, _ := disconnectParticipant(ctx, t, fixture, fixture.draft.participantIDs[0], fixture.pausedAt.Add(2*time.Second), 2*time.Minute)
		assertPauseResumeRejectedForMigration(ctx, t, fixture.gamePauseID, fixture.gamePauseRevisionID, fixture.pausedAt.Add(3*time.Second), "current reconnect evidence")
		reconnectedAt := fixture.pausedAt.Add(4 * time.Second)
		reconnectParticipant(ctx, t, fixture, fixture.draft.participantIDs[0], intervalID, reconnectedAt)
		insertResumeDecision(ctx, t, fixture, fixture.gamePauseID, 2, nil, nil, "resume", reconnectedAt)
		resumeMigrationPause(ctx, t, fixture, true, fixture.pausedAt.Add(5*time.Second))
	})
}

func assertPauseResumeRejectedForMigration(
	ctx context.Context,
	tb testing.TB,
	pauseID, previousRevisionID uuid.UUID,
	resumedAt time.Time,
	expected string,
) {
	tb.Helper()
	// Keep this assertion local to the moved migration suite. The root reconnect
	// assertion helper remains available to the other root reconnect tests.
	tx, err := migrationPool.Begin(ctx)
	require.NoError(tb, err)
	defer func() { _ = tx.Rollback(ctx) }()
	revisionID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO pause_revisions (
			id, pause_id, previous_revision_id, revision_number,
			state, transition_reason, created_at
		)
		VALUES ($1, $2, $3, 2, 'resumed', 'presence restored', $4)`,
		revisionID, pauseID, previousRevisionID, resumedAt)
	require.NoError(tb, err)
	_, err = tx.Exec(ctx, `
		UPDATE pauses
		SET state = 'resumed', current_revision_id = $2, revision = revision + 1,
			resolved_at = $3, updated_at = $3
		WHERE id = $1`, pauseID, revisionID, resumedAt)
	require.ErrorContains(tb, err, expected)
}
