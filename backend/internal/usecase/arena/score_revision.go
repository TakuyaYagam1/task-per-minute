package arena

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var (
	ErrInvalidSeriesScoreRevision  = errors.New("invalid Arena Series score revision")
	ErrSeriesScoreRevisionConflict = errors.New("arena series score revision conflict")
)

type SeriesScoreRevisionOperation string

const (
	SeriesScoreRevisionOperationInitialize    SeriesScoreRevisionOperation = "initialize"
	SeriesScoreRevisionOperationAppendAttempt SeriesScoreRevisionOperation = "append_attempt"
	SeriesScoreRevisionOperationReplaceResult SeriesScoreRevisionOperation = "replace_result"
)

type SeriesScoreRevisionScope struct {
	TournamentID uuid.UUID
	SeriesID     uuid.UUID
}

func (s SeriesScoreRevisionScope) Validate() error {
	if s.TournamentID == uuid.Nil || s.SeriesID == uuid.Nil || s.TournamentID == s.SeriesID {
		return invalidSeriesScoreRevision("invalid score revision scope")
	}
	return nil
}

type SeriesScoreAttemptReference struct {
	SlotID                      uuid.UUID
	SlotPosition                int
	GameID                      uuid.UUID
	AttemptNo                   int
	State                       domain.ArenaGameState
	WinnerID                    *uuid.UUID
	Reason                      domain.ArenaGameResultReason
	CurrentGameResultRevisionID domain.ArenaOfficialResultRevisionID
}

func (r SeriesScoreAttemptReference) Validate() error {
	if r.SlotID == uuid.Nil || r.GameID == uuid.Nil || r.SlotID == r.GameID ||
		r.SlotPosition < 1 || r.AttemptNo < 1 || !r.State.IsTerminal() ||
		r.CurrentGameResultRevisionID.IsZero() || !r.Reason.IsLegalFor(r.State) {
		return invalidSeriesScoreRevision("invalid terminal attempt reference")
	}
	if r.State == domain.ArenaGameStateCompleted {
		if r.WinnerID == nil || *r.WinnerID == uuid.Nil {
			return invalidSeriesScoreRevision("completed attempt has no winner")
		}
		return nil
	}
	if r.WinnerID != nil {
		return invalidSeriesScoreRevision("non scoring attempt has a winner")
	}
	return nil
}

type InitialSeriesScoreRevisionCommand struct {
	Scope                    SeriesScoreRevisionScope
	CommandID                uuid.UUID
	RevisionID               domain.ArenaSeriesScoreRevisionID
	Actor                    ArenaResultActor
	ExpectedSourceProjection domain.ArenaDerivedRevision
}

type SeriesScoreRevisionCommand struct {
	Scope                     SeriesScoreRevisionScope
	Operation                 SeriesScoreRevisionOperation
	CommandID                 uuid.UUID
	RevisionID                domain.ArenaSeriesScoreRevisionID
	Actor                     ArenaResultActor
	ExpectedCurrentRevisionID *domain.ArenaSeriesScoreRevisionID
	ExpectedSourceProjection  domain.ArenaDerivedRevision
	Attempt                   *SeriesScoreAttemptReference
}

type SeriesScoreRevisionAuthority struct {
	Scope            SeriesScoreRevisionScope
	PersistedSeries  domain.ArenaSeries
	ProjectedSeries  domain.ArenaSeries
	SourceProjection domain.ArenaDerivedRevision
	CurrentHead      *SeriesScoreRevisionHead
	SeriesRevision   ArenaSeriesRowRevision
	AttemptRevision  ArenaAttemptRowRevision
}

type SeriesScoreRevisionHead struct {
	Scope               SeriesScoreRevisionScope
	ID                  domain.ArenaSeriesScoreRevisionID
	PreviousRevisionID  *domain.ArenaSeriesScoreRevisionID
	Ordinal             int
	Operation           SeriesScoreRevisionOperation
	CommandID           uuid.UUID
	Actor               ArenaResultActor
	CommandAttempt      *SeriesScoreAttemptReference
	FirstParticipantID  uuid.UUID
	SecondParticipantID uuid.UUID
	Format              domain.ArenaSeriesFormat
	Score               domain.ArenaSeriesScore
	Attempts            []SeriesScoreAttemptReference
	SourceProjection    domain.ArenaDerivedRevision
	RecordedAt          time.Time
}

func (h SeriesScoreRevisionHead) Validate() error {
	return seriesScoreRevisionFromHead(h).Validate()
}

func (h SeriesScoreRevisionHead) Clone() SeriesScoreRevisionHead {
	return cloneSeriesScoreRevisionHead(h)
}

type SeriesScoreRevision struct {
	scope               SeriesScoreRevisionScope
	id                  domain.ArenaSeriesScoreRevisionID
	previousRevisionID  *domain.ArenaSeriesScoreRevisionID
	ordinal             int
	operation           SeriesScoreRevisionOperation
	commandID           uuid.UUID
	actor               ArenaResultActor
	commandAttempt      *SeriesScoreAttemptReference
	firstParticipantID  uuid.UUID
	secondParticipantID uuid.UUID
	format              domain.ArenaSeriesFormat
	score               domain.ArenaSeriesScore
	attempts            []SeriesScoreAttemptReference
	sourceProjection    domain.ArenaDerivedRevision
	recordedAt          time.Time
}

