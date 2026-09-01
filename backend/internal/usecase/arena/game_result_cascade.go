package arena

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var (
	ErrInvalidGameResultCascade  = errors.New("invalid Arena Game result cascade")
	ErrGameResultCascadeConflict = errors.New("arena Game result cascade conflict")
)

type GameResultCascadeCommand struct {
	AuditEventID  uuid.UUID
	GameResult    OfficialResultRevisionCommand
	ScoreRevision SeriesScoreRevisionCommand
}

type GameResultCascadeAuthority struct {
	GameResult    OfficialResultRevisionAuthority
	ScoreRevision SeriesScoreRevisionAuthority
}

type GameResultCascadeAudit struct {
	EventID              uuid.UUID
	CommandID            uuid.UUID
	Actor                ArenaResultActor
	Scope                OfficialResultScope
	GameResultRevisionID domain.ArenaOfficialResultRevisionID
	ScoreRevisionID      domain.ArenaSeriesScoreRevisionID
	Dependency           domain.ArenaRevisionDependency
	RecordedAt           time.Time
}

func (a GameResultCascadeAudit) Clone() GameResultCascadeAudit {
	clone := a
	clone.Actor = cloneArenaResultActor(a.Actor)
	return clone
}

func (a GameResultCascadeAudit) Validate() error {
	if a.EventID == uuid.Nil || a.CommandID == uuid.Nil || a.Actor.Validate() != nil ||
		a.Scope.Validate() != nil || a.Scope.Kind != OfficialResultSubjectGame ||
		a.GameResultRevisionID.IsZero() || a.ScoreRevisionID.IsZero() ||
		a.Dependency.SourceRevisionID.IsZero() || a.Dependency.DerivedRevisionID.IsZero() ||
		a.Dependency.SourceRevisionID == a.Dependency.DerivedRevisionID ||
		!validArenaServerTime(a.RecordedAt) {
		return invalidGameResultCascade("invalid audit evidence")
	}
	return nil
}

type GameResultCascadeCondition struct {
	scope                   OfficialResultScope
	expectedSeries          domain.ArenaSeries
	expectedScoreHead       SeriesScoreRevisionHead
	expectedGameSource      domain.ArenaDerivedRevision
	expectedScoreSource     domain.ArenaDerivedRevision
	expectedSeriesRevision  ArenaSeriesRowRevision
	expectedAttemptRevision ArenaAttemptRowRevision
	commandID               uuid.UUID
	auditEventID            uuid.UUID
	plannedGameResultID     domain.ArenaOfficialResultRevisionID
	plannedScoreRevisionID  domain.ArenaSeriesScoreRevisionID
	idempotencyKey          [sha256.Size]byte
}

func (c GameResultCascadeCondition) Validate() error {
	if err := validateGameResultCascadeConditionScope(c); err != nil {
		return err
	}
	if err := validateGameResultCascadeConditionSources(c); err != nil {
		return err
	}
	return validateGameResultCascadeConditionIdentity(c)
}

func validateGameResultCascadeConditionScope(c GameResultCascadeCondition) error {
	if err := c.scope.Validate(); err != nil || c.scope.Kind != OfficialResultSubjectGame {
		return invalidGameResultCascade("invalid aggregate scope")
	}
	if err := c.expectedSeries.Validate(); err != nil || c.expectedSeries.State.IsTerminal() ||
		c.expectedSeries.CurrentResultRevisionID != nil {
		return invalidGameResultCascade("invalid expected Series")
	}
	if err := c.expectedScoreHead.Validate(); err != nil ||
		c.expectedScoreHead.Scope != (SeriesScoreRevisionScope{
			TournamentID: c.scope.TournamentID,
			SeriesID:     c.scope.SeriesID,
		}) || c.expectedSeries.CurrentScoreRevisionID == nil ||
		*c.expectedSeries.CurrentScoreRevisionID != c.expectedScoreHead.ID {
		return invalidGameResultCascade("invalid expected score head")
	}
	return nil
}

func validateGameResultCascadeConditionSources(c GameResultCascadeCondition) error {
	if err := validateOfficialSourceProjection(c.expectedGameSource, c.scope); err != nil {
		return invalidGameResultCascade("invalid Game projection condition")
	}
	if err := validateSeriesScoreSourceProjection(c.expectedScoreSource, SeriesScoreRevisionScope{
		TournamentID: c.scope.TournamentID,
		SeriesID:     c.scope.SeriesID,
	}); err != nil {
		return invalidGameResultCascade("invalid score projection condition")
	}
	return nil
}

