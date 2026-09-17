//go:build integration

package reconnect

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/stretchr/testify/require"
)

func assertNormalPauseIsSeparateFromGameChain(
	ctx context.Context, tb testing.TB,
	fixture reconnectMigrationFixture,
) {
	tb.Helper()

	var (
		seriesScope     string
		gameScope       string
		normalScope     string
		gameParentID    uuid.UUID
		normalParentID  pgtype.UUID
		ancestorOverlap int
		coverageCount   int
	)
	err := sharedPool.QueryRow(
		ctx, `
		SELECT
			series_pause.scope_kind,
			game_pause.scope_kind,
			normal_pause.scope_kind,
			game_pause.parent_pause_id,
			normal_pause.parent_pause_id
		FROM pauses AS series_pause
		JOIN pauses AS game_pause ON game_pause.id = $2
		JOIN pauses AS normal_pause ON normal_pause.id = $3
		WHERE series_pause.id = $1`,
		fixture.rootPauseID,
		fixture.gamePauseID,
		fixture.normalPauseID,
	).Scan(
		&seriesScope,
		&gameScope,
		&normalScope,
		&gameParentID,
		&normalParentID,
	)
	require.NoError(tb, err)
	require.Equal(tb, "series", seriesScope)
	require.Equal(tb, "game_attempt", gameScope)
	require.Equal(tb, "wave", normalScope)
	require.Equal(tb, fixture.rootPauseID, gameParentID)
	require.False(tb, normalParentID.Valid)

	err = sharedPool.QueryRow(ctx, `
		WITH RECURSIVE game_ancestors AS (
			SELECT parent_pause_id
			FROM pauses
			WHERE id = $1

			UNION ALL

			SELECT pause.parent_pause_id
			FROM pauses AS pause
			JOIN game_ancestors AS ancestor ON pause.id = ancestor.parent_pause_id
			WHERE ancestor.parent_pause_id IS NOT NULL
		)
		SELECT COUNT(*)
		FROM game_ancestors
		WHERE parent_pause_id = $2`, fixture.gamePauseID, fixture.normalPauseID).Scan(&ancestorOverlap)
	require.NoError(tb, err)
	require.Zero(tb, ancestorOverlap)

	err = sharedPool.QueryRow(
		ctx, `
		SELECT COUNT(*)
		FROM wave_members
		WHERE wave_id = $1
			AND participant_id = ANY($2::UUID[])`,
		fixture.normalWaveID,
		fixture.draft.participantIDs,
	).Scan(&coverageCount)
	require.NoError(tb, err)
	require.Equal(tb, len(fixture.draft.participantIDs), coverageCount)
}

func insertReconnectContinuation(
	ctx context.Context,
	fixture reconnectMigrationFixture,
	input reconnectContinuationInput,
) error {
	return insertReconnectContinuationWith(ctx, sharedPool, fixture, input)
}

type reconnectExecutor interface {
	Exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, query string, args ...any) pgx.Row
}

func insertReconnectContinuationWith(
	ctx context.Context,
	executor reconnectExecutor,
	fixture reconnectMigrationFixture,
	input reconnectContinuationInput,
) error {
	owningPauseID := fixture.gamePauseID
	if input.owningPauseID != nil {
		owningPauseID = *input.owningPauseID
	}
	gameAttemptID := fixture.attemptID
	if input.gameAttemptID != nil {
		gameAttemptID = *input.gameAttemptID
	}
	state := input.state
	if state == "" {
		state = "open"
	}
	revision := input.revision
	if revision == 0 {
		revision = 1
	}
	createdAt := input.createdAt
	if createdAt.IsZero() {
		createdAt = input.openedAt
	}
	updatedAt := input.updatedAt
	if updatedAt.IsZero() {
		updatedAt = input.openedAt
	}

	_, err := executor.Exec(
		ctx, `
		INSERT INTO reconnect_intervals (
			id, pause_id, roster_id, series_id, game_attempt_id,
			participant_id, presence_epoch, interval_number,
			continuation_number, continued_from_id, suspended_by_pause_id,
			state, opened_at, deadline_at, closed_at, revision,
			created_at, updated_at
		)
		VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8,
			$9, $10, $11,
			$12, $13, $14, $15, $16,
			$17, $18
		)`,
		input.id,
		owningPauseID,
		fixture.draft.rosterID,
		fixture.draft.seriesID,
		gameAttemptID,
		input.participantID,
		input.presenceEpoch,
		input.intervalNumber,
		input.continuationNumber,
		input.continuedFromID,
		input.suspendedByPauseID,
		state,
		input.openedAt,
		input.deadlineAt,
		input.closedAt,
		revision,
		createdAt,
		updatedAt,
	)
	return err
}

func expireReconnectInterval(
	ctx context.Context, tb testing.TB,
	intervalID uuid.UUID,
	expiredAt time.Time,
) {
	tb.Helper()

	commandTag, err := sharedPool.Exec(ctx, `
		UPDATE reconnect_intervals
		SET state = 'expired',
			closed_at = $2,
			revision = revision + 1,
			updated_at = $2
		WHERE id = $1`, intervalID, expiredAt)
	require.NoError(tb, err)
	require.EqualValues(tb, 1, commandTag.RowsAffected())
}
