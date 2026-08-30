package arena

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const (
	forfeitAttempts       = 2
	maxForfeitReasonBytes = 256
	maxForfeitRuleIDBytes = 64
	maxForfeitEvidenceIDs = 16
)

var (
	ErrInvalidForfeit              = errors.New("invalid Arena forfeit")
	ErrForfeitUnavailable          = errors.New("arena forfeit is unavailable")
	ErrSurrenderDisconnected       = errors.New("surrendering participant is disconnected")
	ErrOperatorForfeitUnauthorized = errors.New("operator is not authorized to record a forfeit")
	ErrForfeitAuthorityConflict    = errors.New("arena forfeit authority conflict")
	ErrForfeitCommandReuse         = errors.New("arena forfeit command was reused")
)

type ForfeitSource string

const (
	ForfeitSourceSurrender ForfeitSource = "surrender"
	ForfeitSourceOperator  ForfeitSource = "operator"
)

type OperatorForfeitBasis string

const OperatorForfeitBasisRuleViolation OperatorForfeitBasis = "rule_violation"

type ForfeitScope struct {
	TournamentID uuid.UUID
	SeriesID     uuid.UUID
}

type ForfeitGameExpectation struct {
	SlotID    uuid.UUID
	GameID    uuid.UUID
	AttemptNo int
	State     domain.ArenaGameState
}

type ForfeitRevisionSet struct {
	GameResultRevisionID   *domain.ArenaOfficialResultRevisionID
	ScoreRevisionID        domain.ArenaSeriesScoreRevisionID
	SeriesResultRevisionID domain.ArenaOfficialResultRevisionID
	AuditEventID           uuid.UUID
	OutboxEventID          uuid.UUID
	ProjectionRevisionID   uuid.UUID
}

type OperatorForfeitEvidence struct {
	Confirmed   bool
	Basis       OperatorForfeitBasis
	Reason      string
	RuleID      string
	EvidenceIDs []uuid.UUID
}

type SurrenderCommand struct {
	Scope                   ForfeitScope
	CommandID               uuid.UUID
	ActorParticipantID      uuid.UUID
	ForfeitingParticipantID uuid.UUID
	ExpectedGame            ForfeitGameExpectation
	Revisions               ForfeitRevisionSet
}

type OperatorForfeitCommand struct {
	Scope                   ForfeitScope
	CommandID               uuid.UUID
	ActorOperatorID         uuid.UUID
	ForfeitingParticipantID uuid.UUID
	ExpectedGame            *ForfeitGameExpectation
	Evidence                OperatorForfeitEvidence
	Revisions               ForfeitRevisionSet
}

type ForfeitAuthority struct {
	Scope                        ForfeitScope
	Revision                     int64
	Series                       SeriesExecution
	ConnectedParticipantIDs      []uuid.UUID
	AuthorizedOperatorIDs        []uuid.UUID
	CurrentOrdinal               int
	CurrentProjectionRevision    int64
	CurrentGameResultRevisionIDs []domain.ArenaOfficialResultRevisionID
	Current                      *ForfeitResolution
}

type ForfeitGameRevision struct {
	Ordinal    int
	ID         domain.ArenaOfficialResultRevisionID
	GameID     uuid.UUID
	WinnerID   uuid.UUID
	Reason     domain.ArenaGameResultReason
	RecordedAt time.Time
}

type ForfeitSeriesRevision struct {
	Ordinal            int
	ID                 domain.ArenaOfficialResultRevisionID
	SeriesID           uuid.UUID
	PreviousRevisionID *domain.ArenaOfficialResultRevisionID
	State              domain.ArenaSeriesState
	WinnerID           *uuid.UUID
	ScoreRevisionID    domain.ArenaSeriesScoreRevisionID
	Reason             domain.ArenaGameResultReason
	RecordedAt         time.Time
}

type ForfeitResolution struct {
	Source                    ForfeitSource
	Reason                    domain.ArenaGameResultReason
	Scope                     ForfeitScope
	CommandID                 uuid.UUID
	ActorID                   uuid.UUID
	ForfeitingParticipantID   uuid.UUID
	ExpectedGame              *ForfeitGameExpectation
	ExpectedAuthorityRevision int64
	Series                    SeriesExecution
	Game                      *domain.ArenaGame
	GameRevision              *ForfeitGameRevision
	ScoreRevision             ArenaSettlementScoreRevision
	SeriesRevision            ForfeitSeriesRevision
	OperatorEvidence          *OperatorForfeitEvidence
	Evidence                  ArenaSettlementEvidence
	ResolvedAt                time.Time
}

// ForfeitRepository owns one transaction that revalidates the actor, current
// connectivity or operator authorization, Series and Game heads, then commits
// the Game, score, Series, audit, outbox and projection evidence together.
type ForfeitRepository interface {
	LoadForfeitAuthority(ctx context.Context, scope ForfeitScope) (ForfeitAuthority, error)
	CommitForfeitResolution(
		ctx context.Context,
		resolution ForfeitResolution,
	) (*ForfeitResolution, bool, error)
}

type ForfeitUseCase struct {
	repository ForfeitRepository
	clock      Clock
}

type forfeitRequest struct {
	source                  ForfeitSource
	scope                   ForfeitScope
	commandID               uuid.UUID
	actorID                 uuid.UUID
	forfeitingParticipantID uuid.UUID
	expectedGame            *ForfeitGameExpectation
	operatorEvidence        *OperatorForfeitEvidence
	revisions               ForfeitRevisionSet
}

func NewForfeitUseCase(repository ForfeitRepository, clock Clock) *ForfeitUseCase {
	return &ForfeitUseCase{repository: repository, clock: clock}
}

