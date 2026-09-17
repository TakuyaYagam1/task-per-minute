//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type taskExposureMigrationSnapshot struct {
	receiptID           uuid.UUID
	assignmentID        uuid.UUID
	participantID       uuid.UUID
	taskID              uuid.UUID
	taskVersion         int32
	deliveredAt         time.Time
	receiptCreated      time.Time
	reservationID       uuid.UUID
	reservationTask     uuid.UUID
	reservationVersion  int32
	reservationState    string
	reservationRevision int64
	committedAt         time.Time
	disclosedAt         time.Time
	planID              uuid.UUID
	planRevisionID      uuid.UUID
	planState           string
	constraintGraph     string
	proofEvidence       string
	childBranchID       uuid.UUID
	childPlanID         uuid.UUID
	childCreatedAt      time.Time
}

func TestTaskExposureMigrationPreservesExistingEvidence(t *testing.T) {
	const migration17 int64 = 17
	const migration18 int64 = 18

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	pool, database := testkit.CreateIsolatedDatabase(ctx, t, sharedPool, "task_exposure_upgrade")
	previousPool := sharedPool
	defer func() { sharedPool = previousPool }()

	parallelDatabaseMigrationMu.Lock()
	applyErr := func() error {
		defer parallelDatabaseMigrationMu.Unlock()
		if err := goose.SetDialect("postgres"); err != nil {
			return err
		}
		return goose.UpToContext(ctx, database, migrationsDirAbs(), migration17)
	}()
	require.NoError(t, applyErr)

	sharedPool = pool
	fixture, plan := createCommittedReplayAuthorityFixture(ctx, t)
	assignmentID := uuid.New()
	assignmentTx, err := pool.Begin(ctx)
	require.NoError(t, err)
	require.NoError(t, insertReplayAuthorityAssignment(ctx, assignmentTx, fixture, plan, assignmentID))
	require.NoError(t, insertReplayAuthorityHead(ctx, assignmentTx, assignmentID))
	require.NoError(t, insertReplayAuthorityPool(ctx, assignmentTx, assignmentID))
	require.NoError(t, assignmentTx.Commit(ctx))

	var participantID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT id
		FROM participants
		WHERE roster_id = $1
		ORDER BY seed, id
		LIMIT 1`, fixture.rosterID).Scan(&participantID))

	receipt := loadTaskExposureMigrationReceipt(ctx, t, assignmentID, participantID)
	deliveredAt := fixture.createdAt.Add(4 * time.Minute)
	disclosePrivateTaskReceiptReservation(ctx, t, receipt, deliveredAt)
	insertErr := insertPrivateTaskReceipt(
		ctx,
		receipt,
		participantID,
		domain.ParticipantTaskInstanceID(assignmentID, participantID),
		receipt.snapshotID,
		receipt.taskID,
		receipt.taskVersion,
		deliveredAt,
	)
	require.NoError(t, insertErr)

	childCreatedAt := deliveredAt.Add(time.Second)
	_, err = pool.Exec(ctx, `
		INSERT INTO exact_draft_assignment_child_history (
			child_branch_id, plan_id, participant_id, task_id, created_at
		)
		VALUES ($1, $2, $3, $4, $5)`,
		plan.branchID, plan.id, participantID, receipt.taskID, childCreatedAt)
	require.NoError(t, err)

	before := loadTaskExposureMigrationSnapshot(ctx, t, fixture, plan, assignmentID, participantID, childCreatedAt)

	parallelDatabaseMigrationMu.Lock()
	applyErr = func() error {
		defer parallelDatabaseMigrationMu.Unlock()
		return goose.UpToContext(ctx, database, migrationsDirAbs(), migration18)
	}()
	require.NoError(t, applyErr)

	var migrationVersion int64
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT version_id
		FROM goose_db_version
		WHERE is_applied
		ORDER BY version_id DESC
		LIMIT 1`).Scan(&migrationVersion))
	require.Equal(t, migration18, migrationVersion)

	var exposureTable string
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT to_regclass('public.task_public_exposures')::text`).Scan(&exposureTable))
	require.Equal(t, "task_public_exposures", exposureTable)

	after := loadTaskExposureMigrationSnapshot(ctx, t, fixture, plan, assignmentID, participantID, childCreatedAt)
	require.Equal(t, before, after)

	var scopedTournamentID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT tournament_id
		FROM task_version_reservations
		WHERE id = $1`, before.reservationID).Scan(&scopedTournamentID))
	require.Equal(t, fixture.tournamentID, scopedTournamentID)

	var childTaskVersion int32
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT task_version
		FROM exact_draft_assignment_child_history
		WHERE child_branch_id = $1 AND plan_id = $2 AND participant_id = $3 AND task_id = $4`,
		before.childBranchID, before.childPlanID, before.participantID, before.taskID).Scan(&childTaskVersion))
	require.Zero(t, childTaskVersion, "legacy child history rows retain version zero")

	var privateExposureCount int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM task_public_exposures
		WHERE task_id = $1 AND task_version = $2`, before.taskID, before.taskVersion).Scan(&privateExposureCount))
	require.Zero(t, privateExposureCount, "migration must not reinterpret private receipt evidence as public exposure")

	publicEvidenceID := uuid.New()
	disclosedAt := time.Now().UTC().Add(-time.Minute)
	publicTx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = publicTx.Rollback(ctx) }()
	_, err = publicTx.Exec(ctx, `
		INSERT INTO task_public_exposures (
			id, task_id, task_version, audience, evidence_id, disclosed_at
		)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		uuid.New(), before.taskID, before.taskVersion, "public", publicEvidenceID, disclosedAt)
	require.NoError(t, err)

	downResult := make(chan error, 1)
	downFinished := make(chan struct{})
	defer func() {
		_ = publicTx.Rollback(ctx)
		<-downFinished
	}()
	go func() {
		defer close(downFinished)
		parallelDatabaseMigrationMu.Lock()
		defer parallelDatabaseMigrationMu.Unlock()
		downResult <- goose.DownContext(ctx, database, migrationsDirAbs())
	}()
	require.Eventually(t, func() bool {
		var blocked bool
		err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM pg_stat_activity
				WHERE datname = current_database()
					AND $1 = ANY(pg_blocking_pids(pid))
			)`, int64(publicTx.Conn().PgConn().PID())).Scan(&blocked)
		return err == nil && blocked
	}, 3*time.Second, 10*time.Millisecond)
	require.NoError(t, publicTx.Commit(ctx))
	select {
	case downErr := <-downResult:
		require.ErrorContains(t, downErr, "cannot roll back task exposure evidence")
	case <-ctx.Done():
		t.Fatal("migration rollback did not finish after public disclosure committed")
	}

	var retainedVersion int64
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT version_id
		FROM goose_db_version
		WHERE is_applied
		ORDER BY version_id DESC
		LIMIT 1`).Scan(&retainedVersion))
	require.Equal(t, migration18, retainedVersion)

	var retainedExposureCount int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM task_public_exposures
		WHERE evidence_id = $1`, publicEvidenceID).Scan(&retainedExposureCount))
	require.Equal(t, 1, retainedExposureCount, "failed rollback must preserve public exposure evidence")
}

