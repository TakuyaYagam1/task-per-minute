package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func prepareCorrectionProjectionPlan(
	ctx context.Context,
	querier *sqlc.Queries,
	in CorrectionInput,
	current sqlc.ProjectionRevision,
	descendants []sqlc.LockCorrectionDescendantsRow,
) (correctionProjectionPlan, error) {
	affected := make(map[uuid.UUID]struct{}, len(descendants))
	for _, row := range descendants {
		affected[row.ArtifactID] = struct{}{}
	}
	links, err := querier.ListProjectionRevisionArtifacts(
		ctx,
		sqlc.ListProjectionRevisionArtifactsParams{
			RevisionID: current.ID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		},
	)
	if err != nil {
		return correctionProjectionPlan{}, fmt.Errorf("CorrectionPostgres - projection links: %w", err)
	}
	if in.ReplaceProjectionSet {
		for _, link := range links {
			affected[link.ArtifactID] = struct{}{}
		}
	}
	newByKind, err := correctionNewArtifacts(in.ProjectionArtifacts)
	if err != nil {
		return correctionProjectionPlan{}, fmt.Errorf("new projection artifacts: %w", err)
	}
	reusedWanted, err := correctionReusedArtifacts(in.ReusedProjectionArtifactIDs)
	if err != nil {
		return correctionProjectionPlan{}, err
	}
	plan, err := classifyCorrectionProjectionArtifacts(
		ctx, querier, in, links, affected, newByKind, reusedWanted,
	)
	if err != nil {
		return correctionProjectionPlan{}, err
	}
	if !validCorrectionProjectionPlan(in, plan, links, affected, newByKind, reusedWanted) {
		return correctionProjectionPlan{}, fmt.Errorf(
			"%w: projection plan links=%d affected=%d new_remaining=%d reused_remaining=%d new=%d reused=%d kinds=%t has_source=%t",
			domain.ErrValidation,
			len(links),
			len(affected),
			len(newByKind),
			len(reusedWanted),
			len(plan.newArtifacts),
			len(plan.reused),
			validCorrectionBaseArtifactKinds(plan.allKinds),
			correctionHasNewSource(in),
		)
	}
	return plan, nil
}

func correctionNewArtifacts(
	artifacts []ProjectionArtifactInput,
) (map[string]ProjectionArtifactInput, error) {
	byKind := make(map[string]ProjectionArtifactInput, len(artifacts))
	for _, artifact := range artifacts {
		kind := projectionArtifactKind(artifact.Kind)
		if _, exists := byKind[kind]; exists || !validProjectionArtifact(artifact) {
			return nil, fmt.Errorf("%w: invalid or duplicate %s artifact", domain.ErrValidation, kind)
		}
		byKind[kind] = artifact
	}
	return byKind, nil
}

func correctionReusedArtifacts(ids []uuid.UUID) (map[uuid.UUID]struct{}, error) {
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

func classifyCorrectionProjectionArtifacts(
	ctx context.Context,
	querier *sqlc.Queries,
	in CorrectionInput,
	links []sqlc.ProjectionRevisionArtifact,
	affected map[uuid.UUID]struct{},
	newByKind map[string]ProjectionArtifactInput,
	reusedWanted map[uuid.UUID]struct{},
) (correctionProjectionPlan, error) {
	plan := correctionProjectionPlan{newArtifacts: in.ProjectionArtifacts}
	for _, link := range links {
		artifact, loadErr := querier.GetProjectionArtifactScoped(
			ctx,
			sqlc.GetProjectionArtifactScopedParams{
				ID: link.ArtifactID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
			},
		)
		if loadErr != nil {
			return correctionProjectionPlan{}, correctionLookupError("projection artifact", loadErr)
		}
		_, isAffected := affected[artifact.ID]
		_, isReused := reusedWanted[artifact.ID]
		_, hasReplacement := newByKind[artifact.ArtifactKind]
		if isAffected {
			if isReused || !hasReplacement {
				return correctionProjectionPlan{}, fmt.Errorf(
					"%w: affected %s artifact reuse=%t replacement=%t",
					domain.ErrValidation,
					artifact.ArtifactKind,
					isReused,
					hasReplacement,
				)
			}
			delete(newByKind, artifact.ArtifactKind)
		} else {
			if !isReused || hasReplacement {
				return correctionProjectionPlan{}, fmt.Errorf(
					"%w: unaffected %s artifact reuse=%t replacement=%t",
					domain.ErrValidation,
					artifact.ArtifactKind,
					isReused,
					hasReplacement,
				)
			}
			delete(reusedWanted, artifact.ID)
			plan.reused = append(plan.reused, artifact)
		}
		plan.allKinds = append(plan.allKinds, correctionDomainArtifactKind(artifact.ArtifactKind))
	}
	return plan, nil
}

func validCorrectionProjectionPlan(
	in CorrectionInput,
	plan correctionProjectionPlan,
	links []sqlc.ProjectionRevisionArtifact,
	affected map[uuid.UUID]struct{},
	newByKind map[string]ProjectionArtifactInput,
	reusedWanted map[uuid.UUID]struct{},
) bool {
	return len(links) == 3 && len(affected) > 0 && len(newByKind) == 0 && len(reusedWanted) == 0 &&
		len(plan.newArtifacts)+len(plan.reused) == 3 && validCorrectionBaseArtifactKinds(plan.allKinds) &&
		correctionHasNewSource(in)
}

func validCorrectionBaseArtifactKinds(kinds []domain.ArtifactKind) bool {
	if len(kinds) != 3 {
		return false
	}
	wanted := map[domain.ArtifactKind]bool{
		domain.ArtifactKindStandings: false,
		domain.ArtifactKindBracket:   false,
		domain.ArtifactKindTopFour:   false,
	}
	for _, kind := range kinds {
		if _, ok := wanted[kind]; !ok || wanted[kind] {
			return false
		}
		wanted[kind] = true
	}
	return true
}

func correctionDomainArtifactKind(kind string) domain.ArtifactKind {
	return domain.ArtifactKind(kind)
}
