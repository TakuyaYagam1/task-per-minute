package arena

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var (
	ErrInvalidArenaResultActor        = errors.New("invalid Arena result actor")
	ErrInvalidOfficialResultRevision  = errors.New("invalid official Arena result revision")
	ErrOfficialResultRevisionConflict = errors.New("official Arena result revision conflict")
)

type ArenaResultActorKind string

const (
	ArenaResultActorServer   ArenaResultActorKind = "server"
	ArenaResultActorOperator ArenaResultActorKind = "operator"
)

type ArenaResultActor struct {
	Kind        ArenaResultActorKind
	PrincipalID *uuid.UUID
}

type ArenaSeriesRowRevision int64

type ArenaAttemptRowRevision int64

func (a ArenaResultActor) Validate() error {
	switch a.Kind {
	case ArenaResultActorServer:
		if a.PrincipalID != nil {
			return fmt.Errorf("%w: server actor has a principal", ErrInvalidArenaResultActor)
		}
	case ArenaResultActorOperator:
		if a.PrincipalID == nil || *a.PrincipalID == uuid.Nil {
			return fmt.Errorf("%w: operator actor has no principal", ErrInvalidArenaResultActor)
		}
	default:
		return fmt.Errorf("%w: unknown result actor", ErrInvalidArenaResultActor)
	}
	return nil
}

type ArenaSeriesResultReason string

const (
	ArenaSeriesResultReasonScoreComplete       ArenaSeriesResultReason = "score_complete"
	ArenaSeriesResultReasonOperatorCorrection  ArenaSeriesResultReason = "operator_correction"
	ArenaSeriesResultReasonSeriesCancelled     ArenaSeriesResultReason = "series_cancelled"
	ArenaSeriesResultReasonTournamentCancelled ArenaSeriesResultReason = "tournament_cancelled"
)

func (r ArenaSeriesResultReason) IsLegalFor(state domain.ArenaSeriesState) bool {
	switch state {
	case domain.ArenaSeriesStateCompleted:
		return r == ArenaSeriesResultReasonScoreComplete ||
			r == ArenaSeriesResultReasonOperatorCorrection
	case domain.ArenaSeriesStateCancelled:
		return r == ArenaSeriesResultReasonSeriesCancelled ||
			r == ArenaSeriesResultReasonTournamentCancelled ||
			r == ArenaSeriesResultReasonOperatorCorrection
	case domain.ArenaSeriesStatePlanned,
		domain.ArenaSeriesStateLocked,
		domain.ArenaSeriesStateDraft,
		domain.ArenaSeriesStateReady,
		domain.ArenaSeriesStateActive,
		domain.ArenaSeriesStateReplayRequired,
		domain.ArenaSeriesStateTechnicalPause:
		return false
	default:
		return false
	}
}

type OfficialResultSubjectKind string

const (
	OfficialResultSubjectGame   OfficialResultSubjectKind = "game"
	OfficialResultSubjectSeries OfficialResultSubjectKind = "series"
)

type OfficialResultScope struct {
	TournamentID uuid.UUID
	SeriesID     uuid.UUID
	GameID       uuid.UUID
	Kind         OfficialResultSubjectKind
}

func (s OfficialResultScope) Validate() error {
	if s.TournamentID == uuid.Nil || s.SeriesID == uuid.Nil || s.TournamentID == s.SeriesID {
		return invalidOfficialResultRevision("invalid result scope")
	}
	switch s.Kind {
	case OfficialResultSubjectGame:
		if s.GameID == uuid.Nil || s.GameID == s.TournamentID || s.GameID == s.SeriesID {
			return invalidOfficialResultRevision("invalid Game result scope")
		}
	case OfficialResultSubjectSeries:
		if s.GameID != uuid.Nil {
			return invalidOfficialResultRevision("Series result has a Game identity")
		}
	default:
		return invalidOfficialResultRevision("unknown result subject")
	}
	return nil
}

type OfficialResultOutcome struct {
	GameState       domain.ArenaGameState
	GameReason      domain.ArenaGameResultReason
	SeriesState     domain.ArenaSeriesState
	SeriesReason    ArenaSeriesResultReason
	WinnerID        *uuid.UUID
	ScoreRevisionID *domain.ArenaSeriesScoreRevisionID
}

func (o OfficialResultOutcome) Validate(kind OfficialResultSubjectKind) error {
	switch kind {
	case OfficialResultSubjectGame:
		return validateOfficialGameOutcome(o)
	case OfficialResultSubjectSeries:
		return validateOfficialSeriesOutcome(o)
	default:
		return invalidOfficialResultRevision("unknown result outcome subject")
	}
}

type OfficialResultRevisionCommand struct {
	Scope                     OfficialResultScope
	CommandID                 uuid.UUID
	RevisionID                domain.ArenaOfficialResultRevisionID
	Actor                     ArenaResultActor
	ExpectedCurrentRevisionID *domain.ArenaOfficialResultRevisionID
	ExpectedSourceProjection  domain.ArenaDerivedRevision
	Outcome                   OfficialResultOutcome
}

type OfficialResultRevisionAuthority struct {
	Scope                 OfficialResultScope
	PersistedSeries       domain.ArenaSeries
	ProjectedSeries       domain.ArenaSeries
	ProjectedSeriesReason ArenaSeriesResultReason
	SourceProjection      domain.ArenaDerivedRevision
	CurrentHead           *OfficialResultRevisionHead
	SeriesRevision        ArenaSeriesRowRevision
	AttemptRevision       ArenaAttemptRowRevision
}

type OfficialResultRevisionHead struct {
	Scope              OfficialResultScope
	ID                 domain.ArenaOfficialResultRevisionID
	PreviousRevisionID *domain.ArenaOfficialResultRevisionID
	Ordinal            int
	CommandID          uuid.UUID
	Actor              ArenaResultActor
	Outcome            OfficialResultOutcome
	SourceProjection   domain.ArenaDerivedRevision
	RecordedAt         time.Time
}

func (h OfficialResultRevisionHead) Validate() error {
	return officialResultRevisionFromHead(h).Validate()
}

func (h OfficialResultRevisionHead) Clone() OfficialResultRevisionHead {
	return cloneOfficialResultRevisionHead(h)
}

