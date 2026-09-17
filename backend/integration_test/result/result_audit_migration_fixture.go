//go:build integration

package result

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/draftseed"
	resultaudit "github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/resultaudit"
	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/tournamentseed"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type draftMigrationFixture struct {
	prepared             draftseed.PreparedScope
	tournamentID         uuid.UUID
	rosterID             uuid.UUID
	seriesID             uuid.UUID
	draftID              uuid.UUID
	categoryRevisionID   uuid.UUID
	normalPoolRevisionID uuid.UUID
	initialRevisionID    uuid.UUID
	participantIDs       []uuid.UUID
	initialServiceEpoch  uuid.UUID
	createdAt            time.Time
}

func createDraftMigrationFixture(ctx context.Context, tb testing.TB) draftMigrationFixture {
	tb.Helper()
	createdAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	prepared, err := draftseed.Prepare(ctx, migrationPool, draftseed.PrepareInput{
		CreatedAt: createdAt,
		Content:   createCorrectionContent,
	})
	require.NoError(tb, err)
	return draftMigrationFixture{
		prepared:             prepared,
		tournamentID:         prepared.TournamentID,
		rosterID:             prepared.RosterID,
		seriesID:             prepared.SeriesID,
		draftID:              prepared.Draft.DraftID,
		categoryRevisionID:   prepared.Draft.CategoryRevisionID,
		normalPoolRevisionID: prepared.Content.NormalPoolRevisionID,
		initialRevisionID:    prepared.Draft.InitialRevisionID,
		participantIDs: []uuid.UUID{
			prepared.Draft.FirstParticipantID,
			prepared.Draft.SecondParticipantID,
		},
		initialServiceEpoch: prepared.Draft.InitialServiceEpoch,
		createdAt:           prepared.CreatedAt,
	}
}

func createResultAuditMigrationFixture(
	ctx context.Context,
	tb testing.TB,
) resultAuditMigrationFixture {
	tb.Helper()
	draft := createDraftMigrationFixture(ctx, tb)
	createdAt := draft.createdAt.Add(5 * time.Second)
	conservativePlanID, err := createConservativePlan(ctx, migrationPool, draft.prepared, createdAt)
	require.NoError(tb, err)
	exactPlanID, err := createExactPlan(
		ctx, migrationPool, draft.prepared, conservativePlanID, createdAt.Add(time.Second),
	)
	require.NoError(tb, err)
	branchID, err := createBranch(
		ctx, migrationPool, draft.prepared, exactPlanID, "web-final", createdAt.Add(2*time.Second),
	)
	require.NoError(tb, err)
	releasedBranchID, err := createBranch(
		ctx, migrationPool, draft.prepared, exactPlanID, "web-fallback", createdAt.Add(2*time.Second),
	)
	require.NoError(tb, err)
	reservations, err := createReservations(
		ctx, migrationPool, draft.prepared, exactPlanID, branchID, createdAt.Add(3*time.Second),
	)
	require.NoError(tb, err)
	releasedReservations, err := createReservations(
		ctx, migrationPool, draft.prepared, exactPlanID, releasedBranchID, createdAt.Add(3*time.Second),
	)
	require.NoError(tb, err)
	committedAt := createdAt.Add(4 * time.Second)
	require.NoError(tb, updateAssignmentState(
		ctx, migrationPool, exactPlanID, branchID, releasedBranchID,
		reservations, releasedReservations, committedAt,
	))

	lockedAt := time.Now().UTC().Add(5 * time.Second).Truncate(time.Microsecond)
	prepared, err := resultaudit.PrepareScope(ctx, migrationPool, resultaudit.PrepareInput{
		TournamentID:   draft.tournamentID,
		RosterID:       draft.rosterID,
		SeriesID:       draft.seriesID,
		ParticipantIDs: [2]uuid.UUID{draft.participantIDs[0], draft.participantIDs[1]},
		Assignment: resultaudit.AssignmentInput{
			ID:            uuid.New(),
			PlanID:        exactPlanID,
			BranchID:      branchID,
			ReservationID: reservations[0].reservationID,
			SnapshotID:    reservations[0].snapshotID,
			TaskID:        reservations[0].taskID,
			TaskVersion:   reservations[0].taskVersion,
		},
		GameCategory:        domain.CategoryWeb,
		GameCreatedAt:       committedAt.Add(3 * time.Second),
		AssignmentCreatedAt: committedAt.Add(4 * time.Second),
		LockedAt:            lockedAt,
	})
	require.NoError(tb, err)

	return resultAuditMigrationFixture{
		draft:                  draft,
		attemptID:              prepared.Fixture.Scope.AttemptID,
		assignmentID:           prepared.Fixture.Scope.AssignmentID,
		initialScoreRevisionID: prepared.Fixture.InitialScoreRevisionID,
		lockedAt:               prepared.Fixture.LockedAt,
	}
}

func beginResultAuditLockProbe(ctx context.Context, tb testing.TB) pgx.Tx {
	tb.Helper()
	probe, err := resultaudit.BeginLockProbe(ctx, migrationPool)
	require.NoError(tb, err)
	return probe
}

