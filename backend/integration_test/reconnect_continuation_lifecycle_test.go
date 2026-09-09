//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/stretchr/testify/require"
)

func testReconnectContinuationLifecycle(t *testing.T) {
	ctx := context.Background()

	t.Run("continues a suspended interval without charging another slot", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture, sourceID, sourceDeadline, suspendedAt := createCancelledReconnectRoot(ctx, t, 1)
		assertNormalPauseIsSeparateFromGameChain(ctx, t, fixture)
		participantID := fixture.draft.participantIDs[0]
		before := loadReconnectIntervalSnapshot(ctx, t, sourceID)
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

		err := insertReconnectContinuation(ctx, fixture, reconnectContinuationInput{
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
			FROM reconnect_intervals
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
		require.Equal(t, before, loadReconnectIntervalSnapshot(ctx, t, sourceID))

		err = sharedPool.QueryRow(
			ctx, `
			SELECT slots_used, revision
			FROM reconnect_slot_counters
			WHERE pause_id = $1 AND participant_id = $2`,
			fixture.gamePauseID,
			participantID,
		).Scan(&slotsUsed, &counterRevision)
		require.NoError(t, err)
		require.Equal(t, 1, slotsUsed)
		require.EqualValues(t, 2, counterRevision)

		insertResumeDecision(
			ctx, t,
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
		mutate func(reconnectMigrationFixture, *reconnectContinuationInput)
	}{
		{
			name: "rejects reconnected continuation insertion",
			mutate: func(_ reconnectMigrationFixture, input *reconnectContinuationInput) {
				closedAt := input.openedAt.Add(time.Second)
				input.state = "reconnected"
				input.closedAt = &closedAt
				input.updatedAt = closedAt
			},
		},
		{
			name: "rejects expired continuation insertion",
			mutate: func(_ reconnectMigrationFixture, input *reconnectContinuationInput) {
				closedAt := input.deadlineAt
				input.state = "expired"
				input.closedAt = &closedAt
				input.updatedAt = closedAt
			},
		},
		{
			name: "rejects cancelled continuation insertion with provenance",
			mutate: func(fixture reconnectMigrationFixture, input *reconnectContinuationInput) {
				closedAt := input.openedAt.Add(time.Second)
				input.state = "cancelled"
				input.closedAt = &closedAt
				input.suspendedByPauseID = &fixture.gamePauseID
				input.updatedAt = closedAt
			},
		},
		{
			name: "rejects open continuation insertion with closed timestamp",
			mutate: func(_ reconnectMigrationFixture, input *reconnectContinuationInput) {
				closedAt := input.openedAt.Add(time.Second)
				input.closedAt = &closedAt
			},
		},
		{
			name: "rejects continuation insertion above revision one",
			mutate: func(_ reconnectMigrationFixture, input *reconnectContinuationInput) {
				input.revision = 2
			},
		},
		{
			name: "rejects continuation insertion with creation timestamp drift",
			mutate: func(_ reconnectMigrationFixture, input *reconnectContinuationInput) {
				input.createdAt = input.openedAt.Add(time.Second)
				input.updatedAt = input.createdAt
			},
		},
		{
			name: "rejects continuation insertion with update timestamp drift",
			mutate: func(_ reconnectMigrationFixture, input *reconnectContinuationInput) {
				input.updatedAt = input.openedAt.Add(time.Second)
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			resetMigrationTables(ctx, t)
			t.Cleanup(func() { resetMigrationTables(ctx, t) })

			fixture, sourceID, sourceDeadline, suspendedAt := createCancelledReconnectRoot(ctx, t, 1)
			openedAt := suspendedAt.Add(30 * time.Second)
			input := reconnectContinuationInput{
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

			err := insertReconnectContinuation(ctx, fixture, input)
			requireReconnectExactCheckViolation(
				t,
				err,
				"reconnect continuation must start as a canonical open segment",
			)
		})
	}

	t.Run("rejects a root insertion born with suspension provenance", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture := createReconnectMigrationFixtureWithSlotLimit(ctx, t, 1)
		participantID := fixture.draft.participantIDs[0]
		openedAt := fixture.pausedAt.Add(time.Second)
		disconnectPresence(ctx, t, fixture, participantID, openedAt)
		closedAt := openedAt.Add(time.Second)
		tx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()
		err = insertReconnectContinuationWith(
			ctx,
			tx,
			fixture,
			reconnectContinuationInput{
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
			_, err = tx.Exec(
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
		}
		if err == nil {
			err = tx.Commit(ctx)
		}
		requireReconnectExactCheckViolation(
			t,
			err,
			"reconnect interval insert cannot carry suspension provenance",
		)
	})

	t.Run("reconnects a continuation and charges the next fresh root slot", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture, sourceID, sourceDeadline, suspendedAt := createCancelledReconnectRoot(ctx, t, 2)
		participantID := fixture.draft.participantIDs[0]
		continuationID := uuid.New()
		openedAt := suspendedAt.Add(30 * time.Second)
		require.NoError(t, insertReconnectContinuation(
			ctx,
			fixture,
			reconnectContinuationInput{
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

		resumeNormalWavePause(ctx, t, fixture.normalPauseID, openedAt)
		reconnectedAt := openedAt.Add(10 * time.Second)
		reconnectParticipant(
			ctx, t,
			fixture,
			participantID,
			continuationID,
			reconnectedAt,
		)
		terminalContinuation := loadReconnectIntervalSnapshot(ctx, t, continuationID)
		require.Equal(t, "reconnected", terminalContinuation.state)
		require.True(t, terminalContinuation.closedAt.Valid)
		require.True(t, reconnectedAt.Equal(terminalContinuation.closedAt.Time))
		require.Equal(t, 1, terminalContinuation.continuationNumber)
		require.Equal(t, sourceID, uuid.UUID(terminalContinuation.continuedFromID.Bytes))
		freshOpenedAt := reconnectedAt.Add(10 * time.Second)
		freshRootID, _ := disconnectParticipantAtInterval(
			ctx, t,
			fixture,
			participantID,
			2,
			freshOpenedAt,
			2*time.Minute,
		)
		freshRoot := loadReconnectIntervalSnapshot(ctx, t, freshRootID)
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
		err := sharedPool.QueryRow(
			ctx, `
			SELECT slots_used, revision
			FROM reconnect_slot_counters
			WHERE pause_id = $1 AND participant_id = $2`,
			fixture.gamePauseID,
			participantID,
		).Scan(&slotsUsed, &counterRevision)
		require.NoError(t, err)
		require.Equal(t, 2, slotsUsed)
		require.EqualValues(t, 3, counterRevision)

		insertResumeDecision(
			ctx, t,
			fixture,
			fixture.gamePauseID,
			1,
			&freshRootID,
			nil,
			"wait_first",
			freshOpenedAt,
		)
		var decisionIntervalID uuid.UUID
		err = sharedPool.QueryRow(
			ctx, `
			SELECT first_reconnect_interval_id
			FROM resume_decisions
			WHERE pause_id = $1 AND decision_number = 1`,
			fixture.gamePauseID,
		).Scan(&decisionIntervalID)
		require.NoError(t, err)
		require.Equal(t, freshRootID, decisionIntervalID)
	})

	t.Run("retains repeatable suspension provenance across two continuations", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture, segment0ID, segment0Deadline, suspendedAtA := createCancelledReconnectRoot(ctx, t, 2)
		participantID := fixture.draft.participantIDs[0]
		segment1ID := uuid.New()
		segment1OpenedAt := suspendedAtA.Add(30 * time.Second)
		segment1Deadline := segment1OpenedAt.Add(segment0Deadline.Sub(suspendedAtA))
		require.NoError(t, insertReconnectContinuation(ctx, fixture, reconnectContinuationInput{
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
		resumeNormalWavePause(ctx, t, fixture.normalPauseID, segment1OpenedAt)

		suspendedAtB := segment1OpenedAt.Add(20 * time.Second)
		waveBID := createCoveringWave(ctx, t, fixture, segment1OpenedAt)
		pauseBID := createNormalWavePauseAndSuspendInterval(
			ctx, t,
			fixture,
			waveBID,
			segment1ID,
			suspendedAtB,
		)
		segment2ID := uuid.New()
		segment2OpenedAt := suspendedAtB.Add(30 * time.Second)
		segment2Deadline := segment2OpenedAt.Add(segment1Deadline.Sub(suspendedAtB))
		require.NoError(t, insertReconnectContinuation(ctx, fixture, reconnectContinuationInput{
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

		segment0 := loadReconnectIntervalSnapshot(ctx, t, segment0ID)
		segment1 := loadReconnectIntervalSnapshot(ctx, t, segment1ID)
		segment2 := loadReconnectIntervalSnapshot(ctx, t, segment2ID)
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
		err := sharedPool.QueryRow(
			ctx, `
			SELECT slots_used
			FROM reconnect_slot_counters
			WHERE pause_id = $1 AND participant_id = $2`,
			fixture.gamePauseID,
			participantID,
		).Scan(&slotsUsed)
		require.NoError(t, err)
		require.Equal(t, 1, slotsUsed)

		_, err = sharedPool.Exec(ctx, `
			UPDATE reconnect_intervals
			SET suspended_by_pause_id = $2,
				revision = revision + 1,
				updated_at = $3
			WHERE id = $1`, segment0ID, pauseBID, segment2OpenedAt)
		requireReconnectCheckViolation(
			t,
			err,
			"reconnect suspension provenance is immutable",
		)
		require.Equal(t, segment0, loadReconnectIntervalSnapshot(ctx, t, segment0ID))
		require.Equal(t, segment1, loadReconnectIntervalSnapshot(ctx, t, segment1ID))
	})
}
