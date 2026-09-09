package game

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
)

type OperatorReserveUseCase struct {
	repository OperatorReserveRepository
}

func NewOperatorReserveUseCase(
	repository OperatorReserveRepository,
) *OperatorReserveUseCase {
	return &OperatorReserveUseCase{
		repository: repository,
	}
}

func (u *OperatorReserveUseCase) Reserve(
	ctx context.Context,
	command OperatorReserveCommand,
) (*OperatorReserve, bool, error) {
	if u == nil || u.repository == nil {
		return nil, false, domain.ErrValidation
	}
	command.Reserve.Reason = strings.TrimSpace(command.Reserve.Reason)
	if err := validateOperatorReserveCommand(command); err != nil {
		return nil, false, err
	}
	for range operatorReserveAttempts {
		record, changed, retry, err := u.reserveAttempt(ctx, command)
		if retry {
			continue
		}
		return record, changed, err
	}
	return nil, false, ErrOperatorReserveConflict
}

func (u *OperatorReserveUseCase) reserveAttempt(
	ctx context.Context,
	command OperatorReserveCommand,
) (*OperatorReserve, bool, bool, error) {
	authority, err := u.repository.LoadOperatorReserveAuthority(ctx, command.Scope)
	if err != nil {
		return nil, false, false, fmt.Errorf("OperatorReserveUseCase - load authority: %w", err)
	}
	if err := validateOperatorReserveAuthority(authority); err != nil {
		return nil, false, false, err
	}
	if authority.Scope != command.Scope {
		return nil, false, false, operatorReserveError("authority scope does not match command")
	}
	if authority.Current != nil {
		current, reconcileErr := reconcileOperatorReserve(*authority.Current, command)
		return current, false, false, reconcileErr
	}
	record, err := buildOperatorReserve(command, authority)
	if err != nil {
		return nil, false, false, err
	}
	committed, changed, err := u.repository.CommitOperatorReserve(ctx, record)
	if errors.Is(err, domain.ErrConflict) {
		return nil, false, true, nil
	}
	if err != nil {
		return nil, false, false, fmt.Errorf("OperatorReserveUseCase - commit reserve: %w", err)
	}
	if !validCommittedOperatorReserve(committed, record, command, changed) {
		return nil, false, false, domain.ErrInternal
	}
	result := cloneOperatorReserve(*committed)
	return &result, changed, false, nil
}

func buildOperatorReserve(
	command OperatorReserveCommand,
	authority OperatorReserveAuthority,
) (OperatorReserve, error) {
	if authority.Exhaustion.CommandID != command.ExpectedExhaustionCommandID ||
		authority.Reserve.Revisions != command.ExpectedRevisions ||
		authority.Reserve.CurrentSnapshotID != command.Reserve.ExpectedSnapshotID ||
		authority.Reserve.CandidateSnapshot.TaskID != command.ProposedTaskID ||
		authority.Reserve.CandidateSnapshot.Version != command.ProposedVersion ||
		authority.Reserve.CandidateSnapshot.SnapshotID != command.ProposedSnapshotID {
		return OperatorReserve{}, ErrOperatorReserveConflict
	}
	reserve, err := assignmentusecase.BuildReserveAssignmentRecord(command.Reserve, authority.Reserve)
	if err != nil {
		return OperatorReserve{}, err
	}
	if reserve.Snapshot.Category != authority.Exhaustion.Category ||
		reserve.CategoryExhaustion != nil ||
		reserve.Evidence.Mode != assignmentusecase.ReserveAssignmentModeOperator {
		return OperatorReserve{}, operatorReserveError(
			"operator reserve changed category or evidence mode",
		)
	}
	series, changed, err := seriesdomain.Transition(
		authority.Exhaustion.Series,
		seriesdomain.TransitionCommand{NextState: domain.SeriesStateReplayRequired},
	)
	if err != nil || !changed {
		return OperatorReserve{}, operatorReserveError("resume Series: %v", err)
	}
	record := OperatorReserve{
		Scope:                     command.Scope,
		CommandID:                 command.CommandID,
		ExpectedAuthorityRevision: authority.Revision,
		Exhaustion:                cloneReplayReserveExhaustion(authority.Exhaustion),
		Reserve:                   reserve,
		Series:                    series,
	}
	if err := record.Validate(); err != nil {
		return OperatorReserve{}, err
	}
	return cloneOperatorReserve(record), nil
}
