package cutoff

import (
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const (
	minCorrectionYear = 2000
	maxCorrectionYear = 2200
)

func (i correctionCutoffIndex) validateRevisionChains(
	edges map[[2]domain.DerivedRevisionID]struct{},
) error {
	previous := make(map[domain.ArtifactRef]domain.DerivedRevision)
	for _, revision := range i.ordered {
		prior, exists := previous[revision.Artifact()]
		if !exists {
			if revision.RevisionNo() != 1 || revision.PreviousRevisionID() != nil {
				return rejectCorrection(
					RejectionMalformed, ErrInvalid, "broken projection lineage",
				)
			}
			previous[revision.Artifact()] = revision
			continue
		}
		predecessor := revision.PreviousRevisionID()
		if prior.RevisionNo() == int(^uint(0)>>1) || revision.RevisionNo() != prior.RevisionNo()+1 ||
			predecessor == nil || *predecessor != prior.ID() {
			return rejectCorrection(
				RejectionMalformed, ErrInvalid, "broken projection lineage",
			)
		}
		if _, linked := edges[[2]domain.DerivedRevisionID{prior.ID(), revision.ID()}]; !linked {
			return rejectCorrection(
				RejectionIncomplete, ErrInvalid, "projection predecessor edge is missing",
			)
		}
		previous[revision.Artifact()] = revision
	}
	return nil
}

func (i correctionCutoffIndex) isAcyclic() bool {
	indegree := make(map[domain.DerivedRevisionID]int, len(i.ordered))
	for _, revision := range i.ordered {
		indegree[revision.ID()] = 0
	}
	for _, derivedIDs := range i.outgoing {
		for _, derivedID := range derivedIDs {
			indegree[derivedID]++
		}
	}
	queue := make([]domain.DerivedRevisionID, 0, len(i.ordered))
	for _, revision := range i.ordered {
		if indegree[revision.ID()] == 0 {
			queue = append(queue, revision.ID())
		}
	}
	processed := 0
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		processed++
		for _, derived := range i.outgoing[current] {
			indegree[derived]--
			if indegree[derived] == 0 {
				queue = append(queue, derived)
			}
		}
	}
	return processed == len(i.ordered)
}

func (i correctionCutoffIndex) validateEvents(events []CutoffEvent) error {
	seen := make(map[uuid.UUID]struct{}, len(events))
	for _, event := range events {
		if event.TournamentID != i.tournamentID {
			return rejectCorrection(
				RejectionCrossTournament, ErrInvalid, "cutoff event belongs to another tournament",
			)
		}
		source, exists := i.byID[event.SourceRevisionID]
		if !exists {
			return rejectCorrection(
				RejectionStale, ErrInvalid, "cutoff event source is missing",
			)
		}
		if event.ID == uuid.Nil || !validCorrectionCutoffKind(event.Kind) ||
			!validCorrectionTime(event.OccurredAt) || event.OccurredAt.Before(source.CreatedAt()) {
			return rejectCorrection(
				RejectionMalformed, ErrInvalid, "invalid cutoff event",
			)
		}
		if _, duplicate := seen[event.ID]; duplicate {
			return rejectCorrection(
				RejectionIdentityAlias, ErrInvalid, "duplicate cutoff event identity",
			)
		}
		if _, aliased := i.reservedIDs[event.ID]; aliased {
			return rejectCorrection(
				RejectionIdentityAlias, ErrInvalid, "cutoff event identity aliases revision evidence",
			)
		}
		seen[event.ID] = struct{}{}
	}
	return nil
}

func validCorrectionCutoffKind(kind CutoffKind) bool {
	switch kind {
	case CutoffWaveStarted,
		CutoffTaskDelivered,
		CutoffNoShowRecorded,
		CutoffForfeitRecorded,
		CutoffGoldenAllocated:
		return true
	default:
		return false
	}
}

func validCorrectionTime(value time.Time) bool {
	return validCorrectionServerTime(value) && value.Year() >= minCorrectionYear && value.Year() <= maxCorrectionYear
}

func canonicalCorrectionTime(value time.Time) string {
	return value.Format(time.RFC3339Nano)
}
