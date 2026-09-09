package result

import (
	"math"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func PlanSeriesScoreRevision(
	command SeriesScoreRevisionCommand,
	authority SeriesScoreRevisionAuthority,
	recordedAt time.Time,
) (SeriesScoreRevisionPlan, error) {
	command = cloneSeriesScoreRevisionCommand(command)
	authority = cloneSeriesScoreRevisionAuthority(authority)
	if err := validateSeriesScorePlanInputs(command, authority, recordedAt); err != nil {
		return SeriesScoreRevisionPlan{}, err
	}
	projectedAttempts, err := seriesScoreAttemptReferencesFromSeries(authority.ProjectedSeries)
	if err != nil {
		return SeriesScoreRevisionPlan{}, err
	}
	if err := validateSeriesScorePlanTransition(command, authority, projectedAttempts, recordedAt); err != nil {
		return SeriesScoreRevisionPlan{}, err
	}
	revision := buildSeriesScoreRevision(command, authority, projectedAttempts, recordedAt)
	condition := newSeriesScoreRevisionCondition(authority)
	condition.plannedRevisionID = command.RevisionID
	condition.plannedOperation = command.Operation
	if err := revision.Validate(); err != nil {
		return SeriesScoreRevisionPlan{}, err
	}
	if err := condition.Validate(); err != nil {
		return SeriesScoreRevisionPlan{}, err
	}
	plan := SeriesScoreRevisionPlan{condition: condition, revision: revision}
	if err := plan.Validate(); err != nil {
		return SeriesScoreRevisionPlan{}, err
	}
	return plan, nil
}

func validateSeriesScorePlanInputs(
	command SeriesScoreRevisionCommand,
	authority SeriesScoreRevisionAuthority,
	recordedAt time.Time,
) error {
	if err := validateSeriesScoreRevisionCommand(command, recordedAt); err != nil {
		return err
	}
	if err := validateSeriesScoreRevisionAuthority(authority); err != nil {
		return err
	}
	if err := validateScorePlannerUUIDRoles(command, authority); err != nil {
		return err
	}
	if authority.CurrentHead == nil || authority.PersistedSeries.State == domain.SeriesStatePlanned ||
		authority.ProjectedSeries.State == domain.SeriesStatePlanned {
		return seriesScoreRevisionConflict("Series has no current score plan")
	}
	return validateSeriesScorePlanCAS(command, authority)
}

func validateSeriesScorePlanCAS(
	command SeriesScoreRevisionCommand,
	authority SeriesScoreRevisionAuthority,
) error {
	if command.Scope != authority.Scope ||
		!derivedRevisionsEqual(command.ExpectedSourceProjection, authority.SourceProjection) {
		return seriesScoreRevisionConflict("stale score scope or source")
	}
	if !seriesScoreRevisionIDPointersEqual(
		command.ExpectedCurrentRevisionID,
		seriesScoreCurrentHeadID(authority.CurrentHead),
	) {
		return seriesScoreRevisionConflict("stale current score revision")
	}
	if command.CommandID == authority.CurrentHead.CommandID {
		return invalidSeriesScoreRevision("score successor reused command identity")
	}
	if authority.CurrentHead.PreviousRevisionID != nil &&
		command.RevisionID == *authority.CurrentHead.PreviousRevisionID {
		return invalidSeriesScoreRevision("score revision identity cycled")
	}
	if authority.ProjectedSeries.CurrentScoreRevisionID == nil ||
		*authority.ProjectedSeries.CurrentScoreRevisionID != command.RevisionID {
		return seriesScoreRevisionConflict("projected score head changed")
	}
	return nil
}

func validateSeriesScorePlanTransition(
	command SeriesScoreRevisionCommand,
	authority SeriesScoreRevisionAuthority,
	projectedAttempts []SeriesScoreAttemptReference,
	recordedAt time.Time,
) error {
	if err := validateRequestedScoreTransition(
		command,
		authority.CurrentHead.Attempts,
		projectedAttempts,
	); err != nil {
		return err
	}
	if err := validateScoreSeriesTransition(command, authority); err != nil {
		return err
	}
	return validateSeriesScoreSuccessor(command, authority, recordedAt)
}

func validateSeriesScoreSuccessor(
	command SeriesScoreRevisionCommand,
	authority SeriesScoreRevisionAuthority,
	recordedAt time.Time,
) error {
	if command.Operation == SeriesScoreRevisionOperationReplaceResult &&
		(command.Actor.Kind != domain.ResultActorOperator || command.Actor.PrincipalID == nil) {
		return invalidSeriesScoreRevision("score replacement requires operator")
	}
	if !derivedRevisionDirectSuccessor(
		authority.CurrentHead.SourceProjection,
		authority.SourceProjection,
	) {
		return seriesScoreRevisionConflict("score source is not the direct successor")
	}
	if recordedAt.Before(authority.CurrentHead.RecordedAt) {
		return invalidSeriesScoreRevision("score revision time moved backwards")
	}
	if authority.CurrentHead.Ordinal == math.MaxInt {
		return invalidSeriesScoreRevision("score revision ordinal overflow")
	}
	return nil
}

func buildSeriesScoreRevision(
	command SeriesScoreRevisionCommand,
	authority SeriesScoreRevisionAuthority,
	projectedAttempts []SeriesScoreAttemptReference,
	recordedAt time.Time,
) SeriesScoreRevision {
	return newSeriesScoreRevision(
		command.Scope,
		command.RevisionID,
		seriesScoreCurrentHeadID(authority.CurrentHead),
		authority.CurrentHead.Ordinal+1,
		command.Operation,
		command.CommandID,
		command.Actor,
		command.Attempt,
		authority.ProjectedSeries,
		projectedAttempts,
		command.ExpectedSourceProjection,
		recordedAt,
	)
}
