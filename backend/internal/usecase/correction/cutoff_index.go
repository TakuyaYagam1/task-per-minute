package correction

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
)

type correctionCutoffIndex struct {
	ordered      []domain.DerivedRevision
	byID         map[domain.DerivedRevisionID]domain.DerivedRevision
	current      map[domain.ArtifactRef]domain.DerivedRevisionID
	outgoing     map[domain.DerivedRevisionID][]domain.DerivedRevisionID
	reservedIDs  map[uuid.UUID]struct{}
	tournamentID uuid.UUID
}

//nolint:gocyclo // The bounded index validates all graph, identity, and event invariants in one pass.
func newCorrectionCutoffIndex(
	snapshot resultprojection.RevisionDAGSnapshot,
	input CutoffInput,
) (correctionCutoffIndex, error) {
	index := correctionCutoffIndex{
		ordered:      snapshotRevisions(snapshot),
		byID:         make(map[domain.DerivedRevisionID]domain.DerivedRevision, len(snapshot.Projections)),
		current:      make(map[domain.ArtifactRef]domain.DerivedRevisionID),
		outgoing:     make(map[domain.DerivedRevisionID][]domain.DerivedRevisionID),
		reservedIDs:  correctionProjectionRebuildReservedIDs(input.DAG),
		tournamentID: input.TournamentID,
	}
	for projectionIndex, revision := range index.ordered {
		if snapshot.Projections[projectionIndex].Validate() != nil {
			return correctionCutoffIndex{}, rejectCorrection(
				RejectionMalformed, ErrInvalid, "invalid projection revision",
			)
		}
		if revision.TournamentID() != input.TournamentID {
			return correctionCutoffIndex{}, rejectCorrection(
				RejectionCrossTournament, ErrInvalid, "revision belongs to another tournament",
			)
		}
		if _, duplicate := index.byID[revision.ID()]; duplicate {
			return correctionCutoffIndex{}, rejectCorrection(
				RejectionIdentityAlias, ErrInvalid, "duplicate projection identity",
			)
		}
		index.byID[revision.ID()] = revision
		currentID, exists := index.current[revision.Artifact()]
		if !exists || index.byID[currentID].RevisionNo() < revision.RevisionNo() {
			index.current[revision.Artifact()] = revision.ID()
		}
	}
	edges := make(map[[2]domain.DerivedRevisionID]struct{}, len(snapshot.Dependencies))
	for _, dependency := range snapshot.Dependencies {
		source, sourceExists := index.byID[dependency.SourceRevisionID]
		derived, derivedExists := index.byID[dependency.DerivedRevisionID]
		key := [2]domain.DerivedRevisionID{
			dependency.SourceRevisionID, dependency.DerivedRevisionID,
		}
		if !sourceExists || !derivedExists || dependency.SourceRevisionID == dependency.DerivedRevisionID ||
			derived.CreatedAt().Before(source.CreatedAt()) {
			return correctionCutoffIndex{}, rejectCorrection(
				RejectionMalformed, ErrInvalid, "invalid projection dependency",
			)
		}
		if _, duplicate := edges[key]; duplicate {
			return correctionCutoffIndex{}, rejectCorrection(
				RejectionMalformed, ErrInvalid, "duplicate projection dependency",
			)
		}
		if source.Artifact() != derived.Artifact() &&
			!validCorrectionRevisionDAGEdge(source.Artifact().Kind, derived.Artifact().Kind) {
			return correctionCutoffIndex{}, rejectCorrection(
				RejectionMalformed, ErrInvalid, "invalid projection dependency kind",
			)
		}
		edges[key] = struct{}{}
		index.outgoing[dependency.SourceRevisionID] = append(
			index.outgoing[dependency.SourceRevisionID], dependency.DerivedRevisionID,
		)
	}
	if err := index.validateRevisionChains(edges); err != nil {
		return correctionCutoffIndex{}, err
	}
	if !index.isAcyclic() {
		return correctionCutoffIndex{}, rejectCorrection(
			RejectionMalformed, ErrInvalid, "projection graph is cyclic",
		)
	}
	target, exists := index.byID[input.TargetRevisionID]
	if !exists {
		return correctionCutoffIndex{}, rejectCorrection(
			RejectionStale, ErrInvalid, "target projection is missing",
		)
	}
	if index.current[target.Artifact()] != input.TargetRevisionID {
		return correctionCutoffIndex{}, rejectCorrection(
			RejectionStale, ErrInvalid, "target projection is not current",
		)
	}
	if err := index.validateEvents(input.Events); err != nil {
		return correctionCutoffIndex{}, err
	}
	return index, nil
}

func snapshotRevisions(snapshot resultprojection.RevisionDAGSnapshot) []domain.DerivedRevision {
	revisions := make([]domain.DerivedRevision, len(snapshot.Projections))
	for index := range snapshot.Projections {
		revisions[index] = snapshot.Projections[index].Revision()
	}
	return revisions
}