func (u *ForfeitUseCase) Surrender(
	ctx context.Context,
	command SurrenderCommand,
) (*ForfeitResolution, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil {
		return nil, false, domain.ErrValidation
	}
	if command.ActorParticipantID != command.ForfeitingParticipantID ||
		command.ActorParticipantID == uuid.Nil {
		return nil, false, domain.ErrArenaAssignmentParticipant
	}
	request := forfeitRequest{
		source: ForfeitSourceSurrender, scope: command.Scope, commandID: command.CommandID,
		actorID: command.ActorParticipantID, forfeitingParticipantID: command.ForfeitingParticipantID,
		expectedGame: &command.ExpectedGame, revisions: command.Revisions,
	}
	if err := validateForfeitRequest(request); err != nil {
		return nil, false, err
	}
	return u.resolve(ctx, request)
}

func (u *ForfeitUseCase) OperatorForfeit(
	ctx context.Context,
	command OperatorForfeitCommand,
) (*ForfeitResolution, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil {
		return nil, false, domain.ErrValidation
	}
	evidence := cloneOperatorForfeitEvidence(command.Evidence)
	request := forfeitRequest{
		source: ForfeitSourceOperator, scope: command.Scope, commandID: command.CommandID,
		actorID: command.ActorOperatorID, forfeitingParticipantID: command.ForfeitingParticipantID,
		expectedGame:     cloneForfeitGameExpectation(command.ExpectedGame),
		operatorEvidence: &evidence, revisions: command.Revisions,
	}
	if err := validateForfeitRequest(request); err != nil {
		return nil, false, err
	}
	return u.resolve(ctx, request)
}

func (u *ForfeitUseCase) resolve(
	ctx context.Context,
	request forfeitRequest,
) (*ForfeitResolution, bool, error) {
	resolvedAt := u.clock.Now().Round(0).UTC()
	if !validArenaServerTime(resolvedAt) {
		return nil, false, domain.ErrValidation
	}
	for range forfeitAttempts {
		resolution, changed, retry, err := u.resolveAttempt(ctx, request, resolvedAt)
		if retry {
			continue
		}
		return resolution, changed, err
	}
	return nil, false, ErrForfeitAuthorityConflict
}

func (u *ForfeitUseCase) resolveAttempt(
	ctx context.Context,
	request forfeitRequest,
	resolvedAt time.Time,
) (*ForfeitResolution, bool, bool, error) {
	authority, err := u.repository.LoadForfeitAuthority(ctx, request.scope)
	if err != nil {
		return nil, false, false, fmt.Errorf("ForfeitUseCase - load authority: %w", err)
	}
	if err := validateForfeitAuthority(authority); err != nil {
		return nil, false, false, err
	}
	if authority.Scope != request.scope {
		return nil, false, false, forfeitError("authority scope does not match command")
	}
	if authority.Current != nil {
		current, reconcileErr := reconcileForfeitResolution(*authority.Current, request)
		return current, false, false, reconcileErr
	}
	if err := validateForfeitAuthorityForRequest(authority, request); err != nil {
		return nil, false, false, err
	}
	resolution, err := buildForfeitResolution(authority, request, resolvedAt)
	if err != nil {
		return nil, false, false, err
	}
	committed, changed, err := u.repository.CommitForfeitResolution(ctx, resolution)
	if errors.Is(err, domain.ErrConflict) {
		return nil, false, true, nil
	}
	if err != nil {
		return nil, false, false, fmt.Errorf("ForfeitUseCase - commit resolution: %w", err)
	}
	if !validCommittedForfeitResolution(committed, resolution, changed) {
		return nil, false, false, domain.ErrInternal
	}
	result := cloneForfeitResolution(*committed)
	return &result, changed, false, nil
}

func (r ForfeitResolution) Validate() error {
	if err := validateForfeitResolutionIdentity(r); err != nil {
		return err
	}
	if err := validateForfeitResolutionSeries(r); err != nil {
		return err
	}
	if !forfeitParticipantsMatch(r) {
		return forfeitError("forfeit winner or loser does not match Series")
	}
	if err := validateForfeitGameEvidence(r); err != nil {
		return err
	}
	if err := validateForfeitScoreRevision(r); err != nil {
		return err
	}
	if err := validateForfeitSeriesRevision(r); err != nil {
		return err
	}
	if err := validateForfeitOperatorEvidence(r); err != nil {
		return err
	}
	return validateForfeitSettlementEvidence(r)
}

func validateForfeitResolutionIdentity(resolution ForfeitResolution) error {
	if !resolution.Source.IsValid() || !resolution.Scope.IsValid() ||
		resolution.CommandID == uuid.Nil || resolution.ActorID == uuid.Nil ||
		resolution.ForfeitingParticipantID == uuid.Nil ||
		resolution.ExpectedAuthorityRevision < 1 || !validArenaServerTime(resolution.ResolvedAt) ||
		resolution.Reason != resolution.Source.ResultReason() {
		return forfeitError("invalid resolution identity or timestamp")
	}
	if resolution.ExpectedGame != nil && !resolution.ExpectedGame.IsValid() {
		return forfeitError("invalid expected Game binding")
	}
	return nil
}

func validateForfeitResolutionSeries(resolution ForfeitResolution) error {
	if err := resolution.Series.Validate(); err != nil ||
		resolution.Series.Series.ID != resolution.Scope.SeriesID ||
		resolution.Series.Series.TournamentID != resolution.Scope.TournamentID {
		return forfeitError("invalid resolved Series")
	}
	if err := ValidateCompetitiveSeriesResolution(
		resolution.Source.SeriesRoute(),
		resolution.Series,
	); err != nil {
		return forfeitError("Series resolution: %v", err)
	}
	return nil
}

func (s ForfeitSource) IsValid() bool {
	return s == ForfeitSourceSurrender || s == ForfeitSourceOperator
}

