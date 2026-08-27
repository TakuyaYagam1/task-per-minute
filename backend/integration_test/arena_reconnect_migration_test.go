//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

type arenaReconnectMigrationFixture struct {
	draft               arenaDraftMigrationFixture
	attemptID           uuid.UUID
	rootPauseID         uuid.UUID
	rootPauseRevisionID uuid.UUID
	gamePauseID         uuid.UUID
	gamePauseRevisionID uuid.UUID
	firstIntervalID     uuid.UUID
	secondIntervalID    uuid.UUID
	firstDeadline       time.Time
	secondDeadline      time.Time
	pausedAt            time.Time
}

func TestArenaReconnectMigration(t *testing.T) {
	ctx := context.Background()
	resetArenaMigrationTables(t)
	t.Cleanup(func() { resetArenaMigrationTables(t) })

	fixture := createArenaReconnectMigrationFixture(t, ctx)
	insertArenaResumeDecision(t, ctx, fixture, fixture.rootPauseID, 1, nil, nil, "resume", fixture.pausedAt)
	insertArenaResumeDecision(t, ctx, fixture, fixture.gamePauseID, 1, nil, nil, "resume", fixture.pausedAt)
	assertArenaParentPauseWaitsForChild(t, ctx, fixture)

	fixture.firstIntervalID, fixture.firstDeadline = disconnectArenaParticipant(
		t,
		ctx,
		fixture,
		fixture.draft.participantIDs[0],
		fixture.pausedAt.Add(time.Second),
		2*time.Minute,
	)
	insertArenaResumeDecision(
		t,
		ctx,
		fixture,
		fixture.gamePauseID,
		2,
		&fixture.firstIntervalID,
		nil,
		"wait_first",
		fixture.pausedAt.Add(time.Second),
	)

	fixture.secondIntervalID, fixture.secondDeadline = disconnectArenaParticipant(
		t,
		ctx,
		fixture,
		fixture.draft.participantIDs[1],
		fixture.pausedAt.Add(2*time.Second),
		3*time.Minute,
	)
	insertArenaResumeDecision(
		t,
		ctx,
		fixture,
		fixture.gamePauseID,
		3,
		&fixture.firstIntervalID,
		&fixture.secondIntervalID,
		"wait_both",
		fixture.pausedAt.Add(2*time.Second),
	)
	require.NotEqual(t, fixture.firstDeadline, fixture.secondDeadline)

	reconnectArenaParticipant(
		t,
		ctx,
		fixture,
		fixture.draft.participantIDs[0],
		fixture.firstIntervalID,
		fixture.pausedAt.Add(3*time.Second),
	)
	insertArenaResumeDecision(
		t,
		ctx,
		fixture,
		fixture.gamePauseID,
		4,
		nil,
		&fixture.secondIntervalID,
		"wait_second",
		fixture.pausedAt.Add(3*time.Second),
	)

	reconnectArenaParticipant(
		t,
		ctx,
		fixture,
		fixture.draft.participantIDs[1],
		fixture.secondIntervalID,
		fixture.pausedAt.Add(4*time.Second),
	)
	insertArenaResumeDecision(
		t,
		ctx,
		fixture,
		fixture.gamePauseID,
		5,
		nil,
		nil,
		"resume",
		fixture.pausedAt.Add(4*time.Second),
	)
	resumeArenaMigrationPause(t, ctx, fixture, true, fixture.pausedAt.Add(5*time.Second))

	insertArenaResumeDecision(
		t,
		ctx,
		fixture,
		fixture.rootPauseID,
		2,
		nil,
		nil,
		"resume",
		fixture.pausedAt.Add(5*time.Second),
	)
	resumeArenaMigrationPause(t, ctx, fixture, false, fixture.pausedAt.Add(6*time.Second))

	assertArenaReconnectPersistence(t, ctx, fixture)
	assertArenaReconnectCAS(t, ctx, fixture)
}

