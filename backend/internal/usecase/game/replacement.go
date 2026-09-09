package game

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
)

type ReplayReplacementUseCase struct {
	repository ReplayReplacementRepository
	clock      ReplayClock
}

func NewReplayReplacementUseCase(
	repository ReplayReplacementRepository,
	clock ReplayClock,
) *ReplayReplacementUseCase {
	return &ReplayReplacementUseCase{repository: repository, clock: clock}
}

func (u *ReplayReplacementUseCase) Replace(
	ctx context.Context,
	command ReplayReplacementCommand,
) (*ReplayReplacement, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil {
		return nil, false, domain.ErrValidation
	}
	if err := validateReplayReplacementCommand(command); err != nil {
		return nil, false, err
	}
	openedAt := u.clock.Now().Round(0).UTC()
	if !replayValidServerTime(openedAt) {
		return nil, false, domain.ErrValidation
	}
	for range replayReplacementAttempts {
		replacement, changed, retry, err := u.replaceAttempt(ctx, command, openedAt)
		if retry {
			continue
		}
		return replacement, changed, err
	}
	return nil, false, ErrReplayReplacementConflict
}

func (u *ReplayReplacementUseCase) replaceAttempt(
	ctx context.Context,
	command ReplayReplacementCommand,
	openedAt time.Time,
) (*ReplayReplacement, bool, bool, error) {
	authority, err := u.repository.LoadReplayReplacementAuthority(ctx, command.Scope)
	if err != nil {
		return nil, false, false, fmt.Errorf("ReplayReplacementUseCase - load authority: %w", err)
	}
	if authority.Current != nil {
		current, reconcileErr := reconcileReplayReplacement(*authority.Current, command)
		return current, false, false, reconcileErr
	}
	if err := validateReplayReplacementAuthority(authority); err != nil {
		return nil, false, false, err
	}
	if authority.Scope != command.Scope {
		return nil, false, false, replayReplacementError("authority scope does not match command")
	}
	if authority.ReserveChain.ActiveIndex+1 >= len(authority.ReserveChain.Snapshots) {
		return nil, false, false, ErrReplayReservesExhausted
	}
	replacement, err := buildReplayReplacement(command, authority, openedAt)
	if err != nil {
		return nil, false, false, err
	}
	committed, changed, err := u.repository.CommitReplayReplacement(ctx, replacement)
	if errors.Is(err, domain.ErrConflict) {
		return nil, false, true, nil
	}
	if err != nil {
		return nil, false, false, fmt.Errorf("ReplayReplacementUseCase - commit replacement: %w", err)
	}
	if !validCommittedReplayReplacement(committed, replacement, changed) {
		return nil, false, false, domain.ErrInternal
	}
	result := cloneReplayReplacement(*committed)
	return &result, changed, false, nil
}

func buildReplayReplacement(
	command ReplayReplacementCommand,
	authority ReplayReplacementAuthority,
	openedAt time.Time,
) (ReplayReplacement, error) {
	if authority.OldWaveClosure.Wave.RevisionID != command.ExpectedClosureRevisionID {
		return ReplayReplacement{}, ErrReplayReplacementConflict
	}
	if !freshReplayCommandIdentities(command, authority) {
		return ReplayReplacement{}, replayReplacementError("replacement reuses old identity")
	}
	nextIndex := authority.ReserveChain.ActiveIndex + 1
	if nextIndex >= len(authority.ReserveChain.Snapshots) {
		return ReplayReplacement{}, ErrReplayReservesExhausted
	}
	snapshot := cloneReplayTaskSnapshot(authority.ReserveChain.Snapshots[nextIndex])
	slot, err := replaySourceSlot(authority.FailedAttempt)
	if err != nil {
		return ReplayReplacement{}, err
	}
	openedSlot, changed, err := gamedomain.OpenSlotAttempt(slot, command.GameID)
	if err != nil || !changed {
		return ReplayReplacement{}, replayReplacementError("open Game attempt: %v", err)
	}
	replacementGame := replayCloneGame(openedSlot.Attempts[len(openedSlot.Attempts)-1])
	wave := domain.Wave{
		ID: command.WaveID, TournamentID: command.Scope.TournamentID,
		RevisionID: command.WaveRevisionID, State: domain.WaveStatePlanned,
		Members: []domain.WaveMember{
			{ParticipantID: authority.ParticipantIDs[0]},
			{ParticipantID: authority.ParticipantIDs[1]},
		},
	}
	if err := wave.OpenReadyWindow(
		command.ReadyWindowID,
		command.ReadyWindowRevisionID,
		openedAt,
		openedAt.Add(domain.ReadyWindowDuration),
	); err != nil {
		return ReplayReplacement{}, replayReplacementError("open ready window: %v", err)
	}
	replacement := ReplayReplacement{
		Scope: command.Scope, CommandID: command.CommandID,
		ExpectedAuthorityRevision: authority.Revision,
		ClosureRevisionID:         command.ExpectedClosureRevisionID,
		FromSnapshotID:            authority.FailedAttempt.ActiveSnapshotID,
		AssignmentAttemptID:       command.AssignmentAttemptID,
		ReservePosition:           nextIndex + 1, Snapshot: snapshot, Category: snapshot.Category,
		Slot: openedSlot, Game: replacementGame, Wave: wave, OpenedAt: openedAt,
	}
	if err := replacement.Validate(); err != nil {
		return ReplayReplacement{}, err
	}
	return cloneReplayReplacement(replacement), nil
}
