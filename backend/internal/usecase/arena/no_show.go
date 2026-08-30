package arena

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const normalNoShowAttempts = 2

var (
	ErrInvalidNormalNoShow           = errors.New("invalid normal Arena no-show resolution")
	ErrNormalNoShowCutoff            = errors.New("arena no-show cutoff has not passed")
	ErrNormalNoShowNotRequired       = errors.New("arena no-show resolution is not required")
	ErrNormalNoShowAuthorityConflict = errors.New("arena no-show authority conflict")
	ErrNormalNoShowConflict          = errors.New("arena no-show commit conflict")
)

type NormalNoShowAction string

const (
	NormalNoShowActionReopenWave NormalNoShowAction = "reopen_wave"
	NormalNoShowActionPauseWave  NormalNoShowAction = "pause_wave"
)

type NormalNoShowScope struct {
	TournamentID uuid.UUID
	WaveID       uuid.UUID
	WindowID     uuid.UUID
	SeriesID     uuid.UUID
}

type NormalNoShowAuthority struct {
	Scope          NormalNoShowScope
	Revision       int64
	Wave           domain.ArenaWave
	Series         SeriesExecution
	CurrentOrdinal int
	Current        *NormalNoShowResolution
}

type NormalNoShowCommand struct {
	Scope                    NormalNoShowScope
	CommandID                uuid.UUID
	ExpectedWaveRevisionID   domain.ArenaWaveRevisionID
	ExpectedWindowRevisionID domain.ArenaReadyWindowRevisionID
	ExpectedSeriesState      domain.ArenaSeriesState
	GameResultRevisionIDs    []domain.ArenaOfficialResultRevisionID
	ScoreRevisionID          domain.ArenaSeriesScoreRevisionID
	SeriesResultRevisionID   domain.ArenaOfficialResultRevisionID
}

type NormalNoShowGameRevision struct {
	Ordinal            int
	ID                 domain.ArenaOfficialResultRevisionID
	GameID             uuid.UUID
	PreviousRevisionID *domain.ArenaOfficialResultRevisionID
	State              domain.ArenaGameState
	Reason             domain.ArenaGameResultReason
	RecordedAt         time.Time
}

type NormalNoShowScoreRevision struct {
	Ordinal               int
	ID                    domain.ArenaSeriesScoreRevisionID
	SeriesID              uuid.UUID
	PreviousRevisionID    *domain.ArenaSeriesScoreRevisionID
	Score                 domain.ArenaSeriesScore
	GameResultRevisionIDs []domain.ArenaOfficialResultRevisionID
	RecordedAt            time.Time
}

type NormalNoShowSeriesRevision struct {
	Ordinal            int
	ID                 domain.ArenaOfficialResultRevisionID
	SeriesID           uuid.UUID
	PreviousRevisionID *domain.ArenaOfficialResultRevisionID
	State              domain.ArenaSeriesState
	WinnerID           *uuid.UUID
	ScoreRevisionID    domain.ArenaSeriesScoreRevisionID
	RecordedAt         time.Time
}

type NormalNoShowResolution struct {
	Scope                     NormalNoShowScope
	CommandID                 uuid.UUID
	ExpectedAuthorityRevision int64
	Action                    NormalNoShowAction
	Wave                      domain.ArenaWave
	Series                    SeriesExecution
	GameRevisions             []NormalNoShowGameRevision
	ScoreRevision             NormalNoShowScoreRevision
	SeriesRevision            NormalNoShowSeriesRevision
	ResolvedAt                time.Time
}

// NormalNoShowRepository owns one transaction that expires the ready window,
// terminalizes every unstarted Game, appends ordered result and score evidence,
// and commits the Series outcome without exposing an intermediate state.
type NormalNoShowRepository interface {
	LoadNormalNoShowAuthority(ctx context.Context, scope NormalNoShowScope) (NormalNoShowAuthority, error)
	CommitNormalNoShow(
		ctx context.Context,
		resolution NormalNoShowResolution,
	) (*NormalNoShowResolution, bool, error)
}

type NormalNoShowUseCase struct {
	repository NormalNoShowRepository
	clock      Clock
}

func NewNormalNoShowUseCase(
	repository NormalNoShowRepository,
	clock Clock,
) *NormalNoShowUseCase {
	return &NormalNoShowUseCase{repository: repository, clock: clock}
}