func validateGameResultCascadeConditionIdentity(c GameResultCascadeCondition) error {
	if c.expectedGameSource.ID() == c.expectedScoreSource.ID() ||
		c.expectedScoreSource.CreatedAt().Before(c.expectedGameSource.CreatedAt()) ||
		c.expectedSeriesRevision <= 0 || c.expectedSeriesRevision == ArenaSeriesRowRevision(math.MaxInt64) ||
		c.expectedAttemptRevision <= 0 || c.expectedAttemptRevision == ArenaAttemptRowRevision(math.MaxInt64) ||
		c.commandID == uuid.Nil || c.auditEventID == uuid.Nil ||
		c.plannedGameResultID.IsZero() || c.plannedScoreRevisionID.IsZero() ||
		c.idempotencyKey == ([sha256.Size]byte{}) {
		return invalidGameResultCascade("invalid aggregate CAS identity")
	}
	return nil
}

func (c GameResultCascadeCondition) Scope() OfficialResultScope {
	return c.scope
}

func (c GameResultCascadeCondition) ExpectedSeries() domain.ArenaSeries {
	return cloneRevisionArenaSeries(c.expectedSeries)
}

func (c GameResultCascadeCondition) ExpectedScoreHead() SeriesScoreRevisionHead {
	return c.expectedScoreHead.Clone()
}

func (c GameResultCascadeCondition) ExpectedGameResultRevisionID() *domain.ArenaOfficialResultRevisionID {
	return nil
}

func (c GameResultCascadeCondition) ExpectedSeriesRevision() ArenaSeriesRowRevision {
	return c.expectedSeriesRevision
}

func (c GameResultCascadeCondition) ExpectedAttemptRevision() ArenaAttemptRowRevision {
	return c.expectedAttemptRevision
}

func (c GameResultCascadeCondition) GameSourceProjection() domain.ArenaDerivedRevision {
	return cloneArenaDerivedRevision(c.expectedGameSource)
}

func (c GameResultCascadeCondition) ScoreSourceProjection() domain.ArenaDerivedRevision {
	return cloneArenaDerivedRevision(c.expectedScoreSource)
}

func (c GameResultCascadeCondition) CommandID() uuid.UUID {
	return c.commandID
}

func (c GameResultCascadeCondition) AuditEventID() uuid.UUID {
	return c.auditEventID
}

func (c GameResultCascadeCondition) PlannedGameResultRevisionID() domain.ArenaOfficialResultRevisionID {
	return c.plannedGameResultID
}

func (c GameResultCascadeCondition) PlannedScoreRevisionID() domain.ArenaSeriesScoreRevisionID {
	return c.plannedScoreRevisionID
}

func (c GameResultCascadeCondition) IdempotencyKey() [sha256.Size]byte {
	return c.idempotencyKey
}

type GameResultCascadePlan struct {
	condition     GameResultCascadeCondition
	gameResult    OfficialResultRevisionPlan
	scoreRevision SeriesScoreRevisionPlan
	dependency    domain.ArenaRevisionDependency
	audit         GameResultCascadeAudit
}

func (p GameResultCascadePlan) Condition() GameResultCascadeCondition {
	return cloneGameResultCascadeCondition(p.condition)
}

func (p GameResultCascadePlan) GameResult() OfficialResultRevisionPlan {
	return OfficialResultRevisionPlan{
		condition: cloneOfficialResultRevisionCondition(p.gameResult.condition),
		revision:  cloneOfficialResultRevision(p.gameResult.revision),
	}
}

func (p GameResultCascadePlan) ScoreRevision() SeriesScoreRevisionPlan {
	return SeriesScoreRevisionPlan{
		condition: cloneSeriesScoreRevisionCondition(p.scoreRevision.condition),
		revision:  cloneSeriesScoreRevision(p.scoreRevision.revision),
	}
}

func (p GameResultCascadePlan) Dependency() domain.ArenaRevisionDependency {
	return p.dependency
}

func (p GameResultCascadePlan) Audit() GameResultCascadeAudit {
	return p.audit.Clone()
}

func (p GameResultCascadePlan) Validate() error {
	if err := p.condition.Validate(); err != nil {
		return err
	}
	if err := p.gameResult.Validate(); err != nil {
		return err
	}
	if err := p.scoreRevision.Validate(); err != nil {
		return err
	}
	if err := p.audit.Validate(); err != nil {
		return err
	}
	if err := validateGameResultCascadePlanProvenance(p); err != nil {
		return err
	}
	if err := validateGameResultCascadePlanCAS(p); err != nil {
		return err
	}
	if err := validateGameResultCascadePlanEvidence(p); err != nil {
		return err
	}
	return validateGameResultCascadePlanIdentities(p)
}

