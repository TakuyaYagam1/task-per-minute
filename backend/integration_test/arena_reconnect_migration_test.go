//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
)

type arenaReconnectMigrationFixture struct {
	draft               arenaDraftMigrationFixture
	attemptID           uuid.UUID
	normalPauseID       uuid.UUID
	normalWaveID        uuid.UUID
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

type arenaReconnectContinuationInput struct {
	id                 uuid.UUID
	continuedFromID    *uuid.UUID
	suspendedByPauseID *uuid.UUID
	owningPauseID      *uuid.UUID
	gameAttemptID      *uuid.UUID
	participantID      uuid.UUID
	presenceEpoch      int64
	intervalNumber     int
	continuationNumber int
	state              string
	openedAt           time.Time
	deadlineAt         time.Time
	closedAt           *time.Time
	revision           int64
	createdAt          time.Time
	updatedAt          time.Time
}

type arenaReconnectIntervalSnapshot struct {
	id                 uuid.UUID
	pauseID            uuid.UUID
	rosterID           uuid.UUID
	seriesID           uuid.UUID
	gameAttemptID      uuid.UUID
	participantID      uuid.UUID
	presenceEpoch      int64
	intervalNumber     int
	continuationNumber int
	continuedFromID    pgtype.UUID
	suspendedByPauseID pgtype.UUID
	state              string
	openedAt           time.Time
	deadlineAt         time.Time
	closedAt           pgtype.Timestamptz
	revision           int64
	createdAt          time.Time
	updatedAt          time.Time
}

type arenaReconnectSchemaSnapshot struct {
	columns     string
	constraints string
	indexes     string
	triggers    string
	functions   string
	comments    string
}

func TestArenaReconnectContinuationRootCompatibility(t *testing.T) {
	ctx := context.Background()
	resetArenaMigrationTables(t)
	t.Cleanup(func() { resetArenaMigrationTables(t) })

	fixture := createArenaReconnectMigrationFixture(t, ctx)
	participantID := fixture.draft.participantIDs[0]
	intervalID, _ := disconnectArenaParticipant(
		t,
		ctx,
		fixture,
		participantID,
		fixture.pausedAt.Add(time.Second),
		2*time.Minute,
	)

	var (
		continuationNumber int
		continuedFromID    pgtype.UUID
		suspendedByPauseID pgtype.UUID
		slotsUsed          int
		counterRevision    int64
	)
	err := sharedPool.QueryRow(ctx, `
		SELECT continuation_number, continued_from_id, suspended_by_pause_id
		FROM arena_reconnect_intervals
		WHERE id = $1`, intervalID).Scan(
		&continuationNumber,
		&continuedFromID,
		&suspendedByPauseID,
	)
	require.NoError(t, err)
	require.Zero(t, continuationNumber)
	require.False(t, continuedFromID.Valid)
	require.False(t, suspendedByPauseID.Valid)

	err = sharedPool.QueryRow(ctx, `
		SELECT slots_used, revision
		FROM arena_reconnect_slot_counters
		WHERE pause_id = $1 AND participant_id = $2`,
		fixture.gamePauseID,
		participantID,
	).Scan(&slotsUsed, &counterRevision)
	require.NoError(t, err)
	require.Equal(t, 1, slotsUsed)
	require.EqualValues(t, 2, counterRevision)
}

func TestArenaReconnectContinuationRootPresenceEpoch(t *testing.T) {
	ctx := context.Background()

	t.Run("rejects a second fresh root in the same presence epoch", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture := createArenaReconnectMigrationFixtureWithSlotLimit(t, ctx, 2)
		participantID := fixture.draft.participantIDs[0]
		openedAt := fixture.pausedAt.Add(time.Second)
		firstID, _ := disconnectArenaParticipant(
			t, ctx, fixture, participantID, openedAt, 2*time.Minute,
		)
		cancelArenaReconnectInterval(t, ctx, firstID, openedAt.Add(time.Second))

		tx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()
		err = insertArenaReconnectRootAtEpoch(
			ctx, tx, fixture, participantID, uuid.New(), 2, 2, openedAt.Add(2*time.Second),
		)
		if err == nil {
			err = tx.Commit(ctx)
		}
		requireArenaReconnectPostgresError(
			t,
			err,
			"23505",
			"arena_reconnect_intervals_root_presence_epoch_key",
			"",
		)
	})

	t.Run("allows a fresh root after presence advances to a new epoch", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture := createArenaReconnectMigrationFixtureWithSlotLimit(t, ctx, 2)
		participantID := fixture.draft.participantIDs[0]
		openedAt := fixture.pausedAt.Add(time.Second)
		firstID, _ := disconnectArenaParticipant(
			t, ctx, fixture, participantID, openedAt, 2*time.Minute,
		)
		cancelledAt := openedAt.Add(time.Second)
		cancelArenaReconnectInterval(t, ctx, firstID, cancelledAt)
		connectedAt := cancelledAt.Add(time.Second)
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
			participantID,
			connectedAt,
		)
		require.NoError(t, err)

		secondID, _ := disconnectArenaParticipantAtInterval(
			t,
			ctx,
			fixture,
			participantID,
			2,
			connectedAt.Add(time.Second),
			2*time.Minute,
		)
		second := loadArenaReconnectIntervalSnapshot(t, ctx, secondID)
		require.EqualValues(t, 4, second.presenceEpoch)
		require.Equal(t, 2, second.intervalNumber)
		require.Zero(t, second.continuationNumber)
	})

	t.Run("serializes concurrent roots in one new presence epoch", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture := createArenaReconnectMigrationFixtureWithSlotLimit(t, ctx, 2)
		participantID := fixture.draft.participantIDs[0]
		openedAt := fixture.pausedAt.Add(time.Second)
		disconnectArenaPresence(t, ctx, fixture, participantID, openedAt)

		winnerTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = winnerTx.Rollback(ctx) }()
		require.NoError(t, setArenaReconnectDeadlockTimeouts(ctx, winnerTx))
		require.NoError(t, insertArenaReconnectRootAtEpoch(
			ctx, winnerTx, fixture, participantID, uuid.New(), 2, 1, openedAt,
		))

		loserTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = loserTx.Rollback(ctx) }()
		require.NoError(t, setArenaReconnectDeadlockTimeouts(ctx, loserTx))
		var loserPID int
		require.NoError(t, loserTx.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&loserPID))
		loserResult := make(chan error, 1)
		go func() {
			loserErr := insertArenaReconnectRootAtEpoch(
				ctx, loserTx, fixture, participantID, uuid.New(), 2, 1, openedAt,
			)
			if loserErr == nil {
				loserErr = loserTx.Commit(ctx)
			}
			loserResult <- loserErr
		}()
		waitArenaReconnectBackendLock(t, ctx, loserPID)
		require.NoError(t, winnerTx.Commit(ctx))
		loserErr := <-loserResult
		require.Error(t, loserErr)
		var postgresError *pgconn.PgError
		require.ErrorAs(t, loserErr, &postgresError)
		require.Contains(t, []string{"23505", "23514"}, postgresError.Code)

		var (
			rootCount int
			slotsUsed int
		)
		require.NoError(t, sharedPool.QueryRow(ctx, `
			SELECT COUNT(*)
			FROM arena_reconnect_intervals
			WHERE pause_id = $1
				AND participant_id = $2
				AND presence_epoch = 2
				AND continuation_number = 0`,
			fixture.gamePauseID,
			participantID,
		).Scan(&rootCount))
		require.Equal(t, 1, rootCount)
		require.NoError(t, sharedPool.QueryRow(ctx, `
			SELECT slots_used
			FROM arena_reconnect_slot_counters
			WHERE pause_id = $1 AND participant_id = $2`,
			fixture.gamePauseID,
			participantID,
		).Scan(&slotsUsed))
		require.Equal(t, 1, slotsUsed)
	})
}

