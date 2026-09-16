package correction

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	correctionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/correction"
)

const correctionOutboxTopic = "tournament.result.corrected"

var (
	ErrCorrectionNotFound = errors.New("correction repository: source not found")
	ErrCorrectionCutoff   = errors.New("correction repository: cutoff reached")
)

type CorrectionCutoffError struct {
	Code string
}

func (e *CorrectionCutoffError) Error() string {
	return fmt.Sprintf("%s: %s", ErrCorrectionCutoff, e.Code)
}

func (e *CorrectionCutoffError) Unwrap() error {
	return ErrCorrectionCutoff
}

type CorrectionPostgres struct {
	tx *db.TxManager
}

var _ correctionusecase.TransactionManager = (*db.TxManager)(nil)

type CorrectionDescendant struct {
	ArtifactID           uuid.UUID
	ArtifactKind         string
	ProducedByRevisionID uuid.UUID
	RevisionID           uuid.UUID
	RevisionNumber       int64
	RevisionState        string
}

type CorrectionTraversal struct {
	SourceRevisionID uuid.UUID
	SourceIsCurrent  bool
	CutoffCode       string
	Descendants      []CorrectionDescendant
}

type CorrectionInput struct {
	IDs                            ResultSettlementIDs
	ProjectionIDs                  ProjectionIDs
	Scope                          ResultScope
	SourceRevisionID               uuid.UUID
	ExpectedAttemptRevision        int64
	ExpectedAttemptState           domain.GameState
	ExpectedScoreRevisionID        uuid.UUID
	ExpectedScoreRevision          int64
	ExpectedSeriesRevision         int64
	ExpectedSeriesState            domain.SeriesState
	ExpectedSeriesResultRevisionID *uuid.UUID
	ExpectedProjectionRevisionID   uuid.UUID
	SubmissionEventID              uuid.UUID
	GameResultCommandID            uuid.UUID
	ScoreCommandID                 uuid.UUID
	SeriesResultCommandID          uuid.UUID
	GameState                      domain.GameState
	GameReason                     domain.GameResultReason
	GameWinnerID                   *uuid.UUID
	Score                          domain.SeriesScore
	NextSeriesState                domain.SeriesState
	SeriesResultReason             string
	SeriesWinnerID                 *uuid.UUID
	OperatorID                     uuid.UUID
	Reason                         string
	ProjectionArtifacts            []ProjectionArtifactInput
	ReusedProjectionArtifactIDs    []uuid.UUID
	ReplaceProjectionSet           bool
	ProjectionPayloadDigest        [32]byte
	CorrectedAt                    time.Time
}

type CorrectionRecord struct {
	ResultCommit       *ResultCommitRecord
	Projection         *ProjectionRecord
	ClosedReadyWindows []uuid.UUID
}

type correctionProjectionPlan struct {
	newArtifacts []ProjectionArtifactInput
	reused       []sqlc.ProjectionArtifact
	allKinds     []domain.ArtifactKind
}

func NewCorrectionPostgres(tx *db.TxManager) *CorrectionPostgres {
	return &CorrectionPostgres{tx: tx}
}

func (r *CorrectionPostgres) Traverse(
	ctx context.Context,
	scope ResultScope,
	sourceRevisionID uuid.UUID,
) (*CorrectionTraversal, error) {
	if r == nil || r.tx == nil || !validResultScope(scope) || sourceRevisionID == uuid.Nil {
		return nil, domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	source, err := querier.GetCorrectionSource(ctx, correctionSourceParams(scope, sourceRevisionID))
	if err != nil {
		return nil, correctionLookupError("Traverse - source", err)
	}
	cutoff, err := querier.GetCorrectionCutoff(ctx, correctionCutoffParams(scope, sourceRevisionID))
	if err != nil {
		return nil, fmt.Errorf("CorrectionPostgres - Traverse - cutoff: %w", err)
	}
	traversal := &CorrectionTraversal{
		SourceRevisionID: sourceRevisionID,
		SourceIsCurrent:  source.CurrentRevisionID == sourceRevisionID,
		CutoffCode:       cutoff,
		Descendants:      []CorrectionDescendant{},
	}
	if cutoff != "" {
		return traversal, nil
	}
	rows, err := querier.ListCorrectionDescendants(
		ctx,
		sqlc.ListCorrectionDescendantsParams{
			TournamentID: scope.TournamentID, RosterID: scope.RosterID,
			SourceRevisionID: nullableUUIDValue(sourceRevisionID),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("CorrectionPostgres - Traverse - descendants: %w", err)
	}
	traversal.Descendants = make([]CorrectionDescendant, 0, len(rows))
	for _, row := range rows {
		traversal.Descendants = append(traversal.Descendants, correctionDescendant(
			row.ArtifactID, row.ArtifactKind, row.ProducedByRevisionID,
			row.RevisionID, row.RevisionNumber, row.RevisionState,
		))
	}
	return traversal, nil
}

func (r *CorrectionPostgres) Rebuild(
	ctx context.Context,
	in CorrectionInput,
) (*CorrectionRecord, error) {
	if r == nil || r.tx == nil || !validCorrectionInput(in) {
		return nil, domain.ErrValidation
	}
	var record *CorrectionRecord
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		var rebuildErr error
		record, rebuildErr = r.rebuildLocked(txCtx, in)
		return rebuildErr
	})
	if err != nil {
		return nil, err
	}
	return record, nil
}
