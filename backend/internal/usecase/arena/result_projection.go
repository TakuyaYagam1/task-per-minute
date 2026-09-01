package arena

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var ErrInvalidOfficialResultProjection = errors.New("invalid official Arena result projection")

type OfficialResultStatus string

const (
	OfficialResultStatusSolved     OfficialResultStatus = "solved"
	OfficialResultStatusCompleted  OfficialResultStatus = "completed"
	OfficialResultStatusNoShow     OfficialResultStatus = "no_show"
	OfficialResultStatusVoid       OfficialResultStatus = "void"
	OfficialResultStatusCancelled  OfficialResultStatus = "cancelled"
	OfficialResultStatusSuperseded OfficialResultStatus = "superseded"
)

type OfficialResultCause string

const OfficialResultCauseNoShow OfficialResultCause = "no_show"

type PublicOfficialResult struct {
	Subject      OfficialResultSubjectKind `json:"subject"`
	TournamentID uuid.UUID                 `json:"tournament_id"`
	SeriesID     uuid.UUID                 `json:"series_id"`
	GameID       *uuid.UUID                `json:"game_id"`
	Status       OfficialResultStatus      `json:"status"`
	WinnerID     *uuid.UUID                `json:"winner_id"`
	Score        *domain.ArenaSeriesScore  `json:"score"`
	ResolvedAt   time.Time                 `json:"resolved_at"`
}

type OperatorOfficialResult struct {
	Public                     PublicOfficialResult                  `json:"public"`
	ResultRevisionID           domain.ArenaOfficialResultRevisionID  `json:"result_revision_id"`
	PreviousResultRevisionID   *domain.ArenaOfficialResultRevisionID `json:"previous_result_revision_id"`
	ScoreRevisionID            *domain.ArenaSeriesScoreRevisionID    `json:"score_revision_id"`
	SourceProjectionRevisionID domain.ArenaDerivedRevisionID         `json:"source_projection_revision_id"`
	ScoreProjectionRevisionID  *domain.ArenaDerivedRevisionID        `json:"score_projection_revision_id"`
	GameState                  *domain.ArenaGameState                `json:"game_state"`
	GameReason                 *domain.ArenaGameResultReason         `json:"game_reason"`
	SeriesState                *domain.ArenaSeriesState              `json:"series_state"`
	SeriesReason               *ArenaSeriesResultReason              `json:"series_reason"`
	Cause                      *OfficialResultCause                  `json:"cause"`
	CommandID                  uuid.UUID                             `json:"command_id"`
	NoShowAction               *NormalNoShowAction                   `json:"no_show_action"`
}

type RecordedNoGameResult struct {
	Scope                NormalNoShowScope
	CommandID            uuid.UUID
	Action               NormalNoShowAction
	Format               domain.ArenaSeriesFormat
	FirstParticipantID   uuid.UUID
	SecondParticipantID  uuid.UUID
	ReadyParticipantID   *uuid.UUID
	GameResults          []NormalNoShowGameRevision
	Topology             []RecordedNoGameAttempt
	Score                NormalNoShowScoreRevision
	Series               NormalNoShowSeriesRevision
	GameSourceRevisions  []domain.ArenaDerivedRevision
	ScoreSourceRevision  domain.ArenaDerivedRevision
	ResultSourceRevision domain.ArenaDerivedRevision
	GameProjections      []domain.ArenaProjectionRevision
	GameDependencies     []domain.ArenaRevisionDependency
	ScoreProjection      domain.ArenaProjectionRevision
	ResultProjection     domain.ArenaProjectionRevision
	ResultDependency     domain.ArenaRevisionDependency
	ResolvedAt           time.Time
}

type RecordedNoGameAttempt struct {
	SeriesID         uuid.UUID
	SlotID           uuid.UUID
	SlotPosition     int
	GameID           uuid.UUID
	AttemptNo        int
	ResultRevisionID domain.ArenaOfficialResultRevisionID
}

type OfficialResultProjectionInput struct {
	Result           OfficialResultRevisionHead
	ResultProjection domain.ArenaProjectionRevision
	Score            *SeriesScoreRevisionHead
	ScoreProjection  *domain.ArenaProjectionRevision
	NoGame           *RecordedNoGameResult
}

type OfficialResultProjectionPlan struct {
	input    OfficialResultProjectionInput
	public   PublicOfficialResult
	operator OperatorOfficialResult
}