func TestArenaReconnectContinuationSegments(t *testing.T) {
	ctx := context.Background()

	t.Run("continues a suspended interval without charging another slot", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture, sourceID, sourceDeadline, suspendedAt :=
			createCancelledArenaReconnectRoot(t, ctx, 1)
		assertArenaNormalPauseIsSeparateFromGameChain(t, ctx, fixture)
		participantID := fixture.draft.participantIDs[0]
		before := loadArenaReconnectIntervalSnapshot(t, ctx, sourceID)
		require.Equal(t, "cancelled", before.state)
		require.Zero(t, before.continuationNumber)
		require.False(t, before.continuedFromID.Valid)
		require.True(t, before.suspendedByPauseID.Valid)
		require.Equal(t, fixture.normalPauseID, uuid.UUID(before.suspendedByPauseID.Bytes))
		require.True(t, before.closedAt.Valid)
		require.True(t, suspendedAt.Equal(before.closedAt.Time))
		continuationID := uuid.New()
		openedAt := suspendedAt.Add(30 * time.Second)
		shiftedDeadline := openedAt.Add(sourceDeadline.Sub(suspendedAt))

		err := insertArenaReconnectContinuation(ctx, fixture, arenaReconnectContinuationInput{
			id:                 continuationID,
			continuedFromID:    &sourceID,
			suspendedByPauseID: nil,
			participantID:      participantID,
			presenceEpoch:      2,
			intervalNumber:     1,
			continuationNumber: 1,
			openedAt:           openedAt,
			deadlineAt:         shiftedDeadline,
		})
		require.NoError(t, err)

		var (
			storedID                 uuid.UUID
			storedPauseID            uuid.UUID
			storedRosterID           uuid.UUID
			storedSeriesID           uuid.UUID
			storedAttemptID          uuid.UUID
			storedParticipantID      uuid.UUID
			storedEpoch              int64
			storedIntervalNumber     int
			storedContinuationNumber int
			storedContinuedFromID    uuid.UUID
			storedSuspendedByPauseID pgtype.UUID
			storedOpenedAt           time.Time
			storedDeadlineAt         time.Time
			slotsUsed                int
			counterRevision          int64
		)
		err = sharedPool.QueryRow(ctx, `
			SELECT
				id, pause_id, roster_id, series_id, game_attempt_id,
				participant_id, presence_epoch, interval_number,
				continuation_number, continued_from_id, suspended_by_pause_id,
				opened_at, deadline_at
			FROM arena_reconnect_intervals
			WHERE id = $1`, continuationID).Scan(
			&storedID,
			&storedPauseID,
			&storedRosterID,
			&storedSeriesID,
			&storedAttemptID,
			&storedParticipantID,
			&storedEpoch,
			&storedIntervalNumber,
			&storedContinuationNumber,
			&storedContinuedFromID,
			&storedSuspendedByPauseID,
			&storedOpenedAt,
			&storedDeadlineAt,
		)
		require.NoError(t, err)
		require.Equal(t, continuationID, storedID)
		require.NotEqual(t, sourceID, storedID)
		require.Equal(t, fixture.gamePauseID, storedPauseID)
		require.Equal(t, fixture.draft.rosterID, storedRosterID)
		require.Equal(t, fixture.draft.seriesID, storedSeriesID)
		require.Equal(t, fixture.attemptID, storedAttemptID)
		require.Equal(t, participantID, storedParticipantID)
		require.EqualValues(t, 2, storedEpoch)
		require.Equal(t, 1, storedIntervalNumber)
		require.Equal(t, 1, storedContinuationNumber)
		require.Equal(t, sourceID, storedContinuedFromID)
		require.False(t, storedSuspendedByPauseID.Valid)
		require.True(t, openedAt.Equal(storedOpenedAt))
		require.True(t, shiftedDeadline.Equal(storedDeadlineAt))
		require.Equal(t, before, loadArenaReconnectIntervalSnapshot(t, ctx, sourceID))

		err = sharedPool.QueryRow(ctx, `
			SELECT slots_used, revision
			FROM arena_reconnect_slot_counters
			WHERE pause_id = $1 AND participant_id = $2`,
			fixture.gamePauseID,
			participantID,
		).Scan(&slotsUsed, &counterRevision)
		require.NoError(t, err)
		require.Equal(t, 1, slotsUsed)
		require.EqualValues(t, 2, counterRevision)

		insertArenaResumeDecision(
			t,
			ctx,
			fixture,
			fixture.gamePauseID,
			1,
			&continuationID,
			nil,
			"wait_first",
			openedAt,
		)
	})

	for _, testCase := range []struct {
		name   string
		mutate func(arenaReconnectMigrationFixture, *arenaReconnectContinuationInput)
	}{
		{
			name: "rejects reconnected continuation insertion",
			mutate: func(_ arenaReconnectMigrationFixture, input *arenaReconnectContinuationInput) {
				closedAt := input.openedAt.Add(time.Second)
				input.state = "reconnected"
				input.closedAt = &closedAt
				input.updatedAt = closedAt
			},
		},
		{
			name: "rejects expired continuation insertion",
			mutate: func(_ arenaReconnectMigrationFixture, input *arenaReconnectContinuationInput) {
				closedAt := input.deadlineAt
				input.state = "expired"
				input.closedAt = &closedAt
				input.updatedAt = closedAt
			},
		},
		{
			name: "rejects cancelled continuation insertion with provenance",
			mutate: func(fixture arenaReconnectMigrationFixture, input *arenaReconnectContinuationInput) {
				closedAt := input.openedAt.Add(time.Second)
				input.state = "cancelled"
				input.closedAt = &closedAt
				input.suspendedByPauseID = &fixture.gamePauseID
				input.updatedAt = closedAt
			},
		},
		{
			name: "rejects open continuation insertion with closed timestamp",
			mutate: func(_ arenaReconnectMigrationFixture, input *arenaReconnectContinuationInput) {
				closedAt := input.openedAt.Add(time.Second)
				input.closedAt = &closedAt
			},
		},
		{
			name: "rejects continuation insertion above revision one",
			mutate: func(_ arenaReconnectMigrationFixture, input *arenaReconnectContinuationInput) {
				input.revision = 2
			},
		},
		{
			name: "rejects continuation insertion with creation timestamp drift",
			mutate: func(_ arenaReconnectMigrationFixture, input *arenaReconnectContinuationInput) {
				input.createdAt = input.openedAt.Add(time.Second)
				input.updatedAt = input.createdAt
			},
		},
		{
			name: "rejects continuation insertion with update timestamp drift",
			mutate: func(_ arenaReconnectMigrationFixture, input *arenaReconnectContinuationInput) {
				input.updatedAt = input.openedAt.Add(time.Second)
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			resetArenaMigrationTables(t)
			t.Cleanup(func() { resetArenaMigrationTables(t) })

			fixture, sourceID, sourceDeadline, suspendedAt :=
				createCancelledArenaReconnectRoot(t, ctx, 1)
			openedAt := suspendedAt.Add(30 * time.Second)
			input := arenaReconnectContinuationInput{
				id:                 uuid.New(),
				continuedFromID:    &sourceID,
				participantID:      fixture.draft.participantIDs[0],
				presenceEpoch:      2,
				intervalNumber:     1,
				continuationNumber: 1,
				openedAt:           openedAt,
				deadlineAt:         openedAt.Add(sourceDeadline.Sub(suspendedAt)),
			}
			testCase.mutate(fixture, &input)

			err := insertArenaReconnectContinuation(ctx, fixture, input)
			requireArenaReconnectExactCheckViolation(
				t,
				err,
				"Arena reconnect continuation must start as a canonical open segment",
			)
		})
	}

	t.Run("rejects a root insertion born with suspension provenance", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture := createArenaReconnectMigrationFixtureWithSlotLimit(t, ctx, 1)
		participantID := fixture.draft.participantIDs[0]
		openedAt := fixture.pausedAt.Add(time.Second)
		disconnectArenaPresence(t, ctx, fixture, participantID, openedAt)
		closedAt := openedAt.Add(time.Second)
		tx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()
		err = insertArenaReconnectContinuationWith(
			ctx,
			tx,
			fixture,
			arenaReconnectContinuationInput{
				id:                 uuid.New(),
				suspendedByPauseID: &fixture.gamePauseID,
				participantID:      participantID,
				presenceEpoch:      2,
				intervalNumber:     1,
				continuationNumber: 0,
				state:              "cancelled",
				openedAt:           openedAt,
				deadlineAt:         openedAt.Add(2 * time.Minute),
				closedAt:           &closedAt,
				updatedAt:          closedAt,
			},
		)
		if err == nil {
			_, err = tx.Exec(ctx, `
				UPDATE arena_reconnect_slot_counters
				SET slots_used = slots_used + 1,
					revision = revision + 1,
					updated_at = $3
				WHERE pause_id = $1 AND participant_id = $2`,
				fixture.gamePauseID,
				participantID,
				openedAt,
			)
		}
		if err == nil {
			err = tx.Commit(ctx)
		}
		requireArenaReconnectExactCheckViolation(
			t,
			err,
			"Arena reconnect interval insert cannot carry suspension provenance",
		)
	})

	t.Run("reconnects a continuation and charges the next fresh root slot", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture, sourceID, sourceDeadline, suspendedAt :=
			createCancelledArenaReconnectRoot(t, ctx, 2)
		participantID := fixture.draft.participantIDs[0]
		continuationID := uuid.New()
		openedAt := suspendedAt.Add(30 * time.Second)
		require.NoError(t, insertArenaReconnectContinuation(
			ctx,
			fixture,
			arenaReconnectContinuationInput{
				id:                 continuationID,
				continuedFromID:    &sourceID,
				suspendedByPauseID: nil,
				participantID:      participantID,
				presenceEpoch:      2,
				intervalNumber:     1,
				continuationNumber: 1,
				openedAt:           openedAt,
				deadlineAt:         openedAt.Add(sourceDeadline.Sub(suspendedAt)),
			},
		))

		resumeArenaNormalWavePause(t, ctx, fixture.normalPauseID, openedAt)
		reconnectedAt := openedAt.Add(10 * time.Second)
		reconnectArenaParticipant(
			t,
			ctx,
			fixture,
			participantID,
			continuationID,
			reconnectedAt,
		)
		terminalContinuation := loadArenaReconnectIntervalSnapshot(t, ctx, continuationID)
		require.Equal(t, "reconnected", terminalContinuation.state)
		require.True(t, terminalContinuation.closedAt.Valid)
		require.True(t, reconnectedAt.Equal(terminalContinuation.closedAt.Time))
		require.Equal(t, 1, terminalContinuation.continuationNumber)
		require.Equal(t, sourceID, uuid.UUID(terminalContinuation.continuedFromID.Bytes))
		freshOpenedAt := reconnectedAt.Add(10 * time.Second)
		freshRootID, _ := disconnectArenaParticipantAtInterval(
			t,
			ctx,
			fixture,
			participantID,
			2,
			freshOpenedAt,
			2*time.Minute,
		)
		freshRoot := loadArenaReconnectIntervalSnapshot(t, ctx, freshRootID)
		require.Equal(t, "open", freshRoot.state)
		require.Equal(t, 2, freshRoot.intervalNumber)
		require.Zero(t, freshRoot.continuationNumber)
		require.False(t, freshRoot.continuedFromID.Valid)
		require.False(t, freshRoot.suspendedByPauseID.Valid)
		require.EqualValues(t, 4, freshRoot.presenceEpoch)

		var (
			slotsUsed       int
			counterRevision int64
		)
		err := sharedPool.QueryRow(ctx, `
			SELECT slots_used, revision
			FROM arena_reconnect_slot_counters
			WHERE pause_id = $1 AND participant_id = $2`,
			fixture.gamePauseID,
			participantID,
		).Scan(&slotsUsed, &counterRevision)
		require.NoError(t, err)
		require.Equal(t, 2, slotsUsed)
		require.EqualValues(t, 3, counterRevision)

		insertArenaResumeDecision(
			t,
			ctx,
			fixture,
			fixture.gamePauseID,
			1,
			&freshRootID,
			nil,
			"wait_first",
			freshOpenedAt,
		)
		var decisionIntervalID uuid.UUID
		err = sharedPool.QueryRow(ctx, `
			SELECT first_reconnect_interval_id
			FROM arena_resume_decisions
			WHERE pause_id = $1 AND decision_number = 1`,
			fixture.gamePauseID,
		).Scan(&decisionIntervalID)
		require.NoError(t, err)
		require.Equal(t, freshRootID, decisionIntervalID)
	})

	t.Run("retains repeatable suspension provenance across two continuations", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture, segment0ID, segment0Deadline, suspendedAtA :=
			createCancelledArenaReconnectRoot(t, ctx, 2)
		participantID := fixture.draft.participantIDs[0]
		segment1ID := uuid.New()
		segment1OpenedAt := suspendedAtA.Add(30 * time.Second)
		segment1Deadline := segment1OpenedAt.Add(segment0Deadline.Sub(suspendedAtA))
		require.NoError(t, insertArenaReconnectContinuation(ctx, fixture, arenaReconnectContinuationInput{
			id:                 segment1ID,
			continuedFromID:    &segment0ID,
			suspendedByPauseID: nil,
			participantID:      participantID,
			presenceEpoch:      2,
			intervalNumber:     1,
			continuationNumber: 1,
			openedAt:           segment1OpenedAt,
			deadlineAt:         segment1Deadline,
		}))
		resumeArenaNormalWavePause(t, ctx, fixture.normalPauseID, segment1OpenedAt)

		suspendedAtB := segment1OpenedAt.Add(20 * time.Second)
		waveBID := createArenaCoveringWave(t, ctx, fixture, segment1OpenedAt)
		pauseBID := createArenaNormalWavePauseAndSuspendInterval(
			t,
			ctx,
			fixture,
			waveBID,
			segment1ID,
			suspendedAtB,
		)
		segment2ID := uuid.New()
		segment2OpenedAt := suspendedAtB.Add(30 * time.Second)
		segment2Deadline := segment2OpenedAt.Add(segment1Deadline.Sub(suspendedAtB))
		require.NoError(t, insertArenaReconnectContinuation(ctx, fixture, arenaReconnectContinuationInput{
			id:                 segment2ID,
			continuedFromID:    &segment1ID,
			suspendedByPauseID: nil,
			participantID:      participantID,
			presenceEpoch:      2,
			intervalNumber:     1,
			continuationNumber: 2,
			openedAt:           segment2OpenedAt,
			deadlineAt:         segment2Deadline,
		}))

		segment0 := loadArenaReconnectIntervalSnapshot(t, ctx, segment0ID)
		segment1 := loadArenaReconnectIntervalSnapshot(t, ctx, segment1ID)
		segment2 := loadArenaReconnectIntervalSnapshot(t, ctx, segment2ID)
		require.Equal(t, "cancelled", segment0.state)
		require.Equal(t, fixture.normalPauseID, uuid.UUID(segment0.suspendedByPauseID.Bytes))
		require.Equal(t, "cancelled", segment1.state)
		require.Equal(t, segment0ID, uuid.UUID(segment1.continuedFromID.Bytes))
		require.Equal(t, pauseBID, uuid.UUID(segment1.suspendedByPauseID.Bytes))
		require.Equal(t, "open", segment2.state)
		require.Equal(t, segment1ID, uuid.UUID(segment2.continuedFromID.Bytes))
		require.False(t, segment2.suspendedByPauseID.Valid)
		require.Equal(t, []int{0, 1, 2}, []int{
			segment0.continuationNumber,
			segment1.continuationNumber,
			segment2.continuationNumber,
		})

		var slotsUsed int
		err := sharedPool.QueryRow(ctx, `
			SELECT slots_used
			FROM arena_reconnect_slot_counters
			WHERE pause_id = $1 AND participant_id = $2`,
			fixture.gamePauseID,
			participantID,
		).Scan(&slotsUsed)
		require.NoError(t, err)
		require.Equal(t, 1, slotsUsed)

		_, err = sharedPool.Exec(ctx, `
			UPDATE arena_reconnect_intervals
			SET suspended_by_pause_id = $2,
				revision = revision + 1,
				updated_at = $3
			WHERE id = $1`, segment0ID, pauseBID, segment2OpenedAt)
		requireArenaReconnectCheckViolation(
			t,
			err,
			"Arena reconnect suspension provenance is immutable",
		)
		require.Equal(t, segment0, loadArenaReconnectIntervalSnapshot(t, ctx, segment0ID))
		require.Equal(t, segment1, loadArenaReconnectIntervalSnapshot(t, ctx, segment1ID))
	})

	for _, testCase := range []struct {
		name     string
		openedAt func(time.Time) time.Time
		deadline func(time.Time, time.Duration) time.Time
	}{
		{
			name:     "rejects shifted deadline one microsecond early",
			openedAt: func(suspendedAt time.Time) time.Time { return suspendedAt.Add(30 * time.Second) },
			deadline: func(openedAt time.Time, remaining time.Duration) time.Time {
				return openedAt.Add(remaining).Add(-time.Microsecond)
			},
		},
		{
			name:     "rejects shifted deadline one microsecond late",
			openedAt: func(suspendedAt time.Time) time.Time { return suspendedAt.Add(30 * time.Second) },
			deadline: func(openedAt time.Time, remaining time.Duration) time.Time {
				return openedAt.Add(remaining).Add(time.Microsecond)
			},
		},
		{
			name:     "rejects continuation opened at suspension boundary",
			openedAt: func(suspendedAt time.Time) time.Time { return suspendedAt },
			deadline: func(openedAt time.Time, remaining time.Duration) time.Time {
				return openedAt.Add(remaining)
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			resetArenaMigrationTables(t)
			t.Cleanup(func() { resetArenaMigrationTables(t) })

			fixture, sourceID, sourceDeadline, suspendedAt :=
				createCancelledArenaReconnectRoot(t, ctx, 1)
			openedAt := testCase.openedAt(suspendedAt)
			err := insertArenaReconnectContinuation(ctx, fixture, arenaReconnectContinuationInput{
				id:                 uuid.New(),
				continuedFromID:    &sourceID,
				suspendedByPauseID: nil,
				participantID:      fixture.draft.participantIDs[0],
				presenceEpoch:      2,
				intervalNumber:     1,
				continuationNumber: 1,
				openedAt:           openedAt,
				deadlineAt:         testCase.deadline(openedAt, sourceDeadline.Sub(suspendedAt)),
			})
			requireArenaReconnectCheckViolation(
				t,
				err,
				"Arena reconnect continuation does not match suspended predecessor",
			)
		})
	}

	t.Run("rejects missing predecessor", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture, _, sourceDeadline, suspendedAt := createCancelledArenaReconnectRoot(t, ctx, 1)
		openedAt := suspendedAt.Add(30 * time.Second)
		missingID := uuid.New()
		err := insertArenaReconnectContinuation(ctx, fixture, arenaReconnectContinuationInput{
			id:                 uuid.New(),
			continuedFromID:    &missingID,
			suspendedByPauseID: nil,
			participantID:      fixture.draft.participantIDs[0],
			presenceEpoch:      2,
			intervalNumber:     1,
			continuationNumber: 1,
			openedAt:           openedAt,
			deadlineAt:         openedAt.Add(sourceDeadline.Sub(suspendedAt)),
		})
		requireArenaReconnectPostgresError(
			t,
			err,
			"23503",
			"arena_reconnect_intervals_continued_from_fk",
			"",
		)
	})

	for _, testCase := range []struct {
		name   string
		mutate func(*arenaReconnectContinuationInput)
	}{
		{
			name: "rejects continuation number without predecessor",
			mutate: func(input *arenaReconnectContinuationInput) {
				input.continuedFromID = nil
			},
		},
		{
			name: "rejects predecessor on a root segment",
			mutate: func(input *arenaReconnectContinuationInput) {
				input.continuationNumber = 0
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			resetArenaMigrationTables(t)
			t.Cleanup(func() { resetArenaMigrationTables(t) })

			fixture, sourceID, sourceDeadline, suspendedAt :=
				createCancelledArenaReconnectRoot(t, ctx, 1)
			openedAt := suspendedAt.Add(30 * time.Second)
			input := arenaReconnectContinuationInput{
				id:                 uuid.New(),
				continuedFromID:    &sourceID,
				suspendedByPauseID: nil,
				participantID:      fixture.draft.participantIDs[0],
				presenceEpoch:      2,
				intervalNumber:     1,
				continuationNumber: 1,
				openedAt:           openedAt,
				deadlineAt:         openedAt.Add(sourceDeadline.Sub(suspendedAt)),
			}
			testCase.mutate(&input)

			err := insertArenaReconnectContinuation(ctx, fixture, input)
			requireArenaReconnectPostgresError(
				t,
				err,
				"23514",
				"arena_reconnect_intervals_lineage_check",
				"",
			)
		})
	}

	for _, testCase := range []struct {
		name   string
		mutate func(testing.TB, arenaReconnectMigrationFixture, *arenaReconnectContinuationInput)
	}{
		{
			name: "rejects cross-participant predecessor",
			mutate: func(
				t testing.TB,
				fixture arenaReconnectMigrationFixture,
				input *arenaReconnectContinuationInput,
			) {
				participantID := fixture.draft.participantIDs[1]
				disconnectArenaPresence(t, ctx, fixture, participantID, input.openedAt)
				input.participantID = participantID
			},
		},
		{
			name: "rejects wrong logical interval number",
			mutate: func(
				_ testing.TB,
				_ arenaReconnectMigrationFixture,
				input *arenaReconnectContinuationInput,
			) {
				input.intervalNumber = 2
			},
		},
		{
			name: "rejects wrong owning Game pause",
			mutate: func(
				_ testing.TB,
				fixture arenaReconnectMigrationFixture,
				input *arenaReconnectContinuationInput,
			) {
				input.owningPauseID = &fixture.rootPauseID
			},
		},
		{
			name: "rejects wrong Game attempt for owning pause",
			mutate: func(
				t testing.TB,
				fixture arenaReconnectMigrationFixture,
				input *arenaReconnectContinuationInput,
			) {
				slotID := createArenaMigrationGameSlot(
					t,
					ctx,
					fixture.draft.seriesID,
					fixture.draft.rosterID,
					2,
					"crypto",
				)
				attemptID := createActiveArenaMigrationAttempt(
					t,
					ctx,
					slotID,
					fixture.draft.seriesID,
					fixture.draft.rosterID,
					input.openedAt.Add(-time.Second),
				)
				input.gameAttemptID = &attemptID
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			resetArenaMigrationTables(t)
			t.Cleanup(func() { resetArenaMigrationTables(t) })

			fixture, sourceID, sourceDeadline, suspendedAt :=
				createCancelledArenaReconnectRoot(t, ctx, 1)
			openedAt := suspendedAt.Add(30 * time.Second)
			input := arenaReconnectContinuationInput{
				id:                 uuid.New(),
				continuedFromID:    &sourceID,
				suspendedByPauseID: nil,
				participantID:      fixture.draft.participantIDs[0],
				presenceEpoch:      2,
				intervalNumber:     1,
				continuationNumber: 1,
				openedAt:           openedAt,
				deadlineAt:         openedAt.Add(sourceDeadline.Sub(suspendedAt)),
			}
			testCase.mutate(t, fixture, &input)

			err := insertArenaReconnectContinuation(ctx, fixture, input)
			requireArenaReconnectExactCheckViolation(
				t,
				err,
				"Arena reconnect continuation does not match predecessor identity",
			)
		})
	}

	t.Run("defines predecessor no-fork as a unique partial index", func(t *testing.T) {
		var (
			isUnique   bool
			indexDef   string
			predicate  string
			keyColumns string
		)
		err := sharedPool.QueryRow(ctx, `
			SELECT
				index_row.indisunique,
				pg_get_indexdef(index_row.indexrelid),
				pg_get_expr(index_row.indpred, index_row.indrelid),
				STRING_AGG(attribute.attname, ',' ORDER BY key_column.ordinality)
			FROM pg_index AS index_row
			JOIN pg_class AS index_relation
				ON index_relation.oid = index_row.indexrelid
			JOIN pg_class AS table_relation
				ON table_relation.oid = index_row.indrelid
			JOIN pg_namespace AS namespace
				ON namespace.oid = table_relation.relnamespace
			JOIN LATERAL UNNEST(index_row.indkey)
				WITH ORDINALITY AS key_column(attnum, ordinality) ON TRUE
			JOIN pg_attribute AS attribute
				ON attribute.attrelid = table_relation.oid
				AND attribute.attnum = key_column.attnum
			WHERE namespace.nspname = 'public'
				AND table_relation.relname = 'arena_reconnect_intervals'
				AND index_relation.relname = 'arena_reconnect_intervals_continued_from_key'
			GROUP BY index_row.indisunique, index_row.indexrelid,
				index_row.indpred, index_row.indrelid`).Scan(
			&isUnique,
			&indexDef,
			&predicate,
			&keyColumns,
		)
		require.NoError(t, err)
		require.True(t, isUnique)
		require.Equal(t, "continued_from_id", keyColumns)
		require.Equal(t, "(continued_from_id IS NOT NULL)", predicate)
		require.Contains(t, indexDef, "UNIQUE INDEX")
	})

	t.Run("rejects source cancellation by a non-covering normal Wave pause", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture := createArenaReconnectMigrationFixtureWithSlotLimit(t, ctx, 1)
		participantID := fixture.draft.participantIDs[0]
		sourceID, _ := disconnectArenaParticipant(
			t,
			ctx,
			fixture,
			participantID,
			fixture.pausedAt.Add(time.Second),
			2*time.Minute,
		)
		suspendedAt := fixture.pausedAt.Add(20 * time.Second)
		otherParticipantID := fixture.draft.participantIDs[1]
		waveID := createArenaWaveWithMembers(
			t,
			ctx,
			fixture,
			[]uuid.UUID{otherParticipantID},
			fixture.pausedAt,
		)
		err := attemptArenaNormalWavePauseAndSuspendInterval(
			t,
			ctx,
			fixture,
			waveID,
			[]uuid.UUID{otherParticipantID},
			sourceID,
			suspendedAt,
		)
		requireArenaReconnectCheckViolation(
			t,
			err,
			"Arena reconnect cancellation requires a covering active normal Wave pause",
		)
	})

	t.Run("rejects suspension provenance on a newly open continuation", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture, sourceID, sourceDeadline, suspendedAt :=
			createCancelledArenaReconnectRoot(t, ctx, 1)
		openedAt := suspendedAt.Add(30 * time.Second)
		err := insertArenaReconnectContinuation(ctx, fixture, arenaReconnectContinuationInput{
			id:                 uuid.New(),
			continuedFromID:    &sourceID,
			suspendedByPauseID: &fixture.gamePauseID,
			participantID:      fixture.draft.participantIDs[0],
			presenceEpoch:      2,
			intervalNumber:     1,
			continuationNumber: 1,
			openedAt:           openedAt,
			deadlineAt:         openedAt.Add(sourceDeadline.Sub(suspendedAt)),
		})
		requireArenaReconnectCheckViolation(
			t,
			err,
			"Arena open reconnect continuation cannot carry suspension provenance",
		)
	})

	t.Run("rejects wrong presence epoch", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture, sourceID, sourceDeadline, suspendedAt :=
			createCancelledArenaReconnectRoot(t, ctx, 1)
		openedAt := suspendedAt.Add(30 * time.Second)
		err := insertArenaReconnectContinuation(ctx, fixture, arenaReconnectContinuationInput{
			id:                 uuid.New(),
			continuedFromID:    &sourceID,
			suspendedByPauseID: nil,
			participantID:      fixture.draft.participantIDs[0],
			presenceEpoch:      3,
			intervalNumber:     1,
			continuationNumber: 1,
			openedAt:           openedAt,
			deadlineAt:         openedAt.Add(sourceDeadline.Sub(suspendedAt)),
		})
		requireArenaReconnectCheckViolation(
			t,
			err,
			"Arena reconnect continuation does not match predecessor identity",
		)
	})

	t.Run("rejects reconnected predecessor", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture := createArenaReconnectMigrationFixtureWithSlotLimit(t, ctx, 1)
		participantID := fixture.draft.participantIDs[0]
		sourceID, sourceDeadline := disconnectArenaParticipant(
			t,
			ctx,
			fixture,
			participantID,
			fixture.pausedAt.Add(time.Second),
			2*time.Minute,
		)
		reconnectedAt := fixture.pausedAt.Add(20 * time.Second)
		reconnectArenaParticipant(t, ctx, fixture, participantID, sourceID, reconnectedAt)
		openedAt := fixture.pausedAt.Add(30 * time.Second)
		err := insertArenaReconnectContinuation(ctx, fixture, arenaReconnectContinuationInput{
			id:                 uuid.New(),
			continuedFromID:    &sourceID,
			suspendedByPauseID: nil,
			participantID:      participantID,
			presenceEpoch:      2,
			intervalNumber:     1,
			continuationNumber: 1,
			openedAt:           openedAt,
			deadlineAt:         openedAt.Add(sourceDeadline.Sub(reconnectedAt)),
		})
		requireArenaReconnectCheckViolation(
			t,
			err,
			"Arena reconnect continuation requires a cancelled predecessor with suspension provenance",
		)
	})

	t.Run("rejects expired predecessor", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture := createArenaReconnectMigrationFixtureWithSlotLimit(t, ctx, 1)
		participantID := fixture.draft.participantIDs[0]
		sourceID, sourceDeadline := disconnectArenaParticipant(
			t,
			ctx,
			fixture,
			participantID,
			fixture.pausedAt.Add(time.Second),
			2*time.Minute,
		)
		_, err := sharedPool.Exec(ctx, `
			UPDATE arena_reconnect_intervals
			SET state = 'expired',
				closed_at = $2,
				revision = revision + 1,
				updated_at = $2
			WHERE id = $1`, sourceID, sourceDeadline)
		require.NoError(t, err)
		openedAt := sourceDeadline.Add(30 * time.Second)
		err = insertArenaReconnectContinuation(ctx, fixture, arenaReconnectContinuationInput{
			id:                 uuid.New(),
			continuedFromID:    &sourceID,
			suspendedByPauseID: nil,
			participantID:      participantID,
			presenceEpoch:      2,
			intervalNumber:     1,
			continuationNumber: 1,
			openedAt:           openedAt,
			deadlineAt:         openedAt.Add(time.Minute),
		})
		requireArenaReconnectCheckViolation(
			t,
			err,
			"Arena reconnect continuation requires a cancelled predecessor with suspension provenance",
		)
	})

	t.Run("retains one open interval per participant", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture, sourceID, sourceDeadline, suspendedAt :=
			createCancelledArenaReconnectRoot(t, ctx, 2)
		participantID := fixture.draft.participantIDs[0]
		openedAt := suspendedAt.Add(30 * time.Second)
		require.NoError(t, insertArenaReconnectContinuation(
			ctx,
			fixture,
			arenaReconnectContinuationInput{
				id:                 uuid.New(),
				continuedFromID:    &sourceID,
				suspendedByPauseID: nil,
				participantID:      participantID,
				presenceEpoch:      2,
				intervalNumber:     1,
				continuationNumber: 1,
				openedAt:           openedAt,
				deadlineAt:         openedAt.Add(sourceDeadline.Sub(suspendedAt)),
			},
		))

		tx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()
		_, err = tx.Exec(ctx, `
			INSERT INTO arena_reconnect_intervals (
				id, pause_id, roster_id, series_id, game_attempt_id,
				participant_id, presence_epoch, interval_number,
				opened_at, deadline_at, created_at, updated_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, 2, 2, $7, $8, $7, $7)`,
			uuid.New(),
			fixture.gamePauseID,
			fixture.draft.rosterID,
			fixture.draft.seriesID,
			fixture.attemptID,
			participantID,
			openedAt.Add(time.Second),
			openedAt.Add(2*time.Minute),
		)
		require.ErrorContains(t, err, "arena_reconnect_intervals_one_open_idx")
	})

	t.Run("rejects root count without counter advance", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture := createArenaReconnectMigrationFixtureWithSlotLimit(t, ctx, 2)
		participantID := fixture.draft.participantIDs[0]
		disconnectedAt := fixture.pausedAt.Add(time.Second)
		disconnectArenaPresence(t, ctx, fixture, participantID, disconnectedAt)

		tx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `
			INSERT INTO arena_reconnect_intervals (
				id, pause_id, roster_id, series_id, game_attempt_id,
				participant_id, presence_epoch, interval_number,
				opened_at, deadline_at, created_at, updated_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, 2, 1, $7, $8, $7, $7)`,
			uuid.New(),
			fixture.gamePauseID,
			fixture.draft.rosterID,
			fixture.draft.seriesID,
			fixture.attemptID,
			participantID,
			disconnectedAt,
			disconnectedAt.Add(2*time.Minute),
		)
		require.NoError(t, err)
		err = tx.Commit(ctx)
		requireArenaReconnectExactCheckViolation(
			t,
			err,
			"Arena reconnect slot count differs from retained root intervals",
		)
	})

	t.Run("rejects a gap in root interval numbers", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture := createArenaReconnectMigrationFixtureWithSlotLimit(t, ctx, 2)
		participantID := fixture.draft.participantIDs[0]
		disconnectedAt := fixture.pausedAt.Add(time.Second)
		disconnectArenaPresence(t, ctx, fixture, participantID, disconnectedAt)

		tx, err := sharedPool.Begin(ctx)
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
		_, err = tx.Exec(ctx, `
			INSERT INTO arena_reconnect_intervals (
				id, pause_id, roster_id, series_id, game_attempt_id,
				participant_id, presence_epoch, interval_number,
				opened_at, deadline_at, created_at, updated_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, 2, 2, $7, $8, $7, $7)`,
			uuid.New(),
			fixture.gamePauseID,
			fixture.draft.rosterID,
			fixture.draft.seriesID,
			fixture.attemptID,
			participantID,
			disconnectedAt,
			disconnectedAt.Add(2*time.Minute),
		)
		require.NoError(t, err)
		err = tx.Commit(ctx)
		requireArenaReconnectExactCheckViolation(
			t,
			err,
			"Arena reconnect root interval numbers must be contiguous",
		)
	})
}

