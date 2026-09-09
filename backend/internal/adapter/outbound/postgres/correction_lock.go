package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type correctionLockedResult struct {
	attempt sqlc.LockResultAttemptRow
	series  sqlc.LockResultSeriesRow
	heads   []sqlc.OfficialResultHead
	source  sqlc.GetCorrectionSourceRow
}

type correctionLockedProjection struct {
	current sqlc.ProjectionRevision
	plan    correctionProjectionPlan
}

func (r *CorrectionPostgres) rebuildLocked(
	ctx context.Context,
	in CorrectionInput,
) (*CorrectionRecord, error) {
	querier := r.tx.Querier(ctx)
	if err := lockCorrectionScope(ctx, querier, in.Scope); err != nil {
		return nil, err
	}
	result, err := lockCorrectionResult(ctx, querier, in)
	if err != nil {
		return nil, fmt.Errorf("CorrectionPostgres - lock result: %w", err)
	}
	projection, err := lockCorrectionProjection(ctx, querier, in)
	if err != nil {
		return nil, fmt.Errorf("CorrectionPostgres - lock projection: %w", err)
	}
	windows, err := lockCorrectionReadiness(ctx, querier, in)
	if err != nil {
		return nil, fmt.Errorf("CorrectionPostgres - lock readiness: %w", err)
	}
	evidence, err := createCorrectionResultEvidence(
		ctx, querier, in, result.source, result.attempt, result.series, projection.current,
	)
	if err != nil {
		return nil, fmt.Errorf("CorrectionPostgres - create result evidence: %w", err)
	}
	if err := prepareCorrectionProjection(ctx, querier, in, projection.current, projection.plan); err != nil {
		return nil, fmt.Errorf("CorrectionPostgres - prepare projection: %w", err)
	}
	if err := createCorrectionSideEvidence(
		ctx,
		querier,
		in,
		evidence.resultEventID,
		evidence.seriesResultID,
		projection.plan.allKinds,
		in.ProjectionIDs.RevisionID,
		projection.current.RevisionNumber+1,
	); err != nil {
		return nil, fmt.Errorf("CorrectionPostgres - create side evidence: %w", err)
	}
	if err := advanceCorrectionHeads(ctx, querier, in, result.source, result.heads); err != nil {
		return nil, fmt.Errorf("CorrectionPostgres - advance heads: %w", err)
	}
	if err := publishCorrectionProjection(ctx, querier, in, projection.current); err != nil {
		return nil, fmt.Errorf("CorrectionPostgres - publish projection: %w", err)
	}
	closed, err := closeCorrectionReadiness(ctx, querier, in, windows)
	if err != nil {
		return nil, fmt.Errorf("CorrectionPostgres - close readiness: %w", err)
	}
	record, err := loadCorrectionRecord(ctx, querier, in, closed)
	if err != nil {
		return nil, fmt.Errorf("CorrectionPostgres - load record: %w", err)
	}
	return record, nil
}

func lockCorrectionResult(
	ctx context.Context,
	querier *sqlc.Queries,
	in CorrectionInput,
) (correctionLockedResult, error) {
	attempt, err := querier.LockResultAttempt(ctx, resultAttemptParams(in.Scope))
	if err != nil {
		return correctionLockedResult{}, correctionLookupError("Rebuild - attempt", err)
	}
	series, err := querier.LockResultSeries(ctx, sqlc.LockResultSeriesParams{
		SeriesID: in.Scope.SeriesID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
	})
	if err != nil {
		return correctionLockedResult{}, correctionLookupError("Rebuild - series", err)
	}
	heads, err := querier.LockCorrectionOfficialHeads(ctx, sqlc.LockCorrectionOfficialHeadsParams{
		RosterID: in.Scope.RosterID, AttemptID: in.Scope.AttemptID, SeriesID: in.Scope.SeriesID,
	})
	if err != nil {
		return correctionLockedResult{}, fmt.Errorf("CorrectionPostgres - Rebuild - heads: %w", err)
	}
	source, err := querier.GetCorrectionSource(ctx, correctionSourceParams(in.Scope, in.SourceRevisionID))
	if err != nil {
		return correctionLockedResult{}, correctionLookupError("Rebuild - source", err)
	}
	if err := validateCorrectionLockedResult(in, attempt, series, heads, source); err != nil {
		return correctionLockedResult{}, err
	}
	return correctionLockedResult{attempt: attempt, series: series, heads: heads, source: source}, nil
}

