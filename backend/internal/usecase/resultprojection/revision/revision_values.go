package revision

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func cloneUUIDPointer(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneSeriesScoreRevisionIDPointer(
	value *domain.SeriesScoreRevisionID,
) *domain.SeriesScoreRevisionID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneOfficialResultRevisionIDPointer(
	value *domain.OfficialResultRevisionID,
) *domain.OfficialResultRevisionID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func derivedRevisionIDPointersEqual(
	first *domain.DerivedRevisionID,
	second *domain.DerivedRevisionID,
) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}

func derivedRevisionsEqual(first, second domain.DerivedRevision) bool {
	return first.ID() == second.ID() && first.TournamentID() == second.TournamentID() &&
		first.Artifact() == second.Artifact() && first.RevisionNo() == second.RevisionNo() &&
		derivedRevisionIDPointersEqual(first.PreviousRevisionID(), second.PreviousRevisionID()) &&
		first.CreatedAt().Equal(second.CreatedAt()) && first.PayloadDigest() == second.PayloadDigest()
}

func officialResultRevisionIDPointersEqual(
	first *domain.OfficialResultRevisionID,
	second *domain.OfficialResultRevisionID,
) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}

func seriesScoreRevisionPointersEqual(
	first *domain.SeriesScoreRevisionID,
	second *domain.SeriesScoreRevisionID,
) bool {
	if first == nil || second == nil {
		return first == second
	}
	return *first == *second
}

func uuidPointersEqual(first, second *uuid.UUID) bool {
	if first == nil || second == nil {
		return first == second
	}
	return *first == *second
}
