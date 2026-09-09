package result

import (
	"math"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func PlanOfficialResultRevision(
	command OfficialResultRevisionCommand,
	authority OfficialResultRevisionAuthority,
	recordedAt time.Time,
) (OfficialResultRevisionPlan, error) {
	command = cloneOfficialResultRevisionCommand(command)
	authority = cloneOfficialResultRevisionAuthority(authority)
	if err := validateOfficialPlanInputs(command, authority, recordedAt); err != nil {
		return OfficialResultRevisionPlan{}, err
	}
	revision, err := buildOfficialResultRevision(command, authority, recordedAt)
	if err != nil {
		return OfficialResultRevisionPlan{}, err
	}
	condition := newOfficialResultRevisionCondition(command, authority)
	plan := OfficialResultRevisionPlan{condition: condition, revision: revision}
	if err := plan.Validate(); err != nil {
		return OfficialResultRevisionPlan{}, err
	}
	return plan, nil
}

func validateOfficialPlanInputs(
	command OfficialResultRevisionCommand,
	authority OfficialResultRevisionAuthority,
	recordedAt time.Time,
) error {
	if err := validateOfficialResultRevisionCommand(command, recordedAt); err != nil {
		return err
	}
	if err := validateOfficialResultRevisionAuthority(authority); err != nil {
		return err
	}
	if err := validateOfficialPlanOwnership(command, authority); err != nil {
		return err
	}
	if err := validateOfficialPlannerUUIDRoles(command, authority); err != nil {
		return err
	}
	if err := validateOfficialPlanCAS(command, authority); err != nil {
		return err
	}
	return validateOfficialPlanProjection(command, authority, recordedAt)
}

func validateOfficialPlanOwnership(
	command OfficialResultRevisionCommand,
	authority OfficialResultRevisionAuthority,
) error {
	if err := validateOfficialResultIdentityOwnership(
		authority.PersistedSeries,
		command.Scope,
		command.RevisionID,
		false,
	); err != nil {
		return err
	}
	return validateOfficialResultIdentityOwnership(
		authority.ProjectedSeries,
		command.Scope,
		command.RevisionID,
		true,
	)
}

func validateOfficialPlanCAS(
	command OfficialResultRevisionCommand,
	authority OfficialResultRevisionAuthority,
) error {
	if command.Scope != authority.Scope ||
		!derivedRevisionsEqual(command.ExpectedSourceProjection, authority.SourceProjection) {
		return officialResultRevisionConflict("stale result scope or source")
	}
	if !officialResultRevisionIDPointersEqual(
		command.ExpectedCurrentRevisionID,
		officialResultCurrentHeadID(authority.CurrentHead),
	) {
		return officialResultRevisionConflict("stale current result revision")
	}
	if authority.CurrentHead == nil {
		return nil
	}
	if command.CommandID == authority.CurrentHead.CommandID {
		return invalidOfficialResultRevision("result successor reused command identity")
	}
	if authority.CurrentHead.PreviousRevisionID != nil &&
		command.RevisionID == *authority.CurrentHead.PreviousRevisionID {
		return invalidOfficialResultRevision("result revision identity cycled")
	}
	return nil
}

func validateOfficialPlanProjection(
	command OfficialResultRevisionCommand,
	authority OfficialResultRevisionAuthority,
	recordedAt time.Time,
) error {
	projectedOutcome, err := officialProjectedOutcome(authority)
	if err != nil {
		return err
	}
	if !officialResultOutcomesEqual(command.Outcome, projectedOutcome) {
		return officialResultRevisionConflict("projected result changed")
	}
	if err := validateOfficialProjectedHead(command, authority); err != nil {
		return err
	}
	return validateOfficialSuccessor(command, authority, recordedAt)
}

func validateOfficialSuccessor(
	command OfficialResultRevisionCommand,
	authority OfficialResultRevisionAuthority,
	recordedAt time.Time,
) error {
	if authority.CurrentHead == nil {
		return nil
	}
	if command.Actor.Kind != domain.ResultActorOperator || command.Actor.PrincipalID == nil {
		return invalidOfficialResultRevision("result successor requires operator")
	}
	if !derivedRevisionDirectSuccessor(
		authority.CurrentHead.SourceProjection,
		authority.SourceProjection,
	) {
		return officialResultRevisionConflict("result source is not the direct successor")
	}
	if recordedAt.Before(authority.CurrentHead.RecordedAt) {
		return invalidOfficialResultRevision("result revision time moved backwards")
	}
	return nil
}

func buildOfficialResultRevision(
	command OfficialResultRevisionCommand,
	authority OfficialResultRevisionAuthority,
	recordedAt time.Time,
) (OfficialResultRevision, error) {
	ordinal := 1
	var previousRevisionID *domain.OfficialResultRevisionID
	if authority.CurrentHead != nil {
		if authority.CurrentHead.Ordinal == math.MaxInt {
			return OfficialResultRevision{}, invalidOfficialResultRevision("result revision ordinal overflow")
		}
		ordinal = authority.CurrentHead.Ordinal + 1
		previousRevisionID = officialResultCurrentHeadID(authority.CurrentHead)
	}
	return OfficialResultRevision{
		scope:              command.Scope,
		id:                 command.RevisionID,
		previousRevisionID: previousRevisionID,
		ordinal:            ordinal,
		commandID:          command.CommandID,
		actor:              cloneResultActor(command.Actor),
		outcome:            cloneOfficialResultOutcome(command.Outcome),
		sourceProjection:   cloneDerivedRevision(command.ExpectedSourceProjection),
		recordedAt:         recordedAt,
	}, nil
}

func newOfficialResultRevisionCondition(
	command OfficialResultRevisionCommand,
	authority OfficialResultRevisionAuthority,
) OfficialResultRevisionCondition {
	return OfficialResultRevisionCondition{
		scope:                    command.Scope,
		expectedSeries:           cloneRevisionSeries(authority.PersistedSeries),
		expectedCurrentHead:      cloneOfficialResultRevisionHeadPointer(authority.CurrentHead),
		expectedSourceProjection: cloneDerivedRevision(authority.SourceProjection),
		expectedSeriesRevision:   authority.SeriesRevision,
		expectedAttemptRevision:  authority.AttemptRevision,
		plannedRevisionID:        command.RevisionID,
	}
}
