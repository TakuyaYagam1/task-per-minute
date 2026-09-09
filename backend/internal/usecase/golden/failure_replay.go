package golden

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type GoldenFailureCommit struct {
	Expected       GoldenFailureExpectation
	Route          GoldenFailureRoute
	NewIdentityIDs []uuid.UUID
	Record         GoldenFailureRecord
}

type GoldenFailureReplayCommand struct {
	Scope                    GoldenSubmissionScope
	CommandID                uuid.UUID
	FailureID                uuid.UUID
	Expected                 GoldenFailureExpectation
	ClosedWaveRevisionID     domain.WaveRevisionID
	NextAttemptID            uuid.UUID
	NextAssignmentID         uuid.UUID
	NextAssignmentRevisionID uuid.UUID
	NextWaveID               uuid.UUID
	NextWaveRevisionID       domain.WaveRevisionID
	NextWindowID             uuid.UUID
	NextWaveWindowRevisionID domain.ReadyWindowRevisionID
	NextWindowRevisionID     uuid.UUID
	NextReadinessRevisionID  uuid.UUID
	NextPresenceRevisionID   uuid.UUID
	NextMembershipID         uuid.UUID
	NextMembershipRevisionID uuid.UUID
	PrivateAssignments       []GoldenPrivateAssignmentCommand
}

type GoldenFailureReplayUseCase struct {
	repository FailureRepository
	clock      FailureClock
}

func NewGoldenFailureReplayUseCase(repository FailureRepository, clock FailureClock) *GoldenFailureReplayUseCase {
	return &GoldenFailureReplayUseCase{repository: repository, clock: clock}
}

func (u *GoldenFailureReplayUseCase) Replay(
	ctx context.Context,
	command GoldenFailureReplayCommand,
) (*GoldenFailureRecord, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil || !validGoldenFailureReplayCommand(command) {
		return nil, false, domain.ErrValidation
	}
	command.PrivateAssignments = append([]GoldenPrivateAssignmentCommand(nil), command.PrivateAssignments...)
	digest := goldenFailureReplayCommandDigest(command)
	if replay, err := u.repository.FindGoldenFailure(ctx, command.Scope.State.TournamentID, command.CommandID); err != nil {
		return nil, false, fmt.Errorf("GoldenFailureReplayUseCase - find replay: %w", err)
	} else if replay != nil {
		return reconcileGoldenFailure(*replay, command.Scope, command.CommandID, GoldenFailureRouteReplay, digest)
	}
	failedAt := u.clock.Now().Round(0).UTC()
	if !domain.IsValidServerTime(failedAt) {
		return nil, false, domain.ErrValidation
	}
	for range goldenFailureCommitAttempts {
		record, changed, retry, err := u.replayAttempt(ctx, command, digest, failedAt)
		if retry {
			continue
		}
		return record, changed, err
	}
	return nil, false, ErrGoldenFailureConflict
}

func (u *GoldenFailureReplayUseCase) replayAttempt(
	ctx context.Context,
	command GoldenFailureReplayCommand,
	digest [sha256.Size]byte,
	failedAt time.Time,
) (*GoldenFailureRecord, bool, bool, error) {
	if replay, err := u.repository.FindGoldenFailure(ctx, command.Scope.State.TournamentID, command.CommandID); err != nil {
		return nil, false, false, fmt.Errorf("GoldenFailureReplayUseCase - find attempt replay: %w", err)
	} else if replay != nil {
		result, changed, replayErr := reconcileGoldenFailure(
			*replay, command.Scope, command.CommandID, GoldenFailureRouteReplay, digest,
		)
		return result, changed, false, replayErr
	}
	authority, err := u.repository.LoadGoldenFailureAuthority(ctx, command.Scope)
	if err != nil {
		return nil, false, false, fmt.Errorf("GoldenFailureReplayUseCase - load authority: %w", err)
	}
	if authority.Validate() != nil {
		return nil, false, false, domain.ErrInternal
	}
	if !authority.Expectation().Equal(command.Expected) {
		return nil, false, false, ErrGoldenFailureAuthorityConflict
	}
	if authority.Current != nil || authority.Classification.Exhausted {
		return nil, false, false, ErrGoldenFailureRouteConflict
	}
	if failedAt.Before(authority.Active.StartedAt) {
		return nil, false, false, goldenFailureError("failure time precedes start")
	}
	record, err := buildGoldenFailureReplayRecord(authority, command, digest, failedAt)
	if err != nil {
		return nil, false, false, err
	}
	return commitGoldenFailure(ctx, u.repository, authority.Expectation(), record)
}