func TestArenaReconnectMigrationResumeCAS(t *testing.T) {
	ctx := context.Background()
	resetArenaMigrationTables(t)
	t.Cleanup(func() { resetArenaMigrationTables(t) })

	fixture := createArenaReconnectMigrationFixture(t, ctx)
	assertArenaPauseResumeRejected(
		t,
		ctx,
		fixture.gamePauseID,
		fixture.gamePauseRevisionID,
		fixture.pausedAt.Add(time.Second),
		"current resume decision",
	)

	insertArenaResumeDecision(
		t,
		ctx,
		fixture,
		fixture.gamePauseID,
		1,
		nil,
		nil,
		"resume",
		fixture.pausedAt.Add(time.Second),
	)
	_, _ = disconnectArenaParticipant(
		t,
		ctx,
		fixture,
		fixture.draft.participantIDs[0],
		fixture.pausedAt.Add(2*time.Second),
		2*time.Minute,
	)
	assertArenaPauseResumeRejected(
		t,
		ctx,
		fixture.gamePauseID,
		fixture.gamePauseRevisionID,
		fixture.pausedAt.Add(3*time.Second),
		"current reconnect evidence",
	)

	reconnectedAt := fixture.pausedAt.Add(4 * time.Second)
	_, err := sharedPool.Exec(ctx, `
		UPDATE arena_presence_states
		SET state = 'connected',
			presence_epoch = presence_epoch + 1,
			revision = revision + 1,
			connected_at = $3,
			disconnected_at = NULL,
			updated_at = $3
		WHERE series_id = $1 AND participant_id = $2`,
		fixture.draft.seriesID,
		fixture.draft.participantIDs[0],
		reconnectedAt,
	)
	require.NoError(t, err)
	insertArenaResumeDecision(
		t,
		ctx,
		fixture,
		fixture.gamePauseID,
		2,
		nil,
		nil,
		"resume",
		reconnectedAt,
	)
	assertArenaPauseResumeRejected(
		t,
		ctx,
		fixture.gamePauseID,
		fixture.gamePauseRevisionID,
		fixture.pausedAt.Add(5*time.Second),
		"current reconnect evidence",
	)
}

