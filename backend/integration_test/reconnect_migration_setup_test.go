//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/stretchr/testify/require"
)

func createReconnectMigrationFixture(
	ctx context.Context, tb testing.TB,
) reconnectMigrationFixture {
	tb.Helper()
	return createReconnectMigrationFixtureWithSlotLimit(ctx, tb, 2)
}

func createReconnectMigrationFixtureWithSlotLimit(
	ctx context.Context, tb testing.TB,
	slotLimit int,
) reconnectMigrationFixture {
	tb.Helper()

	draft := createDraftMigrationFixture(ctx, tb)
	lockedAt := time.Now().UTC().Add(5 * time.Second).Truncate(time.Microsecond)
	_ = lockMigrationSeries(ctx, tb, draft, lockedAt)
	slotID := createMigrationGameSlot(ctx, tb, draft.seriesID, draft.rosterID, 1, "web")
	attemptID := createActiveMigrationAttempt(
		ctx, tb,
		slotID,
		draft.seriesID,
		draft.rosterID,
		lockedAt.Add(time.Second),
	)
	presenceAt := lockedAt.Add(2 * time.Second)
	for _, participantID := range draft.participantIDs {
		_, err := sharedPool.Exec(
			ctx, `
			INSERT INTO presence_states (
				tournament_id, roster_id, series_id, participant_id,
				state, connected_at, updated_at
			)
			VALUES ($1, $2, $3, $4, 'connected', $5, $5)`,
			draft.tournamentID,
			draft.rosterID,
			draft.seriesID,
			participantID,
			presenceAt,
		)
		require.NoError(tb, err)
	}

	rootPauseID, rootRevisionID := createMigrationPause(
		ctx, tb,
		draft,
		"series",
		draft.seriesID,
		nil,
		nil,
		0,
		"operator",
		"locked",
		presenceAt.Add(time.Second),
		slotLimit,
	)
	gamePauseID, gameRevisionID := createMigrationPause(
		ctx, tb,
		draft,
		"game_attempt",
		attemptID,
		&attemptID,
		&rootPauseID,
		1,
		"disconnect",
		"active",
		presenceAt.Add(2*time.Second),
		slotLimit,
	)

	return reconnectMigrationFixture{
		draft:               draft,
		attemptID:           attemptID,
		rootPauseID:         rootPauseID,
		rootPauseRevisionID: rootRevisionID,
		gamePauseID:         gamePauseID,
		gamePauseRevisionID: gameRevisionID,
		pausedAt:            presenceAt.Add(2 * time.Second),
	}
}

func createMigrationPause(
	ctx context.Context, tb testing.TB,
	draft draftMigrationFixture,
	scopeKind string,
	scopeID uuid.UUID,
	gameAttemptID *uuid.UUID,
	parentPauseID *uuid.UUID,
	depth int,
	reason string,
	pausedFromState string,
	pausedAt time.Time,
	slotLimit int,
) (uuid.UUID, uuid.UUID) {
	tb.Helper()

	pauseID := uuid.New()
	revisionID := uuid.New()
	tx, err := sharedPool.Begin(ctx)
	require.NoError(tb, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(
		ctx, `
		INSERT INTO pauses (
			id, tournament_id, roster_id, scope_kind, scope_id,
			series_id, game_attempt_id, parent_pause_id, depth,
			reason, paused_from_state, current_revision_id,
			started_at, created_at, updated_at
		)
		VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9,
			$10, $11, $12,
			$13, $13, $13
		)`,
		pauseID,
		draft.tournamentID,
		draft.rosterID,
		scopeKind,
		scopeID,
		draft.seriesID,
		gameAttemptID,
		parentPauseID,
		depth,
		reason,
		pausedFromState,
		revisionID,
		pausedAt,
	)
	require.NoError(tb, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO pause_revisions (
			id, pause_id, revision_number, state, created_at
		)
		VALUES ($1, $2, 1, 'active', $3)`, revisionID, pauseID, pausedAt)
	require.NoError(tb, err)

	for _, participantID := range draft.participantIDs {
		_, err = tx.Exec(
			ctx, `
			INSERT INTO pause_presence_snapshots (
				pause_id, roster_id, series_id, participant_id,
				presence_state, presence_epoch, presence_revision,
				captured_at, created_at
			)
			VALUES ($1, $2, $3, $4, 'connected', 1, 1, $5, $5)`,
			pauseID,
			draft.rosterID,
			draft.seriesID,
			participantID,
			pausedAt,
		)
		require.NoError(tb, err)
		_, err = tx.Exec(
			ctx, `
			INSERT INTO reconnect_slot_counters (
				pause_id, roster_id, participant_id, slot_limit,
				created_at, updated_at
			)
			VALUES ($1, $2, $3, $4, $5, $5)`,
			pauseID,
			draft.rosterID,
			participantID,
			slotLimit,
			pausedAt,
		)
		require.NoError(tb, err)
	}

	if gameAttemptID != nil {
		_, err = tx.Exec(
			ctx, `
			INSERT INTO pause_clocks (
				pause_id, game_attempt_id, original_deadline,
				frozen_at, frozen_remaining_ms, created_at, updated_at
			)
			VALUES ($1, $2, $3, $4, 300000, $4, $4)`,
			pauseID,
			*gameAttemptID,
			pausedAt.Add(5*time.Minute),
			pausedAt,
		)
		require.NoError(tb, err)
		_, err = tx.Exec(ctx, `
			UPDATE game_attempts
			SET state = 'paused', revision = revision + 1, updated_at = $2
			WHERE id = $1`, *gameAttemptID, pausedAt)
		require.NoError(tb, err)
	}
	require.NoError(tb, tx.Commit(ctx))
	return pauseID, revisionID
}
