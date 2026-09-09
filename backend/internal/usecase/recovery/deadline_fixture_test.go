package recovery_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery"
	recoverymocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery/mocks"
)

func recoveryDeadlineID(value byte) uuid.UUID {
	var id uuid.UUID
	id[len(id)-1] = value
	return id
}

func gameDeadline(at time.Time, revision int64) recovery.PendingDeadline {
	return recovery.PendingDeadline{
		Kind: recovery.DeadlineKindGame, ID: recoveryDeadlineID(10),
		TournamentID: recoveryDeadlineID(1), RosterID: recoveryDeadlineID(2),
		WaveID: recoveryDeadlineID(3), SeriesID: recoveryDeadlineID(4),
		SlotID: recoveryDeadlineID(5), GameID: recoveryDeadlineID(10),
		ExpectedRevision: revision, DueAt: at.Round(0).UTC(),
	}
}

func readyWindowDeadline(at time.Time, revision int64) recovery.PendingDeadline {
	return recovery.PendingDeadline{
		Kind: recovery.DeadlineKindReadyWindow, ID: recoveryDeadlineID(20),
		TournamentID: recoveryDeadlineID(1), RosterID: recoveryDeadlineID(2),
		WaveID: recoveryDeadlineID(6), ReadyWindowRevisionID: recoveryDeadlineID(21),
		ExpectedRevision: revision, DueAt: at.Round(0).UTC(),
	}
}

func reconnectDeadline(at time.Time, revision int64) recovery.PendingDeadline {
	return recovery.PendingDeadline{
		Kind: recovery.DeadlineKindReconnect, ID: recoveryDeadlineID(30),
		TournamentID: recoveryDeadlineID(1), RosterID: recoveryDeadlineID(2),
		WaveID: recoveryDeadlineID(3), SeriesID: recoveryDeadlineID(4),
		SlotID: recoveryDeadlineID(5), GameID: recoveryDeadlineID(10),
		PauseID: recoveryDeadlineID(31), ParticipantID: recoveryDeadlineID(32),
		ExpectedRevision: revision, DueAt: at.Round(0).UTC(),
	}
}

func newDeadlineSweep(
	t *testing.T,
) (*recovery.DeadlineSweep, *recoverymocks.MockDeadlineSource, *recoverymocks.MockDeadlineRearmer) {
	t.Helper()
	source := recoverymocks.NewMockDeadlineSource(t)
	rearmer := recoverymocks.NewMockDeadlineRearmer(t)
	return recovery.NewDeadlineSweep(source, rearmer), source, rearmer
}

func allowEmptyDeadlineSweeps(source *recoverymocks.MockDeadlineSource) {
	source.EXPECT().ListPendingDeadlines(mock.Anything, mock.Anything, mock.Anything).
		Return([]recovery.PendingDeadline{}, nil).Maybe()
}