func (u *NormalNoShowUseCase) Resolve(
	ctx context.Context,
	command NormalNoShowCommand,
) (*NormalNoShowResolution, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil {
		return nil, false, domain.ErrValidation
	}
	if err := validateNormalNoShowCommand(command); err != nil {
		return nil, false, err
	}
	resolvedAt := u.clock.Now().Round(0).UTC()
	if !validArenaServerTime(resolvedAt) {
		return nil, false, domain.ErrValidation
	}

	for range normalNoShowAttempts {
		resolution, changed, retry, err := u.resolveAttempt(ctx, command, resolvedAt)
		if retry {
			continue
		}
		return resolution, changed, err
	}
	return nil, false, ErrNormalNoShowConflict
}

func (u *NormalNoShowUseCase) resolveAttempt(
	ctx context.Context,
	command NormalNoShowCommand,
	resolvedAt time.Time,
) (*NormalNoShowResolution, bool, bool, error) {
	authority, err := u.repository.LoadNormalNoShowAuthority(ctx, command.Scope)
	if err != nil {
		return nil, false, false, fmt.Errorf("NormalNoShowUseCase - load authority: %w", err)
	}
	if err := validateNormalNoShowAuthority(authority); err != nil {
		return nil, false, false, err
	}
	if authority.Current != nil {
		resolution, reconcileErr := reconcileNormalNoShow(*authority.Current, command)
		return resolution, false, false, reconcileErr
	}
	if !normalNoShowAuthorityMatchesCommand(authority, command) {
		return nil, false, false, ErrNormalNoShowAuthorityConflict
	}
	if !resolvedAt.After(authority.Wave.ReadyWindow.Deadline) {
		return nil, false, false, ErrNormalNoShowCutoff
	}
	resolution, err := buildNormalNoShowResolution(command, authority, resolvedAt)
	if err != nil {
		return nil, false, false, err
	}
	return u.commitNormalNoShow(ctx, command, resolution)
}

func (u *NormalNoShowUseCase) commitNormalNoShow(
	ctx context.Context,
	command NormalNoShowCommand,
	resolution NormalNoShowResolution,
) (*NormalNoShowResolution, bool, bool, error) {
	committed, changed, err := u.repository.CommitNormalNoShow(ctx, resolution)
	if errors.Is(err, domain.ErrConflict) {
		return nil, false, true, nil
	}
	if err != nil {
		return nil, false, false, fmt.Errorf("NormalNoShowUseCase - commit resolution: %w", err)
	}
	if committed == nil || committed.Validate() != nil {
		return nil, false, false, domain.ErrInternal
	}
	result, reconcileErr := reconcileNormalNoShow(*committed, command)
	if reconcileErr != nil || (changed && !normalNoShowResolutionsEqual(*result, resolution)) {
		return nil, false, false, domain.ErrInternal
	}
	return result, changed, false, nil
}

func (r NormalNoShowResolution) Validate() error {
	if err := validateNormalNoShowResolutionIdentity(r); err != nil {
		return err
	}
	if err := validateNormalNoShowResolutionWave(r); err != nil {
		return err
	}
	if err := validateNormalNoShowResolutionSeries(r); err != nil {
		return err
	}
	if err := validateNormalNoShowOutcome(r); err != nil {
		return err
	}
	return validateNormalNoShowRevisions(r)
}

func validateNormalNoShowResolutionIdentity(r NormalNoShowResolution) error {
	if !validNormalNoShowScope(r.Scope) || r.CommandID == uuid.Nil ||
		r.ExpectedAuthorityRevision < 1 || !validArenaServerTime(r.ResolvedAt) {
		return normalNoShowError("invalid resolution identity or timestamp")
	}
	return nil
}

func validateNormalNoShowResolutionWave(r NormalNoShowResolution) error {
	if err := r.Wave.Validate(); err != nil || r.Wave.ID != r.Scope.WaveID ||
		r.Wave.TournamentID != r.Scope.TournamentID ||
		r.Wave.State != domain.ArenaWaveStateReadyWindowExpired ||
		r.Wave.ReadyWindow == nil || r.Wave.ReadyWindow.ID != r.Scope.WindowID {
		return normalNoShowError("invalid expired Wave")
	}
	return nil
}