func (r SeriesScoreRevision) Validate() error {
	if err := validateSeriesScoreRevisionProvenance(r); err != nil {
		return err
	}
	if err := validateSeriesScoreRevisionPayload(r); err != nil {
		return err
	}
	return validateSeriesScoreRevisionOperation(r)
}

func validateSeriesScoreRevisionProvenance(r SeriesScoreRevision) error {
	if err := r.scope.Validate(); err != nil {
		return err
	}
	if !validSeriesScoreLedgerRevisionIdentity(r) {
		return invalidSeriesScoreRevision("invalid score revision identity or provenance")
	}
	if err := validateSeriesScoreRevisionPredecessor(r); err != nil {
		return err
	}
	if err := validateSeriesScoreSourceProjection(r.sourceProjection, r.scope); err != nil {
		return err
	}
	return validateSeriesScoreRevisionLocalUUIDRoles(r)
}

func validSeriesScoreLedgerRevisionIdentity(r SeriesScoreRevision) bool {
	return !r.id.IsZero() && r.commandID != uuid.Nil && r.ordinal >= 1 && r.actor.Validate() == nil &&
		r.firstParticipantID != uuid.Nil && r.secondParticipantID != uuid.Nil &&
		r.firstParticipantID != r.secondParticipantID && r.format.IsValid() &&
		validArenaServerTime(r.recordedAt) && !r.recordedAt.Before(r.sourceProjection.CreatedAt())
}

func validateSeriesScoreRevisionPredecessor(r SeriesScoreRevision) error {
	if (r.ordinal == 1) != (r.previousRevisionID == nil) {
		return invalidSeriesScoreRevision("invalid score revision predecessor")
	}
	if r.previousRevisionID != nil && (r.previousRevisionID.IsZero() || *r.previousRevisionID == r.id) {
		return invalidSeriesScoreRevision("invalid score revision predecessor identity")
	}
	return nil
}

func validateSeriesScoreRevisionPayload(r SeriesScoreRevision) error {
	if err := validateSeriesScoreAttemptReferences(
		r.attempts,
		r.firstParticipantID,
		r.secondParticipantID,
	); err != nil {
		return err
	}
	calculated, err := scoreFromAttemptReferences(
		r.attempts,
		r.firstParticipantID,
		r.secondParticipantID,
		r.format,
	)
	if err != nil || calculated != r.score {
		return invalidSeriesScoreRevision("score does not match terminal attempts")
	}
	return nil
}

func validateSeriesScoreRevisionOperation(r SeriesScoreRevision) error {
	switch r.operation {
	case SeriesScoreRevisionOperationInitialize:
		if r.ordinal != 1 || r.commandAttempt != nil || len(r.attempts) != 0 ||
			r.score != (domain.ArenaSeriesScore{}) {
			return invalidSeriesScoreRevision("invalid initial score revision")
		}
	case SeriesScoreRevisionOperationAppendAttempt:
		if r.ordinal == 1 || r.commandAttempt == nil || len(r.attempts) == 0 ||
			!seriesScoreAttemptReferenceEqual(*r.commandAttempt, r.attempts[len(r.attempts)-1]) {
			return invalidSeriesScoreRevision("invalid appended attempt evidence")
		}
	case SeriesScoreRevisionOperationReplaceResult:
		if r.ordinal == 1 || r.commandAttempt == nil ||
			!containsSeriesScoreAttempt(r.attempts, *r.commandAttempt) {
			return invalidSeriesScoreRevision("invalid replaced attempt evidence")
		}
	default:
		return invalidSeriesScoreRevision("unknown score revision operation")
	}
	return nil
}

func (r SeriesScoreRevision) Scope() SeriesScoreRevisionScope {
	return r.scope
}

func (r SeriesScoreRevision) ID() domain.ArenaSeriesScoreRevisionID {
	return r.id
}

func (r SeriesScoreRevision) PreviousRevisionID() *domain.ArenaSeriesScoreRevisionID {
	return cloneSeriesScoreRevisionIDPointer(r.previousRevisionID)
}

func (r SeriesScoreRevision) Ordinal() int {
	return r.ordinal
}

func (r SeriesScoreRevision) Operation() SeriesScoreRevisionOperation {
	return r.operation
}

func (r SeriesScoreRevision) CommandID() uuid.UUID {
	return r.commandID
}

func (r SeriesScoreRevision) Actor() ArenaResultActor {
	return cloneArenaResultActor(r.actor)
}

func (r SeriesScoreRevision) CommandAttempt() *SeriesScoreAttemptReference {
	return cloneSeriesScoreAttemptReferencePointer(r.commandAttempt)
}

func (r SeriesScoreRevision) FirstParticipantID() uuid.UUID {
	return r.firstParticipantID
}

func (r SeriesScoreRevision) SecondParticipantID() uuid.UUID {
	return r.secondParticipantID
}

func (r SeriesScoreRevision) Format() domain.ArenaSeriesFormat {
	return r.format
}

