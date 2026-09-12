package assignment

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain/capacity"
)

func TestExactNormalProofKeepsLegacyHistoryDigestAndIncludesPositiveVersion(t *testing.T) {
	t.Parallel()

	participantID := uuid.MustParse("00000000-0000-0000-0000-000000000021")
	taskID := uuid.MustParse("00000000-0000-0000-0000-000000000022")
	legacy := ExactNormalAssignmentPlan{
		History: []capacity.TaskUse{{ParticipantID: participantID, TaskID: taskID}},
	}
	legacyDigest, err := exactNormalAssignmentProofHash(legacy)
	require.NoError(t, err)

	serialized, err := json.Marshal(legacy.History)
	require.NoError(t, err)
	// These are the persisted encodings before versioned receipt history was
	// introduced. Recovery must continue to validate their existing hashes.
	require.JSONEq(t, `[{"ParticipantID":"00000000-0000-0000-0000-000000000021","TaskID":"00000000-0000-0000-0000-000000000022"}]`, string(serialized))
	proofHistory, err := json.Marshal(exactNormalHistoryDocuments(legacy.History))
	require.NoError(t, err)
	require.JSONEq(t, `[{"participant_id":"00000000-0000-0000-0000-000000000021","task_id":"00000000-0000-0000-0000-000000000022"}]`, string(proofHistory))
	var decoded []capacity.TaskUse
	require.NoError(t, json.Unmarshal(serialized, &decoded))
	legacy.History = decoded
	decodedDigest, err := exactNormalAssignmentProofHash(legacy)
	require.NoError(t, err)
	require.Equal(t, legacyDigest, decodedDigest)

	legacy.History[0].Version = 1
	versionedDigest, err := exactNormalAssignmentProofHash(legacy)
	require.NoError(t, err)
	require.NotEqual(t, legacyDigest, versionedDigest)
}
