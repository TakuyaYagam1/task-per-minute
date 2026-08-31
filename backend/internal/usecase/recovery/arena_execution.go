package recovery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

var (
	ErrInvalidArenaExecutionRecovery  = errors.New("invalid Arena execution recovery")
	ErrArenaExecutionNotAuthoritative = errors.New("arena recovery is not authoritative")
)

type ArenaExecutionRecoveryCommand struct {
	TournamentID uuid.UUID
	Authority    arena.ExecutionAuthorityIdentity
}

type ArenaActiveGameRecovery struct {
	Scope          arena.FailedAttemptScope
	AttemptNo      int
	State          domain.ArenaGameState
	SnapshotID     uuid.UUID
	Category       domain.Category
	BoundAuthority arena.ExecutionAuthorityStamp
	Deadline       time.Time
	EpochReplay    *arena.ExecutionEpochReplayCommand
}

type ArenaGameDeadlineArm struct {
	Scope     arena.FailedAttemptScope
	AttemptNo int
	Authority arena.ExecutionAuthorityStamp
	Deadline  time.Time
}

type ArenaExecutionRecoveryReport struct {
	Rearmed          int
	TechnicalReplays int
	Paused           int
	Changed          int
}

type ArenaExecutionAuthorityReader interface {
	LoadExecutionAuthority(
		ctx context.Context,
		tournamentID uuid.UUID,
	) (*arena.ExecutionAuthorityLease, error)
}

type ArenaActiveGameRecoverySource interface {
	ListActiveArenaGames(
		ctx context.Context,
		tournamentID uuid.UUID,
	) ([]ArenaActiveGameRecovery, error)
}

// ArenaGameDeadlineRearmer owns the deadline-arm linearization boundary. It
// must atomically fence by Tournament scope and authority stamp and prove that
// the matching durable lease is live using authoritative time before arming
// the exact persisted deadline.
type ArenaGameDeadlineRearmer interface {
	RearmArenaGameDeadline(arm ArenaGameDeadlineArm) error
}

type ArenaExecutionEpochReplayer interface {
	ReplayExecutionEpoch(
		ctx context.Context,
		command arena.ExecutionEpochReplayCommand,
	) (bool, error)
}

type ArenaExecutionRecoverer struct {
	authority ArenaExecutionAuthorityReader
	source    ArenaActiveGameRecoverySource
	timers    ArenaGameDeadlineRearmer
	replayer  ArenaExecutionEpochReplayer
	clock     Clock
}

func NewArenaExecutionRecoverer(
	authority ArenaExecutionAuthorityReader,
	source ArenaActiveGameRecoverySource,
	timers ArenaGameDeadlineRearmer,
	replayer ArenaExecutionEpochReplayer,
	clock Clock,
) *ArenaExecutionRecoverer {
	return &ArenaExecutionRecoverer{
		authority: authority, source: source, timers: timers, replayer: replayer, clock: clock,
	}
}

func (r *ArenaExecutionRecoverer) Recover(
	ctx context.Context,
	command ArenaExecutionRecoveryCommand,
) (ArenaExecutionRecoveryReport, error) {
	if !r.available() {
		return ArenaExecutionRecoveryReport{}, domain.ErrValidation
	}
	if err := validateArenaExecutionRecoveryCommand(command); err != nil {
		return ArenaExecutionRecoveryReport{}, err
	}
	now := r.clock.Now().Round(0).UTC()
	if now.IsZero() {
		return ArenaExecutionRecoveryReport{}, domain.ErrValidation
	}
	lease, err := r.loadArenaExecutionAuthority(ctx, command, now)
	if err != nil {
		return ArenaExecutionRecoveryReport{}, err
	}
	candidates, err := r.loadArenaExecutionCandidates(ctx, command, *lease.Stamp())
	if err != nil {
		return ArenaExecutionRecoveryReport{}, err
	}
	return r.recoverArenaExecutionCandidates(ctx, candidates, *lease.Stamp())
}

func (r *ArenaExecutionRecoverer) available() bool {
	return r != nil && r.authority != nil && r.source != nil && r.timers != nil &&
		r.replayer != nil && r.clock != nil
}

func validateArenaExecutionRecoveryCommand(command ArenaExecutionRecoveryCommand) error {
	if command.TournamentID == uuid.Nil || command.Authority.Validate() != nil ||
		command.Authority.TournamentID != command.TournamentID {
		return arenaExecutionRecoveryError("invalid recovery command")
	}
	return nil
}

func (r *ArenaExecutionRecoverer) loadArenaExecutionAuthority(
	ctx context.Context,
	command ArenaExecutionRecoveryCommand,
	now time.Time,
) (*arena.ExecutionAuthorityLease, error) {
	lease, err := r.authority.LoadExecutionAuthority(ctx, command.TournamentID)
	if err != nil {
		return nil, fmt.Errorf("ArenaExecutionRecoverer - load authority: %w", err)
	}
	if lease == nil || !lease.Proves(command.Authority, now) {
		return nil, ErrArenaExecutionNotAuthoritative
	}
	return lease, nil
}

func (r *ArenaExecutionRecoverer) loadArenaExecutionCandidates(
	ctx context.Context,
	command ArenaExecutionRecoveryCommand,
	currentStamp arena.ExecutionAuthorityStamp,
) ([]ArenaActiveGameRecovery, error) {
	loadedCandidates, err := r.source.ListActiveArenaGames(ctx, command.TournamentID)
	if err != nil {
		return nil,
			fmt.Errorf("ArenaExecutionRecoverer - list active Games: %w", err)
	}
	candidates := make([]ArenaActiveGameRecovery, len(loadedCandidates))
	seenGameIDs := make(map[uuid.UUID]struct{}, len(loadedCandidates))
	for index, loaded := range loadedCandidates {
		candidate := cloneArenaActiveGameRecovery(loaded)
		if err := validateArenaExecutionBatchCandidate(
			candidate,
			command,
			currentStamp,
			seenGameIDs,
		); err != nil {
			return nil, err
		}
		candidates[index] = candidate
	}
	return candidates, nil
}

