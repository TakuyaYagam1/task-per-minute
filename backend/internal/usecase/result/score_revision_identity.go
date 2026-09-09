package result

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

// RestoreOrdinarySeriesScoreHead restores a score node after its repository has
// verified ordinary result-commit (or score-genesis Wave) provenance.
func RestoreOrdinarySeriesScoreHead(head SeriesScoreRevisionHead) (SeriesScoreRevisionHead, error) {
	head = head.Clone()
	head.sourceOrigin = ordinaryRevisionSource
	if err := head.Validate(); err != nil {
		return SeriesScoreRevisionHead{}, err
	}
	return head, nil
}

// RestoreCorrectionSeriesScoreHead restores a correction-backed score only
// after its repository has proved the exact source-to-node binding and scope.
func RestoreCorrectionSeriesScoreHead(
	head SeriesScoreRevisionHead,
	binding PersistedCorrectionSourceBinding,
) (SeriesScoreRevisionHead, error) {
	head = head.Clone()
	if head.PreviousRevisionID == nil {
		return SeriesScoreRevisionHead{}, domain.ErrValidation
	}
	restored, err := restoreCorrectionSourceBinding(
		binding, head.Scope.TournamentID, head.Scope.SeriesID, head.Scope.SeriesID,
		domain.ArtifactKindSeriesScore, head.CommandID, head.ID.UUID(), head.PreviousRevisionID.UUID(),
		head.SourceProjection, head.Ordinal,
	)
	if err != nil {
		return SeriesScoreRevisionHead{}, err
	}
	head.sourceOrigin = correctionRevisionSource
	head.correctionSource = restored
	if err := head.Validate(); err != nil {
		return SeriesScoreRevisionHead{}, err
	}
	return head, nil
}

func (h SeriesScoreRevisionHead) HasOrdinarySourceIdentity() bool {
	return h.sourceOrigin == ordinaryRevisionSource && h.Validate() == nil
}

// HasCorrectionSourceIdentity reports only the private origin installed by
// RestoreCorrectionSeriesScoreHead after exact binding validation.
func (h SeriesScoreRevisionHead) HasCorrectionSourceIdentity() bool {
	return h.sourceOrigin == correctionRevisionSource && h.Validate() == nil
}

func validateScoreCommandUUIDRoles(command SeriesScoreRevisionCommand) error {
	roles := newPlannerUUIDRegistry(invalidSeriesScoreRevision)
	values := []struct {
		id   uuid.UUID
		role plannerUUIDRole
	}{
		{command.Scope.TournamentID, plannerRoleTournament},
		{command.Scope.SeriesID, plannerRoleSeries},
		{command.CommandID, plannerRoleCommand},
		{command.RevisionID.UUID(), plannerRoleScore},
		{command.ExpectedCurrentRevisionID.UUID(), plannerRoleScore},
		{command.Attempt.SlotID, plannerRoleSlot},
		{command.Attempt.GameID, plannerRoleGame},
		{command.Attempt.CurrentGameResultRevisionID.UUID(), plannerRoleOfficial},
	}
	if command.RevisionID == *command.ExpectedCurrentRevisionID {
		return invalidSeriesScoreRevision("score revision did not advance")
	}
	if command.Attempt.WinnerID != nil {
		values = append(values, struct {
			id   uuid.UUID
			role plannerUUIDRole
		}{*command.Attempt.WinnerID, plannerRoleParticipant})
	}
	for _, value := range values {
		if err := roles.add(value.id, value.role); err != nil {
			return err
		}
	}
	if err := roles.addSource(command.ExpectedSourceProjection); err != nil {
		return err
	}
	return roles.addActor(command.Actor)
}

