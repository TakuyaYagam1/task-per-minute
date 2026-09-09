package admin

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
)

type ReplayWorkflow struct {
	transactions ExecutionTransactionManager
	repository   ReplayWorkflowRepository
}

func NewReplayWorkflow(deps ReplayWorkflowDependencies) *ReplayWorkflow {
	return &ReplayWorkflow{transactions: deps.Transactions, repository: deps.Repository}
}

func (w *ReplayWorkflow) AssignReserve(ctx context.Context, command ReserveCommand) error {
	if ctx == nil || !validReserveCommand(command) {
		return domain.ErrValidation
	}
	if !w.available() {
		return domain.ErrInternal
	}
	command.Reason = strings.TrimSpace(command.Reason)
	digest, err := replayWorkflowDigest("operator_reserve", command)
	if err != nil {
		return err
	}
	return w.transactions.Do(ctx, func(txCtx context.Context) error {
		promotedAt, err := w.replayTime(txCtx, command.CommandID)
		if err != nil {
			return err
		}
		operatorRepository := &operatorReserveRepository{
			repository: w.repository,
			command:    command,
			digest:     digest,
		}
		reserved, _, err := gameusecase.NewOperatorReserveUseCase(operatorRepository).Reserve(
			txCtx,
			operatorReserveCommand(command, promotedAt),
		)
		if err != nil {
			return operatorRepository.mapError(err, command.ExpectedAuthorityRevision)
		}
		if reserved == nil || reserved.CommandID != command.CommandID ||
			reserved.Exhaustion.CommandID != command.ExpectedExhaustionCommandID {
			return domain.ErrInternal
		}
		return nil
	})
}

func (w *ReplayWorkflow) ReplayGame(ctx context.Context, command ReplayCommand) error {
	if ctx == nil || !validReplayCommand(command) {
		return domain.ErrValidation
	}
	if !w.available() {
		return domain.ErrInternal
	}
	command.Reason = strings.TrimSpace(command.Reason)
	digest, err := replayWorkflowDigest("replacement", command)
	if err != nil {
		return err
	}
	return w.transactions.Do(ctx, func(txCtx context.Context) error {
		openedAt, err := w.replayTime(txCtx, command.CommandID)
		if err != nil {
			return err
		}
		repository := &replayReplacementRepository{
			repository: w.repository,
			command:    command,
			digest:     digest,
		}
		replacement, _, err := gameusecase.NewReplayReplacementUseCase(
			repository,
			replayWorkflowClock{at: openedAt},
		).Replace(txCtx, replayReplacementCommand(command))
		if err != nil {
			return repository.mapError(err, command.ExpectedAuthorityRevision)
		}
		if replacement == nil || replacement.CommandID != command.CommandID ||
			replacement.Game.ID != command.ReplacementGameID ||
			replacement.Wave.ID != command.ReplacementWaveID {
			return domain.ErrInternal
		}
		return nil
	})
}

func (w *ReplayWorkflow) available() bool {
	return w != nil && w.transactions != nil && w.repository != nil
}

func (w *ReplayWorkflow) replayTime(ctx context.Context, commandID uuid.UUID) (time.Time, error) {
	value, err := w.repository.ReadReplayTime(ctx, commandID)
	if err != nil {
		return time.Time{}, err
	}
	value = value.Round(0).UTC()
	if !domain.IsValidServerTime(value) {
		return time.Time{}, domain.ErrInternal
	}
	return value, nil
}

func replayScope(tournamentID, oldWaveID, seriesID, slotID, assignmentID uuid.UUID) gameusecase.ReplayReplacementScope {
	return gameusecase.ReplayReplacementScope{
		TournamentID: tournamentID,
		OldWaveID:    oldWaveID,
		SeriesID:     seriesID,
		SlotID:       slotID,
		AssignmentID: assignmentID,
	}
}

