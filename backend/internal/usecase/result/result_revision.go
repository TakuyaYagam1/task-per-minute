package result

import (
	"errors"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var (
	ErrInvalidOfficialResultRevision  = errors.New("invalid official result revision")
	ErrOfficialResultRevisionConflict = errors.New("official result revision conflict")
)

type SeriesRowRevision int64

type AttemptRowRevision int64

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
	GameState       domain.GameState
	GameReason      domain.GameResultReason
	SeriesState     domain.SeriesState
	SeriesReason    domain.SeriesResultReason
	WinnerID        *uuid.UUID
	ScoreRevisionID *domain.SeriesScoreRevisionID
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
	RevisionID                domain.OfficialResultRevisionID
	Actor                     domain.ResultActor
	ExpectedCurrentRevisionID *domain.OfficialResultRevisionID
	ExpectedSourceProjection  domain.DerivedRevision
	Outcome                   OfficialResultOutcome
}

type OfficialResultRevisionAuthority struct {
	Scope                 OfficialResultScope
	PersistedSeries       domain.Series
	ProjectedSeries       domain.Series
	ProjectedSeriesReason domain.SeriesResultReason
	SourceProjection      domain.DerivedRevision
	CurrentHead           *OfficialResultRevisionHead
	SeriesRevision        SeriesRowRevision
	AttemptRevision       AttemptRowRevision
}

type OfficialResultRevisionHead struct {
	sourceOrigin       revisionSourceOrigin
	correctionSource   persistedCorrectionSourceBinding
	Scope              OfficialResultScope
	ID                 domain.OfficialResultRevisionID
	PreviousRevisionID *domain.OfficialResultRevisionID
	Ordinal            int
	CommandID          uuid.UUID
	Actor              domain.ResultActor
	Outcome            OfficialResultOutcome
	SourceProjection   domain.DerivedRevision
	RecordedAt         time.Time
}

func (h OfficialResultRevisionHead) Validate() error {
	return officialResultRevisionFromHead(h).Validate()
}

func (h OfficialResultRevisionHead) Clone() OfficialResultRevisionHead {
	return cloneOfficialResultRevisionHead(h)
}