func validateSeriesScoreRevisionLocalUUIDRoles(revision SeriesScoreRevision) error {
	roles := newPlannerUUIDRegistry(invalidSeriesScoreRevision)
	values := []struct {
		id   uuid.UUID
		role plannerUUIDRole
	}{
		{revision.scope.TournamentID, plannerRoleTournament},
		{revision.scope.SeriesID, plannerRoleSeries},
		{revision.commandID, plannerRoleCommand},
		{revision.id.UUID(), plannerRoleScore},
		{revision.firstParticipantID, plannerRoleParticipant},
		{revision.secondParticipantID, plannerRoleParticipant},
	}
	if revision.previousRevisionID != nil {
		values = append(values, struct {
			id   uuid.UUID
			role plannerUUIDRole
		}{revision.previousRevisionID.UUID(), plannerRoleScore})
	}
	for _, value := range values {
		if err := roles.add(value.id, value.role); err != nil {
			return err
		}
	}
	for _, reference := range revision.attempts {
		if err := roles.addAttempt(reference); err != nil {
			return err
		}
	}
	if revision.sourceOrigin == ordinaryRevisionSource {
		var previous uuid.UUID
		if revision.previousRevisionID != nil {
			previous = revision.previousRevisionID.UUID()
		}
		if !matchesOrdinarySource(revision.sourceProjection, revision.id.UUID(), revision.ordinal, previous) {
			return invalidSeriesScoreRevision("ordinary source lineage does not match score revision")
		}
	} else if revision.sourceOrigin == correctionRevisionSource {
		if revision.previousRevisionID == nil || !matchesCorrectionSourceBinding(
			revision.correctionSource, revision.scope.TournamentID, revision.scope.SeriesID, revision.scope.SeriesID,
			domain.ArtifactKindSeriesScore, revision.commandID, revision.id.UUID(), revision.previousRevisionID.UUID(),
			revision.sourceProjection, revision.ordinal,
		) {
			return invalidSeriesScoreRevision("invalid persisted correction source binding")
		}
	} else if revision.sourceOrigin != 0 {
		return invalidSeriesScoreRevision("unknown score source origin")
	} else if err := roles.addSource(revision.sourceProjection); err != nil {
		return err
	}
	return roles.addActor(revision.actor)
}

func validateScorePlannerUUIDRoles(
	command SeriesScoreRevisionCommand,
	authority SeriesScoreRevisionAuthority,
) error {
	return validateScorePlannerSeriesUUIDRoles(
		authority,
		command.CommandID,
		command.RevisionID,
		command.Actor,
		command.ExpectedSourceProjection,
		command.Attempt,
	)
}

func validateScorePlannerSeriesUUIDRoles(
	authority SeriesScoreRevisionAuthority,
	commandID uuid.UUID,
	revisionID domain.SeriesScoreRevisionID,
	actor domain.ResultActor,
	source domain.DerivedRevision,
	commandAttempt *SeriesScoreAttemptReference,
) error {
	roles := newPlannerUUIDRegistry(invalidSeriesScoreRevision)
	for _, value := range []struct {
		id   uuid.UUID
		role plannerUUIDRole
	}{
		{commandID, plannerRoleCommand},
		{revisionID.UUID(), plannerRoleScore},
	} {
		if err := roles.add(value.id, value.role); err != nil {
			return err
		}
	}
	if err := roles.addSource(source); err != nil {
		return err
	}
	if err := roles.addActor(actor); err != nil {
		return err
	}
	if authority.CurrentHead != nil {
		if err := addScoreHeadUUIDRoles(roles, *authority.CurrentHead); err != nil {
			return err
		}
	}
	if commandAttempt != nil {
		if err := roles.addAttempt(*commandAttempt); err != nil {
			return err
		}
	}
	for _, series := range []domain.Series{authority.PersistedSeries, authority.ProjectedSeries} {
		if err := roles.addSeries(series); err != nil {
			return err
		}
	}
	return nil
}

