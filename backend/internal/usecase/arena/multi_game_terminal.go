package arena

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var (
	ErrInvalidCanonicalMultiGameTerminalization  = errors.New("invalid canonical multi-Game terminalization")
	ErrCanonicalMultiGameTerminalizationConflict = errors.New("canonical multi-Game terminalization conflict")
)

type CanonicalMultiGameTerminalizationCommand struct {
	CommandID    uuid.UUID
	Games        []GameResultCascadeCommand
	SeriesResult SeriesResultCascadeCommand
}

type CanonicalMultiGameTerminalizationAuthority struct {
	Games        []GameResultCascadeAuthority
	SeriesResult SeriesResultCascadeAuthority
}

type CanonicalGameAttemptCondition struct {
	Scope                   OfficialResultScope
	SlotID                  uuid.UUID
	SlotPosition            int
	AttemptNo               int
	ExpectedAttemptRevision ArenaAttemptRowRevision
}

type CanonicalMultiGameTerminalizationCondition struct {
	commandID              uuid.UUID
	expectedSeries         domain.ArenaSeries
	expectedScoreHead      SeriesScoreRevisionHead
	expectedSeriesRevision ArenaSeriesRowRevision
	expectedGameAttempts   []CanonicalGameAttemptCondition
	plannedSeriesResultID  domain.ArenaOfficialResultRevisionID
	recordedAt             time.Time
	idempotencyKey         [sha256.Size]byte
}

func (c CanonicalMultiGameTerminalizationCondition) Validate() error {
	if err := validateCanonicalConditionIdentity(c); err != nil {
		return err
	}
	if err := validateCanonicalConditionSeries(c); err != nil {
		return err
	}
	return validateCanonicalConditionAttempts(c)
}

func validateCanonicalConditionIdentity(c CanonicalMultiGameTerminalizationCondition) error {
	if c.commandID == uuid.Nil || c.expectedSeriesRevision <= 0 ||
		c.expectedSeriesRevision == ArenaSeriesRowRevision(math.MaxInt64) ||
		c.plannedSeriesResultID.IsZero() || c.idempotencyKey == ([sha256.Size]byte{}) ||
		len(c.expectedGameAttempts) == 0 || !validArenaServerTime(c.recordedAt) {
		return invalidCanonicalMultiGameTerminalization("invalid aggregate condition identity")
	}
	return nil
}

func validateCanonicalConditionSeries(c CanonicalMultiGameTerminalizationCondition) error {
	if err := c.expectedSeries.Validate(); err != nil || c.expectedSeries.State.IsTerminal() ||
		c.expectedSeries.CurrentResultRevisionID != nil {
		return invalidCanonicalMultiGameTerminalization("invalid initial Series condition")
	}
	if err := c.expectedScoreHead.Validate(); err != nil ||
		c.expectedSeries.CurrentScoreRevisionID == nil ||
		*c.expectedSeries.CurrentScoreRevisionID != c.expectedScoreHead.ID ||
		c.expectedScoreHead.Scope != (SeriesScoreRevisionScope{
			TournamentID: c.expectedSeries.TournamentID,
			SeriesID:     c.expectedSeries.ID,
		}) || c.expectedScoreHead.Score != c.expectedSeries.Score {
		return invalidCanonicalMultiGameTerminalization("invalid initial score condition")
	}
	initialAttempts, err := seriesScoreAttemptReferencesFromSeries(c.expectedSeries)
	if err != nil || !seriesScoreAttemptReferencesEqual(initialAttempts, c.expectedScoreHead.Attempts) {
		return invalidCanonicalMultiGameTerminalization("initial score provenance is ambiguous")
	}
	return validateCanonicalSeriesGameEligibility(c.expectedSeries)
}

func validateCanonicalConditionAttempts(c CanonicalMultiGameTerminalizationCondition) error {
	want := canonicalUnstartedGameConditions(c.expectedSeries)
	if len(want) != len(c.expectedGameAttempts) {
		return invalidCanonicalMultiGameTerminalization("unstarted Game coverage is incomplete")
	}
	seen := make(map[uuid.UUID]struct{}, len(c.expectedGameAttempts))
	for index, attempt := range c.expectedGameAttempts {
		if err := validateCanonicalAttemptCondition(attempt, want[index]); err != nil {
			return err
		}
		if _, exists := seen[attempt.Scope.GameID]; exists {
			return invalidCanonicalMultiGameTerminalization("duplicate Game attempt condition")
		}
		if index > 0 && attempt.Scope.TournamentID != c.expectedGameAttempts[0].Scope.TournamentID ||
			index > 0 && attempt.Scope.SeriesID != c.expectedGameAttempts[0].Scope.SeriesID {
			return invalidCanonicalMultiGameTerminalization("mixed aggregate scope")
		}
		seen[attempt.Scope.GameID] = struct{}{}
	}
	return nil
}

func validateCanonicalAttemptCondition(
	attempt CanonicalGameAttemptCondition,
	want CanonicalGameAttemptCondition,
) error {
	if attempt.Scope.Validate() != nil || attempt.Scope.Kind != OfficialResultSubjectGame ||
		attempt.SlotID == uuid.Nil || attempt.SlotPosition < 1 || attempt.AttemptNo < 1 ||
		attempt.ExpectedAttemptRevision <= 0 ||
		attempt.ExpectedAttemptRevision == ArenaAttemptRowRevision(math.MaxInt64) {
		return invalidCanonicalMultiGameTerminalization("invalid Game attempt condition")
	}
	if attempt.Scope != want.Scope || attempt.SlotID != want.SlotID ||
		attempt.SlotPosition != want.SlotPosition || attempt.AttemptNo != want.AttemptNo {
		return invalidCanonicalMultiGameTerminalization("Game attempt condition is not canonical")
	}
	return nil
}

func (c CanonicalMultiGameTerminalizationCondition) CommandID() uuid.UUID {
	return c.commandID
}

func (c CanonicalMultiGameTerminalizationCondition) ExpectedSeries() domain.ArenaSeries {
	return cloneRevisionArenaSeries(c.expectedSeries)
}

func (c CanonicalMultiGameTerminalizationCondition) ExpectedScoreHead() SeriesScoreRevisionHead {
	return c.expectedScoreHead.Clone()
}

func (c CanonicalMultiGameTerminalizationCondition) ExpectedSeriesRevision() ArenaSeriesRowRevision {
	return c.expectedSeriesRevision
}

func (c CanonicalMultiGameTerminalizationCondition) ExpectedGameAttempts() []CanonicalGameAttemptCondition {
	return append([]CanonicalGameAttemptCondition(nil), c.expectedGameAttempts...)
}