func loadTaskExposureMigrationReceipt(
	ctx context.Context,
	t *testing.T,
	assignmentID uuid.UUID,
	participantID uuid.UUID,
) privateTaskReceiptIdentity {
	t.Helper()
	var receipt privateTaskReceiptIdentity
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT assignment.id, assignment.attempt_id, assignment.roster_id,
			assignment.snapshot_id, assignment.task_id, assignment.task_version,
			assignment.reservation_id
		FROM assignments AS assignment
		WHERE assignment.id = $1`, assignmentID).Scan(
		&receipt.assignmentID, &receipt.attemptID, &receipt.rosterID,
		&receipt.snapshotID, &receipt.taskID, &receipt.taskVersion, &receipt.reservationID,
	))
	return receipt
}

func loadTaskExposureMigrationSnapshot(
	ctx context.Context,
	t *testing.T,
	fixture exactDraftReservationFixture,
	plan replayAuthorityPlanFixture,
	assignmentID uuid.UUID,
	participantID uuid.UUID,
	childCreatedAt time.Time,
) taskExposureMigrationSnapshot {
	t.Helper()
	var snapshot taskExposureMigrationSnapshot
	snapshot.participantID = participantID
	snapshot.childBranchID = plan.branchID
	snapshot.childPlanID = plan.id
	snapshot.childCreatedAt = childCreatedAt
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT receipt.id, receipt.assignment_id, receipt.participant_id,
			receipt.task_id, receipt.task_version, receipt.delivered_at, receipt.created_at
		FROM task_delivery_receipts AS receipt
		WHERE receipt.assignment_id = $1 AND receipt.participant_id = $2`,
		assignmentID, participantID).Scan(
		&snapshot.receiptID, &snapshot.assignmentID, &snapshot.participantID,
		&snapshot.taskID, &snapshot.taskVersion, &snapshot.deliveredAt, &snapshot.receiptCreated,
	))
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT reservation.id, reservation.task_id, reservation.task_version,
			reservation.state, reservation.revision, reservation.committed_at,
			reservation.disclosed_at
		FROM task_version_reservations AS reservation
		WHERE reservation.id = (
			SELECT assignment.reservation_id
			FROM assignments AS assignment
			WHERE assignment.id = $1
		)`, assignmentID).Scan(
		&snapshot.reservationID, &snapshot.reservationTask, &snapshot.reservationVersion,
		&snapshot.reservationState, &snapshot.reservationRevision, &snapshot.committedAt,
		&snapshot.disclosedAt,
	))
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT plan.id, plan.revision_id, plan.state,
			plan.constraint_graph::text, plan.proof_evidence::text
		FROM assignment_plans AS plan
		WHERE plan.id = $1`, plan.id).Scan(
		&snapshot.planID, &snapshot.planRevisionID, &snapshot.planState,
		&snapshot.constraintGraph, &snapshot.proofEvidence,
	))
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT child_branch_id, plan_id, created_at
		FROM exact_draft_assignment_child_history
		WHERE child_branch_id = $1 AND plan_id = $2 AND participant_id = $3 AND task_id = $4`,
		plan.branchID, plan.id, participantID, snapshot.taskID).Scan(
		&snapshot.childBranchID, &snapshot.childPlanID, &snapshot.childCreatedAt,
	))
	require.Equal(t, fixture.tournamentID, loadTaskExposureMigrationTournament(ctx, t, assignmentID))
	return snapshot
}

func loadTaskExposureMigrationTournament(ctx context.Context, t *testing.T, assignmentID uuid.UUID) uuid.UUID {
	t.Helper()
	var tournamentID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT plan.tournament_id
		FROM assignments AS assignment
		JOIN assignment_plans AS plan ON plan.id = assignment.plan_id
		WHERE assignment.id = $1`, assignmentID).Scan(&tournamentID))
	return tournamentID
}
