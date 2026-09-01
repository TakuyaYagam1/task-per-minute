package arena

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var (
	ErrInvalidSeriesResultCascade  = errors.New("invalid Arena Series result cascade")
	ErrSeriesResultCascadeConflict = errors.New("arena Series result cascade conflict")
)

type SeriesResultCascadeCommand struct {
	AuditEventID uuid.UUID
	SeriesResult OfficialResultRevisionCommand
}

type SeriesResultCascadeAuthority struct {
	SeriesResult OfficialResultRevisionAuthority
	CurrentScore SeriesScoreRevisionHead
}

type SeriesResultCascadeAudit struct {
	EventID                uuid.UUID
	CommandID              uuid.UUID
	Actor                  ArenaResultActor
	Scope                  OfficialResultScope
	ScoreRevisionID        domain.ArenaSeriesScoreRevisionID
	SeriesResultRevisionID domain.ArenaOfficialResultRevisionID
	Dependency             domain.ArenaRevisionDependency
	RecordedAt             time.Time
}

func (a SeriesResultCascadeAudit) Clone() SeriesResultCascadeAudit {
	clone := a
	clone.Actor = cloneArenaResultActor(a.Actor)
	return clone
}

func (a SeriesResultCascadeAudit) Validate() error {
	if a.EventID == uuid.Nil || a.CommandID == uuid.Nil || a.Actor.Validate() != nil ||
		a.Scope.Validate() != nil || a.Scope.Kind != OfficialResultSubjectSeries ||
		a.ScoreRevisionID.IsZero() || a.SeriesResultRevisionID.IsZero() ||
		a.Dependency.SourceRevisionID.IsZero() || a.Dependency.DerivedRevisionID.IsZero() ||
		a.Dependency.SourceRevisionID == a.Dependency.DerivedRevisionID ||
		!validArenaServerTime(a.RecordedAt) {
		return invalidSeriesResultCascade("invalid audit evidence")
	}
	return nil
}

type SeriesResultCascadeCondition struct {
	scope                  OfficialResultScope
	expectedSeries         domain.ArenaSeries
	expectedScoreHead      SeriesScoreRevisionHead
	expectedScoreSource    domain.ArenaDerivedRevision
	expectedResultSource   domain.ArenaDerivedRevision
	expectedSeriesRevision ArenaSeriesRowRevision
	commandID              uuid.UUID
	auditEventID           uuid.UUID
	plannedResultID        domain.ArenaOfficialResultRevisionID
	idempotencyKey         [sha256.Size]byte
}

func (c SeriesResultCascadeCondition) Validate() error {
	if err := validateSeriesResultCascadeConditionScope(c); err != nil {
		return err
	}
	if err := validateSeriesResultCascadeConditionSources(c); err != nil {
		return err
	}
	return validateSeriesResultCascadeConditionIdentity(c)
}

func validateSeriesResultCascadeConditionScope(c SeriesResultCascadeCondition) error {
	if err := c.scope.Validate(); err != nil || c.scope.Kind != OfficialResultSubjectSeries {
		return invalidSeriesResultCascade("invalid aggregate scope")
	}
	if err := c.expectedSeries.Validate(); err != nil || c.expectedSeries.State.IsTerminal() ||
		c.expectedSeries.CurrentResultRevisionID != nil {
		return invalidSeriesResultCascade("invalid expected Series")
	}
	return nil
}

func validateSeriesResultCascadeConditionSources(c SeriesResultCascadeCondition) error {
	if err := c.expectedScoreHead.Validate(); err != nil ||
		c.expectedScoreHead.Scope != (SeriesScoreRevisionScope{
			TournamentID: c.scope.TournamentID,
			SeriesID:     c.scope.SeriesID,
		}) || c.expectedSeries.CurrentScoreRevisionID == nil ||
		*c.expectedSeries.CurrentScoreRevisionID != c.expectedScoreHead.ID {
		return invalidSeriesResultCascade("invalid expected score head")
	}
	if err := validateSeriesScoreSourceProjection(c.expectedScoreSource, c.expectedScoreHead.Scope); err != nil {
		return invalidSeriesResultCascade("invalid score source condition")
	}
	if err := validateOfficialSourceProjection(c.expectedResultSource, c.scope); err != nil {
		return invalidSeriesResultCascade("invalid Series result source condition")
	}
	return nil
}

