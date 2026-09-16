package result

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResultSequenceAllocatorUsesDurablePerAttemptCursors(t *testing.T) {
	t.Parallel()

	migration, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "db", "migrations", "000008_result_schema.sql"))
	require.NoError(t, err)
	resultQueries, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "db", "queries", "result.sql"))
	require.NoError(t, err)
	participantQueries, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "db", "queries", "participant_command.sql"))
	require.NoError(t, err)
	operatorQueries, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "db", "queries", "operator_result.sql"))
	require.NoError(t, err)

	for _, fragment := range []string{
		"result_event_sequence bigint DEFAULT 0 NOT NULL",
		"submission_event_sequence bigint DEFAULT 0 NOT NULL",
		"NEW.server_sequence <> attempt_row.result_event_sequence",
		"NEW.server_sequence <> attempt_row.submission_event_sequence",
	} {
		require.Contains(t, string(migration), fragment)
	}
	for _, fragment := range []string{
		"-- name: AllocateResultEventSequence :one",
		"SET result_event_sequence = attempt.result_event_sequence + 1",
		"-- name: AllocateSubmissionEventSequence :one",
		"SET submission_event_sequence = attempt.submission_event_sequence + 1",
	} {
		require.Contains(t, string(resultQueries), fragment)
	}

	require.NotContains(t, string(participantQueries), "MAX(submission.server_sequence)")
	require.NotContains(t, string(participantQueries), "MAX(result.server_sequence)")
	require.NotContains(t, string(operatorQueries), "GetOperatorResultServerSequence")
	require.NotContains(t, string(operatorQueries), "MAX(result_event.server_sequence)")
}