func (s ForfeitSource) ResultReason() domain.ArenaGameResultReason {
	if s == ForfeitSourceSurrender {
		return domain.ArenaGameResultReasonSurrender
	}
	if s == ForfeitSourceOperator {
		return domain.ArenaGameResultReasonOperatorForfeit
	}
	return ""
}

func (s ForfeitSource) SeriesRoute() CompetitiveSeriesRoute {
	if s == ForfeitSourceSurrender {
		return CompetitiveSeriesRouteSurrender
	}
	if s == ForfeitSourceOperator {
		return CompetitiveSeriesRouteOperatorForfeit
	}
	return ""
}

func (s ForfeitScope) IsValid() bool {
	return s.TournamentID != uuid.Nil && s.SeriesID != uuid.Nil
}

func validateForfeitRequest(request forfeitRequest) error {
	if !request.source.IsValid() || !request.scope.IsValid() || request.commandID == uuid.Nil ||
		request.actorID == uuid.Nil || request.forfeitingParticipantID == uuid.Nil {
		return forfeitError("invalid command identity")
	}
	if request.expectedGame != nil && !request.expectedGame.IsValid() {
		return forfeitError("invalid expected Game")
	}
	if request.source == ForfeitSourceSurrender {
		if request.actorID != request.forfeitingParticipantID || request.expectedGame == nil ||
			request.operatorEvidence != nil {
			return forfeitError("invalid surrender actor or Game")
		}
	} else if request.operatorEvidence == nil || request.operatorEvidence.Validate() != nil {
		return forfeitError("invalid operator evidence")
	}
	return validateForfeitRevisionSet(request.commandID, request.revisions)
}

func (e ForfeitGameExpectation) IsValid() bool {
	return e.SlotID != uuid.Nil && e.GameID != uuid.Nil && e.AttemptNo > 0 && e.State.IsValid()
}

func (e OperatorForfeitEvidence) Validate() error {
	if !e.Confirmed || e.Basis != OperatorForfeitBasisRuleViolation ||
		!validForfeitText(e.Reason, maxForfeitReasonBytes) ||
		!validForfeitRuleID(e.RuleID) || len(e.EvidenceIDs) == 0 ||
		len(e.EvidenceIDs) > maxForfeitEvidenceIDs {
		return forfeitError("operator evidence is incomplete")
	}
	seen := make(map[uuid.UUID]struct{}, len(e.EvidenceIDs))
	for _, evidenceID := range e.EvidenceIDs {
		if evidenceID == uuid.Nil {
			return forfeitError("operator evidence has an empty identity")
		}
		if _, duplicate := seen[evidenceID]; duplicate {
			return forfeitError("operator evidence has a duplicate identity")
		}
		seen[evidenceID] = struct{}{}
	}
	return nil
}

func validateForfeitRevisionSet(commandID uuid.UUID, revisions ForfeitRevisionSet) error {
	identities := []uuid.UUID{
		commandID,
		revisions.ScoreRevisionID.UUID(),
		revisions.SeriesResultRevisionID.UUID(),
		revisions.AuditEventID,
		revisions.OutboxEventID,
		revisions.ProjectionRevisionID,
	}
	if revisions.GameResultRevisionID != nil {
		identities = append(identities, revisions.GameResultRevisionID.UUID())
	}
	seen := make(map[uuid.UUID]struct{}, len(identities))
	for _, identity := range identities {
		if identity == uuid.Nil {
			return forfeitError("missing revision or evidence identity")
		}
		if _, duplicate := seen[identity]; duplicate {
			return forfeitError("duplicate revision or evidence identity")
		}
		seen[identity] = struct{}{}
	}
	return nil
}

func validateForfeitAuthority(authority ForfeitAuthority) error {
	if !validForfeitAuthorityHeader(authority) {
		return forfeitError("invalid authority identity or Series")
	}
	if err := validateForfeitAuthorityActors(authority); err != nil {
		return err
	}
	if err := validateCurrentGameResultRevisionIDs(authority.CurrentGameResultRevisionIDs); err != nil {
		return forfeitError("invalid retained Game revisions: %v", err)
	}
	if authority.CurrentOrdinal > 0 && authority.Series.Series.CurrentScoreRevisionID == nil {
		return forfeitError("revision ordinal has no current score revision")
	}
	if authority.Current != nil {
		if authority.Current.Validate() != nil || authority.Current.Scope != authority.Scope ||
			!forfeitSeriesHeadsEqual(authority.Current.Series, authority.Series) {
			return forfeitError("invalid current resolution")
		}
	}
	return nil
}

func validForfeitAuthorityHeader(authority ForfeitAuthority) bool {
	return authority.Scope.IsValid() && authority.Revision >= 1 && authority.CurrentOrdinal >= 0 &&
		authority.CurrentProjectionRevision >= 1 && authority.Series.Validate() == nil &&
		authority.Series.Series.ID == authority.Scope.SeriesID &&
		authority.Series.Series.TournamentID == authority.Scope.TournamentID
}

func validateForfeitAuthorityActors(authority ForfeitAuthority) error {
	connected := make(map[uuid.UUID]struct{}, len(authority.ConnectedParticipantIDs))
	for _, participantID := range authority.ConnectedParticipantIDs {
		if !arenaSeriesParticipant(authority.Series.Series, participantID) {
			return forfeitError("connected actor is not a Series participant")
		}
		if _, duplicate := connected[participantID]; duplicate {
			return forfeitError("duplicate connected participant")
		}
		connected[participantID] = struct{}{}
	}
	operators := make(map[uuid.UUID]struct{}, len(authority.AuthorizedOperatorIDs))
	for _, operatorID := range authority.AuthorizedOperatorIDs {
		if operatorID == uuid.Nil {
			return forfeitError("empty authorized operator identity")
		}
		if _, duplicate := operators[operatorID]; duplicate {
			return forfeitError("duplicate authorized operator")
		}
		operators[operatorID] = struct{}{}
	}
	return nil
}