func ProjectOfficialResult(input OfficialResultProjectionInput) (OfficialResultProjectionPlan, error) {
	if input.NoGame != nil && !ordinaryOfficialResultInputIsZero(input) {
		return OfficialResultProjectionPlan{}, invalidOfficialResultProjection("mixed projection evidence")
	}
	cloned, err := cloneOfficialResultProjectionInput(input)
	if err != nil {
		return OfficialResultProjectionPlan{}, invalidOfficialResultProjection("clone evidence: %v", err)
	}
	public, operator, err := deriveOfficialResultProjection(cloned)
	if err != nil {
		return OfficialResultProjectionPlan{}, err
	}
	plan := OfficialResultProjectionPlan{input: cloned, public: public, operator: operator}
	if err := plan.Validate(); err != nil {
		return OfficialResultProjectionPlan{}, err
	}
	return plan, nil
}

func (p OfficialResultProjectionPlan) Validate() error {
	public, operator, err := deriveOfficialResultProjection(p.input)
	if err != nil {
		return err
	}
	if !publicOfficialResultsEqual(public, p.public) || !operatorOfficialResultsEqual(operator, p.operator) {
		return invalidOfficialResultProjection("projection plan was spliced")
	}
	return nil
}

func (p OfficialResultProjectionPlan) Public() PublicOfficialResult {
	return clonePublicOfficialResult(p.public)
}

func (p OfficialResultProjectionPlan) Operator() OperatorOfficialResult {
	return cloneOperatorOfficialResult(p.operator)
}

func deriveOfficialResultProjection(
	input OfficialResultProjectionInput,
) (PublicOfficialResult, OperatorOfficialResult, error) {
	if input.NoGame != nil {
		if !ordinaryOfficialResultInputIsZero(input) {
			return PublicOfficialResult{}, OperatorOfficialResult{}, invalidOfficialResultProjection("mixed projection evidence")
		}
		return deriveRecordedNoGameProjection(*input.NoGame)
	}
	return deriveRevisionHeadProjection(input)
}

func ordinaryOfficialResultInputIsZero(input OfficialResultProjectionInput) bool {
	result := input.Result
	return result.Scope == (OfficialResultScope{}) && result.ID.IsZero() &&
		result.PreviousRevisionID == nil && result.Ordinal == 0 && result.CommandID == uuid.Nil &&
		result.Actor == (ArenaResultActor{}) && result.Outcome == (OfficialResultOutcome{}) &&
		result.SourceProjection.ID().IsZero() && result.RecordedAt.IsZero() &&
		input.Score == nil && input.ScoreProjection == nil &&
		input.ResultProjection.Revision().ID().IsZero() && len(input.ResultProjection.Payload()) == 0
}

func deriveRevisionHeadProjection(
	input OfficialResultProjectionInput,
) (PublicOfficialResult, OperatorOfficialResult, error) {
	if input.Result.Ordinal == math.MaxInt || input.Result.Validate() != nil {
		return PublicOfficialResult{}, OperatorOfficialResult{}, invalidOfficialResultProjection("invalid current result head")
	}
	if err := validateExactProjection(input.Result.SourceProjection, input.ResultProjection); err != nil {
		return PublicOfficialResult{}, OperatorOfficialResult{}, err
	}

	scope := input.Result.Scope
	outcome := input.Result.Outcome
	public := PublicOfficialResult{
		Subject: scope.Kind, TournamentID: scope.TournamentID, SeriesID: scope.SeriesID,
		WinnerID: cloneUUIDPointer(outcome.WinnerID), ResolvedAt: input.Result.RecordedAt,
	}
	operator := OperatorOfficialResult{
		ResultRevisionID:           input.Result.ID,
		PreviousResultRevisionID:   cloneOfficialResultRevisionIDPointer(input.Result.PreviousRevisionID),
		SourceProjectionRevisionID: input.Result.SourceProjection.ID(),
		CommandID:                  input.Result.CommandID,
	}

	switch scope.Kind {
	case OfficialResultSubjectGame:
		if input.Score != nil || input.ScoreProjection != nil {
			return PublicOfficialResult{}, OperatorOfficialResult{}, invalidOfficialResultProjection("Game projection has Series score evidence")
		}
		status, err := projectGameResultStatus(outcome.GameState, outcome.GameReason)
		if err != nil {
			return PublicOfficialResult{}, OperatorOfficialResult{}, err
		}
		public.GameID = cloneUUIDPointer(&scope.GameID)
		public.Status = status
		operator.GameState = cloneArenaGameStatePointer(&outcome.GameState)
		operator.GameReason = cloneArenaGameReasonPointer(&outcome.GameReason)
	case OfficialResultSubjectSeries:
		if input.Score == nil || input.ScoreProjection == nil {
			return PublicOfficialResult{}, OperatorOfficialResult{}, invalidOfficialResultProjection("Series projection is missing current score evidence")
		}
		if err := validateSeriesProjectionBinding(input.Result, *input.Score, *input.ScoreProjection); err != nil {
			return PublicOfficialResult{}, OperatorOfficialResult{}, err
		}
		public.Status = OfficialResultStatusCompleted
		if outcome.SeriesState == domain.ArenaSeriesStateCancelled {
			public.Status = OfficialResultStatusVoid
		}
		score := input.Score.Score
		public.Score = &score
		operator.ScoreRevisionID = cloneSeriesScoreRevisionIDPointer(&input.Score.ID)
		scoreSourceID := input.Score.SourceProjection.ID()
		operator.ScoreProjectionRevisionID = &scoreSourceID
		operator.SeriesState = cloneArenaSeriesStatePointer(&outcome.SeriesState)
		operator.SeriesReason = cloneArenaSeriesReasonPointer(&outcome.SeriesReason)
	default:
		return PublicOfficialResult{}, OperatorOfficialResult{}, invalidOfficialResultProjection("unknown result subject")
	}
	operator.Public = clonePublicOfficialResult(public)
	return public, operator, nil
}

