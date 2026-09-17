package assignment_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	assignmentadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment"
	exactdraft "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment/exactdraft"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/capacity"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
)

func TestAssignmentHistoryMappingsPreserveExactTaskVersions(t *testing.T) {
	t.Parallel()

	first := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	second := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	task := uuid.MustParse("00000000-0000-0000-0000-000000000010")
	normal, err := assignmentadapter.ExactNormalHistory([]uuid.UUID{first, second}, []sqlc.LockExactNormalAssignmentHistoryRow{
		{ParticipantID: first, TaskID: task, TaskVersion: 2},
		{ParticipantID: first, TaskID: task, TaskVersion: 1},
		{ParticipantID: second, TaskID: task, TaskVersion: 3},
	})
	require.NoError(t, err)
	require.Equal(t, []capacity.TaskUse{
		{ParticipantID: first, TaskID: task, Version: 1},
		{ParticipantID: first, TaskID: task, Version: 2},
		{ParticipantID: second, TaskID: task, Version: 3},
	}, normal)

	draftHistory, err := exactdraft.ExactDraftPlanningHistory(draftusecase.Execution{
		FirstParticipantID: first, SecondParticipantID: second,
	}, []sqlc.LockExactDraftPlanningHistoryRow{
		{ParticipantID: second, TaskID: task, TaskVersion: 3},
		{ParticipantID: first, TaskID: task, TaskVersion: 1},
	})
	require.NoError(t, err)
	require.Equal(t, []capacity.TaskUse{
		{ParticipantID: first, TaskID: task, Version: 1},
		{ParticipantID: second, TaskID: task, Version: 3},
	}, draftHistory)
}

func TestRehydrateAssignmentChildHistoryRetainsLegacyWildcardVersion(t *testing.T) {
	t.Parallel()

	participantID := uuid.MustParse("00000000-0000-0000-0000-000000000003")
	taskID := uuid.MustParse("00000000-0000-0000-0000-000000000011")
	history, err := exactdraft.RehydrateExactDraftHistory([]sqlc.ExactDraftAssignmentChildHistory{
		{ParticipantID: participantID, TaskID: taskID, TaskVersion: 0},
	}, []assignmentusecase.ExactNormalParticipantReservation{{ParticipantID: participantID}})
	require.NoError(t, err)
	require.Equal(t, []capacity.TaskUse{{ParticipantID: participantID, TaskID: taskID}}, history)
}

func TestPlanningHistoryRejectsLegacyWildcardVersion(t *testing.T) {
	t.Parallel()

	first := uuid.MustParse("00000000-0000-0000-0000-000000000004")
	second := uuid.MustParse("00000000-0000-0000-0000-000000000005")
	task := uuid.MustParse("00000000-0000-0000-0000-000000000012")
	_, err := assignmentadapter.ExactNormalHistory([]uuid.UUID{first, second}, []sqlc.LockExactNormalAssignmentHistoryRow{
		{ParticipantID: first, TaskID: task, TaskVersion: 0},
	})
	require.Error(t, err)

	_, err = exactdraft.ExactDraftPlanningHistory(draftusecase.Execution{
		FirstParticipantID: first, SecondParticipantID: second,
	}, []sqlc.LockExactDraftPlanningHistoryRow{
		{ParticipantID: first, TaskID: task, TaskVersion: 0},
	})
	require.Error(t, err)
}
