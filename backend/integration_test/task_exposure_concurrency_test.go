//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestPublicExposureSerializesWithPrivateDelivery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	TruncateTables(t, sharedPool)
	t.Cleanup(func() { TruncateTables(t, sharedPool) })

	fixture := createResultAuditMigrationFixture(ctx, t)
	receipt := loadPrivateTaskReceiptIdentity(ctx, t, fixture)
	deliveredAt := fixture.lockedAt.Add(time.Second)
	disclosePrivateTaskReceiptReservation(ctx, t, receipt, deliveredAt)

	publicTx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = publicTx.Rollback(ctx) }()
	_, err = sqlc.New(publicTx).RecordTaskPublicExposure(ctx, sqlc.RecordTaskPublicExposureParams{
		ID: uuid.New(), TaskID: receipt.taskID, TaskVersion: receipt.taskVersion,
		Audience: "spectator", EvidenceID: uuid.New(),
		DisclosedAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	})
	require.NoError(t, err)

	deliveryTx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = deliveryTx.Rollback(ctx) }()
	var deliveryPID int
	require.NoError(t, deliveryTx.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&deliveryPID))
	participantID := fixture.draft.participantIDs[0]
	deliveryResult := make(chan error, 1)
	deliveryFinished := make(chan struct{})
	defer func() {
		_ = publicTx.Rollback(ctx)
		<-deliveryFinished
	}()
	go func() {
		defer close(deliveryFinished)
		_, insertErr := sqlc.New(deliveryTx).CreateAssignmentTaskDeliveryReceipt(ctx, sqlc.CreateAssignmentTaskDeliveryReceiptParams{
			ID: uuid.New(), AssignmentID: receipt.assignmentID, AttemptID: receipt.attemptID,
			RosterID: receipt.rosterID, ParticipantID: participantID,
			InstanceID: domain.ParticipantTaskInstanceID(receipt.assignmentID, participantID),
			SnapshotID: receipt.snapshotID, TaskID: receipt.taskID, TaskVersion: receipt.taskVersion,
			DeliveredAt: pgtype.Timestamptz{Time: deliveredAt, Valid: true},
		})
		deliveryResult <- insertErr
	}()

	// The delivery statement starts before the exposure commits. Its guard
	// must wait for the same version and then read a fresh exposure snapshot.
	waitReconnectBackendLock(ctx, t, deliveryPID)
	require.NoError(t, publicTx.Commit(ctx))
	select {
	case err = <-deliveryResult:
		require.ErrorContains(t, err, "publicly exposed task versions cannot be delivered privately")
	case <-ctx.Done():
		t.Fatal("private delivery did not finish after public disclosure committed")
	}
	require.NoError(t, deliveryTx.Rollback(ctx))
	var receiptCount int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM task_delivery_receipts WHERE assignment_id = $1`, receipt.assignmentID).Scan(&receiptCount))
	require.Zero(t, receiptCount)
}