func validateSeriesProjectionBinding(
	result OfficialResultRevisionHead,
	score SeriesScoreRevisionHead,
	scoreProjection domain.ArenaProjectionRevision,
) error {
	if score.Ordinal == math.MaxInt || score.Validate() != nil {
		return invalidOfficialResultProjection("invalid current score head")
	}
	if err := validateExactProjection(score.SourceProjection, scoreProjection); err != nil {
		return err
	}
	if score.Scope != (SeriesScoreRevisionScope{
		TournamentID: result.Scope.TournamentID,
		SeriesID:     result.Scope.SeriesID,
	}) || result.Outcome.ScoreRevisionID == nil || *result.Outcome.ScoreRevisionID != score.ID {
		return invalidOfficialResultProjection("Series result and score heads do not match")
	}
	if result.RecordedAt.Before(score.RecordedAt) ||
		result.SourceProjection.CreatedAt().Before(score.SourceProjection.CreatedAt()) {
		return invalidOfficialResultProjection("Series result predates its current score")
	}
	if result.Outcome.SeriesState == domain.ArenaSeriesStateCompleted {
		winner := score.Score.Winner(score.FirstParticipantID, score.SecondParticipantID, score.Format)
		if winner == nil || result.Outcome.WinnerID == nil || *winner != *result.Outcome.WinnerID {
			return invalidOfficialResultProjection("Series winner does not match current score")
		}
	}
	return nil
}

func validateExactProjection(
	want domain.ArenaDerivedRevision,
	projection domain.ArenaProjectionRevision,
) error {
	if projection.Validate() != nil || !arenaDerivedRevisionsEqual(want, projection.Revision()) {
		return invalidOfficialResultProjection("projection payload does not match current revision")
	}
	return nil
}

func projectGameResultStatus(
	state domain.ArenaGameState,
	reason domain.ArenaGameResultReason,
) (OfficialResultStatus, error) {
	switch state {
	case domain.ArenaGameStateCompleted:
		if reason == domain.ArenaGameResultReasonSolved {
			return OfficialResultStatusSolved, nil
		}
		return OfficialResultStatusCompleted, nil
	case domain.ArenaGameStateVoid:
		return OfficialResultStatusVoid, nil
	case domain.ArenaGameStateCancelled:
		return OfficialResultStatusCancelled, nil
	case domain.ArenaGameStateSuperseded:
		return OfficialResultStatusSuperseded, nil
	case domain.ArenaGameStatePlanned, domain.ArenaGameStateReady,
		domain.ArenaGameStateActive, domain.ArenaGameStatePaused:
		return "", invalidOfficialResultProjection("non-terminal Game status")
	default:
		return "", invalidOfficialResultProjection("unknown Game status")
	}
}