func validateSeriesResultCascadeConditionIdentity(c SeriesResultCascadeCondition) error {
	if c.expectedScoreSource.ID() == c.expectedResultSource.ID() ||
		c.expectedSeriesRevision <= 0 || c.expectedSeriesRevision == ArenaSeriesRowRevision(math.MaxInt64) ||
		c.commandID == uuid.Nil || c.auditEventID == uuid.Nil ||
		c.plannedResultID.IsZero() || c.idempotencyKey == ([sha256.Size]byte{}) {
		return invalidSeriesResultCascade("invalid aggregate CAS identity")
	}
	return nil
}

func (c SeriesResultCascadeCondition) Scope() OfficialResultScope {
	return c.scope
}

func (c SeriesResultCascadeCondition) ExpectedSeries() domain.ArenaSeries {
	return cloneRevisionArenaSeries(c.expectedSeries)
}

func (c SeriesResultCascadeCondition) ExpectedScoreHead() SeriesScoreRevisionHead {
	return c.expectedScoreHead.Clone()
}

func (c SeriesResultCascadeCondition) ExpectedSeriesResultRevisionID() *domain.ArenaOfficialResultRevisionID {
	return nil
}

func (c SeriesResultCascadeCondition) ExpectedSeriesRevision() ArenaSeriesRowRevision {
	return c.expectedSeriesRevision
}

func (c SeriesResultCascadeCondition) ScoreSourceProjection() domain.ArenaDerivedRevision {
	return cloneArenaDerivedRevision(c.expectedScoreSource)
}

func (c SeriesResultCascadeCondition) ResultSourceProjection() domain.ArenaDerivedRevision {
	return cloneArenaDerivedRevision(c.expectedResultSource)
}

func (c SeriesResultCascadeCondition) CommandID() uuid.UUID {
	return c.commandID
}

func (c SeriesResultCascadeCondition) AuditEventID() uuid.UUID {
	return c.auditEventID
}

func (c SeriesResultCascadeCondition) PlannedResultRevisionID() domain.ArenaOfficialResultRevisionID {
	return c.plannedResultID
}

func (c SeriesResultCascadeCondition) IdempotencyKey() [sha256.Size]byte {
	return c.idempotencyKey
}

type SeriesResultCascadePlan struct {
	condition  SeriesResultCascadeCondition
	result     OfficialResultRevisionPlan
	dependency domain.ArenaRevisionDependency
	audit      SeriesResultCascadeAudit
}

func (p SeriesResultCascadePlan) Condition() SeriesResultCascadeCondition {
	return cloneSeriesResultCascadeCondition(p.condition)
}

func (p SeriesResultCascadePlan) SeriesResult() OfficialResultRevisionPlan {
	return OfficialResultRevisionPlan{
		condition: cloneOfficialResultRevisionCondition(p.result.condition),
		revision:  cloneOfficialResultRevision(p.result.revision),
	}
}

func (p SeriesResultCascadePlan) Dependency() domain.ArenaRevisionDependency {
	return p.dependency
}

func (p SeriesResultCascadePlan) Dependencies() []domain.ArenaRevisionDependency {
	return []domain.ArenaRevisionDependency{p.dependency}
}

func (p SeriesResultCascadePlan) Audit() SeriesResultCascadeAudit {
	return p.audit.Clone()
}