type OfficialResultRevision struct {
	scope              OfficialResultScope
	id                 domain.ArenaOfficialResultRevisionID
	previousRevisionID *domain.ArenaOfficialResultRevisionID
	ordinal            int
	commandID          uuid.UUID
	actor              ArenaResultActor
	outcome            OfficialResultOutcome
	sourceProjection   domain.ArenaDerivedRevision
	recordedAt         time.Time
}

func (r OfficialResultRevision) Validate() error {
	if err := r.scope.Validate(); err != nil {
		return err
	}
	if r.id.IsZero() || r.commandID == uuid.Nil || r.ordinal < 1 || r.actor.Validate() != nil ||
		!validArenaServerTime(r.recordedAt) || r.recordedAt.Before(r.sourceProjection.CreatedAt()) {
		return invalidOfficialResultRevision("invalid revision identity or provenance")
	}
	if (r.ordinal == 1) != (r.previousRevisionID == nil) {
		return invalidOfficialResultRevision("invalid revision predecessor")
	}
	if r.previousRevisionID != nil && (r.previousRevisionID.IsZero() || *r.previousRevisionID == r.id) {
		return invalidOfficialResultRevision("invalid revision predecessor identity")
	}
	if err := validateOfficialSourceProjection(r.sourceProjection, r.scope); err != nil {
		return err
	}
	if err := r.outcome.Validate(r.scope.Kind); err != nil {
		return err
	}
	return validateOfficialLocalUUIDRoles(OfficialResultRevisionCommand{
		Scope:                     r.scope,
		CommandID:                 r.commandID,
		RevisionID:                r.id,
		Actor:                     r.actor,
		ExpectedCurrentRevisionID: r.previousRevisionID,
		ExpectedSourceProjection:  r.sourceProjection,
		Outcome:                   r.outcome,
	})
}

func (r OfficialResultRevision) Scope() OfficialResultScope {
	return r.scope
}

func (r OfficialResultRevision) ID() domain.ArenaOfficialResultRevisionID {
	return r.id
}

func (r OfficialResultRevision) PreviousRevisionID() *domain.ArenaOfficialResultRevisionID {
	return cloneOfficialResultRevisionIDPointer(r.previousRevisionID)
}

func (r OfficialResultRevision) Ordinal() int {
	return r.ordinal
}

func (r OfficialResultRevision) CommandID() uuid.UUID {
	return r.commandID
}

func (r OfficialResultRevision) Actor() ArenaResultActor {
	return cloneArenaResultActor(r.actor)
}

func (r OfficialResultRevision) Outcome() OfficialResultOutcome {
	return cloneOfficialResultOutcome(r.outcome)
}

func (r OfficialResultRevision) SourceProjection() domain.ArenaDerivedRevision {
	return cloneArenaDerivedRevision(r.sourceProjection)
}

func (r OfficialResultRevision) RecordedAt() time.Time {
	return r.recordedAt
}

func (r OfficialResultRevision) Head() OfficialResultRevisionHead {
	return OfficialResultRevisionHead{
		Scope:              r.scope,
		ID:                 r.id,
		PreviousRevisionID: cloneOfficialResultRevisionIDPointer(r.previousRevisionID),
		Ordinal:            r.ordinal,
		CommandID:          r.commandID,
		Actor:              cloneArenaResultActor(r.actor),
		Outcome:            cloneOfficialResultOutcome(r.outcome),
		SourceProjection:   cloneArenaDerivedRevision(r.sourceProjection),
		RecordedAt:         r.recordedAt,
	}
}

type OfficialResultRevisionCondition struct {
	scope                    OfficialResultScope
	expectedSeries           domain.ArenaSeries
	expectedCurrentHead      *OfficialResultRevisionHead
	expectedSourceProjection domain.ArenaDerivedRevision
	expectedSeriesRevision   ArenaSeriesRowRevision
	expectedAttemptRevision  ArenaAttemptRowRevision
	plannedRevisionID        domain.ArenaOfficialResultRevisionID
}

func (c OfficialResultRevisionCondition) Validate() error {
	if err := c.scope.Validate(); err != nil {
		return err
	}
	if err := validateOfficialConditionSeries(c); err != nil {
		return err
	}
	if err := validateOfficialSourceProjection(c.expectedSourceProjection, c.scope); err != nil {
		return err
	}
	if err := validateOfficialConditionRows(c); err != nil {
		return err
	}
	return validateOfficialConditionHead(c)
}

func validateOfficialConditionSeries(c OfficialResultRevisionCondition) error {
	if err := c.expectedSeries.Validate(); err != nil {
		return invalidOfficialResultRevision("invalid expected Series")
	}
	if c.expectedSeries.ID != c.scope.SeriesID || c.expectedSeries.TournamentID != c.scope.TournamentID {
		return invalidOfficialResultRevision("expected Series scope mismatch")
	}
	return nil
}

func validateOfficialConditionRows(c OfficialResultRevisionCondition) error {
	if c.expectedSeriesRevision <= 0 ||
		(c.scope.Kind == OfficialResultSubjectGame && c.expectedAttemptRevision <= 0) ||
		(c.scope.Kind == OfficialResultSubjectSeries && c.expectedAttemptRevision != 0) ||
		c.plannedRevisionID.IsZero() {
		return invalidOfficialResultRevision("invalid expected row revision")
	}
	return nil
}

func validateOfficialConditionHead(c OfficialResultRevisionCondition) error {
	if c.expectedCurrentHead != nil {
		if err := c.expectedCurrentHead.Validate(); err != nil ||
			c.expectedCurrentHead.Scope != c.scope {
			return invalidOfficialResultRevision("invalid expected current revision")
		}
	}
	persistedHead, err := officialResultSubjectHead(c.scope, c.expectedSeries)
	if err != nil || !officialResultRevisionIDPointersEqual(
		persistedHead,
		officialResultCurrentHeadID(c.expectedCurrentHead),
	) {
		return invalidOfficialResultRevision("expected subject head mismatch")
	}
	return nil
}

func (c OfficialResultRevisionCondition) Scope() OfficialResultScope {
	return c.scope
}

func (c OfficialResultRevisionCondition) ExpectedSeries() domain.ArenaSeries {
	return cloneRevisionArenaSeries(c.expectedSeries)
}

func (c OfficialResultRevisionCondition) ExpectedCurrentHead() *OfficialResultRevisionHead {
	return cloneOfficialResultRevisionHeadPointer(c.expectedCurrentHead)
}