func TestArenaReconnectMigrationCASLocks(t *testing.T) {
	ctx := context.Background()

	t.Run("interval creation locks live presence", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture := createArenaReconnectMigrationFixture(t, ctx)
		participantID := fixture.draft.participantIDs[0]
		disconnectedAt := fixture.pausedAt.Add(time.Second)
		_, err := sharedPool.Exec(ctx, `
			UPDATE arena_presence_states
			SET state = 'disconnected',
				presence_epoch = presence_epoch + 1,
				revision = revision + 1,
				disconnected_at = $3,
				updated_at = $3
			WHERE series_id = $1 AND participant_id = $2`,
			fixture.draft.seriesID,
			participantID,
			disconnectedAt,
		)
		require.NoError(t, err)

		lockTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = lockTx.Rollback(ctx) }()
		_, err = lockTx.Exec(ctx, `
			SELECT 1
			FROM arena_presence_states
			WHERE series_id = $1 AND participant_id = $2
			FOR NO KEY UPDATE`, fixture.draft.seriesID, participantID)
		require.NoError(t, err)

		probeTx := beginArenaReconnectLockProbe(t, ctx)
		defer func() { _ = probeTx.Rollback(ctx) }()
		_, err = probeTx.Exec(ctx, `
			INSERT INTO arena_reconnect_intervals (
				id, pause_id, roster_id, series_id, game_attempt_id,
				participant_id, presence_epoch, interval_number,
				opened_at, deadline_at, created_at, updated_at
			)
			VALUES (
				$1, $2, $3, $4, $5,
				$6, 2, 1,
				$7, $8, $7, $7
			)`,
			uuid.New(),
			fixture.gamePauseID,
			fixture.draft.rosterID,
			fixture.draft.seriesID,
			fixture.attemptID,
			participantID,
			disconnectedAt,
			disconnectedAt.Add(2*time.Minute),
		)
		require.ErrorContains(t, err, "lock timeout")
	})

	t.Run("resume decision locks live presence", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture := createArenaReconnectMigrationFixture(t, ctx)
		lockTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = lockTx.Rollback(ctx) }()
		_, err = lockTx.Exec(ctx, `
			SELECT 1
			FROM arena_presence_states
			WHERE series_id = $1 AND participant_id = $2
			FOR NO KEY UPDATE`, fixture.draft.seriesID, fixture.draft.participantIDs[0])
		require.NoError(t, err)

		probeTx := beginArenaReconnectLockProbe(t, ctx)
		defer func() { _ = probeTx.Rollback(ctx) }()
		err = insertArenaResumeDecisionEvidence(
			ctx,
			probeTx,
			fixture,
			fixture.gamePauseID,
			1,
			nil,
			nil,
			"connected",
			"connected",
			1,
			1,
			1,
			1,
			"resume",
			fixture.pausedAt,
		)
		require.ErrorContains(t, err, "lock timeout")
	})

	t.Run("resume decision locks reconnect interval", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture := createArenaReconnectMigrationFixture(t, ctx)
		intervalID, _ := disconnectArenaParticipant(
			t,
			ctx,
			fixture,
			fixture.draft.participantIDs[0],
			fixture.pausedAt.Add(time.Second),
			2*time.Minute,
		)

		lockTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = lockTx.Rollback(ctx) }()
		_, err = lockTx.Exec(ctx, `
			SELECT 1
			FROM arena_reconnect_intervals
			WHERE id = $1
			FOR NO KEY UPDATE`, intervalID)
		require.NoError(t, err)

		probeTx := beginArenaReconnectLockProbe(t, ctx)
		defer func() { _ = probeTx.Rollback(ctx) }()
		err = insertArenaResumeDecisionEvidence(
			ctx,
			probeTx,
			fixture,
			fixture.gamePauseID,
			1,
			&intervalID,
			nil,
			"disconnected",
			"connected",
			2,
			1,
			2,
			1,
			"wait_first",
			fixture.pausedAt.Add(time.Second),
		)
		require.ErrorContains(t, err, "lock timeout")
	})

	t.Run("pause resume locks live presence", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture := createArenaReconnectMigrationFixture(t, ctx)
		insertArenaResumeDecision(
			t,
			ctx,
			fixture,
			fixture.gamePauseID,
			1,
			nil,
			nil,
			"resume",
			fixture.pausedAt,
		)

		lockTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = lockTx.Rollback(ctx) }()
		_, err = lockTx.Exec(ctx, `
			SELECT 1
			FROM arena_presence_states
			WHERE series_id = $1 AND participant_id = $2
			FOR NO KEY UPDATE`, fixture.draft.seriesID, fixture.draft.participantIDs[0])
		require.NoError(t, err)

		probeTx := beginArenaReconnectLockProbe(t, ctx)
		defer func() { _ = probeTx.Rollback(ctx) }()
		_, err = probeTx.Exec(ctx, `
			UPDATE arena_pauses
			SET state = 'resumed',
				current_revision_id = $2,
				revision = revision + 1,
				resolved_at = $3,
				updated_at = $3
			WHERE id = $1`,
			fixture.gamePauseID,
			uuid.New(),
			fixture.pausedAt.Add(time.Second),
		)
		require.ErrorContains(t, err, "lock timeout")
	})
}

func assertArenaPauseResumeRejected(
	t testing.TB,
	ctx context.Context,
	pauseID uuid.UUID,
	previousRevisionID uuid.UUID,
	resumedAt time.Time,
	expected string,
) {
	t.Helper()

	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	revisionID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO arena_pause_revisions (
			id, pause_id, previous_revision_id, revision_number,
			state, transition_reason, created_at
		)
		VALUES ($1, $2, $3, 2, 'resumed', 'presence restored', $4)`,
		revisionID,
		pauseID,
		previousRevisionID,
		resumedAt,
	)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		UPDATE arena_pauses
		SET state = 'resumed',
			current_revision_id = $2,
			revision = revision + 1,
			resolved_at = $3,
			updated_at = $3
		WHERE id = $1`, pauseID, revisionID, resumedAt)
	require.ErrorContains(t, err, expected)
}

