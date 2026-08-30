package arena_test

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestTaskEligibilityAcceptsExactPrivateHealthyVersionAndIgnoresCasualHistory(t *testing.T) {
	t.Parallel()

	input := taskEligibilityFixture()
	input.CasualHistory = []arena.CasualTaskSolve{{
		PlayerID: task034ID(90),
		TaskID:   input.Candidate.Task.ID,
	}}

	decision, err := arena.EvaluateTaskEligibility(input)
	require.NoError(t, err)
	require.True(t, decision.Eligible)
	require.Empty(t, decision.Reasons)
}

func TestTaskEligibilityRejectsEveryArenaExclusionReason(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		reason arena.TaskIneligibilityReason
		mutate func(*arena.TaskEligibilityInput)
	}{
		{
			name:   "prior Arena receipt",
			reason: arena.TaskIneligibilityPriorArenaReceipt,
			mutate: func(input *arena.TaskEligibilityInput) {
				input.ArenaHistory = []arena.ArenaTaskReceiptRef{{
					ParticipantID: input.ParticipantIDs[0],
					TaskID:        input.Candidate.Task.ID,
					Version:       input.Candidate.Version,
				}}
			},
		},
		{
			name:   "wrong pool identity",
			reason: arena.TaskIneligibilityWrongPool,
			mutate: func(input *arena.TaskEligibilityInput) {
				input.Candidate.Health.PoolRevisionID = task034ID(91)
			},
		},
		{
			name:   "wrong pool kind",
			reason: arena.TaskIneligibilityWrongPool,
			mutate: func(input *arena.TaskEligibilityInput) {
				input.Candidate.Health.PoolKind = domain.ArenaTaskKindGolden
			},
		},
		{
			name:   "wrong category",
			reason: arena.TaskIneligibilityWrongCategory,
			mutate: func(input *arena.TaskEligibilityInput) {
				input.Candidate.Task.Category = domain.CategoryCrypto
			},
		},
		{
			name:   "missing version",
			reason: arena.TaskIneligibilityMissing,
			mutate: func(input *arena.TaskEligibilityInput) {
				input.Candidate.Health.Exists = false
			},
		},
		{
			name:   "disabled version",
			reason: arena.TaskIneligibilityDisabled,
			mutate: func(input *arena.TaskEligibilityInput) {
				input.Candidate.Health.Enabled = false
			},
		},
		{
			name:   "unhealthy version",
			reason: arena.TaskIneligibilityUnhealthy,
			mutate: func(input *arena.TaskEligibilityInput) {
				input.Candidate.Health.Healthy = false
			},
		},
		{
			name:   "mutable version",
			reason: arena.TaskIneligibilityMutable,
			mutate: func(input *arena.TaskEligibilityInput) {
				input.Candidate.Health.MutationLocked = false
			},
		},
		{
			name:   "public exposure",
			reason: arena.TaskIneligibilityPublicExposure,
			mutate: func(input *arena.TaskEligibilityInput) {
				input.Candidate.Health.PubliclyExposed = true
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			input := taskEligibilityFixture()
			test.mutate(&input)
			decision, err := arena.EvaluateTaskEligibility(input)
			require.NoError(t, err)
			require.False(t, decision.Eligible)
			require.Contains(t, decision.Reasons, test.reason)
		})
	}
}

func taskEligibilityFixture() arena.TaskEligibilityInput {
	poolID := task034ID(1)
	participantIDs := []uuid.UUID{task034ID(2), task034ID(3)}
	task := task034Task(4, domain.CategoryWeb)
	return arena.TaskEligibilityInput{
		Pool: arena.TaskPoolRevision{
			ID: poolID, Revision: 7, Kind: domain.ArenaTaskKindNormal,
			Versions: []arena.TaskVersionRef{{TaskID: task.ID, Version: 3}},
		},
		Category:       domain.CategoryWeb,
		ParticipantIDs: participantIDs,
		Candidate: arena.TaskEligibilityCandidate{
			Version: 3,
			Task:    task,
			Health: arena.TaskVersionHealth{
				TaskID: task.ID, Version: 3, PoolRevisionID: poolID,
				PoolKind: domain.ArenaTaskKindNormal, Exists: true, Enabled: true,
				Healthy: true, MutationLocked: true,
			},
		},
	}
}

func task034Task(id int, category domain.Category) domain.Task {
	return domain.Task{
		ID: task034ID(id), Title: fmt.Sprintf("Arena task %d", id),
		Description: "Solve the isolated challenge.", Category: category,
		Difficulty: domain.DifficultyHard, TimeLimit: 90,
		Flag: fmt.Sprintf("FLAG{%d}", id), Hints: []string{"first hint", "second hint"},
	}
}

func task034ID(value int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("00000000-0000-0000-0034-%012d", value))
}