func operatorReserveCommand(
	command ReserveCommand,
	promotedAt time.Time,
) gameusecase.OperatorReserveCommand {
	scope := replayScope(command.TournamentID, command.OldWaveID, command.SeriesID, command.SlotID, command.AssignmentID)
	return gameusecase.OperatorReserveCommand{
		Scope:                       scope,
		CommandID:                   command.CommandID,
		ExpectedExhaustionCommandID: command.ExpectedExhaustionCommandID,
		ExpectedRevisions: assignmentusecase.ReserveAssignmentSourceRevisions{
			AssignmentRevision: command.ExpectedAssignmentRevision,
			PoolRevisionID:     command.ExpectedPoolRevisionID, PoolRevision: command.ExpectedPoolRevision,
			HistoryRevisionID: command.ExpectedHistoryRevisionID, HistoryRevision: command.ExpectedHistoryRevision,
			ArtifactRevisionID: command.ExpectedArtifactRevisionID, ArtifactRevision: command.ExpectedArtifactRevision,
			ReservationRevisionID: command.ExpectedReservationRevisionID,
			ReservationRevision:   command.ExpectedReservationRevision,
			CategoryRevisionID:    command.ExpectedCategoryRevisionID, CategoryRevision: command.ExpectedCategoryRevision,
		},
		ProposedTaskID:     command.ProposedTaskID,
		ProposedVersion:    command.ProposedVersion,
		ProposedSnapshotID: command.ProposedSnapshotID,
		Reserve: assignmentusecase.ReserveAssignmentCommand{
			Scope: assignmentusecase.ReserveAssignmentScope{
				TournamentID: command.TournamentID,
				AssignmentID: command.AssignmentID,
				AttemptID:    command.AssignmentAttemptID,
				SlotID:       command.SlotID,
			},
			ExpectedSnapshotID: command.ExpectedSnapshotID,
			EvidenceID:         command.EvidenceID,
			Mode:               assignmentusecase.ReserveAssignmentModeOperator,
			OperatorID:         command.Operator.ActorID,
			Reason:             command.Reason,
			PromotedAt:         promotedAt,
		},
	}
}

func replayReplacementCommand(command ReplayCommand) gameusecase.ReplayReplacementCommand {
	return gameusecase.ReplayReplacementCommand{
		Scope:                     replayScope(command.TournamentID, command.OldWaveID, command.SeriesID, command.SlotID, command.AssignmentID),
		CommandID:                 command.CommandID,
		ExpectedClosureRevisionID: domain.WaveRevisionID(command.ExpectedClosureRevisionID),
		AssignmentAttemptID:       command.AssignmentAttemptID,
		GameID:                    command.ReplacementGameID,
		WaveID:                    command.ReplacementWaveID,
		WaveRevisionID:            domain.WaveRevisionID(command.ReplacementWaveRevisionID),
		ReadyWindowID:             command.ReadyWindowID,
		ReadyWindowRevisionID:     domain.ReadyWindowRevisionID(command.ReadyWindowRevisionID),
	}
}

type replayWorkflowClock struct{ at time.Time }

func (c replayWorkflowClock) Now() time.Time { return c.at }

type operatorReserveRepository struct {
	repository      ReplayWorkflowRepository
	command         ReserveCommand
	digest          [32]byte
	currentRevision int64
	currentState    domain.TournamentState
}

func (r *operatorReserveRepository) LoadOperatorReserveAuthority(
	ctx context.Context,
	_ gameusecase.ReplayReplacementScope,
) (gameusecase.OperatorReserveAuthority, error) {
	authority, err := r.repository.LoadOperatorReserveAuthority(ctx, r.command)
	if err != nil {
		return gameusecase.OperatorReserveAuthority{}, err
	}
	r.currentRevision = authority.Replay.Revision
	r.currentState = authority.TournamentState
	if authority.Replay.Current == nil && authority.Replay.Revision != r.command.ExpectedAuthorityRevision {
		return gameusecase.OperatorReserveAuthority{}, newReplayWorkflowConflict(
			r.command.ExpectedAuthorityRevision,
			r.currentRevision,
			r.currentState,
		)
	}
	return authority.Replay, nil
}