func TestArenaReconnectContinuationPauseAtomicity(t *testing.T) {
	ctx := context.Background()

	t.Run("rejects a normal Wave pause committed over an open covered source", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture := createArenaReconnectMigrationFixtureWithSlotLimit(t, ctx, 1)
		disconnectArenaParticipant(
			t,
			ctx,
			fixture,
			fixture.draft.participantIDs[0],
			fixture.pausedAt.Add(time.Second),
			2*time.Minute,
		)
		waveID := createArenaCoveringWave(t, ctx, fixture, fixture.pausedAt)
		suspendedAt := fixture.pausedAt.Add(20 * time.Second)
		tx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()
		require.NoError(t, insertArenaNormalWavePauseEvidence(
			ctx,
			tx,
			fixture,
			waveID,
			uuid.New(),
			uuid.New(),
			suspendedAt,
		))

		err = tx.Commit(ctx)
		requireArenaReconnectExactCheckViolation(
			t,
			err,
			"Arena normal Wave pause requires atomic reconnect suspension",
		)
	})

	t.Run("commits a normal Wave pause with exact source cancellation", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture := createArenaReconnectMigrationFixtureWithSlotLimit(t, ctx, 1)
		participantID := fixture.draft.participantIDs[0]
		sourceID, _ := disconnectArenaParticipant(
			t,
			ctx,
			fixture,
			participantID,
			fixture.pausedAt.Add(time.Second),
			2*time.Minute,
		)
		waveID := createArenaCoveringWave(t, ctx, fixture, fixture.pausedAt)
		suspendedAt := fixture.pausedAt.Add(20 * time.Second)
		pauseID := uuid.New()
		tx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()
		require.NoError(t, insertArenaNormalWavePauseEvidence(
			ctx,
			tx,
			fixture,
			waveID,
			pauseID,
			uuid.New(),
			suspendedAt,
		))
		_, err = tx.Exec(ctx, `
			UPDATE arena_reconnect_intervals
			SET state = 'cancelled',
				closed_at = $2,
				suspended_by_pause_id = $3,
				revision = revision + 1,
				updated_at = $2
			WHERE id = $1`, sourceID, suspendedAt, pauseID)
		require.NoError(t, err)
		require.NoError(t, tx.Commit(ctx))

		source := loadArenaReconnectIntervalSnapshot(t, ctx, sourceID)
		require.Equal(t, "cancelled", source.state)
		require.Equal(t, pauseID, uuid.UUID(source.suspendedByPauseID.Bytes))
		require.True(t, source.closedAt.Valid)
		require.True(t, suspendedAt.Equal(source.closedAt.Time))
	})

	t.Run("commits source insertion Wave pause and exact suspension in one transaction", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture := createArenaReconnectMigrationFixtureWithSlotLimit(t, ctx, 1)
		participantID := fixture.draft.participantIDs[0]
		openedAt := fixture.pausedAt.Add(time.Second)
		suspendedAt := fixture.pausedAt.Add(20 * time.Second)
		waveID := createArenaCoveringWave(t, ctx, fixture, fixture.pausedAt)
		sourceID := uuid.New()
		pauseID := uuid.New()
		tx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()
		require.NoError(t, insertArenaReconnectRootEvidence(
			ctx,
			tx,
			fixture,
			participantID,
			sourceID,
			openedAt,
			openedAt.Add(2*time.Minute),
		))
		require.NoError(t, insertArenaNormalWavePauseEvidence(
			ctx,
			tx,
			fixture,
			waveID,
			pauseID,
			uuid.New(),
			suspendedAt,
		))
		_, err = tx.Exec(ctx, `
			UPDATE arena_reconnect_intervals
			SET state = 'cancelled',
				closed_at = $2,
				suspended_by_pause_id = $3,
				revision = revision + 1,
				updated_at = $2
			WHERE id = $1`, sourceID, suspendedAt, pauseID)
		require.NoError(t, err)
		require.NoError(t, tx.Commit(ctx))

		source := loadArenaReconnectIntervalSnapshot(t, ctx, sourceID)
		require.Equal(t, "cancelled", source.state)
		require.Equal(t, pauseID, uuid.UUID(source.suspendedByPauseID.Bytes))
		require.True(t, source.closedAt.Valid)
		require.True(t, suspendedAt.Equal(source.closedAt.Time))
	})

	t.Run("allows a normal Wave pause exactly at the source deadline", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture := createArenaReconnectMigrationFixtureWithSlotLimit(t, ctx, 1)
		participantID := fixture.draft.participantIDs[0]
		suspendedAt := fixture.pausedAt.Add(20 * time.Second)
		sourceID, sourceDeadline := disconnectArenaParticipant(
			t,
			ctx,
			fixture,
			participantID,
			fixture.pausedAt.Add(time.Second),
			19*time.Second,
		)
		require.True(t, suspendedAt.Equal(sourceDeadline))
		waveID := createArenaCoveringWave(t, ctx, fixture, fixture.pausedAt)
		tx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()
		require.NoError(t, insertArenaNormalWavePauseEvidence(
			ctx,
			tx,
			fixture,
			waveID,
			uuid.New(),
			uuid.New(),
			suspendedAt,
		))
		require.NoError(t, tx.Commit(ctx))

		source := loadArenaReconnectIntervalSnapshot(t, ctx, sourceID)
		require.Equal(t, "open", source.state)
		require.False(t, source.closedAt.Valid)
		require.False(t, source.suspendedByPauseID.Valid)
	})

	t.Run("allows a retained root that closed before the normal Wave pause", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture := createArenaReconnectMigrationFixtureWithSlotLimit(t, ctx, 1)
		participantID := fixture.draft.participantIDs[0]
		waveID := createArenaCoveringWave(t, ctx, fixture, fixture.pausedAt)
		suspendedAt := fixture.pausedAt.Add(20 * time.Second)
		pauseTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		require.NoError(t, insertArenaNormalWavePauseEvidence(
			ctx,
			pauseTx,
			fixture,
			waveID,
			uuid.New(),
			uuid.New(),
			suspendedAt,
		))
		require.NoError(t, pauseTx.Commit(ctx))

		openedAt := suspendedAt.Add(-15 * time.Second)
		closedAt := suspendedAt.Add(-5 * time.Second)
		disconnectArenaPresence(t, ctx, fixture, participantID, openedAt)
		rootID := uuid.New()
		rootTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = rootTx.Rollback(ctx) }()
		err = insertArenaReconnectContinuationWith(
			ctx,
			rootTx,
			fixture,
			arenaReconnectContinuationInput{
				id:                 rootID,
				participantID:      participantID,
				presenceEpoch:      2,
				intervalNumber:     1,
				continuationNumber: 0,
				state:              "cancelled",
				openedAt:           openedAt,
				deadlineAt:         suspendedAt.Add(time.Minute),
				closedAt:           &closedAt,
				updatedAt:          closedAt,
			},
		)
		if err == nil {
			_, err = rootTx.Exec(ctx, `
				UPDATE arena_reconnect_slot_counters
				SET slots_used = slots_used + 1,
					revision = revision + 1,
					updated_at = $3
				WHERE pause_id = $1 AND participant_id = $2`,
				fixture.gamePauseID,
				participantID,
				closedAt,
			)
		}
		require.NoError(t, err)
		require.NoError(t, rootTx.Commit(ctx))

		root := loadArenaReconnectIntervalSnapshot(t, ctx, rootID)
		require.Equal(t, "cancelled", root.state)
		require.True(t, root.closedAt.Valid)
		require.True(t, closedAt.Equal(root.closedAt.Time))
		require.False(t, root.suspendedByPauseID.Valid)
	})

	t.Run("rejects a covered root backdated across an active normal Wave pause", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture := createArenaReconnectMigrationFixtureWithSlotLimit(t, ctx, 1)
		participantID := fixture.draft.participantIDs[0]
		waveID := createArenaCoveringWave(t, ctx, fixture, fixture.pausedAt)
		suspendedAt := fixture.pausedAt.Add(20 * time.Second)
		pauseTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		require.NoError(t, insertArenaNormalWavePauseEvidence(
			ctx,
			pauseTx,
			fixture,
			waveID,
			uuid.New(),
			uuid.New(),
			suspendedAt,
		))
		require.NoError(t, pauseTx.Commit(ctx))

		openedAt := suspendedAt.Add(-10 * time.Second)
		intervalTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = intervalTx.Rollback(ctx) }()
		_, err = intervalTx.Exec(ctx, `
			SELECT 1 FROM arena_pauses WHERE id = $1 FOR UPDATE`, fixture.gamePauseID)
		require.NoError(t, err)
		_, err = intervalTx.Exec(ctx, `
			UPDATE arena_presence_states
			SET state = 'disconnected',
				presence_epoch = presence_epoch + 1,
				revision = revision + 1,
				disconnected_at = $3,
				updated_at = $3
			WHERE series_id = $1 AND participant_id = $2`,
			fixture.draft.seriesID,
			participantID,
			openedAt,
		)
		require.NoError(t, err)
		_, err = intervalTx.Exec(ctx, `
			INSERT INTO arena_reconnect_intervals (
				id, pause_id, roster_id, series_id, game_attempt_id,
				participant_id, presence_epoch, interval_number,
				opened_at, deadline_at, created_at, updated_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, 2, 1, $7, $8, $7, $7)`,
			uuid.New(),
			fixture.gamePauseID,
			fixture.draft.rosterID,
			fixture.draft.seriesID,
			fixture.attemptID,
			participantID,
			openedAt,
			suspendedAt.Add(time.Minute),
		)
		if err == nil {
			_, err = intervalTx.Exec(ctx, `
				UPDATE arena_reconnect_slot_counters
				SET slots_used = slots_used + 1,
					revision = revision + 1,
					updated_at = $3
				WHERE pause_id = $1 AND participant_id = $2`,
				fixture.gamePauseID,
				participantID,
				openedAt,
			)
		}
		if err == nil {
			err = intervalTx.Commit(ctx)
		}
		requireArenaReconnectExactCheckViolation(
			t,
			err,
			"Arena reconnect interval cannot backdate across normal Wave pause history",
		)
	})

	t.Run("rejects a covered root backdated across a resolved normal Wave pause", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture := createArenaReconnectMigrationFixtureWithSlotLimit(t, ctx, 1)
		waveID := createArenaCoveringWave(t, ctx, fixture, fixture.pausedAt)
		suspendedAt := fixture.pausedAt.Add(20 * time.Second)
		pauseID := uuid.New()
		pauseTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		require.NoError(t, insertArenaNormalWavePauseEvidence(
			ctx,
			pauseTx,
			fixture,
			waveID,
			pauseID,
			uuid.New(),
			suspendedAt,
		))
		require.NoError(t, pauseTx.Commit(ctx))
		resumeArenaNormalWavePause(t, ctx, pauseID, suspendedAt.Add(time.Second))

		err = attemptArenaReconnectRoot(
			ctx,
			fixture,
			fixture.draft.participantIDs[0],
			suspendedAt.Add(-10*time.Second),
			suspendedAt.Add(time.Minute),
			nil,
		)
		requireArenaReconnectExactCheckViolation(
			t,
			err,
			"Arena reconnect interval cannot backdate across normal Wave pause history",
		)
	})

	t.Run("serializes a normal Wave pause against a concurrent backdated root", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture := createArenaReconnectMigrationFixtureWithSlotLimit(t, ctx, 1)
		waveID := createArenaCoveringWave(t, ctx, fixture, fixture.pausedAt)
		suspendedAt := fixture.pausedAt.Add(20 * time.Second)
		pauseTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = pauseTx.Rollback(ctx) }()
		require.NoError(t, insertArenaNormalWavePauseEvidence(
			ctx,
			pauseTx,
			fixture,
			waveID,
			uuid.New(),
			uuid.New(),
			suspendedAt,
		))

		started := make(chan int)
		intervalResult := make(chan error, 1)
		go func() {
			intervalResult <- attemptArenaReconnectRoot(
				ctx,
				fixture,
				fixture.draft.participantIDs[0],
				suspendedAt.Add(-10*time.Second),
				suspendedAt.Add(time.Minute),
				started,
			)
		}()

		rootPID := <-started
		waitArenaReconnectBackendLock(t, ctx, rootPID)
		require.NoError(t, pauseTx.Commit(ctx))
		requireArenaReconnectExactCheckViolation(
			t,
			<-intervalResult,
			"Arena reconnect interval cannot backdate across normal Wave pause history",
		)
	})

	t.Run("preserves a Game-first root writer during normal Wave entry", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture := createArenaReconnectMigrationFixtureWithSlotLimit(t, ctx, 1)
		participantID := fixture.draft.participantIDs[0]
		waveID := createArenaCoveringWave(t, ctx, fixture, fixture.pausedAt)
		suspendedAt := fixture.pausedAt.Add(20 * time.Second)

		rootTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = rootTx.Rollback(ctx) }()
		require.NoError(t, setArenaReconnectDeadlockTimeouts(ctx, rootTx))
		_, err = rootTx.Exec(ctx, `
			SELECT 1 FROM arena_pauses WHERE id = $1 FOR UPDATE`, fixture.gamePauseID)
		require.NoError(t, err)

		pauseTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = pauseTx.Rollback(ctx) }()
		require.NoError(t, setArenaReconnectDeadlockTimeouts(ctx, pauseTx))
		var pausePID int
		require.NoError(t, pauseTx.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pausePID))
		pauseResult := make(chan error, 1)
		go func() {
			pauseResult <- insertArenaNormalWavePauseEvidence(
				ctx,
				pauseTx,
				fixture,
				waveID,
				uuid.New(),
				uuid.New(),
				suspendedAt,
			)
		}()
		waitArenaReconnectBackendLock(t, ctx, pausePID)

		rootID := uuid.New()
		openedAt := suspendedAt.Add(-10 * time.Second)
		require.NoError(t, insertArenaReconnectRootEvidence(
			ctx,
			rootTx,
			fixture,
			participantID,
			rootID,
			openedAt,
			suspendedAt.Add(time.Minute),
		))
		require.NoError(t, rootTx.Commit(ctx))
		require.NoError(t, <-pauseResult)
		requireArenaReconnectExactCheckViolation(
			t,
			pauseTx.Commit(ctx),
			"Arena normal Wave pause requires atomic reconnect suspension",
		)

		root := loadArenaReconnectIntervalSnapshot(t, ctx, rootID)
		require.Equal(t, "open", root.state)
	})

	t.Run("holds the roster fence against concurrent pause topology insertion", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture := createArenaReconnectMigrationFixtureWithSlotLimit(t, ctx, 1)
		waveID := createArenaCoveringWave(t, ctx, fixture, fixture.pausedAt)
		suspendedAt := fixture.pausedAt.Add(20 * time.Second)
		pauseTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = pauseTx.Rollback(ctx) }()
		require.NoError(t, insertArenaNormalWavePauseEvidence(
			ctx,
			pauseTx,
			fixture,
			waveID,
			uuid.New(),
			uuid.New(),
			suspendedAt,
		))

		topologyTx := beginArenaReconnectLockProbe(t, ctx)
		defer func() { _ = topologyTx.Rollback(ctx) }()
		_, err = topologyTx.Exec(ctx, `
			INSERT INTO arena_pauses (
				id, tournament_id, roster_id, scope_kind, scope_id,
				depth, reason, paused_from_state, current_revision_id,
				started_at, created_at, updated_at
			)
			VALUES (
				$1, $2, $3, 'tournament', $2,
				0, 'operator', 'active', $4,
				$5, $5, $5
			)`,
			uuid.New(),
			fixture.draft.tournamentID,
			fixture.draft.rosterID,
			uuid.New(),
			suspendedAt,
		)
		requireArenaReconnectPostgresError(t, err, "55P03", "", "lock timeout")
	})
}