func (p SeriesResultCascadePlan) Validate() error {
	if err := p.condition.Validate(); err != nil {
		return err
	}
	if err := p.result.Validate(); err != nil {
		return err
	}
	if err := p.audit.Validate(); err != nil {
		return err
	}
	if err := validateSeriesResultCascadePlanChild(p); err != nil {
		return err
	}
	if err := validateSeriesResultCascadePlanEvidence(p); err != nil {
		return err
	}
	return validateSeriesResultCascadePlanIdentities(p)
}

func validateSeriesResultCascadePlanChild(p SeriesResultCascadePlan) error {
	result := p.result.Revision()
	resultCondition := p.result.Condition()
	outcome := result.Outcome()
	if result.Scope() != p.condition.scope || result.Scope().Kind != OfficialResultSubjectSeries ||
		result.CommandID() != p.condition.commandID || result.ID() != p.condition.plannedResultID ||
		!officialArenaSeriesEqual(resultCondition.ExpectedSeries(), p.condition.expectedSeries) ||
		resultCondition.ExpectedCurrentRevisionID() != nil ||
		resultCondition.ExpectedAttemptRevision() != 0 ||
		resultCondition.ExpectedSeriesRevision() != p.condition.expectedSeriesRevision ||
		outcome.ScoreRevisionID == nil || *outcome.ScoreRevisionID != p.condition.expectedScoreHead.ID ||
		outcome.SeriesState != domain.ArenaSeriesStateCompleted &&
			outcome.SeriesState != domain.ArenaSeriesStateCancelled {
		return invalidSeriesResultCascade("spliced terminal result child")
	}
	return nil
}

func validateSeriesResultCascadePlanEvidence(p SeriesResultCascadePlan) error {
	result := p.result.Revision()
	if p.dependency != (domain.ArenaRevisionDependency{
		SourceRevisionID:  p.condition.expectedScoreHead.SourceProjection.ID(),
		DerivedRevisionID: result.SourceProjection().ID(),
	}) {
		return invalidSeriesResultCascade("invalid score to Series result edge")
	}
	wantAudit := newSeriesResultCascadeAudit(p.audit.EventID, p.condition.expectedScoreHead, result, p.dependency)
	if !seriesResultCascadeAuditsEqual(p.audit, wantAudit) {
		return invalidSeriesResultCascade("audit evidence does not match terminal result")
	}
	wantKey := seriesResultCascadeIdempotencyKey(
		p.audit,
		p.condition.expectedScoreHead,
		result,
		p.condition.expectedSeries,
		p.condition.expectedSeriesRevision,
	)
	if p.condition.idempotencyKey != wantKey ||
		p.condition.auditEventID != p.audit.EventID ||
		!arenaDerivedRevisionsEqual(p.condition.expectedScoreSource, p.condition.expectedScoreHead.SourceProjection) ||
		!arenaDerivedRevisionsEqual(p.condition.expectedResultSource, result.SourceProjection()) {
		return invalidSeriesResultCascade("aggregate condition does not match terminal result")
	}
	return nil
}