func (c CanonicalMultiGameTerminalizationCondition) PlannedSeriesResultRevisionID() domain.ArenaOfficialResultRevisionID {
	return c.plannedSeriesResultID
}

func (c CanonicalMultiGameTerminalizationCondition) IdempotencyKey() [sha256.Size]byte {
	return c.idempotencyKey
}

func (c CanonicalMultiGameTerminalizationCondition) RecordedAt() time.Time {
	return c.recordedAt
}

type CanonicalTerminalizationEnvelopeKind string

const (
	CanonicalTerminalizationEnvelopeGame   CanonicalTerminalizationEnvelopeKind = "game_result_and_score"
	CanonicalTerminalizationEnvelopeSeries CanonicalTerminalizationEnvelopeKind = "series_result"
)

type CanonicalTerminalizationEnvelope struct {
	kind   CanonicalTerminalizationEnvelopeKind
	game   *GameResultCascadePlan
	series *SeriesResultCascadePlan
}

func (e CanonicalTerminalizationEnvelope) Kind() CanonicalTerminalizationEnvelopeKind {
	return e.kind
}

func (e CanonicalTerminalizationEnvelope) GameCascade() (GameResultCascadePlan, bool) {
	if e.game == nil {
		return GameResultCascadePlan{}, false
	}
	return cloneGameResultCascadePlan(*e.game), true
}

func (e CanonicalTerminalizationEnvelope) SeriesCascade() (SeriesResultCascadePlan, bool) {
	if e.series == nil {
		return SeriesResultCascadePlan{}, false
	}
	return cloneSeriesResultCascadePlan(*e.series), true
}

type CanonicalMultiGameTerminalizationPlan struct {
	condition       CanonicalMultiGameTerminalizationCondition
	games           []GameResultCascadePlan
	seriesResult    SeriesResultCascadePlan
	projectedStages []domain.ArenaSeries
	finalSeries     domain.ArenaSeries
	dependencies    []domain.ArenaRevisionDependency
}

func (p CanonicalMultiGameTerminalizationPlan) Condition() CanonicalMultiGameTerminalizationCondition {
	return cloneCanonicalMultiGameCondition(p.condition)
}

func (p CanonicalMultiGameTerminalizationPlan) GameCascades() []GameResultCascadePlan {
	clones := make([]GameResultCascadePlan, len(p.games))
	for index := range p.games {
		clones[index] = cloneGameResultCascadePlan(p.games[index])
	}
	return clones
}

func (p CanonicalMultiGameTerminalizationPlan) SeriesResult() SeriesResultCascadePlan {
	return cloneSeriesResultCascadePlan(p.seriesResult)
}

func (p CanonicalMultiGameTerminalizationPlan) Dependencies() []domain.ArenaRevisionDependency {
	return append([]domain.ArenaRevisionDependency(nil), p.dependencies...)
}

func (p CanonicalMultiGameTerminalizationPlan) FinalSeries() domain.ArenaSeries {
	return cloneRevisionArenaSeries(p.finalSeries)
}

func (p CanonicalMultiGameTerminalizationPlan) Envelopes() []CanonicalTerminalizationEnvelope {
	envelopes := make([]CanonicalTerminalizationEnvelope, 0, len(p.games)+1)
	for index := range p.games {
		game := cloneGameResultCascadePlan(p.games[index])
		envelopes = append(envelopes, CanonicalTerminalizationEnvelope{
			kind: CanonicalTerminalizationEnvelopeGame,
			game: &game,
		})
	}
	series := cloneSeriesResultCascadePlan(p.seriesResult)
	envelopes = append(envelopes, CanonicalTerminalizationEnvelope{
		kind:   CanonicalTerminalizationEnvelopeSeries,
		series: &series,
	})
	return envelopes
}

func (p CanonicalMultiGameTerminalizationPlan) Validate() error {
	if err := p.condition.Validate(); err != nil {
		return err
	}
	if len(p.games) == 0 || len(p.games) != len(p.projectedStages) ||
		len(p.dependencies) != len(p.games)+1 {
		return invalidCanonicalMultiGameTerminalization("incomplete atomic envelope")
	}
	if err := p.seriesResult.Validate(); err != nil {
		return err
	}
	state := canonicalPlanValidationState{
		currentSeries: cloneRevisionArenaSeries(p.condition.expectedSeries),
		currentScore:  p.condition.expectedScoreHead.Clone(),
		actor:         p.games[0].GameResult().Revision().Actor(),
		seenCommands:  make(map[uuid.UUID]struct{}, len(p.games)+1),
		seenTargets:   make(map[uuid.UUID]struct{}, len(p.games)),
	}
	if err := validateCanonicalGameEnvelopes(p, &state); err != nil {
		return err
	}
	if err := validateCanonicalSeriesEnvelope(p, state); err != nil {
		return err
	}
	if err := validateCanonicalSeriesSnapshot(p, state); err != nil {
		return err
	}
	wantKey := canonicalMultiGameIdempotencyKey(
		p.condition.commandID,
		p.condition.recordedAt,
		p.games,
		p.seriesResult,
	)
	if p.condition.idempotencyKey != wantKey {
		return invalidCanonicalMultiGameTerminalization("aggregate idempotency key mismatch")
	}
	return validateCanonicalPlanIdentityUniqueness(p)
}

type canonicalPlanValidationState struct {
	currentSeries domain.ArenaSeries
	currentScore  SeriesScoreRevisionHead
	actor         ArenaResultActor
	seenCommands  map[uuid.UUID]struct{}
	seenTargets   map[uuid.UUID]struct{}
}

func validateCanonicalGameEnvelopes(
	p CanonicalMultiGameTerminalizationPlan,
	state *canonicalPlanValidationState,
) error {
	for index := range p.games {
		game := p.games[index]
		if err := game.Validate(); err != nil {
			return err
		}
		if err := validateCanonicalGameEnvelopeTime(game, p.condition.recordedAt); err != nil {
			return err
		}
		condition := game.Condition()
		attempt := game.ScoreRevision().Revision().CommandAttempt()
		if err := validateCanonicalGameEnvelopeMatch(p, *state, index, game, condition, attempt); err != nil {
			return err
		}
		if err := validateCanonicalProjectedGameStage(
			state.currentSeries,
			p.projectedStages[index],
			game,
		); err != nil {
			return err
		}
		if err := validateCanonicalGameEnvelopeOrder(p, state, index, condition, attempt); err != nil {
			return err
		}
		if !arenaResultActorsEqual(state.actor, game.GameResult().Revision().Actor()) ||
			!arenaResultActorsEqual(state.actor, game.ScoreRevision().Revision().Actor()) {
			return invalidCanonicalMultiGameTerminalization("child actors differ")
		}
		state.seenTargets[condition.Scope().GameID] = struct{}{}
		state.seenCommands[condition.CommandID()] = struct{}{}
		state.currentSeries = cloneRevisionArenaSeries(p.projectedStages[index])
		state.currentScore = game.ScoreRevision().Revision().Head()
	}
	return nil
}