func validateNormalNoShowResolutionSeries(r NormalNoShowResolution) error {
	if err := r.Series.Validate(); err != nil || r.Series.Series.ID != r.Scope.SeriesID ||
		r.Series.Series.TournamentID != r.Scope.TournamentID || !r.Series.Series.State.IsTerminal() {
		return normalNoShowError("invalid terminal Series")
	}
	return nil
}

func buildNormalNoShowResolution(
	command NormalNoShowCommand,
	authority NormalNoShowAuthority,
	resolvedAt time.Time,
) (NormalNoShowResolution, error) {
	readyParticipantIDs := normalNoShowReadyParticipants(authority)
	if len(readyParticipantIDs) == 2 {
		return NormalNoShowResolution{}, ErrNormalNoShowNotRequired
	}

	wave := cloneArenaWaveExecution(authority.Wave)
	if err := wave.ExpireReadyWindow(command.Scope.WindowID, resolvedAt); err != nil {
		return NormalNoShowResolution{}, normalNoShowError("expire ready window: %v", err)
	}
	series := cloneSeriesExecution(authority.Series)
	targets := normalNoShowGameTargets(series.Series)
	if len(targets) != len(command.GameResultRevisionIDs) {
		return NormalNoShowResolution{}, normalNoShowError("Game result evidence does not cover unstarted Games")
	}

	gameRevisions := make([]NormalNoShowGameRevision, len(targets))
	for index, target := range targets {
		revisionID := command.GameResultRevisionIDs[index]
		if revisionID.IsZero() {
			return NormalNoShowResolution{}, normalNoShowError("missing Game result revision")
		}
		slot := series.Series.Slots[target.slotIndex]
		attempt := slot.Attempts[target.attemptIndex]
		transitioned, changed, err := TransitionGameSlotAttempt(slot, GameAttemptTransitionCommand{
			GameID: attempt.ID, ExpectedAttemptNo: attempt.AttemptNo, ExpectedState: attempt.State,
			NextState: domain.ArenaGameStateCancelled,
			Terminal: &GameTerminalEvidence{
				Reason:           domain.ArenaGameResultReasonSeriesCancelled,
				ResultRevisionID: &revisionID,
			},
		})
		if err != nil || !changed {
			return NormalNoShowResolution{}, normalNoShowError("cancel unstarted Game: %v", err)
		}
		series.Series.Slots[target.slotIndex] = transitioned
		gameRevisions[index] = NormalNoShowGameRevision{
			Ordinal: authority.CurrentOrdinal + index + 1,
			ID:      revisionID, GameID: attempt.ID, State: domain.ArenaGameStateCancelled,
			Reason: domain.ArenaGameResultReasonSeriesCancelled, RecordedAt: resolvedAt,
		}
	}

	terminal, action, err := terminalizeNormalNoShowSeries(
		series,
		readyParticipantIDs,
		command.ScoreRevisionID,
		command.SeriesResultRevisionID,
	)
	if err != nil {
		return NormalNoShowResolution{}, err
	}
	scoreOrdinal := authority.CurrentOrdinal + len(gameRevisions) + 1
	gameRevisionIDs := append([]domain.ArenaOfficialResultRevisionID(nil), command.GameResultRevisionIDs...)
	resolution := NormalNoShowResolution{
		Scope: command.Scope, CommandID: command.CommandID,
		ExpectedAuthorityRevision: authority.Revision, Action: action,
		Wave: wave, Series: terminal, GameRevisions: gameRevisions,
		ScoreRevision: NormalNoShowScoreRevision{
			Ordinal: scoreOrdinal, ID: command.ScoreRevisionID, SeriesID: command.Scope.SeriesID,
			PreviousRevisionID: cloneSeriesScoreRevisionIDPointer(authority.Series.Series.CurrentScoreRevisionID),
			Score:              terminal.Series.Score, GameResultRevisionIDs: gameRevisionIDs, RecordedAt: resolvedAt,
		},
		SeriesRevision: NormalNoShowSeriesRevision{
			Ordinal: scoreOrdinal + 1, ID: command.SeriesResultRevisionID,
			SeriesID:           command.Scope.SeriesID,
			PreviousRevisionID: cloneOfficialResultRevisionIDPointer(authority.Series.Series.CurrentResultRevisionID),
			State:              terminal.Series.State, WinnerID: cloneUUIDPointer(terminal.Series.WinnerID),
			ScoreRevisionID: command.ScoreRevisionID, RecordedAt: resolvedAt,
		},
		ResolvedAt: resolvedAt,
	}
	if err := resolution.Validate(); err != nil {
		return NormalNoShowResolution{}, err
	}
	return cloneNormalNoShowResolution(resolution), nil
}