func validateForfeitAuthorityForRequest(
	authority ForfeitAuthority,
	request forfeitRequest,
) error {
	series := authority.Series.Series
	if series.State.IsTerminal() || series.WinnerID != nil || series.CurrentResultRevisionID != nil ||
		series.Score.Winner(series.FirstParticipantID, series.SecondParticipantID, series.Format) != nil ||
		!arenaSeriesParticipant(series, request.forfeitingParticipantID) {
		return ErrForfeitUnavailable
	}
	if request.source == ForfeitSourceSurrender {
		return validateSurrenderAuthority(authority, request)
	}
	return validateOperatorForfeitAuthority(authority, request)
}

func validateSurrenderAuthority(authority ForfeitAuthority, request forfeitRequest) error {
	if request.actorID != request.forfeitingParticipantID {
		return domain.ErrArenaAssignmentParticipant
	}
	if !slices.Contains(authority.ConnectedParticipantIDs, request.actorID) {
		return ErrSurrenderDisconnected
	}
	if !liveForfeitSeriesState(authority.Series) || request.expectedGame == nil ||
		request.revisions.GameResultRevisionID == nil {
		return ErrForfeitUnavailable
	}
	game, _, found := currentForfeitGame(authority.Series.Series)
	if !found || !liveForfeitGameState(game.State) ||
		!forfeitGameMatchesExpectation(game, *request.expectedGame) {
		return ErrForfeitAuthorityConflict
	}
	return nil
}

func validateOperatorForfeitAuthority(authority ForfeitAuthority, request forfeitRequest) error {
	if !slices.Contains(authority.AuthorizedOperatorIDs, request.actorID) {
		return ErrOperatorForfeitUnauthorized
	}
	if request.operatorEvidence == nil || request.operatorEvidence.Validate() != nil {
		return forfeitError("invalid operator evidence")
	}
	series := authority.Series
	game, _, hasGame := currentForfeitGame(series.Series)
	if series.Series.State == domain.ArenaSeriesStateReady {
		return validatePreStartOperatorForfeit(game, hasGame, request)
	}
	if !liveForfeitSeriesState(series) || request.expectedGame == nil ||
		request.revisions.GameResultRevisionID == nil || !hasGame ||
		!liveForfeitGameState(game.State) || !forfeitGameMatchesExpectation(game, *request.expectedGame) {
		return ErrForfeitUnavailable
	}
	return nil
}

func validatePreStartOperatorForfeit(
	game domain.ArenaGame,
	hasGame bool,
	request forfeitRequest,
) error {
	if request.revisions.GameResultRevisionID != nil {
		return forfeitError("pre-start forfeit cannot invent a Game result")
	}
	if !hasGame {
		if request.expectedGame != nil {
			return ErrForfeitAuthorityConflict
		}
		return nil
	}
	if request.expectedGame == nil ||
		(game.State != domain.ArenaGameStatePlanned && game.State != domain.ArenaGameStateReady) ||
		!forfeitGameMatchesExpectation(game, *request.expectedGame) {
		return ErrForfeitAuthorityConflict
	}
	return nil
}

func buildForfeitResolution(
	authority ForfeitAuthority,
	request forfeitRequest,
	resolvedAt time.Time,
) (ForfeitResolution, error) {
	winnerID, ok := opposingArenaSeriesParticipant(
		authority.Series.Series,
		request.forfeitingParticipantID,
	)
	if !ok {
		return ForfeitResolution{}, ErrForfeitUnavailable
	}
	working := cloneSeriesExecution(authority.Series)
	reason := request.source.ResultReason()
	game, gameRevision, err := applyForfeitGame(
		&working,
		request,
		winnerID,
		reason,
		authority.CurrentOrdinal+1,
		resolvedAt,
	)
	if err != nil {
		return ForfeitResolution{}, err
	}

	scoreBefore := working.Series.Score
	scoreAfter := forfeitWinningScore(working.Series, winnerID)
	scoreRevisionID := request.revisions.ScoreRevisionID
	seriesResultRevisionID := request.revisions.SeriesResultRevisionID
	terminal, changed, err := ResolveCompetitiveSeries(working, CompetitiveSeriesResolutionCommand{
		Route: request.source.SeriesRoute(),
		Terminal: &SeriesTerminalEvidence{
			Score: scoreAfter, WinnerID: &winnerID,
			ScoreRevisionID: &scoreRevisionID, ResultRevisionID: &seriesResultRevisionID,
		},
	})
	if err != nil || !changed {
		return ForfeitResolution{}, forfeitError("complete Series: %v", err)
	}
	gameResultRevisionIDs := append(
		[]domain.ArenaOfficialResultRevisionID(nil),
		authority.CurrentGameResultRevisionIDs...,
	)
	gameRevisionCount := 0
	if gameRevision != nil {
		gameResultRevisionIDs = append(gameResultRevisionIDs, gameRevision.ID)
		gameRevisionCount = 1
	}
	scoreOrdinal := authority.CurrentOrdinal + gameRevisionCount + 1
	resolution := ForfeitResolution{
		Source: request.source, Reason: reason, Scope: request.scope,
		CommandID: request.commandID, ActorID: request.actorID,
		ForfeitingParticipantID:   request.forfeitingParticipantID,
		ExpectedGame:              cloneForfeitGameExpectation(request.expectedGame),
		ExpectedAuthorityRevision: authority.Revision,
		Series:                    terminal, Game: game, GameRevision: gameRevision,
		ScoreRevision: ArenaSettlementScoreRevision{
			ID: request.revisions.ScoreRevisionID, SeriesID: request.scope.SeriesID,
			FirstParticipantID:  authority.Series.Series.FirstParticipantID,
			SecondParticipantID: authority.Series.Series.SecondParticipantID,
			PreviousRevisionID: cloneSeriesScoreRevisionIDPointer(
				authority.Series.Series.CurrentScoreRevisionID,
			),
			Ordinal: scoreOrdinal, Format: authority.Series.Series.Format,
			ScoreBefore: scoreBefore, ScoreAfter: scoreAfter,
			GameResultRevisionIDs: gameResultRevisionIDs, RecordedAt: resolvedAt,
		},
		SeriesRevision: ForfeitSeriesRevision{
			Ordinal: scoreOrdinal + 1, ID: request.revisions.SeriesResultRevisionID,
			SeriesID: request.scope.SeriesID,
			PreviousRevisionID: cloneOfficialResultRevisionIDPointer(
				authority.Series.Series.CurrentResultRevisionID,
			),
			State: domain.ArenaSeriesStateCompleted, WinnerID: &winnerID,
			ScoreRevisionID: request.revisions.ScoreRevisionID, Reason: reason, RecordedAt: resolvedAt,
		},
		OperatorEvidence: cloneOperatorForfeitEvidencePointer(request.operatorEvidence),
		Evidence: ArenaSettlementEvidence{
			AuditEventID:             request.revisions.AuditEventID,
			OutboxEventID:            request.revisions.OutboxEventID,
			ProjectionRevisionID:     request.revisions.ProjectionRevisionID,
			SourceProjectionRevision: authority.CurrentProjectionRevision,
			ProjectionRevision:       authority.CurrentProjectionRevision + 1,
			RecordedAt:               resolvedAt,
		},
		ResolvedAt: resolvedAt,
	}
	if err := resolution.Validate(); err != nil {
		return ForfeitResolution{}, err
	}
	return cloneForfeitResolution(resolution), nil
}

