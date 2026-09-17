//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	resultaudit "github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/resultaudit"
)

func createResultCommit(
	ctx context.Context, tb testing.TB,
	fixture resultAuditMigrationFixture,
	submissionID uuid.UUID,
	advanceCurrentHeads bool,
) (resultAuditCommit, error) {
	tb.Helper()

	result, err := resultaudit.CommitSolvedResult(
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
		resultaudit.CommitInput{
			SubmissionID: submissionID,
			IDs: resultaudit.CommitIDs{
				ResultEventID:             uuid.New(),
				ResultEventIdempotencyKey: uuid.New(),
				GameResultRevisionID:      uuid.New(),
				SeriesScoreRevisionID:     uuid.New(),
				SeriesResultRevisionID:    uuid.New(),
				AuditEventID:              uuid.New(),
				OutboxEventID:             uuid.New(),
				ProjectionEvidenceID:      uuid.New(),
				OutboxIdempotencyKey:      uuid.New(),
				CommitIdempotencyKey:      uuid.New(),
			},
			WinnerID:            fixture.draft.participantIDs[0],
			SettledAt:           fixture.lockedAt.Add(5 * time.Second),
			AdvanceCurrentHeads: advanceCurrentHeads,
		},
	)
	if err != nil {
		return resultAuditCommit{}, err
	}
	return resultAuditCommit{
		resultEventID:          result.IDs.ResultEventID,
		gameResultRevisionID:   result.IDs.GameResultRevisionID,
		seriesResultRevisionID: result.IDs.SeriesResultRevisionID,
		scoreRevisionID:        result.IDs.SeriesScoreRevisionID,
		auditEventID:           result.IDs.AuditEventID,
		outboxEventID:          result.IDs.OutboxEventID,
		projectionEvidenceID:   result.IDs.ProjectionEvidenceID,
		settledAt:              result.SettledAt,
	}, nil
}