func TestArenaReconnectContinuationWaveMembershipFence(t *testing.T) {
	ctx := context.Background()

	t.Run("concurrent Wave creation does not deadlock on the roster", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture := createArenaReconnectMigrationFixtureWithSlotLimit(t, ctx, 1)
		createdAt := fixture.pausedAt.Add(time.Second)
		firstWaveID := uuid.New()
		secondWaveID := uuid.New()
		firstTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = firstTx.Rollback(ctx) }()
		require.NoError(t, setArenaReconnectDeadlockTimeouts(ctx, firstTx))
		secondTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = secondTx.Rollback(ctx) }()
		require.NoError(t, setArenaReconnectDeadlockTimeouts(ctx, secondTx))

		for _, wave := range []struct {
			tx     pgx.Tx
			waveID uuid.UUID
		}{
			{tx: firstTx, waveID: firstWaveID},
			{tx: secondTx, waveID: secondWaveID},
		} {
			_, err = wave.tx.Exec(ctx, `
				INSERT INTO arena_waves (
					id, tournament_id, roster_id, revision_id,
					revision, state, created_at, updated_at
				)
				VALUES ($1, $2, $3, $4, 1, 'planned', $5, $5)`,
				wave.waveID,
				fixture.draft.tournamentID,
				fixture.draft.rosterID,
				uuid.New(),
				createdAt,
			)
			require.NoError(t, err)
		}

		start := make(chan struct{})
		memberResult := make(chan error, 2)
		insertMember := func(tx pgx.Tx, waveID, participantID uuid.UUID) {
			<-start
			_, memberErr := tx.Exec(ctx, `
				INSERT INTO arena_wave_members (
					wave_id, roster_id, participant_id, created_at
				)
				VALUES ($1, $2, $3, $4)`,
				waveID,
				fixture.draft.rosterID,
				participantID,
				createdAt,
			)
			memberResult <- memberErr
		}
		go insertMember(firstTx, firstWaveID, fixture.draft.participantIDs[0])
		go insertMember(secondTx, secondWaveID, fixture.draft.participantIDs[1])
		close(start)
		firstMemberErr := <-memberResult
		secondMemberErr := <-memberResult
		require.NoError(t, firstMemberErr)
		require.NoError(t, secondMemberErr)
		require.NoError(t, firstTx.Commit(ctx))
		require.NoError(t, secondTx.Commit(ctx))
	})

	t.Run("normal Wave pause rejects a later covered member", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture := createArenaReconnectMigrationFixtureWithSlotLimit(t, ctx, 1)
		firstParticipantID := fixture.draft.participantIDs[0]
		lateParticipantID := fixture.draft.participantIDs[1]
		disconnectArenaParticipant(
			t, ctx, fixture, lateParticipantID,
			fixture.pausedAt.Add(time.Second), 2*time.Minute,
		)
		waveID := createArenaWaveWithMembers(
			t, ctx, fixture, []uuid.UUID{firstParticipantID}, fixture.pausedAt,
		)
		suspendedAt := fixture.pausedAt.Add(20 * time.Second)
		pauseTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = pauseTx.Rollback(ctx) }()
		require.NoError(t, setArenaReconnectDeadlockTimeouts(ctx, pauseTx))
		require.NoError(t, insertArenaNormalWavePauseEvidenceForParticipants(
			ctx, pauseTx, fixture, waveID, uuid.New(), uuid.New(), suspendedAt,
			[]uuid.UUID{firstParticipantID},
		))
		_, err = pauseTx.Exec(ctx, "SET CONSTRAINTS ALL IMMEDIATE")
		require.NoError(t, err)

		memberTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = memberTx.Rollback(ctx) }()
		require.NoError(t, setArenaReconnectDeadlockTimeouts(ctx, memberTx))
		var memberPID int
		require.NoError(t, memberTx.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&memberPID))
		memberResult := make(chan error, 1)
		go func() {
			_, memberErr := memberTx.Exec(ctx, `
				INSERT INTO arena_wave_members (
					wave_id, roster_id, participant_id, created_at
				)
				VALUES ($1, $2, $3, $4)`,
				waveID,
				fixture.draft.rosterID,
				lateParticipantID,
				suspendedAt.Add(time.Second),
			)
			memberResult <- memberErr
		}()
		waitArenaReconnectBackendLock(t, ctx, memberPID)
		require.NoError(t, pauseTx.Commit(ctx))
		requireArenaReconnectExactCheckViolation(
			t,
			<-memberResult,
			"Arena Wave membership is immutable after normal pause history",
		)
	})

	t.Run("normal Wave pause rechecks membership committed first", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture := createArenaReconnectMigrationFixtureWithSlotLimit(t, ctx, 1)
		firstParticipantID := fixture.draft.participantIDs[0]
		lateParticipantID := fixture.draft.participantIDs[1]
		disconnectArenaParticipant(
			t, ctx, fixture, lateParticipantID,
			fixture.pausedAt.Add(time.Second), 2*time.Minute,
		)
		waveID := createArenaWaveWithMembers(
			t, ctx, fixture, []uuid.UUID{firstParticipantID}, fixture.pausedAt,
		)
		suspendedAt := fixture.pausedAt.Add(20 * time.Second)

		memberTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = memberTx.Rollback(ctx) }()
		require.NoError(t, setArenaReconnectDeadlockTimeouts(ctx, memberTx))
		_, err = memberTx.Exec(ctx, `
			INSERT INTO arena_wave_members (
				wave_id, roster_id, participant_id, created_at
			)
			VALUES ($1, $2, $3, $4)`,
			waveID,
			fixture.draft.rosterID,
			lateParticipantID,
			suspendedAt.Add(-time.Second),
		)
		require.NoError(t, err)

		pauseTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = pauseTx.Rollback(ctx) }()
		require.NoError(t, setArenaReconnectDeadlockTimeouts(ctx, pauseTx))
		var pausePID int
		require.NoError(t, pauseTx.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pausePID))
		pauseResult := make(chan error, 1)
		go func() {
			pauseErr := insertArenaNormalWavePauseEvidenceForParticipants(
				ctx, pauseTx, fixture, waveID, uuid.New(), uuid.New(), suspendedAt,
				[]uuid.UUID{firstParticipantID},
			)
			if pauseErr == nil {
				_, pauseErr = pauseTx.Exec(
					ctx,
					"SET CONSTRAINTS arena_normal_wave_reconnect_suspension IMMEDIATE",
				)
			}
			pauseResult <- pauseErr
		}()
		waitArenaReconnectBackendLock(t, ctx, pausePID)
		require.NoError(t, memberTx.Commit(ctx))
		requireArenaReconnectExactCheckViolation(
			t,
			<-pauseResult,
			"Arena normal Wave pause requires exact membership snapshot coverage",
		)
	})
}

func TestArenaReconnectContinuationLocks(t *testing.T) {
	ctx := context.Background()

	for _, lockTarget := range []string{
		"owning Game pause",
		"predecessor interval",
		"suspending Wave pause",
		"live Presence",
		"slot counter",
	} {
		t.Run(lockTarget, func(t *testing.T) {
			resetArenaMigrationTables(t)
			t.Cleanup(func() { resetArenaMigrationTables(t) })

			fixture, sourceID, sourceDeadline, suspendedAt :=
				createCancelledArenaReconnectRoot(t, ctx, 1)
			participantID := fixture.draft.participantIDs[0]
			holder, err := sharedPool.Begin(ctx)
			require.NoError(t, err)
			defer func() { _ = holder.Rollback(ctx) }()

			switch lockTarget {
			case "owning Game pause":
				_, err = holder.Exec(ctx, `
					SELECT 1 FROM arena_pauses
					WHERE id = $1 FOR NO KEY UPDATE`, fixture.gamePauseID)
			case "predecessor interval":
				_, err = holder.Exec(ctx, `
					SELECT 1 FROM arena_reconnect_intervals
					WHERE id = $1 FOR NO KEY UPDATE`, sourceID)
			case "suspending Wave pause":
				_, err = holder.Exec(ctx, `
					SELECT 1 FROM arena_pauses
					WHERE id = $1 FOR NO KEY UPDATE`, fixture.normalPauseID)
			case "live Presence":
				_, err = holder.Exec(ctx, `
					SELECT 1 FROM arena_presence_states
					WHERE series_id = $1 AND participant_id = $2
					FOR NO KEY UPDATE`, fixture.draft.seriesID, participantID)
			case "slot counter":
				_, err = holder.Exec(ctx, `
					SELECT 1 FROM arena_reconnect_slot_counters
					WHERE pause_id = $1 AND participant_id = $2
					FOR NO KEY UPDATE`, fixture.gamePauseID, participantID)
			}
			require.NoError(t, err)

			probe := beginArenaReconnectLockProbe(t, ctx)
			defer func() { _ = probe.Rollback(ctx) }()
			openedAt := suspendedAt.Add(30 * time.Second)
			err = insertArenaReconnectContinuationWith(
				ctx,
				probe,
				fixture,
				arenaReconnectContinuationInput{
					id:                 uuid.New(),
					continuedFromID:    &sourceID,
					suspendedByPauseID: nil,
					participantID:      participantID,
					presenceEpoch:      2,
					intervalNumber:     1,
					continuationNumber: 1,
					openedAt:           openedAt,
					deadlineAt:         openedAt.Add(sourceDeadline.Sub(suspendedAt)),
				},
			)
			requireArenaReconnectPostgresError(t, err, "55P03", "", "lock timeout")
		})
	}
}