// PlanSeriesResultCascade plans only the initial non-terminal-to-terminal
// Series result. Result corrections require a separate correction flow.
func PlanSeriesResultCascade(
	command SeriesResultCascadeCommand,
	authority SeriesResultCascadeAuthority,
	recordedAt time.Time,
) (SeriesResultCascadePlan, error) {
	command = cloneSeriesResultCascadeCommand(command)
	authority = cloneSeriesResultCascadeAuthority(authority)
	if err := validateSeriesResultCascadeInputs(command, authority, recordedAt); err != nil {
		return SeriesResultCascadePlan{}, err
	}
	resultPlan, err := PlanOfficialResultRevision(command.SeriesResult, authority.SeriesResult, recordedAt)
	if err != nil {
		return SeriesResultCascadePlan{}, err
	}
	result := resultPlan.Revision()
	dependency := domain.ArenaRevisionDependency{
		SourceRevisionID:  authority.CurrentScore.SourceProjection.ID(),
		DerivedRevisionID: result.SourceProjection().ID(),
	}
	audit := newSeriesResultCascadeAudit(command.AuditEventID, authority.CurrentScore, result, dependency)
	condition := SeriesResultCascadeCondition{
		scope:                  command.SeriesResult.Scope,
		expectedSeries:         cloneRevisionArenaSeries(authority.SeriesResult.PersistedSeries),
		expectedScoreHead:      authority.CurrentScore.Clone(),
		expectedScoreSource:    cloneArenaDerivedRevision(authority.CurrentScore.SourceProjection),
		expectedResultSource:   cloneArenaDerivedRevision(command.SeriesResult.ExpectedSourceProjection),
		expectedSeriesRevision: authority.SeriesResult.SeriesRevision,
		commandID:              command.SeriesResult.CommandID,
		auditEventID:           command.AuditEventID,
		plannedResultID:        command.SeriesResult.RevisionID,
	}
	condition.idempotencyKey = seriesResultCascadeIdempotencyKey(
		audit,
		authority.CurrentScore,
		result,
		condition.expectedSeries,
		condition.expectedSeriesRevision,
	)
	plan := SeriesResultCascadePlan{
		condition: condition, result: resultPlan, dependency: dependency, audit: audit,
	}
	if err := plan.Validate(); err != nil {
		return SeriesResultCascadePlan{}, err
	}
	return plan, nil
}

func validateSeriesResultCascadeInputs(
	command SeriesResultCascadeCommand,
	authority SeriesResultCascadeAuthority,
	recordedAt time.Time,
) error {
	if err := validateSeriesResultCascadeCommandEnvelope(command, authority, recordedAt); err != nil {
		return err
	}
	if err := validateSeriesResultCascadeTransition(command, authority); err != nil {
		return err
	}
	if err := validateSeriesResultCascadeScoreBinding(command, authority); err != nil {
		return err
	}
	if err := validateSeriesResultCascadeProvenance(command, authority, recordedAt); err != nil {
		return err
	}
	return validateSeriesResultCascadeInputIdentities(command, authority)
}

func validateSeriesResultCascadeCommandEnvelope(
	command SeriesResultCascadeCommand,
	authority SeriesResultCascadeAuthority,
	recordedAt time.Time,
) error {
	if command.AuditEventID == uuid.Nil || !validArenaServerTime(recordedAt) ||
		command.SeriesResult.Scope.Kind != OfficialResultSubjectSeries ||
		command.SeriesResult.ExpectedCurrentRevisionID != nil ||
		authority.SeriesResult.CurrentHead != nil {
		return invalidSeriesResultCascade("invalid command envelope")
	}
	return nil
}

func validateSeriesResultCascadeTransition(
	command SeriesResultCascadeCommand,
	authority SeriesResultCascadeAuthority,
) error {
	persisted := authority.SeriesResult.PersistedSeries
	projected := authority.SeriesResult.ProjectedSeries
	if persisted.State.IsTerminal() || persisted.CurrentResultRevisionID != nil ||
		!projected.State.IsTerminal() || projected.CurrentResultRevisionID == nil ||
		*projected.CurrentResultRevisionID != command.SeriesResult.RevisionID {
		return seriesResultCascadeConflict("Series is not a non-terminal to terminal projection")
	}
	return nil
}

func validateSeriesResultCascadeScoreBinding(
	command SeriesResultCascadeCommand,
	authority SeriesResultCascadeAuthority,
) error {
	persisted := authority.SeriesResult.PersistedSeries
	projected := authority.SeriesResult.ProjectedSeries
	if err := authority.CurrentScore.Validate(); err != nil {
		return invalidSeriesResultCascade("invalid current score revision")
	}
	scope := SeriesScoreRevisionScope{
		TournamentID: command.SeriesResult.Scope.TournamentID,
		SeriesID:     command.SeriesResult.Scope.SeriesID,
	}
	if authority.CurrentScore.Scope != scope || persisted.CurrentScoreRevisionID == nil ||
		projected.CurrentScoreRevisionID == nil ||
		*persisted.CurrentScoreRevisionID != authority.CurrentScore.ID ||
		*projected.CurrentScoreRevisionID != authority.CurrentScore.ID ||
		persisted.Score != authority.CurrentScore.Score || projected.Score != authority.CurrentScore.Score ||
		command.SeriesResult.Outcome.ScoreRevisionID == nil ||
		*command.SeriesResult.Outcome.ScoreRevisionID != authority.CurrentScore.ID {
		return seriesResultCascadeConflict("terminal result is not bound to current score")
	}
	return nil
}

