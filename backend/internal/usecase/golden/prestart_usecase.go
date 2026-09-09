package golden

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type RetainedGoldenPrestartCommit struct {
	ExpectedExecution         *GoldenWaveExecutionExpectation
	ExpectedArchivedExecution *GoldenWaveExecutionExpectation
	ExpectedSession           *RetainedGoldenPrestartExpectation
	ExpectedAuthorization     GoldenPrestartOperatorAuthorizationExpectation
	ArchivedExecution         *GoldenWaveExecution
	PublishedExecution        *GoldenWaveExecution
	NewIdentityIDs            []uuid.UUID
	Record                    RetainedGoldenPrestartRecord
}

type RetainedGoldenPrestartPauseCommand struct {
	Scope                 GoldenStateScope
	CommandID             uuid.UUID
	SessionID             uuid.UUID
	ActorID               uuid.UUID
	Reason                GoldenPrestartPauseReason
	ExpectedExecution     GoldenWaveExecutionExpectation
	ExpectedAuthorization GoldenPrestartOperatorAuthorizationExpectation
	NextSessionRevisionID uuid.UUID
}

type RetainedGoldenPrestartResumeCommand struct {
	Scope                    GoldenStateScope
	CommandID                uuid.UUID
	SessionID                uuid.UUID
	ActorID                  uuid.UUID
	ExpectedSession          RetainedGoldenPrestartExpectation
	ExpectedAuthorization    GoldenPrestartOperatorAuthorizationExpectation
	NextSessionRevisionID    uuid.UUID
	NextExecutionRevisionID  uuid.UUID
	NextWaveRevisionID       domain.WaveRevisionID
	NextWindowID             uuid.UUID
	NextWaveWindowRevisionID domain.ReadyWindowRevisionID
	NextWindowRevisionID     uuid.UUID
	NextReadinessRevisionID  uuid.UUID
	NextPresenceRevisionID   uuid.UUID
}

type RetainedGoldenPrestartPauseUseCase struct {
	repository PrestartRepository
	clock      PrestartClock
}

func NewRetainedGoldenPrestartPauseUseCase(
	repository PrestartRepository,
	clock PrestartClock,
) *RetainedGoldenPrestartPauseUseCase {
	return &RetainedGoldenPrestartPauseUseCase{repository: repository, clock: clock}
}

func (u *RetainedGoldenPrestartPauseUseCase) Pause(
	ctx context.Context,
	command RetainedGoldenPrestartPauseCommand,
) (*RetainedGoldenPrestartRecord, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil || !validRetainedGoldenPauseCommand(command) {
		return nil, false, domain.ErrValidation
	}
	digest := retainedGoldenPauseCommandDigest(command)
	if replay, err := u.repository.FindRetainedGoldenPrestartCommand(ctx, command.Scope.TournamentID, command.CommandID); err != nil {
		return nil, false, fmt.Errorf("RetainedGoldenPrestartPauseUseCase - find pause replay: %w", err)
	} else if replay != nil {
		return reconcileRetainedGoldenPrestart(*replay, command.Scope, command.CommandID, digest)
	}
	pausedAt := u.clock.Now().Round(0).UTC()
	if !domain.IsValidServerTime(pausedAt) {
		return nil, false, domain.ErrValidation
	}
	for range retainedGoldenPrestartCommitAttempts {
		record, changed, retry, err := u.pauseAttempt(ctx, command, digest, pausedAt)
		if !retry {
			return record, changed, err
		}
		replay, replayErr := u.repository.FindRetainedGoldenPrestartCommand(ctx, command.Scope.TournamentID, command.CommandID)
		if replayErr != nil {
			return nil, false, fmt.Errorf("RetainedGoldenPrestartPauseUseCase - find retry pause replay: %w", replayErr)
		}
		if replay != nil {
			return reconcileRetainedGoldenPrestart(*replay, command.Scope, command.CommandID, digest)
		}
	}
	return nil, false, ErrGoldenPrestartConflict
}

func (u *RetainedGoldenPrestartPauseUseCase) pauseAttempt(
	ctx context.Context,
	command RetainedGoldenPrestartPauseCommand,
	digest [sha256.Size]byte,
	pausedAt time.Time,
) (*RetainedGoldenPrestartRecord, bool, bool, error) {
	if replay, err := u.repository.FindRetainedGoldenPrestartCommand(
		ctx, command.Scope.TournamentID, command.CommandID,
	); err != nil {
		return nil, false, false, fmt.Errorf("RetainedGoldenPrestartPauseUseCase - find pause attempt replay: %w", err)
	} else if replay != nil {
		result, changed, replayErr := reconcileRetainedGoldenPrestart(*replay, command.Scope, command.CommandID, digest)
		return result, changed, false, replayErr
	}
	authority, err := u.repository.LoadRetainedGoldenPrestartAuthority(ctx, command.Scope)
	if err != nil {
		return nil, false, false, fmt.Errorf("RetainedGoldenPrestartPauseUseCase - load pause authority: %w", err)
	}
	if authority.Validate() != nil {
		return nil, false, false, domain.ErrInternal
	}
	if !authority.Authorization.Expectation().Equal(command.ExpectedAuthorization) ||
		authority.Authorization.ActorID != command.ActorID {
		return nil, false, false, ErrGoldenPrestartAuthorityConflict
	}
	if authority.Execution == nil || authority.Current != nil {
		return nil, false, false, ErrGoldenPrestartSessionStateConflict
	}
	execution := authority.Execution.Snapshot()
	if !execution.Expectation().Equal(command.ExpectedExecution) {
		return nil, false, false, ErrGoldenPrestartAuthorityConflict
	}
	if execution.Start != nil || execution.Attempt.StartedAt != nil || execution.Wave.StartedAt != nil {
		return nil, false, false, ErrGoldenPrestartAlreadyStarted
	}
	if pausedAt.Before(execution.OpenedAt) {
		return nil, false, false, goldenPrestartError("pause time precedes ready window")
	}
	record, err := buildRetainedGoldenPauseRecord(execution, command, digest, pausedAt)
	if err != nil {
		return nil, false, false, err
	}
	expected := command.ExpectedExecution
	commit := RetainedGoldenPrestartCommit{
		ExpectedExecution: &expected, ExpectedAuthorization: command.ExpectedAuthorization,
		ArchivedExecution: &execution, NewIdentityIDs: append([]uuid.UUID(nil), record.NewIdentityIDs...),
		Record: record.Snapshot(),
	}
	return commitRetainedGoldenPrestart(ctx, u.repository, commit, record)
}

