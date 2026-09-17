package recovery

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
)

func validateRecoveryCommand(command RecoveryCommand) error {
	if command.TournamentID == uuid.Nil || command.Authority.Validate() != nil ||
		command.Authority.TournamentID != command.TournamentID {
		return recoveryError("invalid recovery command")
	}
	return nil
}

func (recoverer *Recoverer) loadAuthorityAt(
	ctx context.Context,
	command RecoveryCommand,
	now time.Time,
) (*authoritydomain.Lease, error) {
	lease, err := recoverer.authority.LoadAuthority(ctx, command.TournamentID)
	if err != nil {
		return nil, fmt.Errorf("execution recovery - load authority: %w", err)
	}
	if lease == nil || !lease.Proves(command.Authority, now) {
		return nil, ErrNotAuthoritative
	}
	return lease, nil
}

func (recoverer *Recoverer) loadCandidates(
	ctx context.Context,
	command RecoveryCommand,
	currentStamp authoritydomain.Stamp,
) ([]RecoveryCandidate, error) {
	loadedCandidates, err := recoverer.source.ListActiveGames(ctx, command.TournamentID, command.Authority)
	if err != nil {
		return nil, fmt.Errorf("execution recovery - list active games: %w", err)
	}
	candidates := make([]RecoveryCandidate, len(loadedCandidates))
	seenGameIDs := make(map[uuid.UUID]struct{}, len(loadedCandidates))
	for index, loaded := range loadedCandidates {
		candidate := cloneRecoveryCandidate(loaded)
		if err := validateRecoveryCandidate(candidate, command, currentStamp, seenGameIDs); err != nil {
			return nil, err
		}
		candidates[index] = candidate
	}
	return candidates, nil
}

func validateRecoveryCandidate(
	candidate RecoveryCandidate,
	command RecoveryCommand,
	currentStamp authoritydomain.Stamp,
	seenGameIDs map[uuid.UUID]struct{},
) error {
	if err := validateRecoveryCandidateShape(candidate, command); err != nil {
		return err
	}
	if _, exists := seenGameIDs[candidate.Scope.GameID]; exists {
		return recoveryError("duplicate active game recovery candidate")
	}
	seenGameIDs[candidate.Scope.GameID] = struct{}{}
	brokenActive := candidate.State == domain.GameStateActive &&
		candidate.BoundAuthority != currentStamp
	// A paused attempt has no live deadline or recovery mutation. Its historical
	// binding is retained until the resume boundary fences and rebinds it, so a
	// normal restart must not turn a legitimate technical pause into permanent
	// startup failure.
	if brokenActive && (candidate.EpochReplay == nil ||
		!replayMatchesCandidate(*candidate.EpochReplay, candidate, command)) {
		return recoveryError("epoch break has no exact replay command")
	}
	return nil
}

func validateRecoveryCandidateShape(
	candidate RecoveryCandidate,
	command RecoveryCommand,
) error {
	if !candidate.Scope.IsValid() || candidate.Scope.TournamentID != command.TournamentID ||
		candidate.RosterID == uuid.Nil || candidate.AttemptNo < 1 || candidate.SnapshotID == uuid.Nil ||
		!candidate.Category.IsValid() || candidate.BoundAuthority.Validate() != nil ||
		candidate.Deadline.IsZero() || candidate.Deadline.Location() != time.UTC ||
		(candidate.State != domain.GameStateActive && candidate.State != domain.GameStatePaused) {
		return recoveryError("invalid active game recovery candidate")
	}
	return nil
}

func replayMatchesCandidate(
	replay EpochReplayCommand,
	candidate RecoveryCandidate,
	command RecoveryCommand,
) bool {
	return replay.Validate() == nil && replay.CurrentAuthority == command.Authority &&
		replay.BrokenAuthority == candidate.BoundAuthority &&
		replay.RosterID == candidate.RosterID &&
		replay.Attempt.Scope == candidate.Scope &&
		replay.Attempt.FailureClass == gamedomain.FailureExecutionEpochBreak &&
		replay.Attempt.Expected.AttemptNo == candidate.AttemptNo &&
		replay.Attempt.Expected.State == domain.GameStateActive &&
		replay.Attempt.Expected.SnapshotID == candidate.SnapshotID &&
		replay.Attempt.Expected.Category == candidate.Category
}

func cloneRecoveryCandidate(candidate RecoveryCandidate) RecoveryCandidate {
	clone := candidate
	if candidate.EpochReplay != nil {
		replay := *candidate.EpochReplay
		clone.EpochReplay = &replay
	}
	return clone
}

func recoveryError(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidRecovery, message)
}