func validateSeriesResultCascadeProvenance(
	command SeriesResultCascadeCommand,
	authority SeriesResultCascadeAuthority,
	recordedAt time.Time,
) error {
	persisted := authority.SeriesResult.PersistedSeries
	persistedAttempts, err := seriesScoreAttemptReferencesFromSeries(persisted)
	if err != nil || !seriesScoreAttemptReferencesEqual(persistedAttempts, authority.CurrentScore.Attempts) {
		return seriesResultCascadeConflict("current score attempt provenance is ambiguous")
	}
	if command.SeriesResult.ExpectedSourceProjection.CreatedAt().Before(
		authority.CurrentScore.SourceProjection.CreatedAt(),
	) || recordedAt.Before(authority.CurrentScore.RecordedAt) {
		return invalidSeriesResultCascade("terminal result predates its score")
	}
	return nil
}

func validateSeriesResultCascadeInputIdentities(
	command SeriesResultCascadeCommand,
	authority SeriesResultCascadeAuthority,
) error {
	if err := validateCascadeSourceProjectionIdentities(
		[]domain.ArenaDerivedRevision{
			authority.CurrentScore.SourceProjection,
			command.SeriesResult.ExpectedSourceProjection,
		},
		invalidSeriesResultCascade,
	); err != nil {
		return err
	}
	if command.SeriesResult.ExpectedSourceProjection.ID() == authority.CurrentScore.SourceProjection.ID() ||
		command.SeriesResult.RevisionID.UUID() == authority.CurrentScore.ID.UUID() {
		return invalidSeriesResultCascade("score and result identities are shared")
	}
	roles := newArenaPlannerUUIDRegistry(invalidSeriesResultCascade)
	for _, value := range []struct {
		id   uuid.UUID
		role arenaPlannerUUIDRole
	}{
		{command.AuditEventID, arenaPlannerUUIDRole("audit")},
		{command.SeriesResult.CommandID, arenaPlannerRoleCommand},
		{command.SeriesResult.RevisionID.UUID(), arenaPlannerRoleOfficial},
	} {
		if err := roles.add(value.id, value.role); err != nil {
			return err
		}
	}
	if err := roles.addActor(command.SeriesResult.Actor); err != nil {
		return err
	}
	if err := addCascadeScoreHeadUUIDRoles(roles, authority.CurrentScore); err != nil {
		return err
	}
	if err := roles.addSource(command.SeriesResult.ExpectedSourceProjection); err != nil {
		return err
	}
	if err := roles.addSeries(authority.SeriesResult.PersistedSeries); err != nil {
		return err
	}
	return roles.addSeries(authority.SeriesResult.ProjectedSeries)
}

func validateSeriesResultCascadePlanIdentities(p SeriesResultCascadePlan) error {
	return validateSeriesResultCascadeInputIdentities(
		SeriesResultCascadeCommand{
			AuditEventID: p.audit.EventID,
			SeriesResult: OfficialResultRevisionCommand{
				Scope: p.result.revision.scope, CommandID: p.result.revision.commandID,
				RevisionID: p.result.revision.id, Actor: p.result.revision.actor,
				ExpectedSourceProjection: p.result.revision.sourceProjection,
			},
		},
		SeriesResultCascadeAuthority{
			SeriesResult: OfficialResultRevisionAuthority{
				PersistedSeries: p.condition.expectedSeries,
				ProjectedSeries: p.condition.expectedSeries,
			},
			CurrentScore: p.condition.expectedScoreHead,
		},
	)
}

