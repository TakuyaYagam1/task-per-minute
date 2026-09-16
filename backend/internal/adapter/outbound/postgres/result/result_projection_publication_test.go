package result

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
)

func TestResultProjectionTargetUsesNextImmutableRevision(t *testing.T) {
	t.Parallel()

	targetID := uuid.New()
	source := sqlc.LockResultSourceProjectionRow{ID: uuid.New(), RevisionNumber: 7}
	target, ok := resultProjectionTarget(source, targetID)

	require.True(t, ok)
	require.Equal(t, targetID, target.ID)
	require.Equal(t, int64(8), target.Revision)
	require.NotEqual(t, source.ID, target.ID)
}

func TestResultProjectionTargetRejectsInvalidSourceOrTarget(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name   string
		source sqlc.LockResultSourceProjectionRow
		target uuid.UUID
	}{
		{name: "missing source", source: sqlc.LockResultSourceProjectionRow{RevisionNumber: 1}, target: uuid.New()},
		{name: "missing target", source: sqlc.LockResultSourceProjectionRow{ID: uuid.New(), RevisionNumber: 1}},
		{name: "same revision", source: sqlc.LockResultSourceProjectionRow{ID: uuid.New(), RevisionNumber: 0}, target: uuid.New()},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			_, ok := resultProjectionTarget(testCase.source, testCase.target)
			require.False(t, ok)
		})
	}
}