func applyForfeitGame(
	series *SeriesExecution,
	request forfeitRequest,
	winnerID uuid.UUID,
	reason domain.ArenaGameResultReason,
	ordinal int,
	resolvedAt time.Time,
) (*domain.ArenaGame, *ForfeitGameRevision, error) {
	if request.revisions.GameResultRevisionID == nil {
		return nil, nil, nil
	}
	game, slotIndex, found := currentForfeitGame(series.Series)
	if !found || request.expectedGame == nil ||
		!forfeitGameMatchesExpectation(game, *request.expectedGame) {
		return nil, nil, ErrForfeitAuthorityConflict
	}
	resultRevisionID := *request.revisions.GameResultRevisionID
	transitioned, changed, err := TransitionGameSlotAttempt(
		series.Series.Slots[slotIndex],
		GameAttemptTransitionCommand{
			GameID: game.ID, ExpectedAttemptNo: game.AttemptNo, ExpectedState: game.State,
			NextState: domain.ArenaGameStateCompleted,
			Terminal: &GameTerminalEvidence{
				Reason: reason, WinnerID: &winnerID, ResultRevisionID: &resultRevisionID,
			},
		},
	)
	if err != nil || !changed {
		return nil, nil, forfeitError("complete Game: %v", err)
	}
	series.Series.Slots[slotIndex] = transitioned
	completed := cloneArenaGame(transitioned.Attempts[len(transitioned.Attempts)-1])
	revision := ForfeitGameRevision{
		Ordinal: ordinal, ID: resultRevisionID, GameID: completed.ID,
		WinnerID: winnerID, Reason: reason, RecordedAt: resolvedAt,
	}
	return &completed, &revision, nil
}

func validateForfeitGameEvidence(resolution ForfeitResolution) error {
	if resolution.Game == nil || resolution.GameRevision == nil {
		if !validForfeitWithoutGameEvidence(resolution) {
			return forfeitError("missing or partial Game forfeit evidence")
		}
		return nil
	}
	if !validForfeitCompletedGame(resolution) || !validForfeitGameRevision(resolution) {
		return forfeitError("invalid Game forfeit evidence")
	}
	return nil
}

func validForfeitWithoutGameEvidence(resolution ForfeitResolution) bool {
	if resolution.Game != nil || resolution.GameRevision != nil ||
		resolution.Source != ForfeitSourceOperator {
		return false
	}
	game, _, found := currentForfeitGame(resolution.Series.Series)
	if !found {
		return resolution.ExpectedGame == nil
	}
	return resolution.ExpectedGame != nil &&
		(game.State == domain.ArenaGameStatePlanned || game.State == domain.ArenaGameStateReady) &&
		forfeitGameMatchesExpectation(game, *resolution.ExpectedGame)
}

func validForfeitCompletedGame(resolution ForfeitResolution) bool {
	game := resolution.Game
	revision := resolution.GameRevision
	return game != nil && revision != nil && game.Validate() == nil &&
		game.State == domain.ArenaGameStateCompleted && game.ResultReason == resolution.Reason &&
		game.WinnerID != nil && game.ResultRevisionID != nil &&
		resolution.Series.Series.WinnerID != nil && *game.WinnerID == *resolution.Series.Series.WinnerID &&
		*game.ResultRevisionID == revision.ID && *game.WinnerID == revision.WinnerID &&
		forfeitCompletedGameMatchesExpectation(*game, resolution.ExpectedGame)
}

func validForfeitGameRevision(resolution ForfeitResolution) bool {
	game := resolution.Game
	revision := resolution.GameRevision
	return game != nil && revision != nil && revision.Ordinal >= 1 && !revision.ID.IsZero() &&
		revision.GameID == game.ID && revision.Reason == resolution.Reason &&
		revision.RecordedAt.Equal(resolution.ResolvedAt)
}

