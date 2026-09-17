//go:build integration

package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/correctionseed"
	resultrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result"
)

func createCorrectionRepositoryFixture(
	ctx context.Context, tb testing.TB,
) correctionRepositoryFixture {
	tb.Helper()

	resultFixture := createResultAuditMigrationFixture(ctx, tb)
	seed, err := correctionseed.Build(ctx, sharedPool, correctionseed.Input{
		ResultScope: resultrepo.ResultScope{
			TournamentID: resultFixture.draft.tournamentID,
			RosterID:     resultFixture.draft.rosterID,
			SeriesID:     resultFixture.draft.seriesID,
			AttemptID:    resultFixture.attemptID,
		},
		AssignmentID:   resultFixture.assignmentID,
		ParticipantIDs: resultFixture.draft.participantIDs,
		LockedAt:       resultFixture.lockedAt,
	})
	require.NoError(tb, err)

	return correctionRepositoryFixture{
		resultFixture: resultFixture, participants: seed.Participants,
		result: seed.Result, projection: seed.Projection,
		waveID: seed.WaveID, windowID: seed.WindowID, waveRevision: seed.WaveRevision,
		readinessRevisions: seed.ReadinessRevisions,
		deadline:           seed.Deadline, nextTime: seed.NextTime,
	}
}