func validateGameResultCascadePlanProvenance(p GameResultCascadePlan) error {
	game := p.gameResult.Revision()
	score := p.scoreRevision.Revision()
	if game.Scope().Kind != OfficialResultSubjectGame ||
		game.Scope() != p.condition.scope ||
		score.Scope() != (SeriesScoreRevisionScope{
			TournamentID: p.condition.scope.TournamentID,
			SeriesID:     p.condition.scope.SeriesID,
		}) || game.CommandID() != score.CommandID() ||
		game.CommandID() != p.condition.commandID ||
		!arenaResultActorsEqual(game.Actor(), score.Actor()) ||
		!game.RecordedAt().Equal(score.RecordedAt()) {
		return invalidGameResultCascade("spliced child provenance")
	}
	return nil
}

func validateGameResultCascadePlanCAS(p GameResultCascadePlan) error {
	gameCondition := p.gameResult.Condition()
	scoreCondition := p.scoreRevision.Condition()
	expectedHead := scoreCondition.ExpectedCurrentHead()
	if !officialArenaSeriesEqual(gameCondition.ExpectedSeries(), scoreCondition.ExpectedSeries()) ||
		!officialArenaSeriesEqual(gameCondition.ExpectedSeries(), p.condition.expectedSeries) ||
		gameCondition.ExpectedSeriesRevision() != scoreCondition.ExpectedSeriesRevision() ||
		gameCondition.ExpectedAttemptRevision() != scoreCondition.ExpectedAttemptRevision() ||
		gameCondition.ExpectedSeriesRevision() != p.condition.expectedSeriesRevision ||
		gameCondition.ExpectedAttemptRevision() != p.condition.expectedAttemptRevision ||
		gameCondition.ExpectedCurrentRevisionID() != nil ||
		scoreCondition.ExpectedCurrentRevisionID() == nil ||
		*scoreCondition.ExpectedCurrentRevisionID() != p.condition.expectedScoreHead.ID ||
		expectedHead == nil ||
		!seriesScoreRevisionHeadsEqual(*expectedHead, p.condition.expectedScoreHead) {
		return invalidGameResultCascade("child CAS conditions do not collapse")
	}
	return nil
}

func validateGameResultCascadePlanEvidence(p GameResultCascadePlan) error {
	game := p.gameResult.Revision()
	score := p.scoreRevision.Revision()
	if score.Operation() != SeriesScoreRevisionOperationAppendAttempt ||
		!gameCascadeAttemptMatchesResult(score.CommandAttempt(), game) ||
		p.dependency != (domain.ArenaRevisionDependency{
			SourceRevisionID:  game.SourceProjection().ID(),
			DerivedRevisionID: score.SourceProjection().ID(),
		}) {
		return invalidGameResultCascade("invalid Game to score cascade edge")
	}
	wantAudit := newGameResultCascadeAudit(
		p.audit.EventID,
		game,
		score,
		p.dependency,
	)
	if !gameResultCascadeAuditsEqual(p.audit, wantAudit) {
		return invalidGameResultCascade("audit evidence does not match children")
	}
	wantKey := gameResultCascadeIdempotencyKey(
		p.audit,
		game,
		score,
		p.condition.expectedSeries,
		p.condition.expectedScoreHead,
		p.condition.expectedSeriesRevision,
		p.condition.expectedAttemptRevision,
	)
	if p.condition.idempotencyKey != wantKey ||
		p.condition.auditEventID != p.audit.EventID ||
		p.condition.plannedGameResultID != game.ID() ||
		p.condition.plannedScoreRevisionID != score.ID() ||
		!arenaDerivedRevisionsEqual(p.condition.expectedGameSource, game.SourceProjection()) ||
		!arenaDerivedRevisionsEqual(p.condition.expectedScoreSource, score.SourceProjection()) {
		return invalidGameResultCascade("aggregate condition does not match children")
	}
	return nil
}