func validateArenaExecutionBatchCandidate(
	candidate ArenaActiveGameRecovery,
	command ArenaExecutionRecoveryCommand,
	currentStamp arena.ExecutionAuthorityStamp,
	seenGameIDs map[uuid.UUID]struct{},
) error {
	if err := validateArenaActiveGameRecovery(candidate, command); err != nil {
		return err
	}
	if _, exists := seenGameIDs[candidate.Scope.GameID]; exists {
		return arenaExecutionRecoveryError("duplicate active Game recovery candidate")
	}
	seenGameIDs[candidate.Scope.GameID] = struct{}{}
	brokenActive := candidate.State == domain.ArenaGameStateActive &&
		candidate.BoundAuthority != currentStamp
	if brokenActive && (candidate.EpochReplay == nil ||
		!arenaExecutionReplayMatchesCandidate(*candidate.EpochReplay, candidate, command)) {
		return arenaExecutionRecoveryError("epoch break has no exact replay command")
	}
	return nil
}

func (r *ArenaExecutionRecoverer) recoverArenaExecutionCandidates(
	ctx context.Context,
	candidates []ArenaActiveGameRecovery,
	currentStamp arena.ExecutionAuthorityStamp,
) (ArenaExecutionRecoveryReport, error) {
	report := ArenaExecutionRecoveryReport{}
	for _, candidate := range candidates {
		candidateReport, err := r.recoverArenaExecutionCandidate(ctx, candidate, currentStamp)
		if err != nil {
			return ArenaExecutionRecoveryReport{}, err
		}
		report.Rearmed += candidateReport.Rearmed
		report.TechnicalReplays += candidateReport.TechnicalReplays
		report.Paused += candidateReport.Paused
		report.Changed += candidateReport.Changed
	}
	return report, nil
}

func (r *ArenaExecutionRecoverer) recoverArenaExecutionCandidate(
	ctx context.Context,
	candidate ArenaActiveGameRecovery,
	currentStamp arena.ExecutionAuthorityStamp,
) (ArenaExecutionRecoveryReport, error) {
	if candidate.State == domain.ArenaGameStatePaused {
		return ArenaExecutionRecoveryReport{Paused: 1}, nil
	}
	if candidate.BoundAuthority == currentStamp {
		if err := r.timers.RearmArenaGameDeadline(ArenaGameDeadlineArm{
			Scope: candidate.Scope, AttemptNo: candidate.AttemptNo,
			Authority: candidate.BoundAuthority, Deadline: candidate.Deadline,
		}); err != nil {
			return ArenaExecutionRecoveryReport{},
				fmt.Errorf("ArenaExecutionRecoverer - rearm Game deadline: %w", err)
		}
		return ArenaExecutionRecoveryReport{Rearmed: 1}, nil
	}
	changed, err := r.replayer.ReplayExecutionEpoch(ctx, *candidate.EpochReplay)
	if err != nil {
		return ArenaExecutionRecoveryReport{},
			fmt.Errorf("ArenaExecutionRecoverer - replay execution epoch: %w", err)
	}
	report := ArenaExecutionRecoveryReport{TechnicalReplays: 1}
	if changed {
		report.Changed = 1
	}
	return report, nil
}

func validateArenaActiveGameRecovery(
	candidate ArenaActiveGameRecovery,
	command ArenaExecutionRecoveryCommand,
) error {
	if !candidate.Scope.IsValid() || candidate.Scope.TournamentID != command.TournamentID ||
		candidate.AttemptNo < 1 || candidate.SnapshotID == uuid.Nil ||
		!candidate.Category.IsValid() || candidate.BoundAuthority.Validate() != nil ||
		candidate.Deadline.IsZero() || candidate.Deadline.Location() != time.UTC ||
		(candidate.State != domain.ArenaGameStateActive &&
			candidate.State != domain.ArenaGameStatePaused) {
		return arenaExecutionRecoveryError("invalid active Game recovery candidate")
	}
	return nil
}

func arenaExecutionReplayMatchesCandidate(
	replay arena.ExecutionEpochReplayCommand,
	candidate ArenaActiveGameRecovery,
	command ArenaExecutionRecoveryCommand,
) bool {
	return replay.Validate() == nil && replay.CurrentAuthority == command.Authority &&
		replay.BrokenAuthority == candidate.BoundAuthority && replay.Attempt.Scope == candidate.Scope &&
		replay.Attempt.FailureClass == arena.NormalAttemptFailureExecutionEpochBreak &&
		replay.Attempt.Expected.AttemptNo == candidate.AttemptNo &&
		replay.Attempt.Expected.State == domain.ArenaGameStateActive &&
		replay.Attempt.Expected.SnapshotID == candidate.SnapshotID &&
		replay.Attempt.Expected.Category == candidate.Category
}

func cloneArenaActiveGameRecovery(candidate ArenaActiveGameRecovery) ArenaActiveGameRecovery {
	clone := candidate
	if candidate.EpochReplay != nil {
		replay := *candidate.EpochReplay
		clone.EpochReplay = &replay
	}
	return clone
}

func arenaExecutionRecoveryError(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidArenaExecutionRecovery, message)
}
