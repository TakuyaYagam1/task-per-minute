//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit"
	"github.com/TakuyaYagam1/task-per-minute/internal/bootstrap"
)

type goldenSnapshotParticipantReservation struct {
	PlanID        uuid.UUID
	TournamentID  uuid.UUID
	RosterID      uuid.UUID
	ParticipantID uuid.UUID
	PlayerID      uuid.UUID
	ReservationID uuid.UUID
	Revision      int64
	AcquiredAt    time.Time
	UpdatedAt     time.Time
	CreatedAt     time.Time
}

func TestGoldenPlanSnapshotMigrationPreservesHistoricalEvidence(t *testing.T) {
	t.Run("migration_28", func(t *testing.T) {
		testGoldenPlanSnapshotMigrationPreservesHistoricalEvidence(t, true)
	})
	t.Run("current_schema", func(t *testing.T) {
		testGoldenPlanSnapshotMigrationPreservesHistoricalEvidence(t, false)
	})
}

func testGoldenPlanSnapshotMigrationPreservesHistoricalEvidence(t *testing.T, historical bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	parallelDatabaseMigrationMu.Lock()
	defer parallelDatabaseMigrationMu.Unlock()

	pool, database := testkit.CreateIsolatedDatabase(ctx, t, sharedPool, "golden_snapshot_upgrade")
	migrationsDir := bootstrap.ResolveMigrationsDir("db/migrations")
	if historical {
		migrationsDir = testkit.MigrationsThrough(t, migrationsDir, 28)
	}
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.UpToContext(ctx, database, migrationsDir, 27))

	previousPool := sharedPool
	sharedPool = pool
	t.Cleanup(func() { sharedPool = previousPool })

	fixture := createGoldenExactPlanAuthorityFixture(ctx, t)
	planID := createSealedGoldenExactPlan(ctx, t, fixture)
	before := readGoldenSnapshotParticipantReservations(ctx, t, pool, fixture.golden.tournamentID)
	require.Len(t, before, len(fixture.golden.participantIDs))

	commandTag, err := pool.Exec(ctx, `
		UPDATE participant_reservations
		SET revision = revision + 1,
		    updated_at = updated_at + interval '1 second'
		WHERE tournament_id = $1`, fixture.golden.tournamentID)
	require.NoError(t, err)
	require.Equal(t, int64(len(before)), commandTag.RowsAffected())

	sealBefore := readGoldenSnapshotSeal(ctx, t, pool, planID)
	migrator := bootstrap.NewMigrator(testkit.MigrationDSN(t, pool, "public"), migrationsDir)
	require.NoError(t, migrator.Up(ctx))

	after := readGoldenSnapshotParticipantReservations(ctx, t, pool, fixture.golden.tournamentID)
	require.Equal(t, before, after, "migration must leave captured reservation history unchanged")
	require.Equal(t, sealBefore, readGoldenSnapshotSeal(ctx, t, pool, planID))

	var liveRowsDiffer int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM golden_exact_plan_snapshot_participant_reservations AS snapshot
		JOIN participant_reservations AS live
			ON live.player_id = snapshot.player_id
			AND live.tournament_id = snapshot.tournament_id
		WHERE snapshot.plan_id = $1
			AND (live.revision <> snapshot.revision OR live.updated_at <> snapshot.updated_at)`, planID,
	).Scan(&liveRowsDiffer))
	require.Equal(t, len(before), liveRowsDiffer,
		"upgrade must validate stable identity without requiring current reservation metadata to equal historical capture")

	commandTag, err = pool.Exec(ctx, `
		DELETE FROM participant_reservations
		WHERE tournament_id = $1`, fixture.golden.tournamentID)
	require.NoError(t, err)
	require.Equal(t, int64(len(before)), commandTag.RowsAffected(),
		"released live reservations must not erase Golden snapshot history")

	if !historical {
		return
	}
	// Exercise migration 28's guard without crossing later irreversible migrations.
	err = migrator.Down(ctx)
	require.ErrorContains(t, err, "cannot roll back while Golden snapshot reservation history differs")
	require.Equal(t, before, readGoldenSnapshotParticipantReservations(ctx, t, pool, fixture.golden.tournamentID),
		"failed Down must retain historical reservation evidence")
	require.Equal(t, sealBefore, readGoldenSnapshotSeal(ctx, t, pool, planID),
		"failed Down must retain the original seal")

	var appliedVersion int64
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT version_id
		FROM goose_db_version
		WHERE is_applied
		ORDER BY version_id DESC
		LIMIT 1`).Scan(&appliedVersion))
	require.EqualValues(t, 28, appliedVersion, "failed Down must leave migration 28 applied")

	var identityConstraint, triggerPresent, oldLockConstraint bool
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT
			EXISTS (
				SELECT 1 FROM pg_constraint
				WHERE conrelid = 'public.golden_exact_plan_snapshot_participant_reservations'::regclass
					AND conname = 'golden_plan_snapshot_participant_reservation_identity_fk'
			),
			EXISTS (
				SELECT 1 FROM pg_trigger
				WHERE tgrelid = 'public.golden_exact_plan_snapshot_participant_reservations'::regclass
					AND tgname = 'golden_snapshot_participant_reservation_live_guard'
					AND NOT tgisinternal
			),
			EXISTS (
				SELECT 1 FROM pg_constraint
				WHERE conrelid = 'public.golden_exact_plan_snapshot_participant_reservations'::regclass
					AND conname = 'golden_plan_snapshot_participant_reservation_lock_fk'
			)`).Scan(&identityConstraint, &triggerPresent, &oldLockConstraint))
	require.True(t, identityConstraint, "failed Down must retain the stable identity guard")
	require.True(t, triggerPresent, "failed Down must retain the live capture guard")
	require.False(t, oldLockConstraint, "failed Down must not partially restore the ephemeral lock FK")
}

func TestGoldenPlanSnapshotMigrationRejectsInvalidReservationCaptures(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	parallelDatabaseMigrationMu.Lock()
	defer parallelDatabaseMigrationMu.Unlock()

	pool, database := testkit.CreateIsolatedDatabase(ctx, t, sharedPool, "golden_snapshot_capture_guard")
	migrationsDir := bootstrap.ResolveMigrationsDir("db/migrations")
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.UpToContext(ctx, database, migrationsDir, 28))

	previousPool := sharedPool
	sharedPool = pool
	t.Cleanup(func() { sharedPool = previousPool })

	fixture := createGoldenExactPlanAuthorityFixture(ctx, t)
	createSealedGoldenExactPlan(ctx, t, fixture)
	validCaptures := readGoldenSnapshotParticipantReservations(ctx, t, pool, fixture.golden.tournamentID)
	require.Len(t, validCaptures, len(fixture.golden.participantIDs))

	byParticipant := make(map[uuid.UUID]goldenSnapshotParticipantReservation, len(validCaptures))
	for _, capture := range validCaptures {
		byParticipant[capture.ParticipantID] = capture
	}
	first := byParticipant[fixture.golden.participantIDs[0]]
	second := byParticipant[fixture.golden.participantIDs[1]]
	require.NotEqual(t, first.PlayerID, second.PlayerID)

	wrongTuple := first
	wrongTuple.Revision++

	missingTuple := first
	missingTuple.ReservationID = uuid.New()

	crossWiredIdentity := first
	crossWiredIdentity.PlayerID = second.PlayerID
	crossWiredIdentity.ReservationID = second.ReservationID
	crossWiredIdentity.Revision = second.Revision
	crossWiredIdentity.AcquiredAt = second.AcquiredAt
	crossWiredIdentity.UpdatedAt = second.UpdatedAt

	for _, testCase := range []struct {
		name           string
		capture        goldenSnapshotParticipantReservation
		constraintName string
	}{
		{
			name:           "wrong live tuple",
			capture:        wrongTuple,
			constraintName: "golden_snapshot_participant_reservation_live_guard",
		},
		{
			name:           "missing live tuple",
			capture:        missingTuple,
			constraintName: "golden_snapshot_participant_reservation_live_guard",
		},
		{
			name:           "cross-wired participant identity",
			capture:        crossWiredIdentity,
			constraintName: "golden_plan_snapshot_participant_reservation_identity_fk",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			tx, err := pool.Begin(ctx)
			require.NoError(t, err)
			defer func() { _ = tx.Rollback(ctx) }()

			capture := testCase.capture
			capture.PlanID = insertGoldenExactPlanRoot(ctx, t, tx, fixture)
			err = insertGoldenSnapshotParticipantReservation(ctx, tx, capture)
			require.Error(t, err)

			var databaseError *pgconn.PgError
			require.ErrorAs(t, err, &databaseError)
			require.Equal(t, "23503", databaseError.Code)
			require.Equal(t, testCase.constraintName, databaseError.ConstraintName)
			require.NoError(t, tx.Rollback(ctx))
		})
	}
}

func TestGoldenPlanSnapshotMigrationCaptureBlocksReservationRelease(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	parallelDatabaseMigrationMu.Lock()
	defer parallelDatabaseMigrationMu.Unlock()

	pool, database := testkit.CreateIsolatedDatabase(ctx, t, sharedPool, "golden_snapshot_capture_lock")
	migrationsDir := bootstrap.ResolveMigrationsDir("db/migrations")
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.UpToContext(ctx, database, migrationsDir, 28))

	previousPool := sharedPool
	sharedPool = pool
	t.Cleanup(func() { sharedPool = previousPool })

	fixture := createGoldenExactPlanAuthorityFixture(ctx, t)
	liveCaptures := make(map[uuid.UUID]goldenSnapshotParticipantReservation, len(fixture.golden.participantIDs))
	for _, participantID := range fixture.golden.participantIDs {
		liveCaptures[participantID] = createLiveGoldenSnapshotParticipantReservation(ctx, t, pool, fixture, participantID)
	}
	target := liveCaptures[fixture.golden.participantIDs[0]]

	releaseConn, err := pool.Acquire(ctx)
	require.NoError(t, err)
	defer releaseConn.Release()
	var releasePID int32
	require.NoError(t, releaseConn.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&releasePID))

	captureTx, err := pool.Begin(ctx)
	require.NoError(t, err)
	captureCommitted := false
	releaseStarted := false
	releaseReceived := false
	releaseDone := make(chan error, 1)
	releaseCtx, releaseCancel := context.WithTimeout(ctx, 10*time.Second)
	defer func() {
		if !captureCommitted {
			rollbackCtx, cancelRollback := context.WithTimeout(context.Background(), 2*time.Second)
			_ = captureTx.Rollback(rollbackCtx)
			cancelRollback()
		}
		releaseCancel()
		if releaseStarted && !releaseReceived {
			cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancelCleanup()
			select {
			case <-releaseDone:
			case <-cleanupCtx.Done():
				t.Errorf("reservation release goroutine did not exit during cleanup")
			}
		}
	}()

	var capturePID int32
	require.NoError(t, captureTx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&capturePID))
	planID := insertGoldenExactPlanRoot(ctx, t, captureTx, fixture)
	insertGoldenExactPlanGroup(ctx, t, captureTx, fixture, planID)
	insertGoldenExactPlanMembers(ctx, t, captureTx, fixture, planID)
	insertGoldenExactPlanEdges(ctx, t, captureTx, fixture, planID)
	insertGoldenExactPlanCandidates(ctx, t, captureTx, fixture, planID)
	insertGoldenExactPlanReservations(ctx, t, captureTx, fixture, planID)
	for _, participantID := range fixture.golden.participantIDs {
		capture := liveCaptures[participantID]
		capture.PlanID = planID
		require.NoError(t, insertGoldenSnapshotParticipantReservation(ctx, captureTx, capture))
	}
	sealGoldenExactPlan(ctx, t, captureTx, fixture, planID)

	releaseStarted = true
	go func() {
		_, deleteErr := releaseConn.Exec(releaseCtx, `
			DELETE FROM participant_reservations
			WHERE player_id = $1 AND tournament_id = $2`, target.PlayerID, target.TournamentID)
		releaseDone <- deleteErr
	}()

	lockWaitCtx, cancelLockWait := context.WithTimeout(ctx, 4*time.Second)
	var waitingForCapture bool
	var lockQueryErr error
	require.Eventually(t, func() bool {
		lockQueryErr = pool.QueryRow(lockWaitCtx, `
			SELECT EXISTS (
				SELECT 1 FROM pg_locks
				WHERE pid = $1 AND NOT granted
			) AND $2 = ANY(pg_blocking_pids($1))`, releasePID, capturePID,
		).Scan(&waitingForCapture)
		return lockQueryErr != nil || waitingForCapture
	}, 4*time.Second, 10*time.Millisecond, "release must wait on the capture transaction's row lock")
	cancelLockWait()
	require.NoError(t, lockQueryErr)
	require.True(t, waitingForCapture, "pg_locks must show the capture transaction blocking release")
	select {
	case deleteErr := <-releaseDone:
		releaseReceived = true
		require.Failf(t, "reservation release completed before capture commit", "%v", deleteErr)
	default:
	}

	require.NoError(t, captureTx.Commit(ctx))
	captureCommitted = true
	select {
	case deleteErr := <-releaseDone:
		releaseReceived = true
		require.NoError(t, deleteErr, "release must proceed after the capture transaction commits")
	case <-releaseCtx.Done():
		t.Fatalf("release remained blocked after capture commit: %v", releaseCtx.Err())
	}

	var liveReservationExists bool
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM participant_reservations
			WHERE player_id = $1 AND tournament_id = $2
		)`, target.PlayerID, target.TournamentID).Scan(&liveReservationExists))
	require.False(t, liveReservationExists)
	require.Len(t, readGoldenSnapshotParticipantReservations(ctx, t, pool, target.TournamentID),
		len(fixture.golden.participantIDs), "reservation release must preserve the inserted snapshot history")
}