func deriveRecordedNoGameProjection(
	recorded RecordedNoGameResult,
) (PublicOfficialResult, OperatorOfficialResult, error) {
	if err := validateRecordedNoGameResult(recorded); err != nil {
		return PublicOfficialResult{}, OperatorOfficialResult{}, err
	}
	public := PublicOfficialResult{
		Subject:      OfficialResultSubjectSeries,
		TournamentID: recorded.Scope.TournamentID,
		SeriesID:     recorded.Scope.SeriesID,
		WinnerID:     cloneUUIDPointer(recorded.Series.WinnerID),
		Score:        cloneArenaSeriesScorePointer(&recorded.Score.Score),
		ResolvedAt:   recorded.ResolvedAt,
	}
	cause := OfficialResultCauseNoShow
	if recorded.Action == NormalNoShowActionReopenWave {
		public.Status = OfficialResultStatusNoShow
	} else {
		public.Status = OfficialResultStatusVoid
	}
	seriesState := recorded.Series.State
	resultSourceID := recorded.ResultProjection.Revision().ID()
	scoreSourceID := recorded.ScoreProjection.Revision().ID()
	action := recorded.Action
	operator := OperatorOfficialResult{
		Public:                     clonePublicOfficialResult(public),
		ResultRevisionID:           recorded.Series.ID,
		PreviousResultRevisionID:   cloneOfficialResultRevisionIDPointer(recorded.Series.PreviousRevisionID),
		ScoreRevisionID:            cloneSeriesScoreRevisionIDPointer(&recorded.Score.ID),
		SourceProjectionRevisionID: resultSourceID,
		ScoreProjectionRevisionID:  &scoreSourceID,
		SeriesState:                &seriesState,
		Cause:                      &cause,
		CommandID:                  recorded.CommandID,
		NoShowAction:               &action,
	}
	return public, operator, nil
}

