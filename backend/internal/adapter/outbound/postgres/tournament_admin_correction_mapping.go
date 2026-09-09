package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	correctionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/correction"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/result"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
)

type correctionAuthorityInputs struct {
	rosterID          uuid.UUID
	tournamentState   domain.TournamentState
	tournamentVersion int64
	series            sqlc.LockResultSeriesRow
	attempt           sqlc.LockResultAttemptRow
	gameRows          []sqlc.ListTournamentAdminCorrectionGameResultsRow
	target            sqlc.ListTournamentAdminCorrectionGameResultsRow
	projection        sqlc.ProjectionRevision
	artifacts         map[domain.ArtifactKind]sqlc.ProjectionArtifact
	readiness         sqlc.LockCorrectionOpenReadyWindowsRow
	reservations      []sqlc.ListTournamentAdminCorrectionReservationsRow
}

func (r *TournamentAdminCorrectionPostgres) loadCorrectionAuthority(
	ctx context.Context,
	tournamentID, seriesID, gameID uuid.UUID,
) (tournamentadmin.CorrectionWorkflowAuthority, error) {
	querier := r.tx.Querier(ctx)
	rosterID, err := r.correctionRosterID(ctx, tournamentID, seriesID)
	if err != nil {
		return tournamentadmin.CorrectionWorkflowAuthority{}, err
	}
	scope := ResultScope{TournamentID: tournamentID, RosterID: rosterID, SeriesID: seriesID, AttemptID: gameID}
	if err := lockCorrectionScope(ctx, querier, scope); err != nil {
		return tournamentadmin.CorrectionWorkflowAuthority{}, err
	}

	locked, err := r.loadCorrectionAuthorityInputs(ctx, querier, scope)
	if err != nil {
		return tournamentadmin.CorrectionWorkflowAuthority{}, err
	}
	if cutoff, cutoffErr := querier.GetCorrectionCutoff(ctx, correctionCutoffParams(scope, locked.target.ResultRevisionID)); cutoffErr != nil {
		return tournamentadmin.CorrectionWorkflowAuthority{}, tournamentAdminCorrectionError("load cutoff", cutoffErr)
	} else if cutoff != "" {
		return tournamentadmin.CorrectionWorkflowAuthority{}, &CorrectionCutoffError{Code: cutoff}
	}

	core, err := buildTournamentAdminCorrectionCore(ctx, querier, locked, scope)
	if err != nil {
		return tournamentadmin.CorrectionWorkflowAuthority{}, fmt.Errorf("TournamentAdminCorrectionPostgres - build correction core: %w", err)
	}
	stage, err := loadTournamentAdminCorrectionStage(ctx, querier, locked, scope)
	if err != nil {
		return tournamentadmin.CorrectionWorkflowAuthority{}, fmt.Errorf("TournamentAdminCorrectionPostgres - load correction stage: %w", err)
	}
	return tournamentadmin.CorrectionWorkflowAuthority{
		RosterID:             rosterID,
		ProjectionRevisionID: locked.projection.ID,
		ProjectionRevision:   locked.projection.RevisionNumber,
		Core:                 core,
		Stage:                stage,
	}, nil
}

func loadTournamentAdminCorrectionStage(
	ctx context.Context,
	querier *sqlc.Queries,
	input correctionAuthorityInputs,
	scope ResultScope,
) (correctionusecase.StageSnapshot, error) {
	participants, err := querier.ListTournamentAdminCorrectionProjectionParticipants(ctx, scope.RosterID)
	if err != nil {
		return correctionusecase.StageSnapshot{}, tournamentAdminCorrectionError("load stage participants", err)
	}
	if _, err := querier.LockTournamentAdminCorrectionSwissPointSourceSeries(
		ctx,
		sqlc.LockTournamentAdminCorrectionSwissPointSourceSeriesParams{
			TournamentID: scope.TournamentID,
			RosterID:     scope.RosterID,
		},
	); err != nil {
		return correctionusecase.StageSnapshot{}, tournamentAdminCorrectionError("lock stage Swiss source Series", err)
	}
	ledger, err := querier.ListTournamentAdminCorrectionSwissPointLedger(
		ctx,
		sqlc.ListTournamentAdminCorrectionSwissPointLedgerParams{
			TournamentID: scope.TournamentID,
			RosterID:     scope.RosterID,
		},
	)
	if err != nil {
		return correctionusecase.StageSnapshot{}, tournamentAdminCorrectionError("load stage Swiss ledger", err)
	}

	stage := correctionusecase.StageSnapshot{
		TournamentID:       scope.TournamentID,
		TournamentState:    input.tournamentState,
		TournamentRevision: input.tournamentVersion,
		Swiss: correctionusecase.StageSwissAuthority{
			Participants: make([]resultprojection.CanonicalSwissParticipant, len(participants)),
			Ledger:       make([]resultprojection.CanonicalSwissPointLedgerEntry, len(ledger)),
		},
	}
	for index, participant := range participants {
		if participant.ID == uuid.Nil || participant.Seed < 1 {
			return correctionusecase.StageSnapshot{}, domain.ErrConflict
		}
		stage.Swiss.Participants[index] = resultprojection.CanonicalSwissParticipant{
			ID: participant.ID, StableSeed: int(participant.Seed),
		}
	}
	for index, entry := range ledger {
		canonical, mapErr := correctionCanonicalSwissLedgerEntry(entry)
		if mapErr != nil {
			return correctionusecase.StageSnapshot{}, mapErr
		}
		stage.Swiss.Ledger[index] = canonical
	}
	if len(stage.Swiss.Participants) > 0 && len(stage.Swiss.Ledger) > 0 {
		_, canonicalErr := resultprojection.BuildCanonicalSwissRounds(stage.Swiss.Ledger)
		stage.Swiss.Complete = canonicalErr == nil
	}
	//nolint:exhaustive // This switch intentionally handles only the valid states for this boundary.
	switch input.tournamentState {
	case domain.TournamentStatePlayoffs:
		stage.Layout.Mode = correctionusecase.StageModePlayoff
	case domain.TournamentStateGolden:
		stage.Layout, err = loadTournamentAdminCorrectionGoldenStage(
			ctx, querier, scope, input.tournamentVersion,
		)
		if err != nil {
			return correctionusecase.StageSnapshot{}, err
		}
	default:
		return correctionusecase.StageSnapshot{}, domain.ErrConflict
	}
	return stage, nil
}