func validateForfeitScoreRevision(resolution ForfeitResolution) error {
	revision := resolution.ScoreRevision
	series := resolution.Series.Series
	if !validForfeitScoreRevisionHeader(resolution, revision, series) {
		return forfeitError("invalid forfeit score revision")
	}
	if !forfeitScoreAddsWinner(revision, *series.WinnerID) {
		return forfeitError("forfeit score does not establish one winner")
	}
	if resolution.GameRevision != nil {
		if len(revision.GameResultRevisionIDs) == 0 ||
			revision.GameResultRevisionIDs[len(revision.GameResultRevisionIDs)-1] != resolution.GameRevision.ID ||
			revision.Ordinal != resolution.GameRevision.Ordinal+1 {
			return forfeitError("score revision does not follow Game evidence")
		}
	}
	return nil
}

func validForfeitScoreRevisionHeader(
	resolution ForfeitResolution,
	revision ArenaSettlementScoreRevision,
	series domain.ArenaSeries,
) bool {
	return !revision.ID.IsZero() && revision.SeriesID == series.ID && revision.Ordinal >= 1 &&
		revision.FirstParticipantID == series.FirstParticipantID &&
		revision.SecondParticipantID == series.SecondParticipantID && revision.Format == series.Format &&
		revision.ScoreBefore.Validate(revision.Format) == nil && revision.ScoreAfter == series.Score &&
		revision.RecordedAt.Equal(resolution.ResolvedAt) && series.CurrentScoreRevisionID != nil &&
		*series.CurrentScoreRevisionID == revision.ID &&
		validateCurrentGameResultRevisionIDs(revision.GameResultRevisionIDs) == nil
}

func validateForfeitSeriesRevision(resolution ForfeitResolution) error {
	revision := resolution.SeriesRevision
	series := resolution.Series.Series
	if revision.Ordinal != resolution.ScoreRevision.Ordinal+1 || revision.ID.IsZero() ||
		revision.SeriesID != series.ID || revision.State != domain.ArenaSeriesStateCompleted ||
		revision.WinnerID == nil || series.WinnerID == nil || *revision.WinnerID != *series.WinnerID ||
		revision.ScoreRevisionID != resolution.ScoreRevision.ID || revision.Reason != resolution.Reason ||
		!revision.RecordedAt.Equal(resolution.ResolvedAt) || series.CurrentResultRevisionID == nil ||
		*series.CurrentResultRevisionID != revision.ID {
		return forfeitError("invalid forfeit Series revision")
	}
	return nil
}

func validateForfeitOperatorEvidence(resolution ForfeitResolution) error {
	if resolution.Source == ForfeitSourceSurrender {
		if resolution.OperatorEvidence != nil || resolution.ActorID != resolution.ForfeitingParticipantID ||
			resolution.ExpectedGame == nil {
			return forfeitError("surrender has operator evidence or a foreign actor")
		}
		return nil
	}
	if resolution.OperatorEvidence == nil || resolution.OperatorEvidence.Validate() != nil {
		return forfeitError("operator forfeit has invalid evidence")
	}
	return nil
}

func validateForfeitSettlementEvidence(resolution ForfeitResolution) error {
	evidence := resolution.Evidence
	identities := []uuid.UUID{
		resolution.CommandID,
		resolution.ScoreRevision.ID.UUID(),
		resolution.SeriesRevision.ID.UUID(),
		evidence.AuditEventID,
		evidence.OutboxEventID,
		evidence.ProjectionRevisionID,
	}
	if resolution.GameRevision != nil {
		identities = append(identities, resolution.GameRevision.ID.UUID())
	}
	if resolution.OperatorEvidence != nil {
		identities = append(identities, resolution.OperatorEvidence.EvidenceIDs...)
	}
	seen := make(map[uuid.UUID]struct{}, len(identities))
	for _, identity := range identities {
		if identity == uuid.Nil {
			return forfeitError("missing settlement evidence")
		}
		if _, duplicate := seen[identity]; duplicate {
			return forfeitError("duplicate settlement evidence")
		}
		seen[identity] = struct{}{}
	}
	if evidence.SourceProjectionRevision < 1 ||
		evidence.ProjectionRevision != evidence.SourceProjectionRevision+1 ||
		!evidence.RecordedAt.Equal(resolution.ResolvedAt) {
		return forfeitError("invalid projection evidence")
	}
	return nil
}

func forfeitParticipantsMatch(resolution ForfeitResolution) bool {
	series := resolution.Series.Series
	if series.WinnerID == nil || !arenaSeriesParticipant(series, resolution.ForfeitingParticipantID) ||
		!arenaSeriesParticipant(series, *series.WinnerID) ||
		*series.WinnerID == resolution.ForfeitingParticipantID {
		return false
	}
	return true
}

func forfeitScoreAddsWinner(revision ArenaSettlementScoreRevision, winnerID uuid.UUID) bool {
	if revision.ScoreAfter.Winner(
		revision.FirstParticipantID,
		revision.SecondParticipantID,
		revision.Format,
	) == nil {
		return false
	}
	switch winnerID {
	case revision.FirstParticipantID:
		return revision.ScoreAfter.FirstParticipantWins == revision.Format.WinsRequired() &&
			revision.ScoreAfter.FirstParticipantWins > revision.ScoreBefore.FirstParticipantWins &&
			revision.ScoreAfter.SecondParticipantWins == revision.ScoreBefore.SecondParticipantWins
	case revision.SecondParticipantID:
		return revision.ScoreAfter.SecondParticipantWins == revision.Format.WinsRequired() &&
			revision.ScoreAfter.SecondParticipantWins > revision.ScoreBefore.SecondParticipantWins &&
			revision.ScoreAfter.FirstParticipantWins == revision.ScoreBefore.FirstParticipantWins
	default:
		return false
	}
}