func validateCanonicalGameEnvelopeTime(game GameResultCascadePlan, recordedAt time.Time) error {
	if !game.GameResult().Revision().RecordedAt().Equal(recordedAt) ||
		!game.ScoreRevision().Revision().RecordedAt().Equal(recordedAt) ||
		!game.Audit().RecordedAt.Equal(recordedAt) {
		return invalidCanonicalMultiGameTerminalization("Game envelope timestamp differs")
	}
	return nil
}

func validateCanonicalGameEnvelopeMatch(
	p CanonicalMultiGameTerminalizationPlan,
	state canonicalPlanValidationState,
	index int,
	game GameResultCascadePlan,
	condition GameResultCascadeCondition,
	attempt *SeriesScoreAttemptReference,
) error {
	expected := p.condition.expectedGameAttempts[index]
	if attempt == nil || !officialArenaSeriesEqual(condition.ExpectedSeries(), state.currentSeries) ||
		condition.ExpectedSeriesRevision() != p.condition.expectedSeriesRevision ||
		condition.ExpectedAttemptRevision() != expected.ExpectedAttemptRevision ||
		condition.Scope() != expected.Scope || attempt.SlotID != expected.SlotID ||
		attempt.SlotPosition != expected.SlotPosition || attempt.AttemptNo != expected.AttemptNo ||
		!seriesScoreRevisionHeadsEqual(condition.ExpectedScoreHead(), state.currentScore) ||
		p.dependencies[index] != game.Dependency() ||
		!canonicalGameAttemptIsUnstarted(state.currentSeries, condition.Scope().GameID) {
		return invalidCanonicalMultiGameTerminalization("Game envelope does not match aggregate condition")
	}
	return nil
}

func validateCanonicalGameEnvelopeOrder(
	p CanonicalMultiGameTerminalizationPlan,
	state *canonicalPlanValidationState,
	index int,
	condition GameResultCascadeCondition,
	attempt *SeriesScoreAttemptReference,
) error {
	if index > 0 && !canonicalAttemptFollows(
		p.games[index-1].ScoreRevision().Revision().CommandAttempt(),
		attempt,
	) {
		return invalidCanonicalMultiGameTerminalization("Game envelopes are not canonical")
	}
	if _, exists := state.seenTargets[condition.Scope().GameID]; exists {
		return invalidCanonicalMultiGameTerminalization("duplicate Game target")
	}
	if _, exists := state.seenCommands[condition.CommandID()]; exists ||
		condition.CommandID() == p.condition.commandID {
		return invalidCanonicalMultiGameTerminalization("reused child command")
	}
	return nil
}

func validateCanonicalSeriesEnvelope(
	p CanonicalMultiGameTerminalizationPlan,
	state canonicalPlanValidationState,
) error {
	seriesCondition := p.seriesResult.Condition()
	seriesResult := p.seriesResult.SeriesResult().Revision()
	if !seriesResult.RecordedAt().Equal(p.condition.recordedAt) ||
		!p.seriesResult.Audit().RecordedAt.Equal(p.condition.recordedAt) {
		return invalidCanonicalMultiGameTerminalization("terminal Series timestamp differs")
	}
	if !officialArenaSeriesEqual(seriesCondition.ExpectedSeries(), state.currentSeries) ||
		!seriesScoreRevisionHeadsEqual(seriesCondition.ExpectedScoreHead(), state.currentScore) ||
		seriesCondition.ExpectedSeriesRevision() != p.condition.expectedSeriesRevision ||
		p.dependencies[len(p.dependencies)-1] != p.seriesResult.Dependency() ||
		seriesResult.ID() != p.condition.plannedSeriesResultID ||
		seriesResult.CommandID() == p.condition.commandID {
		return invalidCanonicalMultiGameTerminalization("terminal Series envelope is not last")
	}
	if _, exists := state.seenCommands[seriesResult.CommandID()]; exists {
		return invalidCanonicalMultiGameTerminalization("reused terminal Series command")
	}
	if !arenaResultActorsEqual(state.actor, seriesResult.Actor()) {
		return invalidCanonicalMultiGameTerminalization("terminal Series actor differs")
	}
	return nil
}

func validateCanonicalSeriesSnapshot(
	p CanonicalMultiGameTerminalizationPlan,
	state canonicalPlanValidationState,
) error {
	seriesResult := p.seriesResult.SeriesResult().Revision()
	if err := p.finalSeries.Validate(); err != nil || !p.finalSeries.State.IsTerminal() ||
		p.finalSeries.CurrentResultRevisionID == nil ||
		*p.finalSeries.CurrentResultRevisionID != seriesResult.ID() {
		return invalidCanonicalMultiGameTerminalization("invalid final Series snapshot")
	}
	if err := validateCanonicalFinalSeries(
		state.currentSeries,
		state.currentScore,
		p.finalSeries,
		p.seriesResult,
	); err != nil {
		return err
	}
	return nil
}