func lockCorrectionProjection(
	ctx context.Context,
	querier *sqlc.Queries,
	in CorrectionInput,
) (correctionLockedProjection, error) {
	if _, err := querier.LockProjectionRevisionSet(ctx, sqlc.LockProjectionRevisionSetParams{
		TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
	}); err != nil {
		return correctionLockedProjection{}, fmt.Errorf(
			"CorrectionPostgres - Rebuild - projection revisions: %w", err,
		)
	}
	current, err := querier.GetCurrentProjectionRevision(ctx, sqlc.GetCurrentProjectionRevisionParams{
		TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
	})
	if err != nil {
		return correctionLockedProjection{}, correctionLookupError("Rebuild - current projection", err)
	}
	if current.ID != in.ExpectedProjectionRevisionID {
		return correctionLockedProjection{}, domain.ErrConflict
	}
	descendants, err := querier.LockCorrectionDescendants(ctx, sqlc.LockCorrectionDescendantsParams{
		TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		SourceRevisionID: nullableUUIDValue(in.SourceRevisionID),
	})
	if err != nil {
		return correctionLockedProjection{}, fmt.Errorf(
			"CorrectionPostgres - Rebuild - descendants: %w", err,
		)
	}
	plan, err := prepareCorrectionProjectionPlan(ctx, querier, in, current, descendants)
	return correctionLockedProjection{current: current, plan: plan}, err
}

func lockCorrectionReadiness(
	ctx context.Context,
	querier *sqlc.Queries,
	in CorrectionInput,
) ([]sqlc.LockCorrectionOpenReadyWindowsRow, error) {
	windows, err := querier.LockCorrectionOpenReadyWindows(ctx, sqlc.LockCorrectionOpenReadyWindowsParams{
		TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
	})
	if err != nil {
		return nil, fmt.Errorf("CorrectionPostgres - Rebuild - ready windows: %w", err)
	}
	cutoff, err := querier.GetCorrectionCutoff(ctx, correctionCutoffParams(in.Scope, in.SourceRevisionID))
	if err != nil {
		return nil, fmt.Errorf("CorrectionPostgres - Rebuild - cutoff recheck: %w", err)
	}
	if cutoff != "" {
		return nil, &CorrectionCutoffError{Code: cutoff}
	}
	return windows, nil
}

func loadCorrectionRecord(
	ctx context.Context,
	querier *sqlc.Queries,
	in CorrectionInput,
	closed []uuid.UUID,
) (*CorrectionRecord, error) {
	commit, err := querier.GetResultCommitByID(ctx, in.IDs.CommitID)
	if err != nil {
		return nil, fmt.Errorf("CorrectionPostgres - Rebuild - result commit: %w", err)
	}
	result, err := loadResultCommit(ctx, querier, commit)
	if err != nil {
		return nil, err
	}
	projection, err := loadProjectionRecord(
		ctx,
		querier,
		ProjectionScope{TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID},
		in.ProjectionIDs.RevisionID,
	)
	if err != nil {
		return nil, err
	}
	return &CorrectionRecord{
		ResultCommit: result, Projection: projection, ClosedReadyWindows: closed,
	}, nil
}