func TestArenaReconnectContinuationDeadlockOrder(t *testing.T) {
	ctx := context.Background()

	t.Run("Game cancellation loses without deadlock to Wave suspension", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture := createArenaReconnectMigrationFixtureWithSlotLimit(t, ctx, 1)
		participantID := fixture.draft.participantIDs[0]
		sourceID, _ := disconnectArenaParticipant(
			t,
			ctx,
			fixture,
			participantID,
			fixture.pausedAt.Add(time.Second),
			2*time.Minute,
		)
		waveID := createArenaCoveringWave(t, ctx, fixture, fixture.pausedAt)
		suspendedAt := fixture.pausedAt.Add(20 * time.Second)

		cancelTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = cancelTx.Rollback(ctx) }()
		require.NoError(t, setArenaReconnectDeadlockTimeouts(ctx, cancelTx))
		_, err = cancelTx.Exec(ctx, `
			SELECT 1 FROM arena_pauses WHERE id = $1 FOR UPDATE`, fixture.gamePauseID)
		require.NoError(t, err)
		cancelRevisionID := uuid.New()
		_, err = cancelTx.Exec(ctx, `
			INSERT INTO arena_pause_revisions (
				id, pause_id, previous_revision_id, revision_number,
				state, transition_reason, created_at
			)
			VALUES ($1, $2, $3, 2, 'cancelled', 'operator cancel', $4)`,
			cancelRevisionID,
			fixture.gamePauseID,
			fixture.gamePauseRevisionID,
			suspendedAt,
		)
		require.NoError(t, err)

		suspensionTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = suspensionTx.Rollback(ctx) }()
		require.NoError(t, setArenaReconnectDeadlockTimeouts(ctx, suspensionTx))
		var suspensionPID int
		require.NoError(t, suspensionTx.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&suspensionPID))
		pauseID := uuid.New()
		suspensionResult := make(chan error, 1)
		go func() {
			suspensionErr := insertArenaNormalWavePauseEvidence(
				ctx,
				suspensionTx,
				fixture,
				waveID,
				pauseID,
				uuid.New(),
				suspendedAt,
			)
			if suspensionErr == nil {
				_, suspensionErr = suspensionTx.Exec(ctx, `
					UPDATE arena_reconnect_intervals
					SET state = 'cancelled',
						closed_at = $2,
						suspended_by_pause_id = $3,
						revision = revision + 1,
						updated_at = $2
					WHERE id = $1`, sourceID, suspendedAt, pauseID)
			}
			suspensionResult <- suspensionErr
		}()
		waitArenaReconnectBackendLock(t, ctx, suspensionPID)

		_, cancelErr := cancelTx.Exec(ctx, `
			UPDATE arena_pauses
			SET state = 'cancelled',
				current_revision_id = $2,
				revision = revision + 1,
				resolved_at = $3,
				updated_at = $3
			WHERE id = $1`, fixture.gamePauseID, cancelRevisionID, suspendedAt)
		requireArenaReconnectExactCheckViolation(
			t,
			cancelErr,
			"Arena pause cancellation requires closed reconnect intervals",
		)
		require.NoError(t, cancelTx.Rollback(ctx))
		require.NoError(t, <-suspensionResult)
		require.NoError(t, suspensionTx.Commit(ctx))
	})

	t.Run("resume decision loses without deadlock to continuation insertion", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture, sourceID, sourceDeadline, suspendedAt :=
			createCancelledArenaReconnectRoot(t, ctx, 1)
		participantID := fixture.draft.participantIDs[0]
		openedAt := suspendedAt.Add(30 * time.Second)

		decisionTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = decisionTx.Rollback(ctx) }()
		require.NoError(t, setArenaReconnectDeadlockTimeouts(ctx, decisionTx))
		_, err = decisionTx.Exec(ctx, `
			SELECT 1 FROM arena_pauses WHERE id = $1 FOR UPDATE`, fixture.gamePauseID)
		require.NoError(t, err)

		continuationTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = continuationTx.Rollback(ctx) }()
		require.NoError(t, setArenaReconnectDeadlockTimeouts(ctx, continuationTx))
		var continuationPID int
		require.NoError(t, continuationTx.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&continuationPID))
		continuationResult := make(chan error, 1)
		go func() {
			continuationResult <- insertArenaReconnectContinuationWith(
				ctx,
				continuationTx,
				fixture,
				arenaReconnectContinuationInput{
					id:                 uuid.New(),
					continuedFromID:    &sourceID,
					participantID:      participantID,
					presenceEpoch:      2,
					intervalNumber:     1,
					continuationNumber: 1,
					openedAt:           openedAt,
					deadlineAt:         openedAt.Add(sourceDeadline.Sub(suspendedAt)),
				},
			)
		}()
		waitArenaReconnectBackendLock(t, ctx, continuationPID)

		decisionErr := insertArenaResumeDecisionEvidence(
			ctx,
			decisionTx,
			fixture,
			fixture.gamePauseID,
			1,
			&sourceID,
			nil,
			"disconnected",
			"connected",
			2,
			1,
			2,
			1,
			"wait_first",
			openedAt,
		)
		requireArenaReconnectExactCheckViolation(
			t,
			decisionErr,
			"Arena resume decision does not match durable presence evidence",
		)
		require.NoError(t, decisionTx.Rollback(ctx))
		require.NoError(t, <-continuationResult)
		require.NoError(t, continuationTx.Commit(ctx))
	})

	t.Run("Wave suspension completes before a waiting Game cancellation", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture := createArenaReconnectMigrationFixtureWithSlotLimit(t, ctx, 1)
		participantID := fixture.draft.participantIDs[0]
		sourceID, _ := disconnectArenaParticipant(
			t,
			ctx,
			fixture,
			participantID,
			fixture.pausedAt.Add(time.Second),
			2*time.Minute,
		)
		waveID := createArenaCoveringWave(t, ctx, fixture, fixture.pausedAt)
		suspendedAt := fixture.pausedAt.Add(20 * time.Second)
		pauseID := uuid.New()
		suspensionTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = suspensionTx.Rollback(ctx) }()
		require.NoError(t, setArenaReconnectDeadlockTimeouts(ctx, suspensionTx))
		require.NoError(t, insertArenaNormalWavePauseEvidence(
			ctx,
			suspensionTx,
			fixture,
			waveID,
			pauseID,
			uuid.New(),
			suspendedAt,
		))

		cancelTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = cancelTx.Rollback(ctx) }()
		require.NoError(t, setArenaReconnectDeadlockTimeouts(ctx, cancelTx))
		var cancelPID int
		require.NoError(t, cancelTx.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&cancelPID))
		cancelResult := make(chan error, 1)
		go func() {
			_, cancelErr := cancelTx.Exec(ctx, `
				SELECT 1 FROM arena_pauses WHERE id = $1 FOR UPDATE`, fixture.gamePauseID)
			if cancelErr == nil {
				cancelRevisionID := uuid.New()
				_, cancelErr = cancelTx.Exec(ctx, `
					INSERT INTO arena_pause_revisions (
						id, pause_id, previous_revision_id, revision_number,
						state, transition_reason, created_at
					)
					VALUES ($1, $2, $3, 2, 'cancelled', 'operator cancel', $4)`,
					cancelRevisionID,
					fixture.gamePauseID,
					fixture.gamePauseRevisionID,
					suspendedAt.Add(time.Second),
				)
				if cancelErr == nil {
					_, cancelErr = cancelTx.Exec(ctx, `
						UPDATE arena_pauses
						SET state = 'cancelled',
							current_revision_id = $2,
							revision = revision + 1,
							resolved_at = $3,
							updated_at = $3
						WHERE id = $1`,
						fixture.gamePauseID,
						cancelRevisionID,
						suspendedAt.Add(time.Second),
					)
				}
			}
			cancelResult <- cancelErr
		}()
		waitArenaReconnectBackendLock(t, ctx, cancelPID)

		_, err = suspensionTx.Exec(ctx, `
			UPDATE arena_reconnect_intervals
			SET state = 'cancelled',
				closed_at = $2,
				suspended_by_pause_id = $3,
				revision = revision + 1,
				updated_at = $2
			WHERE id = $1`, sourceID, suspendedAt, pauseID)
		require.NoError(t, err)
		require.NoError(t, suspensionTx.Commit(ctx))
		require.NoError(t, <-cancelResult)
		require.NoError(t, cancelTx.Commit(ctx))
	})

	t.Run("continuation completes before a waiting resume decision", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture, sourceID, sourceDeadline, suspendedAt :=
			createCancelledArenaReconnectRoot(t, ctx, 1)
		participantID := fixture.draft.participantIDs[0]
		openedAt := suspendedAt.Add(30 * time.Second)
		continuationID := uuid.New()
		continuationTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = continuationTx.Rollback(ctx) }()
		require.NoError(t, setArenaReconnectDeadlockTimeouts(ctx, continuationTx))
		require.NoError(t, insertArenaReconnectContinuationWith(
			ctx,
			continuationTx,
			fixture,
			arenaReconnectContinuationInput{
				id:                 continuationID,
				continuedFromID:    &sourceID,
				participantID:      participantID,
				presenceEpoch:      2,
				intervalNumber:     1,
				continuationNumber: 1,
				openedAt:           openedAt,
				deadlineAt:         openedAt.Add(sourceDeadline.Sub(suspendedAt)),
			},
		))

		decisionTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = decisionTx.Rollback(ctx) }()
		require.NoError(t, setArenaReconnectDeadlockTimeouts(ctx, decisionTx))
		var decisionPID int
		require.NoError(t, decisionTx.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&decisionPID))
		decisionResult := make(chan error, 1)
		go func() {
			decisionResult <- insertArenaResumeDecisionEvidence(
				ctx,
				decisionTx,
				fixture,
				fixture.gamePauseID,
				1,
				&continuationID,
				nil,
				"disconnected",
				"connected",
				2,
				1,
				2,
				1,
				"wait_first",
				openedAt,
			)
		}()
		waitArenaReconnectBackendLock(t, ctx, decisionPID)
		require.NoError(t, continuationTx.Commit(ctx))
		require.NoError(t, <-decisionResult)
		require.NoError(t, decisionTx.Commit(ctx))
	})

	t.Run("normal Wave entry completes before a later continuation", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture, sourceID, sourceDeadline, suspendedAt :=
			createCancelledArenaReconnectRoot(t, ctx, 1)
		participantID := fixture.draft.participantIDs[0]
		waveID := createArenaCoveringWave(t, ctx, fixture, fixture.pausedAt)
		waveStartedAt := suspendedAt.Add(30 * time.Second)
		waveTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = waveTx.Rollback(ctx) }()
		require.NoError(t, setArenaReconnectDeadlockTimeouts(ctx, waveTx))
		require.NoError(t, insertArenaNormalWavePauseEvidence(
			ctx,
			waveTx,
			fixture,
			waveID,
			uuid.New(),
			uuid.New(),
			waveStartedAt,
		))

		continuationTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = continuationTx.Rollback(ctx) }()
		require.NoError(t, setArenaReconnectDeadlockTimeouts(ctx, continuationTx))
		var continuationPID int
		require.NoError(t, continuationTx.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&continuationPID))
		continuationResult := make(chan error, 1)
		go func() {
			openedAt := waveStartedAt.Add(time.Second)
			continuationResult <- insertArenaReconnectContinuationWith(
				ctx,
				continuationTx,
				fixture,
				arenaReconnectContinuationInput{
					id:                 uuid.New(),
					continuedFromID:    &sourceID,
					participantID:      participantID,
					presenceEpoch:      2,
					intervalNumber:     1,
					continuationNumber: 1,
					openedAt:           openedAt,
					deadlineAt:         openedAt.Add(sourceDeadline.Sub(suspendedAt)),
				},
			)
		}()
		waitArenaReconnectBackendLock(t, ctx, continuationPID)
		require.NoError(t, waveTx.Commit(ctx))
		require.NoError(t, <-continuationResult)
		require.NoError(t, continuationTx.Commit(ctx))
	})

	t.Run("continuation commits before a covering normal Wave entry is rejected", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture, sourceID, sourceDeadline, suspendedAt :=
			createCancelledArenaReconnectRoot(t, ctx, 1)
		participantID := fixture.draft.participantIDs[0]
		openedAt := suspendedAt.Add(30 * time.Second)
		continuationTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = continuationTx.Rollback(ctx) }()
		require.NoError(t, setArenaReconnectDeadlockTimeouts(ctx, continuationTx))
		require.NoError(t, insertArenaReconnectContinuationWith(
			ctx,
			continuationTx,
			fixture,
			arenaReconnectContinuationInput{
				id:                 uuid.New(),
				continuedFromID:    &sourceID,
				participantID:      participantID,
				presenceEpoch:      2,
				intervalNumber:     1,
				continuationNumber: 1,
				openedAt:           openedAt,
				deadlineAt:         openedAt.Add(sourceDeadline.Sub(suspendedAt)),
			},
		))

		waveID := createArenaCoveringWave(t, ctx, fixture, fixture.pausedAt)
		waveStartedAt := openedAt.Add(time.Second)
		waveTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = waveTx.Rollback(ctx) }()
		require.NoError(t, setArenaReconnectDeadlockTimeouts(ctx, waveTx))
		var wavePID int
		require.NoError(t, waveTx.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&wavePID))
		waveResult := make(chan error, 1)
		go func() {
			waveResult <- insertArenaNormalWavePauseEvidence(
				ctx,
				waveTx,
				fixture,
				waveID,
				uuid.New(),
				uuid.New(),
				waveStartedAt,
			)
		}()
		waitArenaReconnectBackendLock(t, ctx, wavePID)
		require.NoError(t, continuationTx.Commit(ctx))
		waveErr := <-waveResult
		if waveErr == nil {
			waveErr = waveTx.Commit(ctx)
		}
		requireArenaReconnectExactCheckViolation(
			t,
			waveErr,
			"Arena normal Wave pause requires atomic reconnect suspension",
		)
	})
}

func TestArenaReconnectContinuationMigrationRejectsRetainedRootEpochReuse(t *testing.T) {
	ctx := context.Background()
	tempPool, tempDB := createArenaReconnectMigrationDatabase(t, ctx, 30)
	originalPool := sharedPool
	sharedPool = tempPool
	t.Cleanup(func() { sharedPool = originalPool })

	fixture := createArenaReconnectMigrationFixtureWithSlotLimit(t, ctx, 2)
	participantID := fixture.draft.participantIDs[0]
	openedAt := fixture.pausedAt.Add(time.Second)
	firstID, _ := disconnectArenaParticipant(
		t, ctx, fixture, participantID, openedAt, 2*time.Minute,
	)
	cancelArenaReconnectInterval(t, ctx, firstID, openedAt.Add(time.Second))
	tx, err := tempPool.Begin(ctx)
	require.NoError(t, err)
	require.NoError(t, insertArenaReconnectRootAtEpoch(
		ctx, tx, fixture, participantID, uuid.New(), 2, 2, openedAt.Add(2*time.Second),
	))
	require.NoError(t, tx.Commit(ctx))
	schemaV30 := loadArenaReconnectSchemaSnapshot(t, ctx, tempPool)

	err = goose.UpToContext(ctx, tempDB, migrationsDirAbs(), 31)
	require.ErrorContains(t, err, "Arena retained reconnect roots reuse a presence epoch")
	var (
		appliedVersion int64
		rootCount      int
	)
	require.NoError(t, tempPool.QueryRow(ctx, `
		SELECT MAX(version_id)
		FROM goose_db_version
		WHERE is_applied`).Scan(&appliedVersion))
	require.EqualValues(t, 30, appliedVersion)
	require.NoError(t, tempPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM arena_reconnect_intervals
		WHERE pause_id = $1
			AND participant_id = $2
			AND presence_epoch = 2`,
		fixture.gamePauseID,
		participantID,
	).Scan(&rootCount))
	require.Equal(t, 2, rootCount)
	requireArenaReconnectSchemaSnapshotEqual(
		t,
		schemaV30,
		loadArenaReconnectSchemaSnapshot(t, ctx, tempPool),
	)
}

func TestArenaReconnectContinuationMigrationRejectsRetainedWaveMembershipDrift(t *testing.T) {
	ctx := context.Background()
	tempPool, tempDB := createArenaReconnectMigrationDatabase(t, ctx, 30)
	originalPool := sharedPool
	sharedPool = tempPool
	t.Cleanup(func() { sharedPool = originalPool })

	fixture := createArenaReconnectMigrationFixtureWithSlotLimit(t, ctx, 1)
	firstParticipantID := fixture.draft.participantIDs[0]
	lateParticipantID := fixture.draft.participantIDs[1]
	waveID := createArenaWaveWithMembers(
		t, ctx, fixture, []uuid.UUID{firstParticipantID}, fixture.pausedAt,
	)
	suspendedAt := fixture.pausedAt.Add(20 * time.Second)
	pauseTx, err := tempPool.Begin(ctx)
	require.NoError(t, err)
	require.NoError(t, insertArenaNormalWavePauseEvidenceForParticipants(
		ctx, pauseTx, fixture, waveID, uuid.New(), uuid.New(), suspendedAt,
		[]uuid.UUID{firstParticipantID},
	))
	require.NoError(t, pauseTx.Commit(ctx))
	_, err = tempPool.Exec(ctx, `
		INSERT INTO arena_wave_members (
			wave_id, roster_id, participant_id, created_at
		)
		VALUES ($1, $2, $3, $4)`,
		waveID,
		fixture.draft.rosterID,
		lateParticipantID,
		suspendedAt.Add(time.Second),
	)
	require.NoError(t, err)
	schemaV30 := loadArenaReconnectSchemaSnapshot(t, ctx, tempPool)

	err = goose.UpToContext(ctx, tempDB, migrationsDirAbs(), 31)
	require.ErrorContains(
		t,
		err,
		"Arena retained normal Wave pause lacks exact membership snapshot coverage",
	)
	var appliedVersion int64
	require.NoError(t, tempPool.QueryRow(ctx, `
		SELECT MAX(version_id)
		FROM goose_db_version
		WHERE is_applied`).Scan(&appliedVersion))
	require.EqualValues(t, 30, appliedVersion)
	requireArenaReconnectSchemaSnapshotEqual(
		t,
		schemaV30,
		loadArenaReconnectSchemaSnapshot(t, ctx, tempPool),
	)
}

func TestArenaReconnectContinuationMigrationRejectsRetainedWaveOverlap(t *testing.T) {
	ctx := context.Background()
	tempPool, tempDB := createArenaReconnectMigrationDatabase(t, ctx, 30)
	originalPool := sharedPool
	sharedPool = tempPool
	t.Cleanup(func() { sharedPool = originalPool })

	fixture := createArenaReconnectMigrationFixtureWithSlotLimit(t, ctx, 1)
	participantID := fixture.draft.participantIDs[0]
	sourceID, _ := disconnectArenaParticipant(
		t,
		ctx,
		fixture,
		participantID,
		fixture.pausedAt.Add(time.Second),
		2*time.Minute,
	)
	waveID := createArenaCoveringWave(t, ctx, fixture, fixture.pausedAt)
	suspendedAt := fixture.pausedAt.Add(20 * time.Second)
	pauseTx, err := tempPool.Begin(ctx)
	require.NoError(t, err)
	require.NoError(t, insertArenaNormalWavePauseEvidence(
		ctx,
		pauseTx,
		fixture,
		waveID,
		uuid.New(),
		uuid.New(),
		suspendedAt,
	))
	require.NoError(t, pauseTx.Commit(ctx))
	schemaV30 := loadArenaReconnectSchemaSnapshot(t, ctx, tempPool)

	err = goose.UpToContext(ctx, tempDB, migrationsDirAbs(), 31)
	require.ErrorContains(
		t,
		err,
		"Arena retained normal Wave pause lacks atomic reconnect suspension",
	)

	var (
		appliedVersion     int64
		continuationColumn int
		sourceState        string
	)
	require.NoError(t, tempPool.QueryRow(ctx, `
		SELECT MAX(version_id)
		FROM goose_db_version
		WHERE is_applied`).Scan(&appliedVersion))
	require.EqualValues(t, 30, appliedVersion)
	require.NoError(t, tempPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM information_schema.columns
		WHERE table_schema = 'public'
			AND table_name = 'arena_reconnect_intervals'
			AND column_name = 'continuation_number'`).Scan(&continuationColumn))
	require.Zero(t, continuationColumn)
	require.NoError(t, tempPool.QueryRow(ctx, `
		SELECT state FROM arena_reconnect_intervals WHERE id = $1`, sourceID).Scan(&sourceState))
	require.Equal(t, "open", sourceState)
	requireArenaReconnectSchemaSnapshotEqual(
		t,
		schemaV30,
		loadArenaReconnectSchemaSnapshot(t, ctx, tempPool),
	)
}

