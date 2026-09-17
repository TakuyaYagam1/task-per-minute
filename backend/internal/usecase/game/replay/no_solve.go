package replay

import (
	"context"
	"errors"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
	attemptusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/attempt"
	closeusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/close"
)

type NoSolveReplayUseCase struct {
	terminalizer FailedAttemptTerminalizer
	closer       OldWaveCloser
	replacer     ReplayReplacementPlanner
}

func NewNoSolveReplayUseCase(
	terminalizer FailedAttemptTerminalizer,
	closer OldWaveCloser,
	replacer ReplayReplacementPlanner,
) *NoSolveReplayUseCase {
	return &NoSolveReplayUseCase{
		terminalizer: terminalizer,
		closer:       closer,
		replacer:     replacer,
	}
}

func (u *NoSolveReplayUseCase) Replay(
	ctx context.Context,
	command NoSolveReplayCommand,
) (*NoSolveReplayResult, bool, error) {
	if u == nil || u.terminalizer == nil || u.closer == nil || u.replacer == nil {
		return nil, false, domain.ErrValidation
	}
	if err := validateNoSolveReplayCommand(command); err != nil {
		return nil, false, err
	}
	failed, failedChanged, err := u.terminalizer.Terminalize(ctx, command.Terminalize)
	if err != nil {
		return nil, false, err
	}
	if !validNoSolveFailedAttempt(failed, command.Terminalize) {
		return nil, false, domain.ErrInternal
	}
	return u.closeNoSolveWave(ctx, command, failed, failedChanged)
}

func (u *NoSolveReplayUseCase) closeNoSolveWave(
	ctx context.Context,
	command NoSolveReplayCommand,
	failed *attemptusecase.AttemptRecord,
	failedChanged bool,
) (*NoSolveReplayResult, bool, error) {
	closure, closureChanged, err := u.closer.Close(ctx, command.Close)
	if err != nil {
		return nil, failedChanged, err
	}
	if !validNoSolveClosure(closure, failed, command.Close) {
		return nil, failedChanged, domain.ErrInternal
	}
	return u.replaceNoSolveAttempt(
		ctx,
		command.Replace,
		failed,
		closure,
		failedChanged || closureChanged,
	)
}

func (u *NoSolveReplayUseCase) replaceNoSolveAttempt(
	ctx context.Context,
	command ReplayReplacementCommand,
	failed *attemptusecase.AttemptRecord,
	closure *closeusecase.Closure,
	priorChanged bool,
) (*NoSolveReplayResult, bool, error) {
	replacement, replacementChanged, err := u.replacer.Replace(ctx, command)
	if errors.Is(err, ErrReplayReservesExhausted) {
		result := &NoSolveReplayResult{
			FailedAttempt:  cloneFailedAttemptRecordPointer(failed),
			OldWaveClosure: cloneOldWaveClosurePointer(closure),
			Exhausted:      true,
		}
		return result, priorChanged, nil
	}
	if err != nil {
		return nil, priorChanged, err
	}
	if !validNoSolveReplacement(replacement, closure, command) {
		return nil, priorChanged, domain.ErrInternal
	}
	result := &NoSolveReplayResult{
		FailedAttempt:  cloneFailedAttemptRecordPointer(failed),
		OldWaveClosure: cloneOldWaveClosurePointer(closure),
		Replacement:    cloneReplayReplacementPointer(replacement),
	}
	return result, priorChanged || replacementChanged, nil
}

func validNoSolveFailedAttempt(
	failed *attemptusecase.AttemptRecord,
	command attemptusecase.AttemptCommand,
) bool {
	if failed == nil || failed.Failure.Class != gamedomain.FailureNoSolve {
		return false
	}
	_, err := attemptusecase.Reconcile(*failed, command)
	return err == nil
}

func validNoSolveClosure(
	closure *closeusecase.Closure,
	failed *attemptusecase.AttemptRecord,
	command closeusecase.CloseCommand,
) bool {
	if closure == nil || failed == nil || !closureContainsFailedRoute(*closure, *failed) {
		return false
	}
	_, err := closeusecase.ReconcileClosure(*closure, command)
	return err == nil
}

func validNoSolveReplacement(
	replacement *ReplayReplacement,
	closure *closeusecase.Closure,
	command ReplayReplacementCommand,
) bool {
	if replacement == nil || closure == nil ||
		replacement.ClosureRevisionID != closure.Wave.RevisionID {
		return false
	}
	_, err := reconcileReplayReplacement(*replacement, command)
	return err == nil
}

func validateNoSolveReplayCommand(command NoSolveReplayCommand) error {
	if command.Terminalize.FailureClass != gamedomain.FailureNoSolve ||
		attemptusecase.ValidateCommand(command.Terminalize) != nil ||
		closeusecase.ValidateCloseCommand(command.Close) != nil ||
		validateReplayReplacementCommand(command.Replace) != nil {
		return replayReplacementError("invalid no-solve pipeline command")
	}
	failedScope := command.Terminalize.Scope
	if command.Close.Scope.TournamentID != failedScope.TournamentID ||
		command.Close.Scope.WaveID != failedScope.WaveID ||
		command.Replace.Scope.TournamentID != failedScope.TournamentID ||
		command.Replace.Scope.OldWaveID != failedScope.WaveID ||
		command.Replace.Scope.SeriesID != failedScope.SeriesID ||
		command.Replace.Scope.SlotID != failedScope.SlotID ||
		command.Replace.Scope.AssignmentID != failedScope.AssignmentID ||
		command.Replace.ExpectedClosureRevisionID != command.Close.ClosedWaveRevisionID {
		return replayReplacementError("pipeline scopes or closure revision do not align")
	}
	return nil
}