func (c OfficialResultRevisionCondition) ExpectedCurrentRevisionID() *domain.ArenaOfficialResultRevisionID {
	return officialResultCurrentHeadID(c.expectedCurrentHead)
}

func (c OfficialResultRevisionCondition) ExpectedCurrentOrdinal() int {
	if c.expectedCurrentHead == nil {
		return 0
	}
	return c.expectedCurrentHead.Ordinal
}

func (c OfficialResultRevisionCondition) ExpectedSourceProjection() domain.ArenaDerivedRevision {
	return cloneArenaDerivedRevision(c.expectedSourceProjection)
}

func (c OfficialResultRevisionCondition) ExpectedSeriesRevision() ArenaSeriesRowRevision {
	return c.expectedSeriesRevision
}

func (c OfficialResultRevisionCondition) ExpectedAttemptRevision() ArenaAttemptRowRevision {
	return c.expectedAttemptRevision
}

type OfficialResultRevisionPlan struct {
	condition OfficialResultRevisionCondition
	revision  OfficialResultRevision
}

func (p OfficialResultRevisionPlan) Condition() OfficialResultRevisionCondition {
	return cloneOfficialResultRevisionCondition(p.condition)
}

func (p OfficialResultRevisionPlan) Revision() OfficialResultRevision {
	return cloneOfficialResultRevision(p.revision)
}

func (p OfficialResultRevisionPlan) Validate() error {
	if err := p.condition.Validate(); err != nil {
		return err
	}
	if err := p.revision.Validate(); err != nil {
		return err
	}
	if p.condition.scope != p.revision.scope ||
		p.condition.plannedRevisionID != p.revision.id ||
		!arenaDerivedRevisionsEqual(p.condition.expectedSourceProjection, p.revision.sourceProjection) {
		return invalidOfficialResultRevision("spliced result revision plan")
	}
	if p.condition.expectedCurrentHead == nil {
		if p.revision.ordinal != 1 || p.revision.previousRevisionID != nil {
			return invalidOfficialResultRevision("invalid initial result revision plan")
		}
		return nil
	}
	current := p.condition.expectedCurrentHead
	if current.Ordinal == math.MaxInt || p.revision.ordinal != current.Ordinal+1 ||
		p.revision.previousRevisionID == nil || *p.revision.previousRevisionID != current.ID ||
		!arenaDerivedRevisionDirectSuccessor(current.SourceProjection, p.revision.sourceProjection) {
		return invalidOfficialResultRevision("invalid successor result revision plan")
	}
	return nil
}

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
		!arenaDerivedRevisionsEqual(command.ExpectedSourceProjection, authority.SourceProjection) {
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
	if command.Actor.Kind != ArenaResultActorOperator || command.Actor.PrincipalID == nil {
		return invalidOfficialResultRevision("result successor requires operator")
	}
	if !arenaDerivedRevisionDirectSuccessor(
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
	var previousRevisionID *domain.ArenaOfficialResultRevisionID
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
		actor:              cloneArenaResultActor(command.Actor),
		outcome:            cloneOfficialResultOutcome(command.Outcome),
		sourceProjection:   cloneArenaDerivedRevision(command.ExpectedSourceProjection),
		recordedAt:         recordedAt,
	}, nil
}

func newOfficialResultRevisionCondition(
	command OfficialResultRevisionCommand,
	authority OfficialResultRevisionAuthority,
) OfficialResultRevisionCondition {
	return OfficialResultRevisionCondition{
		scope:                    command.Scope,
		expectedSeries:           cloneRevisionArenaSeries(authority.PersistedSeries),
		expectedCurrentHead:      cloneOfficialResultRevisionHeadPointer(authority.CurrentHead),
		expectedSourceProjection: cloneArenaDerivedRevision(authority.SourceProjection),
		expectedSeriesRevision:   authority.SeriesRevision,
		expectedAttemptRevision:  authority.AttemptRevision,
		plannedRevisionID:        command.RevisionID,
	}
}

func validateOfficialGameOutcome(outcome OfficialResultOutcome) error {
	if !outcome.GameState.IsTerminal() || !outcome.GameReason.IsLegalFor(outcome.GameState) ||
		outcome.SeriesState != "" || outcome.SeriesReason != "" || outcome.ScoreRevisionID != nil {
		return invalidOfficialResultRevision("invalid Game outcome")
	}
	if outcome.GameState == domain.ArenaGameStateCompleted {
		if outcome.WinnerID == nil || *outcome.WinnerID == uuid.Nil {
			return invalidOfficialResultRevision("completed Game outcome has no winner")
		}
		return nil
	}
	if outcome.WinnerID != nil {
		return invalidOfficialResultRevision("non scoring Game outcome has a winner")
	}
	return nil
}

func validateOfficialSeriesOutcome(outcome OfficialResultOutcome) error {
	if !outcome.SeriesState.IsTerminal() || !outcome.SeriesReason.IsLegalFor(outcome.SeriesState) ||
		outcome.GameState != "" || outcome.GameReason != "" ||
		outcome.ScoreRevisionID == nil || outcome.ScoreRevisionID.IsZero() {
		return invalidOfficialResultRevision("invalid Series outcome")
	}
	if outcome.SeriesState == domain.ArenaSeriesStateCompleted {
		if outcome.WinnerID == nil || *outcome.WinnerID == uuid.Nil {
			return invalidOfficialResultRevision("completed Series outcome has no winner")
		}
		return nil
	}
	if outcome.WinnerID != nil {
		return invalidOfficialResultRevision("cancelled Series outcome has a winner")
	}
	return nil
}

func validateOfficialResultRevisionCommand(
	command OfficialResultRevisionCommand,
	recordedAt time.Time,
) error {
	if err := command.Scope.Validate(); err != nil {
		return err
	}
	if command.CommandID == uuid.Nil || command.RevisionID.IsZero() || command.Actor.Validate() != nil ||
		!validArenaServerTime(recordedAt) || recordedAt.Before(command.ExpectedSourceProjection.CreatedAt()) {
		return invalidOfficialResultRevision("invalid command provenance")
	}
	if command.ExpectedCurrentRevisionID != nil && command.ExpectedCurrentRevisionID.IsZero() {
		return invalidOfficialResultRevision("invalid expected result head")
	}
	if err := validateOfficialSourceProjection(command.ExpectedSourceProjection, command.Scope); err != nil {
		return err
	}
	if err := command.Outcome.Validate(command.Scope.Kind); err != nil {
		return err
	}
	return validateOfficialLocalUUIDRoles(command)
}