type normalNoShowGameTarget struct {
	slotIndex    int
	attemptIndex int
}

func normalNoShowGameTargets(series domain.ArenaSeries) []normalNoShowGameTarget {
	result := make([]normalNoShowGameTarget, 0, len(series.Slots))
	for slotIndex, slot := range series.Slots {
		if len(slot.Attempts) == 0 {
			continue
		}
		attemptIndex := len(slot.Attempts) - 1
		state := slot.Attempts[attemptIndex].State
		if state == domain.ArenaGameStatePlanned || state == domain.ArenaGameStateReady {
			result = append(result, normalNoShowGameTarget{slotIndex: slotIndex, attemptIndex: attemptIndex})
		}
	}
	return result
}

func terminalizeNormalNoShowSeries(
	current SeriesExecution,
	readyParticipantIDs []uuid.UUID,
	scoreRevisionID domain.ArenaSeriesScoreRevisionID,
	resultRevisionID domain.ArenaOfficialResultRevisionID,
) (SeriesExecution, NormalNoShowAction, error) {
	if scoreRevisionID.IsZero() || resultRevisionID.IsZero() {
		return SeriesExecution{}, "", normalNoShowError("missing terminal revision identity")
	}
	terminalState := domain.ArenaSeriesStateCancelled
	action := NormalNoShowActionPauseWave
	score := current.Series.Score
	var winnerID *uuid.UUID
	if len(readyParticipantIDs) == 1 {
		terminalState = domain.ArenaSeriesStateCompleted
		action = NormalNoShowActionReopenWave
		winnerID = cloneUUIDPointer(&readyParticipantIDs[0])
		winsRequired := current.Series.Format.WinsRequired()
		if readyParticipantIDs[0] == current.Series.FirstParticipantID {
			score.FirstParticipantWins = winsRequired
		} else {
			score.SecondParticipantWins = winsRequired
		}
	}

	working := cloneSeriesExecution(current)
	if working.Series.State == domain.ArenaSeriesStateReplayRequired &&
		terminalState == domain.ArenaSeriesStateCompleted {
		ready, changed, err := TransitionSeriesExecution(working, SeriesExecutionTransitionCommand{
			NextState: domain.ArenaSeriesStateReady,
		})
		if err != nil || !changed {
			return SeriesExecution{}, "", normalNoShowError("prepare replay terminalization: %v", err)
		}
		working = ready
	}
	scoreID := scoreRevisionID
	resultID := resultRevisionID
	terminal, changed, err := TransitionSeriesExecution(working, SeriesExecutionTransitionCommand{
		NextState: terminalState,
		Terminal: &SeriesTerminalEvidence{
			Score: score, WinnerID: winnerID,
			ScoreRevisionID: &scoreID, ResultRevisionID: &resultID,
		},
	})
	if err != nil || !changed {
		return SeriesExecution{}, "", normalNoShowError("terminalize Series: %v", err)
	}
	return terminal, action, nil
}

func validateNormalNoShowCommand(command NormalNoShowCommand) error {
	if !validNormalNoShowScope(command.Scope) || command.CommandID == uuid.Nil ||
		command.ExpectedWaveRevisionID.IsZero() || command.ExpectedWindowRevisionID.IsZero() ||
		!command.ExpectedSeriesState.IsValid() || command.ScoreRevisionID.IsZero() ||
		command.SeriesResultRevisionID.IsZero() {
		return normalNoShowError("invalid command identity or revision")
	}
	seen := make(map[domain.ArenaOfficialResultRevisionID]struct{}, len(command.GameResultRevisionIDs)+1)
	seen[command.SeriesResultRevisionID] = struct{}{}
	for _, revisionID := range command.GameResultRevisionIDs {
		if revisionID.IsZero() {
			return normalNoShowError("missing Game result revision")
		}
		if _, duplicate := seen[revisionID]; duplicate {
			return normalNoShowError("duplicate result revision identity")
		}
		seen[revisionID] = struct{}{}
	}
	return nil
}