//nolint:gocyclo // This is one fail-closed audit boundary for the recorded no-show aggregate.
func validateRecordedNoGameResult(recorded RecordedNoGameResult) error {
	gameCount := len(recorded.GameResults)
	if !validNormalNoShowScope(recorded.Scope) || recorded.CommandID == uuid.Nil ||
		!recorded.Format.IsValid() || recorded.FirstParticipantID == uuid.Nil ||
		recorded.SecondParticipantID == uuid.Nil || recorded.FirstParticipantID == recorded.SecondParticipantID ||
		!validArenaServerTime(recorded.ResolvedAt) || gameCount == 0 || gameCount > 16 {
		return invalidOfficialResultProjection("invalid no-show revision bounds")
	}
	firstOrdinal := recorded.GameResults[0].Ordinal
	if firstOrdinal < 1 || firstOrdinal > math.MaxInt-gameCount-1 ||
		recorded.Score.Ordinal != firstOrdinal+gameCount ||
		recorded.Series.Ordinal != firstOrdinal+gameCount+1 {
		return invalidOfficialResultProjection("invalid no-show revision ordinals")
	}
	if len(recorded.Topology) != gameCount || len(recorded.GameSourceRevisions) != gameCount ||
		len(recorded.GameProjections) != gameCount || len(recorded.GameDependencies) != gameCount ||
		len(recorded.Score.GameResultRevisionIDs) != gameCount {
		return invalidOfficialResultProjection("no-show Game evidence is incomplete")
	}
	for index, game := range recorded.GameResults {
		binding := recorded.Topology[index]
		if game.Ordinal != firstOrdinal+index || game.ID.IsZero() || game.GameID == uuid.Nil ||
			game.State != domain.ArenaGameStateCancelled || game.Reason != domain.ArenaGameResultReasonSeriesCancelled ||
			!validArenaServerTime(game.RecordedAt) || !game.RecordedAt.Equal(recorded.ResolvedAt) ||
			recorded.Score.GameResultRevisionIDs[index] != game.ID || binding.SeriesID != recorded.Scope.SeriesID ||
			binding.SlotID == uuid.Nil || binding.SlotPosition < 1 || binding.GameID != game.GameID ||
			binding.AttemptNo < 1 || binding.ResultRevisionID != game.ID || game.PreviousRevisionID != nil ||
			(index > 0 && binding.SlotPosition <= recorded.Topology[index-1].SlotPosition) {
			return invalidOfficialResultProjection("invalid no-show Game revision provenance")
		}
	}
	if recorded.Score.ID.IsZero() || recorded.Score.SeriesID != recorded.Scope.SeriesID ||
		recorded.Score.Score.Validate(recorded.Format) != nil ||
		!validArenaServerTime(recorded.Score.RecordedAt) || !recorded.Score.RecordedAt.Equal(recorded.ResolvedAt) ||
		recorded.Series.ID.IsZero() ||
		recorded.Series.SeriesID != recorded.Scope.SeriesID ||
		recorded.Series.ScoreRevisionID != recorded.Score.ID ||
		!validArenaServerTime(recorded.Series.RecordedAt) || !recorded.Series.RecordedAt.Equal(recorded.ResolvedAt) {
		return invalidOfficialResultProjection("invalid no-show revision timestamp")
	}
	if err := validateRecordedNoGameIdentities(recorded); err != nil {
		return err
	}
	for index, projection := range recorded.GameProjections {
		revision := projection.Revision()
		game := recorded.GameResults[index]
		if validateExactProjection(recorded.GameSourceRevisions[index], projection) != nil ||
			revision.TournamentID() != recorded.Scope.TournamentID ||
			revision.Artifact() != (domain.ArenaArtifactRef{Kind: domain.ArenaArtifactKindGameResult, EntityID: game.GameID}) ||
			revision.CreatedAt().After(game.RecordedAt) || recorded.GameDependencies[index] != (domain.ArenaRevisionDependency{
			SourceRevisionID: revision.ID(), DerivedRevisionID: recorded.ScoreProjection.Revision().ID(),
		}) {
			return invalidOfficialResultProjection("invalid no-show Game projection")
		}
	}
	if err := validateExactProjection(recorded.ScoreSourceRevision, recorded.ScoreProjection); err != nil {
		return invalidOfficialResultProjection("invalid recorded no-show score source: %v", err)
	}
	if err := validateNoGameProjectionNode(
		recorded.ScoreProjection, recorded.Scope.TournamentID,
		domain.ArenaArtifactKindSeriesScore, recorded.Scope.SeriesID, recorded.ResolvedAt,
	); err != nil {
		return err
	}
	if err := validateExactProjection(recorded.ResultSourceRevision, recorded.ResultProjection); err != nil {
		return invalidOfficialResultProjection("invalid recorded no-show result source: %v", err)
	}
	if err := validateNoGameProjectionNode(
		recorded.ResultProjection, recorded.Scope.TournamentID,
		domain.ArenaArtifactKindSeriesResult, recorded.Scope.SeriesID, recorded.ResolvedAt,
	); err != nil {
		return err
	}
	if recorded.ResultProjection.Revision().CreatedAt().Before(recorded.ScoreProjection.Revision().CreatedAt()) ||
		recorded.ResultDependency != (domain.ArenaRevisionDependency{
			SourceRevisionID:  recorded.ScoreProjection.Revision().ID(),
			DerivedRevisionID: recorded.ResultProjection.Revision().ID(),
		}) {
		return invalidOfficialResultProjection("no-show result projection predates score")
	}
	return validateNoGameOutcome(recorded)
}

func validateNoGameProjectionNode(
	projection domain.ArenaProjectionRevision,
	tournamentID uuid.UUID,
	kind domain.ArenaArtifactKind,
	entityID uuid.UUID,
	recordedAt time.Time,
) error {
	revision := projection.Revision()
	if projection.Validate() != nil || revision.TournamentID() != tournamentID ||
		revision.Artifact() != (domain.ArenaArtifactRef{Kind: kind, EntityID: entityID}) ||
		revision.CreatedAt().After(recordedAt) {
		return invalidOfficialResultProjection("invalid no-show projection provenance")
	}
	return nil
}

func seriesScoreIDAsUUID(value *domain.ArenaSeriesScoreRevisionID) *uuid.UUID {
	if value == nil {
		return nil
	}
	id := value.UUID()
	return &id
}

func officialResultIDAsUUID(value *domain.ArenaOfficialResultRevisionID) *uuid.UUID {
	if value == nil {
		return nil
	}
	id := value.UUID()
	return &id
}

