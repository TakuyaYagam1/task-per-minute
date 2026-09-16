//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	taskrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/task"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestPrivateTaskAvailabilityBacklogCoversActiveTournamentGraphs(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	fixture := createResultAuditMigrationFixture(ctx, t)
	startedAt := fixture.lockedAt.Add(time.Second)
	activatePrivateTaskAvailabilityGraph(ctx, t, fixture, startedAt)

	repository := taskrepo.NewPrivateTaskAvailabilityPostgres(postgres.NewTxManager(sharedPool))
	for _, tournamentState := range []string{"swiss", "golden", "playoffs"} {
		_, err := sharedPool.Exec(ctx, `
			UPDATE tournaments
			SET state = $2,
				revision = revision + 1,
				updated_at = $3
			WHERE id = $1`, fixture.draft.tournamentID, tournamentState, startedAt)
		require.NoError(t, err)

		backlog, err := repository.TaskDeliveryBacklog(ctx)
		require.NoError(t, err)
		require.EqualValues(t, 2, backlog.PendingCount)
		require.NotNil(t, backlog.OldestPendingAt)
		require.Equal(t, startedAt, backlog.OldestPendingAt.UTC())
	}

	createPrivateTaskReceipts(ctx, t, fixture, startedAt)
	backlog, err := repository.TaskDeliveryBacklog(ctx)
	require.NoError(t, err)
	require.Zero(t, backlog.PendingCount)
	require.Nil(t, backlog.OldestPendingAt)
}

func TestPrivateTaskAvailabilityBacklogRejectsMismatchedReceiptIdentity(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	fixture := createResultAuditMigrationFixture(ctx, t)
	startedAt := fixture.lockedAt.Add(time.Second)
	activatePrivateTaskAvailabilityGraph(ctx, t, fixture, startedAt)
	receipt := loadPrivateTaskReceiptIdentity(ctx, t, fixture)
	disclosePrivateTaskReceiptReservation(ctx, t, receipt, startedAt)

	var databaseInstanceID uuid.UUID
	err := sharedPool.QueryRow(
		ctx,
		`SELECT public.participant_task_instance_id($1, $2)`,
		receipt.assignmentID,
		fixture.draft.participantIDs[0],
	).Scan(&databaseInstanceID)
	require.NoError(t, err)
	require.Equal(
		t,
		domain.ParticipantTaskInstanceID(receipt.assignmentID, fixture.draft.participantIDs[0]),
		databaseInstanceID,
	)

	var alternate struct {
		snapshotID  uuid.UUID
		taskID      uuid.UUID
		taskVersion int32
	}
	err = sharedPool.QueryRow(ctx, `
		SELECT snapshot.id, snapshot.task_id, snapshot.task_version
		FROM task_snapshots AS snapshot
		WHERE snapshot.id <> $1
		ORDER BY snapshot.id
		LIMIT 1`, receipt.snapshotID).Scan(
		&alternate.snapshotID,
		&alternate.taskID,
		&alternate.taskVersion,
	)
	require.NoError(t, err)

	err = insertPrivateTaskReceipt(
		ctx,
		receipt,
		fixture.draft.participantIDs[0],
		databaseInstanceID,
		alternate.snapshotID,
		alternate.taskID,
		alternate.taskVersion,
		startedAt,
	)
	require.ErrorContains(t, err, "delivery receipt must match assignment and deterministic instance identity")

	wrongInstanceID := uuid.New()
	require.NotEqual(t, databaseInstanceID, wrongInstanceID)
	err = insertPrivateTaskReceipt(
		ctx,
		receipt,
		fixture.draft.participantIDs[0],
		wrongInstanceID,
		receipt.snapshotID,
		receipt.taskID,
		receipt.taskVersion,
		startedAt,
	)
	require.ErrorContains(t, err, "delivery receipt must match assignment and deterministic instance identity")

	backlog, err := sqlc.New(sharedPool).GetPrivateTaskDeliveryBacklog(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 2, backlog.PendingCount)
}