func validateNormalNoShowAuthority(authority NormalNoShowAuthority) error {
	if !validNormalNoShowScope(authority.Scope) || authority.Revision < 1 || authority.CurrentOrdinal < 0 {
		return normalNoShowError("invalid authority identity")
	}
	if authority.Current != nil {
		return validateCurrentNormalNoShow(authority)
	}
	if err := validateNormalNoShowWaveAuthority(authority); err != nil {
		return err
	}
	return validateNormalNoShowSeriesAuthority(authority)
}

func validateCurrentNormalNoShow(authority NormalNoShowAuthority) error {
	if err := authority.Current.Validate(); err != nil || authority.Current.Scope != authority.Scope {
		return normalNoShowError("invalid current resolution")
	}
	return nil
}

func validateNormalNoShowWaveAuthority(authority NormalNoShowAuthority) error {
	if err := authority.Wave.Validate(); err != nil || authority.Wave.ID != authority.Scope.WaveID ||
		authority.Wave.TournamentID != authority.Scope.TournamentID ||
		authority.Wave.ReadyWindow == nil || authority.Wave.ReadyWindow.ID != authority.Scope.WindowID ||
		authority.Wave.ReadyWindow.State != domain.ArenaReadyWindowStateOpen ||
		(authority.Wave.State != domain.ArenaWaveStateReadyWindowOpen &&
			authority.Wave.State != domain.ArenaWaveStateReady) {
		return normalNoShowError("invalid open Wave authority")
	}
	return nil
}

func validateNormalNoShowSeriesAuthority(authority NormalNoShowAuthority) error {
	if err := authority.Series.Validate(); err != nil ||
		authority.Series.Series.ID != authority.Scope.SeriesID ||
		authority.Series.Series.TournamentID != authority.Scope.TournamentID ||
		!normalNoShowSeriesState(authority.Series.Series.State) {
		return normalNoShowError("invalid Series authority")
	}
	return nil
}

func validateNormalNoShowOutcome(resolution NormalNoShowResolution) error {
	switch resolution.Action {
	case NormalNoShowActionReopenWave:
		if resolution.Series.Series.State != domain.ArenaSeriesStateCompleted ||
			resolution.Series.Series.WinnerID == nil {
			return normalNoShowError("reopen outcome requires one winner")
		}
	case NormalNoShowActionPauseWave:
		if resolution.Series.Series.State != domain.ArenaSeriesStateCancelled ||
			resolution.Series.Series.WinnerID != nil {
			return normalNoShowError("pause outcome cannot invent a winner")
		}
	default:
		return normalNoShowError("unknown resolution action")
	}
	return nil
}

func validateNormalNoShowRevisions(resolution NormalNoShowResolution) error {
	expectedOrdinal := resolution.ScoreRevision.Ordinal
	if len(resolution.GameRevisions) != 0 {
		expectedOrdinal = resolution.GameRevisions[0].Ordinal
	}
	if err := validateNormalNoShowGameRevisions(resolution, expectedOrdinal); err != nil {
		return err
	}
	scoreOrdinal := expectedOrdinal + len(resolution.GameRevisions)
	if err := validateNormalNoShowScoreRevision(resolution, scoreOrdinal); err != nil {
		return err
	}
	return validateNormalNoShowSeriesRevision(resolution, scoreOrdinal+1)
}

func validateNormalNoShowGameRevisions(
	resolution NormalNoShowResolution,
	expectedOrdinal int,
) error {
	for index, revision := range resolution.GameRevisions {
		if revision.Ordinal != expectedOrdinal+index || revision.ID.IsZero() || revision.GameID == uuid.Nil ||
			revision.State != domain.ArenaGameStateCancelled ||
			revision.Reason != domain.ArenaGameResultReasonSeriesCancelled ||
			!revision.RecordedAt.Equal(resolution.ResolvedAt) {
			return normalNoShowError("invalid ordered Game revision")
		}
	}
	return nil
}