func newSeriesResultCascadeAudit(
	eventID uuid.UUID,
	score SeriesScoreRevisionHead,
	result OfficialResultRevision,
	dependency domain.ArenaRevisionDependency,
) SeriesResultCascadeAudit {
	return SeriesResultCascadeAudit{
		EventID: eventID, CommandID: result.CommandID(), Actor: result.Actor(), Scope: result.Scope(),
		ScoreRevisionID: score.ID, SeriesResultRevisionID: result.ID(),
		Dependency: dependency, RecordedAt: result.RecordedAt(),
	}
}

func seriesResultCascadeAuditsEqual(first, second SeriesResultCascadeAudit) bool {
	return first.EventID == second.EventID && first.CommandID == second.CommandID &&
		arenaResultActorsEqual(first.Actor, second.Actor) && first.Scope == second.Scope &&
		first.ScoreRevisionID == second.ScoreRevisionID &&
		first.SeriesResultRevisionID == second.SeriesResultRevisionID &&
		first.Dependency == second.Dependency && first.RecordedAt.Equal(second.RecordedAt)
}

func seriesResultCascadeIdempotencyKey(
	audit SeriesResultCascadeAudit,
	score SeriesScoreRevisionHead,
	result OfficialResultRevision,
	expectedSeries domain.ArenaSeries,
	seriesRevision ArenaSeriesRowRevision,
) [sha256.Size]byte {
	digest := &cascadeDigestBuilder{}
	digest.formatf(
		"audit=%s;command=%s;scope=%s/%s;score=%s;result=%s;edge=%s>%s;at=%s;series_row=%d;",
		audit.EventID,
		audit.CommandID,
		audit.Scope.TournamentID,
		audit.Scope.SeriesID,
		audit.ScoreRevisionID.UUID(),
		audit.SeriesResultRevisionID.UUID(),
		audit.Dependency.SourceRevisionID.UUID(),
		audit.Dependency.DerivedRevisionID.UUID(),
		audit.RecordedAt.Format(time.RFC3339Nano),
		seriesRevision,
	)
	writeCascadeActor(digest, audit.Actor)
	writeCascadeSeries(digest, expectedSeries)
	writeCascadeScoreRevision(digest, seriesScoreRevisionFromHead(score))
	writeCascadeOfficialResultRevision(digest, result)
	return digest.sum()
}

func cloneSeriesResultCascadeCondition(condition SeriesResultCascadeCondition) SeriesResultCascadeCondition {
	clone := condition
	clone.expectedSeries = cloneRevisionArenaSeries(condition.expectedSeries)
	clone.expectedScoreHead = condition.expectedScoreHead.Clone()
	clone.expectedScoreSource = cloneArenaDerivedRevision(condition.expectedScoreSource)
	clone.expectedResultSource = cloneArenaDerivedRevision(condition.expectedResultSource)
	return clone
}

func cloneSeriesResultCascadeCommand(command SeriesResultCascadeCommand) SeriesResultCascadeCommand {
	clone := command
	clone.SeriesResult = cloneOfficialResultRevisionCommand(command.SeriesResult)
	return clone
}

func cloneSeriesResultCascadeAuthority(authority SeriesResultCascadeAuthority) SeriesResultCascadeAuthority {
	seriesResult := cloneOfficialResultRevisionAuthority(authority.SeriesResult)
	seriesResult.PersistedSeries = canonicalizeCascadeSeries(seriesResult.PersistedSeries)
	seriesResult.ProjectedSeries = canonicalizeCascadeSeries(seriesResult.ProjectedSeries)
	return SeriesResultCascadeAuthority{SeriesResult: seriesResult, CurrentScore: authority.CurrentScore.Clone()}
}

func invalidSeriesResultCascade(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidSeriesResultCascade, message)
}

func seriesResultCascadeConflict(message string) error {
	return fmt.Errorf("%w: %s", ErrSeriesResultCascadeConflict, message)
}
