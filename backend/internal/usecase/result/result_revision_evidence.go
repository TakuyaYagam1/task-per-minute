package result

import (
	"math"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func validateOfficialSourceProjection(
	source domain.DerivedRevision,
	scope OfficialResultScope,
) error {
	if err := source.Validate(); err != nil || source.TournamentID() != scope.TournamentID {
		return invalidOfficialResultRevision("invalid source projection")
	}
	want := domain.ArtifactRef{EntityID: scope.SeriesID}
	switch scope.Kind {
	case OfficialResultSubjectGame:
		want.Kind = domain.ArtifactKindGameResult
		want.EntityID = scope.GameID
	case OfficialResultSubjectSeries:
		want.Kind = domain.ArtifactKindSeriesResult
	default:
		return invalidOfficialResultRevision("unknown source projection subject")
	}
	if source.Artifact() != want {
		return invalidOfficialResultRevision("source projection artifact mismatch")
	}
	return nil
}

// ValidateOfficialSourceProjection verifies that a projection belongs to the
// official result subject that a coordinator is about to update.
func ValidateOfficialSourceProjection(
	source domain.DerivedRevision,
	scope OfficialResultScope,
) error {
	return validateOfficialSourceProjection(source, scope)
}

func derivedRevisionsEqual(first, second domain.DerivedRevision) bool {
	return first.ID() == second.ID() && first.TournamentID() == second.TournamentID() &&
		first.Artifact() == second.Artifact() && first.RevisionNo() == second.RevisionNo() &&
		derivedRevisionIDPointersEqual(first.PreviousRevisionID(), second.PreviousRevisionID()) &&
		first.CreatedAt().Equal(second.CreatedAt()) && first.PayloadDigest() == second.PayloadDigest()
}

func derivedRevisionDirectSuccessor(
	previous domain.DerivedRevision,
	current domain.DerivedRevision,
) bool {
	predecessor := current.PreviousRevisionID()
	if previous.RevisionNo() == math.MaxInt {
		return false
	}
	prior := previous.PreviousRevisionID()
	if prior != nil && current.ID() == *prior {
		return false
	}
	return previous.TournamentID() == current.TournamentID() &&
		previous.Artifact() == current.Artifact() &&
		current.RevisionNo() == previous.RevisionNo()+1 && predecessor != nil &&
		*predecessor == previous.ID() && !current.CreatedAt().Before(previous.CreatedAt())
}