func TestArenaReconnectContinuationMigrationRejectsResolvedRetainedWaveOverlap(t *testing.T) {
	ctx := context.Background()
	tempPool, tempDB := createArenaReconnectMigrationDatabase(t, ctx, 30)
	originalPool := sharedPool
	sharedPool = tempPool
	t.Cleanup(func() { sharedPool = originalPool })

	fixture := createArenaReconnectMigrationFixtureWithSlotLimit(t, ctx, 1)
	disconnectArenaParticipant(
		t,
		ctx,
		fixture,
		fixture.draft.participantIDs[0],
		fixture.pausedAt.Add(time.Second),
		2*time.Minute,
	)
	waveID := createArenaCoveringWave(t, ctx, fixture, fixture.pausedAt)
	suspendedAt := fixture.pausedAt.Add(20 * time.Second)
	pauseID := uuid.New()
	pauseTx, err := tempPool.Begin(ctx)
	require.NoError(t, err)
	require.NoError(t, insertArenaNormalWavePauseEvidence(
		ctx,
		pauseTx,
		fixture,
		waveID,
		pauseID,
		uuid.New(),
		suspendedAt,
	))
	require.NoError(t, pauseTx.Commit(ctx))
	resumeArenaNormalWavePause(t, ctx, pauseID, suspendedAt.Add(time.Second))
	schemaV30 := loadArenaReconnectSchemaSnapshot(t, ctx, tempPool)

	err = goose.UpToContext(ctx, tempDB, migrationsDirAbs(), 31)
	require.ErrorContains(
		t,
		err,
		"Arena retained normal Wave pause lacks atomic reconnect suspension",
	)
	var appliedVersion int64
	require.NoError(t, tempPool.QueryRow(ctx, `
		SELECT MAX(version_id)
		FROM goose_db_version
		WHERE is_applied`).Scan(&appliedVersion))
	require.EqualValues(t, 30, appliedVersion)
	requireArenaReconnectSchemaSnapshotEqual(
		t,
		schemaV30,
		loadArenaReconnectSchemaSnapshot(t, ctx, tempPool),
	)
}

func TestArenaReconnectContinuationMigrationAcceptsRetainedDeadlineBoundary(t *testing.T) {
	ctx := context.Background()
	tempPool, tempDB := createArenaReconnectMigrationDatabase(t, ctx, 30)
	originalPool := sharedPool
	sharedPool = tempPool
	t.Cleanup(func() { sharedPool = originalPool })

	fixture := createArenaReconnectMigrationFixtureWithSlotLimit(t, ctx, 1)
	suspendedAt := fixture.pausedAt.Add(20 * time.Second)
	sourceID, sourceDeadline := disconnectArenaParticipant(
		t,
		ctx,
		fixture,
		fixture.draft.participantIDs[0],
		fixture.pausedAt.Add(time.Second),
		19*time.Second,
	)
	require.True(t, suspendedAt.Equal(sourceDeadline))
	waveID := createArenaCoveringWave(t, ctx, fixture, fixture.pausedAt)
	pauseTx, err := tempPool.Begin(ctx)
	require.NoError(t, err)
	require.NoError(t, insertArenaNormalWavePauseEvidence(
		ctx,
		pauseTx,
		fixture,
		waveID,
		uuid.New(),
		uuid.New(),
		suspendedAt,
	))
	require.NoError(t, pauseTx.Commit(ctx))

	require.NoError(t, goose.UpToContext(ctx, tempDB, migrationsDirAbs(), 31))
	var (
		appliedVersion int64
		sourceState    string
	)
	require.NoError(t, tempPool.QueryRow(ctx, `
		SELECT MAX(version_id)
		FROM goose_db_version
		WHERE is_applied`).Scan(&appliedVersion))
	require.EqualValues(t, 31, appliedVersion)
	require.NoError(t, tempPool.QueryRow(ctx, `
		SELECT state FROM arena_reconnect_intervals WHERE id = $1`, sourceID).Scan(&sourceState))
	require.Equal(t, "open", sourceState)
}

func TestArenaReconnectContinuationMigrationBackfillAndDownGuard(t *testing.T) {
	ctx := context.Background()
	tempPool, tempDB := createArenaReconnectMigrationDatabase(t, ctx, 30)
	originalPool := sharedPool
	sharedPool = tempPool
	t.Cleanup(func() { sharedPool = originalPool })

	fixture := createArenaReconnectMigrationFixtureWithSlotLimit(t, ctx, 1)
	sourceID, historicalDeadline := disconnectArenaParticipant(
		t,
		ctx,
		fixture,
		fixture.draft.participantIDs[0],
		fixture.pausedAt.Add(time.Second),
		2*time.Minute,
	)
	historicalClosedAt := fixture.pausedAt.Add(20 * time.Second)
	schemaV30 := loadArenaReconnectSchemaSnapshot(t, ctx, tempPool)
	require.NoError(t, goose.UpToContext(ctx, tempDB, migrationsDirAbs(), 31))

	var (
		continuationNumber int
		continuedFromID    pgtype.UUID
		suspendedByPauseID pgtype.UUID
	)
	err := tempPool.QueryRow(ctx, `
		SELECT continuation_number, continued_from_id, suspended_by_pause_id
		FROM arena_reconnect_intervals
		WHERE id = $1`, sourceID).Scan(
		&continuationNumber,
		&continuedFromID,
		&suspendedByPauseID,
	)
	require.NoError(t, err)
	require.Zero(t, continuationNumber)
	require.False(t, continuedFromID.Valid)
	require.False(t, suspendedByPauseID.Valid)

	cancelArenaReconnectInterval(t, ctx, sourceID, historicalClosedAt)
	genericCancelledRoot := loadArenaReconnectIntervalSnapshot(t, ctx, sourceID)
	require.Equal(t, "cancelled", genericCancelledRoot.state)
	require.True(t, genericCancelledRoot.closedAt.Valid)
	require.True(t, historicalClosedAt.Equal(genericCancelledRoot.closedAt.Time))
	require.False(t, genericCancelledRoot.suspendedByPauseID.Valid)

	historicalOpenedAt := historicalClosedAt.Add(30 * time.Second)
	err = insertArenaReconnectContinuation(ctx, fixture, arenaReconnectContinuationInput{
		id:                 uuid.New(),
		continuedFromID:    &sourceID,
		suspendedByPauseID: nil,
		participantID:      fixture.draft.participantIDs[0],
		presenceEpoch:      2,
		intervalNumber:     1,
		continuationNumber: 1,
		openedAt:           historicalOpenedAt,
		deadlineAt:         historicalOpenedAt.Add(historicalDeadline.Sub(historicalClosedAt)),
	})
	requireArenaReconnectCheckViolation(
		t,
		err,
		"Arena reconnect continuation requires a cancelled predecessor with suspension provenance",
	)

	require.NoError(t, goose.DownToContext(ctx, tempDB, migrationsDirAbs(), 30))
	requireArenaReconnectSchemaSnapshotEqual(
		t,
		schemaV30,
		loadArenaReconnectSchemaSnapshot(t, ctx, tempPool),
	)
	require.NoError(t, goose.UpToContext(ctx, tempDB, migrationsDirAbs(), 31))
	resetArenaMigrationTables(t)
	fixture, sourceID, sourceDeadline, suspendedAt := createCancelledArenaReconnectRoot(t, ctx, 1)

	openedAt := suspendedAt.Add(30 * time.Second)
	continuationID := uuid.New()
	err = insertArenaReconnectContinuation(ctx, fixture, arenaReconnectContinuationInput{
		id:                 continuationID,
		continuedFromID:    &sourceID,
		suspendedByPauseID: nil,
		participantID:      fixture.draft.participantIDs[0],
		presenceEpoch:      2,
		intervalNumber:     1,
		continuationNumber: 1,
		openedAt:           openedAt,
		deadlineAt:         openedAt.Add(sourceDeadline.Sub(suspendedAt)),
	})
	require.NoError(t, err)
	schemaBeforeDown := loadArenaReconnectSchemaSnapshot(t, ctx, tempPool)
	require.Contains(t, schemaBeforeDown.columns, "continuation_number")
	require.Contains(t, schemaBeforeDown.constraints, "continu")

	err = goose.DownToContext(ctx, tempDB, migrationsDirAbs(), 30)
	require.ErrorContains(t, err, "continuation")

	var (
		appliedVersion int64
		rowCount       int
	)
	err = tempPool.QueryRow(ctx, `
		SELECT MAX(version_id)
		FROM goose_db_version
		WHERE is_applied`).Scan(&appliedVersion)
	require.NoError(t, err)
	require.EqualValues(t, 31, appliedVersion)
	err = tempPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM arena_reconnect_intervals
		WHERE id = $1`, continuationID).Scan(&rowCount)
	require.NoError(t, err)
	require.Equal(t, 1, rowCount)
	requireArenaReconnectSchemaSnapshotEqual(
		t,
		schemaBeforeDown,
		loadArenaReconnectSchemaSnapshot(t, ctx, tempPool),
	)

	insertArenaResumeDecision(
		t,
		ctx,
		fixture,
		fixture.gamePauseID,
		1,
		&continuationID,
		nil,
		"wait_first",
		openedAt,
	)
}

func TestArenaReconnectContinuationDownRejectsProvenanceOnly(t *testing.T) {
	ctx := context.Background()
	tempPool, tempDB := createArenaReconnectMigrationDatabase(t, ctx, 31)
	originalPool := sharedPool
	sharedPool = tempPool
	t.Cleanup(func() { sharedPool = originalPool })

	_, sourceID, _, _ := createCancelledArenaReconnectRoot(t, ctx, 1)
	schemaBeforeDown := loadArenaReconnectSchemaSnapshot(t, ctx, tempPool)
	var continuationCount int
	require.NoError(t, tempPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM arena_reconnect_intervals
		WHERE continuation_number > 0`).Scan(&continuationCount))
	require.Zero(t, continuationCount)

	err := goose.DownToContext(ctx, tempDB, migrationsDirAbs(), 30)
	require.ErrorContains(t, err, "continuation history prevents schema downgrade")

	var (
		appliedVersion int64
		provenanceID   uuid.UUID
	)
	require.NoError(t, tempPool.QueryRow(ctx, `
		SELECT MAX(version_id)
		FROM goose_db_version
		WHERE is_applied`).Scan(&appliedVersion))
	require.EqualValues(t, 31, appliedVersion)
	require.NoError(t, tempPool.QueryRow(ctx, `
		SELECT suspended_by_pause_id
		FROM arena_reconnect_intervals
		WHERE id = $1`, sourceID).Scan(&provenanceID))
	require.NotEqual(t, uuid.Nil, provenanceID)
	requireArenaReconnectSchemaSnapshotEqual(
		t,
		schemaBeforeDown,
		loadArenaReconnectSchemaSnapshot(t, ctx, tempPool),
	)
}

func createArenaReconnectMigrationDatabase(
	t testing.TB,
	ctx context.Context,
	version int64,
) (*pgxpool.Pool, *sql.DB) {
	t.Helper()

	adminPool := sharedPool
	databaseName := uniq("arena_continuation")
	_, err := adminPool.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{databaseName}.Sanitize())
	require.NoError(t, err)

	config := adminPool.Config().Copy()
	config.ConnConfig.Database = databaseName
	database := stdlib.OpenDB(*config.ConnConfig)
	require.NoError(t, database.PingContext(ctx))
	require.NoError(t, goose.UpToContext(ctx, database, migrationsDirAbs(), version))

	pool, err := pgxpool.NewWithConfig(ctx, config)
	require.NoError(t, err)
	require.NoError(t, pool.Ping(ctx))
	t.Cleanup(func() {
		pool.Close()
		_ = database.Close()
		_, dropErr := adminPool.Exec(
			context.Background(),
			"DROP DATABASE "+pgx.Identifier{databaseName}.Sanitize()+" WITH (FORCE)",
		)
		require.NoError(t, dropErr)
	})
	return pool, database
}