// PlanGameResultCascade plans only the initial live-to-terminal Game result and
// its score successor. Result corrections require a separate correction flow.
func PlanGameResultCascade(
	command GameResultCascadeCommand,
	authority GameResultCascadeAuthority,
	recordedAt time.Time,
) (GameResultCascadePlan, error) {
	command = cloneGameResultCascadeCommand(command)
	authority = cloneGameResultCascadeAuthority(authority)
	if err := validateGameResultCascadeInputs(command, authority, recordedAt); err != nil {
		return GameResultCascadePlan{}, err
	}
	gamePlan, err := PlanOfficialResultRevision(command.GameResult, authority.GameResult, recordedAt)
	if err != nil {
		return GameResultCascadePlan{}, err
	}
	scorePlan, err := PlanSeriesScoreRevision(command.ScoreRevision, authority.ScoreRevision, recordedAt)
	if err != nil {
		return GameResultCascadePlan{}, err
	}
	game := gamePlan.Revision()
	score := scorePlan.Revision()
	dependency := domain.ArenaRevisionDependency{
		SourceRevisionID:  game.SourceProjection().ID(),
		DerivedRevisionID: score.SourceProjection().ID(),
	}
	audit := newGameResultCascadeAudit(command.AuditEventID, game, score, dependency)
	condition := GameResultCascadeCondition{
		scope:                   command.GameResult.Scope,
		expectedSeries:          cloneRevisionArenaSeries(authority.GameResult.PersistedSeries),
		expectedScoreHead:       authority.ScoreRevision.CurrentHead.Clone(),
		expectedGameSource:      cloneArenaDerivedRevision(command.GameResult.ExpectedSourceProjection),
		expectedScoreSource:     cloneArenaDerivedRevision(command.ScoreRevision.ExpectedSourceProjection),
		expectedSeriesRevision:  authority.GameResult.SeriesRevision,
		expectedAttemptRevision: authority.GameResult.AttemptRevision,
		commandID:               command.GameResult.CommandID,
		auditEventID:            command.AuditEventID,
		plannedGameResultID:     command.GameResult.RevisionID,
		plannedScoreRevisionID:  command.ScoreRevision.RevisionID,
	}
	condition.idempotencyKey = gameResultCascadeIdempotencyKey(
		audit,
		game,
		score,
		condition.expectedSeries,
		condition.expectedScoreHead,
		condition.expectedSeriesRevision,
		condition.expectedAttemptRevision,
	)
	plan := GameResultCascadePlan{
		condition: condition, gameResult: gamePlan, scoreRevision: scorePlan,
		dependency: dependency, audit: audit,
	}
	if err := plan.Validate(); err != nil {
		return GameResultCascadePlan{}, err
	}
	return plan, nil
}

func validateGameResultCascadeInputs(
	command GameResultCascadeCommand,
	authority GameResultCascadeAuthority,
	recordedAt time.Time,
) error {
	if err := validateGameResultCascadeCommandEnvelope(command, authority, recordedAt); err != nil {
		return err
	}
	if err := validateGameResultCascadeCausality(command); err != nil {
		return err
	}
	if err := validateGameResultCascadeAuthority(command, authority); err != nil {
		return err
	}
	return validateGameResultCascadeInputIdentities(command, authority)
}

func validateGameResultCascadeCommandEnvelope(
	command GameResultCascadeCommand,
	authority GameResultCascadeAuthority,
	recordedAt time.Time,
) error {
	if command.AuditEventID == uuid.Nil || !validArenaServerTime(recordedAt) ||
		command.GameResult.Scope.Kind != OfficialResultSubjectGame ||
		command.GameResult.CommandID == uuid.Nil ||
		command.GameResult.CommandID != command.ScoreRevision.CommandID ||
		command.ScoreRevision.Operation != SeriesScoreRevisionOperationAppendAttempt ||
		command.GameResult.ExpectedCurrentRevisionID != nil || authority.GameResult.CurrentHead != nil {
		return invalidGameResultCascade("invalid command envelope")
	}
	return nil
}

func validateGameResultCascadeCausality(command GameResultCascadeCommand) error {
	if command.ScoreRevision.Attempt == nil ||
		!gameCascadeCommandAttemptMatchesResult(command.ScoreRevision.Attempt, command.GameResult) {
		return invalidGameResultCascade("score attempt does not match Game result")
	}
	if !arenaResultActorsEqual(command.GameResult.Actor, command.ScoreRevision.Actor) {
		return invalidGameResultCascade("child actors differ")
	}
	if command.ScoreRevision.ExpectedSourceProjection.CreatedAt().Before(
		command.GameResult.ExpectedSourceProjection.CreatedAt(),
	) {
		return invalidGameResultCascade("score projection predates Game result projection")
	}
	if command.GameResult.Scope.TournamentID != command.ScoreRevision.Scope.TournamentID ||
		command.GameResult.Scope.SeriesID != command.ScoreRevision.Scope.SeriesID {
		return invalidGameResultCascade("child scopes differ")
	}
	return nil
}

func validateGameResultCascadeAuthority(
	command GameResultCascadeCommand,
	authority GameResultCascadeAuthority,
) error {
	if authority.GameResult.Scope != command.GameResult.Scope ||
		authority.ScoreRevision.Scope != command.ScoreRevision.Scope ||
		!officialArenaSeriesEqual(authority.GameResult.PersistedSeries, authority.ScoreRevision.PersistedSeries) ||
		!officialArenaSeriesEqual(authority.GameResult.ProjectedSeries, authority.ScoreRevision.ProjectedSeries) ||
		authority.GameResult.SeriesRevision != authority.ScoreRevision.SeriesRevision ||
		authority.GameResult.AttemptRevision != authority.ScoreRevision.AttemptRevision {
		return gameResultCascadeConflict("child authority snapshots or rows differ")
	}
	if authority.GameResult.ProjectedSeries.State.IsTerminal() ||
		authority.GameResult.ProjectedSeries.CurrentResultRevisionID != nil {
		return invalidGameResultCascade("Game cascade cannot append an early Series result")
	}
	if authority.ScoreRevision.CurrentHead == nil {
		return gameResultCascadeConflict("missing current score revision")
	}
	return nil
}