func (r *operatorReserveRepository) CommitOperatorReserve(
	ctx context.Context,
	record gameusecase.OperatorReserve,
) (*gameusecase.OperatorReserve, bool, error) {
	return r.repository.CommitOperatorReserve(ctx, r.command, r.digest, record)
}

func (r *operatorReserveRepository) mapError(err error, expected int64) error {
	return mapReplayWorkflowError(err, expected, r.currentRevision, r.currentState)
}

type replayReplacementRepository struct {
	repository      ReplayWorkflowRepository
	command         ReplayCommand
	digest          [32]byte
	currentRevision int64
	currentState    domain.TournamentState
}

func (r *replayReplacementRepository) LoadReplayReplacementAuthority(
	ctx context.Context,
	_ gameusecase.ReplayReplacementScope,
) (gameusecase.ReplayReplacementAuthority, error) {
	authority, err := r.repository.LoadReplayReplacementAuthority(ctx, r.command)
	if err != nil {
		return gameusecase.ReplayReplacementAuthority{}, err
	}
	r.currentRevision = authority.Replay.Revision
	r.currentState = authority.TournamentState
	if authority.Replay.Current == nil && authority.Replay.Revision != r.command.ExpectedAuthorityRevision {
		return gameusecase.ReplayReplacementAuthority{}, newReplayWorkflowConflict(
			r.command.ExpectedAuthorityRevision,
			r.currentRevision,
			r.currentState,
		)
	}
	return authority.Replay, nil
}

func (r *replayReplacementRepository) CommitReplayReplacement(
	ctx context.Context,
	replacement gameusecase.ReplayReplacement,
) (*gameusecase.ReplayReplacement, bool, error) {
	return r.repository.CommitReplayReplacement(ctx, r.command, r.digest, replacement)
}

func (r *replayReplacementRepository) mapError(err error, expected int64) error {
	return mapReplayWorkflowError(err, expected, r.currentRevision, r.currentState)
}

func replayWorkflowDigest(action string, command any) ([sha256.Size]byte, error) {
	payload, err := json.Marshal(struct {
		Action  string `json:"action"`
		Command any    `json:"command"`
	}{Action: action, Command: command})
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("ReplayWorkflow - encode command: %w", err)
	}
	return sha256.Sum256(payload), nil
}

func mapReplayWorkflowError(
	err error,
	expected int64,
	current int64,
	state domain.TournamentState,
) error {
	if errors.Is(err, domain.ErrConflict) ||
		errors.Is(err, gameusecase.ErrReplayReserveExhaustionConflict) ||
		errors.Is(err, gameusecase.ErrReplayReserveExhaustionReuse) ||
		errors.Is(err, gameusecase.ErrOperatorReserveConflict) ||
		errors.Is(err, gameusecase.ErrOperatorReserveReuse) ||
		errors.Is(err, gameusecase.ErrReplayReplacementConflict) ||
		errors.Is(err, gameusecase.ErrReplayReplacementReuse) {
		return newReplayWorkflowConflict(expected, current, state)
	}
	return err
}

func newReplayWorkflowConflict(expected, current int64, state domain.TournamentState) error {
	if current < 1 || !state.IsValid() {
		return domain.ErrInternal
	}
	return &RevisionConflictError{
		ExpectedRevision: expected,
		CurrentRevision:  current,
		CurrentState:     state,
	}
}

var (
	_ ReservePort                             = (*ReplayWorkflow)(nil)
	_ ReplayPort                              = (*ReplayWorkflow)(nil)
	_ gameusecase.OperatorReserveRepository   = (*operatorReserveRepository)(nil)
	_ gameusecase.ReplayReplacementRepository = (*replayReplacementRepository)(nil)
)