func lockCorrectionScope(
	ctx context.Context,
	querier *sqlc.Queries,
	scope ResultScope,
) error {
	if _, err := querier.LockCorrectionTournamentScope(
		ctx,
		sqlc.LockCorrectionTournamentScopeParams{
			TournamentID: scope.TournamentID, RosterID: scope.RosterID,
		},
	); err != nil {
		return correctionLookupError("lock Tournament scope", err)
	}
	// Every correction path takes the canonical Tournament -> Roster ->
	// Projection prefix before it can lock a Series or any child row.
	if _, err := querier.LockProjectionRevisionSet(
		ctx,
		sqlc.LockProjectionRevisionSetParams{
			TournamentID: scope.TournamentID, RosterID: scope.RosterID,
		},
	); err != nil {
		return fmt.Errorf("CorrectionPostgres - lock projection revisions: %w", err)
	}
	if _, err := querier.LockCorrectionSeriesAttempts(
		ctx,
		sqlc.LockCorrectionSeriesAttemptsParams{
			SeriesID: scope.SeriesID, TournamentID: scope.TournamentID, RosterID: scope.RosterID,
		},
	); err != nil {
		return fmt.Errorf("CorrectionPostgres - lock attempts: %w", err)
	}
	if _, err := querier.LockCorrectionCutoffWaves(
		ctx,
		sqlc.LockCorrectionCutoffWavesParams{
			TournamentID: scope.TournamentID, RosterID: scope.RosterID,
		},
	); err != nil {
		return fmt.Errorf("CorrectionPostgres - lock Waves: %w", err)
	}
	if _, err := querier.LockCorrectionAssignments(
		ctx,
		sqlc.LockCorrectionAssignmentsParams{
			TournamentID: scope.TournamentID, RosterID: scope.RosterID,
		},
	); err != nil {
		return fmt.Errorf("CorrectionPostgres - lock assignments: %w", err)
	}
	if _, err := querier.LockCorrectionGoldenAttempts(
		ctx,
		sqlc.LockCorrectionGoldenAttemptsParams{
			TournamentID: scope.TournamentID, RosterID: scope.RosterID,
		},
	); err != nil {
		return fmt.Errorf("CorrectionPostgres - lock Golden: %w", err)
	}
	return nil
}

func validateCorrectionLockedResult(
	in CorrectionInput,
	attempt sqlc.LockResultAttemptRow,
	series sqlc.LockResultSeriesRow,
	heads []sqlc.OfficialResultHead,
	source sqlc.GetCorrectionSourceRow,
) error {
	if !correctionLockedGameMatches(in, attempt, source) || !correctionLockedSeriesMatches(in, series) {
		return domain.ErrConflict
	}
	if err := in.Score.Validate(domain.SeriesFormat(series.Format)); err != nil {
		return fmt.Errorf("%w: corrected series score: %w", domain.ErrValidation, err)
	}
	if !correctionSeriesWinnerMatches(in, series) {
		return fmt.Errorf("%w: corrected series winner does not match score", domain.ErrValidation)
	}
	gameHead, seriesHead := correctionHeads(heads, in.Scope)
	if gameHead == nil || gameHead.CurrentRevisionID != in.SourceRevisionID ||
		gameHead.Revision != source.HeadRevision {
		return domain.ErrConflict
	}
	if !correctionSeriesHeadMatches(in.ExpectedSeriesResultRevisionID, series, seriesHead) {
		return domain.ErrConflict
	}
	return nil
}

func correctionLockedGameMatches(
	in CorrectionInput,
	attempt sqlc.LockResultAttemptRow,
	source sqlc.GetCorrectionSourceRow,
) bool {
	return source.CurrentRevisionID == in.SourceRevisionID &&
		attempt.Revision == in.ExpectedAttemptRevision && attempt.State == string(in.ExpectedAttemptState) &&
		attempt.ResultRevisionID.Valid && attempt.ResultRevisionID.UUID == in.SourceRevisionID
}

func correctionLockedSeriesMatches(
	in CorrectionInput,
	series sqlc.LockResultSeriesRow,
) bool {
	return series.Revision == in.ExpectedSeriesRevision && series.State == string(in.ExpectedSeriesState) &&
		series.ScoreHeadRevisionID == in.ExpectedScoreRevisionID &&
		series.ScoreHeadRevision == in.ExpectedScoreRevision
}

func correctionSeriesHeadMatches(
	expected *uuid.UUID,
	series sqlc.LockResultSeriesRow,
	head *sqlc.OfficialResultHead,
) bool {
	if expected == nil {
		return head == nil && !series.CurrentResultRevisionID.Valid
	}
	return head != nil && head.CurrentRevisionID == *expected &&
		series.CurrentResultRevisionID.Valid && series.CurrentResultRevisionID.UUID == *expected
}