func (r *TournamentAdminCorrectionPostgres) correctionRosterID(
	ctx context.Context,
	tournamentID, seriesID uuid.UUID,
) (uuid.UUID, error) {
	const query = `
SELECT roster_id
FROM series
WHERE id = $1
    AND tournament_id = $2
`
	var rosterID uuid.UUID
	if err := r.tx.Conn(ctx).QueryRow(ctx, query, seriesID, tournamentID).Scan(&rosterID); err != nil {
		return uuid.Nil, tournamentAdminCorrectionError("discover series roster", err)
	}
	if rosterID == uuid.Nil {
		return uuid.Nil, domain.ErrConflict
	}
	return rosterID, nil
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func (r *TournamentAdminCorrectionPostgres) loadCorrectionAuthorityInputs(
	ctx context.Context,
	querier *sqlc.Queries,
	scope ResultScope,
) (correctionAuthorityInputs, error) {
	tournament, err := querier.LockCorrectionTournamentScope(ctx, sqlc.LockCorrectionTournamentScopeParams{
		TournamentID: scope.TournamentID, RosterID: scope.RosterID,
	})
	if err != nil {
		return correctionAuthorityInputs{}, tournamentAdminCorrectionError("lock tournament scope", err)
	}
	if domain.TournamentState(tournament.TournamentState).IsTerminal() {
		return correctionAuthorityInputs{}, domain.ErrConflict
	}
	attempt, err := querier.LockResultAttempt(ctx, resultAttemptParams(scope))
	if err != nil {
		return correctionAuthorityInputs{}, tournamentAdminCorrectionError("lock game attempt", err)
	}
	series, err := querier.LockResultSeries(ctx, sqlc.LockResultSeriesParams{
		SeriesID: scope.SeriesID, TournamentID: scope.TournamentID, RosterID: scope.RosterID,
	})
	if err != nil {
		return correctionAuthorityInputs{}, tournamentAdminCorrectionError("lock series", err)
	}
	if !domain.SeriesState(series.State).IsTerminal() || !domain.GameState(attempt.State).IsTerminal() ||
		!attempt.ResultRevisionID.Valid || !series.CurrentScoreRevisionID.Valid || !series.CurrentResultRevisionID.Valid {
		return correctionAuthorityInputs{}, domain.ErrConflict
	}
	if _, err := querier.LockCorrectionOfficialHeads(ctx, sqlc.LockCorrectionOfficialHeadsParams{
		RosterID: scope.RosterID, AttemptID: scope.AttemptID, SeriesID: scope.SeriesID,
	}); err != nil {
		return correctionAuthorityInputs{}, tournamentAdminCorrectionError("lock result heads", err)
	}
	gameRows, err := querier.ListTournamentAdminCorrectionGameResults(ctx, sqlc.ListTournamentAdminCorrectionGameResultsParams{
		SeriesID: scope.SeriesID, RosterID: scope.RosterID, TournamentID: scope.TournamentID,
	})
	if err != nil {
		return correctionAuthorityInputs{}, tournamentAdminCorrectionError("load game result heads", err)
	}
	target, found := correctionTargetGameRow(gameRows, scope.AttemptID)
	if !found || target.ResultRevisionID != attempt.ResultRevisionID.UUID || !domain.GameState(target.GameState).IsTerminal() {
		return correctionAuthorityInputs{}, domain.ErrConflict
	}
	if _, err := querier.LockProjectionRevisionSet(ctx, sqlc.LockProjectionRevisionSetParams{
		TournamentID: scope.TournamentID, RosterID: scope.RosterID,
	}); err != nil {
		return correctionAuthorityInputs{}, tournamentAdminCorrectionError("lock projection revisions", err)
	}
	projection, err := querier.GetCurrentProjectionRevision(ctx, sqlc.GetCurrentProjectionRevisionParams{
		TournamentID: scope.TournamentID, RosterID: scope.RosterID,
	})
	if err != nil {
		return correctionAuthorityInputs{}, tournamentAdminCorrectionError("load current projection", err)
	}
	artifacts, err := correctionCurrentBaseArtifacts(ctx, querier, projection, scope)
	if err != nil {
		return correctionAuthorityInputs{}, fmt.Errorf("load current correction artifacts: %w", err)
	}
	windows, err := querier.LockCorrectionOpenReadyWindows(ctx, sqlc.LockCorrectionOpenReadyWindowsParams{
		TournamentID: scope.TournamentID, RosterID: scope.RosterID,
	})
	if err != nil {
		return correctionAuthorityInputs{}, tournamentAdminCorrectionError("lock ready windows", err)
	}
	if len(windows) > 1 {
		return correctionAuthorityInputs{}, fmt.Errorf("correction readiness cardinality: %w", domain.ErrConflict)
	}
	var readiness sqlc.LockCorrectionOpenReadyWindowsRow
	if len(windows) == 1 {
		if windows[0].ReadyWindowState != "open" || windows[0].WaveState == "" || windows[0].WaveRevision < 1 {
			return correctionAuthorityInputs{}, domain.ErrConflict
		}
		readiness = windows[0]
	}
	reservations, err := querier.ListTournamentAdminCorrectionReservations(ctx, sqlc.ListTournamentAdminCorrectionReservationsParams{
		TournamentID: scope.TournamentID, RosterID: scope.RosterID,
	})
	if err != nil {
		return correctionAuthorityInputs{}, tournamentAdminCorrectionError("lock task reservations", err)
	}
	return correctionAuthorityInputs{
		rosterID: scope.RosterID, tournamentState: domain.TournamentState(tournament.TournamentState),
		tournamentVersion: tournament.TournamentRevision, series: series, attempt: attempt,
		gameRows: gameRows, target: target, projection: projection, artifacts: artifacts,
		readiness: readiness, reservations: reservations,
	}, nil
}

func correctionTargetGameRow(
	rows []sqlc.ListTournamentAdminCorrectionGameResultsRow,
	gameID uuid.UUID,
) (sqlc.ListTournamentAdminCorrectionGameResultsRow, bool) {
	for _, row := range rows {
		if row.GameAttemptID == gameID {
			return row, true
		}
	}
	return sqlc.ListTournamentAdminCorrectionGameResultsRow{}, false
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func correctionCurrentBaseArtifacts(
	ctx context.Context,
	querier *sqlc.Queries,
	projection sqlc.ProjectionRevision,
	scope ResultScope,
) (map[domain.ArtifactKind]sqlc.ProjectionArtifact, error) {
	links, err := querier.ListProjectionRevisionArtifacts(ctx, sqlc.ListProjectionRevisionArtifactsParams{
		RevisionID: projection.ID, TournamentID: scope.TournamentID, RosterID: scope.RosterID,
	})
	if err != nil {
		return nil, tournamentAdminCorrectionError("list current projection artifacts", err)
	}
	if len(links) < 1 || len(links) > 3 {
		return nil, domain.ErrConflict
	}
	artifacts := make(map[domain.ArtifactKind]sqlc.ProjectionArtifact, len(links))
	for _, link := range links {
		kind, valid := correctionArtifactKind(link.ArtifactKind)
		if !valid || (kind != domain.ArtifactKindStandings && kind != domain.ArtifactKindTopFour && kind != domain.ArtifactKindBracket) {
			return nil, domain.ErrConflict
		}
		artifact, loadErr := querier.GetProjectionArtifactScoped(ctx, sqlc.GetProjectionArtifactScopedParams{
			ID: link.ArtifactID, TournamentID: scope.TournamentID, RosterID: scope.RosterID,
		})
		if loadErr != nil {
			return nil, tournamentAdminCorrectionError("load current projection artifact", loadErr)
		}
		if artifact.ArtifactKind != link.ArtifactKind || len(artifact.PayloadDigest) != sha256.Size {
			return nil, domain.ErrConflict
		}
		// A Final Swiss receipt carries unchanged Top4/bracket members forward.
		// Their producer remains the historical projection, while membership in
		// this locked revision makes them part of the exact correction authority.
		if _, duplicate := artifacts[kind]; duplicate {
			return nil, domain.ErrConflict
		}
		artifacts[kind] = artifact
	}
	if _, found := artifacts[domain.ArtifactKindStandings]; !found {
		return nil, domain.ErrConflict
	}
	if _, bracket := artifacts[domain.ArtifactKindBracket]; bracket {
		if _, topFour := artifacts[domain.ArtifactKindTopFour]; !topFour {
			return nil, domain.ErrConflict
		}
	}
	return artifacts, nil
}

func buildTournamentAdminCorrectionCore(
	ctx context.Context,
	querier *sqlc.Queries,
	input correctionAuthorityInputs,
	scope ResultScope,
) (correctionusecase.Authority, error) {
	series, scoreReferences, err := correctionAuthoritySeries(input.series, input.gameRows)
	if err != nil {
		return correctionusecase.Authority{}, fmt.Errorf("map Series authority: %w", err)
	}
	logical, err := correctionAuthorityLogicalGraph(ctx, querier, input, scope)
	if err != nil {
		return correctionusecase.Authority{}, fmt.Errorf("map logical graph: %w", err)
	}
	gameSource := logical.current[domain.ArtifactRef{Kind: domain.ArtifactKindGameResult, EntityID: scope.AttemptID}]
	scoreSource := logical.current[domain.ArtifactRef{Kind: domain.ArtifactKindSeriesScore, EntityID: scope.SeriesID}]
	seriesSource := logical.current[domain.ArtifactRef{Kind: domain.ArtifactKindSeriesResult, EntityID: scope.SeriesID}]
	if gameSource.ID().IsZero() || scoreSource.ID().IsZero() || seriesSource.ID().IsZero() {
		return correctionusecase.Authority{}, domain.ErrConflict
	}

	game, err := correctionAuthorityGameHead(
		input.target, gameSource, scope, input.rosterID,
		logical.bindings[domain.ArtifactRef{Kind: domain.ArtifactKindGameResult, EntityID: scope.AttemptID}],
	)
	if err != nil {
		return correctionusecase.Authority{}, fmt.Errorf("map Game result authority: %w", err)
	}
	score, err := correctionAuthorityScoreHead(
		ctx, querier, input, scoreReferences, scoreSource,
		logical.bindings[domain.ArtifactRef{Kind: domain.ArtifactKindSeriesScore, EntityID: scope.SeriesID}],
	)
	if err != nil {
		return correctionusecase.Authority{}, fmt.Errorf("map score authority: %w", err)
	}
	seriesResult, err := correctionAuthoritySeriesHead(
		ctx, querier, input, score.ID, seriesSource,
		logical.bindings[domain.ArtifactRef{Kind: domain.ArtifactKindSeriesResult, EntityID: scope.SeriesID}],
	)
	if err != nil {
		return correctionusecase.Authority{}, fmt.Errorf("map Series result authority: %w", err)
	}

	dag, err := resultprojection.BuildRevisionDAG(resultprojection.RevisionDAGInput{
		Graph: logical.graph,
		Results: []resultprojection.OfficialResultProjectionInput{
			{
				TerminalSource:   resultprojection.TerminalResultSourcePlayed,
				Result:           game,
				ResultProjection: logical.byID[gameSource.ID()],
			},
			{
				TerminalSource:   resultprojection.TerminalResultSourcePlayed,
				Result:           seriesResult,
				ResultProjection: logical.byID[seriesSource.ID()],
				Score:            &score,
				ScoreProjection:  projectionPointer(logical.byID[scoreSource.ID()]),
			},
		},
	})
	if err != nil {
		return correctionusecase.Authority{}, fmt.Errorf("TournamentAdminCorrectionPostgres - build revision DAG: %w", err)
	}
	decisions, err := correctionAuthorityDecisions(ctx, querier, logical, input, scope)
	if err != nil {
		return correctionusecase.Authority{}, fmt.Errorf("map projection decisions: %w", err)
	}
	var readiness correctionusecase.Readiness
	if input.readiness.ReadyWindowID != uuid.Nil {
		readiness = correctionusecase.Readiness{
			TournamentID: scope.TournamentID, OwnerID: scope.SeriesID, WaveID: input.readiness.WaveID,
			WindowID:   input.readiness.ReadyWindowID,
			RevisionID: correctionWorkflowUUID(input.readiness.ReadyWindowID, "revision"),
			Revision:   input.readiness.WaveRevision, State: correctionusecase.ReadinessOpen,
			ParticipantIDs: []uuid.UUID{input.series.FirstParticipantID, input.series.SecondParticipantID},
		}
	}
	reservations := correctionAuthorityReservations(input.reservations, scope, gameSource.ID())
	return correctionusecase.Authority{
		TournamentState: input.tournamentState, TournamentRevision: input.tournamentVersion,
		DAG: dag, Series: series, GameResult: game, Score: score, SeriesResult: seriesResult,
		SeriesRevision:  resultusecase.SeriesRowRevision(input.series.Revision),
		AttemptRevision: resultusecase.AttemptRowRevision(input.attempt.Revision),
		CurrentSolve:    correctionAuthoritySolve(ctx, querier, input, scope, gameSource.CreatedAt(), game.RecordedAt),
		Readiness:       readiness, Reservations: reservations, Decisions: decisions, CutoffEvents: nil,
	}, nil
}

type correctionLogicalGraph struct {
	graph    domain.RevisionGraph
	byID     map[domain.DerivedRevisionID]domain.ProjectionRevision
	current  map[domain.ArtifactRef]domain.DerivedRevision
	bindings map[domain.ArtifactRef]correctionLogicalSourceBinding
}

type correctionLogicalSourceBinding struct {
	commandID      uuid.UUID
	sourceID       uuid.UUID
	nodeID         uuid.UUID
	previousNodeID uuid.UUID
	nodeRevision   int
}

func correctionAuthorityLogicalGraph(
	ctx context.Context,
	querier *sqlc.Queries,
	input correctionAuthorityInputs,
	scope ResultScope,
) (correctionLogicalGraph, error) {
	latest := maxCorrectionTime(input.target.OccurredAt.Time, input.target.RevisionCreatedAt.Time, input.series.UpdatedAt.Time)
	if !validServerTime(latest) {
		return correctionLogicalGraph{}, domain.ErrConflict
	}
	targets := []struct {
		kind     domain.ArtifactKind
		entityID uuid.UUID
		sourceID uuid.UUID
		at       time.Time
	}{
		{domain.ArtifactKindGameResult, scope.AttemptID, input.target.ResultRevisionID, latest},
		{domain.ArtifactKindSeriesScore, scope.SeriesID, input.series.ScoreHeadRevisionID, latest},
		{domain.ArtifactKindSeriesResult, scope.SeriesID, input.series.CurrentResultRevisionID.UUID, latest},
	}
	for _, kind := range []domain.ArtifactKind{
		domain.ArtifactKindStandings, domain.ArtifactKindTopFour, domain.ArtifactKindBracket,
	} {
		if artifact, found := input.artifacts[kind]; found {
			createdAt := artifact.CreatedAt.Time
			if input.projection.CreatedAt.Valid {
				createdAt = maxCorrectionTime(createdAt, input.projection.CreatedAt.Time)
			}
			targets = append(targets, struct {
				kind               domain.ArtifactKind
				entityID, sourceID uuid.UUID
				at                 time.Time
			}{kind: kind, entityID: scope.TournamentID, sourceID: artifact.ID, at: createdAt})
		}
	}
	projections := make([]domain.ProjectionRevision, 0, len(targets)*2)
	dependencies := make([]domain.RevisionDependency, 0, len(targets)*2)
	current := make(map[domain.ArtifactRef]domain.DerivedRevision, len(targets))
	bindings := make(map[domain.ArtifactRef]correctionLogicalSourceBinding, len(targets))
	for _, target := range targets {
		chain, head, binding, err := correctionLogicalProjectionChain(ctx, querier, scope, target.kind, target.entityID, target.sourceID, target.at)
		if err != nil {
			return correctionLogicalGraph{}, err
		}
		projections = append(projections, chain...)
		for index := 1; index < len(chain); index++ {
			dependencies = append(dependencies, domain.RevisionDependency{
				SourceRevisionID:  chain[index-1].Revision().ID(),
				DerivedRevisionID: chain[index].Revision().ID(),
			})
		}
		current[head.Revision().Artifact()] = head.Revision()
		if binding != nil {
			bindings[head.Revision().Artifact()] = *binding
		}
	}
	sortCorrectionProjections(projections)
	dependencies = append(dependencies,
		domain.RevisionDependency{SourceRevisionID: current[domain.ArtifactRef{Kind: domain.ArtifactKindGameResult, EntityID: scope.AttemptID}].ID(), DerivedRevisionID: current[domain.ArtifactRef{Kind: domain.ArtifactKindSeriesScore, EntityID: scope.SeriesID}].ID()},
		domain.RevisionDependency{SourceRevisionID: current[domain.ArtifactRef{Kind: domain.ArtifactKindSeriesScore, EntityID: scope.SeriesID}].ID(), DerivedRevisionID: current[domain.ArtifactRef{Kind: domain.ArtifactKindSeriesResult, EntityID: scope.SeriesID}].ID()},
		domain.RevisionDependency{SourceRevisionID: current[domain.ArtifactRef{Kind: domain.ArtifactKindSeriesResult, EntityID: scope.SeriesID}].ID(), DerivedRevisionID: current[domain.ArtifactRef{Kind: domain.ArtifactKindStandings, EntityID: scope.TournamentID}].ID()},
	)
	previous := current[domain.ArtifactRef{Kind: domain.ArtifactKindStandings, EntityID: scope.TournamentID}].ID()
	for _, kind := range []domain.ArtifactKind{domain.ArtifactKindTopFour, domain.ArtifactKindBracket} {
		if next, found := current[domain.ArtifactRef{Kind: kind, EntityID: scope.TournamentID}]; found {
			dependencies = append(dependencies, domain.RevisionDependency{
				SourceRevisionID: previous, DerivedRevisionID: next.ID(),
			})
			previous = next.ID()
		}
	}
	graph, err := domain.NewRevisionGraph(projections, dependencies)
	if err != nil {
		return correctionLogicalGraph{}, fmt.Errorf("TournamentAdminCorrectionPostgres - logical graph: %w", err)
	}
	byID := make(map[domain.DerivedRevisionID]domain.ProjectionRevision, len(projections))
	for _, projection := range projections {
		byID[projection.Revision().ID()] = projection
	}
	return correctionLogicalGraph{graph: graph, byID: byID, current: current, bindings: bindings}, nil
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func correctionLogicalProjectionChain(
	ctx context.Context,
	querier *sqlc.Queries,
	scope ResultScope,
	kind domain.ArtifactKind,
	entityID, sourceID uuid.UUID,
	createdAt time.Time,
) ([]domain.ProjectionRevision, domain.ProjectionRevision, *correctionLogicalSourceBinding, error) {
	binding, err := querier.GetCorrectionProjectionBinding(ctx, sqlc.GetCorrectionProjectionBindingParams{
		TournamentID: scope.TournamentID, RosterID: scope.RosterID, ArtifactKind: correctionArtifactKindSQL(kind), EntityID: entityID, SourceID: sourceID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		baseline, baselineErr := correctionBaselineProjection(scope.TournamentID, kind, entityID, sourceID, createdAt)
		if baselineErr != nil {
			return nil, domain.ProjectionRevision{}, nil, baselineErr
		}
		return []domain.ProjectionRevision{baseline}, baseline, nil, nil
	}
	if err != nil {
		return nil, domain.ProjectionRevision{}, nil, tournamentAdminCorrectionError("load projection binding", err)
	}
	if binding.CommandID == uuid.Nil || binding.TournamentID != scope.TournamentID || binding.RosterID != scope.RosterID ||
		binding.ArtifactKind != correctionArtifactKindSQL(kind) || binding.EntityID != entityID ||
		binding.SourceID != sourceID || binding.NodeID == uuid.Nil || binding.NodeID == sourceID {
		return nil, domain.ProjectionRevision{}, nil, domain.ErrConflict
	}
	chain := make([]domain.ProjectionRevision, 0, 2)
	seen := map[uuid.UUID]struct{}{}
	nodeID := binding.NodeID
	var currentNode sqlc.ResultProjectionNode
	for nodeID != uuid.Nil {
		if _, duplicate := seen[nodeID]; duplicate {
			return nil, domain.ProjectionRevision{}, nil, domain.ErrConflict
		}
		seen[nodeID] = struct{}{}
		node, loadErr := querier.GetResultProjectionNode(ctx, nodeID)
		if loadErr != nil {
			return nil, domain.ProjectionRevision{}, nil, tournamentAdminCorrectionError("load projection node", loadErr)
		}
		if len(chain) == 0 {
			currentNode = node
		}
		projection, projectionErr := correctionProjectionNode(node)
		if projectionErr != nil {
			return nil, domain.ProjectionRevision{}, nil, projectionErr
		}
		if projection.Revision().TournamentID() != scope.TournamentID || projection.Revision().Artifact().Kind != kind ||
			projection.Revision().Artifact().EntityID != entityID {
			return nil, domain.ProjectionRevision{}, nil, domain.ErrConflict
		}
		chain = append(chain, projection)
		if !node.PreviousNodeID.Valid {
			break
		}
		nodeID = node.PreviousNodeID.UUID
	}
	for first, last := 0, len(chain)-1; first < last; first, last = first+1, last-1 {
		chain[first], chain[last] = chain[last], chain[first]
	}
	if len(chain) == 0 {
		return nil, domain.ProjectionRevision{}, nil, domain.ErrConflict
	}
	if currentNode.ID != binding.NodeID || currentNode.RevisionNumber < 2 || !currentNode.PreviousNodeID.Valid ||
		currentNode.PreviousNodeID.UUID == uuid.Nil || currentNode.PreviousNodeID.UUID == currentNode.ID ||
		int64(int(currentNode.RevisionNumber)) != currentNode.RevisionNumber {
		return nil, domain.ProjectionRevision{}, nil, domain.ErrConflict
	}
	return chain, chain[len(chain)-1], &correctionLogicalSourceBinding{
		commandID: binding.CommandID, sourceID: binding.SourceID, nodeID: binding.NodeID,
		previousNodeID: currentNode.PreviousNodeID.UUID, nodeRevision: int(currentNode.RevisionNumber),
	}, nil
}

func correctionBaselineProjection(
	tournamentID uuid.UUID,
	kind domain.ArtifactKind,
	entityID, sourceID uuid.UUID,
	createdAt time.Time,
) (domain.ProjectionRevision, error) {
	payload, _, err := correctionJSONDocument(struct {
		Schema   string `json:"schema"`
		Kind     string `json:"kind"`
		EntityID string `json:"entity_id"`
		SourceID string `json:"source_id"`
	}{
		Schema: "tournament-correction-baseline-v1", Kind: string(kind), EntityID: entityID.String(), SourceID: sourceID.String(),
	})
	if err != nil || tournamentID == uuid.Nil || entityID == uuid.Nil || sourceID == uuid.Nil || !validServerTime(correctionTime(createdAt)) {
		return domain.ProjectionRevision{}, domain.ErrConflict
	}
	return domain.NewProjectionRevision(
		domain.DerivedRevisionID(correctionLogicalBaselineUUID(kind, sourceID)), tournamentID,
		domain.ArtifactRef{Kind: kind, EntityID: entityID}, 1, nil, correctionTime(createdAt), payload,
	)
}

func correctionProjectionNode(node sqlc.ResultProjectionNode) (domain.ProjectionRevision, error) {
	kind, valid := correctionArtifactKindFromSQL(node.ArtifactKind)
	digest, digestValid := correctionDigestFromBytes(node.PayloadDigest)
	if !valid || !digestValid || node.ID == uuid.Nil || node.TournamentID == uuid.Nil || node.EntityID == uuid.Nil ||
		node.RevisionNumber < 1 || !node.CreatedAt.Valid || !validServerTime(correctionTime(node.CreatedAt.Time)) ||
		sha256.Sum256(node.Payload) != digest {
		return domain.ProjectionRevision{}, domain.ErrConflict
	}
	previous := correctionDerivedPointer(node.PreviousNodeID)
	projection, err := domain.NewProjectionRevision(
		domain.DerivedRevisionID(node.ID), node.TournamentID, domain.ArtifactRef{Kind: kind, EntityID: node.EntityID},
		int(node.RevisionNumber), previous, correctionTime(node.CreatedAt.Time), node.Payload,
	)
	if err != nil {
		return domain.ProjectionRevision{}, domain.ErrConflict
	}
	return projection, nil
}

func correctionAuthoritySeries(
	series sqlc.LockResultSeriesRow,
	rows []sqlc.ListTournamentAdminCorrectionGameResultsRow,
) (domain.Series, []resultusecase.SeriesScoreAttemptReference, error) {
	if len(rows) == 0 || !series.CurrentScoreRevisionID.Valid || !series.CurrentResultRevisionID.Valid {
		return domain.Series{}, nil, domain.ErrConflict
	}
	slots := make(map[uuid.UUID]*domain.GameSlot)
	slotOrder := make([]uuid.UUID, 0, len(rows))
	references := make([]resultusecase.SeriesScoreAttemptReference, 0, len(rows))
	for _, row := range rows {
		state := domain.GameState(row.GameState)
		reason := domain.GameResultReason(row.ResultReason)
		winner := correctionNullPointer(row.GameWinnerID)
		if !state.IsTerminal() || !reason.IsLegalFor(state) || row.ResultRevisionID == uuid.Nil ||
			row.GameAttemptID == uuid.Nil || row.SlotID == uuid.Nil {
			return domain.Series{}, nil, domain.ErrConflict
		}
		resultID := domain.OfficialResultRevisionID(row.ResultRevisionID)
		slot, found := slots[row.SlotID]
		if !found {
			slot = &domain.GameSlot{
				ID: row.SlotID, SeriesID: series.ID, Position: int(row.SlotNumber), Category: domain.CategoryWeb,
				ScoreBefore: domain.SeriesScore{}, Attempts: []domain.Game{},
			}
			slots[row.SlotID] = slot
			slotOrder = append(slotOrder, row.SlotID)
		}
		slot.Attempts = append(slot.Attempts, domain.Game{
			ID: row.GameAttemptID, SlotID: row.SlotID, AttemptNo: int(row.AttemptNumber), State: state,
			ResultReason: reason, WinnerID: winner, ResultRevisionID: &resultID,
		})
		references = append(references, resultusecase.SeriesScoreAttemptReference{
			SlotID: row.SlotID, SlotPosition: int(row.SlotNumber), GameID: row.GameAttemptID, AttemptNo: int(row.AttemptNumber),
			State: state, WinnerID: winner, Reason: reason, CurrentGameResultRevisionID: resultID,
		})
	}
	sort.Slice(slotOrder, func(first, second int) bool {
		return slots[slotOrder[first]].Position < slots[slotOrder[second]].Position
	})
	domainSlots := make([]domain.GameSlot, len(slotOrder))
	for index, slotID := range slotOrder {
		slot := slots[slotID]
		sort.Slice(slot.Attempts, func(first, second int) bool { return slot.Attempts[first].AttemptNo < slot.Attempts[second].AttemptNo })
		domainSlots[index] = *slot
	}
	sort.Slice(references, func(first, second int) bool {
		if references[first].SlotPosition != references[second].SlotPosition {
			return references[first].SlotPosition < references[second].SlotPosition
		}
		return references[first].AttemptNo < references[second].AttemptNo
	})
	scoreID := domain.SeriesScoreRevisionID(series.CurrentScoreRevisionID.UUID)
	resultID := domain.OfficialResultRevisionID(series.CurrentResultRevisionID.UUID)
	winner := correctionNullPointer(series.WinnerID)
	value := domain.Series{
		ID: series.ID, TournamentID: series.TournamentID, FirstParticipantID: series.FirstParticipantID,
		SecondParticipantID: series.SecondParticipantID, Format: domain.SeriesFormat(series.Format),
		State: domain.SeriesState(series.State), Score: domain.SeriesScore{
			FirstParticipantWins: int(series.FirstParticipantWins), SecondParticipantWins: int(series.SecondParticipantWins),
		}, WinnerID: winner, Slots: domainSlots, CurrentScoreRevisionID: &scoreID, CurrentResultRevisionID: &resultID,
	}
	if value.Validate() != nil {
		return domain.Series{}, nil, domain.ErrConflict
	}
	return value, references, nil
}

func correctionAuthorityGameHead(
	row sqlc.ListTournamentAdminCorrectionGameResultsRow,
	source domain.DerivedRevision,
	scope ResultScope,
	rosterID uuid.UUID,
	binding correctionLogicalSourceBinding,
) (resultusecase.OfficialResultRevisionHead, error) {
	if !row.RevisionCreatedAt.Valid || !row.OccurredAt.Valid || row.ResultRevisionID == uuid.Nil ||
		row.ResultCommandIdempotencyKey == uuid.Nil {
		return resultusecase.OfficialResultRevisionHead{}, domain.ErrConflict
	}
	state := domain.GameState(row.ResultState)
	reason := domain.GameResultReason(row.ResultReason)
	if !state.IsTerminal() || !reason.IsLegalFor(state) || state != domain.GameState(row.GameState) ||
		reason != domain.GameResultReason(stringValue(row.GameReason)) {
		return resultusecase.OfficialResultRevisionHead{}, domain.ErrConflict
	}
	recordedAt := maxCorrectionTime(row.RevisionCreatedAt.Time, row.OccurredAt.Time)
	if !validServerTime(recordedAt) || recordedAt.Before(source.CreatedAt()) {
		return resultusecase.OfficialResultRevisionHead{}, domain.ErrConflict
	}
	head := resultusecase.OfficialResultRevisionHead{
		Scope: resultusecase.OfficialResultScope{
			TournamentID: scope.TournamentID, SeriesID: scope.SeriesID, GameID: scope.AttemptID,
			Kind: resultusecase.OfficialResultSubjectGame,
		},
		ID: domain.OfficialResultRevisionID(row.ResultRevisionID), PreviousRevisionID: correctionOfficialPointer(row.PreviousRevisionID),
		Ordinal: int(row.RevisionNumber), CommandID: row.ResultCommandIdempotencyKey,
		Actor:            domain.ResultActor{Kind: domain.ResultActorServer},
		Outcome:          resultusecase.OfficialResultOutcome{GameState: state, GameReason: reason, WinnerID: correctionNullPointer(row.RevisionWinnerID)},
		SourceProjection: source, RecordedAt: recordedAt,
	}
	if binding.commandID != uuid.Nil {
		if head.PreviousRevisionID == nil {
			return resultusecase.OfficialResultRevisionHead{}, domain.ErrConflict
		}
		restored, restoreErr := resultusecase.RestoreCorrectionOfficialResultHead(
			head,
			resultusecase.PersistedCorrectionSourceBinding{
				TournamentID: scope.TournamentID, RosterID: rosterID, SeriesID: scope.SeriesID,
				EntityID: scope.AttemptID, ArtifactKind: domain.ArtifactKindGameResult,
				CorrectionCommandID: binding.commandID, HeadCommandID: head.CommandID,
				SourceID: binding.sourceID, NodeID: binding.nodeID,
				PreviousSourceID: head.PreviousRevisionID.UUID(), PreviousNodeID: binding.previousNodeID,
				NodeRevision: binding.nodeRevision,
			},
		)
		if restoreErr != nil {
			return resultusecase.OfficialResultRevisionHead{}, domain.ErrConflict
		}
		return restored, nil
	}
	if head.Validate() != nil {
		return resultusecase.OfficialResultRevisionHead{}, domain.ErrConflict
	}
	return head, nil
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func correctionAuthorityScoreHead(
	ctx context.Context,
	querier *sqlc.Queries,
	input correctionAuthorityInputs,
	references []resultusecase.SeriesScoreAttemptReference,
	source domain.DerivedRevision,
	binding correctionLogicalSourceBinding,
) (resultusecase.SeriesScoreRevisionHead, error) {
	revision, err := querier.GetSeriesScoreRevisionByID(ctx, input.series.ScoreHeadRevisionID)
	if err != nil {
		return resultusecase.SeriesScoreRevisionHead{}, tournamentAdminCorrectionError("load score revision", err)
	}
	if revision.SeriesID != input.series.ID || revision.TournamentID != input.series.TournamentID ||
		revision.RosterID != input.series.RosterID || !revision.ResultEventID.Valid || !revision.CreatedAt.Valid ||
		revision.RevisionNumber < 2 || !revision.PreviousRevisionID.Valid {
		return resultusecase.SeriesScoreRevisionHead{}, domain.ErrConflict
	}
	event, err := querier.GetResultEventByID(ctx, revision.ResultEventID.UUID)
	if err != nil {
		return resultusecase.SeriesScoreRevisionHead{}, tournamentAdminCorrectionError("load score result event", err)
	}
	if event.IdempotencyKey == uuid.Nil || !event.OccurredAt.Valid {
		return resultusecase.SeriesScoreRevisionHead{}, domain.ErrConflict
	}
	recordedAt := maxCorrectionTime(revision.CreatedAt.Time, event.OccurredAt.Time)
	if !validServerTime(recordedAt) || recordedAt.Before(source.CreatedAt()) || len(references) == 0 {
		return resultusecase.SeriesScoreRevisionHead{}, domain.ErrConflict
	}
	commandAttempt := references[len(references)-1]
	head := resultusecase.SeriesScoreRevisionHead{
		Scope: resultusecase.SeriesScoreRevisionScope{TournamentID: input.series.TournamentID, SeriesID: input.series.ID},
		ID:    domain.SeriesScoreRevisionID(revision.ID), PreviousRevisionID: correctionScorePointer(revision.PreviousRevisionID),
		Ordinal: int(revision.RevisionNumber), Operation: resultusecase.SeriesScoreRevisionOperationReplaceResult,
		CommandID: event.IdempotencyKey, Actor: domain.ResultActor{Kind: domain.ResultActorServer}, CommandAttempt: &commandAttempt,
		FirstParticipantID: input.series.FirstParticipantID, SecondParticipantID: input.series.SecondParticipantID,
		Format: domain.SeriesFormat(input.series.Format), Score: domain.SeriesScore{
			FirstParticipantWins: int(revision.FirstParticipantWins), SecondParticipantWins: int(revision.SecondParticipantWins),
		}, Attempts: references, SourceProjection: source, RecordedAt: recordedAt,
	}
	if head.Score != (domain.SeriesScore{FirstParticipantWins: int(input.series.FirstParticipantWins), SecondParticipantWins: int(input.series.SecondParticipantWins)}) {
		return resultusecase.SeriesScoreRevisionHead{}, domain.ErrConflict
	}
	if binding.commandID != uuid.Nil {
		if head.PreviousRevisionID == nil {
			return resultusecase.SeriesScoreRevisionHead{}, domain.ErrConflict
		}
		restored, restoreErr := resultusecase.RestoreCorrectionSeriesScoreHead(
			head,
			resultusecase.PersistedCorrectionSourceBinding{
				TournamentID: input.series.TournamentID, RosterID: input.series.RosterID,
				SeriesID: input.series.ID, EntityID: input.series.ID, ArtifactKind: domain.ArtifactKindSeriesScore,
				CorrectionCommandID: binding.commandID, HeadCommandID: head.CommandID,
				SourceID: binding.sourceID, NodeID: binding.nodeID,
				PreviousSourceID: head.PreviousRevisionID.UUID(), PreviousNodeID: binding.previousNodeID,
				NodeRevision: binding.nodeRevision,
			},
		)
		if restoreErr != nil {
			return resultusecase.SeriesScoreRevisionHead{}, domain.ErrConflict
		}
		return restored, nil
	}
	if head.Validate() != nil {
		return resultusecase.SeriesScoreRevisionHead{}, domain.ErrConflict
	}
	return head, nil
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func correctionAuthoritySeriesHead(
	ctx context.Context,
	querier *sqlc.Queries,
	input correctionAuthorityInputs,
	scoreID domain.SeriesScoreRevisionID,
	source domain.DerivedRevision,
	binding correctionLogicalSourceBinding,
) (resultusecase.OfficialResultRevisionHead, error) {
	revision, err := querier.GetOfficialResultRevisionByID(ctx, input.series.CurrentResultRevisionID.UUID)
	if err != nil {
		return resultusecase.OfficialResultRevisionHead{}, tournamentAdminCorrectionError("load series result revision", err)
	}
	if revision.EntityKind != "series" || revision.EntityID != input.series.ID || revision.SeriesID != input.series.ID ||
		revision.TournamentID != input.series.TournamentID || revision.RosterID != input.series.RosterID || !revision.CreatedAt.Valid {
		return resultusecase.OfficialResultRevisionHead{}, domain.ErrConflict
	}
	event, err := querier.GetResultEventByID(ctx, revision.ResultEventID)
	if err != nil {
		return resultusecase.OfficialResultRevisionHead{}, tournamentAdminCorrectionError("load series result event", err)
	}
	if revision.CommandID == uuid.Nil || !event.OccurredAt.Valid {
		return resultusecase.OfficialResultRevisionHead{}, domain.ErrConflict
	}
	state := domain.SeriesState(revision.ResultState)
	reason := domain.SeriesResultReason(revision.ResultReason)
	winner := correctionNullPointer(revision.WinnerID)
	recordedAt := maxCorrectionTime(revision.CreatedAt.Time, event.OccurredAt.Time)
	if !state.IsTerminal() || !validSeriesResultReason(string(reason)) || !validServerTime(recordedAt) || recordedAt.Before(source.CreatedAt()) {
		return resultusecase.OfficialResultRevisionHead{}, domain.ErrConflict
	}
	head := resultusecase.OfficialResultRevisionHead{
		Scope: resultusecase.OfficialResultScope{TournamentID: input.series.TournamentID, SeriesID: input.series.ID, Kind: resultusecase.OfficialResultSubjectSeries},
		ID:    domain.OfficialResultRevisionID(revision.ID), PreviousRevisionID: correctionOfficialPointer(revision.PreviousRevisionID),
		Ordinal: int(revision.RevisionNumber), CommandID: revision.CommandID,
		Actor:            domain.ResultActor{Kind: domain.ResultActorServer},
		Outcome:          resultusecase.OfficialResultOutcome{SeriesState: state, SeriesReason: reason, WinnerID: winner, ScoreRevisionID: &scoreID},
		SourceProjection: source, RecordedAt: recordedAt,
	}
	if binding.commandID != uuid.Nil {
		if head.PreviousRevisionID == nil {
			return resultusecase.OfficialResultRevisionHead{}, domain.ErrConflict
		}
		restored, restoreErr := resultusecase.RestoreCorrectionOfficialResultHead(
			head,
			resultusecase.PersistedCorrectionSourceBinding{
				TournamentID: input.series.TournamentID, RosterID: input.series.RosterID,
				SeriesID: input.series.ID, EntityID: input.series.ID, ArtifactKind: domain.ArtifactKindSeriesResult,
				CorrectionCommandID: binding.commandID, HeadCommandID: head.CommandID,
				SourceID: binding.sourceID, NodeID: binding.nodeID,
				PreviousSourceID: head.PreviousRevisionID.UUID(), PreviousNodeID: binding.previousNodeID,
				NodeRevision: binding.nodeRevision,
			},
		)
		if restoreErr != nil {
			return resultusecase.OfficialResultRevisionHead{}, domain.ErrConflict
		}
		return restored, nil
	}
	if head.Validate() != nil {
		return resultusecase.OfficialResultRevisionHead{}, domain.ErrConflict
	}
	return head, nil
}

func projectionPointer(value domain.ProjectionRevision) *domain.ProjectionRevision {
	clone := value
	return &clone
}

func correctionAuthorityReservations(
	rows []sqlc.ListTournamentAdminCorrectionReservationsRow,
	scope ResultScope,
	sourceID domain.DerivedRevisionID,
) []correctionusecase.Reservation {
	reservations := make([]correctionusecase.Reservation, 0, len(rows))
	for _, row := range rows {
		digest := sha256.Sum256(row.SelectionEvidence)
		if row.ID == uuid.Nil || row.OwnerID == uuid.Nil || row.Revision < 1 || len(row.SelectionEvidence) == 0 {
			return nil
		}
		reservations = append(reservations, correctionusecase.Reservation{
			ID: row.ID, TournamentID: scope.TournamentID, OwnerID: row.OwnerID, SourceRevisionID: sourceID,
			Revision: row.Revision, Used: false, Disclosed: false, EvidenceDigest: digest,
		})
	}
	return reservations
}

func correctionAuthoritySolve(
	ctx context.Context,
	querier *sqlc.Queries,
	input correctionAuthorityInputs,
	scope ResultScope,
	sourceCreatedAt, recordedAt time.Time,
) correctionusecase.SolveMetadata {
	if domain.GameResultReason(input.target.ResultReason) != domain.GameResultReasonSolved {
		return correctionusecase.SolveMetadata{}
	}
	solve, err := querier.GetTournamentAdminCorrectionSolve(ctx, sqlc.GetTournamentAdminCorrectionSolveParams{
		ResultEventID: input.target.ResultEventID, TournamentID: scope.TournamentID, RosterID: scope.RosterID,
		SeriesID: scope.SeriesID, GameAttemptID: scope.AttemptID,
	})
	if err != nil || !solve.SubmissionEventID.Valid || !solve.SolvedAt.Valid || len(solve.EvidenceDigest) != sha256.Size {
		return correctionusecase.SolveMetadata{}
	}
	solvedAt := correctionTime(solve.SolvedAt.Time)
	if solvedAt.Before(sourceCreatedAt) || solvedAt.After(recordedAt) {
		return correctionusecase.SolveMetadata{}
	}
	digest, valid := correctionDigestFromBytes(solve.EvidenceDigest)
	if !valid {
		return correctionusecase.SolveMetadata{}
	}
	submissionID := solve.SubmissionEventID.UUID
	return correctionusecase.SolveMetadata{SolvedAt: &solvedAt, SubmissionID: &submissionID, EvidenceDigest: digest}
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func correctionAuthorityDecisions(
	ctx context.Context,
	querier *sqlc.Queries,
	logical correctionLogicalGraph,
	input correctionAuthorityInputs,
	scope ResultScope,
) ([]resultprojection.RecordedProjectionDecision, error) {
	rows, err := querier.ListCorrectionProjectionDecisions(ctx, sqlc.ListCorrectionProjectionDecisionsParams{
		TournamentID: scope.TournamentID, RosterID: scope.RosterID,
	})
	if err != nil {
		return nil, tournamentAdminCorrectionError("load projection decisions", err)
	}
	decisions := make([]resultprojection.RecordedProjectionDecision, 0, len(rows))
	for _, row := range rows {
		projection, found := logical.byID[domain.DerivedRevisionID(row.ProjectionNodeID)]
		if !found || !correctionLogicalProjectionIsCurrent(logical, projection) {
			continue
		}
		digest, valid := correctionDigestFromBytes(row.PayloadDigest)
		if !valid || row.ID == uuid.Nil || row.SequenceNumber < 1 || !row.RecordedAt.Valid || sha256.Sum256(row.Payload) != digest {
			return nil, domain.ErrConflict
		}
		decisions = append(decisions, resultprojection.RecordedProjectionDecision{
			ID: row.ID, Sequence: int(row.SequenceNumber), ProjectionRevisionID: domain.DerivedRevisionID(row.ProjectionNodeID),
			RecordedAt: correctionTime(row.RecordedAt.Time), Payload: append([]byte(nil), row.Payload...), PayloadDigest: digest,
		})
	}
	if len(decisions) == 0 {
		var downstream domain.DerivedRevision
		for _, kind := range []domain.ArtifactKind{
			domain.ArtifactKindBracket, domain.ArtifactKindTopFour, domain.ArtifactKindStandings,
		} {
			if current, found := logical.current[domain.ArtifactRef{Kind: kind, EntityID: scope.TournamentID}]; found {
				downstream = current
				break
			}
		}
		if downstream.ID().IsZero() {
			return nil, domain.ErrConflict
		}
		payload, digest, documentErr := correctionJSONDocument(struct {
			Schema string `json:"schema"`
			Source string `json:"source"`
		}{Schema: "tournament-correction-baseline-decision-v1", Source: downstream.ID().UUID().String()})
		if documentErr != nil {
			return nil, documentErr
		}
		decisions = append(decisions, resultprojection.RecordedProjectionDecision{
			ID: correctionWorkflowUUID(input.projection.ID, "baseline-bracket-decision"), Sequence: 1,
			ProjectionRevisionID: downstream.ID(), RecordedAt: downstream.CreatedAt(), Payload: payload, PayloadDigest: digest,
		})
	}
	sort.Slice(decisions, func(first, second int) bool { return decisions[first].Sequence < decisions[second].Sequence })
	for index := range decisions {
		if decisions[index].Sequence != index+1 {
			return nil, domain.ErrConflict
		}
	}
	return decisions, nil
}

func correctionJSONRaw(value []byte) json.RawMessage {
	return append(json.RawMessage(nil), value...)
}
