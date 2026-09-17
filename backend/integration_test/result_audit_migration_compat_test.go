//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	resultaudit "github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/resultaudit"
	resultintegration "github.com/TakuyaYagam1/task-per-minute/integration_test/result"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type resultAuditMigrationFixture struct {
	draft                  draftMigrationFixture
	attemptID              uuid.UUID
	assignmentID           uuid.UUID
	initialScoreRevisionID uuid.UUID
	lockedAt               time.Time
}

type resultAuditCommit struct {
	resultEventID          uuid.UUID
	gameResultRevisionID   uuid.UUID
	seriesResultRevisionID uuid.UUID
	scoreRevisionID        uuid.UUID
	auditEventID           uuid.UUID
	outboxEventID          uuid.UUID
	projectionEvidenceID   uuid.UUID
	settledAt              time.Time
}

func TestResultAuditMigration(t *testing.T) {
	resultintegration.RunResultAuditMigration(t, sharedPool)
}

func createResultAuditMigrationFixture(
	ctx context.Context, tb testing.TB,
) resultAuditMigrationFixture {
	tb.Helper()
	return createResultAuditMigrationFixtureFromDraftMode(ctx, tb, createDraftMigrationFixture(ctx, tb), false)
}

func createResultAuditMigrationFixtureFromDraft(
	ctx context.Context,
	tb testing.TB,
	draft draftMigrationFixture,
) resultAuditMigrationFixture {
	tb.Helper()
	return createResultAuditMigrationFixtureFromDraftMode(ctx, tb, draft, true)
}