func forfeitWinningScore(series domain.ArenaSeries, winnerID uuid.UUID) domain.ArenaSeriesScore {
	score := series.Score
	if winnerID == series.FirstParticipantID {
		score.FirstParticipantWins = series.Format.WinsRequired()
	} else {
		score.SecondParticipantWins = series.Format.WinsRequired()
	}
	return score
}

func liveForfeitSeriesState(series SeriesExecution) bool {
	if series.Series.State == domain.ArenaSeriesStateActive {
		return true
	}
	return series.Series.State == domain.ArenaSeriesStateTechnicalPause &&
		series.ResumeState != nil && *series.ResumeState == domain.ArenaSeriesStateActive
}

func liveForfeitGameState(state domain.ArenaGameState) bool {
	return state == domain.ArenaGameStateActive || state == domain.ArenaGameStatePaused
}

func currentForfeitGame(series domain.ArenaSeries) (domain.ArenaGame, int, bool) {
	if len(series.Slots) == 0 {
		return domain.ArenaGame{}, 0, false
	}
	slotIndex := len(series.Slots) - 1
	slot := series.Slots[slotIndex]
	if len(slot.Attempts) == 0 {
		return domain.ArenaGame{}, 0, false
	}
	return cloneArenaGame(slot.Attempts[len(slot.Attempts)-1]), slotIndex, true
}

func forfeitGameMatchesExpectation(
	game domain.ArenaGame,
	expected ForfeitGameExpectation,
) bool {
	return game.ID == expected.GameID && game.SlotID == expected.SlotID &&
		game.AttemptNo == expected.AttemptNo && game.State == expected.State
}

func arenaSeriesParticipant(series domain.ArenaSeries, participantID uuid.UUID) bool {
	return participantID == series.FirstParticipantID || participantID == series.SecondParticipantID
}

func opposingArenaSeriesParticipant(
	series domain.ArenaSeries,
	participantID uuid.UUID,
) (uuid.UUID, bool) {
	switch participantID {
	case series.FirstParticipantID:
		return series.SecondParticipantID, true
	case series.SecondParticipantID:
		return series.FirstParticipantID, true
	default:
		return uuid.Nil, false
	}
}

func reconcileForfeitResolution(
	resolution ForfeitResolution,
	request forfeitRequest,
) (*ForfeitResolution, error) {
	if resolution.Validate() != nil {
		return nil, domain.ErrInternal
	}
	if resolution.CommandID != request.commandID {
		return nil, ErrForfeitAuthorityConflict
	}
	if resolution.Source != request.source || resolution.Scope != request.scope ||
		resolution.ActorID != request.actorID ||
		resolution.ForfeitingParticipantID != request.forfeitingParticipantID ||
		resolution.ScoreRevision.ID != request.revisions.ScoreRevisionID ||
		resolution.SeriesRevision.ID != request.revisions.SeriesResultRevisionID ||
		resolution.Evidence.AuditEventID != request.revisions.AuditEventID ||
		resolution.Evidence.OutboxEventID != request.revisions.OutboxEventID ||
		resolution.Evidence.ProjectionRevisionID != request.revisions.ProjectionRevisionID ||
		!forfeitGameExpectationsEqual(resolution.ExpectedGame, request.expectedGame) ||
		!officialResultRevisionPointersEqual(
			forfeitGameRevisionIDPointer(resolution.GameRevision),
			request.revisions.GameResultRevisionID,
		) || !operatorForfeitEvidencePointersEqual(resolution.OperatorEvidence, request.operatorEvidence) {
		return nil, ErrForfeitCommandReuse
	}
	clone := cloneForfeitResolution(resolution)
	return &clone, nil
}

func validCommittedForfeitResolution(
	committed *ForfeitResolution,
	proposed ForfeitResolution,
	changed bool,
) bool {
	if committed == nil || committed.Validate() != nil || committed.Scope != proposed.Scope ||
		committed.CommandID != proposed.CommandID {
		return false
	}
	return !changed || forfeitResolutionsEqual(*committed, proposed)
}

func forfeitResolutionsEqual(first, second ForfeitResolution) bool {
	return forfeitResolutionHeadersEqual(first, second) &&
		forfeitSeriesHeadsEqual(first.Series, second.Series) &&
		forfeitGamesEqual(first.Game, second.Game) &&
		forfeitGameRevisionsEqual(first.GameRevision, second.GameRevision) &&
		arenaSettlementScoreRevisionsEqual(first.ScoreRevision, second.ScoreRevision) &&
		forfeitSeriesRevisionsEqual(first.SeriesRevision, second.SeriesRevision) &&
		operatorForfeitEvidencePointersEqual(first.OperatorEvidence, second.OperatorEvidence) &&
		first.Evidence == second.Evidence && first.ResolvedAt.Equal(second.ResolvedAt)
}

func forfeitResolutionHeadersEqual(first, second ForfeitResolution) bool {
	return first.Source == second.Source && first.Reason == second.Reason && first.Scope == second.Scope &&
		first.CommandID == second.CommandID && first.ActorID == second.ActorID &&
		first.ForfeitingParticipantID == second.ForfeitingParticipantID &&
		forfeitGameExpectationsEqual(first.ExpectedGame, second.ExpectedGame) &&
		first.ExpectedAuthorityRevision == second.ExpectedAuthorityRevision
}

func forfeitSeriesHeadsEqual(first, second SeriesExecution) bool {
	if first.ResumeState == nil || second.ResumeState == nil {
		if first.ResumeState != second.ResumeState {
			return false
		}
	} else if *first.ResumeState != *second.ResumeState {
		return false
	}
	if !arenaSeriesHeadersEqual(first.Series, second.Series) || len(first.Series.Slots) != len(second.Series.Slots) {
		return false
	}
	for index := range first.Series.Slots {
		if !arenaGameSlotsEqual(first.Series.Slots[index], second.Series.Slots[index]) {
			return false
		}
	}
	return true
}