func (u *RetainedGoldenPrestartPauseUseCase) Resume(
	ctx context.Context,
	command RetainedGoldenPrestartResumeCommand,
) (*RetainedGoldenPrestartRecord, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil || !validRetainedGoldenResumeCommand(command) {
		return nil, false, domain.ErrValidation
	}
	digest := retainedGoldenResumeCommandDigest(command)
	if replay, err := u.repository.FindRetainedGoldenPrestartCommand(ctx, command.Scope.TournamentID, command.CommandID); err != nil {
		return nil, false, fmt.Errorf("RetainedGoldenPrestartPauseUseCase - find resume replay: %w", err)
	} else if replay != nil {
		return reconcileRetainedGoldenPrestart(*replay, command.Scope, command.CommandID, digest)
	}
	resumedAt := u.clock.Now().Round(0).UTC()
	if !domain.IsValidServerTime(resumedAt) {
		return nil, false, domain.ErrValidation
	}
	for range retainedGoldenPrestartCommitAttempts {
		record, changed, retry, err := u.resumeAttempt(ctx, command, digest, resumedAt)
		if !retry {
			return record, changed, err
		}
		replay, replayErr := u.repository.FindRetainedGoldenPrestartCommand(ctx, command.Scope.TournamentID, command.CommandID)
		if replayErr != nil {
			return nil, false, fmt.Errorf("RetainedGoldenPrestartPauseUseCase - find retry resume replay: %w", replayErr)
		}
		if replay != nil {
			return reconcileRetainedGoldenPrestart(*replay, command.Scope, command.CommandID, digest)
		}
	}
	return nil, false, ErrGoldenPrestartConflict
}

func (u *RetainedGoldenPrestartPauseUseCase) resumeAttempt(
	ctx context.Context,
	command RetainedGoldenPrestartResumeCommand,
	digest [sha256.Size]byte,
	resumedAt time.Time,
) (*RetainedGoldenPrestartRecord, bool, bool, error) {
	if replay, err := u.repository.FindRetainedGoldenPrestartCommand(
		ctx, command.Scope.TournamentID, command.CommandID,
	); err != nil {
		return nil, false, false, fmt.Errorf("RetainedGoldenPrestartPauseUseCase - find resume attempt replay: %w", err)
	} else if replay != nil {
		result, changed, replayErr := reconcileRetainedGoldenPrestart(*replay, command.Scope, command.CommandID, digest)
		return result, changed, false, replayErr
	}
	authority, err := u.repository.LoadRetainedGoldenPrestartAuthority(ctx, command.Scope)
	if err != nil {
		return nil, false, false, fmt.Errorf("RetainedGoldenPrestartPauseUseCase - load resume authority: %w", err)
	}
	if authority.Validate() != nil {
		return nil, false, false, domain.ErrInternal
	}
	if !authority.Authorization.Expectation().Equal(command.ExpectedAuthorization) ||
		authority.Authorization.ActorID != command.ActorID {
		return nil, false, false, ErrGoldenPrestartAuthorityConflict
	}
	if authority.Execution != nil || authority.ArchivedExecution == nil || authority.Current == nil ||
		!authority.Current.Expectation().Equal(command.ExpectedSession) || authority.Current.SessionID != command.SessionID {
		return nil, false, false, ErrGoldenPrestartAuthorityConflict
	}
	if authority.Current.State != RetainedGoldenPrestartPaused {
		return nil, false, false, ErrGoldenPrestartSessionStateConflict
	}
	if resumedAt.Before(authority.Current.OccurredAt) {
		return nil, false, false, goldenPrestartError("resume time precedes retained pause")
	}
	record, execution, err := buildRetainedGoldenResumeRecord(
		*authority.Current, authority.ArchivedExecution.Snapshot(), command, digest, resumedAt,
	)
	if err != nil {
		return nil, false, false, err
	}
	expectedSession := command.ExpectedSession
	expectedArchivedExecution := authority.ArchivedExecution.Expectation()
	commit := RetainedGoldenPrestartCommit{
		ExpectedArchivedExecution: &expectedArchivedExecution,
		ExpectedSession:           &expectedSession, ExpectedAuthorization: command.ExpectedAuthorization,
		PublishedExecution: &execution, NewIdentityIDs: append([]uuid.UUID(nil), record.NewIdentityIDs...),
		Record: record.Snapshot(),
	}
	return commitRetainedGoldenPrestart(ctx, u.repository, commit, record)
}
