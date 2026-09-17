//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/correctionseed"
	projectionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/projection"
	resultrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result"
	correctionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/correction"
)

func newCorrectionInput(
	ctx context.Context, tb testing.TB,
	fixture correctionRepositoryFixture,
	currentResult *resultrepo.ResultCommitRecord,
	currentProjection *projectionrepo.ProjectionRecord,
	winnerIndex int,
	correctedAt time.Time,
) correctionrepo.CorrectionInput {
	tb.Helper()
	input, err := correctionseed.BuildCorrectionInput(ctx, sharedPool, correctionseed.RebuildInput{
		Scope: correctionseed.Scope{
			ResultScope: resultrepo.ResultScope{
				TournamentID: fixture.resultFixture.draft.tournamentID,
				RosterID:     fixture.resultFixture.draft.rosterID,
				SeriesID:     fixture.resultFixture.draft.seriesID,
				AttemptID:    fixture.resultFixture.attemptID,
			},
			AssignmentID: fixture.resultFixture.assignmentID,
			Participants: fixture.participants,
		},
		CurrentResult: currentResult, CurrentProjection: currentProjection,
		WinnerIndex: winnerIndex, CorrectedAt: correctedAt,
	})
	require.NoError(tb, err)
	return input
}
