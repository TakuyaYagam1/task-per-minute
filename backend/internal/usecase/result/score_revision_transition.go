package result

import (
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func validateSeriesScoreRevisionCommand(
	command SeriesScoreRevisionCommand,
	recordedAt time.Time,
) error {
	if err := command.Scope.Validate(); err != nil {
		return err
	}
	if command.CommandID == uuid.Nil || command.RevisionID.IsZero() || command.Actor.Validate() != nil ||
		command.ExpectedCurrentRevisionID == nil || command.ExpectedCurrentRevisionID.IsZero() ||
		command.Attempt == nil || command.Attempt.Validate() != nil ||
		!validServerTime(recordedAt) || recordedAt.Before(command.ExpectedSourceProjection.CreatedAt()) {
		return invalidSeriesScoreRevision("invalid score command provenance")
	}
	if command.Operation != SeriesScoreRevisionOperationAppendAttempt &&
		command.Operation != SeriesScoreRevisionOperationReplaceResult {
		return invalidSeriesScoreRevision("invalid score command operation")
	}
	if err := validateSeriesScoreSourceProjection(command.ExpectedSourceProjection, command.Scope); err != nil {
		return err
	}
	return validateScoreCommandUUIDRoles(command)
}

func validateSeriesScoreRevisionAuthority(authority SeriesScoreRevisionAuthority) error {
	if err := authority.Scope.Validate(); err != nil {
		return err
	}
	if err := validateSeriesScoreAuthoritySeries(authority); err != nil {
		return err
	}
	if err := validateSeriesScoreAuthorityRowsAndGames(authority); err != nil {
		return err
	}
	if err := validateSeriesScoreSourceProjection(authority.SourceProjection, authority.Scope); err != nil {
		return err
	}
	return validatePersistedSeriesScoreHead(authority)
}

func validateSeriesScoreAuthoritySeries(authority SeriesScoreRevisionAuthority) error {
	if err := authority.PersistedSeries.Validate(); err != nil {
		return invalidSeriesScoreRevision("invalid persisted Series")
	}
	if err := authority.ProjectedSeries.Validate(); err != nil {
		return invalidSeriesScoreRevision("invalid projected Series")
	}
	if authority.PersistedSeries.ID != authority.Scope.SeriesID ||
		authority.PersistedSeries.TournamentID != authority.Scope.TournamentID ||
		authority.ProjectedSeries.ID != authority.Scope.SeriesID ||
		authority.ProjectedSeries.TournamentID != authority.Scope.TournamentID ||
		authority.PersistedSeries.FirstParticipantID != authority.ProjectedSeries.FirstParticipantID ||
		authority.PersistedSeries.SecondParticipantID != authority.ProjectedSeries.SecondParticipantID ||
		authority.PersistedSeries.Format != authority.ProjectedSeries.Format {
		return seriesScoreRevisionConflict("score authority Series identity changed")
	}
	return nil
}

func validateSeriesScoreAuthorityRowsAndGames(authority SeriesScoreRevisionAuthority) error {
	if authority.SeriesRevision <= 0 ||
		(authority.CurrentHead == nil && authority.AttemptRevision != 0) ||
		(authority.CurrentHead != nil && authority.AttemptRevision <= 0) {
		return invalidSeriesScoreRevision("invalid authority row revision")
	}
	if err := validateSeriesGameIDsUnique(
		authority.PersistedSeries,
		invalidSeriesScoreRevision,
	); err != nil {
		return err
	}
	if err := validateSeriesGameIDsUnique(
		authority.ProjectedSeries,
		invalidSeriesScoreRevision,
	); err != nil {
		return err
	}
	return nil
}

func validatePersistedSeriesScoreHead(authority SeriesScoreRevisionAuthority) error {
	head := authority.PersistedSeries.CurrentScoreRevisionID
	if authority.CurrentHead == nil {
		if head != nil {
			return seriesScoreRevisionConflict("persisted score head has no current revision")
		}
		return nil
	}
	current := authority.CurrentHead
	if err := current.Validate(); err != nil || current.Scope != authority.Scope ||
		head == nil || *head != current.ID ||
		current.FirstParticipantID != authority.PersistedSeries.FirstParticipantID ||
		current.SecondParticipantID != authority.PersistedSeries.SecondParticipantID ||
		current.Format != authority.PersistedSeries.Format ||
		current.Score != authority.PersistedSeries.Score {
		return seriesScoreRevisionConflict("current score revision does not match persisted Series")
	}
	persistedAttempts, err := seriesScoreAttemptReferencesFromSeries(authority.PersistedSeries)
	if err != nil || !seriesScoreAttemptReferencesEqual(current.Attempts, persistedAttempts) {
		return seriesScoreRevisionConflict("persisted terminal attempts do not match current score revision")
	}
	return nil
}

func validateRequestedScoreTransition(
	command SeriesScoreRevisionCommand,
	current []SeriesScoreAttemptReference,
	projected []SeriesScoreAttemptReference,
) error {
	switch command.Operation {
	case SeriesScoreRevisionOperationAppendAttempt:
		if len(projected) != len(current)+1 ||
			!seriesScoreAttemptReferencesEqual(current, projected[:len(current)]) ||
			!seriesScoreAttemptReferenceEqual(*command.Attempt, projected[len(projected)-1]) {
			return seriesScoreRevisionConflict("append did not add exactly one terminal attempt")
		}
	case SeriesScoreRevisionOperationReplaceResult:
		if len(projected) != len(current) {
			return seriesScoreRevisionConflict("replacement changed attempt cardinality")
		}
		changed := -1
		for index := range current {
			if seriesScoreAttemptReferenceEqual(current[index], projected[index]) {
				continue
			}
			if changed != -1 || !sameSeriesScoreAttemptPosition(current[index], projected[index]) ||
				current[index].CurrentGameResultRevisionID == projected[index].CurrentGameResultRevisionID {
				return seriesScoreRevisionConflict("replacement changed more than one stable attempt")
			}
			changed = index
		}
		if changed == -1 || !seriesScoreAttemptReferenceEqual(*command.Attempt, projected[changed]) {
			return seriesScoreRevisionConflict("replacement did not identify the changed attempt")
		}
	case SeriesScoreRevisionOperationInitialize:
		return invalidSeriesScoreRevision("invalid score command operation")
	default:
		return invalidSeriesScoreRevision("invalid score command operation")
	}
	return nil
}

func validateScoreSeriesTransition(
	command SeriesScoreRevisionCommand,
	authority SeriesScoreRevisionAuthority,
) error {
	if len(authority.PersistedSeries.Slots) != len(authority.ProjectedSeries.Slots) {
		return seriesScoreRevisionConflict("score projection changed slot structure")
	}
	found := 0
	for slotIndex := range authority.PersistedSeries.Slots {
		slotTargets, err := validateScoreSlotTransition(
			command,
			authority.PersistedSeries.Slots[slotIndex],
			authority.ProjectedSeries.Slots[slotIndex],
		)
		if err != nil {
			return err
		}
		found += slotTargets
	}
	if found != 1 {
		return seriesScoreRevisionConflict("score target Game is not unique")
	}
	return nil
}

func validateScoreSlotTransition(
	command SeriesScoreRevisionCommand,
	persisted domain.GameSlot,
	projected domain.GameSlot,
) (int, error) {
	if !scoreSlotStructureEqual(persisted, projected) {
		return 0, seriesScoreRevisionConflict("score projection changed slot structure")
	}
	found := 0
	for gameIndex := range persisted.Attempts {
		isTarget, err := validateScoreGameTransition(
			command,
			persisted,
			persisted.Attempts[gameIndex],
			projected.Attempts[gameIndex],
		)
		if err != nil {
			return 0, err
		}
		if isTarget {
			found++
		}
	}
	return found, nil
}

func scoreSlotStructureEqual(first, second domain.GameSlot) bool {
	return first.ID == second.ID && first.SeriesID == second.SeriesID &&
		first.Position == second.Position && first.Category == second.Category &&
		first.ScoreBefore == second.ScoreBefore && len(first.Attempts) == len(second.Attempts)
}

func validateScoreGameTransition(
	command SeriesScoreRevisionCommand,
	slot domain.GameSlot,
	persisted domain.Game,
	projected domain.Game,
) (bool, error) {
	if !officialGameIdentityEqual(persisted, projected) {
		return false, seriesScoreRevisionConflict("score projection changed attempt identity")
	}
	if persisted.ID != command.Attempt.GameID {
		if !officialGameEqual(persisted, projected) {
			return false, seriesScoreRevisionConflict("score projection changed unrelated Game evidence")
		}
		return false, nil
	}
	if slot.ID != command.Attempt.SlotID || slot.Position != command.Attempt.SlotPosition ||
		persisted.AttemptNo != command.Attempt.AttemptNo {
		return false, seriesScoreRevisionConflict("score command target position changed")
	}
	return true, validateScoreTargetStateTransition(command.Operation, persisted, projected)
}

func validateScoreTargetStateTransition(
	operation SeriesScoreRevisionOperation,
	persisted domain.Game,
	projected domain.Game,
) error {
	switch operation {
	case SeriesScoreRevisionOperationAppendAttempt:
		if persisted.State.IsTerminal() || !projected.State.IsTerminal() {
			return seriesScoreRevisionConflict("append target is not a live to terminal transition")
		}
	case SeriesScoreRevisionOperationReplaceResult:
		if !persisted.State.IsTerminal() || !projected.State.IsTerminal() {
			return seriesScoreRevisionConflict("replacement target is not terminal")
		}
	case SeriesScoreRevisionOperationInitialize:
		return invalidSeriesScoreRevision("invalid score command operation")
	default:
		return invalidSeriesScoreRevision("invalid score command operation")
	}
	return nil
}

func validateSeriesScoreSourceProjection(
	source domain.DerivedRevision,
	scope SeriesScoreRevisionScope,
) error {
	if err := source.Validate(); err != nil || source.TournamentID() != scope.TournamentID ||
		source.Artifact() != (domain.ArtifactRef{
			Kind:     domain.ArtifactKindSeriesScore,
			EntityID: scope.SeriesID,
		}) {
		return invalidSeriesScoreRevision("invalid score source projection")
	}
	return nil
}

// ValidateSeriesScoreSourceProjection verifies that a projection belongs to
// the Series score aggregate that a coordinator is about to update.
func ValidateSeriesScoreSourceProjection(
	source domain.DerivedRevision,
	scope SeriesScoreRevisionScope,
) error {
	return validateSeriesScoreSourceProjection(source, scope)
}