func createResultAuditMigrationFixtureFromDraftMode(
	ctx context.Context,
	tb testing.TB,
	draft draftMigrationFixture,
	copyVersionedSource bool,
) resultAuditMigrationFixture {
	tb.Helper()

	createdAt := draft.createdAt.Add(5 * time.Second)
	conservativePlanID := createConservativeAssignmentPlan(ctx, tb, draft, createdAt)
	exactPlanID := createExactAssignmentPlan(
		ctx, tb,
		draft,
		conservativePlanID,
		createdAt.Add(time.Second),
	)
	branchID := createAssignmentBranch(
		ctx, tb,
		exactPlanID,
		draft,
		"web-final",
		`["web"]`,
		createdAt.Add(2*time.Second),
	)
	releasedBranchID := createAssignmentBranch(
		ctx, tb,
		exactPlanID,
		draft,
		"web-fallback",
		`["web"]`,
		createdAt.Add(2*time.Second),
	)
	reservationBuilder := createAssignmentBranchReservations
	if copyVersionedSource {
		reservationBuilder = createResultAuditVersionedReservations
	}
	reservations := reservationBuilder(ctx, tb, exactPlanID, branchID, "web", createdAt.Add(3*time.Second))
	releasedReservations := reservationBuilder(
		ctx, tb, exactPlanID, releasedBranchID, "web", createdAt.Add(3*time.Second),
	)
	committedAt := createdAt.Add(4 * time.Second)
	for _, reservation := range reservations {
		_, err := sharedPool.Exec(ctx, `
			UPDATE task_version_reservations
			SET state = 'committed',
				revision = revision + 1,
				committed_at = $2
			WHERE id = $1`, reservation.reservationID, committedAt)
		require.NoError(tb, err)
	}
	for _, reservation := range releasedReservations {
		_, err := sharedPool.Exec(ctx, `
			UPDATE task_version_reservations
			SET state = 'released',
				revision = revision + 1,
				released_at = $2,
				release_reason = 'unused fixture branch'
			WHERE id = $1`, reservation.reservationID, committedAt)
		require.NoError(tb, err)
	}

	_, err := sharedPool.Exec(ctx, `
		UPDATE assignment_branches
		SET state = 'active', activated_at = $2
		WHERE id = $1`, branchID, committedAt.Add(time.Second))
	require.NoError(tb, err)
	_, err = sharedPool.Exec(ctx, `
		UPDATE assignment_branches
		SET state = 'released',
			released_at = $2,
			release_reason = 'unused fixture branch'
		WHERE id = $1`, releasedBranchID, committedAt.Add(time.Second))
	require.NoError(tb, err)
	_, err = sharedPool.Exec(ctx, `
		UPDATE assignment_plans
		SET state = 'committed', active_branch_id = $2, committed_at = $3
		WHERE id = $1`, exactPlanID, branchID, committedAt.Add(2*time.Second))
	require.NoError(tb, err)

	lockedAt := time.Now().UTC().Add(5 * time.Second).Truncate(time.Microsecond)
	prepared, err := resultaudit.PrepareScope(ctx, sharedPool, resultaudit.PrepareInput{
		TournamentID: draft.tournamentID,
		RosterID:     draft.rosterID,
		SeriesID:     draft.seriesID,
		ParticipantIDs: [2]uuid.UUID{
			draft.participantIDs[0], draft.participantIDs[1],
		},
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

func createResultAuditVersionedReservations(
	ctx context.Context,
	tb testing.TB,
	planID uuid.UUID,
	branchID uuid.UUID,
	category string,
	createdAt time.Time,
) []assignmentReservationFixture {
	tb.Helper()

	reservations := make([]assignmentReservationFixture, 3)
	for i := range reservations {
		taskID, taskVersion := createAssignmentMigrationTask(ctx, tb, category, i, &planID)
		edgeID := uuid.New()
		_, err := sharedPool.Exec(ctx, `
			INSERT INTO assignment_plan_edges (
				id, plan_id, branch_id, position,
				task_id, task_version, selection_evidence, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, '{"eligible":true}'::JSONB, $7)`,
			edgeID, planID, branchID, i+1, taskID, taskVersion, createdAt)
		require.NoError(tb, err)

		reservationID := uuid.New()
		_, err = sharedPool.Exec(ctx, `
			INSERT INTO task_version_reservations (
				id, edge_id, plan_id, branch_id,
				task_id, task_version, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			reservationID, edgeID, planID, branchID, taskID, taskVersion, createdAt)
		require.NoError(tb, err)

		snapshotID := uuid.New()
		_, err = sharedPool.Exec(ctx, `
			INSERT INTO task_snapshots (
				id, reservation_id, task_id, task_version, kind,
				title, description, category, difficulty,
				time_limit, flag, hints, task_url, source_file_url,
				content_digest, created_at
			)
			SELECT
				$1, $2, version.task_id, version.version, 'normal',
				version.title, version.description, version.category, version.difficulty,
				version.time_limit, version.flag, '[]'::JSONB, version.task_url, version.source_file_url,
				version.content_digest, $5
			FROM task_versions AS version
			WHERE version.task_id = $3 AND version.version = $4`,
			snapshotID, reservationID, taskID, taskVersion, createdAt)
		require.NoError(tb, err)

		reservations[i] = assignmentReservationFixture{
			reservationID: reservationID,
			snapshotID:    snapshotID,
			taskID:        taskID,
			taskVersion:   taskVersion,
		}
	}
	return reservations
}

func lockMigrationSeries(
	ctx context.Context, tb testing.TB,
	draft draftMigrationFixture,
	lockedAt time.Time,
) uuid.UUID {
	tb.Helper()

	fixture, err := resultaudit.LockSeries(ctx, sharedPool, resultaudit.LockInput{
		Scope: resultaudit.Scope{
			TournamentID: draft.tournamentID,
			RosterID:     draft.rosterID,
			SeriesID:     draft.seriesID,
			ParticipantIDs: [2]uuid.UUID{
				draft.participantIDs[0], draft.participantIDs[1],
			},
		},
		LockedAt: lockedAt,
	})
	require.NoError(tb, err)
	return fixture.InitialScoreRevisionID
}

func createAcceptedSubmission(
	ctx context.Context, tb testing.TB,
	fixture resultAuditMigrationFixture,
) (uuid.UUID, uuid.UUID) {
	tb.Helper()

	submissionID := uuid.New()
	idempotencyKey := uuid.New()
	receivedAt := fixture.lockedAt.Add(time.Second)
	submission, err := resultaudit.AcceptSubmission(
		ctx,
		sharedPool,
		resultaudit.Fixture{
			Scope: resultaudit.Scope{
				TournamentID: fixture.draft.tournamentID,
				RosterID:     fixture.draft.rosterID,
				SeriesID:     fixture.draft.seriesID,
				AttemptID:    fixture.attemptID,
				AssignmentID: fixture.assignmentID,
				ParticipantIDs: [2]uuid.UUID{
					fixture.draft.participantIDs[0], fixture.draft.participantIDs[1],
				},
			},
			InitialScoreRevisionID: fixture.initialScoreRevisionID,
			LockedAt:               fixture.lockedAt,
		},
		resultaudit.SubmissionInput{
			ID:             submissionID,
			IdempotencyKey: idempotencyKey,
			ParticipantID:  fixture.draft.participantIDs[0],
			PayloadDigest:  bytes.Repeat([]byte{20}, 32),
			IntentDigest:   bytes.Repeat([]byte{21}, 32),
			ReceivedAt:     receivedAt,
		},
	)
	require.NoError(tb, err)
	return submission.ID, submission.IdempotencyKey
}

func createAtomicResultCommit(
	ctx context.Context, tb testing.TB,
	fixture resultAuditMigrationFixture,
	submissionID uuid.UUID,
) resultAuditCommit {
	tb.Helper()

	commit, err := createResultCommit(ctx, tb, fixture, submissionID, true)
	require.NoError(tb, err)
	return commit
}
