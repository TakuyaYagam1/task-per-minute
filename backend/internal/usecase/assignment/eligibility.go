package assignment

import (
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var ErrInvalidTaskEligibility = errors.New("invalid task eligibility input")

type TaskIneligibilityReason string

const (
	TaskIneligibilityPriorReceipt   TaskIneligibilityReason = "prior_task_receipt"
	TaskIneligibilityWrongPool      TaskIneligibilityReason = "wrong_pool"
	TaskIneligibilityWrongCategory  TaskIneligibilityReason = "wrong_category"
	TaskIneligibilityMissing        TaskIneligibilityReason = "missing"
	TaskIneligibilityDisabled       TaskIneligibilityReason = "disabled"
	TaskIneligibilityUnhealthy      TaskIneligibilityReason = "unhealthy"
	TaskIneligibilityMutable        TaskIneligibilityReason = "mutable"
	TaskIneligibilityPublicExposure TaskIneligibilityReason = "public_exposure"
)

type TaskReceiptRef struct {
	ParticipantID uuid.UUID
	TaskID        uuid.UUID
	Version       int
}

type TaskEligibilityCandidate struct {
	Version int
	Task    domain.Task
	Health  domain.TaskVersionHealth
}

type TaskEligibilityInput struct {
	Pool           domain.TaskPoolRevision
	Category       domain.Category
	ParticipantIDs []uuid.UUID
	Candidate      TaskEligibilityCandidate
	ReceiptHistory []TaskReceiptRef
}

type TaskEligibilityDecision struct {
	Eligible bool
	Reasons  []TaskIneligibilityReason
}

func EvaluateTaskEligibility(input TaskEligibilityInput) (TaskEligibilityDecision, error) {
	participants, pool, err := validateTaskEligibilityInput(input)
	if err != nil {
		return TaskEligibilityDecision{}, err
	}

	reasons := make([]TaskIneligibilityReason, 0, 8)
	if hasPriorReceipt(participants, input.Candidate.Task.ID, input.Candidate.Version, input.ReceiptHistory) {
		reasons = append(reasons, TaskIneligibilityPriorReceipt)
	}
	if !poolContainsTaskVersion(pool, input.Candidate.Task.ID, input.Candidate.Version) ||
		input.Candidate.Health.PoolRevisionID != pool.ID ||
		input.Candidate.Health.PoolKind != pool.Kind {
		reasons = append(reasons, TaskIneligibilityWrongPool)
	}
	if input.Candidate.Task.Category != input.Category {
		reasons = append(reasons, TaskIneligibilityWrongCategory)
	}
	if !input.Candidate.Health.Exists {
		reasons = append(reasons, TaskIneligibilityMissing)
	}
	if !input.Candidate.Health.Enabled {
		reasons = append(reasons, TaskIneligibilityDisabled)
	}
	if !input.Candidate.Health.Healthy {
		reasons = append(reasons, TaskIneligibilityUnhealthy)
	}
	if !input.Candidate.Health.MutationLocked {
		reasons = append(reasons, TaskIneligibilityMutable)
	}
	if input.Candidate.Health.PubliclyExposed {
		reasons = append(reasons, TaskIneligibilityPublicExposure)
	}
	return TaskEligibilityDecision{Eligible: len(reasons) == 0, Reasons: reasons}, nil
}

func validateTaskEligibilityInput(
	input TaskEligibilityInput,
) ([]uuid.UUID, domain.TaskPoolRevision, error) {
	if !input.Category.IsValid() || !input.Pool.Kind.IsValid() ||
		input.Candidate.Version < 1 || input.Candidate.Task.ID == uuid.Nil ||
		input.Candidate.Health.TaskID != input.Candidate.Task.ID ||
		input.Candidate.Health.Version != input.Candidate.Version ||
		!validTaskEligibilityContent(input.Candidate.Task) {
		return nil, domain.TaskPoolRevision{}, taskEligibilityError("invalid candidate identity or content")
	}
	pool, err := domain.NormalizeTaskPoolRevision(input.Pool, input.Pool.Kind)
	if err != nil {
		return nil, domain.TaskPoolRevision{}, taskEligibilityError("invalid task pool: %v", err)
	}
	participants, err := normalizeExactNormalParticipants(input.ParticipantIDs)
	if err != nil {
		return nil, domain.TaskPoolRevision{}, taskEligibilityError("invalid participants: %v", err)
	}
	for _, receipt := range input.ReceiptHistory {
		if receipt.ParticipantID == uuid.Nil || receipt.TaskID == uuid.Nil || receipt.Version < 1 ||
			!slices.Contains(participants, receipt.ParticipantID) {
			return nil, domain.TaskPoolRevision{}, taskEligibilityError("invalid task receipt history")
		}
	}
	return participants, pool, nil
}

func validTaskEligibilityContent(task domain.Task) bool {
	return domain.IsValidTaskTitle(task.Title) &&
		domain.IsValidTaskDescription(task.Description) &&
		task.Category.IsValid() && task.Difficulty.IsValid() &&
		domain.IsValidTaskTimeLimit(task.TimeLimit) &&
		domain.IsValidTaskFlag(task.Flag) &&
		domain.IsValidTaskHints(task.Hints) &&
		domain.IsValidTaskURLShape(task.Category, task.TaskURL, task.SourceFileURL)
}

func hasPriorReceipt(
	participants []uuid.UUID,
	taskID uuid.UUID,
	version int,
	history []TaskReceiptRef,
) bool {
	for _, receipt := range history {
		if receipt.TaskID == taskID && receipt.Version == version && slices.Contains(participants, receipt.ParticipantID) {
			return true
		}
	}
	return false
}

func poolContainsTaskVersion(pool domain.TaskPoolRevision, taskID uuid.UUID, version int) bool {
	return slices.Contains(pool.Versions, domain.TaskVersionRef{TaskID: taskID, Version: version})
}

func taskEligibilityError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidTaskEligibility, fmt.Sprintf(format, arguments...))
}
