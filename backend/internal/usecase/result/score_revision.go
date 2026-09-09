package result

import (
	"errors"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var (
	ErrInvalidSeriesScoreRevision  = errors.New("invalid series score revision")
	ErrSeriesScoreRevisionConflict = errors.New("series score revision conflict")
)

type SeriesScoreRevisionOperation string

const (
	SeriesScoreRevisionOperationInitialize      SeriesScoreRevisionOperation = "initialize"
	SeriesScoreRevisionOperationAppendAttempt   SeriesScoreRevisionOperation = "append_attempt"
	SeriesScoreRevisionOperationReplaceResult   SeriesScoreRevisionOperation = "replace_result"
	SeriesScoreRevisionOperationNoShow          SeriesScoreRevisionOperation = "no_show"
	SeriesScoreRevisionOperationPreStartForfeit SeriesScoreRevisionOperation = "pre_start_forfeit"
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
	State                       domain.GameState
	WinnerID                    *uuid.UUID
	Reason                      domain.GameResultReason
	CurrentGameResultRevisionID domain.OfficialResultRevisionID
}

// SeriesScoreTerminalSource distinguishes the terminal authority for scores
// that cannot be derived from completed game attempts.
type SeriesScoreTerminalSource string

const (
	SeriesScoreTerminalSourceNormalNoShow    SeriesScoreTerminalSource = "normal_no_show"
	SeriesScoreTerminalSourcePreStartForfeit SeriesScoreTerminalSource = "pre_start_forfeit"
)

// SeriesScoreTerminalEvidence is immutable provenance for a non-played
// terminal score. It intentionally carries only normalized record identities.
type SeriesScoreTerminalEvidence struct {
	Source                  SeriesScoreTerminalSource
	CommitID                uuid.UUID
	ForfeitingParticipantID uuid.UUID
	WinnerID                uuid.UUID
	AnchorAttemptID         uuid.UUID
}

func (e SeriesScoreTerminalEvidence) Validate(
	firstParticipantID, secondParticipantID uuid.UUID,
) error {
	if e.CommitID == uuid.Nil {
		return invalidSeriesScoreRevision("terminal score evidence has no commit")
	}
	switch e.Source {
	case SeriesScoreTerminalSourceNormalNoShow:
		if e.ForfeitingParticipantID != uuid.Nil || e.WinnerID != uuid.Nil || e.AnchorAttemptID != uuid.Nil {
			return invalidSeriesScoreRevision("normal no-show score evidence has adjudication fields")
		}
	case SeriesScoreTerminalSourcePreStartForfeit:
		if e.ForfeitingParticipantID == uuid.Nil || e.WinnerID == uuid.Nil || e.AnchorAttemptID == uuid.Nil ||
			e.ForfeitingParticipantID == e.WinnerID ||
			(e.ForfeitingParticipantID != firstParticipantID && e.ForfeitingParticipantID != secondParticipantID) ||
			(e.WinnerID != firstParticipantID && e.WinnerID != secondParticipantID) {
			return invalidSeriesScoreRevision("invalid pre-start forfeit score evidence")
		}
	default:
		return invalidSeriesScoreRevision("unknown terminal score evidence source")
	}
	return nil
}

func (r SeriesScoreAttemptReference) Validate() error {
	if r.SlotID == uuid.Nil || r.GameID == uuid.Nil || r.SlotID == r.GameID ||
		r.SlotPosition < 1 || r.AttemptNo < 1 || !r.State.IsTerminal() ||
		r.CurrentGameResultRevisionID.IsZero() || !r.Reason.IsLegalFor(r.State) {
		return invalidSeriesScoreRevision("invalid terminal attempt reference")
	}
	if r.State == domain.GameStateCompleted {
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

type SeriesScoreRevisionCommand struct {
	Scope                     SeriesScoreRevisionScope
	Operation                 SeriesScoreRevisionOperation
	CommandID                 uuid.UUID
	RevisionID                domain.SeriesScoreRevisionID
	Actor                     domain.ResultActor
	ExpectedCurrentRevisionID *domain.SeriesScoreRevisionID
	ExpectedSourceProjection  domain.DerivedRevision
	Attempt                   *SeriesScoreAttemptReference
}

type SeriesScoreRevisionAuthority struct {
	Scope            SeriesScoreRevisionScope
	PersistedSeries  domain.Series
	ProjectedSeries  domain.Series
	SourceProjection domain.DerivedRevision
	CurrentHead      *SeriesScoreRevisionHead
	SeriesRevision   SeriesRowRevision
	AttemptRevision  AttemptRowRevision
}

type SeriesScoreRevisionHead struct {
	sourceOrigin        revisionSourceOrigin
	correctionSource    persistedCorrectionSourceBinding
	Scope               SeriesScoreRevisionScope
	ID                  domain.SeriesScoreRevisionID
	PreviousRevisionID  *domain.SeriesScoreRevisionID
	Ordinal             int
	Operation           SeriesScoreRevisionOperation
	CommandID           uuid.UUID
	Actor               domain.ResultActor
	CommandAttempt      *SeriesScoreAttemptReference
	FirstParticipantID  uuid.UUID
	SecondParticipantID uuid.UUID
	Format              domain.SeriesFormat
	Score               domain.SeriesScore
	Attempts            []SeriesScoreAttemptReference
	TerminalEvidence    *SeriesScoreTerminalEvidence
	SourceProjection    domain.DerivedRevision
	RecordedAt          time.Time
}

func (h SeriesScoreRevisionHead) Validate() error {
	return seriesScoreRevisionFromHead(h).Validate()
}

func (h SeriesScoreRevisionHead) Clone() SeriesScoreRevisionHead {
	return cloneSeriesScoreRevisionHead(h)
}

type SeriesScoreRevision struct {
	sourceOrigin        revisionSourceOrigin
	correctionSource    persistedCorrectionSourceBinding
	scope               SeriesScoreRevisionScope
	id                  domain.SeriesScoreRevisionID
	previousRevisionID  *domain.SeriesScoreRevisionID
	ordinal             int
	operation           SeriesScoreRevisionOperation
	commandID           uuid.UUID
	actor               domain.ResultActor
	commandAttempt      *SeriesScoreAttemptReference
	firstParticipantID  uuid.UUID
	secondParticipantID uuid.UUID
	format              domain.SeriesFormat
	score               domain.SeriesScore
	attempts            []SeriesScoreAttemptReference
	terminalEvidence    *SeriesScoreTerminalEvidence
	sourceProjection    domain.DerivedRevision
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
		validServerTime(r.recordedAt) && !r.recordedAt.Before(r.sourceProjection.CreatedAt())
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
	if r.terminalEvidence != nil {
		return validateSeriesScoreTerminalPayload(r)
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

func validateSeriesScoreTerminalPayload(r SeriesScoreRevision) error {
	if err := r.terminalEvidence.Validate(r.firstParticipantID, r.secondParticipantID); err != nil {
		return err
	}
	if err := r.score.Validate(r.format); err != nil {
		return invalidSeriesScoreRevision("invalid terminal score")
	}
	switch r.terminalEvidence.Source {
	case SeriesScoreTerminalSourceNormalNoShow:
		if len(r.attempts) == 0 {
			return invalidSeriesScoreRevision("normal no-show has no terminal attempt evidence")
		}
		for _, attempt := range r.attempts {
			if attempt.State != domain.GameStateCancelled {
				return invalidSeriesScoreRevision("normal no-show contains played evidence")
			}
		}
	case SeriesScoreTerminalSourcePreStartForfeit:
		if len(r.attempts) != 0 || r.score.Winner(r.firstParticipantID, r.secondParticipantID, r.format) == nil ||
			*r.score.Winner(r.firstParticipantID, r.secondParticipantID, r.format) != r.terminalEvidence.WinnerID {
			return invalidSeriesScoreRevision("pre-start forfeit does not prove the terminal winner")
		}
	default:
		return invalidSeriesScoreRevision("unknown terminal score evidence source")
	}
	return nil
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func validateSeriesScoreRevisionOperation(r SeriesScoreRevision) error {
	switch r.operation {
	case SeriesScoreRevisionOperationInitialize:
		if r.ordinal != 1 || r.commandAttempt != nil || len(r.attempts) != 0 ||
			r.terminalEvidence != nil || r.score != (domain.SeriesScore{}) {
			return invalidSeriesScoreRevision("invalid initial score revision")
		}
	case SeriesScoreRevisionOperationAppendAttempt:
		if r.ordinal == 1 || r.commandAttempt == nil || len(r.attempts) == 0 ||
			r.terminalEvidence != nil || !seriesScoreAttemptReferenceEqual(*r.commandAttempt, r.attempts[len(r.attempts)-1]) {
			return invalidSeriesScoreRevision("invalid appended attempt evidence")
		}
	case SeriesScoreRevisionOperationReplaceResult:
		if r.ordinal == 1 || r.commandAttempt == nil ||
			r.terminalEvidence != nil || !containsSeriesScoreAttempt(r.attempts, *r.commandAttempt) {
			return invalidSeriesScoreRevision("invalid replaced attempt evidence")
		}
	case SeriesScoreRevisionOperationNoShow:
		if r.ordinal == 1 || r.commandAttempt != nil || r.terminalEvidence == nil ||
			r.terminalEvidence.Source != SeriesScoreTerminalSourceNormalNoShow {
			return invalidSeriesScoreRevision("invalid normal no-show score revision")
		}
	case SeriesScoreRevisionOperationPreStartForfeit:
		if r.ordinal == 1 || r.commandAttempt != nil || r.actor.Kind != domain.ResultActorOperator ||
			r.actor.PrincipalID == nil || r.terminalEvidence == nil ||
			r.terminalEvidence.Source != SeriesScoreTerminalSourcePreStartForfeit {
			return invalidSeriesScoreRevision("invalid pre-start forfeit score revision")
		}
	default:
		return invalidSeriesScoreRevision("unknown score revision operation")
	}
	return nil
}

func (r SeriesScoreRevision) Scope() SeriesScoreRevisionScope {
	return r.scope
}

func (r SeriesScoreRevision) ID() domain.SeriesScoreRevisionID {
	return r.id
}

func (r SeriesScoreRevision) PreviousRevisionID() *domain.SeriesScoreRevisionID {
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

func (r SeriesScoreRevision) Actor() domain.ResultActor {
	return cloneResultActor(r.actor)
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

func (r SeriesScoreRevision) Format() domain.SeriesFormat {
	return r.format
}

func (r SeriesScoreRevision) Score() domain.SeriesScore {
	return r.score
}

func (r SeriesScoreRevision) Attempts() []SeriesScoreAttemptReference {
	return cloneSeriesScoreAttemptReferences(r.attempts)
}

func (r SeriesScoreRevision) TerminalEvidence() *SeriesScoreTerminalEvidence {
	return cloneSeriesScoreTerminalEvidencePointer(r.terminalEvidence)
}

func (r SeriesScoreRevision) SourceProjection() domain.DerivedRevision {
	return cloneDerivedRevision(r.sourceProjection)
}

func (r SeriesScoreRevision) RecordedAt() time.Time {
	return r.recordedAt
}

func (r SeriesScoreRevision) Head() SeriesScoreRevisionHead {
	return SeriesScoreRevisionHead{
		sourceOrigin:        r.sourceOrigin,
		correctionSource:    r.correctionSource,
		Scope:               r.scope,
		ID:                  r.id,
		PreviousRevisionID:  cloneSeriesScoreRevisionIDPointer(r.previousRevisionID),
		Ordinal:             r.ordinal,
		Operation:           r.operation,
		CommandID:           r.commandID,
		Actor:               cloneResultActor(r.actor),
		CommandAttempt:      cloneSeriesScoreAttemptReferencePointer(r.commandAttempt),
		FirstParticipantID:  r.firstParticipantID,
		SecondParticipantID: r.secondParticipantID,
		Format:              r.format,
		Score:               r.score,
		Attempts:            cloneSeriesScoreAttemptReferences(r.attempts),
		TerminalEvidence:    cloneSeriesScoreTerminalEvidencePointer(r.terminalEvidence),
		SourceProjection:    cloneDerivedRevision(r.sourceProjection),
		RecordedAt:          r.recordedAt,
	}
}

type SeriesScoreRevisionCondition struct {
	scope                    SeriesScoreRevisionScope
	expectedSeries           domain.Series
	expectedCurrentHead      *SeriesScoreRevisionHead
	expectedSourceProjection domain.DerivedRevision
	expectedSeriesRevision   SeriesRowRevision
	expectedAttemptRevision  AttemptRowRevision
	plannedRevisionID        domain.SeriesScoreRevisionID
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

func (c SeriesScoreRevisionCondition) ExpectedSeries() domain.Series {
	return cloneRevisionSeries(c.expectedSeries)
}

func (c SeriesScoreRevisionCondition) ExpectedCurrentHead() *SeriesScoreRevisionHead {
	return cloneSeriesScoreRevisionHeadPointer(c.expectedCurrentHead)
}

func (c SeriesScoreRevisionCondition) ExpectedCurrentRevisionID() *domain.SeriesScoreRevisionID {
	return seriesScoreCurrentHeadID(c.expectedCurrentHead)
}

func (c SeriesScoreRevisionCondition) ExpectedCurrentOrdinal() int {
	if c.expectedCurrentHead == nil {
		return 0
	}
	return c.expectedCurrentHead.Ordinal
}

func (c SeriesScoreRevisionCondition) ExpectedSourceProjection() domain.DerivedRevision {
	return cloneDerivedRevision(c.expectedSourceProjection)
}

func (c SeriesScoreRevisionCondition) ExpectedSeriesRevision() SeriesRowRevision {
	return c.expectedSeriesRevision
}

func (c SeriesScoreRevisionCondition) ExpectedAttemptRevision() AttemptRowRevision {
	return c.expectedAttemptRevision
}

type SeriesScoreRevisionPlan struct {
	condition SeriesScoreRevisionCondition
	revision  SeriesScoreRevision
}

func (p SeriesScoreRevisionPlan) Clone() SeriesScoreRevisionPlan {
	return SeriesScoreRevisionPlan{
		condition: cloneSeriesScoreRevisionCondition(p.condition),
		revision:  cloneSeriesScoreRevision(p.revision),
	}
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
		!derivedRevisionsEqual(p.condition.expectedSourceProjection, p.revision.sourceProjection) {
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
		!derivedRevisionDirectSuccessor(current.SourceProjection, p.revision.sourceProjection) {
		return invalidSeriesScoreRevision("invalid successor score revision plan")
	}
	return nil
}
