package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const arenaCorrectionOutboxTopic = "arena.result.corrected"

var (
	ErrArenaCorrectionNotFound = errors.New("arena correction repository: source not found")
	ErrArenaCorrectionCutoff   = errors.New("arena correction repository: cutoff reached")
)

type ArenaCorrectionCutoffError struct {
	Code string
}

func (e *ArenaCorrectionCutoffError) Error() string {
	return fmt.Sprintf("%s: %s", ErrArenaCorrectionCutoff, e.Code)
}

func (e *ArenaCorrectionCutoffError) Unwrap() error {
	return ErrArenaCorrectionCutoff
}

type ArenaCorrectionPostgres struct {
	tx *TxManager
}

type ArenaCorrectionDescendant struct {
	ArtifactID           uuid.UUID
	ArtifactKind         string
	ProducedByRevisionID uuid.UUID
	RevisionID           uuid.UUID
	RevisionNumber       int64
	RevisionState        string
}

type ArenaCorrectionTraversal struct {
	SourceRevisionID uuid.UUID
	SourceIsCurrent  bool
	CutoffCode       string
	Descendants      []ArenaCorrectionDescendant
}

type ArenaCorrectionInput struct {
	IDs                            ArenaResultSettlementIDs
	ProjectionIDs                  ArenaProjectionIDs
	Scope                          ArenaResultScope
	SourceRevisionID               uuid.UUID
	ExpectedAttemptRevision        int64
	ExpectedAttemptState           domain.ArenaGameState
	ExpectedScoreRevisionID        uuid.UUID
	ExpectedScoreRevision          int64
	ExpectedSeriesRevision         int64
	ExpectedSeriesState            domain.ArenaSeriesState
	ExpectedSeriesResultRevisionID *uuid.UUID
	ExpectedProjectionRevisionID   uuid.UUID
	SubmissionEventID              uuid.UUID
	ResultServerSequence           int64
	GameState                      domain.ArenaGameState
	GameReason                     domain.ArenaGameResultReason
	GameWinnerID                   *uuid.UUID
	Score                          domain.ArenaSeriesScore
	NextSeriesState                domain.ArenaSeriesState
	SeriesResultReason             string
	SeriesWinnerID                 *uuid.UUID
	OperatorID                     uuid.UUID
	Reason                         string
	ProjectionArtifacts            []ArenaProjectionArtifactInput
	ReusedProjectionArtifactIDs    []uuid.UUID
	ProjectionPayloadDigest        [32]byte
	CorrectedAt                    time.Time
}

type ArenaCorrectionRecord struct {
	ResultCommit       *ArenaResultCommitRecord
	Projection         *ArenaProjectionRecord
	ClosedReadyWindows []uuid.UUID
}

type arenaCorrectionProjectionPlan struct {
	newArtifacts []ArenaProjectionArtifactInput
	reused       []sqlc.ArenaProjectionArtifact
	allKinds     []domain.ArenaArtifactKind
}

func NewArenaCorrectionPostgres(tx *TxManager) *ArenaCorrectionPostgres {
	return &ArenaCorrectionPostgres{tx: tx}
}