func beginArenaReconnectLockProbe(t testing.TB, ctx context.Context) pgx.Tx {
	t.Helper()

	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, "SET LOCAL lock_timeout = '100ms'")
	require.NoError(t, err)
	return tx
}

func insertArenaResumeDecisionEvidence(
	ctx context.Context,
	tx pgx.Tx,
	fixture arenaReconnectMigrationFixture,
	pauseID uuid.UUID,
	decisionNumber int,
	firstIntervalID *uuid.UUID,
	secondIntervalID *uuid.UUID,
	firstLiveState string,
	secondLiveState string,
	firstPresenceEpoch int64,
	secondPresenceEpoch int64,
	firstPresenceRevision int64,
	secondPresenceRevision int64,
	action string,
	decidedAt time.Time,
) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO arena_resume_decisions (
			pause_id, decision_number,
			first_participant_id, second_participant_id,
			first_pre_pause_state, second_pre_pause_state,
			first_live_state, second_live_state,
			first_presence_epoch, second_presence_epoch,
			first_presence_revision, second_presence_revision,
			first_reconnect_interval_id, second_reconnect_interval_id,
			action, decided_at, created_at
		)
		VALUES (
			$1, $2,
			$3, $4,
			'connected', 'connected',
			$5, $6,
			$7, $8,
			$9, $10,
			$11, $12,
			$13, $14, $14
		)`,
		pauseID,
		decisionNumber,
		fixture.draft.participantIDs[0],
		fixture.draft.participantIDs[1],
		firstLiveState,
		secondLiveState,
		firstPresenceEpoch,
		secondPresenceEpoch,
		firstPresenceRevision,
		secondPresenceRevision,
		firstIntervalID,
		secondIntervalID,
		action,
		decidedAt,
	)
	return err
}

func createArenaReconnectMigrationFixture(
	t testing.TB,
	ctx context.Context,
) arenaReconnectMigrationFixture {
	t.Helper()

	draft := createArenaDraftMigrationFixture(t, ctx)
	lockedAt := time.Now().UTC().Add(5 * time.Second).Truncate(time.Microsecond)
	_ = lockArenaMigrationSeries(t, ctx, draft, lockedAt)
	slotID := createArenaMigrationGameSlot(t, ctx, draft.seriesID, draft.rosterID, 1, "web")
	attemptID := createActiveArenaMigrationAttempt(
		t,
		ctx,
		slotID,
		draft.seriesID,
		draft.rosterID,
		lockedAt.Add(time.Second),
	)
	presenceAt := lockedAt.Add(2 * time.Second)
	for _, participantID := range draft.participantIDs {
		_, err := sharedPool.Exec(ctx, `
			INSERT INTO arena_presence_states (
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
		require.NoError(t, err)
	}

	rootPauseID, rootRevisionID := createArenaMigrationPause(
		t,
		ctx,
		draft,
		"series",
		draft.seriesID,
		nil,
		nil,
		0,
		"operator",
		"locked",
		presenceAt.Add(time.Second),
	)
	gamePauseID, gameRevisionID := createArenaMigrationPause(
		t,
		ctx,
		draft,
		"game_attempt",
		attemptID,
		&attemptID,
		&rootPauseID,
		1,
		"disconnect",
		"active",
		presenceAt.Add(2*time.Second),
	)

	return arenaReconnectMigrationFixture{
		draft:               draft,
		attemptID:           attemptID,
		rootPauseID:         rootPauseID,
		rootPauseRevisionID: rootRevisionID,
		gamePauseID:         gamePauseID,
		gamePauseRevisionID: gameRevisionID,
		pausedAt:            presenceAt.Add(2 * time.Second),
	}
}