func addScoreHeadUUIDRoles(
	roles *plannerUUIDRegistry,
	head SeriesScoreRevisionHead,
) error {
	for _, value := range []struct {
		id   uuid.UUID
		role plannerUUIDRole
	}{
		{head.ID.UUID(), plannerRoleScore},
		{head.CommandID, plannerRoleCommand},
		{head.FirstParticipantID, plannerRoleParticipant},
		{head.SecondParticipantID, plannerRoleParticipant},
	} {
		if err := roles.add(value.id, value.role); err != nil {
			return err
		}
	}
	if head.PreviousRevisionID != nil {
		if err := roles.add(head.PreviousRevisionID.UUID(), plannerRoleScore); err != nil {
			return err
		}
	}
	for _, reference := range head.Attempts {
		if err := roles.addAttempt(reference); err != nil {
			return err
		}
	}
	if !head.HasOrdinarySourceIdentity() && !head.HasCorrectionSourceIdentity() {
		if err := roles.addSource(head.SourceProjection); err != nil {
			return err
		}
	}
	return roles.addActor(head.Actor)
}

func seriesScoreCurrentHeadID(
	head *SeriesScoreRevisionHead,
) *domain.SeriesScoreRevisionID {
	if head == nil {
		return nil
	}
	id := head.ID
	return &id
}

func seriesScoreRevisionIDPointersEqual(
	first *domain.SeriesScoreRevisionID,
	second *domain.SeriesScoreRevisionID,
) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}

func seriesScoreAttemptReferencesEqual(
	first []SeriesScoreAttemptReference,
	second []SeriesScoreAttemptReference,
) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if !seriesScoreAttemptReferenceEqual(first[index], second[index]) {
			return false
		}
	}
	return true
}

func seriesScoreAttemptReferenceEqual(
	first SeriesScoreAttemptReference,
	second SeriesScoreAttemptReference,
) bool {
	return first.SlotID == second.SlotID && first.SlotPosition == second.SlotPosition &&
		first.GameID == second.GameID && first.AttemptNo == second.AttemptNo &&
		first.State == second.State && uuidPointersEqual(first.WinnerID, second.WinnerID) &&
		first.Reason == second.Reason &&
		first.CurrentGameResultRevisionID == second.CurrentGameResultRevisionID
}

func cloneSeriesScoreAttemptReference(
	reference SeriesScoreAttemptReference,
) SeriesScoreAttemptReference {
	clone := reference
	clone.WinnerID = cloneUUIDPointer(reference.WinnerID)
	return clone
}

func cloneSeriesScoreAttemptReferencePointer(
	reference *SeriesScoreAttemptReference,
) *SeriesScoreAttemptReference {
	if reference == nil {
		return nil
	}
	clone := cloneSeriesScoreAttemptReference(*reference)
	return &clone
}

func cloneSeriesScoreAttemptReferences(
	references []SeriesScoreAttemptReference,
) []SeriesScoreAttemptReference {
	cloned := make([]SeriesScoreAttemptReference, len(references))
	for index := range references {
		cloned[index] = cloneSeriesScoreAttemptReference(references[index])
	}
	return cloned
}

func cloneSeriesScoreTerminalEvidence(
	evidence SeriesScoreTerminalEvidence,
) SeriesScoreTerminalEvidence {
	return evidence
}

func cloneSeriesScoreTerminalEvidencePointer(
	evidence *SeriesScoreTerminalEvidence,
) *SeriesScoreTerminalEvidence {
	if evidence == nil {
		return nil
	}
	clone := cloneSeriesScoreTerminalEvidence(*evidence)
	return &clone
}

func cloneSeriesScoreRevision(revision SeriesScoreRevision) SeriesScoreRevision {
	clone := revision
	clone.previousRevisionID = cloneSeriesScoreRevisionIDPointer(revision.previousRevisionID)
	clone.actor = cloneResultActor(revision.actor)
	clone.commandAttempt = cloneSeriesScoreAttemptReferencePointer(revision.commandAttempt)
	clone.attempts = cloneSeriesScoreAttemptReferences(revision.attempts)
	clone.terminalEvidence = cloneSeriesScoreTerminalEvidencePointer(revision.terminalEvidence)
	clone.sourceProjection = cloneDerivedRevision(revision.sourceProjection)
	return clone
}