func validateOfficialResultRevisionAuthority(authority OfficialResultRevisionAuthority) error {
	if err := authority.Scope.Validate(); err != nil {
		return err
	}
	if err := validateOfficialAuthoritySeries(authority); err != nil {
		return err
	}
	if err := validateOfficialAuthorityRowsAndGames(authority); err != nil {
		return err
	}
	if err := validateOfficialSourceProjection(authority.SourceProjection, authority.Scope); err != nil {
		return err
	}
	if err := validateOfficialPersistedHead(authority); err != nil {
		return err
	}
	return validateOfficialSeriesTransition(authority)
}

func validateOfficialAuthoritySeries(authority OfficialResultRevisionAuthority) error {
	if err := authority.PersistedSeries.Validate(); err != nil {
		return invalidOfficialResultRevision("invalid persisted Series")
	}
	if err := authority.ProjectedSeries.Validate(); err != nil {
		return invalidOfficialResultRevision("invalid projected Series")
	}
	if authority.PersistedSeries.ID != authority.Scope.SeriesID ||
		authority.PersistedSeries.TournamentID != authority.Scope.TournamentID ||
		authority.ProjectedSeries.ID != authority.Scope.SeriesID ||
		authority.ProjectedSeries.TournamentID != authority.Scope.TournamentID {
		return invalidOfficialResultRevision("authority Series scope mismatch")
	}
	return nil
}

func validateOfficialAuthorityRowsAndGames(authority OfficialResultRevisionAuthority) error {
	if authority.SeriesRevision <= 0 ||
		(authority.Scope.Kind == OfficialResultSubjectGame && authority.AttemptRevision <= 0) ||
		(authority.Scope.Kind == OfficialResultSubjectSeries && authority.AttemptRevision != 0) {
		return invalidOfficialResultRevision("invalid authority row revision")
	}
	if err := validateArenaSeriesGameIDsUnique(
		authority.PersistedSeries,
		invalidOfficialResultRevision,
	); err != nil {
		return err
	}
	if err := validateArenaSeriesGameIDsUnique(
		authority.ProjectedSeries,
		invalidOfficialResultRevision,
	); err != nil {
		return err
	}
	return nil
}

func validateOfficialPersistedHead(authority OfficialResultRevisionAuthority) error {
	head, err := officialResultSubjectHead(authority.Scope, authority.PersistedSeries)
	if err != nil {
		return err
	}
	if authority.CurrentHead == nil {
		if head != nil {
			return officialResultRevisionConflict("persisted result head has no current revision")
		}
		return nil
	}
	if err := authority.CurrentHead.Validate(); err != nil ||
		authority.CurrentHead.Scope != authority.Scope ||
		head == nil || *head != authority.CurrentHead.ID {
		return officialResultRevisionConflict("current result revision does not match persisted head")
	}
	if !officialCurrentHeadMatchesSubject(*authority.CurrentHead, authority.PersistedSeries) {
		return officialResultRevisionConflict("persisted result payload does not match current revision")
	}
	return nil
}

func validateOfficialSeriesTransition(authority OfficialResultRevisionAuthority) error {
	if authority.PersistedSeries.FirstParticipantID != authority.ProjectedSeries.FirstParticipantID ||
		authority.PersistedSeries.SecondParticipantID != authority.ProjectedSeries.SecondParticipantID ||
		authority.PersistedSeries.Format != authority.ProjectedSeries.Format {
		return officialResultRevisionConflict("projected Series identity changed")
	}
	switch authority.Scope.Kind {
	case OfficialResultSubjectGame:
		if authority.ProjectedSeriesReason != "" {
			return invalidOfficialResultRevision("Game projection has a Series reason")
		}
		persistedGame, ok := findArenaSeriesGame(authority.PersistedSeries, authority.Scope.GameID)
		if !ok {
			return officialResultRevisionConflict("persisted Game is missing")
		}
		projectedGame, ok := findArenaSeriesGame(authority.ProjectedSeries, authority.Scope.GameID)
		if !ok {
			return officialResultRevisionConflict("projected Game is missing")
		}
		if persistedGame.ID != projectedGame.ID || persistedGame.SlotID != projectedGame.SlotID ||
			persistedGame.AttemptNo != projectedGame.AttemptNo {
			return officialResultRevisionConflict("projected Game identity changed")
		}
		normalized := cloneRevisionArenaSeries(authority.ProjectedSeries)
		normalizedGame, _ := findArenaSeriesGamePointer(&normalized, authority.Scope.GameID)
		*normalizedGame = cloneArenaGame(persistedGame)
		normalized.State = authority.PersistedSeries.State
		normalized.Score = authority.PersistedSeries.Score
		normalized.WinnerID = cloneUUIDPointer(authority.PersistedSeries.WinnerID)
		normalized.CurrentScoreRevisionID = cloneSeriesScoreRevisionIDPointer(
			authority.PersistedSeries.CurrentScoreRevisionID,
		)
		normalized.CurrentResultRevisionID = cloneOfficialResultRevisionIDPointer(
			authority.PersistedSeries.CurrentResultRevisionID,
		)
		if !officialArenaSeriesEqual(authority.PersistedSeries, normalized) {
			return officialResultRevisionConflict("projection changed unrelated Game state")
		}
	case OfficialResultSubjectSeries:
		if !authority.ProjectedSeriesReason.IsLegalFor(authority.ProjectedSeries.State) {
			return invalidOfficialResultRevision("invalid projected Series reason")
		}
		if !officialArenaSeriesStructureEqual(
			authority.PersistedSeries,
			authority.ProjectedSeries,
		) {
			return officialResultRevisionConflict("projection changed unrelated Series state")
		}
	default:
		return invalidOfficialResultRevision("unknown result subject")
	}
	return nil
}

