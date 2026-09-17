//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/correctionseed"
	projectionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/projection"
	resultrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result"
)

type correctionRepositoryFixture struct {
	resultFixture      resultAuditMigrationFixture
	participants       []uuid.UUID
	result             *resultrepo.ResultCommitRecord
	projection         *projectionrepo.ProjectionRecord
	waveID             uuid.UUID
	windowID           uuid.UUID
	waveRevision       int64
	readinessRevisions map[uuid.UUID]int64
	deadline           time.Time
	nextTime           time.Time
}

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