type OfficialResultRevision struct {
	sourceOrigin       revisionSourceOrigin
	correctionSource   persistedCorrectionSourceBinding
	scope              OfficialResultScope
	id                 domain.OfficialResultRevisionID
	previousRevisionID *domain.OfficialResultRevisionID
	ordinal            int
	commandID          uuid.UUID
	actor              domain.ResultActor
	outcome            OfficialResultOutcome
	sourceProjection   domain.DerivedRevision
	recordedAt         time.Time
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func (r OfficialResultRevision) Validate() error {
	if err := r.scope.Validate(); err != nil {
		return err
	}
	if r.id.IsZero() || r.commandID == uuid.Nil || r.ordinal < 1 || r.actor.Validate() != nil ||
		!validServerTime(r.recordedAt) || r.recordedAt.Before(r.sourceProjection.CreatedAt()) {
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
	ordinarySource := r.sourceOrigin == ordinaryRevisionSource
	switch {
	case ordinarySource:
		var previous uuid.UUID
		if r.previousRevisionID != nil {
			previous = r.previousRevisionID.UUID()
		}
		if !matchesOrdinarySource(r.sourceProjection, r.id.UUID(), r.ordinal, previous) {
			return invalidOfficialResultRevision("ordinary source lineage does not match official revision")
		}
	case r.sourceOrigin == correctionRevisionSource:
		if r.previousRevisionID == nil {
			return invalidOfficialResultRevision("correction source has no predecessor")
		}
		entityID := r.scope.SeriesID
		kind := domain.ArtifactKindSeriesResult
		if r.scope.Kind == OfficialResultSubjectGame {
			entityID = r.scope.GameID
			kind = domain.ArtifactKindGameResult
		}
		if !matchesCorrectionSourceBinding(
			r.correctionSource, r.scope.TournamentID, r.scope.SeriesID, entityID, kind,
			r.commandID, r.id.UUID(), r.previousRevisionID.UUID(), r.sourceProjection, r.ordinal,
		) {
			return invalidOfficialResultRevision("invalid persisted correction source binding")
		}
	case r.sourceOrigin != 0:
		return invalidOfficialResultRevision("unknown result source origin")
	}
	return validateOfficialUUIDRoles(OfficialResultRevisionCommand{
		Scope:                     r.scope,
		CommandID:                 r.commandID,
		RevisionID:                r.id,
		Actor:                     r.actor,
		ExpectedCurrentRevisionID: r.previousRevisionID,
		ExpectedSourceProjection:  r.sourceProjection,
		Outcome:                   r.outcome,
	}, r.sourceOrigin != 0)
}

func (r OfficialResultRevision) Scope() OfficialResultScope {
	return r.scope
}

func (r OfficialResultRevision) ID() domain.OfficialResultRevisionID {
	return r.id
}

func (r OfficialResultRevision) PreviousRevisionID() *domain.OfficialResultRevisionID {
	return cloneOfficialResultRevisionIDPointer(r.previousRevisionID)
}

func (r OfficialResultRevision) Ordinal() int {
	return r.ordinal
}

func (r OfficialResultRevision) CommandID() uuid.UUID {
	return r.commandID
}

func (r OfficialResultRevision) Actor() domain.ResultActor {
	return cloneResultActor(r.actor)
}

func (r OfficialResultRevision) Outcome() OfficialResultOutcome {
	return cloneOfficialResultOutcome(r.outcome)
}

func (r OfficialResultRevision) SourceProjection() domain.DerivedRevision {
	return cloneDerivedRevision(r.sourceProjection)
}

func (r OfficialResultRevision) RecordedAt() time.Time {
	return r.recordedAt
}

func (r OfficialResultRevision) Head() OfficialResultRevisionHead {
	return OfficialResultRevisionHead{
		sourceOrigin:       r.sourceOrigin,
		correctionSource:   r.correctionSource,
		Scope:              r.scope,
		ID:                 r.id,
		PreviousRevisionID: cloneOfficialResultRevisionIDPointer(r.previousRevisionID),
		Ordinal:            r.ordinal,
		CommandID:          r.commandID,
		Actor:              cloneResultActor(r.actor),
		Outcome:            cloneOfficialResultOutcome(r.outcome),
		SourceProjection:   cloneDerivedRevision(r.sourceProjection),
		RecordedAt:         r.recordedAt,
	}
}

type OfficialResultRevisionCondition struct {
	scope                    OfficialResultScope
	expectedSeries           domain.Series
	expectedCurrentHead      *OfficialResultRevisionHead
	expectedSourceProjection domain.DerivedRevision
	expectedSeriesRevision   SeriesRowRevision
	expectedAttemptRevision  AttemptRowRevision
	plannedRevisionID        domain.OfficialResultRevisionID
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

func (c OfficialResultRevisionCondition) ExpectedSeries() domain.Series {
	return cloneRevisionSeries(c.expectedSeries)
}

func (c OfficialResultRevisionCondition) ExpectedCurrentHead() *OfficialResultRevisionHead {
	return cloneOfficialResultRevisionHeadPointer(c.expectedCurrentHead)
}

func (c OfficialResultRevisionCondition) ExpectedCurrentRevisionID() *domain.OfficialResultRevisionID {
	return officialResultCurrentHeadID(c.expectedCurrentHead)
}

func (c OfficialResultRevisionCondition) ExpectedCurrentOrdinal() int {
	if c.expectedCurrentHead == nil {
		return 0
	}
	return c.expectedCurrentHead.Ordinal
}

func (c OfficialResultRevisionCondition) ExpectedSourceProjection() domain.DerivedRevision {
	return cloneDerivedRevision(c.expectedSourceProjection)
}

func (c OfficialResultRevisionCondition) ExpectedSeriesRevision() SeriesRowRevision {
	return c.expectedSeriesRevision
}

func (c OfficialResultRevisionCondition) ExpectedAttemptRevision() AttemptRowRevision {
	return c.expectedAttemptRevision
}

type OfficialResultRevisionPlan struct {
	condition OfficialResultRevisionCondition
	revision  OfficialResultRevision
}

func (p OfficialResultRevisionPlan) Clone() OfficialResultRevisionPlan {
	return OfficialResultRevisionPlan{
		condition: cloneOfficialResultRevisionCondition(p.condition),
		revision:  cloneOfficialResultRevision(p.revision),
	}
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
		!derivedRevisionsEqual(p.condition.expectedSourceProjection, p.revision.sourceProjection) {
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
		!derivedRevisionDirectSuccessor(current.SourceProjection, p.revision.sourceProjection) {
		return invalidOfficialResultRevision("invalid successor result revision plan")
	}
	return nil
}
