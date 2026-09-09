package game

import (
	"context"
	"errors"
	"fmt"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
)

type ReplayReserveExhaustionUseCase struct {
	repository ReplayReserveExhaustionRepository
}

func NewReplayReserveExhaustionUseCase(
	repository ReplayReserveExhaustionRepository,
) *ReplayReserveExhaustionUseCase {
	return &ReplayReserveExhaustionUseCase{repository: repository}
}

func (u *ReplayReserveExhaustionUseCase) Pause(
	ctx context.Context,
	command ReplayReserveExhaustionCommand,
) (*ReplayReserveExhaustion, bool, error) {
	if u == nil || u.repository == nil {
		return nil, false, domain.ErrValidation
	}
	if err := validateReplayReserveExhaustionCommand(command); err != nil {
		return nil, false, err
	}
	for range replayReserveExhaustionAttempts {
		record, changed, retry, err := u.pauseAttempt(ctx, command)
		if retry {
			continue
		}
		return record, changed, err
	}
	return nil, false, ErrReplayReserveExhaustionConflict
}

func (u *ReplayReserveExhaustionUseCase) pauseAttempt(
	ctx context.Context,
	command ReplayReserveExhaustionCommand,
) (*ReplayReserveExhaustion, bool, bool, error) {
	authority, err := u.repository.LoadReplayReserveExhaustionAuthority(ctx, command.Scope)
	if err != nil {
		return nil, false, false, fmt.Errorf(
			"ReplayReserveExhaustionUseCase - load authority: %w",
			err,
		)
	}
	if err := validateReplayReserveExhaustionAuthority(authority); err != nil {
		return nil, false, false, err
	}
	if authority.Scope != command.Scope {
		return nil, false, false, replayReserveExhaustionError(
			"authority scope does not match command",
		)
	}
	if authority.Current != nil {
		current, reconcileErr := reconcileReplayReserveExhaustion(*authority.Current, command)
		return current, false, false, reconcileErr
	}
	record, err := buildReplayReserveExhaustion(command, authority)
	if err != nil {
		return nil, false, false, err
	}
	committed, changed, err := u.repository.CommitReplayReserveExhaustion(ctx, record)
	if errors.Is(err, domain.ErrConflict) {
		return nil, false, true, nil
	}
	if err != nil {
		return nil, false, false, fmt.Errorf(
			"ReplayReserveExhaustionUseCase - commit pause: %w",
			err,
		)
	}
	if !validCommittedReplayReserveExhaustion(committed, record, command, changed) {
		return nil, false, false, domain.ErrInternal
	}
	result := cloneReplayReserveExhaustion(*committed)
	return &result, changed, false, nil
}

func buildReplayReserveExhaustion(
	command ReplayReserveExhaustionCommand,
	authority ReplayReserveExhaustionAuthority,
) (ReplayReserveExhaustion, error) {
	if authority.OldWaveClosure.Wave.RevisionID != command.ExpectedClosureRevisionID ||
		authority.FailedAttempt.ActiveSnapshotID != command.ExpectedActiveSnapshotID {
		return ReplayReserveExhaustion{}, ErrReplayReserveExhaustionConflict
	}
	paused, changed, err := seriesdomain.ResolveCompetitive(
		authority.FailedAttempt.Series,
		seriesdomain.ResolutionCommand{
			Route: seriesdomain.CompetitiveSeriesRouteReplayExhausted,
		},
	)
	if err != nil || !changed {
		return ReplayReserveExhaustion{}, replayReserveExhaustionError("pause Series: %v", err)
	}
	record := ReplayReserveExhaustion{
		Scope:                     command.Scope,
		CommandID:                 command.CommandID,
		ExpectedAuthorityRevision: authority.Revision,
		ClosureRevisionID:         command.ExpectedClosureRevisionID,
		FailedAttemptCommandID:    authority.FailedAttempt.CommandID,
		AssignmentAttemptID:       authority.FailedAttempt.Scope.AssignmentAttemptID,
		GameID:                    authority.FailedAttempt.Scope.GameID,
		ActiveSnapshotID:          authority.FailedAttempt.ActiveSnapshotID,
		ReservePosition:           authority.ReserveChain.ActiveIndex + 1,
		Category:                  authority.FailedAttempt.Failure.CategoryCutoff,
		PreviousSeries:            cloneSeriesExecution(authority.FailedAttempt.Series),
		Series:                    paused,
		OldWave:                   replayCloneWaveExecution(authority.OldWaveClosure.Wave),
	}
	if err := record.Validate(); err != nil {
		return ReplayReserveExhaustion{}, err
	}
	return cloneReplayReserveExhaustion(record), nil
}
