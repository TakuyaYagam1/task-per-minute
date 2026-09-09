package assignment_test

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
)

func TestTaskEligibilityAcceptsExactPrivateHealthyVersion(t *testing.T) {
	t.Parallel()

	input := taskEligibilityFixture()
	decision, err := assignmentusecase.EvaluateTaskEligibility(input)
	require.NoError(t, err)
	require.True(t, decision.Eligible)
	require.Empty(t, decision.Reasons)
}

func TestTaskEligibilityRejectsEveryExclusionReason(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		reason assignmentusecase.TaskIneligibilityReason
		mutate func(*assignmentusecase.TaskEligibilityInput)
	}{
		{
			name:   "prior task receipt",
			reason: assignmentusecase.TaskIneligibilityPriorReceipt,
			mutate: func(input *assignmentusecase.TaskEligibilityInput) {
				input.ReceiptHistory = []assignmentusecase.TaskReceiptRef{{
					ParticipantID: input.ParticipantIDs[0],
					TaskID:        input.Candidate.Task.ID,
					Version:       input.Candidate.Version,
				}}
			},
		},
		{
			name:   "wrong pool identity",
			reason: assignmentusecase.TaskIneligibilityWrongPool,
			mutate: func(input *assignmentusecase.TaskEligibilityInput) {
				input.Candidate.Health.PoolRevisionID = task034ID(91)
			},
		},
		{
			name:   "wrong pool kind",
			reason: assignmentusecase.TaskIneligibilityWrongPool,
			mutate: func(input *assignmentusecase.TaskEligibilityInput) {
				input.Candidate.Health.PoolKind = domain.AssignmentTaskKindGolden
			},
		},
		{
			name:   "wrong category",
			reason: assignmentusecase.TaskIneligibilityWrongCategory,
			mutate: func(input *assignmentusecase.TaskEligibilityInput) {
				input.Candidate.Task.Category = domain.CategoryCrypto
			},
		},
		{
			name:   "missing version",
			reason: assignmentusecase.TaskIneligibilityMissing,
			mutate: func(input *assignmentusecase.TaskEligibilityInput) {
				input.Candidate.Health.Exists = false
			},
		},
		{
			name:   "disabled version",
			reason: assignmentusecase.TaskIneligibilityDisabled,
			mutate: func(input *assignmentusecase.TaskEligibilityInput) {
				input.Candidate.Health.Enabled = false
			},
		},
		{
			name:   "unhealthy version",
			reason: assignmentusecase.TaskIneligibilityUnhealthy,
			mutate: func(input *assignmentusecase.TaskEligibilityInput) {
				input.Candidate.Health.Healthy = false
			},
		},
		{
			name:   "mutable version",
			reason: assignmentusecase.TaskIneligibilityMutable,
			mutate: func(input *assignmentusecase.TaskEligibilityInput) {
				input.Candidate.Health.MutationLocked = false
			},
		},
		{
			name:   "public exposure",
			reason: assignmentusecase.TaskIneligibilityPublicExposure,
			mutate: func(input *assignmentusecase.TaskEligibilityInput) {
				input.Candidate.Health.PubliclyExposed = true
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			input := taskEligibilityFixture()
			test.mutate(&input)
			decision, err := assignmentusecase.EvaluateTaskEligibility(input)
			require.NoError(t, err)
			require.False(t, decision.Eligible)
			require.Contains(t, decision.Reasons, test.reason)
		})
	}
}

func taskEligibilityFixture() assignmentusecase.TaskEligibilityInput {
	poolID := task034ID(1)
	participantIDs := []uuid.UUID{task034ID(2), task034ID(3)}
	task := task034Task(4, domain.CategoryWeb)
	return assignmentusecase.TaskEligibilityInput{
		Pool: domain.TaskPoolRevision{
			ID: poolID, Revision: 7, Kind: domain.AssignmentTaskKindNormal,
			Versions: []domain.TaskVersionRef{{TaskID: task.ID, Version: 3}},
		},
		Category:       domain.CategoryWeb,
		ParticipantIDs: participantIDs,
		Candidate: assignmentusecase.TaskEligibilityCandidate{
			Version: 3,
			Task:    task,
			Health: domain.TaskVersionHealth{
				TaskID: task.ID, Version: 3, PoolRevisionID: poolID,
				PoolKind: domain.AssignmentTaskKindNormal, Exists: true, Enabled: true,
				Healthy: true, MutationLocked: true,
			},
		},
	}
}

func task034Task(id int, category domain.Category) domain.Task {
	return domain.Task{
		ID: task034ID(id), Title: fmt.Sprintf("Task %d", id),
		Description: "Solve the isolated challenge.", Category: category,
		Difficulty: domain.DifficultyHard, TimeLimit: 90,
		Flag: fmt.Sprintf("FLAG{%d}", id), Hints: []string{"first hint", "second hint"},
	}
}

func task034ID(value int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("00000000-0000-0000-0034-%012d", value))
}