func createCancelledArenaReconnectRoot(
	t testing.TB,
	ctx context.Context,
	slotLimit int,
) (arenaReconnectMigrationFixture, uuid.UUID, time.Time, time.Time) {
	t.Helper()

	fixture := createArenaReconnectMigrationFixtureWithSlotLimit(t, ctx, slotLimit)
	participantID := fixture.draft.participantIDs[0]
	sourceID, sourceDeadline := disconnectArenaParticipant(
		t,
		ctx,
		fixture,
		participantID,
		fixture.pausedAt.Add(time.Second),
		2*time.Minute,
	)
	suspendedAt := fixture.pausedAt.Add(20 * time.Second)
	waveID, _ := createArenaMigrationWave(
		t,
		ctx,
		fixture.draft.tournamentID,
		fixture.draft.rosterID,
		fixture.draft.participantIDs,
		nil,
		fixture.pausedAt,
	)
	fixture.normalWaveID = waveID
	fixture.normalPauseID = createArenaNormalWavePauseAndSuspendInterval(
		t,
		ctx,
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
		FROM arena_pauses AS pause
		JOIN arena_reconnect_intervals AS source ON source.id = $2
		WHERE pause.id = $1`, fixture.normalPauseID, sourceID).Scan(
		&pauseStartedAt,
		&sourceClosedAt,
		&suspendingID,
	)
	require.NoError(t, err)
	require.True(t, suspendedAt.Equal(pauseStartedAt))
	require.True(t, pauseStartedAt.Equal(sourceClosedAt))
	require.Equal(t, fixture.normalPauseID, suspendingID)
	return fixture, sourceID, sourceDeadline, suspendedAt
}

func insertArenaNormalWavePauseEvidence(
	ctx context.Context,
	executor arenaReconnectExecutor,
	fixture arenaReconnectMigrationFixture,
	waveID uuid.UUID,
	pauseID uuid.UUID,
	revisionID uuid.UUID,
	suspendedAt time.Time,
) error {
	return insertArenaNormalWavePauseEvidenceForParticipants(
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

func insertArenaNormalWavePauseEvidenceForParticipants(
	ctx context.Context,
	executor arenaReconnectExecutor,
	fixture arenaReconnectMigrationFixture,
	waveID uuid.UUID,
	pauseID uuid.UUID,
	revisionID uuid.UUID,
	suspendedAt time.Time,
	participantIDs []uuid.UUID,
) error {
	_, err := executor.Exec(ctx, `
		INSERT INTO arena_pauses (
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
		INSERT INTO arena_pause_revisions (
			id, pause_id, revision_number, state, created_at
		)
		VALUES ($1, $2, 1, 'active', $3)`, revisionID, pauseID, suspendedAt)
	if err != nil {
		return err
	}
	for _, participantID := range participantIDs {
		_, err = executor.Exec(ctx, `
			INSERT INTO arena_pause_presence_snapshots (
				pause_id, roster_id, series_id, participant_id,
				presence_state, presence_epoch, presence_revision,
				captured_at, created_at
			)
			SELECT
				$1, presence.roster_id, presence.series_id, presence.participant_id,
				presence.state, presence.presence_epoch, presence.revision,
				$3, $3
			FROM arena_presence_states AS presence
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

func attemptArenaReconnectRoot(
	ctx context.Context,
	fixture arenaReconnectMigrationFixture,
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
		SELECT 1 FROM arena_pauses WHERE id = $1 FOR UPDATE`, fixture.gamePauseID)
	if err != nil {
		return err
	}
	if err = insertArenaReconnectRootEvidence(
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

func insertArenaReconnectRootEvidence(
	ctx context.Context,
	executor arenaReconnectExecutor,
	fixture arenaReconnectMigrationFixture,
	participantID uuid.UUID,
	intervalID uuid.UUID,
	openedAt time.Time,
	deadlineAt time.Time,
) error {
	var presenceEpoch int64
	err := executor.QueryRow(ctx, `
		UPDATE arena_presence_states
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
	_, err = executor.Exec(ctx, `
		INSERT INTO arena_reconnect_intervals (
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
	_, err = executor.Exec(ctx, `
		UPDATE arena_reconnect_slot_counters
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

func insertArenaReconnectRootAtEpoch(
	ctx context.Context,
	executor arenaReconnectExecutor,
	fixture arenaReconnectMigrationFixture,
	participantID uuid.UUID,
	intervalID uuid.UUID,
	presenceEpoch int64,
	intervalNumber int,
	openedAt time.Time,
) error {
	_, err := executor.Exec(ctx, `
		INSERT INTO arena_reconnect_intervals (
			id, pause_id, roster_id, series_id, game_attempt_id,
			participant_id, presence_epoch, interval_number,
			opened_at, deadline_at, created_at, updated_at
		)
		VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8,
			$9, $10, $9, $9
		)`,
		intervalID,
		fixture.gamePauseID,
		fixture.draft.rosterID,
		fixture.draft.seriesID,
		fixture.attemptID,
		participantID,
		presenceEpoch,
		intervalNumber,
		openedAt,
		openedAt.Add(2*time.Minute),
	)
	if err != nil {
		return err
	}
	_, err = executor.Exec(ctx, `
		UPDATE arena_reconnect_slot_counters
		SET slots_used = slots_used + 1,
			revision = revision + 1,
			updated_at = $3
		WHERE pause_id = $1 AND participant_id = $2`,
		fixture.gamePauseID,
		participantID,
		openedAt,
	)
	return err
}

func createArenaNormalWavePauseAndSuspendInterval(
	t testing.TB,
	ctx context.Context,
	fixture arenaReconnectMigrationFixture,
	waveID uuid.UUID,
	intervalID uuid.UUID,
	suspendedAt time.Time,
) uuid.UUID {
	t.Helper()

	pauseID := uuid.New()
	revisionID := uuid.New()
	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `
		INSERT INTO arena_pauses (
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
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO arena_pause_revisions (
			id, pause_id, revision_number, state, created_at
		)
		VALUES ($1, $2, 1, 'active', $3)`, revisionID, pauseID, suspendedAt)
	require.NoError(t, err)

	for _, participantID := range fixture.draft.participantIDs {
		_, err = tx.Exec(ctx, `
			INSERT INTO arena_pause_presence_snapshots (
				pause_id, roster_id, series_id, participant_id,
				presence_state, presence_epoch, presence_revision,
				captured_at, created_at
			)
			SELECT
				$1, presence.roster_id, presence.series_id, presence.participant_id,
				presence.state, presence.presence_epoch, presence.revision,
				$3, $3
			FROM arena_presence_states AS presence
			WHERE presence.series_id = $2 AND presence.participant_id = $4`,
			pauseID,
			fixture.draft.seriesID,
			suspendedAt,
			participantID,
		)
		require.NoError(t, err)
	}
	_, err = tx.Exec(ctx, `
		UPDATE arena_reconnect_intervals
		SET state = 'cancelled',
			closed_at = $2,
			suspended_by_pause_id = $3,
			revision = revision + 1,
			updated_at = $2
		WHERE id = $1`, intervalID, suspendedAt, pauseID)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
	return pauseID
}

func resumeArenaNormalWavePause(
	t testing.TB,
	ctx context.Context,
	pauseID uuid.UUID,
	resumedAt time.Time,
) {
	t.Helper()

	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	var previousRevisionID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT current_revision_id
		FROM arena_pauses
		WHERE id = $1
		FOR UPDATE`, pauseID).Scan(&previousRevisionID)
	require.NoError(t, err)
	revisionID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO arena_pause_revisions (
			id, pause_id, previous_revision_id, revision_number,
			state, transition_reason, created_at
		)
		VALUES ($1, $2, $3, 2, 'resumed', 'continuation opened', $4)`,
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
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
}

func createArenaCoveringWave(
	t testing.TB,
	ctx context.Context,
	fixture arenaReconnectMigrationFixture,
	createdAt time.Time,
) uuid.UUID {
	t.Helper()

	waveID, _ := createArenaMigrationWave(
		t,
		ctx,
		fixture.draft.tournamentID,
		fixture.draft.rosterID,
		fixture.draft.participantIDs,
		nil,
		createdAt,
	)
	return waveID
}

func createArenaWaveWithMembers(
	t testing.TB,
	ctx context.Context,
	fixture arenaReconnectMigrationFixture,
	participantIDs []uuid.UUID,
	createdAt time.Time,
) uuid.UUID {
	t.Helper()

	waveID := uuid.New()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO arena_waves (
			id, tournament_id, roster_id, revision_id, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $5)`,
		waveID,
		fixture.draft.tournamentID,
		fixture.draft.rosterID,
		uuid.New(),
		createdAt,
	)
	require.NoError(t, err)
	for _, participantID := range participantIDs {
		_, err = sharedPool.Exec(ctx, `
			INSERT INTO arena_wave_members (
				wave_id, roster_id, participant_id, created_at
			)
			VALUES ($1, $2, $3, $4)`,
			waveID,
			fixture.draft.rosterID,
			participantID,
			createdAt,
		)
		require.NoError(t, err)
	}
	return waveID
}

func attemptArenaNormalWavePauseAndSuspendInterval(
	t testing.TB,
	ctx context.Context,
	fixture arenaReconnectMigrationFixture,
	waveID uuid.UUID,
	participantIDs []uuid.UUID,
	intervalID uuid.UUID,
	suspendedAt time.Time,
) error {
	t.Helper()

	pauseID := uuid.New()
	revisionID := uuid.New()
	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `
		INSERT INTO arena_pauses (
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
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO arena_pause_revisions (
			id, pause_id, revision_number, state, created_at
		)
		VALUES ($1, $2, 1, 'active', $3)`, revisionID, pauseID, suspendedAt)
	require.NoError(t, err)
	for _, participantID := range participantIDs {
		_, err = tx.Exec(ctx, `
			INSERT INTO arena_pause_presence_snapshots (
				pause_id, roster_id, series_id, participant_id,
				presence_state, presence_epoch, presence_revision,
				captured_at, created_at
			)
			SELECT
				$1, presence.roster_id, presence.series_id, presence.participant_id,
				presence.state, presence.presence_epoch, presence.revision,
				$3, $3
			FROM arena_presence_states AS presence
			WHERE presence.series_id = $2 AND presence.participant_id = $4`,
			pauseID,
			fixture.draft.seriesID,
			suspendedAt,
			participantID,
		)
		require.NoError(t, err)
	}
	_, err = tx.Exec(ctx, `
		UPDATE arena_reconnect_intervals
		SET state = 'cancelled',
			closed_at = $2,
			suspended_by_pause_id = $3,
			revision = revision + 1,
			updated_at = $2
		WHERE id = $1`, intervalID, suspendedAt, pauseID)
	return err
}

func assertArenaNormalPauseIsSeparateFromGameChain(
	t testing.TB,
	ctx context.Context,
	fixture arenaReconnectMigrationFixture,
) {
	t.Helper()

	var (
		seriesScope     string
		gameScope       string
		normalScope     string
		gameParentID    uuid.UUID
		normalParentID  pgtype.UUID
		ancestorOverlap int
		coverageCount   int
	)
	err := sharedPool.QueryRow(ctx, `
		SELECT
			series_pause.scope_kind,
			game_pause.scope_kind,
			normal_pause.scope_kind,
			game_pause.parent_pause_id,
			normal_pause.parent_pause_id
		FROM arena_pauses AS series_pause
		JOIN arena_pauses AS game_pause ON game_pause.id = $2
		JOIN arena_pauses AS normal_pause ON normal_pause.id = $3
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
	require.NoError(t, err)
	require.Equal(t, "series", seriesScope)
	require.Equal(t, "game_attempt", gameScope)
	require.Equal(t, "wave", normalScope)
	require.Equal(t, fixture.rootPauseID, gameParentID)
	require.False(t, normalParentID.Valid)

	err = sharedPool.QueryRow(ctx, `
		WITH RECURSIVE game_ancestors AS (
			SELECT parent_pause_id
			FROM arena_pauses
			WHERE id = $1

			UNION ALL

			SELECT pause.parent_pause_id
			FROM arena_pauses AS pause
			JOIN game_ancestors AS ancestor ON pause.id = ancestor.parent_pause_id
			WHERE ancestor.parent_pause_id IS NOT NULL
		)
		SELECT COUNT(*)
		FROM game_ancestors
		WHERE parent_pause_id = $2`, fixture.gamePauseID, fixture.normalPauseID).Scan(&ancestorOverlap)
	require.NoError(t, err)
	require.Zero(t, ancestorOverlap)

	err = sharedPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM arena_wave_members
		WHERE wave_id = $1
			AND participant_id = ANY($2::UUID[])`,
		fixture.normalWaveID,
		fixture.draft.participantIDs,
	).Scan(&coverageCount)
	require.NoError(t, err)
	require.Equal(t, len(fixture.draft.participantIDs), coverageCount)
}

func insertArenaReconnectContinuation(
	ctx context.Context,
	fixture arenaReconnectMigrationFixture,
	input arenaReconnectContinuationInput,
) error {
	return insertArenaReconnectContinuationWith(ctx, sharedPool, fixture, input)
}

type arenaReconnectExecutor interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func insertArenaReconnectContinuationWith(
	ctx context.Context,
	executor arenaReconnectExecutor,
	fixture arenaReconnectMigrationFixture,
	input arenaReconnectContinuationInput,
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

	_, err := executor.Exec(ctx, `
		INSERT INTO arena_reconnect_intervals (
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

func cancelArenaReconnectInterval(
	t testing.TB,
	ctx context.Context,
	intervalID uuid.UUID,
	cancelledAt time.Time,
) {
	t.Helper()

	commandTag, err := sharedPool.Exec(ctx, `
		UPDATE arena_reconnect_intervals
		SET state = 'cancelled',
			closed_at = $2,
			revision = revision + 1,
			updated_at = $2
		WHERE id = $1`, intervalID, cancelledAt)
	require.NoError(t, err)
	require.EqualValues(t, 1, commandTag.RowsAffected())
}

func disconnectArenaPresence(
	t testing.TB,
	ctx context.Context,
	fixture arenaReconnectMigrationFixture,
	participantID uuid.UUID,
	disconnectedAt time.Time,
) {
	t.Helper()

	commandTag, err := sharedPool.Exec(ctx, `
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
	require.EqualValues(t, 1, commandTag.RowsAffected())
}

func loadArenaReconnectIntervalSnapshot(
	t testing.TB,
	ctx context.Context,
	intervalID uuid.UUID,
) arenaReconnectIntervalSnapshot {
	t.Helper()

	var snapshot arenaReconnectIntervalSnapshot
	err := sharedPool.QueryRow(ctx, `
		SELECT
			id, pause_id, roster_id, series_id, game_attempt_id,
			participant_id, presence_epoch, interval_number,
			continuation_number, continued_from_id, suspended_by_pause_id,
			state, opened_at, deadline_at, closed_at,
			revision, created_at, updated_at
		FROM arena_reconnect_intervals
		WHERE id = $1`, intervalID).Scan(
		&snapshot.id,
		&snapshot.pauseID,
		&snapshot.rosterID,
		&snapshot.seriesID,
		&snapshot.gameAttemptID,
		&snapshot.participantID,
		&snapshot.presenceEpoch,
		&snapshot.intervalNumber,
		&snapshot.continuationNumber,
		&snapshot.continuedFromID,
		&snapshot.suspendedByPauseID,
		&snapshot.state,
		&snapshot.openedAt,
		&snapshot.deadlineAt,
		&snapshot.closedAt,
		&snapshot.revision,
		&snapshot.createdAt,
		&snapshot.updatedAt,
	)
	require.NoError(t, err)
	return snapshot
}

func loadArenaReconnectSchemaSnapshot(
	t testing.TB,
	ctx context.Context,
	pool *pgxpool.Pool,
) arenaReconnectSchemaSnapshot {
	t.Helper()

	var snapshot arenaReconnectSchemaSnapshot
	err := pool.QueryRow(ctx, `
		SELECT STRING_AGG(
			table_name || '.' || column_name || ':' || data_type || ':' || is_nullable || ':'
				|| COALESCE(column_default, ''),
			',' ORDER BY table_name, ordinal_position
		)
		FROM information_schema.columns
		WHERE table_schema = 'public'
			AND table_name = ANY(ARRAY[
				'arena_wave_members',
				'arena_pauses',
				'arena_reconnect_intervals',
				'arena_reconnect_slot_counters',
				'arena_resume_decisions'
			])`).Scan(&snapshot.columns)
	require.NoError(t, err)
	err = pool.QueryRow(ctx, `
		SELECT STRING_AGG(
			relation.relname || '.' || constraint_row.conname || ':'
				|| pg_get_constraintdef(constraint_row.oid, TRUE),
			E'\n---\n' ORDER BY relation.relname, constraint_row.conname
		)
		FROM pg_constraint AS constraint_row
		JOIN pg_class AS relation ON relation.oid = constraint_row.conrelid
		JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
		WHERE namespace.nspname = 'public'
			AND relation.relname = ANY(ARRAY[
				'arena_wave_members',
				'arena_pauses',
				'arena_reconnect_intervals',
				'arena_reconnect_slot_counters',
				'arena_resume_decisions'
			])`).Scan(&snapshot.constraints)
	require.NoError(t, err)
	err = pool.QueryRow(ctx, `
		SELECT STRING_AGG(
			pg_get_indexdef(index_row.indexrelid),
			E'\n---\n' ORDER BY relation.relname, index_relation.relname
		)
		FROM pg_index AS index_row
		JOIN pg_class AS relation ON relation.oid = index_row.indrelid
		JOIN pg_class AS index_relation ON index_relation.oid = index_row.indexrelid
		JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
		WHERE namespace.nspname = 'public'
			AND relation.relname = ANY(ARRAY[
				'arena_wave_members',
				'arena_pauses',
				'arena_reconnect_intervals',
				'arena_reconnect_slot_counters',
				'arena_resume_decisions'
			])`).Scan(&snapshot.indexes)
	require.NoError(t, err)
	err = pool.QueryRow(ctx, `
		SELECT STRING_AGG(
			relation.relname || '.' || trigger_row.tgname || ':'
				|| pg_get_triggerdef(trigger_row.oid, TRUE),
			E'\n---\n' ORDER BY relation.relname, trigger_row.tgname
		)
		FROM pg_trigger AS trigger_row
		JOIN pg_class AS relation ON relation.oid = trigger_row.tgrelid
		JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
		WHERE namespace.nspname = 'public'
			AND relation.relname = ANY(ARRAY[
				'arena_wave_members',
				'arena_pauses',
				'arena_reconnect_intervals',
				'arena_reconnect_slot_counters',
				'arena_resume_decisions'
			])
			AND NOT trigger_row.tgisinternal`).Scan(&snapshot.triggers)
	require.NoError(t, err)
	err = pool.QueryRow(ctx, `
		SELECT STRING_AGG(
			pg_get_functiondef(function_row.oid),
			E'\n---\n' ORDER BY function_namespace.nspname,
				function_row.proname,
				pg_get_function_identity_arguments(function_row.oid)
		)
		FROM pg_proc AS function_row
		JOIN pg_namespace AS function_namespace
			ON function_namespace.oid = function_row.pronamespace
		WHERE function_namespace.nspname = 'public'
			AND function_row.proname LIKE 'arena\_%' ESCAPE '\'`).Scan(&snapshot.functions)
	require.NoError(t, err)
	err = pool.QueryRow(ctx, `
		SELECT COALESCE(STRING_AGG(
			relation.relname || '.' || COALESCE(attribute.attname, '') || ':'
				|| description.description,
			E'\n---\n' ORDER BY relation.relname, description.objsubid
		), '')
		FROM pg_description AS description
		JOIN pg_class AS relation
			ON description.classoid = 'pg_class'::REGCLASS
			AND description.objoid = relation.oid
		JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
		LEFT JOIN pg_attribute AS attribute
			ON attribute.attrelid = relation.oid
			AND attribute.attnum = description.objsubid
		WHERE namespace.nspname = 'public'
			AND relation.relname = ANY(ARRAY[
				'arena_wave_members',
				'arena_pauses',
				'arena_reconnect_intervals',
				'arena_reconnect_slot_counters',
				'arena_resume_decisions'
			])`).Scan(&snapshot.comments)
	require.NoError(t, err)
	return snapshot
}

func requireArenaReconnectSchemaSnapshotEqual(
	t testing.TB,
	expected arenaReconnectSchemaSnapshot,
	actual arenaReconnectSchemaSnapshot,
) {
	t.Helper()
	require.Equal(t, expected.columns, actual.columns, "column definitions")
	require.Equal(t, expected.constraints, actual.constraints, "constraint definitions")
	require.Equal(t, expected.indexes, actual.indexes, "index definitions")
	require.Equal(t, expected.triggers, actual.triggers, "trigger definitions")
	require.Equal(t, expected.functions, actual.functions, "Arena function definitions")
	require.Equal(t, expected.comments, actual.comments, "schema comments")
}

func requireArenaReconnectCheckViolation(
	t testing.TB,
	err error,
	diagnostic string,
) {
	t.Helper()

	var postgresError *pgconn.PgError
	require.ErrorAs(t, err, &postgresError)
	require.Equal(t, "23514", postgresError.Code)
	require.Contains(t, postgresError.Message, diagnostic)
}

func requireArenaReconnectExactCheckViolation(
	t testing.TB,
	err error,
	diagnostic string,
) {
	t.Helper()

	var postgresError *pgconn.PgError
	require.ErrorAs(t, err, &postgresError)
	require.Equal(t, "23514", postgresError.Code)
	require.Equal(t, diagnostic, postgresError.Message)
}

func requireArenaReconnectPostgresError(
	t testing.TB,
	err error,
	code string,
	constraint string,
	diagnostic string,
) {
	t.Helper()

	var postgresError *pgconn.PgError
	require.ErrorAs(t, err, &postgresError)
	require.Equal(t, code, postgresError.Code)
	if constraint != "" {
		require.Equal(t, constraint, postgresError.ConstraintName)
	}
	if diagnostic != "" {
		require.Contains(t, postgresError.Message, diagnostic)
	}
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
	intervalID, _ := disconnectArenaParticipant(
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
	reconnectArenaParticipant(
		t,
		ctx,
		fixture,
		fixture.draft.participantIDs[0],
		intervalID,
		reconnectedAt,
	)
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
	resumeArenaMigrationPause(t, ctx, fixture, true, fixture.pausedAt.Add(5*time.Second))
}

func TestArenaReconnectMigrationIntervalPauseIntegrity(t *testing.T) {
	ctx := context.Background()

	t.Run("rejects interval after Game pause resolution", func(t *testing.T) {
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
		resumeArenaMigrationPause(t, ctx, fixture, true, fixture.pausedAt.Add(time.Second))
		assertArenaReconnectIntervalRejected(
			t,
			ctx,
			fixture,
			fixture.gamePauseID,
			fixture.pausedAt.Add(2*time.Second),
		)
	})

	t.Run("rejects interval outside Game pause scope", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture := createArenaReconnectMigrationFixture(t, ctx)
		assertArenaReconnectIntervalRejected(
			t,
			ctx,
			fixture,
			fixture.rootPauseID,
			fixture.pausedAt.Add(time.Second),
		)
	})

	t.Run("rejects cancellation with open interval", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture := createArenaReconnectMigrationFixture(t, ctx)
		_, _ = disconnectArenaParticipant(
			t,
			ctx,
			fixture,
			fixture.draft.participantIDs[0],
			fixture.pausedAt.Add(time.Second),
			2*time.Minute,
		)
		assertArenaPauseCancellationRejected(
			t,
			ctx,
			fixture.gamePauseID,
			fixture.gamePauseRevisionID,
			fixture.pausedAt.Add(2*time.Second),
			"closed reconnect intervals",
		)
	})

	t.Run("rejects cancellation with active child pause", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture := createArenaReconnectMigrationFixture(t, ctx)
		assertArenaPauseCancellationRejected(
			t,
			ctx,
			fixture.rootPauseID,
			fixture.rootPauseRevisionID,
			fixture.pausedAt.Add(time.Second),
			"active descendants",
		)
	})

	t.Run("rejects interval expiry before deadline", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture := createArenaReconnectMigrationFixture(t, ctx)
		intervalID, deadline := disconnectArenaParticipant(
			t,
			ctx,
			fixture,
			fixture.draft.participantIDs[0],
			fixture.pausedAt.Add(time.Second),
			2*time.Minute,
		)
		closedAt := deadline.Add(-time.Second)
		_, err := sharedPool.Exec(ctx, `
			UPDATE arena_reconnect_intervals
			SET state = 'expired',
				closed_at = $2,
				revision = revision + 1,
				updated_at = $2
			WHERE id = $1`, intervalID, closedAt)
		require.ErrorContains(t, err, "arena_reconnect_intervals_terminal_time_check")
	})

	t.Run("rejects interval reconnect after deadline", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture := createArenaReconnectMigrationFixture(t, ctx)
		intervalID, deadline := disconnectArenaParticipant(
			t,
			ctx,
			fixture,
			fixture.draft.participantIDs[0],
			fixture.pausedAt.Add(time.Second),
			2*time.Minute,
		)
		closedAt := deadline.Add(time.Second)
		_, err := sharedPool.Exec(ctx, `
			UPDATE arena_reconnect_intervals
			SET state = 'reconnected',
				closed_at = $2,
				revision = revision + 1,
				updated_at = $2
			WHERE id = $1`, intervalID, closedAt)
		require.ErrorContains(t, err, "arena_reconnect_intervals_terminal_time_check")
	})

	t.Run("rejects interval close after update evidence", func(t *testing.T) {
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
		updatedAt := fixture.pausedAt.Add(2 * time.Second)
		closedAt := updatedAt.Add(time.Second)
		_, err := sharedPool.Exec(ctx, `
			UPDATE arena_reconnect_intervals
			SET state = 'cancelled',
				closed_at = $2,
				revision = revision + 1,
				updated_at = $3
			WHERE id = $1`, intervalID, closedAt, updatedAt)
		require.ErrorContains(t, err, "arena_reconnect_intervals_terminal_time_check")
	})
}

func TestArenaReconnectMigrationTerminalPresenceCAS(t *testing.T) {
	ctx := context.Background()

	t.Run("rejects reconnect without presence CAS", func(t *testing.T) {
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
		reconnectedAt := fixture.pausedAt.Add(2 * time.Second)
		_, err := sharedPool.Exec(ctx, `
			UPDATE arena_reconnect_intervals
			SET state = 'reconnected',
				closed_at = $2,
				revision = revision + 1,
				updated_at = $2
			WHERE id = $1`, intervalID, reconnectedAt)
		require.ErrorContains(t, err, "terminal state differs from live presence CAS")
	})

	t.Run("rejects presence reconnect with open interval", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture := createArenaReconnectMigrationFixture(t, ctx)
		_, _ = disconnectArenaParticipant(
			t,
			ctx,
			fixture,
			fixture.draft.participantIDs[0],
			fixture.pausedAt.Add(time.Second),
			2*time.Minute,
		)
		reconnectedAt := fixture.pausedAt.Add(2 * time.Second)
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
		require.ErrorContains(t, err, "presence retains an open interval")
	})

	t.Run("rejects expiry with reconnected presence", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture := createArenaReconnectMigrationFixture(t, ctx)
		participantID := fixture.draft.participantIDs[0]
		intervalID, deadline := disconnectArenaParticipant(
			t,
			ctx,
			fixture,
			participantID,
			fixture.pausedAt.Add(time.Second),
			2*time.Minute,
		)
		tx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()
		_, err = tx.Exec(ctx, `
			UPDATE arena_reconnect_intervals
			SET state = 'expired',
				closed_at = $2,
				revision = revision + 1,
				updated_at = $2
			WHERE id = $1`, intervalID, deadline)
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
			deadline,
		)
		require.NoError(t, err)
		err = tx.Commit(ctx)
		require.ErrorContains(t, err, "terminal state differs from live presence CAS")
	})
}

func TestArenaReconnectMigrationCASLocks(t *testing.T) {
	ctx := context.Background()

	t.Run("pause snapshot locks live presence", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture := createArenaReconnectMigrationFixture(t, ctx)
		participantID := fixture.draft.participantIDs[0]
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
		pauseID := uuid.New()
		pausedAt := fixture.pausedAt.Add(10 * time.Second)
		_, err = probeTx.Exec(ctx, `
			INSERT INTO arena_pauses (
				id, tournament_id, roster_id, scope_kind, scope_id,
				reason, paused_from_state, current_revision_id,
				started_at, created_at, updated_at
			)
			VALUES (
				$1, $2, $3, 'tournament', $2,
				'operator', 'active', $4,
				$5, $5, $5
			)`,
			pauseID,
			fixture.draft.tournamentID,
			fixture.draft.rosterID,
			uuid.New(),
			pausedAt,
		)
		require.NoError(t, err)
		_, err = probeTx.Exec(ctx, `
			INSERT INTO arena_pause_presence_snapshots (
				pause_id, roster_id, series_id, participant_id,
				presence_state, presence_epoch, presence_revision,
				captured_at, created_at
			)
			VALUES ($1, $2, $3, $4, 'connected', 1, 1, $5, $5)`,
			pauseID,
			fixture.draft.rosterID,
			fixture.draft.seriesID,
			participantID,
			pausedAt,
		)
		require.ErrorContains(t, err, "lock timeout")
	})

	t.Run("interval terminal transition locks live presence", func(t *testing.T) {
		resetArenaMigrationTables(t)
		t.Cleanup(func() { resetArenaMigrationTables(t) })

		fixture := createArenaReconnectMigrationFixture(t, ctx)
		participantID := fixture.draft.participantIDs[0]
		intervalID, deadline := disconnectArenaParticipant(
			t,
			ctx,
			fixture,
			participantID,
			fixture.pausedAt.Add(time.Second),
			2*time.Minute,
		)

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
			UPDATE arena_reconnect_intervals
			SET state = 'expired',
				closed_at = $2,
				revision = revision + 1,
				updated_at = $2
			WHERE id = $1`, intervalID, deadline)
		require.ErrorContains(t, err, "lock timeout")
	})

	t.Run("interval creation locks owning pause", func(t *testing.T) {
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
			FROM arena_pauses
			WHERE id = $1
			FOR NO KEY UPDATE`, fixture.gamePauseID)
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

	t.Run("pause cancellation locks reconnect intervals", func(t *testing.T) {
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
		revisionID := uuid.New()
		cancelledAt := fixture.pausedAt.Add(2 * time.Second)
		_, err = probeTx.Exec(ctx, `
			INSERT INTO arena_pause_revisions (
				id, pause_id, previous_revision_id, revision_number,
				state, transition_reason, created_at
			)
			VALUES ($1, $2, $3, 2, 'cancelled', 'operator cancel', $4)`,
			revisionID,
			fixture.gamePauseID,
			fixture.gamePauseRevisionID,
			cancelledAt,
		)
		require.NoError(t, err)
		_, err = probeTx.Exec(ctx, `
			UPDATE arena_pauses
			SET state = 'cancelled',
				current_revision_id = $2,
				revision = revision + 1,
				resolved_at = $3,
				updated_at = $3
			WHERE id = $1`, fixture.gamePauseID, revisionID, cancelledAt)
		require.ErrorContains(t, err, "lock timeout")
	})
}

func assertArenaReconnectIntervalRejected(
	t testing.TB,
	ctx context.Context,
	fixture arenaReconnectMigrationFixture,
	pauseID uuid.UUID,
	disconnectedAt time.Time,
) {
	t.Helper()

	participantID := fixture.draft.participantIDs[0]
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

	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
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
		uuid.New(),
		pauseID,
		fixture.draft.rosterID,
		fixture.draft.seriesID,
		fixture.attemptID,
		participantID,
		disconnectedAt,
		disconnectedAt.Add(2*time.Minute),
	)
	require.ErrorContains(t, err, "active Game pause")
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

func assertArenaPauseCancellationRejected(
	t testing.TB,
	ctx context.Context,
	pauseID uuid.UUID,
	previousRevisionID uuid.UUID,
	cancelledAt time.Time,
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
		VALUES ($1, $2, $3, 2, 'cancelled', 'operator cancel', $4)`,
		revisionID,
		pauseID,
		previousRevisionID,
		cancelledAt,
	)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		UPDATE arena_pauses
		SET state = 'cancelled',
			current_revision_id = $2,
			revision = revision + 1,
			resolved_at = $3,
			updated_at = $3
		WHERE id = $1`, pauseID, revisionID, cancelledAt)
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

func setArenaReconnectDeadlockTimeouts(
	ctx context.Context,
	executor arenaReconnectExecutor,
) error {
	for _, statement := range []string{
		"SET LOCAL deadlock_timeout = '100ms'",
		"SET LOCAL lock_timeout = '2s'",
		"SET LOCAL statement_timeout = '3s'",
	} {
		if _, err := executor.Exec(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func waitArenaReconnectBackendLock(t testing.TB, ctx context.Context, backendPID int) {
	t.Helper()

	require.Eventually(t, func() bool {
		var waitEventType string
		err := sharedPool.QueryRow(ctx, `
			SELECT COALESCE(wait_event_type, '')
			FROM pg_stat_activity
			WHERE pid = $1`, backendPID).Scan(&waitEventType)
		return err == nil && waitEventType == "Lock"
	}, 2*time.Second, 10*time.Millisecond)
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
	return createArenaReconnectMigrationFixtureWithSlotLimit(t, ctx, 2)
}

func createArenaReconnectMigrationFixtureWithSlotLimit(
	t testing.TB,
	ctx context.Context,
	slotLimit int,
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
		slotLimit,
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
		slotLimit,
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
	slotLimit int,
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
			VALUES ($1, $2, $3, $4, $5, $5)`,
			pauseID,
			draft.rosterID,
			participantID,
			slotLimit,
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
	return disconnectArenaParticipantAtInterval(
		t,
		ctx,
		fixture,
		participantID,
		1,
		disconnectedAt,
		window,
	)
}

func disconnectArenaParticipantAtInterval(
	t testing.TB,
	ctx context.Context,
	fixture arenaReconnectMigrationFixture,
	participantID uuid.UUID,
	intervalNumber int,
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
		SELECT 1
		FROM arena_pauses
		WHERE id = $1
		FOR UPDATE`, fixture.gamePauseID)
	require.NoError(t, err)
	var presenceEpoch int64
	err = tx.QueryRow(ctx, `
		UPDATE arena_presence_states
		SET state = 'disconnected',
			presence_epoch = presence_epoch + 1,
			revision = revision + 1,
			disconnected_at = $3,
			updated_at = $3
		WHERE series_id = $1 AND participant_id = $2
		RETURNING presence_epoch`,
		fixture.draft.seriesID,
		participantID,
		disconnectedAt,
	).Scan(&presenceEpoch)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO arena_reconnect_intervals (
			id, pause_id, roster_id, series_id, game_attempt_id,
			participant_id, presence_epoch, interval_number,
			opened_at, deadline_at, created_at, updated_at
		)
		VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8,
			$9, $10, $9, $9
		)`,
		intervalID,
		fixture.gamePauseID,
		fixture.draft.rosterID,
		fixture.draft.seriesID,
		fixture.attemptID,
		participantID,
		presenceEpoch,
		intervalNumber,
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
				resumed_deadline = $2::TIMESTAMPTZ + INTERVAL '5 minutes',
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
