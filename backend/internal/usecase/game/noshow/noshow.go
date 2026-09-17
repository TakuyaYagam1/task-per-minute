package noshow

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
)

const normalNoShowAttempts = 2

var (
	ErrInvalidNormalNoShow           = errors.New("invalid normal no-show resolution")
	ErrNormalNoShowCutoff            = errors.New("no-show cutoff has not passed")
	ErrNormalNoShowNotRequired       = errors.New("no-show resolution is not required")
	ErrNormalNoShowAuthorityConflict = errors.New("no-show authority conflict")
	ErrNormalNoShowConflict          = errors.New("no-show commit conflict")
)

type NoShowAuthority struct {
	Scope          domain.NormalNoShowScope
	Revision       int64
	Wave           domain.Wave
	Series         seriesdomain.Execution
	CurrentOrdinal int
	Current        *NoShowResolution
}

type NoShowCommand struct {
	Scope                    domain.NormalNoShowScope
	CommandID                uuid.UUID
	ExpectedWaveRevisionID   domain.WaveRevisionID
	ExpectedWindowRevisionID domain.ReadyWindowRevisionID
	ExpectedSeriesState      domain.SeriesState
	GameResultRevisionIDs    []domain.OfficialResultRevisionID
	ScoreRevisionID          domain.SeriesScoreRevisionID
	SeriesResultRevisionID   domain.OfficialResultRevisionID
}

type NoShowResolution struct {
	Scope                     domain.NormalNoShowScope
	CommandID                 uuid.UUID
	ExpectedAuthorityRevision int64
	Action                    domain.NormalNoShowAction
	Wave                      domain.Wave
	Series                    seriesdomain.Execution
	GameRevisions             []domain.NormalNoShowGameRevision
	ScoreRevision             domain.NormalNoShowScoreRevision
	SeriesRevision            domain.NormalNoShowSeriesRevision
	ResolvedAt                time.Time
}

type NoShowUseCase struct {
	repository NoShowRepository
	clock      NoShowClock
}

type NoShowClock interface {
	Now() time.Time
}

type NoShowRepository interface {
	LoadNormalNoShowAuthority(
		ctx context.Context,
		scope domain.NormalNoShowScope,
	) (NoShowAuthority, error)
	CommitNormalNoShow(
		ctx context.Context,
		resolution NoShowResolution,
	) (*NoShowResolution, bool, error)
}

func NoShowNewUseCase(
	repository NoShowRepository,
	clock NoShowClock,
) *NoShowUseCase {
	return &NoShowUseCase{repository: repository, clock: clock}
}