func officialProjectedOutcome(
	authority OfficialResultRevisionAuthority,
) (OfficialResultOutcome, error) {
	switch authority.Scope.Kind {
	case OfficialResultSubjectGame:
		game, ok := findArenaSeriesGame(authority.ProjectedSeries, authority.Scope.GameID)
		if !ok {
			return OfficialResultOutcome{}, officialResultRevisionConflict("projected Game is missing")
		}
		outcome := OfficialResultOutcome{
			GameState:  game.State,
			GameReason: game.ResultReason,
			WinnerID:   cloneUUIDPointer(game.WinnerID),
		}
		if err := outcome.Validate(OfficialResultSubjectGame); err != nil {
			return OfficialResultOutcome{}, err
		}
		if outcome.WinnerID != nil && !officialWinnerIsParticipant(*outcome.WinnerID, authority.ProjectedSeries) {
			return OfficialResultOutcome{}, invalidOfficialResultRevision("Game winner is not a participant")
		}
		return outcome, nil
	case OfficialResultSubjectSeries:
		outcome := OfficialResultOutcome{
			SeriesState:     authority.ProjectedSeries.State,
			SeriesReason:    authority.ProjectedSeriesReason,
			WinnerID:        cloneUUIDPointer(authority.ProjectedSeries.WinnerID),
			ScoreRevisionID: cloneSeriesScoreRevisionIDPointer(authority.ProjectedSeries.CurrentScoreRevisionID),
		}
		if err := outcome.Validate(OfficialResultSubjectSeries); err != nil {
			return OfficialResultOutcome{}, err
		}
		if outcome.WinnerID != nil && !officialWinnerIsParticipant(*outcome.WinnerID, authority.ProjectedSeries) {
			return OfficialResultOutcome{}, invalidOfficialResultRevision("Series winner is not a participant")
		}
		if authority.ProjectedSeries.State == domain.ArenaSeriesStateCompleted {
			expected := authority.ProjectedSeries.Score.Winner(
				authority.ProjectedSeries.FirstParticipantID,
				authority.ProjectedSeries.SecondParticipantID,
				authority.ProjectedSeries.Format,
			)
			if expected == nil || outcome.WinnerID == nil || *expected != *outcome.WinnerID {
				return OfficialResultOutcome{}, invalidOfficialResultRevision("Series winner does not match score")
			}
		}
		return outcome, nil
	default:
		return OfficialResultOutcome{}, invalidOfficialResultRevision("unknown result subject")
	}
}

func validateOfficialProjectedHead(
	command OfficialResultRevisionCommand,
	authority OfficialResultRevisionAuthority,
) error {
	head, err := officialResultSubjectHead(authority.Scope, authority.ProjectedSeries)
	if err != nil {
		return err
	}
	if head == nil || *head != command.RevisionID {
		return officialResultRevisionConflict("projected result head changed")
	}
	return nil
}

func officialResultSubjectHead(
	scope OfficialResultScope,
	series domain.ArenaSeries,
) (*domain.ArenaOfficialResultRevisionID, error) {
	switch scope.Kind {
	case OfficialResultSubjectGame:
		game, ok := findArenaSeriesGame(series, scope.GameID)
		if !ok {
			return nil, officialResultRevisionConflict("Game is missing from Series")
		}
		return cloneOfficialResultRevisionIDPointer(game.ResultRevisionID), nil
	case OfficialResultSubjectSeries:
		return cloneOfficialResultRevisionIDPointer(series.CurrentResultRevisionID), nil
	default:
		return nil, invalidOfficialResultRevision("unknown result subject")
	}
}

func officialCurrentHeadMatchesSubject(
	head OfficialResultRevisionHead,
	series domain.ArenaSeries,
) bool {
	outcome := head.Outcome
	switch head.Scope.Kind {
	case OfficialResultSubjectGame:
		game, ok := findArenaSeriesGame(series, head.Scope.GameID)
		if !ok {
			return false
		}
		return outcome.GameState == game.State && outcome.GameReason == game.ResultReason &&
			uuidPointersEqual(outcome.WinnerID, game.WinnerID)
	case OfficialResultSubjectSeries:
		return outcome.SeriesState == series.State &&
			uuidPointersEqual(outcome.WinnerID, series.WinnerID) &&
			seriesScoreRevisionPointersEqual(outcome.ScoreRevisionID, series.CurrentScoreRevisionID)
	default:
		return false
	}
}

func validateOfficialSourceProjection(
	source domain.ArenaDerivedRevision,
	scope OfficialResultScope,
) error {
	if err := source.Validate(); err != nil || source.TournamentID() != scope.TournamentID {
		return invalidOfficialResultRevision("invalid source projection")
	}
	want := domain.ArenaArtifactRef{EntityID: scope.SeriesID}
	switch scope.Kind {
	case OfficialResultSubjectGame:
		want.Kind = domain.ArenaArtifactKindGameResult
		want.EntityID = scope.GameID
	case OfficialResultSubjectSeries:
		want.Kind = domain.ArenaArtifactKindSeriesResult
	default:
		return invalidOfficialResultRevision("unknown source projection subject")
	}
	if source.Artifact() != want {
		return invalidOfficialResultRevision("source projection artifact mismatch")
	}
	return nil
}

func findArenaSeriesGame(series domain.ArenaSeries, gameID uuid.UUID) (domain.ArenaGame, bool) {
	for _, slot := range series.Slots {
		for _, game := range slot.Attempts {
			if game.ID == gameID {
				return cloneArenaGame(game), true
			}
		}
	}
	return domain.ArenaGame{}, false
}

func findArenaSeriesGamePointer(
	series *domain.ArenaSeries,
	gameID uuid.UUID,
) (*domain.ArenaGame, bool) {
	for slotIndex := range series.Slots {
		for gameIndex := range series.Slots[slotIndex].Attempts {
			game := &series.Slots[slotIndex].Attempts[gameIndex]
			if game.ID == gameID {
				return game, true
			}
		}
	}
	return nil, false
}

func officialWinnerIsParticipant(winnerID uuid.UUID, series domain.ArenaSeries) bool {
	return winnerID == series.FirstParticipantID || winnerID == series.SecondParticipantID
}

func officialArenaSeriesEqual(first, second domain.ArenaSeries) bool {
	if first.ID != second.ID || first.TournamentID != second.TournamentID ||
		first.FirstParticipantID != second.FirstParticipantID ||
		first.SecondParticipantID != second.SecondParticipantID || first.Format != second.Format ||
		first.State != second.State || first.Score != second.Score ||
		!uuidPointersEqual(first.WinnerID, second.WinnerID) ||
		!seriesScoreRevisionPointersEqual(first.CurrentScoreRevisionID, second.CurrentScoreRevisionID) ||
		!officialResultRevisionPointersEqual(first.CurrentResultRevisionID, second.CurrentResultRevisionID) ||
		len(first.Slots) != len(second.Slots) {
		return false
	}
	for index := range first.Slots {
		if !officialArenaGameSlotEqual(first.Slots[index], second.Slots[index]) {
			return false
		}
	}
	return true
}

