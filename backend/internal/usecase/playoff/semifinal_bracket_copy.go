package playoff

import (
	"bytes"
	"sort"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func cloneSemifinalBracketAuthority(input semifinalBracketAuthority) semifinalBracketAuthority {
	clone := input
	if input.Previous != nil {
		clone.Previous = cloneSemifinalPredecessorReceipt(input.Previous)
	}
	clone.Top4 = input.Top4.Snapshot()
	return clone
}

func canonicalSemifinalReservedIDs(input map[uuid.UUID]struct{}) []uuid.UUID {
	if len(input) > maxPlayoffReservedIdentities {
		return nil
	}
	reserved := make([]uuid.UUID, 0, len(input))
	for id := range input {
		reserved = append(reserved, id)
	}
	sort.Slice(reserved, func(i, j int) bool {
		return bytes.Compare(reserved[i][:], reserved[j][:]) < 0
	})
	return reserved
}

func cloneSemifinalPredecessorReceipt(
	input *semifinalBracketPredecessorReceipt,
) *semifinalBracketPredecessorReceipt {
	if input == nil {
		return nil
	}
	if len(input.Reserved) > maxPlayoffReservedIdentities {
		return &semifinalBracketPredecessorReceipt{
			Projection: cloneFinalSwissDomainProjection(input.Projection), Top4RevisionID: input.Top4RevisionID,
			Semifinals: cloneSemifinalMatches(input.Semifinals), LockedAt: input.LockedAt,
		}
	}
	return &semifinalBracketPredecessorReceipt{
		Projection: cloneFinalSwissDomainProjection(input.Projection), Top4RevisionID: input.Top4RevisionID,
		Reserved:   append([]uuid.UUID(nil), input.Reserved...),
		Semifinals: cloneSemifinalMatches(input.Semifinals), LockedAt: input.LockedAt,
	}
}

func cloneSemifinalBracketState(input semifinalBracketState) semifinalBracketState {
	clone := input
	clone.Authority = cloneSemifinalBracketAuthority(input.Authority)
	clone.Projection = cloneFinalSwissDomainProjection(input.Projection)
	clone.Dependencies = append([]domain.RevisionDependency(nil), input.Dependencies...)
	clone.Semifinals = cloneSemifinalMatches(input.Semifinals)
	return clone
}

func cloneSemifinalMatches(input []SemifinalMatch) []SemifinalMatch {
	clone := make([]SemifinalMatch, len(input))
	for index, match := range input {
		clone[index] = match
		clone[index].Series = cloneTerminalSeries(match.Series)
	}
	return clone
}
