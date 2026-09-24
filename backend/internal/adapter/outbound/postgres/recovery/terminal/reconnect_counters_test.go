package terminal

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	reconnectusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/reconnect"
)

func TestReconnectCounterReceiptRejectsStaleAuthority(t *testing.T) {
	t.Parallel()
	current := reconnectusecase.ReconnectAuthority{
		Scope: pause.GraphScope{TournamentID: uuid.New(), RosterID: uuid.New(), WaveID: uuid.New()},
		Game:  domain.Game{ID: uuid.New(), State: domain.GameStatePaused}, Series: domain.Series{ID: uuid.New()},
		PauseID:  uuid.New(),
		Revision: 4, GameRevision: 5, SeriesRevision: 2, CurrentProjectionRevision: 3,
	}
	record := reconnectusecase.ReconnectRecord{
		ReconnectAuthority: current, ExpectedAuthorityRevision: 3, Kind: reconnectusecase.MutationDisconnect,
		RecordedAt: time.Date(2026, time.September, 23, 12, 0, 0, 0, time.UTC),
	}
	row := sqlc.ReconnectCommandReceipt{
		TournamentID: current.Scope.TournamentID, RosterID: current.Scope.RosterID, WaveID: current.Scope.WaveID,
		ResultAuthorityRevision: 4, ExpectedAuthorityRevision: 3, MutationKind: string(record.Kind), RecordedAt: tstz(record.RecordedAt),
	}
	require.True(t, reconnectCounterReceiptMatches(row, record, current))
	for name, mutate := range map[string]func(*reconnectusecase.ReconnectAuthority){
		"roster":              func(value *reconnectusecase.ReconnectAuthority) { value.Scope.RosterID = uuid.New() },
		"wave":                func(value *reconnectusecase.ReconnectAuthority) { value.Scope.WaveID = uuid.New() },
		"game revision":       func(value *reconnectusecase.ReconnectAuthority) { value.GameRevision++ },
		"series revision":     func(value *reconnectusecase.ReconnectAuthority) { value.SeriesRevision++ },
		"projection revision": func(value *reconnectusecase.ReconnectAuthority) { value.CurrentProjectionRevision++ },
		"pause":               func(value *reconnectusecase.ReconnectAuthority) { value.PauseID = uuid.New() },
		"game identity":       func(value *reconnectusecase.ReconnectAuthority) { value.Game.ID = uuid.New() },
		"series identity":     func(value *reconnectusecase.ReconnectAuthority) { value.Series.ID = uuid.New() },
		"terminal game":       func(value *reconnectusecase.ReconnectAuthority) { value.Game.State = domain.GameStateCompleted },
	} {
		t.Run(name, func(t *testing.T) {
			stale := current
			mutate(&stale)
			if name == "projection revision" {
				require.True(t, reconnectCounterReceiptMatches(row, record, stale))
				return
			}
			require.False(t, reconnectCounterReceiptMatches(row, record, stale))
		})
	}
}

func TestRestoreReconnectCountersKeepsCurrentProjectionRevision(t *testing.T) {
	t.Parallel()
	pauseID := uuid.New()
	rosterID := uuid.New()
	participants := []uuid.UUID{uuid.New(), uuid.New()}
	counters := []pause.PauseReconnectCounter{
		{PauseID: pauseID, RosterID: rosterID, ParticipantID: participants[0], Limit: 3, Used: 1, Revision: 2},
		{PauseID: pauseID, RosterID: rosterID, ParticipantID: participants[1], Limit: 3, Used: 0, Revision: 1},
	}
	record := reconnectusecase.ReconnectRecord{
		ReconnectAuthority: reconnectusecase.ReconnectAuthority{
			PauseID:  pauseID,
			Game:     domain.Game{ID: uuid.New(), State: domain.GameStatePaused},
			Series:   domain.Series{ID: uuid.New()},
			Revision: 4, Current: nil, CurrentProjectionRevision: 7,
			Counters: counters,
		},
	}
	currentProjectionRevision := int64(8)
	restored, err := RestoreReconnectCounters(counters[1:], nil, &record, pauseID, record.ReconnectAuthority.Revision)
	require.NoError(t, err)
	require.ElementsMatch(t, counters, restored)
	require.Equal(t, int64(7), record.ReconnectAuthority.CurrentProjectionRevision)
	require.NotEqual(t, record.ReconnectAuthority.CurrentProjectionRevision, currentProjectionRevision,
		"the current settlement revision is intentionally not replaced by the receipt revision")
}