func (r SeriesScoreRevision) Score() domain.ArenaSeriesScore {
	return r.score
}

func (r SeriesScoreRevision) Attempts() []SeriesScoreAttemptReference {
	return cloneSeriesScoreAttemptReferences(r.attempts)
}

func (r SeriesScoreRevision) SourceProjection() domain.ArenaDerivedRevision {
	return cloneArenaDerivedRevision(r.sourceProjection)
}

func (r SeriesScoreRevision) RecordedAt() time.Time {
	return r.recordedAt
}

func (r SeriesScoreRevision) Head() SeriesScoreRevisionHead {
	return SeriesScoreRevisionHead{
		Scope:               r.scope,
		ID:                  r.id,
		PreviousRevisionID:  cloneSeriesScoreRevisionIDPointer(r.previousRevisionID),
		Ordinal:             r.ordinal,
		Operation:           r.operation,
		CommandID:           r.commandID,
		Actor:               cloneArenaResultActor(r.actor),
		CommandAttempt:      cloneSeriesScoreAttemptReferencePointer(r.commandAttempt),
		FirstParticipantID:  r.firstParticipantID,
		SecondParticipantID: r.secondParticipantID,
		Format:              r.format,
		Score:               r.score,
		Attempts:            cloneSeriesScoreAttemptReferences(r.attempts),
		SourceProjection:    cloneArenaDerivedRevision(r.sourceProjection),
		RecordedAt:          r.recordedAt,
	}
}

type SeriesScoreRevisionCondition struct {
	scope                    SeriesScoreRevisionScope
	expectedSeries           domain.ArenaSeries
	expectedCurrentHead      *SeriesScoreRevisionHead
	expectedSourceProjection domain.ArenaDerivedRevision
	expectedSeriesRevision   ArenaSeriesRowRevision
	expectedAttemptRevision  ArenaAttemptRowRevision
	plannedRevisionID        domain.ArenaSeriesScoreRevisionID
	plannedOperation         SeriesScoreRevisionOperation
}

func (c SeriesScoreRevisionCondition) Validate() error {
	if err := c.scope.Validate(); err != nil {
		return err
	}
	if err := validateSeriesScoreConditionSeries(c); err != nil {
		return err
	}
	if err := validateSeriesScoreSourceProjection(c.expectedSourceProjection, c.scope); err != nil {
		return err
	}
	if err := validateSeriesScoreConditionRows(c); err != nil {
		return err
	}
	return validateSeriesScoreConditionHead(c)
}

func validateSeriesScoreConditionSeries(c SeriesScoreRevisionCondition) error {
	if err := c.expectedSeries.Validate(); err != nil {
		return invalidSeriesScoreRevision("invalid expected Series")
	}
	if c.expectedSeries.ID != c.scope.SeriesID || c.expectedSeries.TournamentID != c.scope.TournamentID {
		return invalidSeriesScoreRevision("expected Series scope mismatch")
	}
	return nil
}

func validateSeriesScoreConditionRows(c SeriesScoreRevisionCondition) error {
	if c.expectedSeriesRevision <= 0 || c.plannedRevisionID.IsZero() {
		return invalidSeriesScoreRevision("invalid expected Series row revision")
	}
	if c.plannedOperation == SeriesScoreRevisionOperationInitialize {
		if c.expectedAttemptRevision != 0 || c.expectedCurrentHead != nil {
			return invalidSeriesScoreRevision("invalid initial row revision condition")
		}
	} else if c.expectedAttemptRevision <= 0 || c.expectedCurrentHead == nil {
		return invalidSeriesScoreRevision("invalid score attempt row revision")
	}
	return nil
}

func validateSeriesScoreConditionHead(c SeriesScoreRevisionCondition) error {
	if c.expectedCurrentHead != nil {
		if err := c.expectedCurrentHead.Validate(); err != nil ||
			c.expectedCurrentHead.Scope != c.scope {
			return invalidSeriesScoreRevision("invalid expected current score revision")
		}
	}
	if !seriesScoreRevisionIDPointersEqual(
		c.expectedSeries.CurrentScoreRevisionID,
		seriesScoreCurrentHeadID(c.expectedCurrentHead),
	) {
		return invalidSeriesScoreRevision("expected score head mismatch")
	}
	return nil
}

func (c SeriesScoreRevisionCondition) Scope() SeriesScoreRevisionScope {
	return c.scope
}

func (c SeriesScoreRevisionCondition) ExpectedSeries() domain.ArenaSeries {
	return cloneRevisionArenaSeries(c.expectedSeries)
}

func (c SeriesScoreRevisionCondition) ExpectedCurrentHead() *SeriesScoreRevisionHead {
	return cloneSeriesScoreRevisionHeadPointer(c.expectedCurrentHead)
}

func (c SeriesScoreRevisionCondition) ExpectedCurrentRevisionID() *domain.ArenaSeriesScoreRevisionID {
	return seriesScoreCurrentHeadID(c.expectedCurrentHead)
}

func (c SeriesScoreRevisionCondition) ExpectedCurrentOrdinal() int {
	if c.expectedCurrentHead == nil {
		return 0
	}
	return c.expectedCurrentHead.Ordinal
}