func activatePrivateTaskAvailabilityGraph(
	ctx context.Context,
	tb testing.TB,
	fixture resultAuditMigrationFixture,
	startedAt time.Time,
) {
	tb.Helper()

	_, err := sharedPool.Exec(ctx, `
		UPDATE tournaments
		SET state = 'swiss',
			started_at = $2,
			revision = revision + 1,
			updated_at = $2
		WHERE id = $1`, fixture.draft.tournamentID, startedAt)
	require.NoError(tb, err)

	waveID, _ := createMigrationWave(
		ctx,
		tb,
		fixture.draft.tournamentID,
		fixture.draft.rosterID,
		fixture.draft.participantIDs,
		startedAt.Add(-3*time.Second),
	)
	windowID, _ := openMigrationReadyWindow(
		ctx,
		tb,
		waveID,
		fixture.draft.rosterID,
		startedAt.Add(-2*time.Second),
		startedAt.Add(time.Minute),
	)
	for _, participantID := range fixture.draft.participantIDs {
		markMigrationReady(
			ctx,
			tb,
			windowID,
			waveID,
			fixture.draft.rosterID,
			participantID,
			startedAt.Add(-time.Second),
		)
	}
	_, err = sharedPool.Exec(ctx, `
		UPDATE waves
		SET state = 'ready',
			revision = revision + 1,
			updated_at = $2
		WHERE id = $1`, waveID, startedAt.Add(-time.Second))
	require.NoError(tb, err)
	require.NoError(tb, startMigrationWave(ctx, waveID, windowID, startedAt))
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO wave_series (wave_id, tournament_id, roster_id, series_id, created_at)
		VALUES ($1, $2, $3, $4, $5)`,
		waveID,
		fixture.draft.tournamentID,
		fixture.draft.rosterID,
		fixture.draft.seriesID,
		startedAt,
	)
	require.NoError(tb, err)
	_, err = sharedPool.Exec(ctx, `
		UPDATE series
		SET state = 'ready',
			revision = revision + 1,
			updated_at = $2
		WHERE id = $1`, fixture.draft.seriesID, startedAt.Add(-time.Second))
	require.NoError(tb, err)
	_, err = sharedPool.Exec(ctx, `
		UPDATE series
		SET state = 'active',
			started_at = $2,
			revision = revision + 1,
			updated_at = $2
		WHERE id = $1`, fixture.draft.seriesID, startedAt)
	require.NoError(tb, err)
}

type privateTaskReceiptIdentity struct {
	assignmentID  uuid.UUID
	attemptID     uuid.UUID
	rosterID      uuid.UUID
	snapshotID    uuid.UUID
	taskID        uuid.UUID
	taskVersion   int32
	reservationID uuid.UUID
}

func createPrivateTaskReceipts(
	ctx context.Context,
	tb testing.TB,
	fixture resultAuditMigrationFixture,
	deliveredAt time.Time,
) {
	tb.Helper()

	receipt := loadPrivateTaskReceiptIdentity(ctx, tb, fixture)
	disclosePrivateTaskReceiptReservation(ctx, tb, receipt, deliveredAt)
	for _, participantID := range fixture.draft.participantIDs {
		err := insertPrivateTaskReceipt(
			ctx,
			receipt,
			participantID,
			domain.ParticipantTaskInstanceID(receipt.assignmentID, participantID),
			receipt.snapshotID,
			receipt.taskID,
			receipt.taskVersion,
			deliveredAt,
		)
		require.NoError(tb, err)
	}
}

func loadPrivateTaskReceiptIdentity(
	ctx context.Context,
	tb testing.TB,
	fixture resultAuditMigrationFixture,
) privateTaskReceiptIdentity {
	tb.Helper()

	var receipt privateTaskReceiptIdentity
	err := sharedPool.QueryRow(ctx, `
		SELECT assignment.id,
			assignment.attempt_id,
			assignment.roster_id,
			assignment.snapshot_id,
			assignment.task_id,
			assignment.task_version,
			assignment.reservation_id
		FROM assignments AS assignment
		WHERE assignment.id = $1`, fixture.assignmentID).Scan(
		&receipt.assignmentID,
		&receipt.attemptID,
		&receipt.rosterID,
		&receipt.snapshotID,
		&receipt.taskID,
		&receipt.taskVersion,
		&receipt.reservationID,
	)
	require.NoError(tb, err)
	return receipt
}

func disclosePrivateTaskReceiptReservation(
	ctx context.Context,
	tb testing.TB,
	receipt privateTaskReceiptIdentity,
	deliveredAt time.Time,
) {
	tb.Helper()

	_, err := sharedPool.Exec(ctx, `
		UPDATE task_version_reservations
		SET disclosed_at = $2,
			revision = revision + 1
		WHERE id = $1
			AND state = 'committed'
			AND disclosed_at IS NULL`, receipt.reservationID, deliveredAt)
	require.NoError(tb, err)
}

func insertPrivateTaskReceipt(
	ctx context.Context,
	receipt privateTaskReceiptIdentity,
	participantID uuid.UUID,
	instanceID uuid.UUID,
	snapshotID uuid.UUID,
	taskID uuid.UUID,
	taskVersion int32,
	deliveredAt time.Time,
) error {
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO task_delivery_receipts (
			assignment_id, attempt_id, roster_id, participant_id, instance_id,
			snapshot_id, task_id, task_version, delivered_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)`,
		receipt.assignmentID,
		receipt.attemptID,
		receipt.rosterID,
		participantID,
		instanceID,
		snapshotID,
		taskID,
		taskVersion,
		deliveredAt,
	)
	return err
}