// PlanCanonicalMultiGameTerminalization plans initial results for every
// unstarted Game, followed by the initial terminal Series result.
func PlanCanonicalMultiGameTerminalization(
	command CanonicalMultiGameTerminalizationCommand,
	authority CanonicalMultiGameTerminalizationAuthority,
	recordedAt time.Time,
) (CanonicalMultiGameTerminalizationPlan, error) {
	command = cloneCanonicalMultiGameCommand(command)
	authority = cloneCanonicalMultiGameAuthority(authority)
	steps, err := canonicalGameSteps(command.Games, authority.Games)
	if err != nil {
		return CanonicalMultiGameTerminalizationPlan{}, err
	}
	if err := validateCanonicalMultiGameInputs(command, authority, steps, recordedAt); err != nil {
		return CanonicalMultiGameTerminalizationPlan{}, err
	}
	gamePlans := make([]GameResultCascadePlan, len(steps))
	projectedStages := make([]domain.ArenaSeries, len(steps))
	dependencies := make([]domain.ArenaRevisionDependency, 0, len(steps)+1)
	attemptConditions := make([]CanonicalGameAttemptCondition, len(steps))
	for index := range steps {
		plan, planErr := PlanGameResultCascade(steps[index].command, steps[index].authority, recordedAt)
		if planErr != nil {
			return CanonicalMultiGameTerminalizationPlan{}, planErr
		}
		gamePlans[index] = plan
		projectedStages[index] = cloneRevisionArenaSeries(steps[index].authority.GameResult.ProjectedSeries)
		dependencies = append(dependencies, plan.Dependency())
		attemptConditions[index] = CanonicalGameAttemptCondition{
			Scope:                   steps[index].command.GameResult.Scope,
			SlotID:                  steps[index].command.ScoreRevision.Attempt.SlotID,
			SlotPosition:            steps[index].command.ScoreRevision.Attempt.SlotPosition,
			AttemptNo:               steps[index].command.ScoreRevision.Attempt.AttemptNo,
			ExpectedAttemptRevision: steps[index].authority.GameResult.AttemptRevision,
		}
	}
	seriesPlan, err := PlanSeriesResultCascade(command.SeriesResult, authority.SeriesResult, recordedAt)
	if err != nil {
		return CanonicalMultiGameTerminalizationPlan{}, err
	}
	dependencies = append(dependencies, seriesPlan.Dependency())
	condition := CanonicalMultiGameTerminalizationCondition{
		commandID:              command.CommandID,
		expectedSeries:         cloneRevisionArenaSeries(steps[0].authority.GameResult.PersistedSeries),
		expectedScoreHead:      steps[0].authority.ScoreRevision.CurrentHead.Clone(),
		expectedSeriesRevision: steps[0].authority.GameResult.SeriesRevision,
		expectedGameAttempts:   attemptConditions,
		plannedSeriesResultID:  command.SeriesResult.SeriesResult.RevisionID,
		recordedAt:             recordedAt,
	}
	condition.idempotencyKey = canonicalMultiGameIdempotencyKey(
		command.CommandID,
		recordedAt,
		gamePlans,
		seriesPlan,
	)
	plan := CanonicalMultiGameTerminalizationPlan{
		condition: condition, games: gamePlans, seriesResult: seriesPlan,
		projectedStages: projectedStages,
		finalSeries:     cloneRevisionArenaSeries(authority.SeriesResult.SeriesResult.ProjectedSeries),
		dependencies:    dependencies,
	}
	if err := plan.Validate(); err != nil {
		return CanonicalMultiGameTerminalizationPlan{}, err
	}
	return plan, nil
}

type canonicalGameStep struct {
	command   GameResultCascadeCommand
	authority GameResultCascadeAuthority
}

func canonicalGameSteps(
	commands []GameResultCascadeCommand,
	authorities []GameResultCascadeAuthority,
) ([]canonicalGameStep, error) {
	if len(commands) == 0 || len(commands) != len(authorities) {
		return nil, invalidCanonicalMultiGameTerminalization("Game commands and authorities differ")
	}
	byGame := make(map[uuid.UUID]GameResultCascadeAuthority, len(authorities))
	for _, authority := range authorities {
		gameID := authority.GameResult.Scope.GameID
		if gameID == uuid.Nil {
			return nil, invalidCanonicalMultiGameTerminalization("authority has no Game target")
		}
		if _, exists := byGame[gameID]; exists {
			return nil, invalidCanonicalMultiGameTerminalization("ambiguous Game authority")
		}
		byGame[gameID] = cloneGameResultCascadeAuthority(authority)
	}
	steps := make([]canonicalGameStep, 0, len(commands))
	seen := make(map[uuid.UUID]struct{}, len(commands))
	for _, command := range commands {
		gameID := command.GameResult.Scope.GameID
		if _, exists := seen[gameID]; exists {
			return nil, invalidCanonicalMultiGameTerminalization("duplicate Game command")
		}
		authority, exists := byGame[gameID]
		if !exists {
			return nil, invalidCanonicalMultiGameTerminalization("missing Game authority")
		}
		seen[gameID] = struct{}{}
		steps = append(steps, canonicalGameStep{
			command: cloneGameResultCascadeCommand(command), authority: authority,
		})
	}
	sort.Slice(steps, func(first, second int) bool {
		left := steps[first].command.ScoreRevision.Attempt
		right := steps[second].command.ScoreRevision.Attempt
		if left == nil || right == nil {
			return left != nil
		}
		if left.SlotPosition != right.SlotPosition {
			return left.SlotPosition < right.SlotPosition
		}
		if left.AttemptNo != right.AttemptNo {
			return left.AttemptNo < right.AttemptNo
		}
		return bytes.Compare(left.GameID[:], right.GameID[:]) < 0
	})
	return steps, nil
}

func validateCanonicalMultiGameInputs(
	command CanonicalMultiGameTerminalizationCommand,
	authority CanonicalMultiGameTerminalizationAuthority,
	steps []canonicalGameStep,
	recordedAt time.Time,
) error {
	state, err := newCanonicalInputValidationState(command, steps, recordedAt)
	if err != nil {
		return err
	}
	if err := validateCascadeSourceProjectionIdentities(
		canonicalInputSourceProjections(command, authority, steps),
		invalidCanonicalMultiGameTerminalization,
	); err != nil {
		return err
	}
	if err := validateCanonicalGameInputStages(steps, recordedAt, &state); err != nil {
		return err
	}
	if err := validateCanonicalSeriesInput(command, authority, state); err != nil {
		return err
	}
	return validateCanonicalInputIdentityUniqueness(command, authority, steps)
}

type canonicalInputValidationState struct {
	seriesRevision ArenaSeriesRowRevision
	currentSeries  domain.ArenaSeries
	currentScore   SeriesScoreRevisionHead
	wantTargets    []CanonicalGameAttemptCondition
	actor          ArenaResultActor
}

func newCanonicalInputValidationState(
	command CanonicalMultiGameTerminalizationCommand,
	steps []canonicalGameStep,
	recordedAt time.Time,
) (canonicalInputValidationState, error) {
	if command.CommandID == uuid.Nil || !validArenaServerTime(recordedAt) || len(steps) == 0 {
		return canonicalInputValidationState{}, invalidCanonicalMultiGameTerminalization("invalid aggregate command")
	}
	if steps[0].authority.ScoreRevision.CurrentHead == nil {
		return canonicalInputValidationState{}, canonicalMultiGameTerminalizationConflict("missing initial score head")
	}
	currentSeries := cloneRevisionArenaSeries(steps[0].authority.GameResult.PersistedSeries)
	if err := validateCanonicalSeriesGameEligibility(currentSeries); err != nil {
		return canonicalInputValidationState{}, err
	}
	wantTargets := canonicalUnstartedGameConditions(currentSeries)
	if len(wantTargets) != len(steps) {
		return canonicalInputValidationState{}, invalidCanonicalMultiGameTerminalization("unstarted Game coverage is incomplete")
	}
	return canonicalInputValidationState{
		seriesRevision: steps[0].authority.GameResult.SeriesRevision,
		currentSeries:  currentSeries,
		currentScore:   steps[0].authority.ScoreRevision.CurrentHead.Clone(),
		wantTargets:    wantTargets,
		actor:          steps[0].command.GameResult.Actor,
	}, nil
}