func createLiveGoldenSnapshotParticipantReservation(
	ctx context.Context,
	t testing.TB,
	pool *pgxpool.Pool,
	fixture goldenExactPlanAuthorityFixture,
	participantID uuid.UUID,
) goldenSnapshotParticipantReservation {
	t.Helper()
	var capture goldenSnapshotParticipantReservation
	capture.TournamentID = fixture.golden.tournamentID
	capture.RosterID = fixture.golden.rosterID
	capture.ParticipantID = participantID
	capture.CreatedAt = fixture.golden.createdAt.Add(time.Minute)
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT player_id
		FROM participants
		WHERE roster_id = $1 AND id = $2`, capture.RosterID, participantID).Scan(&capture.PlayerID))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO participant_reservations (player_id, tournament_id)
		VALUES ($1, $2)
		RETURNING reservation_id, revision, acquired_at, updated_at`,
		capture.PlayerID, capture.TournamentID,
	).Scan(&capture.ReservationID, &capture.Revision, &capture.AcquiredAt, &capture.UpdatedAt))
	return capture
}

func insertGoldenSnapshotParticipantReservation(
	ctx context.Context,
	executor interface {
		Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	},
	capture goldenSnapshotParticipantReservation,
) error {
	_, err := executor.Exec(ctx, `
		INSERT INTO golden_exact_plan_snapshot_participant_reservations (
			plan_id, tournament_id, roster_id, participant_id, player_id,
			reservation_id, revision, acquired_at, updated_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		capture.PlanID,
		capture.TournamentID,
		capture.RosterID,
		capture.ParticipantID,
		capture.PlayerID,
		capture.ReservationID,
		capture.Revision,
		capture.AcquiredAt,
		capture.UpdatedAt,
		capture.CreatedAt,
	)
	return err
}

func readGoldenSnapshotParticipantReservations(
	ctx context.Context,
	t testing.TB,
	pool *pgxpool.Pool,
	tournamentID uuid.UUID,
) []goldenSnapshotParticipantReservation {
	t.Helper()

	rows, err := pool.Query(ctx, `
		SELECT plan_id, tournament_id, roster_id, participant_id, player_id,
		       reservation_id, revision, acquired_at, updated_at, created_at
		FROM golden_exact_plan_snapshot_participant_reservations
		WHERE tournament_id = $1
		ORDER BY plan_id, participant_id`, tournamentID)
	require.NoError(t, err)
	defer rows.Close()

	reservations := make([]goldenSnapshotParticipantReservation, 0)
	for rows.Next() {
		var reservation goldenSnapshotParticipantReservation
		require.NoError(t, rows.Scan(
			&reservation.PlanID,
			&reservation.TournamentID,
			&reservation.RosterID,
			&reservation.ParticipantID,
			&reservation.PlayerID,
			&reservation.ReservationID,
			&reservation.Revision,
			&reservation.AcquiredAt,
			&reservation.UpdatedAt,
			&reservation.CreatedAt,
		))
		reservations = append(reservations, reservation)
	}
	require.NoError(t, rows.Err())
	return reservations
}

func readGoldenSnapshotSeal(
	ctx context.Context,
	t testing.TB,
	pool *pgxpool.Pool,
	planID uuid.UUID,
) struct {
	ProofHash string
	SealedAt  time.Time
} {
	t.Helper()
	var seal struct {
		ProofHash string
		SealedAt  time.Time
	}
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT proof_hash, sealed_at
		FROM golden_exact_plan_snapshot_seals
		WHERE plan_id = $1`, planID).Scan(&seal.ProofHash, &seal.SealedAt))
	return seal
}