func officialArenaSeriesStructureEqual(first, second domain.ArenaSeries) bool {
	if !officialArenaSeriesStructureHeaderEqual(first, second) {
		return false
	}
	for slotIndex := range first.Slots {
		if !officialArenaGameSlotStructureEqual(first.Slots[slotIndex], second.Slots[slotIndex]) {
			return false
		}
	}
	return true
}

func officialArenaSeriesStructureHeaderEqual(first, second domain.ArenaSeries) bool {
	if first.ID != second.ID || first.TournamentID != second.TournamentID ||
		first.FirstParticipantID != second.FirstParticipantID ||
		first.SecondParticipantID != second.SecondParticipantID || first.Format != second.Format ||
		len(first.Slots) != len(second.Slots) {
		return false
	}
	return true
}

func officialArenaGameSlotStructureEqual(first, second domain.ArenaGameSlot) bool {
	if first.ID != second.ID || first.SeriesID != second.SeriesID ||
		first.Position != second.Position || first.Category != second.Category ||
		first.ScoreBefore != second.ScoreBefore || len(first.Attempts) != len(second.Attempts) {
		return false
	}
	for gameIndex := range first.Attempts {
		if !officialArenaGameIdentityEqual(first.Attempts[gameIndex], second.Attempts[gameIndex]) {
			return false
		}
	}
	return true
}

func officialArenaGameIdentityEqual(first, second domain.ArenaGame) bool {
	return first.ID == second.ID && first.SlotID == second.SlotID && first.AttemptNo == second.AttemptNo
}

func officialArenaGameSlotEqual(first, second domain.ArenaGameSlot) bool {
	if first.ID != second.ID || first.SeriesID != second.SeriesID || first.Position != second.Position ||
		first.Category != second.Category || first.ScoreBefore != second.ScoreBefore ||
		len(first.Attempts) != len(second.Attempts) {
		return false
	}
	for index := range first.Attempts {
		if !officialArenaGameEqual(first.Attempts[index], second.Attempts[index]) {
			return false
		}
	}
	return true
}

func officialArenaGameEqual(first, second domain.ArenaGame) bool {
	return first.ID == second.ID && first.SlotID == second.SlotID &&
		first.AttemptNo == second.AttemptNo && first.State == second.State &&
		first.ResultReason == second.ResultReason && uuidPointersEqual(first.WinnerID, second.WinnerID) &&
		officialResultRevisionPointersEqual(first.ResultRevisionID, second.ResultRevisionID)
}

func officialResultCurrentHeadID(
	head *OfficialResultRevisionHead,
) *domain.ArenaOfficialResultRevisionID {
	if head == nil {
		return nil
	}
	id := head.ID
	return &id
}

func officialResultOutcomesEqual(first, second OfficialResultOutcome) bool {
	return first.GameState == second.GameState && first.GameReason == second.GameReason &&
		first.SeriesState == second.SeriesState && first.SeriesReason == second.SeriesReason &&
		uuidPointersEqual(first.WinnerID, second.WinnerID) &&
		seriesScoreRevisionPointersEqual(first.ScoreRevisionID, second.ScoreRevisionID)
}

func arenaDerivedRevisionsEqual(first, second domain.ArenaDerivedRevision) bool {
	return first.ID() == second.ID() && first.TournamentID() == second.TournamentID() &&
		first.Artifact() == second.Artifact() && first.RevisionNo() == second.RevisionNo() &&
		arenaDerivedRevisionIDPointersEqual(first.PreviousRevisionID(), second.PreviousRevisionID()) &&
		first.CreatedAt().Equal(second.CreatedAt()) && first.PayloadDigest() == second.PayloadDigest()
}