func validateGameResultCascadeInputIdentities(
	command GameResultCascadeCommand,
	authority GameResultCascadeAuthority,
) error {
	if err := validateCascadeSourceProjectionIdentities(
		[]domain.ArenaDerivedRevision{
			command.GameResult.ExpectedSourceProjection,
			command.ScoreRevision.ExpectedSourceProjection,
			authority.ScoreRevision.CurrentHead.SourceProjection,
		},
		invalidGameResultCascade,
	); err != nil {
		return err
	}
	if command.GameResult.ExpectedSourceProjection.ID() == command.ScoreRevision.ExpectedSourceProjection.ID() ||
		command.GameResult.RevisionID.UUID() == command.ScoreRevision.RevisionID.UUID() {
		return invalidGameResultCascade("sibling revision identity is shared")
	}
	roles := newArenaPlannerUUIDRegistry(invalidGameResultCascade)
	values := []struct {
		id   uuid.UUID
		role arenaPlannerUUIDRole
	}{
		{command.AuditEventID, arenaPlannerUUIDRole("audit")},
		{command.GameResult.CommandID, arenaPlannerRoleCommand},
		{command.GameResult.RevisionID.UUID(), arenaPlannerRoleOfficial},
		{command.ScoreRevision.RevisionID.UUID(), arenaPlannerRoleScore},
	}
	for _, value := range values {
		if err := roles.add(value.id, value.role); err != nil {
			return err
		}
	}
	if err := roles.addSource(command.GameResult.ExpectedSourceProjection); err != nil {
		return err
	}
	if err := roles.addSource(command.ScoreRevision.ExpectedSourceProjection); err != nil {
		return err
	}
	if err := roles.addActor(command.GameResult.Actor); err != nil {
		return err
	}
	if authority.ScoreRevision.CurrentHead != nil {
		if err := addCascadeScoreHeadUUIDRoles(roles, *authority.ScoreRevision.CurrentHead); err != nil {
			return err
		}
	}
	if command.ScoreRevision.Attempt != nil {
		if err := roles.addAttempt(*command.ScoreRevision.Attempt); err != nil {
			return err
		}
	}
	if err := roles.addSeries(authority.GameResult.PersistedSeries); err != nil {
		return err
	}
	return roles.addSeries(authority.GameResult.ProjectedSeries)
}

func validateCascadeSourceProjectionIdentities(
	sources []domain.ArenaDerivedRevision,
	invalid func(string) error,
) error {
	byID := make(map[domain.ArenaDerivedRevisionID]domain.ArenaDerivedRevision, len(sources))
	for _, source := range sources {
		if existing, found := byID[source.ID()]; found && !arenaDerivedRevisionsEqual(existing, source) {
			return invalid("source projection UUID identifies distinct revisions")
		}
		byID[source.ID()] = source
	}
	for _, source := range sources {
		previousID := source.PreviousRevisionID()
		if previousID == nil {
			continue
		}
		previous, found := byID[*previousID]
		if found && !arenaDerivedRevisionDirectSuccessor(previous, source) {
			return invalid("source projection lineage collides or cycles")
		}
	}
	return nil
}

func validateGameResultCascadePlanIdentities(p GameResultCascadePlan) error {
	expectedScoreHead := p.condition.expectedScoreHead.Clone()
	command := GameResultCascadeCommand{
		AuditEventID: p.audit.EventID,
		GameResult: OfficialResultRevisionCommand{
			Scope: p.gameResult.revision.scope, CommandID: p.gameResult.revision.commandID,
			RevisionID:               p.gameResult.revision.id,
			Actor:                    p.gameResult.revision.actor,
			ExpectedSourceProjection: p.gameResult.revision.sourceProjection,
		},
		ScoreRevision: SeriesScoreRevisionCommand{
			Scope:                    p.scoreRevision.revision.scope,
			CommandID:                p.scoreRevision.revision.commandID,
			RevisionID:               p.scoreRevision.revision.id,
			Actor:                    p.scoreRevision.revision.actor,
			Attempt:                  p.scoreRevision.revision.commandAttempt,
			ExpectedSourceProjection: p.scoreRevision.revision.sourceProjection,
		},
	}
	authority := GameResultCascadeAuthority{
		GameResult: OfficialResultRevisionAuthority{
			PersistedSeries: p.condition.expectedSeries,
			ProjectedSeries: p.condition.expectedSeries,
		},
		ScoreRevision: SeriesScoreRevisionAuthority{
			CurrentHead: &expectedScoreHead,
		},
	}
	return validateGameResultCascadeInputIdentities(command, authority)
}

