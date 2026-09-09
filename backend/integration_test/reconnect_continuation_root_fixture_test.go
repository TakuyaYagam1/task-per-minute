//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/stretchr/testify/require"
)

func createCancelledReconnectRoot(
	ctx context.Context, tb testing.TB,
	slotLimit int,
) (reconnectMigrationFixture, uuid.UUID, time.Time, time.Time) {
	tb.Helper()

	fixture := createReconnectMigrationFixtureWithSlotLimit(ctx, tb, slotLimit)
	participantID := fixture.draft.participantIDs[0]
	sourceID, sourceDeadline := disconnectParticipant(
		ctx, tb,
		fixture,
		participantID,
		fixture.pausedAt.Add(time.Second),
		2*time.Minute,
	)
	suspendedAt := fixture.pausedAt.Add(20 * time.Second)
	waveID, _ := createMigrationWave(
		ctx, tb,
		fixture.draft.tournamentID,
		fixture.draft.rosterID,
		fixture.draft.participantIDs,
		fixture.pausedAt,
	)
	fixture.normalWaveID = waveID
	fixture.normalPauseID = createNormalWavePauseAndSuspendInterval(
		ctx, tb,
		fixture,
		waveID,
		sourceID,
		suspendedAt,
	)

	var (
		pauseStartedAt time.Time
		sourceClosedAt time.Time
		suspendingID   uuid.UUID
	)
	err := sharedPool.QueryRow(ctx, `
		SELECT pause.started_at, source.closed_at, source.suspended_by_pause_id
		FROM pauses AS pause
		JOIN reconnect_intervals AS source ON source.id = $2
		WHERE pause.id = $1`, fixture.normalPauseID, sourceID).Scan(
		&pauseStartedAt,
		&sourceClosedAt,
		&suspendingID,
	)
	require.NoError(tb, err)
	require.True(tb, suspendedAt.Equal(pauseStartedAt))
	require.True(tb, pauseStartedAt.Equal(sourceClosedAt))
	require.Equal(tb, fixture.normalPauseID, suspendingID)
	return fixture, sourceID, sourceDeadline, suspendedAt
}

func insertNormalWavePauseEvidence(
	ctx context.Context,
	executor reconnectExecutor,
	fixture reconnectMigrationFixture,
	waveID uuid.UUID,
	pauseID uuid.UUID,
	revisionID uuid.UUID,
	suspendedAt time.Time,
) error {
	return insertNormalWavePauseEvidenceForParticipants(
		ctx,
		executor,
		fixture,
		waveID,
		pauseID,
		revisionID,
		suspendedAt,
		fixture.draft.participantIDs,
	)
}

func insertNormalWavePauseEvidenceForParticipants(
	ctx context.Context,
	executor reconnectExecutor,
	fixture reconnectMigrationFixture,
	waveID uuid.UUID,
	pauseID uuid.UUID,
	revisionID uuid.UUID,
	suspendedAt time.Time,
	participantIDs []uuid.UUID,
) error {
	_, err := executor.Exec(
		ctx, `
		INSERT INTO pauses (
			id, tournament_id, roster_id, scope_kind, scope_id,
			wave_id, depth, reason, paused_from_state,
			current_revision_id, started_at, created_at, updated_at
		)
		VALUES (
			$1, $2, $3, 'wave', $4,
			$4, 0, 'operator', 'active',
			$5, $6, $6, $6
		)`,
		pauseID,
		fixture.draft.tournamentID,
		fixture.draft.rosterID,
		waveID,
		revisionID,
		suspendedAt,
	)
	if err != nil {
		return err
	}
	_, err = executor.Exec(ctx, `
		INSERT INTO pause_revisions (
			id, pause_id, revision_number, state, created_at
		)
		VALUES ($1, $2, 1, 'active', $3)`, revisionID, pauseID, suspendedAt)
	if err != nil {
		return err
	}
	for _, participantID := range participantIDs {
		_, err = executor.Exec(
			ctx, `
			INSERT INTO pause_presence_snapshots (
				pause_id, roster_id, series_id, participant_id,
				presence_state, presence_epoch, presence_revision,
				captured_at, created_at
			)
			SELECT
				$1, presence.roster_id, presence.series_id, presence.participant_id,
				presence.state, presence.presence_epoch, presence.revision,
				$3, $3
			FROM presence_states AS presence
			WHERE presence.series_id = $2 AND presence.participant_id = $4`,
			pauseID,
			fixture.draft.seriesID,
			suspendedAt,
			participantID,
		)
		if err != nil {
			return err
		}
	}
	return nil
}

func attemptReconnectRoot(
	ctx context.Context,
	fixture reconnectMigrationFixture,
	participantID uuid.UUID,
	openedAt time.Time,
	deadlineAt time.Time,
	started chan<- int,
) error {
	tx, err := sharedPool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, "SET LOCAL lock_timeout = '2s'")
	if err != nil {
		return err
	}
	var backendPID int
	if err = tx.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&backendPID); err != nil {
		return err
	}
	if started != nil {
		started <- backendPID
	}
	_, err = tx.Exec(ctx, `
		SELECT 1 FROM pauses WHERE id = $1 FOR UPDATE`, fixture.gamePauseID)
	if err != nil {
		return err
	}
	if err = insertReconnectRootEvidence(
		ctx,
		tx,
		fixture,
		participantID,
		uuid.New(),
		openedAt,
		deadlineAt,
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func insertReconnectRootEvidence(
	ctx context.Context,
	executor reconnectExecutor,
	fixture reconnectMigrationFixture,
	participantID uuid.UUID,
	intervalID uuid.UUID,
	openedAt time.Time,
	deadlineAt time.Time,
) error {
	var presenceEpoch int64
	err := executor.QueryRow(
		ctx, `
		UPDATE presence_states
		SET state = 'disconnected',
			presence_epoch = presence_epoch + 1,
			revision = revision + 1,
			disconnected_at = $3,
			updated_at = $3
		WHERE series_id = $1 AND participant_id = $2
		RETURNING presence_epoch`,
		fixture.draft.seriesID,
		participantID,
		openedAt,
	).Scan(&presenceEpoch)
	if err != nil {
		return err
	}
	_, err = executor.Exec(
		ctx, `
		INSERT INTO reconnect_intervals (
			id, pause_id, roster_id, series_id, game_attempt_id,
			participant_id, presence_epoch, interval_number,
			opened_at, deadline_at, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 1, $8, $9, $8, $8)`,
		intervalID,
		fixture.gamePauseID,
		fixture.draft.rosterID,
		fixture.draft.seriesID,
		fixture.attemptID,
		participantID,
		presenceEpoch,
		openedAt,
		deadlineAt,
	)
	if err != nil {
		return err
	}
	_, err = executor.Exec(
		ctx, `
		UPDATE reconnect_slot_counters
		SET slots_used = slots_used + 1,
			revision = revision + 1,
			updated_at = $3
		WHERE pause_id = $1 AND participant_id = $2`,
		fixture.gamePauseID,
		participantID,
		openedAt,
	)
	if err != nil {
		return err
	}
	return nil
}