func createArenaMigrationPause(
	t testing.TB,
	ctx context.Context,
	draft arenaDraftMigrationFixture,
	scopeKind string,
	scopeID uuid.UUID,
	gameAttemptID *uuid.UUID,
	parentPauseID *uuid.UUID,
	depth int,
	reason string,
	pausedFromState string,
	pausedAt time.Time,
) (uuid.UUID, uuid.UUID) {
	t.Helper()

	pauseID := uuid.New()
	revisionID := uuid.New()
	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `
		INSERT INTO arena_pauses (
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
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO arena_pause_revisions (
			id, pause_id, revision_number, state, created_at
		)
		VALUES ($1, $2, 1, 'active', $3)`, revisionID, pauseID, pausedAt)
	require.NoError(t, err)

	for _, participantID := range draft.participantIDs {
		_, err = tx.Exec(ctx, `
			INSERT INTO arena_pause_presence_snapshots (
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
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `
			INSERT INTO arena_reconnect_slot_counters (
				pause_id, roster_id, participant_id, slot_limit,
				created_at, updated_at
			)
			VALUES ($1, $2, $3, 2, $4, $4)`,
			pauseID,
			draft.rosterID,
			participantID,
			pausedAt,
		)
		require.NoError(t, err)
	}

	if gameAttemptID != nil {
		_, err = tx.Exec(ctx, `
			INSERT INTO arena_pause_clocks (
				pause_id, game_attempt_id, original_deadline,
				frozen_at, frozen_remaining_ms, created_at, updated_at
			)
			VALUES ($1, $2, $3, $4, 300000, $4, $4)`,
			pauseID,
			*gameAttemptID,
			pausedAt.Add(5*time.Minute),
			pausedAt,
		)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `
			UPDATE arena_game_attempts
			SET state = 'paused', revision = revision + 1, updated_at = $2
			WHERE id = $1`, *gameAttemptID, pausedAt)
		require.NoError(t, err)
	}
	require.NoError(t, tx.Commit(ctx))
	return pauseID, revisionID
}

func insertArenaResumeDecision(
	t testing.TB,
	ctx context.Context,
	fixture arenaReconnectMigrationFixture,
	pauseID uuid.UUID,
	decisionNumber int,
	firstIntervalID *uuid.UUID,
	secondIntervalID *uuid.UUID,
	action string,
	decidedAt time.Time,
) {
	t.Helper()

	type presenceEvidence struct {
		state    string
		epoch    int64
		revision int64
	}
	presence := make([]presenceEvidence, 2)
	for i, participantID := range fixture.draft.participantIDs {
		err := sharedPool.QueryRow(ctx, `
			SELECT state, presence_epoch, revision
			FROM arena_presence_states
			WHERE series_id = $1 AND participant_id = $2`,
			fixture.draft.seriesID,
			participantID,
		).Scan(&presence[i].state, &presence[i].epoch, &presence[i].revision)
		require.NoError(t, err)
	}

	_, err := sharedPool.Exec(ctx, `
		INSERT INTO arena_resume_decisions (
			pause_id, decision_number,
			first_participant_id, second_participant_id,
			first_pre_pause_state, second_pre_pause_state,
			first_live_state, second_live_state,
			first_presence_epoch, second_presence_epoch,
			first_presence_revision, second_presence_revision,
			first_reconnect_interval_id, second_reconnect_interval_id,
			action, decided_at, created_at
		)
		VALUES (
			$1, $2,
			$3, $4,
			'connected', 'connected',
			$5, $6,
			$7, $8,
			$9, $10,
			$11, $12,
			$13, $14, $14
		)`,
		pauseID,
		decisionNumber,
		fixture.draft.participantIDs[0],
		fixture.draft.participantIDs[1],
		presence[0].state,
		presence[1].state,
		presence[0].epoch,
		presence[1].epoch,
		presence[0].revision,
		presence[1].revision,
		firstIntervalID,
		secondIntervalID,
		action,
		decidedAt,
	)
	require.NoError(t, err)
}

func disconnectArenaParticipant(
	t testing.TB,
	ctx context.Context,
	fixture arenaReconnectMigrationFixture,
	participantID uuid.UUID,
	disconnectedAt time.Time,
	window time.Duration,
) (uuid.UUID, time.Time) {
	t.Helper()

	intervalID := uuid.New()
	deadline := disconnectedAt.Add(window)
	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `
		UPDATE arena_presence_states
		SET state = 'disconnected',
			presence_epoch = presence_epoch + 1,
			revision = revision + 1,
			disconnected_at = $3,
			updated_at = $3
		WHERE series_id = $1 AND participant_id = $2`,
		fixture.draft.seriesID,
		participantID,
		disconnectedAt,
	)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO arena_reconnect_intervals (
			id, pause_id, roster_id, series_id, game_attempt_id,
			participant_id, presence_epoch, interval_number,
			opened_at, deadline_at, created_at, updated_at
		)
		VALUES (
			$1, $2, $3, $4, $5,
			$6, 2, 1,
			$7, $8, $7, $7
		)`,
		intervalID,
		fixture.gamePauseID,
		fixture.draft.rosterID,
		fixture.draft.seriesID,
		fixture.attemptID,
		participantID,
		disconnectedAt,
		deadline,
	)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		UPDATE arena_reconnect_slot_counters
		SET slots_used = slots_used + 1,
			revision = revision + 1,
			updated_at = $3
		WHERE pause_id = $1 AND participant_id = $2`,
		fixture.gamePauseID,
		participantID,
		disconnectedAt,
	)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
	return intervalID, deadline
}

func reconnectArenaParticipant(
	t testing.TB,
	ctx context.Context,
	fixture arenaReconnectMigrationFixture,
	participantID uuid.UUID,
	intervalID uuid.UUID,
	reconnectedAt time.Time,
) {
	t.Helper()

	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `
		UPDATE arena_reconnect_intervals
		SET state = 'reconnected',
			closed_at = $2,
			revision = revision + 1,
			updated_at = $2
		WHERE id = $1`, intervalID, reconnectedAt)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		UPDATE arena_presence_states
		SET state = 'connected',
			presence_epoch = presence_epoch + 1,
			revision = revision + 1,
			connected_at = $3,
			disconnected_at = NULL,
			updated_at = $3
		WHERE series_id = $1 AND participant_id = $2`,
		fixture.draft.seriesID,
		participantID,
		reconnectedAt,
	)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
}