func gameCascadeCommandAttemptMatchesResult(
	attempt *SeriesScoreAttemptReference,
	result OfficialResultRevisionCommand,
) bool {
	if attempt == nil {
		return false
	}
	return attempt.GameID == result.Scope.GameID &&
		attempt.State == result.Outcome.GameState &&
		attempt.Reason == result.Outcome.GameReason &&
		uuidPointersEqual(attempt.WinnerID, result.Outcome.WinnerID) &&
		attempt.CurrentGameResultRevisionID == result.RevisionID
}

func gameCascadeAttemptMatchesResult(
	attempt *SeriesScoreAttemptReference,
	result OfficialResultRevision,
) bool {
	if attempt == nil {
		return false
	}
	outcome := result.Outcome()
	return attempt.GameID == result.Scope().GameID &&
		attempt.State == outcome.GameState && attempt.Reason == outcome.GameReason &&
		uuidPointersEqual(attempt.WinnerID, outcome.WinnerID) &&
		attempt.CurrentGameResultRevisionID == result.ID()
}

func arenaResultActorsEqual(first, second ArenaResultActor) bool {
	return first.Kind == second.Kind && uuidPointersEqual(first.PrincipalID, second.PrincipalID)
}

func newGameResultCascadeAudit(
	eventID uuid.UUID,
	game OfficialResultRevision,
	score SeriesScoreRevision,
	dependency domain.ArenaRevisionDependency,
) GameResultCascadeAudit {
	return GameResultCascadeAudit{
		EventID: eventID, CommandID: game.CommandID(), Actor: game.Actor(), Scope: game.Scope(),
		GameResultRevisionID: game.ID(), ScoreRevisionID: score.ID(), Dependency: dependency,
		RecordedAt: game.RecordedAt(),
	}
}

func gameResultCascadeAuditsEqual(first, second GameResultCascadeAudit) bool {
	return first.EventID == second.EventID && first.CommandID == second.CommandID &&
		arenaResultActorsEqual(first.Actor, second.Actor) && first.Scope == second.Scope &&
		first.GameResultRevisionID == second.GameResultRevisionID &&
		first.ScoreRevisionID == second.ScoreRevisionID && first.Dependency == second.Dependency &&
		first.RecordedAt.Equal(second.RecordedAt)
}

func gameResultCascadeIdempotencyKey(
	audit GameResultCascadeAudit,
	game OfficialResultRevision,
	score SeriesScoreRevision,
	expectedSeries domain.ArenaSeries,
	expectedScoreHead SeriesScoreRevisionHead,
	seriesRevision ArenaSeriesRowRevision,
	attemptRevision ArenaAttemptRowRevision,
) [sha256.Size]byte {
	digest := &cascadeDigestBuilder{}
	digest.formatf(
		"audit=%s;command=%s;scope=%s/%s/%s;game_result=%s;score=%s;edge=%s>%s;at=%s;",
		audit.EventID,
		audit.CommandID,
		audit.Scope.TournamentID,
		audit.Scope.SeriesID,
		audit.Scope.GameID,
		audit.GameResultRevisionID.UUID(),
		audit.ScoreRevisionID.UUID(),
		audit.Dependency.SourceRevisionID.UUID(),
		audit.Dependency.DerivedRevisionID.UUID(),
		audit.RecordedAt.Format(time.RFC3339Nano),
	)
	writeCascadeActor(digest, audit.Actor)
	writeCascadeSeries(digest, expectedSeries)
	writeCascadeScoreRevision(digest, seriesScoreRevisionFromHead(expectedScoreHead))
	writeCascadeOfficialResultRevision(digest, game)
	writeCascadeScoreRevision(digest, score)
	digest.formatf("series_row=%d;attempt_row=%d;", seriesRevision, attemptRevision)
	return digest.sum()
}

func addCascadeScoreHeadUUIDRoles(
	roles *arenaPlannerUUIDRegistry,
	head SeriesScoreRevisionHead,
) error {
	values := []struct {
		id   uuid.UUID
		role arenaPlannerUUIDRole
	}{
		{head.Scope.TournamentID, arenaPlannerRoleTournament},
		{head.Scope.SeriesID, arenaPlannerRoleSeries},
		{head.ID.UUID(), arenaPlannerRoleScore},
		{head.CommandID, arenaPlannerRoleCommand},
		{head.FirstParticipantID, arenaPlannerRoleParticipant},
		{head.SecondParticipantID, arenaPlannerRoleParticipant},
	}
	if head.PreviousRevisionID != nil {
		values = append(values, struct {
			id   uuid.UUID
			role arenaPlannerUUIDRole
		}{head.PreviousRevisionID.UUID(), arenaPlannerRoleScore})
	}
	for _, value := range values {
		if err := roles.add(value.id, value.role); err != nil {
			return err
		}
	}
	if err := roles.addActor(head.Actor); err != nil {
		return err
	}
	if head.CommandAttempt != nil {
		if err := roles.addAttempt(*head.CommandAttempt); err != nil {
			return err
		}
	}
	for _, attempt := range head.Attempts {
		if err := roles.addAttempt(attempt); err != nil {
			return err
		}
	}
	return roles.addSource(head.SourceProjection)
}