func seriesScoreRevisionFromHead(head SeriesScoreRevisionHead) SeriesScoreRevision {
	return SeriesScoreRevision{
		sourceOrigin:        head.sourceOrigin,
		correctionSource:    head.correctionSource,
		scope:               head.Scope,
		id:                  head.ID,
		previousRevisionID:  cloneSeriesScoreRevisionIDPointer(head.PreviousRevisionID),
		ordinal:             head.Ordinal,
		operation:           head.Operation,
		commandID:           head.CommandID,
		actor:               cloneResultActor(head.Actor),
		commandAttempt:      cloneSeriesScoreAttemptReferencePointer(head.CommandAttempt),
		firstParticipantID:  head.FirstParticipantID,
		secondParticipantID: head.SecondParticipantID,
		format:              head.Format,
		score:               head.Score,
		attempts:            cloneSeriesScoreAttemptReferences(head.Attempts),
		terminalEvidence:    cloneSeriesScoreTerminalEvidencePointer(head.TerminalEvidence),
		sourceProjection:    cloneDerivedRevision(head.SourceProjection),
		recordedAt:          head.RecordedAt,
	}
}

func cloneSeriesScoreRevisionHead(head SeriesScoreRevisionHead) SeriesScoreRevisionHead {
	clone := head
	clone.PreviousRevisionID = cloneSeriesScoreRevisionIDPointer(head.PreviousRevisionID)
	clone.Actor = cloneResultActor(head.Actor)
	clone.CommandAttempt = cloneSeriesScoreAttemptReferencePointer(head.CommandAttempt)
	clone.Attempts = cloneSeriesScoreAttemptReferences(head.Attempts)
	clone.TerminalEvidence = cloneSeriesScoreTerminalEvidencePointer(head.TerminalEvidence)
	clone.SourceProjection = cloneDerivedRevision(head.SourceProjection)
	return clone
}

func cloneSeriesScoreRevisionHeadPointer(
	head *SeriesScoreRevisionHead,
) *SeriesScoreRevisionHead {
	if head == nil {
		return nil
	}
	clone := cloneSeriesScoreRevisionHead(*head)
	return &clone
}

func cloneSeriesScoreRevisionCondition(
	condition SeriesScoreRevisionCondition,
) SeriesScoreRevisionCondition {
	clone := condition
	clone.expectedSeries = cloneRevisionSeries(condition.expectedSeries)
	clone.expectedCurrentHead = cloneSeriesScoreRevisionHeadPointer(condition.expectedCurrentHead)
	clone.expectedSourceProjection = cloneDerivedRevision(condition.expectedSourceProjection)
	return clone
}

func cloneSeriesScoreRevisionCommand(
	command SeriesScoreRevisionCommand,
) SeriesScoreRevisionCommand {
	clone := command
	clone.Actor = cloneResultActor(command.Actor)
	clone.ExpectedCurrentRevisionID = cloneSeriesScoreRevisionIDPointer(
		command.ExpectedCurrentRevisionID,
	)
	clone.ExpectedSourceProjection = cloneDerivedRevision(command.ExpectedSourceProjection)
	clone.Attempt = cloneSeriesScoreAttemptReferencePointer(command.Attempt)
	return clone
}

func cloneSeriesScoreRevisionAuthority(
	authority SeriesScoreRevisionAuthority,
) SeriesScoreRevisionAuthority {
	clone := authority
	clone.PersistedSeries = cloneRevisionSeries(authority.PersistedSeries)
	clone.ProjectedSeries = cloneRevisionSeries(authority.ProjectedSeries)
	clone.SourceProjection = cloneDerivedRevision(authority.SourceProjection)
	clone.CurrentHead = cloneSeriesScoreRevisionHeadPointer(authority.CurrentHead)
	return clone
}

func invalidSeriesScoreRevision(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidSeriesScoreRevision, message)
}

func seriesScoreRevisionConflict(message string) error {
	return fmt.Errorf("%w: %s", ErrSeriesScoreRevisionConflict, message)
}