func validateNoGameOutcome(recorded RecordedNoGameResult) error {
	switch recorded.Action {
	case NormalNoShowActionReopenWave:
		winner := recorded.Score.Score.Winner(
			recorded.FirstParticipantID, recorded.SecondParticipantID, recorded.Format,
		)
		if recorded.ReadyParticipantID == nil ||
			(*recorded.ReadyParticipantID != recorded.FirstParticipantID && *recorded.ReadyParticipantID != recorded.SecondParticipantID) ||
			recorded.Series.State != domain.ArenaSeriesStateCompleted || winner == nil ||
			recorded.Series.WinnerID == nil || *winner != *recorded.Series.WinnerID ||
			*winner != *recorded.ReadyParticipantID {
			return invalidOfficialResultProjection("no-show winner does not match forced score")
		}
	case NormalNoShowActionPauseWave:
		if recorded.ReadyParticipantID != nil || recorded.Series.State != domain.ArenaSeriesStateCancelled ||
			recorded.Series.WinnerID != nil || recorded.Score.Score.Winner(
			recorded.FirstParticipantID, recorded.SecondParticipantID, recorded.Format,
		) != nil {
			return invalidOfficialResultProjection("cancelled no-show has a winner")
		}
	default:
		return invalidOfficialResultProjection("unknown no-show action")
	}
	return nil
}

//nolint:gocyclo // Identity ownership is intentionally checked in one registry pass.
func validateRecordedNoGameIdentities(recorded RecordedNoGameResult) error {
	seen := make(map[uuid.UUID]string, 12+len(recorded.GameResults)*2)
	add := func(id uuid.UUID, owner string) error {
		if id == uuid.Nil {
			return invalidOfficialResultProjection("missing %s identity", owner)
		}
		if previous, exists := seen[id]; exists {
			return invalidOfficialResultProjection("identity aliases %s and %s", previous, owner)
		}
		seen[id] = owner
		return nil
	}
	identities := []struct {
		id    uuid.UUID
		owner string
	}{
		{recorded.Scope.TournamentID, "tournament"},
		{recorded.Scope.WaveID, "Wave"},
		{recorded.Scope.WindowID, "window"},
		{recorded.Scope.SeriesID, "Series"},
		{recorded.CommandID, "command"},
		{recorded.FirstParticipantID, "first participant"},
		{recorded.SecondParticipantID, "second participant"},
		{recorded.Score.ID.UUID(), "score revision"},
		{recorded.Series.ID.UUID(), "Series result revision"},
		{recorded.ScoreProjection.Revision().ID().UUID(), "score projection"},
		{recorded.ResultProjection.Revision().ID().UUID(), "Series result projection"},
	}
	for _, identity := range identities {
		if err := add(identity.id, identity.owner); err != nil {
			return err
		}
	}
	for index, game := range recorded.GameResults {
		if err := add(game.ID.UUID(), fmt.Sprintf("Game result revision %d", index)); err != nil {
			return err
		}
		if err := add(game.GameID, fmt.Sprintf("Game %d", index)); err != nil {
			return err
		}
		if err := add(recorded.GameProjections[index].Revision().ID().UUID(), fmt.Sprintf("Game projection %d", index)); err != nil {
			return err
		}
		if err := add(recorded.Topology[index].SlotID, fmt.Sprintf("Game slot %d", index)); err != nil {
			return err
		}
	}
	predecessors := []struct {
		id      *uuid.UUID
		current uuid.UUID
		owner   string
	}{
		{seriesScoreIDAsUUID(recorded.Score.PreviousRevisionID), recorded.Score.ID.UUID(), "score predecessor"},
		{officialResultIDAsUUID(recorded.Series.PreviousRevisionID), recorded.Series.ID.UUID(), "Series result predecessor"},
	}
	for index := range recorded.GameResults {
		predecessors = append(predecessors, struct {
			id      *uuid.UUID
			current uuid.UUID
			owner   string
		}{officialResultIDAsUUID(recorded.GameResults[index].PreviousRevisionID), recorded.GameResults[index].ID.UUID(), fmt.Sprintf("Game predecessor %d", index)})
	}
	for _, predecessor := range predecessors {
		if predecessor.id == nil {
			continue
		}
		if *predecessor.id == uuid.Nil || *predecessor.id == predecessor.current {
			return invalidOfficialResultProjection("invalid %s", predecessor.owner)
		}
		if err := add(*predecessor.id, predecessor.owner); err != nil {
			return err
		}
	}
	return nil
}