func validateCanonicalGameInputStages(
	steps []canonicalGameStep,
	recordedAt time.Time,
	state *canonicalInputValidationState,
) error {
	for index := range steps {
		step := steps[index]
		planned, err := validateCanonicalGameInputStage(step, index, *state, recordedAt)
		if err != nil {
			return err
		}
		state.currentSeries = cloneRevisionArenaSeries(step.authority.GameResult.ProjectedSeries)
		state.currentScore = planned.ScoreRevision().Revision().Head()
	}
	return nil
}

func validateCanonicalGameInputStage(
	step canonicalGameStep,
	index int,
	state canonicalInputValidationState,
	recordedAt time.Time,
) (GameResultCascadePlan, error) {
	if err := validateCanonicalGameInputStageMatch(step, index, state); err != nil {
		return GameResultCascadePlan{}, err
	}
	if err := validateCanonicalGameInputStageCausality(step, state.actor); err != nil {
		return GameResultCascadePlan{}, err
	}
	return PlanGameResultCascade(step.command, step.authority, recordedAt)
}

func validateCanonicalGameInputStageMatch(
	step canonicalGameStep,
	index int,
	state canonicalInputValidationState,
) error {
	attempt := step.command.ScoreRevision.Attempt
	want := state.wantTargets[index]
	if step.authority.GameResult.SeriesRevision != state.seriesRevision ||
		step.authority.ScoreRevision.SeriesRevision != state.seriesRevision ||
		!officialArenaSeriesEqual(step.authority.GameResult.PersistedSeries, state.currentSeries) ||
		step.authority.ScoreRevision.CurrentHead == nil ||
		!seriesScoreRevisionHeadsEqual(*step.authority.ScoreRevision.CurrentHead, state.currentScore) ||
		!canonicalGameAttemptIsUnstarted(state.currentSeries, step.command.GameResult.Scope.GameID) ||
		attempt == nil || step.command.GameResult.Scope != want.Scope ||
		attempt.SlotID != want.SlotID || attempt.SlotPosition != want.SlotPosition ||
		attempt.AttemptNo != want.AttemptNo {
		return canonicalMultiGameTerminalizationConflict("Game stages do not share one aggregate CAS")
	}
	return nil
}

func validateCanonicalGameInputStageCausality(
	step canonicalGameStep,
	actor ArenaResultActor,
) error {
	if !arenaResultActorsEqual(actor, step.command.GameResult.Actor) ||
		!arenaResultActorsEqual(actor, step.command.ScoreRevision.Actor) {
		return invalidCanonicalMultiGameTerminalization("child actors differ")
	}
	if step.command.ScoreRevision.ExpectedSourceProjection.CreatedAt().Before(
		step.command.GameResult.ExpectedSourceProjection.CreatedAt(),
	) {
		return invalidCanonicalMultiGameTerminalization("score projection predates Game result projection")
	}
	projectedGame, found := findArenaSeriesGame(
		step.authority.GameResult.ProjectedSeries,
		step.command.GameResult.Scope.GameID,
	)
	if !found || !projectedGame.State.IsTerminal() {
		return canonicalMultiGameTerminalizationConflict("Game stage is not terminal")
	}
	return nil
}

func validateCanonicalSeriesInput(
	command CanonicalMultiGameTerminalizationCommand,
	authority CanonicalMultiGameTerminalizationAuthority,
	state canonicalInputValidationState,
) error {
	seriesAuthority := authority.SeriesResult.SeriesResult
	if seriesAuthority.SeriesRevision != state.seriesRevision ||
		!officialArenaSeriesEqual(seriesAuthority.PersistedSeries, state.currentSeries) ||
		!seriesScoreRevisionHeadsEqual(authority.SeriesResult.CurrentScore, state.currentScore) ||
		!seriesAuthority.ProjectedSeries.State.IsTerminal() {
		return canonicalMultiGameTerminalizationConflict("terminal Series is not the final stage")
	}
	if !arenaResultActorsEqual(state.actor, command.SeriesResult.SeriesResult.Actor) {
		return invalidCanonicalMultiGameTerminalization("terminal Series actor differs")
	}
	if command.SeriesResult.SeriesResult.ExpectedSourceProjection.CreatedAt().Before(
		state.currentScore.SourceProjection.CreatedAt(),
	) {
		return invalidCanonicalMultiGameTerminalization("Series result projection predates terminal score")
	}
	return nil
}

func canonicalGameAttemptIsUnstarted(series domain.ArenaSeries, gameID uuid.UUID) bool {
	game, found := findArenaSeriesGame(series, gameID)
	return found && (game.State == domain.ArenaGameStatePlanned || game.State == domain.ArenaGameStateReady)
}

func validateCanonicalSeriesGameEligibility(series domain.ArenaSeries) error {
	for _, slot := range series.Slots {
		for _, game := range slot.Attempts {
			if game.State.IsTerminal() || game.State == domain.ArenaGameStatePlanned ||
				game.State == domain.ArenaGameStateReady {
				continue
			}
			return invalidCanonicalMultiGameTerminalization("Series contains an in-flight Game")
		}
	}
	return nil
}

func canonicalUnstartedGameConditions(series domain.ArenaSeries) []CanonicalGameAttemptCondition {
	targets := make([]CanonicalGameAttemptCondition, 0)
	for _, slot := range series.Slots {
		for _, game := range slot.Attempts {
			if game.State != domain.ArenaGameStatePlanned && game.State != domain.ArenaGameStateReady {
				continue
			}
			targets = append(targets, CanonicalGameAttemptCondition{
				Scope: OfficialResultScope{
					TournamentID: series.TournamentID,
					SeriesID:     series.ID,
					GameID:       game.ID,
					Kind:         OfficialResultSubjectGame,
				},
				SlotID: slot.ID, SlotPosition: slot.Position, AttemptNo: game.AttemptNo,
			})
		}
	}
	sort.Slice(targets, func(first, second int) bool {
		if targets[first].SlotPosition != targets[second].SlotPosition {
			return targets[first].SlotPosition < targets[second].SlotPosition
		}
		if targets[first].AttemptNo != targets[second].AttemptNo {
			return targets[first].AttemptNo < targets[second].AttemptNo
		}
		return bytes.Compare(targets[first].Scope.GameID[:], targets[second].Scope.GameID[:]) < 0
	})
	return targets
}

func validateCanonicalProjectedGameStage(
	current domain.ArenaSeries,
	projected domain.ArenaSeries,
	plan GameResultCascadePlan,
) error {
	gameResult, scoreRevision, currentGame, err := validateCanonicalProjectedStageHeader(
		current,
		projected,
		plan,
	)
	if err != nil {
		return err
	}
	if err := validateCanonicalProjectedStageRevisions(projected, gameResult, scoreRevision); err != nil {
		return err
	}
	return validateCanonicalProjectedStageIsolation(current, projected, currentGame, gameResult)
}