func assertArenaParentPauseWaitsForChild(
	t testing.TB,
	ctx context.Context,
	fixture arenaReconnectMigrationFixture,
) {
	t.Helper()

	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	revisionID := uuid.New()
	resolvedAt := fixture.pausedAt.Add(time.Second)
	_, err = tx.Exec(ctx, `
		INSERT INTO arena_pause_revisions (
			id, pause_id, previous_revision_id, revision_number,
			state, transition_reason, created_at
		)
		VALUES ($1, $2, $3, 2, 'resumed', 'operator resume', $4)`,
		revisionID,
		fixture.rootPauseID,
		fixture.rootPauseRevisionID,
		resolvedAt,
	)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		UPDATE arena_pauses
		SET state = 'resumed',
			current_revision_id = $2,
			revision = revision + 1,
			resolved_at = $3,
			updated_at = $3
		WHERE id = $1`, fixture.rootPauseID, revisionID, resolvedAt)
	require.Error(t, err)
}

func resumeArenaMigrationPause(
	t testing.TB,
	ctx context.Context,
	fixture arenaReconnectMigrationFixture,
	gamePause bool,
	resumedAt time.Time,
) {
	t.Helper()

	pauseID := fixture.rootPauseID
	previousRevisionID := fixture.rootPauseRevisionID
	if gamePause {
		pauseID = fixture.gamePauseID
		previousRevisionID = fixture.gamePauseRevisionID
	}
	revisionID := uuid.New()
	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `
		INSERT INTO arena_pause_revisions (
			id, pause_id, previous_revision_id, revision_number,
			state, transition_reason, created_at
		)
		VALUES ($1, $2, $3, 2, 'resumed', 'presence restored', $4)`,
		revisionID,
		pauseID,
		previousRevisionID,
		resumedAt,
	)
	require.NoError(t, err)
	if gamePause {
		_, err = tx.Exec(ctx, `
			UPDATE arena_pause_clocks
			SET resumed_at = $2,
				resumed_deadline = $2 + INTERVAL '5 minutes',
				revision = revision + 1,
				updated_at = $2
			WHERE pause_id = $1`, pauseID, resumedAt)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `
			UPDATE arena_game_attempts
			SET state = 'active', revision = revision + 1, updated_at = $2
			WHERE id = $1`, fixture.attemptID, resumedAt)
		require.NoError(t, err)
	}
	_, err = tx.Exec(ctx, `
		UPDATE arena_pauses
		SET state = 'resumed',
			current_revision_id = $2,
			revision = revision + 1,
			resolved_at = $3,
			updated_at = $3
		WHERE id = $1`, pauseID, revisionID, resumedAt)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
}

func assertArenaReconnectPersistence(
	t testing.TB,
	ctx context.Context,
	fixture arenaReconnectMigrationFixture,
) {
	t.Helper()

	var (
		intervalCount int
		usedSlots     int
		decisionCount int
		remainingMS   int64
		resumedAt     time.Time
		deadline      time.Time
	)
	err := sharedPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM arena_reconnect_intervals
		WHERE pause_id = $1`, fixture.gamePauseID).Scan(&intervalCount)
	require.NoError(t, err)
	require.Equal(t, 2, intervalCount)
	err = sharedPool.QueryRow(ctx, `
		SELECT SUM(slots_used)
		FROM arena_reconnect_slot_counters
		WHERE pause_id = $1`, fixture.gamePauseID).Scan(&usedSlots)
	require.NoError(t, err)
	require.Equal(t, 2, usedSlots)
	err = sharedPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM arena_resume_decisions
		WHERE pause_id = $1`, fixture.gamePauseID).Scan(&decisionCount)
	require.NoError(t, err)
	require.Equal(t, 5, decisionCount)
	err = sharedPool.QueryRow(ctx, `
		SELECT frozen_remaining_ms, resumed_at, resumed_deadline
		FROM arena_pause_clocks
		WHERE pause_id = $1`, fixture.gamePauseID).Scan(&remainingMS, &resumedAt, &deadline)
	require.NoError(t, err)
	require.EqualValues(t, 300000, remainingMS)
	require.Equal(t, resumedAt.Add(5*time.Minute), deadline)
}

func assertArenaReconnectCAS(
	t testing.TB,
	ctx context.Context,
	fixture arenaReconnectMigrationFixture,
) {
	t.Helper()

	commandTag, err := sharedPool.Exec(ctx, `
		UPDATE arena_presence_states
		SET state = 'disconnected',
			presence_epoch = presence_epoch + 1,
			revision = revision + 1,
			disconnected_at = $3,
			updated_at = $3
		WHERE series_id = $1 AND participant_id = $2 AND revision = 1`,
		fixture.draft.seriesID,
		fixture.draft.participantIDs[0],
		fixture.pausedAt.Add(10*time.Second),
	)
	require.NoError(t, err)
	require.Zero(t, commandTag.RowsAffected())

	commandTag, err = sharedPool.Exec(ctx, `
		UPDATE arena_reconnect_intervals
		SET state = 'expired',
			closed_at = $2,
			revision = revision + 1,
			updated_at = $2
		WHERE id = $1 AND revision = 1`,
		fixture.firstIntervalID,
		fixture.pausedAt.Add(10*time.Second),
	)
	require.NoError(t, err)
	require.Zero(t, commandTag.RowsAffected())

	commandTag, err = sharedPool.Exec(ctx, `
		UPDATE arena_pauses
		SET updated_at = $2
		WHERE id = $1 AND revision = 1`,
		fixture.gamePauseID,
		fixture.pausedAt.Add(10*time.Second),
	)
	require.NoError(t, err)
	require.Zero(t, commandTag.RowsAffected())

	_, err = sharedPool.Exec(ctx, `
		UPDATE arena_game_attempts
		SET updated_at = $2
		WHERE id = $1`, fixture.attemptID, fixture.pausedAt.Add(10*time.Second))
	require.Error(t, err)
	_, err = sharedPool.Exec(ctx, `
		UPDATE arena_resume_decisions
		SET action = 'wait_both'
		WHERE pause_id = $1 AND decision_number = 5`, fixture.gamePauseID)
	require.Error(t, err)
}
