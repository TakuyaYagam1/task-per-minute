//go:build integration

package correctionseed

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	projectionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/projection"
	resultrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result"
	correctionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/correction"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

// RebuildInput identifies the current correction heads and desired winner for
// a correction repository rebuild.
type RebuildInput struct {
	Scope             Scope
	CurrentResult     *resultrepo.ResultCommitRecord
	CurrentProjection *projectionrepo.ProjectionRecord
	WinnerIndex       int
	CorrectedAt       time.Time
}

// BuildCorrectionInput loads current CAS heads and builds the correction
// command used by the existing root flow tests.
func BuildCorrectionInput(
	ctx context.Context,
	pool *pgxpool.Pool,
	input RebuildInput,
) (correctionrepo.CorrectionInput, error) {
	if pool == nil {
		return correctionrepo.CorrectionInput{}, fmt.Errorf("correction seed: nil pool")
	}
	if input.CurrentResult == nil || input.CurrentProjection == nil ||
		input.WinnerIndex < 0 || input.WinnerIndex >= len(input.Scope.Participants) {
		return correctionrepo.CorrectionInput{}, fmt.Errorf("correction seed: invalid rebuild input")
	}

	var (
		attemptRevision int64
		attemptState    string
		seriesRevision  int64
		seriesState     string
	)
	err := pool.QueryRow(ctx, `
		SELECT revision, state
		FROM game_attempts
		WHERE id = $1`, input.Scope.ResultScope.AttemptID).Scan(&attemptRevision, &attemptState)
	if err != nil {
		return correctionrepo.CorrectionInput{}, fmt.Errorf("correction seed: load attempt head: %w", err)
	}
	err = pool.QueryRow(ctx, `
		SELECT revision, state
		FROM series
		WHERE id = $1`, input.Scope.ResultScope.SeriesID).Scan(&seriesRevision, &seriesState)
	if err != nil {
		return correctionrepo.CorrectionInput{}, fmt.Errorf("correction seed: load series head: %w", err)
	}

	ids := resultrepo.ResultSettlementIDs{
		CommitID: uuid.New(), ResultEventID: uuid.New(), ResultEventIdempotencyKey: uuid.New(),
		GameResultRevisionID: uuid.New(), SeriesScoreRevisionID: uuid.New(),
		SeriesResultRevisionID: uuid.New(), AuditEventID: uuid.New(), OutboxEventID: uuid.New(),
		OutboxIdempotencyKey: uuid.New(), ProjectionEvidenceID: uuid.New(), CommitIdempotencyKey: uuid.New(),
	}
	winnerID := input.Scope.Participants[input.WinnerIndex]
	score := domain.SeriesScore{}
	if input.WinnerIndex == 0 {
		score.FirstParticipantWins = 1
	} else {
		score.SecondParticipantWins = 1
	}
	reason := fmt.Sprintf(
		"operator correction revision %d",
		input.CurrentResult.GameRevision.RevisionNumber+1,
	)
	digest := sha256.Sum256([]byte(reason))
	seriesResultRevisionID := input.CurrentResult.SeriesRevision.ID
	return correctionrepo.CorrectionInput{
		IDs:                            ids,
		ProjectionIDs:                  projectionrepo.ProjectionIDs{RevisionID: uuid.New(), CutoffID: uuid.New()},
		Scope:                          input.Scope.ResultScope,
		SourceRevisionID:               input.CurrentResult.GameRevision.ID,
		ExpectedAttemptRevision:        attemptRevision,
		ExpectedAttemptState:           domain.GameState(attemptState),
		ExpectedScoreRevisionID:        input.CurrentResult.ScoreRevision.ID,
		ExpectedScoreRevision:          input.CurrentResult.ScoreRevision.RevisionNumber,
		ExpectedSeriesRevision:         seriesRevision,
		ExpectedSeriesState:            domain.SeriesState(seriesState),
		ExpectedSeriesResultRevisionID: &seriesResultRevisionID,
		ExpectedProjectionRevisionID:   input.CurrentProjection.Revision.ID,
		GameState:                      domain.GameStateCompleted,
		GameReason:                     domain.GameResultReasonSurrender,
		GameWinnerID:                   &winnerID,
		Score:                          score,
		NextSeriesState:                domain.SeriesStateCompleted,
		SeriesResultReason:             "operator_correction",
		SeriesWinnerID:                 &winnerID,
		OperatorID:                     uuid.New(),
		Reason:                         reason,
		ProjectionArtifacts: ProjectionArtifacts(
			input.Scope.Participants,
			ids.GameResultRevisionID,
			input.Scope.ResultScope.SeriesID,
			"correction-"+ids.GameResultRevisionID.String(),
		),
		ProjectionPayloadDigest: digest,
		CorrectedAt:             input.CorrectedAt,
	}, nil
}