func validateCanonicalProjectedStageHeader(
	current domain.ArenaSeries,
	projected domain.ArenaSeries,
	plan GameResultCascadePlan,
) (OfficialResultRevision, SeriesScoreRevision, domain.ArenaGame, error) {
	if err := projected.Validate(); err != nil || projected.State.IsTerminal() ||
		projected.CurrentResultRevisionID != nil {
		return OfficialResultRevision{}, SeriesScoreRevision{}, domain.ArenaGame{},
			invalidCanonicalMultiGameTerminalization("invalid intermediate Series snapshot")
	}
	gameResult := plan.GameResult().Revision()
	scoreRevision := plan.ScoreRevision().Revision()
	if scoreRevision.SourceProjection().CreatedAt().Before(gameResult.SourceProjection().CreatedAt()) {
		return OfficialResultRevision{}, SeriesScoreRevision{}, domain.ArenaGame{},
			invalidCanonicalMultiGameTerminalization("score projection predates Game result projection")
	}
	currentGame, found := findArenaSeriesGame(current, gameResult.Scope().GameID)
	if !found || !canonicalGameAttemptIsUnstarted(current, currentGame.ID) {
		return OfficialResultRevision{}, SeriesScoreRevision{}, domain.ArenaGame{},
			invalidCanonicalMultiGameTerminalization("intermediate target was already started")
	}
	return gameResult, scoreRevision, currentGame, nil
}

func validateCanonicalProjectedStageRevisions(
	projected domain.ArenaSeries,
	gameResult OfficialResultRevision,
	scoreRevision SeriesScoreRevision,
) error {
	projectedGame, found := findArenaSeriesGame(projected, gameResult.Scope().GameID)
	if !found {
		return invalidCanonicalMultiGameTerminalization("intermediate target is missing")
	}
	outcome := gameResult.Outcome()
	if projectedGame.State != outcome.GameState || projectedGame.ResultReason != outcome.GameReason ||
		!uuidPointersEqual(projectedGame.WinnerID, outcome.WinnerID) ||
		projectedGame.ResultRevisionID == nil || *projectedGame.ResultRevisionID != gameResult.ID() ||
		projected.CurrentScoreRevisionID == nil ||
		*projected.CurrentScoreRevisionID != scoreRevision.ID() ||
		projected.Score != scoreRevision.Score() {
		return invalidCanonicalMultiGameTerminalization("intermediate snapshot differs from child revisions")
	}
	attempts, err := seriesScoreAttemptReferencesFromSeries(projected)
	if err != nil || !seriesScoreAttemptReferencesEqual(attempts, scoreRevision.Attempts()) {
		return invalidCanonicalMultiGameTerminalization("intermediate score provenance differs")
	}
	return nil
}

func validateCanonicalProjectedStageIsolation(
	current domain.ArenaSeries,
	projected domain.ArenaSeries,
	currentGame domain.ArenaGame,
	gameResult OfficialResultRevision,
) error {
	normalized := cloneRevisionArenaSeries(projected)
	normalizedGame, found := findArenaSeriesGamePointer(&normalized, gameResult.Scope().GameID)
	if !found {
		return invalidCanonicalMultiGameTerminalization("intermediate target is missing")
	}
	*normalizedGame = cloneArenaGame(currentGame)
	normalized.Score = current.Score
	normalized.CurrentScoreRevisionID = cloneSeriesScoreRevisionIDPointer(current.CurrentScoreRevisionID)
	if !officialArenaSeriesEqual(current, normalized) {
		return invalidCanonicalMultiGameTerminalization("intermediate stage changed unrelated evidence")
	}
	return nil
}

func validateCanonicalFinalSeries(
	current domain.ArenaSeries,
	currentScore SeriesScoreRevisionHead,
	final domain.ArenaSeries,
	plan SeriesResultCascadePlan,
) error {
	result := plan.SeriesResult().Revision()
	outcome := result.Outcome()
	if result.SourceProjection().CreatedAt().Before(currentScore.SourceProjection.CreatedAt()) {
		return invalidCanonicalMultiGameTerminalization("Series result projection predates terminal score")
	}
	if final.Score != currentScore.Score || final.CurrentScoreRevisionID == nil ||
		*final.CurrentScoreRevisionID != currentScore.ID || final.State != outcome.SeriesState ||
		!uuidPointersEqual(final.WinnerID, outcome.WinnerID) ||
		final.CurrentResultRevisionID == nil || *final.CurrentResultRevisionID != result.ID() {
		return invalidCanonicalMultiGameTerminalization("final Series differs from terminal revisions")
	}
	attempts, err := seriesScoreAttemptReferencesFromSeries(final)
	if err != nil || !seriesScoreAttemptReferencesEqual(attempts, currentScore.Attempts) {
		return invalidCanonicalMultiGameTerminalization("final Series lost score provenance")
	}
	normalized := cloneRevisionArenaSeries(final)
	normalized.State = current.State
	normalized.WinnerID = cloneUUIDPointer(current.WinnerID)
	normalized.CurrentResultRevisionID = cloneOfficialResultRevisionIDPointer(current.CurrentResultRevisionID)
	if !officialArenaSeriesEqual(current, normalized) {
		return invalidCanonicalMultiGameTerminalization("final Series changed non-terminal evidence")
	}
	return nil
}

func canonicalAttemptFollows(
	previous *SeriesScoreAttemptReference,
	next *SeriesScoreAttemptReference,
) bool {
	if previous == nil || next == nil {
		return false
	}
	if next.SlotPosition != previous.SlotPosition {
		return next.SlotPosition > previous.SlotPosition
	}
	if next.AttemptNo != previous.AttemptNo {
		return next.AttemptNo > previous.AttemptNo
	}
	return bytes.Compare(previous.GameID[:], next.GameID[:]) < 0
}

func seriesScoreRevisionHeadsEqual(first, second SeriesScoreRevisionHead) bool {
	return first.Scope == second.Scope && first.ID == second.ID &&
		seriesScoreRevisionIDPointersEqual(first.PreviousRevisionID, second.PreviousRevisionID) &&
		first.Ordinal == second.Ordinal && first.Operation == second.Operation &&
		first.CommandID == second.CommandID && arenaResultActorsEqual(first.Actor, second.Actor) &&
		seriesScoreAttemptReferencePointersEqual(first.CommandAttempt, second.CommandAttempt) &&
		first.FirstParticipantID == second.FirstParticipantID &&
		first.SecondParticipantID == second.SecondParticipantID && first.Format == second.Format &&
		first.Score == second.Score && seriesScoreAttemptReferencesEqual(first.Attempts, second.Attempts) &&
		arenaDerivedRevisionsEqual(first.SourceProjection, second.SourceProjection) &&
		first.RecordedAt.Equal(second.RecordedAt)
}