func cloneOfficialResultProjectionInput(
	input OfficialResultProjectionInput,
) (OfficialResultProjectionInput, error) {
	clone := input
	clone.Result = input.Result.Clone()
	var err error
	clone.ResultProjection, err = cloneOfficialProjectionRevision(input.ResultProjection)
	if input.NoGame == nil && err != nil {
		return OfficialResultProjectionInput{}, err
	}
	if input.Score != nil {
		score := input.Score.Clone()
		clone.Score = &score
	}
	if input.ScoreProjection != nil {
		projection, projectionErr := cloneOfficialProjectionRevision(*input.ScoreProjection)
		if projectionErr != nil {
			return OfficialResultProjectionInput{}, projectionErr
		}
		clone.ScoreProjection = &projection
	}
	if input.NoGame != nil {
		noGame, noGameErr := cloneRecordedNoGameResult(*input.NoGame)
		if noGameErr != nil {
			return OfficialResultProjectionInput{}, noGameErr
		}
		clone.NoGame = &noGame
	}
	return clone, nil
}

func cloneRecordedNoGameResult(recorded RecordedNoGameResult) (RecordedNoGameResult, error) {
	clone := recorded
	clone.GameResults = append([]NormalNoShowGameRevision(nil), recorded.GameResults...)
	clone.Topology = append([]RecordedNoGameAttempt(nil), recorded.Topology...)
	clone.ReadyParticipantID = cloneUUIDPointer(recorded.ReadyParticipantID)
	for index := range clone.GameResults {
		clone.GameResults[index].PreviousRevisionID = cloneOfficialResultRevisionIDPointer(recorded.GameResults[index].PreviousRevisionID)
	}
	clone.Score.PreviousRevisionID = cloneSeriesScoreRevisionIDPointer(recorded.Score.PreviousRevisionID)
	clone.Score.GameResultRevisionIDs = append([]domain.ArenaOfficialResultRevisionID(nil), recorded.Score.GameResultRevisionIDs...)
	clone.Series.PreviousRevisionID = cloneOfficialResultRevisionIDPointer(recorded.Series.PreviousRevisionID)
	clone.Series.WinnerID = cloneUUIDPointer(recorded.Series.WinnerID)
	clone.GameSourceRevisions = append([]domain.ArenaDerivedRevision(nil), recorded.GameSourceRevisions...)
	clone.GameDependencies = append([]domain.ArenaRevisionDependency(nil), recorded.GameDependencies...)
	clone.GameProjections = make([]domain.ArenaProjectionRevision, len(recorded.GameProjections))
	for index := range recorded.GameProjections {
		projection, err := cloneOfficialProjectionRevision(recorded.GameProjections[index])
		if err != nil {
			return RecordedNoGameResult{}, err
		}
		clone.GameProjections[index] = projection
	}
	var err error
	clone.ScoreProjection, err = cloneOfficialProjectionRevision(recorded.ScoreProjection)
	if err != nil {
		return RecordedNoGameResult{}, err
	}
	clone.ResultProjection, err = cloneOfficialProjectionRevision(recorded.ResultProjection)
	if err != nil {
		return RecordedNoGameResult{}, err
	}
	return clone, nil
}

func cloneOfficialProjectionRevision(
	projection domain.ArenaProjectionRevision,
) (domain.ArenaProjectionRevision, error) {
	revision := projection.Revision()
	return domain.NewArenaProjectionRevision(
		revision.ID(), revision.TournamentID(), revision.Artifact(), revision.RevisionNo(),
		revision.PreviousRevisionID(), revision.CreatedAt(), projection.Payload(),
	)
}

func clonePublicOfficialResult(value PublicOfficialResult) PublicOfficialResult {
	clone := value
	clone.GameID = cloneUUIDPointer(value.GameID)
	clone.WinnerID = cloneUUIDPointer(value.WinnerID)
	clone.Score = cloneArenaSeriesScorePointer(value.Score)
	return clone
}

func cloneOperatorOfficialResult(value OperatorOfficialResult) OperatorOfficialResult {
	clone := value
	clone.Public = clonePublicOfficialResult(value.Public)
	clone.PreviousResultRevisionID = cloneOfficialResultRevisionIDPointer(value.PreviousResultRevisionID)
	clone.ScoreRevisionID = cloneSeriesScoreRevisionIDPointer(value.ScoreRevisionID)
	clone.ScoreProjectionRevisionID = cloneArenaDerivedRevisionIDPointer(value.ScoreProjectionRevisionID)
	clone.GameState = cloneArenaGameStatePointer(value.GameState)
	clone.GameReason = cloneArenaGameReasonPointer(value.GameReason)
	clone.SeriesState = cloneArenaSeriesStatePointer(value.SeriesState)
	clone.SeriesReason = cloneArenaSeriesReasonPointer(value.SeriesReason)
	clone.Cause = cloneOfficialResultCausePointer(value.Cause)
	clone.NoShowAction = cloneNoShowActionPointer(value.NoShowAction)
	return clone
}