func (c SeriesScoreRevisionCondition) ExpectedSourceProjection() domain.ArenaDerivedRevision {
	return cloneArenaDerivedRevision(c.expectedSourceProjection)
}

func (c SeriesScoreRevisionCondition) ExpectedSeriesRevision() ArenaSeriesRowRevision {
	return c.expectedSeriesRevision
}

func (c SeriesScoreRevisionCondition) ExpectedAttemptRevision() ArenaAttemptRowRevision {
	return c.expectedAttemptRevision
}

type SeriesScoreRevisionPlan struct {
	condition SeriesScoreRevisionCondition
	revision  SeriesScoreRevision
}

func (p SeriesScoreRevisionPlan) Condition() SeriesScoreRevisionCondition {
	return cloneSeriesScoreRevisionCondition(p.condition)
}

func (p SeriesScoreRevisionPlan) Revision() SeriesScoreRevision {
	return cloneSeriesScoreRevision(p.revision)
}

func (p SeriesScoreRevisionPlan) Validate() error {
	if err := p.condition.Validate(); err != nil {
		return err
	}
	if err := p.revision.Validate(); err != nil {
		return err
	}
	if err := validateSeriesScorePlanLink(p); err != nil {
		return err
	}
	return validateSeriesScorePlanLineage(p)
}

func validateSeriesScorePlanLink(p SeriesScoreRevisionPlan) error {
	if p.condition.scope != p.revision.scope ||
		p.condition.plannedRevisionID != p.revision.id ||
		p.condition.plannedOperation != p.revision.operation ||
		!arenaDerivedRevisionsEqual(p.condition.expectedSourceProjection, p.revision.sourceProjection) {
		return invalidSeriesScoreRevision("spliced score revision plan")
	}
	return nil
}

func validateSeriesScorePlanLineage(p SeriesScoreRevisionPlan) error {
	if p.condition.expectedCurrentHead == nil {
		if p.revision.ordinal != 1 || p.revision.previousRevisionID != nil ||
			p.revision.operation != SeriesScoreRevisionOperationInitialize {
			return invalidSeriesScoreRevision("invalid initial score revision plan")
		}
		return nil
	}
	current := p.condition.expectedCurrentHead
	if current.Ordinal == math.MaxInt || p.revision.ordinal != current.Ordinal+1 ||
		p.revision.previousRevisionID == nil || *p.revision.previousRevisionID != current.ID ||
		!arenaDerivedRevisionDirectSuccessor(current.SourceProjection, p.revision.sourceProjection) {
		return invalidSeriesScoreRevision("invalid successor score revision plan")
	}
	return nil
}

func PlanInitialSeriesScoreRevision(
	command InitialSeriesScoreRevisionCommand,
	authority SeriesScoreRevisionAuthority,
	recordedAt time.Time,
) (SeriesScoreRevisionPlan, error) {
	command = cloneInitialSeriesScoreRevisionCommand(command)
	authority = cloneSeriesScoreRevisionAuthority(authority)
	if err := validateInitialSeriesScoreRevisionCommand(command, recordedAt); err != nil {
		return SeriesScoreRevisionPlan{}, err
	}
	if err := validateSeriesScoreRevisionAuthority(authority); err != nil {
		return SeriesScoreRevisionPlan{}, err
	}
	if err := validateInitialScorePlannerUUIDRoles(command, authority); err != nil {
		return SeriesScoreRevisionPlan{}, err
	}
	if command.Scope != authority.Scope ||
		!arenaDerivedRevisionsEqual(command.ExpectedSourceProjection, authority.SourceProjection) {
		return SeriesScoreRevisionPlan{}, seriesScoreRevisionConflict("stale score scope or source")
	}
	if err := validateInitialSeriesScoreProjection(command, authority); err != nil {
		return SeriesScoreRevisionPlan{}, err
	}
	revision := newSeriesScoreRevision(
		command.Scope,
		command.RevisionID,
		nil,
		1,
		SeriesScoreRevisionOperationInitialize,
		command.CommandID,
		command.Actor,
		nil,
		authority.ProjectedSeries,
		nil,
		command.ExpectedSourceProjection,
		recordedAt,
	)
	condition := newSeriesScoreRevisionCondition(authority)
	condition.plannedRevisionID = command.RevisionID
	condition.plannedOperation = SeriesScoreRevisionOperationInitialize
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
	if authority.CurrentHead == nil || authority.PersistedSeries.State == domain.ArenaSeriesStatePlanned ||
		authority.ProjectedSeries.State == domain.ArenaSeriesStatePlanned {
		return seriesScoreRevisionConflict("Series has no current score plan")
	}
	return validateSeriesScorePlanCAS(command, authority)
}