func validateNormalNoShowScoreRevision(
	resolution NormalNoShowResolution,
	expectedOrdinal int,
) error {
	if resolution.ScoreRevision.Ordinal != expectedOrdinal || resolution.ScoreRevision.ID.IsZero() ||
		resolution.ScoreRevision.SeriesID != resolution.Scope.SeriesID ||
		resolution.ScoreRevision.Score != resolution.Series.Series.Score ||
		!resolution.ScoreRevision.RecordedAt.Equal(resolution.ResolvedAt) ||
		len(resolution.ScoreRevision.GameResultRevisionIDs) != len(resolution.GameRevisions) {
		return normalNoShowError("invalid ordered score revision")
	}
	for index, revisionID := range resolution.ScoreRevision.GameResultRevisionIDs {
		if revisionID != resolution.GameRevisions[index].ID {
			return normalNoShowError("score revision does not reference ordered Games")
		}
	}
	return nil
}

func validateNormalNoShowSeriesRevision(
	resolution NormalNoShowResolution,
	expectedOrdinal int,
) error {
	if resolution.SeriesRevision.Ordinal != expectedOrdinal || resolution.SeriesRevision.ID.IsZero() ||
		resolution.SeriesRevision.SeriesID != resolution.Scope.SeriesID ||
		resolution.SeriesRevision.State != resolution.Series.Series.State ||
		resolution.SeriesRevision.ScoreRevisionID != resolution.ScoreRevision.ID ||
		!resolution.SeriesRevision.RecordedAt.Equal(resolution.ResolvedAt) ||
		!uuidPointersEqual(resolution.SeriesRevision.WinnerID, resolution.Series.Series.WinnerID) {
		return normalNoShowError("invalid ordered Series revision")
	}
	if resolution.Series.Series.CurrentScoreRevisionID == nil ||
		*resolution.Series.Series.CurrentScoreRevisionID != resolution.ScoreRevision.ID ||
		resolution.Series.Series.CurrentResultRevisionID == nil ||
		*resolution.Series.Series.CurrentResultRevisionID != resolution.SeriesRevision.ID {
		return normalNoShowError("terminal Series does not reference current revisions")
	}
	return nil
}

func normalNoShowAuthorityMatchesCommand(
	authority NormalNoShowAuthority,
	command NormalNoShowCommand,
) bool {
	return authority.Scope == command.Scope && authority.Wave.RevisionID == command.ExpectedWaveRevisionID &&
		authority.Wave.ReadyWindow != nil &&
		authority.Wave.ReadyWindow.RevisionID == command.ExpectedWindowRevisionID &&
		authority.Series.Series.State == command.ExpectedSeriesState
}

func normalNoShowReadyParticipants(authority NormalNoShowAuthority) []uuid.UUID {
	result := make([]uuid.UUID, 0, 2)
	for _, participantID := range []uuid.UUID{
		authority.Series.Series.FirstParticipantID,
		authority.Series.Series.SecondParticipantID,
	} {
		for _, member := range authority.Wave.Members {
			if member.ParticipantID == participantID && member.Ready {
				result = append(result, participantID)
				break
			}
		}
	}
	return result
}

func reconcileNormalNoShow(
	resolution NormalNoShowResolution,
	command NormalNoShowCommand,
) (*NormalNoShowResolution, error) {
	if err := resolution.Validate(); err != nil {
		return nil, domain.ErrInternal
	}
	if resolution.Scope != command.Scope || resolution.CommandID != command.CommandID ||
		resolution.SeriesRevision.ID != command.SeriesResultRevisionID ||
		resolution.ScoreRevision.ID != command.ScoreRevisionID ||
		len(resolution.GameRevisions) != len(command.GameResultRevisionIDs) {
		return nil, ErrNormalNoShowAuthorityConflict
	}
	for index, revisionID := range command.GameResultRevisionIDs {
		if resolution.GameRevisions[index].ID != revisionID {
			return nil, ErrNormalNoShowAuthorityConflict
		}
	}
	clone := cloneNormalNoShowResolution(resolution)
	return &clone, nil
}

func normalNoShowResolutionsEqual(first, second NormalNoShowResolution) bool {
	return normalNoShowResolutionHeadersEqual(first, second) &&
		normalNoShowGameRevisionsEqual(first.GameRevisions, second.GameRevisions) &&
		normalNoShowScoreRevisionsEqual(first.ScoreRevision, second.ScoreRevision) &&
		normalNoShowSeriesRevisionsEqual(first.SeriesRevision, second.SeriesRevision)
}