func cloneArenaSeriesScorePointer(value *domain.ArenaSeriesScore) *domain.ArenaSeriesScore {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneArenaDerivedRevisionIDPointer(value *domain.ArenaDerivedRevisionID) *domain.ArenaDerivedRevisionID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneArenaGameStatePointer(value *domain.ArenaGameState) *domain.ArenaGameState {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneArenaGameReasonPointer(value *domain.ArenaGameResultReason) *domain.ArenaGameResultReason {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneArenaSeriesStatePointer(value *domain.ArenaSeriesState) *domain.ArenaSeriesState {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneArenaSeriesReasonPointer(value *ArenaSeriesResultReason) *ArenaSeriesResultReason {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneOfficialResultCausePointer(value *OfficialResultCause) *OfficialResultCause {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneNoShowActionPointer(value *NormalNoShowAction) *NormalNoShowAction {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func publicOfficialResultsEqual(first, second PublicOfficialResult) bool {
	return first.Subject == second.Subject && first.TournamentID == second.TournamentID &&
		first.SeriesID == second.SeriesID && uuidPointersEqual(first.GameID, second.GameID) &&
		first.Status == second.Status && uuidPointersEqual(first.WinnerID, second.WinnerID) &&
		arenaSeriesScorePointersEqual(first.Score, second.Score) && first.ResolvedAt.Equal(second.ResolvedAt)
}

func operatorOfficialResultsEqual(first, second OperatorOfficialResult) bool {
	return publicOfficialResultsEqual(first.Public, second.Public) &&
		first.ResultRevisionID == second.ResultRevisionID &&
		officialResultRevisionIDPointersEqual(first.PreviousResultRevisionID, second.PreviousResultRevisionID) &&
		seriesScoreRevisionPointersEqual(first.ScoreRevisionID, second.ScoreRevisionID) &&
		first.SourceProjectionRevisionID == second.SourceProjectionRevisionID &&
		arenaDerivedRevisionIDPointersEqual(first.ScoreProjectionRevisionID, second.ScoreProjectionRevisionID) &&
		arenaGameStatePointersEqual(first.GameState, second.GameState) &&
		arenaGameReasonPointersEqual(first.GameReason, second.GameReason) &&
		arenaSeriesStatePointersEqual(first.SeriesState, second.SeriesState) &&
		arenaSeriesReasonPointersEqual(first.SeriesReason, second.SeriesReason) &&
		officialResultCausePointersEqual(first.Cause, second.Cause) &&
		first.CommandID == second.CommandID && noShowActionPointersEqual(first.NoShowAction, second.NoShowAction)
}

func arenaSeriesScorePointersEqual(first, second *domain.ArenaSeriesScore) bool {
	return (first == nil && second == nil) || (first != nil && second != nil && *first == *second)
}

func arenaGameStatePointersEqual(first, second *domain.ArenaGameState) bool {
	return (first == nil && second == nil) || (first != nil && second != nil && *first == *second)
}

func arenaGameReasonPointersEqual(first, second *domain.ArenaGameResultReason) bool {
	return (first == nil && second == nil) || (first != nil && second != nil && *first == *second)
}

func arenaSeriesStatePointersEqual(first, second *domain.ArenaSeriesState) bool {
	return (first == nil && second == nil) || (first != nil && second != nil && *first == *second)
}

func arenaSeriesReasonPointersEqual(first, second *ArenaSeriesResultReason) bool {
	return (first == nil && second == nil) || (first != nil && second != nil && *first == *second)
}

func officialResultCausePointersEqual(first, second *OfficialResultCause) bool {
	return (first == nil && second == nil) || (first != nil && second != nil && *first == *second)
}

func noShowActionPointersEqual(first, second *NormalNoShowAction) bool {
	return (first == nil && second == nil) || (first != nil && second != nil && *first == *second)
}

func invalidOfficialResultProjection(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidOfficialResultProjection, fmt.Sprintf(format, arguments...))
}
