package assignment

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const reserveAssignmentAttempts = 2

type ReserveAssignmentUseCase struct {
	repository ReserveAssignmentRepository
}

func NewReserveAssignmentUseCase(repository ReserveAssignmentRepository) *ReserveAssignmentUseCase {
	return &ReserveAssignmentUseCase{repository: repository}
}

func (u *ReserveAssignmentUseCase) Promote(
	ctx context.Context,
	command ReserveAssignmentCommand,
) (*ReserveAssignmentRecord, bool, error) {
	if u == nil || u.repository == nil {
		return nil, false, domain.ErrValidation
	}
	command.Reason = strings.TrimSpace(command.Reason)
	if err := validateReserveAssignmentCommand(command); err != nil {
		return nil, false, err
	}

	for range reserveAssignmentAttempts {
		authority, err := u.repository.LoadReserveAssignmentAuthority(ctx, command.Scope)
		if err != nil {
			return nil, false, fmt.Errorf("ReserveAssignmentUseCase - load authority: %w", err)
		}
		record, err := buildReserveAssignmentRecord(command, authority)
		if err != nil {
			return nil, false, err
		}
		committed, changed, err := u.repository.CommitReserveAssignment(ctx, record)
		if errors.Is(err, domain.ErrConflict) {
			continue
		}
		if err != nil {
			return nil, false, fmt.Errorf("ReserveAssignmentUseCase - commit reserve: %w", err)
		}
		if committed == nil || committed.Validate() != nil || committed.ProofDigest != record.ProofDigest {
			return nil, false, domain.ErrInternal
		}
		result := cloneReserveAssignmentRecord(*committed)
		return &result, changed, nil
	}
	return nil, false, ErrReserveAssignmentConflict
}
