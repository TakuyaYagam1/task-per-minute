package recovery_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	gamereconnect "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/reconnect"
	gamemocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/reconnect/mocks"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery"
	recoverymocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery/mocks"
)

func TestTerminalDeadlineHandlerDoesNotObserveReconnectTimeoutWhenCommitFails(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 7, 16, 0, 0, 0, time.UTC)
	authority := reconnectDeadlineAuthority(now)
	store := recoverymocks.NewMockDeadlineTerminalStore(t)
	store.EXPECT().LoadDeadlineAuthority(mock.Anything, authority.Deadline).Return(authority, true, nil).Once()
	store.EXPECT().CommitDeadlinePlan(mock.Anything, mock.Anything).Return(false, errors.New("commit failed")).Once()
	clock := recoverymocks.NewMockClock(t)
	clock.EXPECT().Now().Return(now).Once()
	observer := gamemocks.NewMockObserver(t)

	changed, err := recovery.NewTerminalDeadlineHandler(store, clock, observer).HandleDeadline(t.Context(), authority.Deadline)

	require.False(t, changed)
	require.Error(t, err)
}

func TestTerminalDeadlineHandlerObservesReconnectTimeoutAfterCommit(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 7, 16, 0, 0, 0, time.UTC)
	authority := reconnectDeadlineAuthority(now)
	store := recoverymocks.NewMockDeadlineTerminalStore(t)
	store.EXPECT().LoadDeadlineAuthority(mock.Anything, authority.Deadline).Return(authority, true, nil).Once()
	store.EXPECT().CommitDeadlinePlan(mock.Anything, mock.Anything).Return(true, nil).Once()
	clock := recoverymocks.NewMockClock(t)
	clock.EXPECT().Now().Return(now).Once()
	observer := gamemocks.NewMockObserver(t)
	observer.EXPECT().Observe(mock.Anything, mock.MatchedBy(func(event gamereconnect.ReconnectEvent) bool {
		return event.ReconnectEvent == "tournament.command.reconnect_timeout" &&
			event.Outcome == gamereconnect.OutcomeSuccess &&
			event.CommandID != uuid.Nil &&
			event.TournamentID == authority.Deadline.TournamentID &&
			event.ParticipantID == authority.Deadline.ParticipantID &&
			event.Stage == "deadline" && event.Transition == "expire_reconnect" &&
			event.ReasonCode == "committed" && event.Revision > authority.ReconnectTimeout.Revision
	})).Once()

	changed, err := recovery.NewTerminalDeadlineHandler(store, clock, observer).HandleDeadline(t.Context(), authority.Deadline)

	require.True(t, changed)
	require.NoError(t, err)
}

func reconnectDeadlineAuthority(now time.Time) recovery.DeadlineTerminalAuthority {
	tournamentID := deadlineTerminalTestID(1)
	rosterID := deadlineTerminalTestID(2)
	waveID := deadlineTerminalTestID(3)
	seriesID := deadlineTerminalTestID(4)
	slotID := deadlineTerminalTestID(5)
	gameID := deadlineTerminalTestID(6)
	pauseID := deadlineTerminalTestID(7)
	firstParticipantID := deadlineTerminalTestID(8)
	secondParticipantID := deadlineTerminalTestID(9)
	intervalID := deadlineTerminalTestID(10)
	disconnectedAt := now.Add(-5 * time.Second)
	game := domain.Game{ID: gameID, SlotID: slotID, AttemptNo: 1, State: domain.GameStatePaused}
	series := domain.Series{
		ID: seriesID, TournamentID: tournamentID, FirstParticipantID: firstParticipantID,
		SecondParticipantID: secondParticipantID, Format: domain.SeriesFormatBO1, State: domain.SeriesStateActive,
		Slots: []domain.GameSlot{{
			ID: slotID, SeriesID: seriesID, Position: 1, Category: domain.CategoryWeb,
			Attempts: []domain.Game{game},
		}},
	}
	deadline := recovery.PendingDeadline{
		Kind: recovery.DeadlineKindReconnect, ID: intervalID, TournamentID: tournamentID, RosterID: rosterID,
		WaveID: waveID, SeriesID: seriesID, SlotID: slotID, GameID: gameID, PauseID: pauseID,
		ParticipantID: firstParticipantID, ExpectedRevision: 1, DueAt: now,
	}
	return recovery.DeadlineTerminalAuthority{
		Deadline: deadline,
		ReconnectTimeout: &gamereconnect.ReconnectAuthority{
			Scope: pause.GraphScope{
				TournamentID: tournamentID, RosterID: rosterID, WaveID: waveID,
				Authority: authoritydomain.Identity{
					TournamentID: tournamentID, HolderID: deadlineTerminalTestID(20),
					LeaseID: deadlineTerminalTestID(21), Epoch: 1, ProcessKind: authoritydomain.ProcessAuthority,
				},
			},
			Revision: 10, PauseID: pauseID, GameRevision: 5, SeriesRevision: 4,
			CurrentOrdinal: 7, CurrentProjectionRevision: 11,
			CurrentGameResultRevisionIDs: []domain.OfficialResultRevisionID{
				domain.OfficialResultRevisionID(deadlineTerminalTestID(22)),
			},
			Game: game, Series: series,
			GameClock: pause.PauseResumeGameClock{
				PauseID: pauseID, GameID: gameID, FrozenAt: disconnectedAt,
				Remaining: 40 * time.Second, OriginalDeadline: disconnectedAt.Add(40 * time.Second), Revision: 2,
			},
			Presence: []pause.PausePresence{
				{
					ID: deadlineTerminalTestID(11), TournamentID: tournamentID, RosterID: rosterID,
					SeriesID: seriesID, ParticipantID: firstParticipantID, State: pause.PresenceStateDisconnected,
					PresenceEpoch: 2, Revision: 2, ConnectedAt: now.Add(-time.Minute),
					DisconnectedAt: &disconnectedAt, UpdatedAt: disconnectedAt,
				},
				{
					ID: deadlineTerminalTestID(12), TournamentID: tournamentID, RosterID: rosterID,
					SeriesID: seriesID, ParticipantID: secondParticipantID, State: pause.PresenceStateConnected,
					PresenceEpoch: 1, Revision: 1, ConnectedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute),
				},
			},
			Reconnect: []pause.PauseReconnectInterval{{
				ID: intervalID, PauseID: pauseID, RosterID: rosterID, SeriesID: seriesID, GameID: gameID,
				ParticipantID: firstParticipantID, PresenceEpoch: 2, Number: 1, State: pause.ReconnectStateOpen,
				OpenedAt: disconnectedAt, Deadline: now, Revision: 1, UpdatedAt: disconnectedAt,
			}},
			Counters: []pause.PauseReconnectCounter{
				{PauseID: pauseID, RosterID: rosterID, ParticipantID: firstParticipantID, Limit: domain.ReconnectCycleLimit, Used: 1, Revision: 2},
				{PauseID: pauseID, RosterID: rosterID, ParticipantID: secondParticipantID, Limit: domain.ReconnectCycleLimit, Revision: 1},
			},
		},
	}
}

func deadlineTerminalTestID(value byte) uuid.UUID {
	var id uuid.UUID
	id[len(id)-1] = value
	return id
}