type cascadeDigestBuilder struct {
	strings.Builder
}

func (b *cascadeDigestBuilder) formatf(format string, values ...any) {
	_, _ = fmt.Fprintf(&b.Builder, format, values...)
}

func (b *cascadeDigestBuilder) text(value string) {
	_, _ = b.WriteString(value)
}

func (b *cascadeDigestBuilder) sum() [sha256.Size]byte {
	return sha256.Sum256([]byte(b.String()))
}

func writeCascadeActor(writer *cascadeDigestBuilder, actor ArenaResultActor) {
	principal := uuid.Nil
	if actor.PrincipalID != nil {
		principal = *actor.PrincipalID
	}
	writer.formatf("actor=%s/%s;", actor.Kind, principal)
}

func writeCascadeSeries(writer *cascadeDigestBuilder, series domain.ArenaSeries) {
	series = canonicalizeCascadeSeries(series)
	winner := uuid.Nil
	if series.WinnerID != nil {
		winner = *series.WinnerID
	}
	scoreRevision := uuid.Nil
	if series.CurrentScoreRevisionID != nil {
		scoreRevision = series.CurrentScoreRevisionID.UUID()
	}
	resultRevision := uuid.Nil
	if series.CurrentResultRevisionID != nil {
		resultRevision = series.CurrentResultRevisionID.UUID()
	}
	writer.formatf(
		"series=%s/%s/%s/%s/%s/%s/%d/%d/%s/%s/%s;",
		series.TournamentID,
		series.ID,
		series.FirstParticipantID,
		series.SecondParticipantID,
		series.Format,
		series.State,
		series.Score.FirstParticipantWins,
		series.Score.SecondParticipantWins,
		winner,
		scoreRevision,
		resultRevision,
	)
	for _, slot := range series.Slots {
		writer.formatf(
			"slot=%s/%s/%d/%s/%d/%d;",
			slot.ID,
			slot.SeriesID,
			slot.Position,
			slot.Category,
			slot.ScoreBefore.FirstParticipantWins,
			slot.ScoreBefore.SecondParticipantWins,
		)
		for _, game := range slot.Attempts {
			gameWinner := uuid.Nil
			if game.WinnerID != nil {
				gameWinner = *game.WinnerID
			}
			gameResult := uuid.Nil
			if game.ResultRevisionID != nil {
				gameResult = game.ResultRevisionID.UUID()
			}
			writer.formatf(
				"game=%s/%s/%d/%s/%s/%s/%s;",
				game.ID,
				game.SlotID,
				game.AttemptNo,
				game.State,
				game.ResultReason,
				gameWinner,
				gameResult,
			)
		}
	}
}

func writeCascadeDerivedRevision(writer *cascadeDigestBuilder, revision domain.ArenaDerivedRevision) {
	previous := uuid.Nil
	if value := revision.PreviousRevisionID(); value != nil {
		previous = value.UUID()
	}
	writer.formatf(
		"source=%s/%s/%s/%s/%d/%s/%s/%x;",
		revision.ID().UUID(),
		revision.TournamentID(),
		revision.Artifact().Kind,
		revision.Artifact().EntityID,
		revision.RevisionNo(),
		previous,
		revision.CreatedAt().Format(time.RFC3339Nano),
		revision.PayloadDigest(),
	)
}

func writeCascadeOfficialResultRevision(
	writer *cascadeDigestBuilder,
	revision OfficialResultRevision,
) {
	previous := uuid.Nil
	if value := revision.PreviousRevisionID(); value != nil {
		previous = value.UUID()
	}
	outcome := revision.Outcome()
	winner := uuid.Nil
	if outcome.WinnerID != nil {
		winner = *outcome.WinnerID
	}
	score := uuid.Nil
	if outcome.ScoreRevisionID != nil {
		score = outcome.ScoreRevisionID.UUID()
	}
	writer.formatf(
		"official=%s/%s/%s/%s/%d/%s/%s/%s/%s/%s/%s/%s/%s;",
		revision.Scope().TournamentID,
		revision.Scope().SeriesID,
		revision.Scope().GameID,
		revision.ID().UUID(),
		revision.Ordinal(),
		previous,
		revision.CommandID(),
		outcome.GameState,
		outcome.GameReason,
		outcome.SeriesState,
		outcome.SeriesReason,
		winner,
		score,
	)
	writeCascadeActor(writer, revision.Actor())
	writeCascadeDerivedRevision(writer, revision.SourceProjection())
	writer.formatf("official_at=%s;", revision.RecordedAt().Format(time.RFC3339Nano))
}

