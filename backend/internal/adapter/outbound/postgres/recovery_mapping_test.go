package postgres

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery"
)

func TestMapRecoveryDeadlinePreservesEachDomainShape(t *testing.T) {
	t.Parallel()

	dueAt := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	common := recoveryDeadlineRow{
		id: recoveryRowID(10), tournamentID: recoveryRowID(1), rosterID: recoveryRowID(2),
		waveID: recoveryRowID(3), expectedRevision: 4,
		dueAt: pgtype.Timestamptz{Time: dueAt, Valid: true},
	}
	tests := []struct {
		name string
		row  recoveryDeadlineRow
		kind recovery.DeadlineKind
	}{
		{name: "game", kind: recovery.DeadlineKindGame, row: func() recoveryDeadlineRow {
			row := common
			row.kind = string(recovery.DeadlineKindGame)
			row.seriesID = recoveryRowID(4)
			row.slotID = recoveryRowID(5)
			row.gameID = row.id
			return row
		}()},
		{name: "ready window", kind: recovery.DeadlineKindReadyWindow, row: func() recoveryDeadlineRow {
			row := common
			row.kind = string(recovery.DeadlineKindReadyWindow)
			row.readyWindowRevisionID = recoveryRowID(11)
			return row
		}()},
		{name: "reconnect", kind: recovery.DeadlineKindReconnect, row: func() recoveryDeadlineRow {
			row := common
			row.kind = string(recovery.DeadlineKindReconnect)
			row.seriesID = recoveryRowID(4)
			row.slotID = recoveryRowID(5)
			row.gameID = recoveryRowID(6)
			row.pauseID = recoveryRowID(7)
			row.participantID = recoveryRowID(8)
			return row
		}()},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			deadline, err := mapRecoveryDeadline(test.row)
			require.NoError(t, err)
			require.Equal(t, test.kind, deadline.Kind)
			require.Equal(t, dueAt, deadline.DueAt)
			require.NoError(t, deadline.Validate())
		})
	}
}

func TestMapRecoveryDeadlineRejectsInvalidDatabaseEvidence(t *testing.T) {
	t.Parallel()

	_, err := mapRecoveryDeadline(recoveryDeadlineRow{
		kind: string(recovery.DeadlineKindGame), id: recoveryRowID(10),
		tournamentID: recoveryRowID(1), rosterID: recoveryRowID(2),
		waveID: recoveryRowID(3), seriesID: recoveryRowID(4),
		slotID: recoveryRowID(5), gameID: recoveryRowID(10),
		expectedRevision: 1,
	})

	require.ErrorIs(t, err, domain.ErrInternal)
}

func recoveryRowID(value byte) uuid.UUID {
	var id uuid.UUID
	id[len(id)-1] = value
	return id
}