func seriesScoreAttemptReferencePointersEqual(
	first *SeriesScoreAttemptReference,
	second *SeriesScoreAttemptReference,
) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return seriesScoreAttemptReferenceEqual(*first, *second)
}

func validateCanonicalInputIdentityUniqueness(
	command CanonicalMultiGameTerminalizationCommand,
	authority CanonicalMultiGameTerminalizationAuthority,
	steps []canonicalGameStep,
) error {
	collector := newCanonicalIdentityCollector()
	initialHead := steps[0].authority.ScoreRevision.CurrentHead
	if initialHead == nil {
		return invalidCanonicalMultiGameTerminalization("missing initial score head identity")
	}
	collector.reserveInitialEvidence(steps[0].authority.GameResult.PersistedSeries, *initialHead)
	if err := collector.add(command.CommandID, "aggregate command", arenaPlannerRoleCommand); err != nil {
		return err
	}
	if err := collector.roles.addSeries(steps[0].authority.GameResult.PersistedSeries); err != nil {
		return err
	}
	if err := collector.roles.addSeries(authority.SeriesResult.SeriesResult.ProjectedSeries); err != nil {
		return err
	}
	for index, step := range steps {
		if err := collector.addGameStep(index, step); err != nil {
			return err
		}
	}
	return collector.addSeriesTail(command.SeriesResult, authority.SeriesResult.CurrentScore)
}

func canonicalInputSourceProjections(
	command CanonicalMultiGameTerminalizationCommand,
	authority CanonicalMultiGameTerminalizationAuthority,
	steps []canonicalGameStep,
) []domain.ArenaDerivedRevision {
	sources := make([]domain.ArenaDerivedRevision, 0, len(steps)*3+2)
	for _, step := range steps {
		sources = append(
			sources,
			step.command.GameResult.ExpectedSourceProjection,
			step.command.ScoreRevision.ExpectedSourceProjection,
		)
		if step.authority.ScoreRevision.CurrentHead != nil {
			sources = append(sources, step.authority.ScoreRevision.CurrentHead.SourceProjection)
		}
	}
	sources = append(
		sources,
		command.SeriesResult.SeriesResult.ExpectedSourceProjection,
		authority.SeriesResult.CurrentScore.SourceProjection,
	)
	return sources
}

type canonicalIdentityCollector struct {
	roles  *arenaPlannerUUIDRegistry
	unique map[uuid.UUID]string
}

func newCanonicalIdentityCollector() *canonicalIdentityCollector {
	return &canonicalIdentityCollector{
		roles:  newArenaPlannerUUIDRegistry(invalidCanonicalMultiGameTerminalization),
		unique: make(map[uuid.UUID]string),
	}
}

func (c *canonicalIdentityCollector) reserve(id uuid.UUID, owner string) {
	if id == uuid.Nil {
		return
	}
	if _, exists := c.unique[id]; !exists {
		c.unique[id] = owner
	}
}

func (c *canonicalIdentityCollector) reserveInitialEvidence(
	series domain.ArenaSeries,
	head SeriesScoreRevisionHead,
) {
	c.reserve(head.ID.UUID(), "initial score revision")
	if head.PreviousRevisionID != nil {
		c.reserve(head.PreviousRevisionID.UUID(), "initial score predecessor")
	}
	c.reserve(head.CommandID, "initial score command")
	c.reserve(head.SourceProjection.ID().UUID(), "initial score source")
	if previousSourceID := head.SourceProjection.PreviousRevisionID(); previousSourceID != nil {
		c.reserve(previousSourceID.UUID(), "initial score source predecessor")
	}
	if head.CommandAttempt != nil {
		c.reserve(
			head.CommandAttempt.CurrentGameResultRevisionID.UUID(),
			"initial score command result provenance",
		)
	}
	for _, attempt := range head.Attempts {
		c.reserve(attempt.CurrentGameResultRevisionID.UUID(), "initial score result provenance")
	}
	if series.CurrentResultRevisionID != nil {
		c.reserve(series.CurrentResultRevisionID.UUID(), "initial Series result")
	}
	for _, slot := range series.Slots {
		for _, game := range slot.Attempts {
			if game.ResultRevisionID != nil {
				c.reserve(game.ResultRevisionID.UUID(), "initial Game result")
			}
		}
	}
}

func (c *canonicalIdentityCollector) add(
	id uuid.UUID,
	owner string,
	role arenaPlannerUUIDRole,
) error {
	if id == uuid.Nil {
		return invalidCanonicalMultiGameTerminalization("missing " + owner)
	}
	if existing, exists := c.unique[id]; exists {
		return invalidCanonicalMultiGameTerminalization(existing + " reuses " + owner)
	}
	c.unique[id] = owner
	return c.roles.add(id, role)
}

func (c *canonicalIdentityCollector) addSource(
	source domain.ArenaDerivedRevision,
	owner string,
) error {
	if err := c.add(source.ID().UUID(), owner, arenaPlannerRoleSource); err != nil {
		return err
	}
	return c.roles.addSource(source)
}

func (c *canonicalIdentityCollector) addGameStep(index int, step canonicalGameStep) error {
	values := []struct {
		id    uuid.UUID
		owner string
		role  arenaPlannerUUIDRole
	}{
		{step.command.GameResult.CommandID, fmt.Sprintf("Game command %d", index), arenaPlannerRoleCommand},
		{step.command.AuditEventID, fmt.Sprintf("Game audit %d", index), arenaPlannerUUIDRole("audit")},
		{step.command.GameResult.RevisionID.UUID(), fmt.Sprintf("Game result %d", index), arenaPlannerRoleOfficial},
		{step.command.ScoreRevision.RevisionID.UUID(), fmt.Sprintf("score revision %d", index), arenaPlannerRoleScore},
	}
	for _, value := range values {
		if err := c.add(value.id, value.owner, value.role); err != nil {
			return err
		}
	}
	if err := c.roles.addActor(step.command.GameResult.Actor); err != nil {
		return err
	}
	if err := c.addSource(
		step.command.GameResult.ExpectedSourceProjection,
		fmt.Sprintf("Game source %d", index),
	); err != nil {
		return err
	}
	if err := c.addSource(
		step.command.ScoreRevision.ExpectedSourceProjection,
		fmt.Sprintf("score source %d", index),
	); err != nil {
		return err
	}
	if step.authority.ScoreRevision.CurrentHead == nil {
		return nil
	}
	return addCascadeScoreHeadUUIDRoles(c.roles, *step.authority.ScoreRevision.CurrentHead)
}

