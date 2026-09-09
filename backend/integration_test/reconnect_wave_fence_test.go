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

func TestReconnectContinuationWaveMembershipFence(t *testing.T) {
	ctx := context.Background()

	t.Run("concurrent Wave creation does not deadlock on the roster", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture := createReconnectMigrationFixtureWithSlotLimit(ctx, t, 1)
		createdAt := fixture.pausedAt.Add(time.Second)
		firstWaveID := uuid.New()
		secondWaveID := uuid.New()
		firstTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = firstTx.Rollback(ctx) }()
		require.NoError(t, setReconnectDeadlockTimeouts(ctx, firstTx))
		secondTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = secondTx.Rollback(ctx) }()
		require.NoError(t, setReconnectDeadlockTimeouts(ctx, secondTx))

		for _, wave := range []struct {
			tx     pgx.Tx
			waveID uuid.UUID
		}{
			{tx: firstTx, waveID: firstWaveID},
			{tx: secondTx, waveID: secondWaveID},
		} {
			_, err = wave.tx.Exec(ctx, `
				INSERT INTO waves (
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
				INSERT INTO wave_members (
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
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture := createReconnectMigrationFixtureWithSlotLimit(ctx, t, 1)
		firstParticipantID := fixture.draft.participantIDs[0]
		lateParticipantID := fixture.draft.participantIDs[1]
		disconnectParticipant(
			ctx, t, fixture, lateParticipantID,
			fixture.pausedAt.Add(time.Second), 2*time.Minute,
		)
		waveID := createWaveWithMembers(
			ctx, t, fixture, []uuid.UUID{firstParticipantID}, fixture.pausedAt,
		)
		suspendedAt := fixture.pausedAt.Add(20 * time.Second)
		pauseTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = pauseTx.Rollback(ctx) }()
		require.NoError(t, setReconnectDeadlockTimeouts(ctx, pauseTx))
		require.NoError(t, insertNormalWavePauseEvidenceForParticipants(
			ctx, pauseTx, fixture, waveID, uuid.New(), uuid.New(), suspendedAt,
			[]uuid.UUID{firstParticipantID},
		))
		_, err = pauseTx.Exec(ctx, "SET CONSTRAINTS ALL IMMEDIATE")
		require.NoError(t, err)

		memberTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = memberTx.Rollback(ctx) }()
		require.NoError(t, setReconnectDeadlockTimeouts(ctx, memberTx))
		var memberPID int
		require.NoError(t, memberTx.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&memberPID))
		memberResult := make(chan error, 1)
		go func() {
			_, memberErr := memberTx.Exec(ctx, `
				INSERT INTO wave_members (
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
		waitReconnectBackendLock(ctx, t, memberPID)
		require.NoError(t, pauseTx.Commit(ctx))
		requireReconnectExactCheckViolation(
			t,
			<-memberResult,
			"Wave membership is immutable after normal pause history",
		)
	})

	t.Run("normal Wave pause rechecks membership committed first", func(t *testing.T) {
		resetMigrationTables(ctx, t)
		t.Cleanup(func() { resetMigrationTables(ctx, t) })

		fixture := createReconnectMigrationFixtureWithSlotLimit(ctx, t, 1)
		firstParticipantID := fixture.draft.participantIDs[0]
		lateParticipantID := fixture.draft.participantIDs[1]
		disconnectParticipant(
			ctx, t, fixture, lateParticipantID,
			fixture.pausedAt.Add(time.Second), 2*time.Minute,
		)
		waveID := createWaveWithMembers(
			ctx, t, fixture, []uuid.UUID{firstParticipantID}, fixture.pausedAt,
		)
		suspendedAt := fixture.pausedAt.Add(20 * time.Second)

		memberTx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = memberTx.Rollback(ctx) }()
		require.NoError(t, setReconnectDeadlockTimeouts(ctx, memberTx))
		_, err = memberTx.Exec(ctx, `
			INSERT INTO wave_members (
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
		require.NoError(t, setReconnectDeadlockTimeouts(ctx, pauseTx))
		var pausePID int
		require.NoError(t, pauseTx.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pausePID))
		pauseResult := make(chan error, 1)
		go func() {
			pauseErr := insertNormalWavePauseEvidenceForParticipants(
				ctx, pauseTx, fixture, waveID, uuid.New(), uuid.New(), suspendedAt,
				[]uuid.UUID{firstParticipantID},
			)
			if pauseErr == nil {
				_, pauseErr = pauseTx.Exec(
					ctx,
					"SET CONSTRAINTS normal_wave_reconnect_suspension IMMEDIATE",
				)
			}
			pauseResult <- pauseErr
		}()
		waitReconnectBackendLock(ctx, t, pausePID)
		require.NoError(t, memberTx.Commit(ctx))
		requireReconnectExactCheckViolation(
			t,
			<-pauseResult,
			"normal Wave pause requires exact membership snapshot coverage",
		)
	})
}