func writeCascadeScoreAttempt(writer *cascadeDigestBuilder, attempt *SeriesScoreAttemptReference) {
	if attempt == nil {
		writer.text("attempt=nil;")
		return
	}
	winner := uuid.Nil
	if attempt.WinnerID != nil {
		winner = *attempt.WinnerID
	}
	writer.formatf(
		"attempt=%s/%d/%s/%d/%s/%s/%s/%s;",
		attempt.SlotID,
		attempt.SlotPosition,
		attempt.GameID,
		attempt.AttemptNo,
		attempt.State,
		winner,
		attempt.Reason,
		attempt.CurrentGameResultRevisionID.UUID(),
	)
}

func writeCascadeScoreRevision(writer *cascadeDigestBuilder, revision SeriesScoreRevision) {
	previous := uuid.Nil
	if value := revision.PreviousRevisionID(); value != nil {
		previous = value.UUID()
	}
	writer.formatf(
		"score=%s/%s/%s/%d/%s/%s/%s/%s/%s/%d/%d;",
		revision.Scope().TournamentID,
		revision.Scope().SeriesID,
		revision.ID().UUID(),
		revision.Ordinal(),
		previous,
		revision.Operation(),
		revision.CommandID(),
		revision.FirstParticipantID(),
		revision.SecondParticipantID(),
		revision.Score().FirstParticipantWins,
		revision.Score().SecondParticipantWins,
	)
	writeCascadeActor(writer, revision.Actor())
	writeCascadeScoreAttempt(writer, revision.CommandAttempt())
	for _, attempt := range revision.Attempts() {
		attemptCopy := attempt
		writeCascadeScoreAttempt(writer, &attemptCopy)
	}
	writeCascadeDerivedRevision(writer, revision.SourceProjection())
	writer.formatf(
		"format=%s;score_at=%s;",
		revision.Format(),
		revision.RecordedAt().Format(time.RFC3339Nano),
	)
}

func cloneGameResultCascadeCondition(condition GameResultCascadeCondition) GameResultCascadeCondition {
	clone := condition
	clone.expectedSeries = cloneRevisionArenaSeries(condition.expectedSeries)
	clone.expectedScoreHead = condition.expectedScoreHead.Clone()
	clone.expectedGameSource = cloneArenaDerivedRevision(condition.expectedGameSource)
	clone.expectedScoreSource = cloneArenaDerivedRevision(condition.expectedScoreSource)
	return clone
}

func cloneGameResultCascadeCommand(command GameResultCascadeCommand) GameResultCascadeCommand {
	clone := command
	clone.GameResult = cloneOfficialResultRevisionCommand(command.GameResult)
	clone.ScoreRevision = cloneSeriesScoreRevisionCommand(command.ScoreRevision)
	return clone
}

func cloneGameResultCascadeAuthority(authority GameResultCascadeAuthority) GameResultCascadeAuthority {
	gameResult := cloneOfficialResultRevisionAuthority(authority.GameResult)
	gameResult.PersistedSeries = canonicalizeCascadeSeries(gameResult.PersistedSeries)
	gameResult.ProjectedSeries = canonicalizeCascadeSeries(gameResult.ProjectedSeries)
	scoreRevision := cloneSeriesScoreRevisionAuthority(authority.ScoreRevision)
	scoreRevision.PersistedSeries = canonicalizeCascadeSeries(scoreRevision.PersistedSeries)
	scoreRevision.ProjectedSeries = canonicalizeCascadeSeries(scoreRevision.ProjectedSeries)
	return GameResultCascadeAuthority{
		GameResult:    gameResult,
		ScoreRevision: scoreRevision,
	}
}

func canonicalizeCascadeSeries(series domain.ArenaSeries) domain.ArenaSeries {
	canonical := cloneRevisionArenaSeries(series)
	sort.Slice(canonical.Slots, func(first, second int) bool {
		if canonical.Slots[first].Position != canonical.Slots[second].Position {
			return canonical.Slots[first].Position < canonical.Slots[second].Position
		}
		return bytes.Compare(canonical.Slots[first].ID[:], canonical.Slots[second].ID[:]) < 0
	})
	for index := range canonical.Slots {
		attempts := canonical.Slots[index].Attempts
		sort.Slice(attempts, func(first, second int) bool {
			if attempts[first].AttemptNo != attempts[second].AttemptNo {
				return attempts[first].AttemptNo < attempts[second].AttemptNo
			}
			return bytes.Compare(attempts[first].ID[:], attempts[second].ID[:]) < 0
		})
	}
	return canonical
}

func invalidGameResultCascade(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidGameResultCascade, message)
}

func gameResultCascadeConflict(message string) error {
	return fmt.Errorf("%w: %s", ErrGameResultCascadeConflict, message)
}