func (c *canonicalIdentityCollector) addSeriesTail(
	tail SeriesResultCascadeCommand,
	currentScore SeriesScoreRevisionHead,
) error {
	for _, value := range []struct {
		id    uuid.UUID
		owner string
		role  arenaPlannerUUIDRole
	}{
		{tail.SeriesResult.CommandID, "Series command", arenaPlannerRoleCommand},
		{tail.AuditEventID, "Series audit", arenaPlannerUUIDRole("audit")},
		{tail.SeriesResult.RevisionID.UUID(), "Series result", arenaPlannerRoleOfficial},
	} {
		if err := c.add(value.id, value.owner, value.role); err != nil {
			return err
		}
	}
	if err := c.addSource(tail.SeriesResult.ExpectedSourceProjection, "Series source"); err != nil {
		return err
	}
	if err := addCascadeScoreHeadUUIDRoles(c.roles, currentScore); err != nil {
		return err
	}
	return c.roles.addActor(tail.SeriesResult.Actor)
}

func validateCanonicalPlanIdentityUniqueness(p CanonicalMultiGameTerminalizationPlan) error {
	commands := make([]GameResultCascadeCommand, len(p.games))
	steps := make([]canonicalGameStep, len(p.games))
	for index := range p.games {
		game := p.games[index]
		commands[index] = GameResultCascadeCommand{
			AuditEventID: game.audit.EventID,
			GameResult: OfficialResultRevisionCommand{
				Scope:                    game.gameResult.revision.scope,
				CommandID:                game.gameResult.revision.commandID,
				RevisionID:               game.gameResult.revision.id,
				Actor:                    game.gameResult.revision.actor,
				ExpectedSourceProjection: game.gameResult.revision.sourceProjection,
			},
			ScoreRevision: SeriesScoreRevisionCommand{
				Scope:                    game.scoreRevision.revision.scope,
				RevisionID:               game.scoreRevision.revision.id,
				Actor:                    game.scoreRevision.revision.actor,
				ExpectedSourceProjection: game.scoreRevision.revision.sourceProjection,
			},
		}
		commands[index].ScoreRevision.CommandID = commands[index].GameResult.CommandID
		steps[index].command = commands[index]
		steps[index].authority.GameResult.PersistedSeries = cloneRevisionArenaSeries(
			p.condition.expectedSeries,
		)
		currentHead := game.condition.expectedScoreHead.Clone()
		steps[index].authority.ScoreRevision.CurrentHead = &currentHead
	}
	tail := p.seriesResult
	authority := CanonicalMultiGameTerminalizationAuthority{
		SeriesResult: SeriesResultCascadeAuthority{
			SeriesResult: OfficialResultRevisionAuthority{
				ProjectedSeries: cloneRevisionArenaSeries(p.finalSeries),
			},
			CurrentScore: p.seriesResult.condition.expectedScoreHead.Clone(),
		},
	}
	return validateCanonicalInputIdentityUniqueness(
		CanonicalMultiGameTerminalizationCommand{
			CommandID: p.condition.commandID,
			Games:     commands,
			SeriesResult: SeriesResultCascadeCommand{
				AuditEventID: tail.audit.EventID,
				SeriesResult: OfficialResultRevisionCommand{
					Scope:                    tail.result.revision.scope,
					CommandID:                tail.result.revision.commandID,
					RevisionID:               tail.result.revision.id,
					Actor:                    tail.result.revision.actor,
					ExpectedSourceProjection: tail.result.revision.sourceProjection,
				},
			},
		},
		authority,
		steps,
	)
}

func canonicalMultiGameIdempotencyKey(
	commandID uuid.UUID,
	recordedAt time.Time,
	games []GameResultCascadePlan,
	series SeriesResultCascadePlan,
) [sha256.Size]byte {
	hash := sha256.New()
	_, _ = hash.Write(commandID[:])
	_, _ = hash.Write([]byte(recordedAt.Format(time.RFC3339Nano)))
	for index := range games {
		key := games[index].Condition().IdempotencyKey()
		_, _ = hash.Write(key[:])
	}
	tailKey := series.Condition().IdempotencyKey()
	_, _ = hash.Write(tailKey[:])
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result
}

func cloneCanonicalMultiGameCondition(
	condition CanonicalMultiGameTerminalizationCondition,
) CanonicalMultiGameTerminalizationCondition {
	clone := condition
	clone.expectedSeries = cloneRevisionArenaSeries(condition.expectedSeries)
	clone.expectedScoreHead = condition.expectedScoreHead.Clone()
	clone.expectedGameAttempts = append(
		[]CanonicalGameAttemptCondition(nil),
		condition.expectedGameAttempts...,
	)
	return clone
}

func cloneGameResultCascadePlan(plan GameResultCascadePlan) GameResultCascadePlan {
	return GameResultCascadePlan{
		condition:     cloneGameResultCascadeCondition(plan.condition),
		gameResult:    plan.GameResult(),
		scoreRevision: plan.ScoreRevision(),
		dependency:    plan.dependency,
		audit:         plan.audit.Clone(),
	}
}

func cloneSeriesResultCascadePlan(plan SeriesResultCascadePlan) SeriesResultCascadePlan {
	return SeriesResultCascadePlan{
		condition:  cloneSeriesResultCascadeCondition(plan.condition),
		result:     plan.SeriesResult(),
		dependency: plan.dependency,
		audit:      plan.audit.Clone(),
	}
}

func cloneCanonicalMultiGameCommand(
	command CanonicalMultiGameTerminalizationCommand,
) CanonicalMultiGameTerminalizationCommand {
	clone := command
	clone.Games = make([]GameResultCascadeCommand, len(command.Games))
	for index := range command.Games {
		clone.Games[index] = cloneGameResultCascadeCommand(command.Games[index])
	}
	clone.SeriesResult = cloneSeriesResultCascadeCommand(command.SeriesResult)
	return clone
}

func cloneCanonicalMultiGameAuthority(
	authority CanonicalMultiGameTerminalizationAuthority,
) CanonicalMultiGameTerminalizationAuthority {
	clone := authority
	clone.Games = make([]GameResultCascadeAuthority, len(authority.Games))
	for index := range authority.Games {
		clone.Games[index] = cloneGameResultCascadeAuthority(authority.Games[index])
	}
	clone.SeriesResult = cloneSeriesResultCascadeAuthority(authority.SeriesResult)
	return clone
}

func invalidCanonicalMultiGameTerminalization(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidCanonicalMultiGameTerminalization, message)
}

func canonicalMultiGameTerminalizationConflict(message string) error {
	return fmt.Errorf("%w: %s", ErrCanonicalMultiGameTerminalizationConflict, message)
}