func normalNoShowResolutionHeadersEqual(first, second NormalNoShowResolution) bool {
	if first.Scope != second.Scope || first.CommandID != second.CommandID ||
		first.ExpectedAuthorityRevision != second.ExpectedAuthorityRevision ||
		first.Action != second.Action || !first.ResolvedAt.Equal(second.ResolvedAt) ||
		!arenaWavesEqual(first.Wave, second.Wave) ||
		first.Series.Series.State != second.Series.Series.State ||
		first.Series.Series.Score != second.Series.Series.Score ||
		!uuidPointersEqual(first.Series.Series.WinnerID, second.Series.Series.WinnerID) ||
		len(first.GameRevisions) != len(second.GameRevisions) {
		return false
	}
	return true
}

func normalNoShowGameRevisionsEqual(
	first []NormalNoShowGameRevision,
	second []NormalNoShowGameRevision,
) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		firstRevision := first[index]
		secondRevision := second[index]
		if firstRevision.Ordinal != secondRevision.Ordinal || firstRevision.ID != secondRevision.ID ||
			firstRevision.GameID != secondRevision.GameID || firstRevision.State != secondRevision.State ||
			firstRevision.Reason != secondRevision.Reason ||
			!firstRevision.RecordedAt.Equal(secondRevision.RecordedAt) {
			return false
		}
	}
	return true
}

func normalNoShowScoreRevisionsEqual(
	first NormalNoShowScoreRevision,
	second NormalNoShowScoreRevision,
) bool {
	if first.Ordinal != second.Ordinal || first.ID != second.ID ||
		first.SeriesID != second.SeriesID || first.Score != second.Score ||
		!first.RecordedAt.Equal(second.RecordedAt) ||
		len(first.GameResultRevisionIDs) != len(second.GameResultRevisionIDs) {
		return false
	}
	for index := range first.GameResultRevisionIDs {
		if first.GameResultRevisionIDs[index] != second.GameResultRevisionIDs[index] {
			return false
		}
	}
	return true
}

func normalNoShowSeriesRevisionsEqual(
	first NormalNoShowSeriesRevision,
	second NormalNoShowSeriesRevision,
) bool {
	return first.Ordinal == second.Ordinal && first.ID == second.ID &&
		first.SeriesID == second.SeriesID && first.State == second.State &&
		uuidPointersEqual(first.WinnerID, second.WinnerID) &&
		first.ScoreRevisionID == second.ScoreRevisionID && first.RecordedAt.Equal(second.RecordedAt)
}

func normalNoShowSeriesState(state domain.ArenaSeriesState) bool {
	return state == domain.ArenaSeriesStateReady || state == domain.ArenaSeriesStateActive ||
		state == domain.ArenaSeriesStateReplayRequired
}

func validNormalNoShowScope(scope NormalNoShowScope) bool {
	return scope.TournamentID != uuid.Nil && scope.WaveID != uuid.Nil &&
		scope.WindowID != uuid.Nil && scope.SeriesID != uuid.Nil
}

func uuidPointersEqual(first, second *uuid.UUID) bool {
	if first == nil || second == nil {
		return first == second
	}
	return *first == *second
}

func cloneNormalNoShowResolution(resolution NormalNoShowResolution) NormalNoShowResolution {
	clone := resolution
	clone.Wave = cloneArenaWaveExecution(resolution.Wave)
	clone.Series = cloneSeriesExecution(resolution.Series)
	clone.GameRevisions = append([]NormalNoShowGameRevision(nil), resolution.GameRevisions...)
	for index := range clone.GameRevisions {
		clone.GameRevisions[index].PreviousRevisionID = cloneOfficialResultRevisionIDPointer(
			resolution.GameRevisions[index].PreviousRevisionID,
		)
	}
	clone.ScoreRevision.PreviousRevisionID = cloneSeriesScoreRevisionIDPointer(
		resolution.ScoreRevision.PreviousRevisionID,
	)
	clone.ScoreRevision.GameResultRevisionIDs = append(
		[]domain.ArenaOfficialResultRevisionID(nil),
		resolution.ScoreRevision.GameResultRevisionIDs...,
	)
	clone.SeriesRevision.PreviousRevisionID = cloneOfficialResultRevisionIDPointer(
		resolution.SeriesRevision.PreviousRevisionID,
	)
	clone.SeriesRevision.WinnerID = cloneUUIDPointer(resolution.SeriesRevision.WinnerID)
	return clone
}

func normalNoShowError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidNormalNoShow, fmt.Sprintf(format, arguments...))
}