func arenaSeriesHeadersEqual(first, second domain.ArenaSeries) bool {
	return first.ID == second.ID && first.TournamentID == second.TournamentID &&
		first.FirstParticipantID == second.FirstParticipantID &&
		first.SecondParticipantID == second.SecondParticipantID && first.Format == second.Format &&
		first.State == second.State && first.Score == second.Score &&
		uuidPointersEqual(first.WinnerID, second.WinnerID) &&
		seriesScoreRevisionPointersEqual(first.CurrentScoreRevisionID, second.CurrentScoreRevisionID) &&
		officialResultRevisionPointersEqual(first.CurrentResultRevisionID, second.CurrentResultRevisionID)
}

func arenaGameSlotsEqual(first, second domain.ArenaGameSlot) bool {
	if first.ID != second.ID || first.SeriesID != second.SeriesID || first.Position != second.Position ||
		first.Category != second.Category || first.ScoreBefore != second.ScoreBefore ||
		len(first.Attempts) != len(second.Attempts) {
		return false
	}
	for index := range first.Attempts {
		if !arenaSettlementGamesEqual(first.Attempts[index], second.Attempts[index]) {
			return false
		}
	}
	return true
}

func forfeitGamesEqual(first, second *domain.ArenaGame) bool {
	if first == nil || second == nil {
		return first == second
	}
	return arenaSettlementGamesEqual(*first, *second)
}

func forfeitGameRevisionsEqual(first, second *ForfeitGameRevision) bool {
	if first == nil || second == nil {
		return first == second
	}
	return *first == *second
}

func forfeitSeriesRevisionsEqual(first, second ForfeitSeriesRevision) bool {
	return first.Ordinal == second.Ordinal && first.ID == second.ID && first.SeriesID == second.SeriesID &&
		officialResultRevisionPointersEqual(first.PreviousRevisionID, second.PreviousRevisionID) &&
		first.State == second.State && uuidPointersEqual(first.WinnerID, second.WinnerID) &&
		first.ScoreRevisionID == second.ScoreRevisionID && first.Reason == second.Reason &&
		first.RecordedAt.Equal(second.RecordedAt)
}

func operatorForfeitEvidencePointersEqual(
	first *OperatorForfeitEvidence,
	second *OperatorForfeitEvidence,
) bool {
	if first == nil || second == nil {
		return first == second
	}
	return first.Confirmed == second.Confirmed && first.Basis == second.Basis &&
		first.Reason == second.Reason && first.RuleID == second.RuleID &&
		slices.Equal(first.EvidenceIDs, second.EvidenceIDs)
}

func forfeitGameRevisionIDPointer(
	revision *ForfeitGameRevision,
) *domain.ArenaOfficialResultRevisionID {
	if revision == nil {
		return nil
	}
	result := revision.ID
	return &result
}

func forfeitCompletedGameMatchesExpectation(
	game domain.ArenaGame,
	expected *ForfeitGameExpectation,
) bool {
	if expected == nil {
		return false
	}
	return game.ID == expected.GameID && game.SlotID == expected.SlotID &&
		game.AttemptNo == expected.AttemptNo && liveForfeitGameState(expected.State)
}

func forfeitGameExpectationsEqual(first, second *ForfeitGameExpectation) bool {
	if first == nil || second == nil {
		return first == second
	}
	return *first == *second
}

func cloneForfeitResolution(resolution ForfeitResolution) ForfeitResolution {
	clone := resolution
	clone.ExpectedGame = cloneForfeitGameExpectation(resolution.ExpectedGame)
	clone.Series = cloneSeriesExecution(resolution.Series)
	if resolution.Game != nil {
		game := cloneArenaGame(*resolution.Game)
		clone.Game = &game
	}
	if resolution.GameRevision != nil {
		gameRevision := *resolution.GameRevision
		clone.GameRevision = &gameRevision
	}
	clone.ScoreRevision = cloneArenaSettlementScoreRevision(resolution.ScoreRevision)
	clone.SeriesRevision.PreviousRevisionID = cloneOfficialResultRevisionIDPointer(
		resolution.SeriesRevision.PreviousRevisionID,
	)
	clone.SeriesRevision.WinnerID = cloneUUIDPointer(resolution.SeriesRevision.WinnerID)
	clone.OperatorEvidence = cloneOperatorForfeitEvidencePointer(resolution.OperatorEvidence)
	return clone
}

func cloneOperatorForfeitEvidence(evidence OperatorForfeitEvidence) OperatorForfeitEvidence {
	clone := evidence
	clone.EvidenceIDs = append([]uuid.UUID(nil), evidence.EvidenceIDs...)
	return clone
}

func cloneOperatorForfeitEvidencePointer(
	evidence *OperatorForfeitEvidence,
) *OperatorForfeitEvidence {
	if evidence == nil {
		return nil
	}
	clone := cloneOperatorForfeitEvidence(*evidence)
	return &clone
}

func cloneForfeitGameExpectation(
	expected *ForfeitGameExpectation,
) *ForfeitGameExpectation {
	if expected == nil {
		return nil
	}
	clone := *expected
	return &clone
}

func validForfeitText(value string, maxBytes int) bool {
	if value == "" || value != strings.TrimSpace(value) || len(value) > maxBytes || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func validForfeitRuleID(value string) bool {
	if len(value) == 0 || len(value) > maxForfeitRuleIDBytes {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '.' || character == '_' ||
			character == ':' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func forfeitError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidForfeit, fmt.Sprintf(format, arguments...))
}