func assertResultParticipantIntegrity(
	ctx context.Context,
	tb testing.TB,
	fixture resultAuditMigrationFixture,
	submissionID uuid.UUID,
) {
	tb.Helper()

	playerIDs, err := tournamentseed.CreatePlayers(ctx, migrationPool, "result_audit_migration", 1)
	require.NoError(tb, err)
	var outsideParticipantID uuid.UUID
	err = migrationPool.QueryRow(ctx, `
		INSERT INTO participants (roster_id, player_id, seed, attendance)
		SELECT $1, $2, COALESCE(MAX(seed), 0) + 1, 'checked_in'
		FROM participants
		WHERE roster_id = $1
		RETURNING id`, fixture.draft.rosterID, playerIDs[0]).Scan(&outsideParticipantID)
	require.NoError(tb, err)

	createdAt := fixture.lockedAt.Add(2 * time.Second)
	probeTx, err := migrationPool.Begin(ctx)
	require.NoError(tb, err)
	var eventSequence int64
	err = probeTx.QueryRow(ctx, `
		UPDATE game_attempts
		SET submission_event_sequence = submission_event_sequence + 1
		WHERE id = $1
		RETURNING submission_event_sequence`, fixture.attemptID).Scan(&eventSequence)
	require.NoError(tb, err)
	_, err = probeTx.Exec(ctx, `
		INSERT INTO submission_events (
			tournament_id, roster_id, series_id, attempt_id, assignment_id,
			participant_id, server_sequence, idempotency_key, status,
			payload_digest, submitted_at, received_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'accepted', $9, $10, $10, $10)`,
		fixture.draft.tournamentID, fixture.draft.rosterID, fixture.draft.seriesID,
		fixture.attemptID, fixture.assignmentID, outsideParticipantID, eventSequence,
		uuid.New(), bytes.Repeat([]byte{23}, 32), createdAt)
	require.ErrorContains(tb, err, "submission participant is outside the Series")
	require.NoError(tb, probeTx.Rollback(ctx))

	probeTx, err = migrationPool.Begin(ctx)
	require.NoError(tb, err)
	err = probeTx.QueryRow(ctx, `
		UPDATE game_attempts
		SET result_event_sequence = result_event_sequence + 1
		WHERE id = $1
		RETURNING result_event_sequence`, fixture.attemptID).Scan(&eventSequence)
	require.NoError(tb, err)
	_, err = probeTx.Exec(ctx, `
		INSERT INTO result_events (
			tournament_id, roster_id, series_id, attempt_id,
			server_sequence, idempotency_key, result_state,
			result_reason, winner_id, occurred_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, 'completed', 'operator_forfeit', $7, $8, $8)`,
		fixture.draft.tournamentID, fixture.draft.rosterID, fixture.draft.seriesID,
		fixture.attemptID, eventSequence, uuid.New(), outsideParticipantID, createdAt)
	require.ErrorContains(tb, err, "result winner is outside the Series")
	require.NoError(tb, probeTx.Rollback(ctx))

	probeTx, err = migrationPool.Begin(ctx)
	require.NoError(tb, err)
	err = probeTx.QueryRow(ctx, `
		UPDATE game_attempts
		SET result_event_sequence = result_event_sequence + 1
		WHERE id = $1
		RETURNING result_event_sequence`, fixture.attemptID).Scan(&eventSequence)
	require.NoError(tb, err)
	_, err = probeTx.Exec(ctx, `
		INSERT INTO result_events (
			tournament_id, roster_id, series_id, attempt_id,
			submission_event_id, server_sequence, idempotency_key,
			result_state, result_reason, winner_id, occurred_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'completed', 'solved', $8, $9, $9)`,
		fixture.draft.tournamentID, fixture.draft.rosterID, fixture.draft.seriesID,
		fixture.attemptID, submissionID, eventSequence, uuid.New(), fixture.draft.participantIDs[1], createdAt)
	require.ErrorContains(tb, err, "solved result winner must match the submission participant")
	require.NoError(tb, probeTx.Rollback(ctx))

	_, err = migrationPool.Exec(ctx, `
		UPDATE series SET first_participant_id = $2 WHERE id = $1`,
		fixture.draft.seriesID, outsideParticipantID)
	require.ErrorContains(tb, err, "Series result identity is immutable")
}

func createAcceptedSubmission(
	ctx context.Context,
	tb testing.TB,
	fixture resultAuditMigrationFixture,
) (uuid.UUID, uuid.UUID) {
	tb.Helper()
	submissionID := uuid.New()
	idempotencyKey := uuid.New()
	submission, err := resultaudit.AcceptSubmission(ctx, migrationPool, resultaudit.Fixture{
		Scope: resultaudit.Scope{
			TournamentID: fixture.draft.tournamentID,
			RosterID:     fixture.draft.rosterID, SeriesID: fixture.draft.seriesID,
			AttemptID: fixture.attemptID, AssignmentID: fixture.assignmentID,
			ParticipantIDs: [2]uuid.UUID{
				fixture.draft.participantIDs[0], fixture.draft.participantIDs[1],
			},
		},
		InitialScoreRevisionID: fixture.initialScoreRevisionID,
		LockedAt:               fixture.lockedAt,
	}, resultaudit.SubmissionInput{
		ID: submissionID, IdempotencyKey: idempotencyKey,
		ParticipantID: fixture.draft.participantIDs[0],
		PayloadDigest: bytes.Repeat([]byte{20}, 32),
		IntentDigest:  bytes.Repeat([]byte{21}, 32),
		ReceivedAt:    fixture.lockedAt.Add(time.Second),
	})
	require.NoError(tb, err)
	return submission.ID, submission.IdempotencyKey
}

func createAtomicResultCommit(
	ctx context.Context,
	tb testing.TB,
	fixture resultAuditMigrationFixture,
	submissionID uuid.UUID,
) resultAuditCommit {
	tb.Helper()
	commit, err := createResultCommit(ctx, tb, fixture, submissionID, true)
	require.NoError(tb, err)
	return commit
}

func assertResultCommitRequiresCurrentHeads(
	ctx context.Context,
	tb testing.TB,
	fixture resultAuditMigrationFixture,
	submissionID uuid.UUID,
) {
	tb.Helper()
	_, err := createResultCommit(ctx, tb, fixture, submissionID, false)
	require.ErrorContains(tb, err, "result commit revisions must be current heads")
}
