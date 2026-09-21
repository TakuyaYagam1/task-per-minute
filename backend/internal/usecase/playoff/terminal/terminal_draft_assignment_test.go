package terminal

import (
	"crypto/sha256"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestReconcileFinalBindings_AllowsOnlyInitialAssignment(t *testing.T) {
	t.Parallel()

	ids, err := FinalStageIdentity(uuid.New())
	require.NoError(t, err)
	planned := []FinalGameBinding{
		terminalDraftBindingFixture(ids, 1),
		terminalDraftBindingFixture(ids, 2),
		terminalDraftBindingFixture(ids, 3),
	}

	bindings, err := reconcileFinalBindings(ids, planned, planned[:1], 1)

	require.NoError(t, err)
	require.Equal(t, planned, bindings)
}

func TestReconcileFinalBindings_RejectsMaterializedBindingMismatch(t *testing.T) {
	t.Parallel()

	ids, err := FinalStageIdentity(uuid.New())
	require.NoError(t, err)
	planned := []FinalGameBinding{
		terminalDraftBindingFixture(ids, 1),
		terminalDraftBindingFixture(ids, 2),
		terminalDraftBindingFixture(ids, 3),
	}
	actual := append([]FinalGameBinding(nil), planned[:1]...)
	actual[0].SnapshotID = uuid.New()

	_, err = reconcileFinalBindings(ids, planned, actual, 1)

	require.ErrorIs(t, err, domain.ErrConflict)
}

func TestReconcileFinalBindings_RequiresMaterializedPrefix(t *testing.T) {
	t.Parallel()

	ids, err := FinalStageIdentity(uuid.New())
	require.NoError(t, err)
	planned := []FinalGameBinding{
		terminalDraftBindingFixture(ids, 1),
		terminalDraftBindingFixture(ids, 2),
		terminalDraftBindingFixture(ids, 3),
	}

	tests := []struct {
		name    string
		actual  []FinalGameBinding
		prefix  int
		wantErr bool
	}{
		{name: "first game", actual: planned[:1], prefix: 1},
		{name: "second game", actual: planned[:2], prefix: 2},
		{name: "third game", actual: planned, prefix: 3},
		{name: "missing earlier game", actual: planned[1:], prefix: 2, wantErr: true},
		{name: "premature game", actual: planned[:2], prefix: 1, wantErr: true},
		{name: "duplicate game", actual: []FinalGameBinding{planned[0], planned[0]}, prefix: 2, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			bindings, err := reconcileFinalBindings(ids, planned, test.actual, test.prefix)

			if test.wantErr {
				require.ErrorIs(t, err, domain.ErrConflict)
				return
			}
			require.NoError(t, err)
			require.Equal(t, planned, bindings)
		})
	}
}

func terminalDraftBindingFixture(ids FinalStageIDs, position int) FinalGameBinding {
	gameID, ok := finalGameID(ids, position)
	if !ok {
		panic("invalid final game position")
	}
	digest := sha256.Sum256([]byte{byte(position)})
	return FinalGameBinding{
		GameID:             gameID,
		AssignmentID:       ids.GameAssignmentID(position),
		AssignmentRevision: 1,
		PlanID:             ids.DraftAssignmentPlanID,
		PlanRevisionID:     ids.DraftAssignmentRevisionID,
		BranchID:           ids.DraftAssignmentChildBranchID("completed-final", position),
		ReservationID:      ids.DraftAssignmentReservationID("completed-final", position, 1),
		SnapshotID:         ids.DraftAssignmentSnapshotID("completed-final", position, 1),
		ContentDigest:      digest,
		DeadlineSeconds:    180,
	}
}
