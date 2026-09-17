package projection

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func (r *ProjectionPostgres) Current(
	ctx context.Context,
	scope ProjectionScope,
) (*ProjectionRecord, error) {
	if r == nil || r.tx == nil || !validProjectionScope(scope) {
		return nil, domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	revision, err := querier.GetCurrentProjectionRevision(ctx, sqlc.GetCurrentProjectionRevisionParams{
		TournamentID: scope.TournamentID, RosterID: scope.RosterID,
	})
	if err != nil {
		return nil, projectionLookupError("Current", err)
	}
	return loadProjectionRecord(ctx, querier, scope, revision.ID)
}

func (r *ProjectionPostgres) CurrentStandings(
	ctx context.Context,
	scope ProjectionScope,
) (*ProjectionArtifactRecord, error) {
	if r == nil || r.tx == nil || !validProjectionScope(scope) {
		return nil, domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	artifact, err := querier.GetCurrentStandingsArtifact(
		ctx,
		sqlc.GetCurrentStandingsArtifactParams{
			TournamentID: scope.TournamentID, RosterID: scope.RosterID,
		},
	)
	if err != nil {
		return nil, projectionLookupError("CurrentStandings", err)
	}
	return loadProjectionArtifact(ctx, querier, scope, artifact)
}

func (r *ProjectionPostgres) CurrentBracket(
	ctx context.Context,
	scope ProjectionScope,
) (*ProjectionArtifactRecord, error) {
	if r == nil || r.tx == nil || !validProjectionScope(scope) {
		return nil, domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	artifact, err := querier.GetCurrentProjectionBracketArtifact(ctx, sqlc.GetCurrentProjectionBracketArtifactParams{
		TournamentID: scope.TournamentID, RosterID: scope.RosterID,
	})
	if err != nil {
		return nil, projectionLookupError("CurrentBracket", err)
	}
	return loadProjectionArtifact(ctx, querier, scope, artifact)
}

func (r *ProjectionPostgres) History(
	ctx context.Context,
	scope ProjectionScope,
) ([]sqlc.ProjectionRevision, error) {
	if r == nil || r.tx == nil || !validProjectionScope(scope) {
		return nil, domain.ErrValidation
	}
	rows, err := r.tx.Querier(ctx).ListProjectionRevisions(
		ctx,
		sqlc.ListProjectionRevisionsParams{
			TournamentID: scope.TournamentID, RosterID: scope.RosterID,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("ProjectionPostgres - History: %w", err)
	}
	return rows, nil
}

func loadProjectionRecord(
	ctx context.Context,
	querier *sqlc.Queries,
	scope ProjectionScope,
	revisionID uuid.UUID,
) (*ProjectionRecord, error) {
	revision, err := querier.GetProjectionRevisionScoped(ctx, sqlc.GetProjectionRevisionScopedParams{
		ID: revisionID, TournamentID: scope.TournamentID, RosterID: scope.RosterID,
	})
	if err != nil {
		return nil, projectionLookupError("load revision", err)
	}
	cutoff, err := querier.GetProjectionCutoffByID(ctx, sqlc.GetProjectionCutoffByIDParams{
		ID: revision.CutoffID, TournamentID: scope.TournamentID, RosterID: scope.RosterID,
	})
	if err != nil {
		return nil, projectionLookupError("load cutoff", err)
	}
	links, err := querier.ListProjectionRevisionArtifacts(ctx, sqlc.ListProjectionRevisionArtifactsParams{
		RevisionID: revision.ID, TournamentID: scope.TournamentID, RosterID: scope.RosterID,
	})
	if err != nil {
		return nil, fmt.Errorf("ProjectionPostgres - load links: %w", err)
	}
	artifacts := make([]ProjectionArtifactRecord, 0, len(links))
	for _, link := range links {
		artifact, loadErr := querier.GetProjectionArtifactScoped(ctx, sqlc.GetProjectionArtifactScopedParams{
			ID: link.ArtifactID, TournamentID: scope.TournamentID, RosterID: scope.RosterID,
		})
		if loadErr != nil {
			return nil, projectionLookupError("load artifact", loadErr)
		}
		record, loadErr := loadProjectionArtifact(ctx, querier, scope, artifact)
		if loadErr != nil {
			return nil, loadErr
		}
		artifacts = append(artifacts, *record)
	}
	return &ProjectionRecord{Revision: revision, Cutoff: cutoff, Artifacts: artifacts}, nil
}

func loadProjectionArtifact(
	ctx context.Context,
	querier *sqlc.Queries,
	scope ProjectionScope,
	artifact sqlc.ProjectionArtifact,
) (*ProjectionArtifactRecord, error) {
	members, err := querier.ListProjectionArtifactMembers(ctx, sqlc.ListProjectionArtifactMembersParams{
		ArtifactID: artifact.ID, TournamentID: scope.TournamentID, RosterID: scope.RosterID,
	})
	if err != nil {
		return nil, fmt.Errorf("ProjectionPostgres - load members: %w", err)
	}
	dependencies, err := querier.ListProjectionArtifactDependencies(
		ctx,
		sqlc.ListProjectionArtifactDependenciesParams{
			ArtifactID: artifact.ID, TournamentID: scope.TournamentID, RosterID: scope.RosterID,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("ProjectionPostgres - load dependencies: %w", err)
	}
	return &ProjectionArtifactRecord{
		Artifact: artifact, Members: members, Dependencies: dependencies,
	}, nil
}