func arenaDerivedRevisionDirectSuccessor(
	previous domain.ArenaDerivedRevision,
	current domain.ArenaDerivedRevision,
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

func validateArenaSeriesGameIDsUnique(
	series domain.ArenaSeries,
	invalid func(string) error,
) error {
	gameIDs := make(map[uuid.UUID]struct{})
	for _, slot := range series.Slots {
		for _, game := range slot.Attempts {
			if _, exists := gameIDs[game.ID]; exists {
				return invalid("duplicate Game identity")
			}
			gameIDs[game.ID] = struct{}{}
		}
	}
	return nil
}

func validateOfficialResultIdentityOwnership(
	series domain.ArenaSeries,
	scope OfficialResultScope,
	proposed domain.ArenaOfficialResultRevisionID,
	allowProjectedTarget bool,
) error {
	seen := make(map[domain.ArenaOfficialResultRevisionID]struct{})
	add := func(
		id *domain.ArenaOfficialResultRevisionID,
		isTarget bool,
	) error {
		if id == nil {
			return nil
		}
		if _, exists := seen[*id]; exists {
			return invalidOfficialResultRevision("official result identity has multiple local owners")
		}
		seen[*id] = struct{}{}
		if *id == proposed && (!allowProjectedTarget || !isTarget) {
			return invalidOfficialResultRevision("proposed official result identity is already owned")
		}
		return nil
	}
	if err := add(
		series.CurrentResultRevisionID,
		scope.Kind == OfficialResultSubjectSeries,
	); err != nil {
		return err
	}
	for _, slot := range series.Slots {
		for _, game := range slot.Attempts {
			if err := add(
				game.ResultRevisionID,
				scope.Kind == OfficialResultSubjectGame && game.ID == scope.GameID,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

type arenaPlannerUUIDRole string

const (
	arenaPlannerRoleTournament  arenaPlannerUUIDRole = "tournament"
	arenaPlannerRoleSeries      arenaPlannerUUIDRole = "series"
	arenaPlannerRoleGame        arenaPlannerUUIDRole = "game"
	arenaPlannerRoleSlot        arenaPlannerUUIDRole = "slot"
	arenaPlannerRoleParticipant arenaPlannerUUIDRole = "participant"
	arenaPlannerRoleCommand     arenaPlannerUUIDRole = "command"
	arenaPlannerRoleOfficial    arenaPlannerUUIDRole = "official_result"
	arenaPlannerRoleScore       arenaPlannerUUIDRole = "series_score"
	arenaPlannerRoleSource      arenaPlannerUUIDRole = "source_projection"
	arenaPlannerRoleActor       arenaPlannerUUIDRole = "actor"
)

type arenaPlannerUUIDRegistry struct {
	roles   map[uuid.UUID]arenaPlannerUUIDRole
	invalid func(string) error
}

func newArenaPlannerUUIDRegistry(invalid func(string) error) *arenaPlannerUUIDRegistry {
	return &arenaPlannerUUIDRegistry{
		roles:   make(map[uuid.UUID]arenaPlannerUUIDRole),
		invalid: invalid,
	}
}

func (r *arenaPlannerUUIDRegistry) add(id uuid.UUID, role arenaPlannerUUIDRole) error {
	if id == uuid.Nil {
		return nil
	}
	if existing, ok := r.roles[id]; ok && existing != role {
		return r.invalid("cross-role identity alias")
	}
	r.roles[id] = role
	return nil
}

func (r *arenaPlannerUUIDRegistry) addSource(source domain.ArenaDerivedRevision) error {
	if err := r.add(source.ID().UUID(), arenaPlannerRoleSource); err != nil {
		return err
	}
	if previous := source.PreviousRevisionID(); previous != nil {
		return r.add(previous.UUID(), arenaPlannerRoleSource)
	}
	return nil
}

func (r *arenaPlannerUUIDRegistry) addActor(actor ArenaResultActor) error {
	if actor.PrincipalID == nil {
		return nil
	}
	return r.add(*actor.PrincipalID, arenaPlannerRoleActor)
}

func (r *arenaPlannerUUIDRegistry) addAttempt(reference SeriesScoreAttemptReference) error {
	for _, value := range []struct {
		id   uuid.UUID
		role arenaPlannerUUIDRole
	}{
		{reference.SlotID, arenaPlannerRoleSlot},
		{reference.GameID, arenaPlannerRoleGame},
		{reference.CurrentGameResultRevisionID.UUID(), arenaPlannerRoleOfficial},
	} {
		if err := r.add(value.id, value.role); err != nil {
			return err
		}
	}
	if reference.WinnerID != nil {
		return r.add(*reference.WinnerID, arenaPlannerRoleParticipant)
	}
	return nil
}

func (r *arenaPlannerUUIDRegistry) addSeries(series domain.ArenaSeries) error {
	if err := r.addSeriesIdentity(series); err != nil {
		return err
	}
	if err := r.addSeriesHeads(series); err != nil {
		return err
	}
	return r.addSeriesSlots(series.Slots)
}

func (r *arenaPlannerUUIDRegistry) addSeriesIdentity(series domain.ArenaSeries) error {
	for _, value := range []struct {
		id   uuid.UUID
		role arenaPlannerUUIDRole
	}{
		{series.TournamentID, arenaPlannerRoleTournament},
		{series.ID, arenaPlannerRoleSeries},
		{series.FirstParticipantID, arenaPlannerRoleParticipant},
		{series.SecondParticipantID, arenaPlannerRoleParticipant},
	} {
		if err := r.add(value.id, value.role); err != nil {
			return err
		}
	}
	if series.WinnerID != nil {
		return r.add(*series.WinnerID, arenaPlannerRoleParticipant)
	}
	return nil
}

func (r *arenaPlannerUUIDRegistry) addSeriesHeads(series domain.ArenaSeries) error {
	if series.CurrentScoreRevisionID != nil {
		if err := r.add(series.CurrentScoreRevisionID.UUID(), arenaPlannerRoleScore); err != nil {
			return err
		}
	}
	if series.CurrentResultRevisionID != nil {
		if err := r.add(series.CurrentResultRevisionID.UUID(), arenaPlannerRoleOfficial); err != nil {
			return err
		}
	}
	return nil
}

func (r *arenaPlannerUUIDRegistry) addSeriesSlots(slots []domain.ArenaGameSlot) error {
	for _, slot := range slots {
		if err := r.add(slot.ID, arenaPlannerRoleSlot); err != nil {
			return err
		}
		for _, game := range slot.Attempts {
			if err := r.addSeriesGame(game); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *arenaPlannerUUIDRegistry) addSeriesGame(game domain.ArenaGame) error {
	if err := r.add(game.ID, arenaPlannerRoleGame); err != nil {
		return err
	}
	if game.WinnerID != nil {
		if err := r.add(*game.WinnerID, arenaPlannerRoleParticipant); err != nil {
			return err
		}
	}
	if game.ResultRevisionID != nil {
		return r.add(game.ResultRevisionID.UUID(), arenaPlannerRoleOfficial)
	}
	return nil
}

func (r *arenaPlannerUUIDRegistry) addOfficialHead(head OfficialResultRevisionHead) error {
	for _, value := range []struct {
		id   uuid.UUID
		role arenaPlannerUUIDRole
	}{
		{head.ID.UUID(), arenaPlannerRoleOfficial},
		{head.CommandID, arenaPlannerRoleCommand},
	} {
		if err := r.add(value.id, value.role); err != nil {
			return err
		}
	}
	if head.PreviousRevisionID != nil {
		if err := r.add(head.PreviousRevisionID.UUID(), arenaPlannerRoleOfficial); err != nil {
			return err
		}
	}
	if head.Outcome.WinnerID != nil {
		if err := r.add(*head.Outcome.WinnerID, arenaPlannerRoleParticipant); err != nil {
			return err
		}
	}
	if head.Outcome.ScoreRevisionID != nil {
		if err := r.add(head.Outcome.ScoreRevisionID.UUID(), arenaPlannerRoleScore); err != nil {
			return err
		}
	}
	if err := r.addSource(head.SourceProjection); err != nil {
		return err
	}
	return r.addActor(head.Actor)
}

func validateOfficialLocalUUIDRoles(command OfficialResultRevisionCommand) error {
	roles := newArenaPlannerUUIDRegistry(invalidOfficialResultRevision)
	values := []struct {
		id   uuid.UUID
		role arenaPlannerUUIDRole
	}{
		{command.Scope.TournamentID, arenaPlannerRoleTournament},
		{command.Scope.SeriesID, arenaPlannerRoleSeries},
		{command.Scope.GameID, arenaPlannerRoleGame},
		{command.CommandID, arenaPlannerRoleCommand},
		{command.RevisionID.UUID(), arenaPlannerRoleOfficial},
	}
	if command.ExpectedCurrentRevisionID != nil {
		values = append(values, struct {
			id   uuid.UUID
			role arenaPlannerUUIDRole
		}{command.ExpectedCurrentRevisionID.UUID(), arenaPlannerRoleOfficial})
		if *command.ExpectedCurrentRevisionID == command.RevisionID {
			return invalidOfficialResultRevision("result revision did not advance")
		}
	}
	if command.Outcome.WinnerID != nil {
		values = append(values, struct {
			id   uuid.UUID
			role arenaPlannerUUIDRole
		}{*command.Outcome.WinnerID, arenaPlannerRoleParticipant})
	}
	if command.Outcome.ScoreRevisionID != nil {
		values = append(values, struct {
			id   uuid.UUID
			role arenaPlannerUUIDRole
		}{command.Outcome.ScoreRevisionID.UUID(), arenaPlannerRoleScore})
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

func validateOfficialPlannerUUIDRoles(
	command OfficialResultRevisionCommand,
	authority OfficialResultRevisionAuthority,
) error {
	roles := newArenaPlannerUUIDRegistry(invalidOfficialResultRevision)
	if err := roles.add(command.CommandID, arenaPlannerRoleCommand); err != nil {
		return err
	}
	if err := roles.add(command.RevisionID.UUID(), arenaPlannerRoleOfficial); err != nil {
		return err
	}
	if err := roles.addSource(command.ExpectedSourceProjection); err != nil {
		return err
	}
	if err := roles.addActor(command.Actor); err != nil {
		return err
	}
	if authority.CurrentHead != nil {
		if err := roles.addOfficialHead(*authority.CurrentHead); err != nil {
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

func arenaDerivedRevisionIDPointersEqual(
	first *domain.ArenaDerivedRevisionID,
	second *domain.ArenaDerivedRevisionID,
) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}

func cloneArenaDerivedRevision(value domain.ArenaDerivedRevision) domain.ArenaDerivedRevision {
	return value
}

func cloneRevisionArenaSeries(series domain.ArenaSeries) domain.ArenaSeries {
	clone := series
	clone.WinnerID = cloneUUIDPointer(series.WinnerID)
	clone.CurrentScoreRevisionID = cloneSeriesScoreRevisionIDPointer(series.CurrentScoreRevisionID)
	clone.CurrentResultRevisionID = cloneOfficialResultRevisionIDPointer(series.CurrentResultRevisionID)
	if series.Slots == nil {
		clone.Slots = nil
		return clone
	}
	clone.Slots = make([]domain.ArenaGameSlot, len(series.Slots))
	for slotIndex := range series.Slots {
		slot := series.Slots[slotIndex]
		clone.Slots[slotIndex] = slot
		if slot.Attempts == nil {
			clone.Slots[slotIndex].Attempts = nil
			continue
		}
		clone.Slots[slotIndex].Attempts = make([]domain.ArenaGame, len(slot.Attempts))
		for gameIndex := range slot.Attempts {
			clone.Slots[slotIndex].Attempts[gameIndex] = cloneArenaGame(slot.Attempts[gameIndex])
		}
	}
	return clone
}

func cloneArenaResultActor(actor ArenaResultActor) ArenaResultActor {
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
	clone.actor = cloneArenaResultActor(revision.actor)
	clone.outcome = cloneOfficialResultOutcome(revision.outcome)
	clone.sourceProjection = cloneArenaDerivedRevision(revision.sourceProjection)
	return clone
}

func officialResultRevisionFromHead(head OfficialResultRevisionHead) OfficialResultRevision {
	return OfficialResultRevision{
		scope:              head.Scope,
		id:                 head.ID,
		previousRevisionID: cloneOfficialResultRevisionIDPointer(head.PreviousRevisionID),
		ordinal:            head.Ordinal,
		commandID:          head.CommandID,
		actor:              cloneArenaResultActor(head.Actor),
		outcome:            cloneOfficialResultOutcome(head.Outcome),
		sourceProjection:   cloneArenaDerivedRevision(head.SourceProjection),
		recordedAt:         head.RecordedAt,
	}
}

func cloneOfficialResultRevisionHead(head OfficialResultRevisionHead) OfficialResultRevisionHead {
	clone := head
	clone.PreviousRevisionID = cloneOfficialResultRevisionIDPointer(head.PreviousRevisionID)
	clone.Actor = cloneArenaResultActor(head.Actor)
	clone.Outcome = cloneOfficialResultOutcome(head.Outcome)
	clone.SourceProjection = cloneArenaDerivedRevision(head.SourceProjection)
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
	clone.expectedSeries = cloneRevisionArenaSeries(condition.expectedSeries)
	clone.expectedCurrentHead = cloneOfficialResultRevisionHeadPointer(condition.expectedCurrentHead)
	clone.expectedSourceProjection = cloneArenaDerivedRevision(condition.expectedSourceProjection)
	return clone
}

func cloneOfficialResultRevisionCommand(
	command OfficialResultRevisionCommand,
) OfficialResultRevisionCommand {
	clone := command
	clone.Actor = cloneArenaResultActor(command.Actor)
	clone.ExpectedCurrentRevisionID = cloneOfficialResultRevisionIDPointer(
		command.ExpectedCurrentRevisionID,
	)
	clone.ExpectedSourceProjection = cloneArenaDerivedRevision(command.ExpectedSourceProjection)
	clone.Outcome = cloneOfficialResultOutcome(command.Outcome)
	return clone
}

func cloneOfficialResultRevisionAuthority(
	authority OfficialResultRevisionAuthority,
) OfficialResultRevisionAuthority {
	clone := authority
	clone.PersistedSeries = cloneRevisionArenaSeries(authority.PersistedSeries)
	clone.ProjectedSeries = cloneRevisionArenaSeries(authority.ProjectedSeries)
	clone.SourceProjection = cloneArenaDerivedRevision(authority.SourceProjection)
	clone.CurrentHead = cloneOfficialResultRevisionHeadPointer(authority.CurrentHead)
	return clone
}

func officialResultRevisionIDPointersEqual(
	first *domain.ArenaOfficialResultRevisionID,
	second *domain.ArenaOfficialResultRevisionID,
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
