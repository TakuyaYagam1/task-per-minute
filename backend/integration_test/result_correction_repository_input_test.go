//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	projectionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/projection"
	resultrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result"
	correctionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/correction"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
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

	var (
		attemptRevision int64
		attemptState    string
		seriesRevision  int64
		seriesState     string
	)
	err := sharedPool.QueryRow(ctx, `
		SELECT revision, state
		FROM game_attempts
		WHERE id = $1`, fixture.resultFixture.attemptID).Scan(&attemptRevision, &attemptState)
	require.NoError(tb, err)
	err = sharedPool.QueryRow(ctx, `
		SELECT revision, state
		FROM series
		WHERE id = $1`, fixture.resultFixture.draft.seriesID).Scan(&seriesRevision, &seriesState)
	require.NoError(tb, err)

	ids := resultrepo.ResultSettlementIDs{
		CommitID: uuid.New(), ResultEventID: uuid.New(), ResultEventIdempotencyKey: uuid.New(),
		GameResultRevisionID: uuid.New(), SeriesScoreRevisionID: uuid.New(),
		SeriesResultRevisionID: uuid.New(), AuditEventID: uuid.New(), OutboxEventID: uuid.New(),
		OutboxIdempotencyKey: uuid.New(), ProjectionEvidenceID: uuid.New(), CommitIdempotencyKey: uuid.New(),
	}
	winnerID := fixture.participants[winnerIndex]
	score := domain.SeriesScore{}
	if winnerIndex == 0 {
		score.FirstParticipantWins = 1
	} else {
		score.SecondParticipantWins = 1
	}
	reason := fmt.Sprintf("operator correction revision %d", currentResult.GameRevision.RevisionNumber+1)
	digest := sha256.Sum256([]byte(reason))
	seriesResultRevisionID := currentResult.SeriesRevision.ID
	return correctionrepo.CorrectionInput{
		IDs: ids, ProjectionIDs: projectionrepo.ProjectionIDs{RevisionID: uuid.New(), CutoffID: uuid.New()},
		Scope: resultrepo.ResultScope{
			TournamentID: fixture.resultFixture.draft.tournamentID, RosterID: fixture.resultFixture.draft.rosterID,
			SeriesID: fixture.resultFixture.draft.seriesID, AttemptID: fixture.resultFixture.attemptID,
		},
		SourceRevisionID:        currentResult.GameRevision.ID,
		ExpectedAttemptRevision: attemptRevision, ExpectedAttemptState: domain.GameState(attemptState),
		ExpectedScoreRevisionID: currentResult.ScoreRevision.ID,
		ExpectedScoreRevision:   currentResult.ScoreRevision.RevisionNumber,
		ExpectedSeriesRevision:  seriesRevision, ExpectedSeriesState: domain.SeriesState(seriesState),
		ExpectedSeriesResultRevisionID: &seriesResultRevisionID,
		ExpectedProjectionRevisionID:   currentProjection.Revision.ID,
		GameState:                      domain.GameStateCompleted, GameReason: domain.GameResultReasonSurrender,
		GameWinnerID: &winnerID, Score: score, NextSeriesState: domain.SeriesStateCompleted,
		SeriesResultReason: "operator_correction", SeriesWinnerID: &winnerID,
		OperatorID: uuid.New(), Reason: reason,
		ProjectionArtifacts: correctionProjectionArtifacts(
			fixture.participants, ids.GameResultRevisionID, fixture.resultFixture.draft.seriesID,
			"correction-"+ids.GameResultRevisionID.String(),
		),
		ProjectionPayloadDigest: digest, CorrectedAt: correctedAt,
	}
}
