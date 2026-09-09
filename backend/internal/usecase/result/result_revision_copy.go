package result

import (
	"fmt"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func derivedRevisionIDPointersEqual(
	first *domain.DerivedRevisionID,
	second *domain.DerivedRevisionID,
) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}

func cloneDerivedRevision(value domain.DerivedRevision) domain.DerivedRevision {
	return value
}

func cloneRevisionSeries(series domain.Series) domain.Series {
	clone := series
	clone.WinnerID = cloneUUIDPointer(series.WinnerID)
	clone.CurrentScoreRevisionID = cloneSeriesScoreRevisionIDPointer(series.CurrentScoreRevisionID)
	clone.CurrentResultRevisionID = cloneOfficialResultRevisionIDPointer(series.CurrentResultRevisionID)
	if series.Slots == nil {
		clone.Slots = nil
		return clone
	}
	clone.Slots = make([]domain.GameSlot, len(series.Slots))
	for slotIndex := range series.Slots {
		slot := series.Slots[slotIndex]
		clone.Slots[slotIndex] = slot
		if slot.Attempts == nil {
			clone.Slots[slotIndex].Attempts = nil
			continue
		}
		clone.Slots[slotIndex].Attempts = make([]domain.Game, len(slot.Attempts))
		for gameIndex := range slot.Attempts {
			clone.Slots[slotIndex].Attempts[gameIndex] = cloneGame(slot.Attempts[gameIndex])
		}
	}
	return clone
}

func cloneResultActor(actor domain.ResultActor) domain.ResultActor {
	clone := actor
	clone.PrincipalID = cloneUUIDPointer(actor.PrincipalID)
	return clone
}

func cloneOfficialResultOutcome(outcome OfficialResultOutcome) OfficialResultOutcome {
	clone := outcome
	clone.WinnerID = cloneUUIDPointer(outcome.WinnerID)
	clone.ScoreRevisionID = cloneSeriesScoreRevisionIDPointer(outcome.ScoreRevisionID)
	return clone
}

func cloneOfficialResultRevision(
	revision OfficialResultRevision,
) OfficialResultRevision {
	clone := revision
	clone.previousRevisionID = cloneOfficialResultRevisionIDPointer(revision.previousRevisionID)
	clone.actor = cloneResultActor(revision.actor)
	clone.outcome = cloneOfficialResultOutcome(revision.outcome)
	clone.sourceProjection = cloneDerivedRevision(revision.sourceProjection)
	return clone
}

func officialResultRevisionFromHead(head OfficialResultRevisionHead) OfficialResultRevision {
	return OfficialResultRevision{
		sourceOrigin:       head.sourceOrigin,
		correctionSource:   head.correctionSource,
		scope:              head.Scope,
		id:                 head.ID,
		previousRevisionID: cloneOfficialResultRevisionIDPointer(head.PreviousRevisionID),
		ordinal:            head.Ordinal,
		commandID:          head.CommandID,
		actor:              cloneResultActor(head.Actor),
		outcome:            cloneOfficialResultOutcome(head.Outcome),
		sourceProjection:   cloneDerivedRevision(head.SourceProjection),
		recordedAt:         head.RecordedAt,
	}
}

func cloneOfficialResultRevisionHead(head OfficialResultRevisionHead) OfficialResultRevisionHead {
	clone := head
	clone.PreviousRevisionID = cloneOfficialResultRevisionIDPointer(head.PreviousRevisionID)
	clone.Actor = cloneResultActor(head.Actor)
	clone.Outcome = cloneOfficialResultOutcome(head.Outcome)
	clone.SourceProjection = cloneDerivedRevision(head.SourceProjection)
	return clone
}

func cloneOfficialResultRevisionHeadPointer(
	head *OfficialResultRevisionHead,
) *OfficialResultRevisionHead {
	if head == nil {
		return nil
	}
	clone := cloneOfficialResultRevisionHead(*head)
	return &clone
}

func cloneOfficialResultRevisionCondition(
	condition OfficialResultRevisionCondition,
) OfficialResultRevisionCondition {
	clone := condition
	clone.expectedSeries = cloneRevisionSeries(condition.expectedSeries)
	clone.expectedCurrentHead = cloneOfficialResultRevisionHeadPointer(condition.expectedCurrentHead)
	clone.expectedSourceProjection = cloneDerivedRevision(condition.expectedSourceProjection)
	return clone
}

func cloneOfficialResultRevisionCommand(
	command OfficialResultRevisionCommand,
) OfficialResultRevisionCommand {
	clone := command
	clone.Actor = cloneResultActor(command.Actor)
	clone.ExpectedCurrentRevisionID = cloneOfficialResultRevisionIDPointer(
		command.ExpectedCurrentRevisionID,
	)
	clone.ExpectedSourceProjection = cloneDerivedRevision(command.ExpectedSourceProjection)
	clone.Outcome = cloneOfficialResultOutcome(command.Outcome)
	return clone
}

func cloneOfficialResultRevisionAuthority(
	authority OfficialResultRevisionAuthority,
) OfficialResultRevisionAuthority {
	clone := authority
	clone.PersistedSeries = cloneRevisionSeries(authority.PersistedSeries)
	clone.ProjectedSeries = cloneRevisionSeries(authority.ProjectedSeries)
	clone.SourceProjection = cloneDerivedRevision(authority.SourceProjection)
	clone.CurrentHead = cloneOfficialResultRevisionHeadPointer(authority.CurrentHead)
	return clone
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

func invalidOfficialResultRevision(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidOfficialResultRevision, message)
}

func officialResultRevisionConflict(message string) error {
	return fmt.Errorf("%w: %s", ErrOfficialResultRevisionConflict, message)
}
