package snapshot

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestValidRecoveryControlRequiresOrderedAttemptsAndLatestFailure(t *testing.T) {
	t.Parallel()

	seriesID, slotID := uuid.New(), uuid.New()
	control := RecoveryControl{
		AssignmentID: uuid.New(), SeriesID: seriesID, SlotID: slotID, OldWaveID: uuid.New(),
		Category: domain.CategoryWeb, ExpectedAuthorityRevision: 4, Kind: RecoveryControlReplay,
		Reason: "recover failed attempt", Replay: &RecoveryReplayDetails{Available: true, ExpectedClosureRevisionID: uuid.New()},
		Attempts: []domain.Game{
			recoveryValidationGame(slotID, 1, domain.GameResultReasonNoSolve),
			recoveryValidationGame(slotID, 2, domain.GameResultReasonTaskFailure),
		},
	}
	require.True(t, validRecoveryControl(control))

	control.Attempts[0].AttemptNo = 3
	require.False(t, validRecoveryControl(control))

	control.Attempts[0].AttemptNo = 1
	control.Attempts[1].AttemptNo = 3
	require.False(t, validRecoveryControl(control))

	control.Attempts[1].AttemptNo = 2
	control.Attempts[1].State = domain.GameStateCompleted
	control.Attempts[1].ResultReason = domain.GameResultReasonSolved
	winner := uuid.New()
	control.Attempts[1].WinnerID = &winner
	require.False(t, validRecoveryControl(control))

	control.Attempts[1].State = domain.GameStateVoid
	control.Attempts[1].ResultReason = domain.GameResultReasonTaskFailure
	control.Attempts[1].WinnerID = nil
	require.True(t, validRecoveryControl(control))
	require.False(t, validSnapshotRecoveryControls([]RecoveryControl{control, control}))
}

func recoveryValidationGame(slotID uuid.UUID, attemptNo int, reason domain.GameResultReason) domain.Game {
	revision := domain.OfficialResultRevisionID(uuid.New())
	return domain.Game{ID: uuid.New(), SlotID: slotID, AttemptNo: attemptNo, State: domain.GameStateVoid, ResultReason: reason, ResultRevisionID: &revision}
}