func validateSeriesScorePlanCAS(
	command SeriesScoreRevisionCommand,
	authority SeriesScoreRevisionAuthority,
) error {
	if command.Scope != authority.Scope ||
		!arenaDerivedRevisionsEqual(command.ExpectedSourceProjection, authority.SourceProjection) {
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
		(command.Actor.Kind != ArenaResultActorOperator || command.Actor.PrincipalID == nil) {
		return invalidSeriesScoreRevision("score replacement requires operator")
	}
	if !arenaDerivedRevisionDirectSuccessor(
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

func validateInitialSeriesScoreRevisionCommand(
	command InitialSeriesScoreRevisionCommand,
	recordedAt time.Time,
) error {
	if err := command.Scope.Validate(); err != nil {
		return err
	}
	if command.CommandID == uuid.Nil || command.RevisionID.IsZero() || command.Actor.Validate() != nil ||
		!validArenaServerTime(recordedAt) || recordedAt.Before(command.ExpectedSourceProjection.CreatedAt()) {
		return invalidSeriesScoreRevision("invalid initial score command provenance")
	}
	if err := validateSeriesScoreSourceProjection(command.ExpectedSourceProjection, command.Scope); err != nil {
		return err
	}
	return validateInitialScoreCommandUUIDRoles(command)
}

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
		!validArenaServerTime(recordedAt) || recordedAt.Before(command.ExpectedSourceProjection.CreatedAt()) {
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
	if err := validateArenaSeriesGameIDsUnique(
		authority.PersistedSeries,
		invalidSeriesScoreRevision,
	); err != nil {
		return err
	}
	if err := validateArenaSeriesGameIDsUnique(
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

func validateInitialSeriesScoreProjection(
	command InitialSeriesScoreRevisionCommand,
	authority SeriesScoreRevisionAuthority,
) error {
	if authority.CurrentHead != nil || authority.PersistedSeries.CurrentScoreRevisionID != nil ||
		authority.PersistedSeries.State != domain.ArenaSeriesStatePlanned ||
		authority.ProjectedSeries.State != domain.ArenaSeriesStateLocked ||
		authority.ProjectedSeries.CurrentScoreRevisionID == nil ||
		*authority.ProjectedSeries.CurrentScoreRevisionID != command.RevisionID ||
		authority.PersistedSeries.Score != (domain.ArenaSeriesScore{}) ||
		authority.ProjectedSeries.Score != (domain.ArenaSeriesScore{}) {
		return seriesScoreRevisionConflict("Series is not an uninitialized planned to locked projection")
	}
	persistedAttempts, err := seriesScoreAttemptReferencesFromSeries(authority.PersistedSeries)
	if err != nil || len(persistedAttempts) != 0 {
		return seriesScoreRevisionConflict("planned Series has terminal attempts")
	}
	projectedAttempts, err := seriesScoreAttemptReferencesFromSeries(authority.ProjectedSeries)
	if err != nil || len(projectedAttempts) != 0 {
		return seriesScoreRevisionConflict("initial score projection has terminal attempts")
	}
	normalized := cloneRevisionArenaSeries(authority.ProjectedSeries)
	normalized.State = domain.ArenaSeriesStatePlanned
	normalized.CurrentScoreRevisionID = nil
	if !officialArenaSeriesEqual(authority.PersistedSeries, normalized) {
		return seriesScoreRevisionConflict("initial score projection changed unrelated Series state")
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
	persisted domain.ArenaGameSlot,
	projected domain.ArenaGameSlot,
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

func scoreSlotStructureEqual(first, second domain.ArenaGameSlot) bool {
	return first.ID == second.ID && first.SeriesID == second.SeriesID &&
		first.Position == second.Position && first.Category == second.Category &&
		first.ScoreBefore == second.ScoreBefore && len(first.Attempts) == len(second.Attempts)
}

func validateScoreGameTransition(
	command SeriesScoreRevisionCommand,
	slot domain.ArenaGameSlot,
	persisted domain.ArenaGame,
	projected domain.ArenaGame,
) (bool, error) {
	if !officialArenaGameIdentityEqual(persisted, projected) {
		return false, seriesScoreRevisionConflict("score projection changed attempt identity")
	}
	if persisted.ID != command.Attempt.GameID {
		if !officialArenaGameEqual(persisted, projected) {
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
	persisted domain.ArenaGame,
	projected domain.ArenaGame,
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

func newSeriesScoreRevision(
	scope SeriesScoreRevisionScope,
	id domain.ArenaSeriesScoreRevisionID,
	previousRevisionID *domain.ArenaSeriesScoreRevisionID,
	ordinal int,
	operation SeriesScoreRevisionOperation,
	commandID uuid.UUID,
	actor ArenaResultActor,
	commandAttempt *SeriesScoreAttemptReference,
	series domain.ArenaSeries,
	attempts []SeriesScoreAttemptReference,
	sourceProjection domain.ArenaDerivedRevision,
	recordedAt time.Time,
) SeriesScoreRevision {
	return SeriesScoreRevision{
		scope:               scope,
		id:                  id,
		previousRevisionID:  cloneSeriesScoreRevisionIDPointer(previousRevisionID),
		ordinal:             ordinal,
		operation:           operation,
		commandID:           commandID,
		actor:               cloneArenaResultActor(actor),
		commandAttempt:      cloneSeriesScoreAttemptReferencePointer(commandAttempt),
		firstParticipantID:  series.FirstParticipantID,
		secondParticipantID: series.SecondParticipantID,
		format:              series.Format,
		score:               series.Score,
		attempts:            cloneSeriesScoreAttemptReferences(attempts),
		sourceProjection:    cloneArenaDerivedRevision(sourceProjection),
		recordedAt:          recordedAt,
	}
}

func newSeriesScoreRevisionCondition(
	authority SeriesScoreRevisionAuthority,
) SeriesScoreRevisionCondition {
	return SeriesScoreRevisionCondition{
		scope:                    authority.Scope,
		expectedSeries:           cloneRevisionArenaSeries(authority.PersistedSeries),
		expectedCurrentHead:      cloneSeriesScoreRevisionHeadPointer(authority.CurrentHead),
		expectedSourceProjection: cloneArenaDerivedRevision(authority.SourceProjection),
		expectedSeriesRevision:   authority.SeriesRevision,
		expectedAttemptRevision:  authority.AttemptRevision,
	}
}

func seriesScoreAttemptReferencesFromSeries(
	series domain.ArenaSeries,
) ([]SeriesScoreAttemptReference, error) {
	slots := append([]domain.ArenaGameSlot(nil), series.Slots...)
	sort.Slice(slots, func(first, second int) bool {
		return slots[first].Position < slots[second].Position
	})
	references := make([]SeriesScoreAttemptReference, 0)
	for _, slot := range slots {
		for _, game := range slot.Attempts {
			if !game.State.IsTerminal() {
				continue
			}
			if game.ResultRevisionID == nil || game.ResultRevisionID.IsZero() {
				return nil, invalidSeriesScoreRevision("terminal Game has no official result revision")
			}
			references = append(references, SeriesScoreAttemptReference{
				SlotID:                      slot.ID,
				SlotPosition:                slot.Position,
				GameID:                      game.ID,
				AttemptNo:                   game.AttemptNo,
				State:                       game.State,
				WinnerID:                    cloneUUIDPointer(game.WinnerID),
				Reason:                      game.ResultReason,
				CurrentGameResultRevisionID: *game.ResultRevisionID,
			})
		}
	}
	if err := validateSeriesScoreAttemptReferences(
		references,
		series.FirstParticipantID,
		series.SecondParticipantID,
	); err != nil {
		return nil, err
	}
	return references, nil
}

func validateSeriesScoreAttemptReferences(
	references []SeriesScoreAttemptReference,
	firstParticipantID uuid.UUID,
	secondParticipantID uuid.UUID,
) error {
	games := make(map[uuid.UUID]struct{}, len(references))
	results := make(map[domain.ArenaOfficialResultRevisionID]struct{}, len(references))
	for index := range references {
		reference := references[index]
		if err := reference.Validate(); err != nil {
			return err
		}
		if reference.WinnerID != nil && *reference.WinnerID != firstParticipantID &&
			*reference.WinnerID != secondParticipantID {
			return invalidSeriesScoreRevision("attempt winner is not a participant")
		}
		if _, exists := games[reference.GameID]; exists {
			return invalidSeriesScoreRevision("duplicate Game attempt reference")
		}
		if _, exists := results[reference.CurrentGameResultRevisionID]; exists {
			return invalidSeriesScoreRevision("duplicate official result reference")
		}
		if index > 0 && !seriesScoreAttemptFollows(references[index-1], reference) {
			return invalidSeriesScoreRevision("terminal attempt references are not ordered")
		}
		games[reference.GameID] = struct{}{}
		results[reference.CurrentGameResultRevisionID] = struct{}{}
	}
	return nil
}

func seriesScoreAttemptFollows(
	previous SeriesScoreAttemptReference,
	next SeriesScoreAttemptReference,
) bool {
	if next.SlotPosition > previous.SlotPosition {
		return true
	}
	return next.SlotPosition == previous.SlotPosition && next.SlotID == previous.SlotID &&
		next.AttemptNo == previous.AttemptNo+1
}

func scoreFromAttemptReferences(
	references []SeriesScoreAttemptReference,
	firstParticipantID uuid.UUID,
	secondParticipantID uuid.UUID,
	format domain.ArenaSeriesFormat,
) (domain.ArenaSeriesScore, error) {
	score := domain.ArenaSeriesScore{}
	for _, reference := range references {
		if reference.State != domain.ArenaGameStateCompleted || reference.WinnerID == nil {
			continue
		}
		switch *reference.WinnerID {
		case firstParticipantID:
			score.FirstParticipantWins++
		case secondParticipantID:
			score.SecondParticipantWins++
		default:
			return domain.ArenaSeriesScore{}, invalidSeriesScoreRevision("attempt winner is not a participant")
		}
	}
	if err := score.Validate(format); err != nil {
		return domain.ArenaSeriesScore{}, invalidSeriesScoreRevision("terminal attempts produce an invalid score")
	}
	return score, nil
}

func sameSeriesScoreAttemptPosition(
	first SeriesScoreAttemptReference,
	second SeriesScoreAttemptReference,
) bool {
	return first.SlotID == second.SlotID && first.SlotPosition == second.SlotPosition &&
		first.GameID == second.GameID && first.AttemptNo == second.AttemptNo
}

func containsSeriesScoreAttempt(
	references []SeriesScoreAttemptReference,
	want SeriesScoreAttemptReference,
) bool {
	for _, reference := range references {
		if seriesScoreAttemptReferenceEqual(reference, want) {
			return true
		}
	}
	return false
}

func validateSeriesScoreSourceProjection(
	source domain.ArenaDerivedRevision,
	scope SeriesScoreRevisionScope,
) error {
	if err := source.Validate(); err != nil || source.TournamentID() != scope.TournamentID ||
		source.Artifact() != (domain.ArenaArtifactRef{
			Kind:     domain.ArenaArtifactKindSeriesScore,
			EntityID: scope.SeriesID,
		}) {
		return invalidSeriesScoreRevision("invalid score source projection")
	}
	return nil
}

func validateInitialScoreCommandUUIDRoles(command InitialSeriesScoreRevisionCommand) error {
	roles := newArenaPlannerUUIDRegistry(invalidSeriesScoreRevision)
	values := []struct {
		id   uuid.UUID
		role arenaPlannerUUIDRole
	}{
		{command.Scope.TournamentID, arenaPlannerRoleTournament},
		{command.Scope.SeriesID, arenaPlannerRoleSeries},
		{command.CommandID, arenaPlannerRoleCommand},
		{command.RevisionID.UUID(), arenaPlannerRoleScore},
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

func validateScoreCommandUUIDRoles(command SeriesScoreRevisionCommand) error {
	roles := newArenaPlannerUUIDRegistry(invalidSeriesScoreRevision)
	values := []struct {
		id   uuid.UUID
		role arenaPlannerUUIDRole
	}{
		{command.Scope.TournamentID, arenaPlannerRoleTournament},
		{command.Scope.SeriesID, arenaPlannerRoleSeries},
		{command.CommandID, arenaPlannerRoleCommand},
		{command.RevisionID.UUID(), arenaPlannerRoleScore},
		{command.ExpectedCurrentRevisionID.UUID(), arenaPlannerRoleScore},
		{command.Attempt.SlotID, arenaPlannerRoleSlot},
		{command.Attempt.GameID, arenaPlannerRoleGame},
		{command.Attempt.CurrentGameResultRevisionID.UUID(), arenaPlannerRoleOfficial},
	}
	if command.RevisionID == *command.ExpectedCurrentRevisionID {
		return invalidSeriesScoreRevision("score revision did not advance")
	}
	if command.Attempt.WinnerID != nil {
		values = append(values, struct {
			id   uuid.UUID
			role arenaPlannerUUIDRole
		}{*command.Attempt.WinnerID, arenaPlannerRoleParticipant})
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
	roles := newArenaPlannerUUIDRegistry(invalidSeriesScoreRevision)
	values := []struct {
		id   uuid.UUID
		role arenaPlannerUUIDRole
	}{
		{revision.scope.TournamentID, arenaPlannerRoleTournament},
		{revision.scope.SeriesID, arenaPlannerRoleSeries},
		{revision.commandID, arenaPlannerRoleCommand},
		{revision.id.UUID(), arenaPlannerRoleScore},
		{revision.firstParticipantID, arenaPlannerRoleParticipant},
		{revision.secondParticipantID, arenaPlannerRoleParticipant},
	}
	if revision.previousRevisionID != nil {
		values = append(values, struct {
			id   uuid.UUID
			role arenaPlannerUUIDRole
		}{revision.previousRevisionID.UUID(), arenaPlannerRoleScore})
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
	if err := roles.addSource(revision.sourceProjection); err != nil {
		return err
	}
	return roles.addActor(revision.actor)
}

func validateInitialScorePlannerUUIDRoles(
	command InitialSeriesScoreRevisionCommand,
	authority SeriesScoreRevisionAuthority,
) error {
	return validateScorePlannerSeriesUUIDRoles(
		authority,
		command.CommandID,
		command.RevisionID,
		command.Actor,
		command.ExpectedSourceProjection,
		nil,
	)
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
	revisionID domain.ArenaSeriesScoreRevisionID,
	actor ArenaResultActor,
	source domain.ArenaDerivedRevision,
	commandAttempt *SeriesScoreAttemptReference,
) error {
	roles := newArenaPlannerUUIDRegistry(invalidSeriesScoreRevision)
	for _, value := range []struct {
		id   uuid.UUID
		role arenaPlannerUUIDRole
	}{
		{commandID, arenaPlannerRoleCommand},
		{revisionID.UUID(), arenaPlannerRoleScore},
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
	for _, series := range []domain.ArenaSeries{authority.PersistedSeries, authority.ProjectedSeries} {
		if err := roles.addSeries(series); err != nil {
			return err
		}
	}
	return nil
}

func addScoreHeadUUIDRoles(
	roles *arenaPlannerUUIDRegistry,
	head SeriesScoreRevisionHead,
) error {
	for _, value := range []struct {
		id   uuid.UUID
		role arenaPlannerUUIDRole
	}{
		{head.ID.UUID(), arenaPlannerRoleScore},
		{head.CommandID, arenaPlannerRoleCommand},
		{head.FirstParticipantID, arenaPlannerRoleParticipant},
		{head.SecondParticipantID, arenaPlannerRoleParticipant},
	} {
		if err := roles.add(value.id, value.role); err != nil {
			return err
		}
	}
	if head.PreviousRevisionID != nil {
		if err := roles.add(head.PreviousRevisionID.UUID(), arenaPlannerRoleScore); err != nil {
			return err
		}
	}
	for _, reference := range head.Attempts {
		if err := roles.addAttempt(reference); err != nil {
			return err
		}
	}
	if err := roles.addSource(head.SourceProjection); err != nil {
		return err
	}
	return roles.addActor(head.Actor)
}

func seriesScoreCurrentHeadID(
	head *SeriesScoreRevisionHead,
) *domain.ArenaSeriesScoreRevisionID {
	if head == nil {
		return nil
	}
	id := head.ID
	return &id
}

func seriesScoreRevisionIDPointersEqual(
	first *domain.ArenaSeriesScoreRevisionID,
	second *domain.ArenaSeriesScoreRevisionID,
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

func cloneSeriesScoreRevision(revision SeriesScoreRevision) SeriesScoreRevision {
	clone := revision
	clone.previousRevisionID = cloneSeriesScoreRevisionIDPointer(revision.previousRevisionID)
	clone.actor = cloneArenaResultActor(revision.actor)
	clone.commandAttempt = cloneSeriesScoreAttemptReferencePointer(revision.commandAttempt)
	clone.attempts = cloneSeriesScoreAttemptReferences(revision.attempts)
	clone.sourceProjection = cloneArenaDerivedRevision(revision.sourceProjection)
	return clone
}

func seriesScoreRevisionFromHead(head SeriesScoreRevisionHead) SeriesScoreRevision {
	return SeriesScoreRevision{
		scope:               head.Scope,
		id:                  head.ID,
		previousRevisionID:  cloneSeriesScoreRevisionIDPointer(head.PreviousRevisionID),
		ordinal:             head.Ordinal,
		operation:           head.Operation,
		commandID:           head.CommandID,
		actor:               cloneArenaResultActor(head.Actor),
		commandAttempt:      cloneSeriesScoreAttemptReferencePointer(head.CommandAttempt),
		firstParticipantID:  head.FirstParticipantID,
		secondParticipantID: head.SecondParticipantID,
		format:              head.Format,
		score:               head.Score,
		attempts:            cloneSeriesScoreAttemptReferences(head.Attempts),
		sourceProjection:    cloneArenaDerivedRevision(head.SourceProjection),
		recordedAt:          head.RecordedAt,
	}
}

func cloneSeriesScoreRevisionHead(head SeriesScoreRevisionHead) SeriesScoreRevisionHead {
	clone := head
	clone.PreviousRevisionID = cloneSeriesScoreRevisionIDPointer(head.PreviousRevisionID)
	clone.Actor = cloneArenaResultActor(head.Actor)
	clone.CommandAttempt = cloneSeriesScoreAttemptReferencePointer(head.CommandAttempt)
	clone.Attempts = cloneSeriesScoreAttemptReferences(head.Attempts)
	clone.SourceProjection = cloneArenaDerivedRevision(head.SourceProjection)
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
	clone.expectedSeries = cloneRevisionArenaSeries(condition.expectedSeries)
	clone.expectedCurrentHead = cloneSeriesScoreRevisionHeadPointer(condition.expectedCurrentHead)
	clone.expectedSourceProjection = cloneArenaDerivedRevision(condition.expectedSourceProjection)
	return clone
}

func cloneInitialSeriesScoreRevisionCommand(
	command InitialSeriesScoreRevisionCommand,
) InitialSeriesScoreRevisionCommand {
	clone := command
	clone.Actor = cloneArenaResultActor(command.Actor)
	clone.ExpectedSourceProjection = cloneArenaDerivedRevision(command.ExpectedSourceProjection)
	return clone
}

func cloneSeriesScoreRevisionCommand(
	command SeriesScoreRevisionCommand,
) SeriesScoreRevisionCommand {
	clone := command
	clone.Actor = cloneArenaResultActor(command.Actor)
	clone.ExpectedCurrentRevisionID = cloneSeriesScoreRevisionIDPointer(
		command.ExpectedCurrentRevisionID,
	)
	clone.ExpectedSourceProjection = cloneArenaDerivedRevision(command.ExpectedSourceProjection)
	clone.Attempt = cloneSeriesScoreAttemptReferencePointer(command.Attempt)
	return clone
}

func cloneSeriesScoreRevisionAuthority(
	authority SeriesScoreRevisionAuthority,
) SeriesScoreRevisionAuthority {
	clone := authority
	clone.PersistedSeries = cloneRevisionArenaSeries(authority.PersistedSeries)
	clone.ProjectedSeries = cloneRevisionArenaSeries(authority.ProjectedSeries)
	clone.SourceProjection = cloneArenaDerivedRevision(authority.SourceProjection)
	clone.CurrentHead = cloneSeriesScoreRevisionHeadPointer(authority.CurrentHead)
	return clone
}

func invalidSeriesScoreRevision(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidSeriesScoreRevision, message)
}

func seriesScoreRevisionConflict(message string) error {
	return fmt.Errorf("%w: %s", ErrSeriesScoreRevisionConflict, message)
}
