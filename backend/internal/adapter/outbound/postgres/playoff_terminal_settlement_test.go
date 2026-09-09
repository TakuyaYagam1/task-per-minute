package postgres

import (
	"crypto/sha256"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
)

func TestFinalMaterializedBindingPrefix(t *testing.T) {
	t.Parallel()

	ids, err := playoff.FinalStageIdentity(uuid.New())
	require.NoError(t, err)
	bindings := []playoff.FinalGameBinding{
		terminalSettlementBindingFixture(ids, 1),
		terminalSettlementBindingFixture(ids, 2),
		terminalSettlementBindingFixture(ids, 3),
	}

	tests := []struct {
		name      string
		slotCount int
		actual    []playoff.FinalGameBinding
		wantErr   bool
	}{
		{name: "first settled game has first binding", slotCount: 1, actual: bindings[:1]},
		{name: "second settled game has first two bindings", slotCount: 2, actual: bindings[:2]},
		{name: "third settled game has all bindings", slotCount: 3, actual: bindings},
		{name: "missing earlier binding", slotCount: 2, actual: bindings[1:], wantErr: true},
		{name: "premature future binding", slotCount: 1, actual: bindings[:2], wantErr: true},
		{name: "duplicate binding", slotCount: 2, actual: []playoff.FinalGameBinding{bindings[0], bindings[0]}, wantErr: true},
		{name: "mismatched materialized binding", slotCount: 1, actual: []playoff.FinalGameBinding{func() playoff.FinalGameBinding {
			binding := bindings[0]
			binding.AssignmentID = uuid.New()
			return binding
		}()}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := validateFinalMaterializedBindingPrefix(
				ids,
				terminalSettlementSeriesFixture(ids, test.slotCount),
				test.actual,
			)

			if test.wantErr {
				require.ErrorIs(t, err, domain.ErrConflict)
				return
			}
			require.NoError(t, err)
		})
	}
}

func terminalSettlementSeriesFixture(ids playoff.FinalStageIDs, slotCount int) domain.Series {
	series := domain.Series{Slots: make([]domain.GameSlot, slotCount)}
	for index := range series.Slots {
		position := index + 1
		gameID, ok := terminalSettlementGameID(ids, position)
		if !ok {
			panic("invalid final game position")
		}
		series.Slots[index] = domain.GameSlot{
			ID:       terminalSettlementSlotID(ids, position),
			Position: position,
			Attempts: []domain.Game{{ID: gameID}},
		}
	}
	return series
}

func terminalSettlementBindingFixture(ids playoff.FinalStageIDs, position int) playoff.FinalGameBinding {
	gameID, ok := terminalSettlementGameID(ids, position)
	if !ok {
		panic("invalid final game position")
	}
	digest := sha256.Sum256([]byte{byte(position)})
	return playoff.FinalGameBinding{
		GameID:             gameID,
		AssignmentID:       ids.GameAssignmentID(position),
		AssignmentRevision: 1,
		PlanID:             ids.DraftAssignmentPlanID,
		PlanRevisionID:     ids.DraftAssignmentRevisionID,
		BranchID:           ids.DraftAssignmentChildBranchID("completed-final", position),
		ReservationID:      ids.DraftAssignmentReservationID("completed-final", position, 1),
		SnapshotID:         ids.DraftAssignmentSnapshotID("completed-final", position, 1),
		ContentDigest:      digest,
		DeadlineSeconds:    60,
	}
}

func terminalSettlementGameID(ids playoff.FinalStageIDs, position int) (uuid.UUID, bool) {
	switch position {
	case 1:
		return ids.FirstGameID, true
	case 2:
		return ids.SecondGameID, true
	case 3:
		return ids.ThirdGameID, true
	default:
		return uuid.Nil, false
	}
}

func terminalSettlementSlotID(ids playoff.FinalStageIDs, position int) uuid.UUID {
	switch position {
	case 1:
		return ids.FirstSlotID
	case 2:
		return ids.SecondSlotID
	case 3:
		return ids.ThirdSlotID
	default:
		panic("invalid final slot position")
	}
}