func (u *NoShowUseCase) Resolve(
	ctx context.Context,
	command NoShowCommand,
) (*NoShowResolution, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil {
		return nil, false, domain.ErrValidation
	}
	if err := validateNormalNoShowCommand(command); err != nil {
		return nil, false, err
	}
	resolvedAt := u.clock.Now().Round(0).UTC()
	if !domain.IsValidServerTime(resolvedAt) {
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

func (u *NoShowUseCase) resolveAttempt(
	ctx context.Context,
	command NoShowCommand,
	resolvedAt time.Time,
) (*NoShowResolution, bool, bool, error) {
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

func (u *NoShowUseCase) commitNormalNoShow(
	ctx context.Context,
	command NoShowCommand,
	resolution NoShowResolution,
) (*NoShowResolution, bool, bool, error) {
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

func (r NoShowResolution) Validate() error {
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

func validateNormalNoShowResolutionIdentity(r NoShowResolution) error {
	if !validNormalNoShowScope(r.Scope) || r.CommandID == uuid.Nil ||
		r.ExpectedAuthorityRevision < 1 || !domain.IsValidServerTime(r.ResolvedAt) {
		return normalNoShowError("invalid resolution identity or timestamp")
	}
	return nil
}

func validateNormalNoShowResolutionWave(r NoShowResolution) error {
	if err := r.Wave.Validate(); err != nil || r.Wave.ID != r.Scope.WaveID ||
		r.Wave.TournamentID != r.Scope.TournamentID ||
		r.Wave.State != domain.WaveStateReadyWindowExpired ||
		r.Wave.ReadyWindow == nil || r.Wave.ReadyWindow.ID != r.Scope.WindowID {
		return normalNoShowError("invalid expired Wave")
	}
	return nil
}

func validateNormalNoShowResolutionSeries(r NoShowResolution) error {
	if err := r.Series.Validate(); err != nil || r.Series.Series.ID != r.Scope.SeriesID ||
		r.Series.Series.TournamentID != r.Scope.TournamentID || !r.Series.Series.State.IsTerminal() {
		return normalNoShowError("invalid terminal Series")
	}
	return nil
}

func buildNormalNoShowResolution(
	command NoShowCommand,
	authority NoShowAuthority,
	resolvedAt time.Time,
) (NoShowResolution, error) {
	readyParticipantIDs := normalNoShowReadyParticipants(authority)
	if len(readyParticipantIDs) == 2 {
		return NoShowResolution{}, ErrNormalNoShowNotRequired
	}

	wave := cloneWave(authority.Wave)
	if err := wave.ExpireReadyWindow(command.Scope.WindowID, resolvedAt); err != nil {
		return NoShowResolution{}, normalNoShowError("expire ready window: %v", err)
	}
	series := seriesdomain.CloneExecution(authority.Series)
	targets := normalNoShowGameTargets(series.Series)
	if len(targets) != len(command.GameResultRevisionIDs) {
		return NoShowResolution{}, normalNoShowError("Game result evidence does not cover unstarted Games")
	}

	gameRevisions := make([]domain.NormalNoShowGameRevision, len(targets))
	for index, target := range targets {
		revisionID := command.GameResultRevisionIDs[index]
		if revisionID.IsZero() {
			return NoShowResolution{}, normalNoShowError("missing Game result revision")
		}
		slot := series.Series.Slots[target.slotIndex]
		attempt := slot.Attempts[target.attemptIndex]
		transitioned, changed, err := gamedomain.TransitionSlotAttempt(slot, gamedomain.TransitionCommand{
			GameID: attempt.ID, ExpectedAttemptNo: attempt.AttemptNo, ExpectedState: attempt.State,
			NextState: domain.GameStateCancelled,
			Terminal: &gamedomain.TerminalEvidence{
				Reason:           domain.GameResultReasonSeriesCancelled,
				ResultRevisionID: &revisionID,
			},
		})
		if err != nil || !changed {
			return NoShowResolution{}, normalNoShowError("cancel unstarted Game: %v", err)
		}
		series.Series.Slots[target.slotIndex] = transitioned
		gameRevisions[index] = domain.NormalNoShowGameRevision{
			Ordinal: authority.CurrentOrdinal + index + 1,
			ID:      revisionID, GameID: attempt.ID, State: domain.GameStateCancelled,
			Reason: domain.GameResultReasonSeriesCancelled, RecordedAt: resolvedAt,
		}
	}

	terminal, action, err := terminalizeNormalNoShowSeries(
		series,
		readyParticipantIDs,
		command.ScoreRevisionID,
		command.SeriesResultRevisionID,
	)
	if err != nil {
		return NoShowResolution{}, err
	}
	scoreOrdinal := authority.CurrentOrdinal + len(gameRevisions) + 1
	gameRevisionIDs := append([]domain.OfficialResultRevisionID(nil), command.GameResultRevisionIDs...)
	resolution := NoShowResolution{
		Scope: command.Scope, CommandID: command.CommandID,
		ExpectedAuthorityRevision: authority.Revision, Action: action,
		Wave: wave, Series: terminal, GameRevisions: gameRevisions,
		ScoreRevision: domain.NormalNoShowScoreRevision{
			Ordinal: scoreOrdinal, ID: command.ScoreRevisionID, SeriesID: command.Scope.SeriesID,
			PreviousRevisionID: noShowCloneSeriesScoreRevisionIDPointer(authority.Series.Series.CurrentScoreRevisionID),
			Score:              terminal.Series.Score, GameResultRevisionIDs: gameRevisionIDs, RecordedAt: resolvedAt,
		},
		SeriesRevision: domain.NormalNoShowSeriesRevision{
			Ordinal: scoreOrdinal + 1, ID: command.SeriesResultRevisionID,
			SeriesID:           command.Scope.SeriesID,
			PreviousRevisionID: noShowCloneOfficialResultRevisionIDPointer(authority.Series.Series.CurrentResultRevisionID),
			State:              terminal.Series.State, WinnerID: noShowCloneUUIDPointer(terminal.Series.WinnerID),
			ScoreRevisionID: command.ScoreRevisionID, RecordedAt: resolvedAt,
		},
		ResolvedAt: resolvedAt,
	}
	if err := resolution.Validate(); err != nil {
		return NoShowResolution{}, err
	}
	return cloneNormalNoShowResolution(resolution), nil
}

type normalNoShowGameTarget struct {
	slotIndex    int
	attemptIndex int
}

func normalNoShowGameTargets(series domain.Series) []normalNoShowGameTarget {
	result := make([]normalNoShowGameTarget, 0, len(series.Slots))
	for slotIndex, slot := range series.Slots {
		if len(slot.Attempts) == 0 {
			continue
		}
		attemptIndex := len(slot.Attempts) - 1
		state := slot.Attempts[attemptIndex].State
		if state == domain.GameStatePlanned || state == domain.GameStateReady {
			result = append(result, normalNoShowGameTarget{slotIndex: slotIndex, attemptIndex: attemptIndex})
		}
	}
	return result
}

func terminalizeNormalNoShowSeries(
	current seriesdomain.Execution,
	readyParticipantIDs []uuid.UUID,
	scoreRevisionID domain.SeriesScoreRevisionID,
	resultRevisionID domain.OfficialResultRevisionID,
) (seriesdomain.Execution, domain.NormalNoShowAction, error) {
	if scoreRevisionID.IsZero() || resultRevisionID.IsZero() {
		return seriesdomain.Execution{}, "", normalNoShowError("missing terminal revision identity")
	}
	terminalState := domain.SeriesStateCancelled
	action := domain.NormalNoShowActionPauseWave
	score := current.Series.Score
	var winnerID *uuid.UUID
	if len(readyParticipantIDs) == 1 {
		terminalState = domain.SeriesStateCompleted
		action = domain.NormalNoShowActionReopenWave
		winnerID = noShowCloneUUIDPointer(&readyParticipantIDs[0])
		winsRequired := current.Series.Format.WinsRequired()
		if readyParticipantIDs[0] == current.Series.FirstParticipantID {
			score.FirstParticipantWins = winsRequired
		} else {
			score.SecondParticipantWins = winsRequired
		}
	}

	working := seriesdomain.CloneExecution(current)
	if working.Series.State == domain.SeriesStateReplayRequired &&
		terminalState == domain.SeriesStateCompleted {
		ready, changed, err := seriesdomain.Transition(working, seriesdomain.TransitionCommand{
			NextState: domain.SeriesStateReady,
		})
		if err != nil || !changed {
			return seriesdomain.Execution{}, "", normalNoShowError("prepare replay terminalization: %v", err)
		}
		working = ready
	}
	scoreID := scoreRevisionID
	resultID := resultRevisionID
	terminal, changed, err := seriesdomain.Transition(working, seriesdomain.TransitionCommand{
		NextState: terminalState,
		Terminal: &seriesdomain.TerminalEvidence{
			Score: score, WinnerID: winnerID,
			ScoreRevisionID: &scoreID, ResultRevisionID: &resultID,
		},
	})
	if err != nil || !changed {
		return seriesdomain.Execution{}, "", normalNoShowError("terminalize Series: %v", err)
	}
	return terminal, action, nil
}

func normalNoShowAuthorityMatchesCommand(
	authority NoShowAuthority,
	command NoShowCommand,
) bool {
	return authority.Scope == command.Scope && authority.Wave.RevisionID == command.ExpectedWaveRevisionID &&
		authority.Wave.ReadyWindow != nil &&
		authority.Wave.ReadyWindow.RevisionID == command.ExpectedWindowRevisionID &&
		authority.Series.Series.State == command.ExpectedSeriesState
}

func normalNoShowReadyParticipants(authority NoShowAuthority) []uuid.UUID {
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
	resolution NoShowResolution,
	command NoShowCommand,
) (*NoShowResolution, error) {
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

func normalNoShowResolutionsEqual(first, second NoShowResolution) bool {
	return normalNoShowResolutionHeadersEqual(first, second) &&
		normalNoShowGameRevisionsEqual(first.GameRevisions, second.GameRevisions) &&
		normalNoShowScoreRevisionsEqual(first.ScoreRevision, second.ScoreRevision) &&
		normalNoShowSeriesRevisionsEqual(first.SeriesRevision, second.SeriesRevision)
}

func normalNoShowResolutionHeadersEqual(first, second NoShowResolution) bool {
	if first.Scope != second.Scope || first.CommandID != second.CommandID ||
		first.ExpectedAuthorityRevision != second.ExpectedAuthorityRevision ||
		first.Action != second.Action || !first.ResolvedAt.Equal(second.ResolvedAt) ||
		!noShowWavesEqual(first.Wave, second.Wave) ||
		first.Series.Series.State != second.Series.Series.State ||
		first.Series.Series.Score != second.Series.Series.Score ||
		!noShowUUIDPointersEqual(first.Series.Series.WinnerID, second.Series.Series.WinnerID) ||
		len(first.GameRevisions) != len(second.GameRevisions) {
		return false
	}
	return true
}

func normalNoShowGameRevisionsEqual(
	first []domain.NormalNoShowGameRevision,
	second []domain.NormalNoShowGameRevision,
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
	first domain.NormalNoShowScoreRevision,
	second domain.NormalNoShowScoreRevision,
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
	first domain.NormalNoShowSeriesRevision,
	second domain.NormalNoShowSeriesRevision,
) bool {
	return first.Ordinal == second.Ordinal && first.ID == second.ID &&
		first.SeriesID == second.SeriesID && first.State == second.State &&
		noShowUUIDPointersEqual(first.WinnerID, second.WinnerID) &&
		first.ScoreRevisionID == second.ScoreRevisionID && first.RecordedAt.Equal(second.RecordedAt)
}

func normalNoShowSeriesState(state domain.SeriesState) bool {
	return state == domain.SeriesStateReady || state == domain.SeriesStateActive ||
		state == domain.SeriesStateReplayRequired
}

func validNormalNoShowScope(scope domain.NormalNoShowScope) bool {
	return scope.TournamentID != uuid.Nil && scope.WaveID != uuid.Nil &&
		scope.WindowID != uuid.Nil && scope.SeriesID != uuid.Nil
}

func cloneNormalNoShowResolution(resolution NoShowResolution) NoShowResolution {
	clone := resolution
	clone.Wave = cloneWave(resolution.Wave)
	clone.Series = seriesdomain.CloneExecution(resolution.Series)
	clone.GameRevisions = append([]domain.NormalNoShowGameRevision(nil), resolution.GameRevisions...)
	for index := range clone.GameRevisions {
		clone.GameRevisions[index].PreviousRevisionID = noShowCloneOfficialResultRevisionIDPointer(
			resolution.GameRevisions[index].PreviousRevisionID,
		)
	}
	clone.ScoreRevision.PreviousRevisionID = noShowCloneSeriesScoreRevisionIDPointer(
		resolution.ScoreRevision.PreviousRevisionID,
	)
	clone.ScoreRevision.GameResultRevisionIDs = append(
		[]domain.OfficialResultRevisionID(nil),
		resolution.ScoreRevision.GameResultRevisionIDs...,
	)
	clone.SeriesRevision.PreviousRevisionID = noShowCloneOfficialResultRevisionIDPointer(
		resolution.SeriesRevision.PreviousRevisionID,
	)
	clone.SeriesRevision.WinnerID = noShowCloneUUIDPointer(resolution.SeriesRevision.WinnerID)
	return clone
}

func normalNoShowError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidNormalNoShow, fmt.Sprintf(format, arguments...))
}
