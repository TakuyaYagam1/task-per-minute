package lifecycle

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestLifecycleActionStateCoversEveryOrdinaryAdminAction(t *testing.T) {
	t.Parallel()
	tests := map[TournamentAction]domain.TournamentState{
		TournamentActionOpenRegistration: domain.TournamentStateRegistration,
		TournamentActionStartSwiss:       domain.TournamentStateSwiss,
		TournamentActionStartGolden:      domain.TournamentStateGolden,
		TournamentActionStartPlayoffs:    domain.TournamentStatePlayoffs,
		TournamentActionComplete:         domain.TournamentStateCompleted,
	}
	for action, expected := range tests {
		t.Run(string(action), func(t *testing.T) {
			t.Parallel()
			state, ok := lifecycleActionState(action)
			require.True(t, ok)
			require.Equal(t, expected, state)
		})
	}
	for _, guarded := range []TournamentAction{TournamentActionPause, TournamentActionResume, TournamentActionCancel} {
		_, ok := lifecycleActionState(guarded)
		require.False(t, ok)
	}
}

func TestValidateLifecycleExecutionSnapshotFailsClosed(t *testing.T) {
	t.Parallel()
	authority := LifecycleAuthority{ProjectionRevision: 7}
	valid := LifecycleExecutionSnapshot{
		Document: []byte(`{"waves":[],"incomplete_count":0}`), GraphRevision: 7,
		ExpectedChildren: 3, ObservedChildren: 3,
	}
	require.NoError(t, validateLifecycleExecutionSnapshot(valid, authority))

	invalid := []LifecycleExecutionSnapshot{
		{Document: valid.Document, GraphRevision: 6, ExpectedChildren: 3, ObservedChildren: 3},
		{Document: valid.Document, GraphRevision: 7, ExpectedChildren: 3, ObservedChildren: 2},
		{Document: valid.Document, GraphRevision: 7, ExpectedChildren: 3, ObservedChildren: 3, IncompleteChildren: 1},
		{Document: valid.Document, GraphRevision: 7, ExpectedChildren: 3, ObservedChildren: 3, ActiveGolden: true},
		{Document: []byte(`[]`), GraphRevision: 7, ExpectedChildren: 3, ObservedChildren: 3},
	}
	for _, snapshot := range invalid {
		require.ErrorIs(t, validateLifecycleExecutionSnapshot(snapshot, authority), domain.ErrConflict)
	}
}