func (r *ArenaCorrectionPostgres) Traverse(
	ctx context.Context,
	scope ArenaResultScope,
	sourceRevisionID uuid.UUID,
) (*ArenaCorrectionTraversal, error) {
	if r == nil || r.tx == nil || !validArenaResultScope(scope) || sourceRevisionID == uuid.Nil {
		return nil, domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	source, err := querier.GetArenaCorrectionSource(ctx, arenaCorrectionSourceParams(scope, sourceRevisionID))
	if err != nil {
		return nil, arenaCorrectionLookupError("Traverse - source", err)
	}
	cutoff, err := querier.GetArenaCorrectionCutoff(ctx, arenaCorrectionCutoffParams(scope, sourceRevisionID))
	if err != nil {
		return nil, fmt.Errorf("ArenaCorrectionPostgres - Traverse - cutoff: %w", err)
	}
	traversal := &ArenaCorrectionTraversal{
		SourceRevisionID: sourceRevisionID,
		SourceIsCurrent:  source.CurrentRevisionID == sourceRevisionID,
		CutoffCode:       cutoff,
		Descendants:      []ArenaCorrectionDescendant{},
	}
	if cutoff != "" {
		return traversal, nil
	}
	rows, err := querier.ListArenaCorrectionDescendants(
		ctx,
		sqlc.ListArenaCorrectionDescendantsParams{
			TournamentID: scope.TournamentID, RosterID: scope.RosterID,
			SourceRevisionID: nullableUUIDValue(sourceRevisionID),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("ArenaCorrectionPostgres - Traverse - descendants: %w", err)
	}
	traversal.Descendants = make([]ArenaCorrectionDescendant, 0, len(rows))
	for _, row := range rows {
		traversal.Descendants = append(traversal.Descendants, arenaCorrectionDescendant(
			row.ArtifactID, row.ArtifactKind, row.ProducedByRevisionID,
			row.RevisionID, row.RevisionNumber, row.RevisionState,
		))
	}
	return traversal, nil
}

func (r *ArenaCorrectionPostgres) Rebuild(
	ctx context.Context,
	in ArenaCorrectionInput,
) (*ArenaCorrectionRecord, error) {
	if r == nil || r.tx == nil || !validArenaCorrectionInput(in) {
		return nil, domain.ErrValidation
	}
	preflight, err := r.Traverse(ctx, in.Scope, in.SourceRevisionID)
	if err != nil {
		return nil, err
	}
	if preflight.CutoffCode != "" {
		return nil, &ArenaCorrectionCutoffError{Code: preflight.CutoffCode}
	}
	if !preflight.SourceIsCurrent {
		return nil, domain.ErrConflict
	}

	var record *ArenaCorrectionRecord
	err = r.tx.Do(ctx, func(txCtx context.Context) error {
		var rebuildErr error
		record, rebuildErr = r.rebuildLocked(txCtx, in)
		return rebuildErr
	})
	if err != nil {
		return nil, err
	}
	return record, nil
}

type arenaCorrectionLockedResult struct {
	series sqlc.LockArenaResultSeriesRow
	heads  []sqlc.ArenaOfficialResultHead
	source sqlc.GetArenaCorrectionSourceRow
}

type arenaCorrectionLockedProjection struct {
	current sqlc.ArenaProjectionRevision
	plan    arenaCorrectionProjectionPlan
}

func (r *ArenaCorrectionPostgres) rebuildLocked(
	ctx context.Context,
	in ArenaCorrectionInput,
) (*ArenaCorrectionRecord, error) {
	querier := r.tx.Querier(ctx)
	if err := lockArenaCorrectionScope(ctx, querier, in.Scope); err != nil {
		return nil, err
	}
	result, err := lockArenaCorrectionResult(ctx, querier, in)
	if err != nil {
		return nil, err
	}
	projection, err := lockArenaCorrectionProjection(ctx, querier, in)
	if err != nil {
		return nil, err
	}
	windows, err := lockArenaCorrectionReadiness(ctx, querier, in)
	if err != nil {
		return nil, err
	}
	if err := createArenaCorrectionResultEvidence(
		ctx, querier, in, result.source, result.series, projection.plan.allKinds,
	); err != nil {
		return nil, err
	}
	if err := createArenaCorrectionProjection(ctx, querier, in, projection.current, projection.plan); err != nil {
		return nil, err
	}
	closed, err := closeArenaCorrectionReadiness(ctx, querier, in, windows)
	if err != nil {
		return nil, err
	}
	if err := advanceArenaCorrectionHeads(ctx, querier, in, result.source, result.heads); err != nil {
		return nil, err
	}
	return loadArenaCorrectionRecord(ctx, querier, in, closed)
}

func lockArenaCorrectionResult(
	ctx context.Context,
	querier *sqlc.Queries,
	in ArenaCorrectionInput,
) (arenaCorrectionLockedResult, error) {
	attempt, err := querier.LockArenaResultAttempt(ctx, arenaResultAttemptParams(in.Scope))
	if err != nil {
		return arenaCorrectionLockedResult{}, arenaCorrectionLookupError("Rebuild - attempt", err)
	}
	series, err := querier.LockArenaResultSeries(ctx, sqlc.LockArenaResultSeriesParams{
		SeriesID: in.Scope.SeriesID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
	})
	if err != nil {
		return arenaCorrectionLockedResult{}, arenaCorrectionLookupError("Rebuild - series", err)
	}
	heads, err := querier.LockArenaCorrectionOfficialHeads(ctx, sqlc.LockArenaCorrectionOfficialHeadsParams{
		RosterID: in.Scope.RosterID, AttemptID: in.Scope.AttemptID, SeriesID: in.Scope.SeriesID,
	})
	if err != nil {
		return arenaCorrectionLockedResult{}, fmt.Errorf("ArenaCorrectionPostgres - Rebuild - heads: %w", err)
	}
	source, err := querier.GetArenaCorrectionSource(ctx, arenaCorrectionSourceParams(in.Scope, in.SourceRevisionID))
	if err != nil {
		return arenaCorrectionLockedResult{}, arenaCorrectionLookupError("Rebuild - source", err)
	}
	if err := validateArenaCorrectionLockedResult(in, attempt, series, heads, source); err != nil {
		return arenaCorrectionLockedResult{}, err
	}
	return arenaCorrectionLockedResult{series: series, heads: heads, source: source}, nil
}

func lockArenaCorrectionProjection(
	ctx context.Context,
	querier *sqlc.Queries,
	in ArenaCorrectionInput,
) (arenaCorrectionLockedProjection, error) {
	if _, err := querier.LockArenaProjectionRevisionSet(ctx, sqlc.LockArenaProjectionRevisionSetParams{
		TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
	}); err != nil {
		return arenaCorrectionLockedProjection{}, fmt.Errorf(
			"ArenaCorrectionPostgres - Rebuild - projection revisions: %w", err,
		)
	}
	current, err := querier.GetCurrentArenaProjectionRevision(ctx, sqlc.GetCurrentArenaProjectionRevisionParams{
		TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
	})
	if err != nil {
		return arenaCorrectionLockedProjection{}, arenaCorrectionLookupError("Rebuild - current projection", err)
	}
	if current.ID != in.ExpectedProjectionRevisionID {
		return arenaCorrectionLockedProjection{}, domain.ErrConflict
	}
	descendants, err := querier.LockArenaCorrectionDescendants(ctx, sqlc.LockArenaCorrectionDescendantsParams{
		TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		SourceRevisionID: nullableUUIDValue(in.SourceRevisionID),
	})
	if err != nil {
		return arenaCorrectionLockedProjection{}, fmt.Errorf(
			"ArenaCorrectionPostgres - Rebuild - descendants: %w", err,
		)
	}
	plan, err := prepareArenaCorrectionProjectionPlan(ctx, querier, in, current, descendants)
	return arenaCorrectionLockedProjection{current: current, plan: plan}, err
}

func lockArenaCorrectionReadiness(
	ctx context.Context,
	querier *sqlc.Queries,
	in ArenaCorrectionInput,
) ([]sqlc.LockArenaCorrectionOpenReadyWindowsRow, error) {
	windows, err := querier.LockArenaCorrectionOpenReadyWindows(ctx, sqlc.LockArenaCorrectionOpenReadyWindowsParams{
		TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
	})
	if err != nil {
		return nil, fmt.Errorf("ArenaCorrectionPostgres - Rebuild - ready windows: %w", err)
	}
	cutoff, err := querier.GetArenaCorrectionCutoff(ctx, arenaCorrectionCutoffParams(in.Scope, in.SourceRevisionID))
	if err != nil {
		return nil, fmt.Errorf("ArenaCorrectionPostgres - Rebuild - cutoff recheck: %w", err)
	}
	if cutoff != "" {
		return nil, &ArenaCorrectionCutoffError{Code: cutoff}
	}
	return windows, nil
}

func loadArenaCorrectionRecord(
	ctx context.Context,
	querier *sqlc.Queries,
	in ArenaCorrectionInput,
	closed []uuid.UUID,
) (*ArenaCorrectionRecord, error) {
	commit, err := querier.GetArenaResultCommitByID(ctx, in.IDs.CommitID)
	if err != nil {
		return nil, fmt.Errorf("ArenaCorrectionPostgres - Rebuild - result commit: %w", err)
	}
	result, err := loadArenaResultCommit(ctx, querier, commit)
	if err != nil {
		return nil, err
	}
	projection, err := loadArenaProjectionRecord(
		ctx,
		querier,
		ArenaProjectionScope{TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID},
		in.ProjectionIDs.RevisionID,
	)
	if err != nil {
		return nil, err
	}
	return &ArenaCorrectionRecord{
		ResultCommit: result, Projection: projection, ClosedReadyWindows: closed,
	}, nil
}

func lockArenaCorrectionScope(
	ctx context.Context,
	querier *sqlc.Queries,
	scope ArenaResultScope,
) error {
	if _, err := querier.LockArenaCorrectionTournamentScope(
		ctx,
		sqlc.LockArenaCorrectionTournamentScopeParams{
			TournamentID: scope.TournamentID, RosterID: scope.RosterID,
		},
	); err != nil {
		return arenaCorrectionLookupError("lock Tournament scope", err)
	}
	if _, err := querier.LockArenaCorrectionSeriesAttempts(
		ctx,
		sqlc.LockArenaCorrectionSeriesAttemptsParams{
			SeriesID: scope.SeriesID, TournamentID: scope.TournamentID, RosterID: scope.RosterID,
		},
	); err != nil {
		return fmt.Errorf("ArenaCorrectionPostgres - lock attempts: %w", err)
	}
	if _, err := querier.LockArenaCorrectionCutoffWaves(
		ctx,
		sqlc.LockArenaCorrectionCutoffWavesParams{
			TournamentID: scope.TournamentID, RosterID: scope.RosterID,
		},
	); err != nil {
		return fmt.Errorf("ArenaCorrectionPostgres - lock Waves: %w", err)
	}
	if _, err := querier.LockArenaCorrectionAssignments(
		ctx,
		sqlc.LockArenaCorrectionAssignmentsParams{
			TournamentID: scope.TournamentID, RosterID: scope.RosterID,
		},
	); err != nil {
		return fmt.Errorf("ArenaCorrectionPostgres - lock assignments: %w", err)
	}
	if _, err := querier.LockArenaCorrectionGoldenAttempts(
		ctx,
		sqlc.LockArenaCorrectionGoldenAttemptsParams{
			TournamentID: scope.TournamentID, RosterID: scope.RosterID,
		},
	); err != nil {
		return fmt.Errorf("ArenaCorrectionPostgres - lock Golden: %w", err)
	}
	return nil
}

func validateArenaCorrectionLockedResult(
	in ArenaCorrectionInput,
	attempt sqlc.ArenaGameAttempt,
	series sqlc.LockArenaResultSeriesRow,
	heads []sqlc.ArenaOfficialResultHead,
	source sqlc.GetArenaCorrectionSourceRow,
) error {
	if !arenaCorrectionLockedGameMatches(in, attempt, source) || !arenaCorrectionLockedSeriesMatches(in, series) {
		return domain.ErrConflict
	}
	if err := in.Score.Validate(domain.ArenaSeriesFormat(series.Format)); err != nil {
		return domain.ErrValidation
	}
	if !arenaCorrectionSeriesWinnerMatches(in, series) {
		return domain.ErrValidation
	}
	gameHead, seriesHead := arenaCorrectionHeads(heads, in.Scope)
	if gameHead == nil || gameHead.CurrentRevisionID != in.SourceRevisionID ||
		gameHead.Revision != source.HeadRevision {
		return domain.ErrConflict
	}
	if !arenaCorrectionSeriesHeadMatches(in.ExpectedSeriesResultRevisionID, series, seriesHead) {
		return domain.ErrConflict
	}
	return nil
}

func arenaCorrectionLockedGameMatches(
	in ArenaCorrectionInput,
	attempt sqlc.ArenaGameAttempt,
	source sqlc.GetArenaCorrectionSourceRow,
) bool {
	return source.CurrentRevisionID == in.SourceRevisionID &&
		attempt.Revision == in.ExpectedAttemptRevision && attempt.State == string(in.ExpectedAttemptState) &&
		attempt.ResultRevisionID.Valid && attempt.ResultRevisionID.UUID == in.SourceRevisionID
}

func arenaCorrectionLockedSeriesMatches(
	in ArenaCorrectionInput,
	series sqlc.LockArenaResultSeriesRow,
) bool {
	return series.Revision == in.ExpectedSeriesRevision && series.State == string(in.ExpectedSeriesState) &&
		series.ScoreHeadRevisionID == in.ExpectedScoreRevisionID &&
		series.ScoreHeadRevision == in.ExpectedScoreRevision
}

func arenaCorrectionSeriesHeadMatches(
	expected *uuid.UUID,
	series sqlc.LockArenaResultSeriesRow,
	head *sqlc.ArenaOfficialResultHead,
) bool {
	if expected == nil {
		return head == nil && !series.CurrentResultRevisionID.Valid
	}
	return head != nil && head.CurrentRevisionID == *expected &&
		series.CurrentResultRevisionID.Valid && series.CurrentResultRevisionID.UUID == *expected
}

func prepareArenaCorrectionProjectionPlan(
	ctx context.Context,
	querier *sqlc.Queries,
	in ArenaCorrectionInput,
	current sqlc.ArenaProjectionRevision,
	descendants []sqlc.LockArenaCorrectionDescendantsRow,
) (arenaCorrectionProjectionPlan, error) {
	affected := make(map[uuid.UUID]struct{}, len(descendants))
	for _, row := range descendants {
		affected[row.ArtifactID] = struct{}{}
	}
	links, err := querier.ListArenaProjectionRevisionArtifacts(
		ctx,
		sqlc.ListArenaProjectionRevisionArtifactsParams{
			RevisionID: current.ID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		},
	)
	if err != nil {
		return arenaCorrectionProjectionPlan{}, fmt.Errorf("ArenaCorrectionPostgres - projection links: %w", err)
	}
	newByKind, err := arenaCorrectionNewArtifacts(in.ProjectionArtifacts)
	if err != nil {
		return arenaCorrectionProjectionPlan{}, err
	}
	reusedWanted, err := arenaCorrectionReusedArtifacts(in.ReusedProjectionArtifactIDs)
	if err != nil {
		return arenaCorrectionProjectionPlan{}, err
	}
	plan, err := classifyArenaCorrectionProjectionArtifacts(
		ctx, querier, in, links, affected, newByKind, reusedWanted,
	)
	if err != nil {
		return arenaCorrectionProjectionPlan{}, err
	}
	if !validArenaCorrectionProjectionPlan(in, plan, links, affected, newByKind, reusedWanted) {
		return arenaCorrectionProjectionPlan{}, domain.ErrValidation
	}
	return plan, nil
}

func arenaCorrectionNewArtifacts(
	artifacts []ArenaProjectionArtifactInput,
) (map[string]ArenaProjectionArtifactInput, error) {
	byKind := make(map[string]ArenaProjectionArtifactInput, len(artifacts))
	for _, artifact := range artifacts {
		kind := arenaProjectionArtifactKind(artifact.Kind)
		if _, exists := byKind[kind]; exists || !validArenaProjectionArtifact(artifact) {
			return nil, domain.ErrValidation
		}
		byKind[kind] = artifact
	}
	return byKind, nil
}

func arenaCorrectionReusedArtifacts(ids []uuid.UUID) (map[uuid.UUID]struct{}, error) {
	wanted := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		if id == uuid.Nil {
			return nil, domain.ErrValidation
		}
		if _, exists := wanted[id]; exists {
			return nil, domain.ErrValidation
		}
		wanted[id] = struct{}{}
	}
	return wanted, nil
}

func classifyArenaCorrectionProjectionArtifacts(
	ctx context.Context,
	querier *sqlc.Queries,
	in ArenaCorrectionInput,
	links []sqlc.ArenaProjectionRevisionArtifact,
	affected map[uuid.UUID]struct{},
	newByKind map[string]ArenaProjectionArtifactInput,
	reusedWanted map[uuid.UUID]struct{},
) (arenaCorrectionProjectionPlan, error) {
	plan := arenaCorrectionProjectionPlan{newArtifacts: in.ProjectionArtifacts}
	for _, link := range links {
		artifact, loadErr := querier.GetArenaProjectionArtifactScoped(
			ctx,
			sqlc.GetArenaProjectionArtifactScopedParams{
				ID: link.ArtifactID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
			},
		)
		if loadErr != nil {
			return arenaCorrectionProjectionPlan{}, arenaCorrectionLookupError("projection artifact", loadErr)
		}
		_, isAffected := affected[artifact.ID]
		_, isReused := reusedWanted[artifact.ID]
		_, hasReplacement := newByKind[artifact.ArtifactKind]
		if isAffected {
			if isReused || !hasReplacement {
				return arenaCorrectionProjectionPlan{}, domain.ErrValidation
			}
			delete(newByKind, artifact.ArtifactKind)
		} else {
			if !isReused || hasReplacement {
				return arenaCorrectionProjectionPlan{}, domain.ErrValidation
			}
			delete(reusedWanted, artifact.ID)
			plan.reused = append(plan.reused, artifact)
		}
		plan.allKinds = append(plan.allKinds, arenaCorrectionDomainArtifactKind(artifact.ArtifactKind))
	}
	return plan, nil
}

func validArenaCorrectionProjectionPlan(
	in ArenaCorrectionInput,
	plan arenaCorrectionProjectionPlan,
	links []sqlc.ArenaProjectionRevisionArtifact,
	affected map[uuid.UUID]struct{},
	newByKind map[string]ArenaProjectionArtifactInput,
	reusedWanted map[uuid.UUID]struct{},
) bool {
	return len(links) == 4 && len(affected) > 0 && len(newByKind) == 0 && len(reusedWanted) == 0 &&
		len(plan.newArtifacts)+len(plan.reused) == 4 && validArenaArtifactKinds(plan.allKinds) &&
		arenaCorrectionHasNewSource(in)
}

func arenaCorrectionDomainArtifactKind(kind string) domain.ArenaArtifactKind {
	if kind == "top4" {
		return domain.ArenaArtifactKindTopFour
	}
	return domain.ArenaArtifactKind(kind)
}

func createArenaCorrectionResultEvidence(
	ctx context.Context,
	querier *sqlc.Queries,
	in ArenaCorrectionInput,
	source sqlc.GetArenaCorrectionSourceRow,
	series sqlc.LockArenaResultSeriesRow,
	artifactKinds []domain.ArenaArtifactKind,
) error {
	event, err := querier.CreateArenaResultEvent(ctx, sqlc.CreateArenaResultEventParams{
		ID: in.IDs.ResultEventID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		SeriesID: in.Scope.SeriesID, AttemptID: in.Scope.AttemptID,
		SubmissionEventID: nullableUUIDValue(in.SubmissionEventID), ServerSequence: in.ResultServerSequence,
		IdempotencyKey: in.IDs.ResultEventIdempotencyKey, ResultState: string(in.GameState),
		ResultReason: string(in.GameReason), WinnerID: nullableUUID(in.GameWinnerID),
		OccurredAt: tstz(in.CorrectedAt), CreatedAt: tstz(in.CorrectedAt),
	})
	if err != nil {
		return mapArenaRepositoryWriteError("ArenaCorrectionPostgres - create result event", err)
	}
	if _, err = querier.CreateArenaOfficialResultRevision(ctx, sqlc.CreateArenaOfficialResultRevisionParams{
		ID: in.IDs.GameResultRevisionID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		EntityKind: "game_attempt", EntityID: in.Scope.AttemptID, SeriesID: in.Scope.SeriesID,
		GameAttemptID: nullableUUIDValue(in.Scope.AttemptID), ResultEventID: event.ID,
		PreviousRevisionID: nullableUUIDValue(in.SourceRevisionID), RevisionNumber: source.RevisionNumber + 1,
		ResultState: string(in.GameState), ResultReason: string(in.GameReason),
		WinnerID: nullableUUID(in.GameWinnerID), CreatedAt: tstz(in.CorrectedAt),
	}); err != nil {
		return mapArenaRepositoryWriteError("ArenaCorrectionPostgres - create Game revision", err)
	}
	if _, err = querier.CreateArenaSeriesScoreRevision(ctx, sqlc.CreateArenaSeriesScoreRevisionParams{
		ID: in.IDs.SeriesScoreRevisionID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		SeriesID: in.Scope.SeriesID, ResultEventID: nullableUUIDValue(event.ID),
		PreviousRevisionID:    nullableUUIDValue(in.ExpectedScoreRevisionID),
		RevisionNumber:        series.ScoreHeadRevision + 1,
		FirstParticipantWins:  int16(in.Score.FirstParticipantWins),  //nolint:gosec // validated against BO1 or BO3 bounds.
		SecondParticipantWins: int16(in.Score.SecondParticipantWins), //nolint:gosec // validated against BO1 or BO3 bounds.
		CreatedAt:             tstz(in.CorrectedAt),
	}); err != nil {
		return mapArenaRepositoryWriteError("ArenaCorrectionPostgres - create score revision", err)
	}
	seriesResultID := uuid.NullUUID{}
	if in.IDs.SeriesResultRevisionID != uuid.Nil {
		seriesResultID = nullableUUIDValue(in.IDs.SeriesResultRevisionID)
		previousID := nullableUUID(in.ExpectedSeriesResultRevisionID)
		revisionNumber := int64(1)
		if in.ExpectedSeriesResultRevisionID != nil {
			previous, loadErr := querier.GetArenaOfficialResultRevisionByID(ctx, *in.ExpectedSeriesResultRevisionID)
			if loadErr != nil {
				return arenaCorrectionLookupError("Series revision", loadErr)
			}
			revisionNumber = previous.RevisionNumber + 1
		}
		if _, err = querier.CreateArenaOfficialResultRevision(ctx, sqlc.CreateArenaOfficialResultRevisionParams{
			ID: in.IDs.SeriesResultRevisionID, TournamentID: in.Scope.TournamentID,
			RosterID: in.Scope.RosterID, EntityKind: "series", EntityID: in.Scope.SeriesID,
			SeriesID: in.Scope.SeriesID, ResultEventID: event.ID, PreviousRevisionID: previousID,
			RevisionNumber: revisionNumber, ResultState: string(in.NextSeriesState),
			ResultReason: in.SeriesResultReason, WinnerID: nullableUUID(in.SeriesWinnerID),
			CreatedAt: tstz(in.CorrectedAt),
		}); err != nil {
			return mapArenaRepositoryWriteError("ArenaCorrectionPostgres - create Series revision", err)
		}
	}
	return createArenaCorrectionSideEvidence(ctx, querier, in, event.ID, seriesResultID, artifactKinds)
}

func createArenaCorrectionSideEvidence(
	ctx context.Context,
	querier *sqlc.Queries,
	in ArenaCorrectionInput,
	resultEventID uuid.UUID,
	seriesResultID uuid.NullUUID,
	artifactKinds []domain.ArenaArtifactKind,
) error {
	kinds := make([]string, len(artifactKinds))
	for i, kind := range artifactKinds {
		kinds[i] = string(kind)
	}
	auditPayload := mustJSON(map[string]any{
		"attempt_id": in.Scope.AttemptID.String(), "entity_id": in.Scope.AttemptID.String(),
		"entity_kind": "game_attempt", "previous_revision_id": in.SourceRevisionID.String(),
		"projection_revision_id": in.ProjectionIDs.RevisionID.String(), "reason": in.Reason,
		"result_reason": string(in.GameReason), "state": string(in.GameState),
	})
	outboxPayload := mustJSON(map[string]any{
		"result_event_id": resultEventID.String(), "series_id": in.Scope.SeriesID.String(),
		"tournament_id": in.Scope.TournamentID.String(),
	})
	if _, err := querier.CreateArenaAuditEvent(ctx, sqlc.CreateArenaAuditEventParams{
		ID: in.IDs.AuditEventID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		SeriesID: in.Scope.SeriesID, ResultEventID: resultEventID,
		ActorKind: arenaResultActorOperator, ActorID: nullableUUIDValue(in.OperatorID),
		Action: arenaCorrectionOutboxTopic, Payload: auditPayload,
		OccurredAt: tstz(in.CorrectedAt), CreatedAt: tstz(in.CorrectedAt),
	}); err != nil {
		return mapArenaRepositoryWriteError("ArenaCorrectionPostgres - create audit", err)
	}
	if _, err := querier.CreateArenaResultProjectionEvidence(ctx, sqlc.CreateArenaResultProjectionEvidenceParams{
		ID: in.IDs.ProjectionEvidenceID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		SeriesID: in.Scope.SeriesID, ResultEventID: resultEventID, ArtifactKinds: mustJSON(kinds),
		PayloadDigest: append([]byte(nil), in.ProjectionPayloadDigest[:]...), CreatedAt: tstz(in.CorrectedAt),
	}); err != nil {
		return mapArenaRepositoryWriteError("ArenaCorrectionPostgres - create projection evidence", err)
	}
	if _, err := querier.CreateArenaOutboxEvent(ctx, sqlc.CreateArenaOutboxEventParams{
		ID: in.IDs.OutboxEventID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		SeriesID: in.Scope.SeriesID, ResultEventID: resultEventID,
		IdempotencyKey: in.IDs.OutboxIdempotencyKey, Topic: arenaCorrectionOutboxTopic,
		Payload: outboxPayload, CreatedAt: tstz(in.CorrectedAt),
	}); err != nil {
		return mapArenaRepositoryWriteError("ArenaCorrectionPostgres - create outbox", err)
	}
	if _, err := querier.CreateArenaResultCommit(ctx, sqlc.CreateArenaResultCommitParams{
		ID: in.IDs.CommitID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		SeriesID: in.Scope.SeriesID, AttemptID: in.Scope.AttemptID, ResultEventID: resultEventID,
		GameResultRevisionID:  in.IDs.GameResultRevisionID,
		SeriesScoreRevisionID: in.IDs.SeriesScoreRevisionID, SeriesResultRevisionID: seriesResultID,
		AuditEventID: in.IDs.AuditEventID, OutboxEventID: in.IDs.OutboxEventID,
		ProjectionEvidenceID: in.IDs.ProjectionEvidenceID,
		IdempotencyKey:       in.IDs.CommitIdempotencyKey, CreatedAt: tstz(in.CorrectedAt),
	}); err != nil {
		return mapArenaRepositoryWriteError("ArenaCorrectionPostgres - create commit", err)
	}
	return nil
}

func createArenaCorrectionProjection(
	ctx context.Context,
	querier *sqlc.Queries,
	in ArenaCorrectionInput,
	current sqlc.ArenaProjectionRevision,
	plan arenaCorrectionProjectionPlan,
) error {
	projectionInput := ArenaProjectionPublishInput{
		IDs:       in.ProjectionIDs,
		Scope:     ArenaProjectionScope{TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID},
		Source:    ArenaProjectionSource{Kind: arenaProjectionSourceOperatorRebuild, Reason: in.Reason},
		Artifacts: plan.newArtifacts, SupersessionReason: in.Reason,
		CutoffAt: in.CorrectedAt, CreatedAt: in.CorrectedAt, PublishedAt: in.CorrectedAt,
	}
	if _, err := querier.CreateArenaProjectionCutoff(ctx, sqlc.CreateArenaProjectionCutoffParams{
		ID: in.ProjectionIDs.CutoffID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		SequenceNumber: current.RevisionNumber + 1, PreviousCutoffID: nullableUUIDValue(current.CutoffID),
		SourceKind: arenaProjectionSourceOperatorRebuild, Reason: in.Reason,
		CutoffAt: tstz(in.CorrectedAt), CreatedAt: tstz(in.CorrectedAt),
	}); err != nil {
		return mapArenaRepositoryWriteError("ArenaCorrectionPostgres - create cutoff", err)
	}
	if _, err := querier.CreateArenaProjectionRevision(ctx, sqlc.CreateArenaProjectionRevisionParams{
		ID: in.ProjectionIDs.RevisionID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		RevisionNumber: current.RevisionNumber + 1, PreviousRevisionID: nullableUUIDValue(current.ID),
		CutoffID: in.ProjectionIDs.CutoffID, CreatedAt: tstz(in.CorrectedAt),
	}); err != nil {
		return mapArenaRepositoryWriteError("ArenaCorrectionPostgres - create projection revision", err)
	}
	for _, artifact := range plan.newArtifacts {
		if err := createArenaProjectionArtifact(ctx, querier, projectionInput, artifact); err != nil {
			return err
		}
	}
	for _, artifact := range plan.reused {
		if _, err := querier.LinkArenaProjectionArtifact(ctx, sqlc.LinkArenaProjectionArtifactParams{
			RevisionID: in.ProjectionIDs.RevisionID, TournamentID: in.Scope.TournamentID,
			RosterID: in.Scope.RosterID, ArtifactKind: artifact.ArtifactKind,
			ArtifactID: artifact.ID, ChangeKind: "reused", CreatedAt: tstz(in.CorrectedAt),
		}); err != nil {
			return mapArenaRepositoryWriteError("ArenaCorrectionPostgres - reuse artifact", err)
		}
	}
	if _, err := querier.SupersedeArenaProjectionRevisionCAS(
		ctx,
		sqlc.SupersedeArenaProjectionRevisionCASParams{
			SupersededByRevisionID: nullableUUIDValue(in.ProjectionIDs.RevisionID),
			SupersededAt:           tstz(in.CorrectedAt), SupersessionReason: &in.Reason,
			ID: current.ID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		},
	); err != nil {
		return arenaProjectionCASWriteError("correction supersession", err)
	}
	if _, err := querier.PublishArenaProjectionRevisionCAS(
		ctx,
		sqlc.PublishArenaProjectionRevisionCASParams{
			PublishedAt: tstz(in.CorrectedAt), ID: in.ProjectionIDs.RevisionID,
			TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		},
	); err != nil {
		return arenaProjectionCASWriteError("correction publish", err)
	}
	return nil
}

func closeArenaCorrectionReadiness(
	ctx context.Context,
	querier *sqlc.Queries,
	in ArenaCorrectionInput,
	windows []sqlc.LockArenaCorrectionOpenReadyWindowsRow,
) ([]uuid.UUID, error) {
	closed := make([]uuid.UUID, 0, len(windows))
	for _, window := range windows {
		if _, err := querier.CloseArenaReadyWindowCAS(ctx, sqlc.CloseArenaReadyWindowCASParams{
			NextState: "superseded", ID: window.ReadyWindowID,
			WaveID: window.WaveID, ExpectedState: window.ReadyWindowState,
		}); err != nil {
			return nil, arenaResultCASWriteError("close correction readiness", err)
		}
		if _, err := querier.TransitionArenaWaveCAS(ctx, sqlc.TransitionArenaWaveCASParams{
			NextState: "superseded", UpdatedAt: tstz(in.CorrectedAt), ClosedAt: tstz(in.CorrectedAt),
			ID: window.WaveID, TournamentID: in.Scope.TournamentID,
			ExpectedRevision: window.WaveRevision, ExpectedState: window.WaveState,
		}); err != nil {
			return nil, arenaResultCASWriteError("supersede correction Wave", err)
		}
		closed = append(closed, window.ReadyWindowID)
	}
	return closed, nil
}

func advanceArenaCorrectionHeads(
	ctx context.Context,
	querier *sqlc.Queries,
	in ArenaCorrectionInput,
	source sqlc.GetArenaCorrectionSourceRow,
	heads []sqlc.ArenaOfficialResultHead,
) error {
	if _, err := querier.AdvanceArenaCorrectionOfficialHeadCAS(
		ctx,
		sqlc.AdvanceArenaCorrectionOfficialHeadCASParams{
			CurrentRevisionID: in.IDs.GameResultRevisionID, UpdatedAt: tstz(in.CorrectedAt),
			EntityKind: "game_attempt", EntityID: in.Scope.AttemptID, RosterID: in.Scope.RosterID,
			ExpectedRevisionID: in.SourceRevisionID, ExpectedRevision: source.HeadRevision,
		},
	); err != nil {
		return arenaResultCASWriteError("advance corrected Game head", err)
	}
	_, seriesHead := arenaCorrectionHeads(heads, in.Scope)
	seriesResultID := uuid.NullUUID{}
	if in.IDs.SeriesResultRevisionID != uuid.Nil {
		seriesResultID = nullableUUIDValue(in.IDs.SeriesResultRevisionID)
		if seriesHead == nil {
			return domain.ErrConflict
		}
		if _, err := querier.AdvanceArenaCorrectionOfficialHeadCAS(
			ctx,
			sqlc.AdvanceArenaCorrectionOfficialHeadCASParams{
				CurrentRevisionID: in.IDs.SeriesResultRevisionID, UpdatedAt: tstz(in.CorrectedAt),
				EntityKind: "series", EntityID: in.Scope.SeriesID, RosterID: in.Scope.RosterID,
				ExpectedRevisionID: seriesHead.CurrentRevisionID, ExpectedRevision: seriesHead.Revision,
			},
		); err != nil {
			return arenaResultCASWriteError("advance corrected Series head", err)
		}
	}
	if _, err := querier.AdvanceArenaSeriesScoreHeadCAS(ctx, sqlc.AdvanceArenaSeriesScoreHeadCASParams{
		CurrentRevisionID: in.IDs.SeriesScoreRevisionID, UpdatedAt: tstz(in.CorrectedAt),
		SeriesID: in.Scope.SeriesID, RosterID: in.Scope.RosterID,
		ExpectedRevisionID: in.ExpectedScoreRevisionID, ExpectedRevision: in.ExpectedScoreRevision,
	}); err != nil {
		return arenaResultCASWriteError("advance corrected score head", err)
	}
	if _, err := querier.CorrectArenaGameAttemptCAS(ctx, sqlc.CorrectArenaGameAttemptCASParams{
		ResultState: string(in.GameState), ResultReason: optionalTrimmedString(string(in.GameReason)),
		WinnerID: nullableUUID(in.GameWinnerID), ResultRevisionID: nullableUUIDValue(in.IDs.GameResultRevisionID),
		CorrectedAt: tstz(in.CorrectedAt), AttemptID: in.Scope.AttemptID,
		SeriesID: in.Scope.SeriesID, RosterID: in.Scope.RosterID, TournamentID: in.Scope.TournamentID,
		ExpectedRevision: in.ExpectedAttemptRevision, ExpectedState: string(in.ExpectedAttemptState),
		ExpectedResultRevisionID: nullableUUIDValue(in.SourceRevisionID),
	}); err != nil {
		return arenaResultCASWriteError("correct Game", err)
	}
	if _, err := querier.CorrectArenaSeriesCAS(ctx, sqlc.CorrectArenaSeriesCASParams{
		NextState:             string(in.NextSeriesState),
		FirstParticipantWins:  int16(in.Score.FirstParticipantWins),  //nolint:gosec // validated against BO1 or BO3 bounds.
		SecondParticipantWins: int16(in.Score.SecondParticipantWins), //nolint:gosec // validated against BO1 or BO3 bounds.
		WinnerID:              nullableUUID(in.SeriesWinnerID), ScoreRevisionID: nullableUUIDValue(in.IDs.SeriesScoreRevisionID),
		ResultRevisionID: seriesResultID, CorrectedAt: tstz(in.CorrectedAt),
		SeriesID: in.Scope.SeriesID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		ExpectedRevision: in.ExpectedSeriesRevision, ExpectedState: string(in.ExpectedSeriesState),
		ExpectedScoreRevisionID:  nullableUUIDValue(in.ExpectedScoreRevisionID),
		ExpectedResultRevisionID: nullableUUID(in.ExpectedSeriesResultRevisionID),
	}); err != nil {
		return arenaResultCASWriteError("correct Series", err)
	}
	return nil
}

func validArenaCorrectionInput(in ArenaCorrectionInput) bool {
	if !validArenaCorrectionIdentity(in) || !validArenaCorrectionMetadata(in) ||
		!validArenaCorrectionStates(in) || !validArenaCorrectionGame(in) {
		return false
	}
	return validArenaCorrectionSeries(in)
}

func validArenaCorrectionIdentity(in ArenaCorrectionInput) bool {
	return validArenaResultScope(in.Scope) && validArenaResultSettlementIDs(in.IDs) &&
		in.ProjectionIDs.RevisionID != uuid.Nil && in.ProjectionIDs.CutoffID != uuid.Nil &&
		in.SourceRevisionID != uuid.Nil && in.ExpectedProjectionRevisionID != uuid.Nil &&
		in.ExpectedScoreRevisionID != uuid.Nil && in.OperatorID != uuid.Nil
}

func validArenaCorrectionMetadata(in ArenaCorrectionInput) bool {
	return in.ExpectedAttemptRevision >= 1 && in.ExpectedScoreRevision >= 1 && in.ExpectedSeriesRevision >= 1 &&
		in.ResultServerSequence >= 1 && validServerTime(in.CorrectedAt) && validTrimmedText(in.Reason) &&
		len(in.Reason) <= 512 && !zeroDigest(in.ProjectionPayloadDigest[:]) && len(in.ProjectionArtifacts) > 0
}

func validArenaCorrectionStates(in ArenaCorrectionInput) bool {
	return in.ExpectedAttemptState.IsTerminal() && in.GameState.IsTerminal() &&
		in.GameReason.IsLegalFor(in.GameState) && in.ExpectedSeriesState.IsValid() &&
		in.NextSeriesState.IsValid() &&
		in.ExpectedSeriesState.IsTerminal() == in.NextSeriesState.IsTerminal()
}

func validArenaCorrectionGame(in ArenaCorrectionInput) bool {
	if in.GameState == domain.ArenaGameStateCompleted {
		if in.GameWinnerID == nil || *in.GameWinnerID == uuid.Nil {
			return false
		}
	} else if in.GameWinnerID != nil {
		return false
	}
	if in.GameReason == domain.ArenaGameResultReasonSolved && in.SubmissionEventID == uuid.Nil {
		return false
	}
	return true
}

func validArenaCorrectionSeries(in ArenaCorrectionInput) bool {
	if in.ExpectedSeriesState.IsTerminal() {
		return in.ExpectedSeriesResultRevisionID != nil && *in.ExpectedSeriesResultRevisionID != uuid.Nil &&
			in.IDs.SeriesResultRevisionID != uuid.Nil && validArenaSeriesResultReason(in.SeriesResultReason)
	}
	return in.ExpectedSeriesResultRevisionID == nil && in.IDs.SeriesResultRevisionID == uuid.Nil &&
		in.SeriesResultReason == "" && in.SeriesWinnerID == nil
}

func arenaCorrectionSeriesWinnerMatches(
	in ArenaCorrectionInput,
	series sqlc.LockArenaResultSeriesRow,
) bool {
	if in.NextSeriesState == domain.ArenaSeriesStateCancelled {
		return in.SeriesWinnerID == nil
	}
	if !in.NextSeriesState.IsTerminal() {
		return in.SeriesWinnerID == nil
	}
	winner := in.Score.Winner(series.FirstParticipantID, series.SecondParticipantID, domain.ArenaSeriesFormat(series.Format))
	return winner != nil && in.SeriesWinnerID != nil && *winner == *in.SeriesWinnerID
}

func arenaCorrectionHeads(
	heads []sqlc.ArenaOfficialResultHead,
	scope ArenaResultScope,
) (*sqlc.ArenaOfficialResultHead, *sqlc.ArenaOfficialResultHead) {
	var gameHead *sqlc.ArenaOfficialResultHead
	var seriesHead *sqlc.ArenaOfficialResultHead
	for i := range heads {
		head := &heads[i]
		switch {
		case head.EntityKind == "game_attempt" && head.EntityID == scope.AttemptID:
			gameHead = head
		case head.EntityKind == "series" && head.EntityID == scope.SeriesID:
			seriesHead = head
		}
	}
	return gameHead, seriesHead
}

func arenaCorrectionHasNewSource(in ArenaCorrectionInput) bool {
	for _, artifact := range in.ProjectionArtifacts {
		for _, dependency := range artifact.Dependencies {
			if dependency.OfficialResultRevisionID != nil &&
				(*dependency.OfficialResultRevisionID == in.IDs.GameResultRevisionID ||
					*dependency.OfficialResultRevisionID == in.IDs.SeriesResultRevisionID) {
				return true
			}
		}
	}
	return false
}

func arenaCorrectionSourceParams(
	scope ArenaResultScope,
	sourceRevisionID uuid.UUID,
) sqlc.GetArenaCorrectionSourceParams {
	return sqlc.GetArenaCorrectionSourceParams{
		SourceRevisionID: sourceRevisionID, TournamentID: scope.TournamentID,
		RosterID: scope.RosterID, AttemptID: scope.AttemptID, SeriesID: scope.SeriesID,
	}
}

func arenaCorrectionCutoffParams(
	scope ArenaResultScope,
	sourceRevisionID uuid.UUID,
) sqlc.GetArenaCorrectionCutoffParams {
	return sqlc.GetArenaCorrectionCutoffParams{
		RosterID: scope.RosterID, SeriesID: scope.SeriesID,
		TournamentID: scope.TournamentID, SourceRevisionID: sourceRevisionID,
	}
}

func arenaCorrectionDescendant(
	artifactID uuid.UUID,
	artifactKind string,
	producedByRevisionID uuid.UUID,
	revisionID uuid.UUID,
	revisionNumber int64,
	revisionState string,
) ArenaCorrectionDescendant {
	return ArenaCorrectionDescendant{
		ArtifactID: artifactID, ArtifactKind: artifactKind,
		ProducedByRevisionID: producedByRevisionID, RevisionID: revisionID,
		RevisionNumber: revisionNumber, RevisionState: revisionState,
	}
}

func arenaCorrectionLookupError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: %s", ErrArenaCorrectionNotFound, operation)
	}
	return fmt.Errorf("ArenaCorrectionPostgres - %s: %w", operation, err)
}
