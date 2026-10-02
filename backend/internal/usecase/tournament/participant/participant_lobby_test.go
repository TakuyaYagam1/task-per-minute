package participant

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

func TestDeriveParticipantLobbyState(t *testing.T) {
	t.Parallel()

	participantID := uuid.New()
	opponentID := uuid.New()
	base := usecase.RecoveryView{
		TournamentID:       uuid.New(),
		ParticipantID:      participantID,
		ProjectionRevision: 4,
		Lobby: usecase.LobbyView{
			TournamentID:       uuid.New(),
			ParticipantID:      participantID,
			Attendance:         domain.AttendanceStateCheckedIn,
			CurrentSwissRound:  intPointer(2),
			SwissPoints:        3,
			ProjectionRevision: 4,
			State:              domain.TournamentStateSwiss,
		},
	}
	base.Lobby.TournamentID = base.TournamentID

	tests := []struct {
		name   string
		mutate func(*usecase.RecoveryView)
		status usecase.LobbyStatus
		action usecase.LobbyRequiredAction
	}{
		{
			name: "waiting requires check in",
			mutate: func(view *usecase.RecoveryView) {
				view.Lobby.Attendance = domain.AttendanceStateRegistered
			},
			status: usecase.LobbyStatusWaiting, action: usecase.LobbyRequiredActionCheckIn,
		},
		{
			name: "ready wave",
			mutate: func(view *usecase.RecoveryView) {
				view.Wave = &usecase.WaveView{
					Wave: domain.Wave{
						Members:     []domain.WaveMember{{ParticipantID: participantID, Ready: false}},
						ReadyWindow: &domain.ReadyWindow{State: domain.ReadyWindowStateOpen},
					},
				}
			},
			status: usecase.LobbyStatusAssigned, action: usecase.LobbyRequiredActionReady,
		},
		{
			name: "completed BO3 game allows readiness for the next wave",
			mutate: func(view *usecase.RecoveryView) {
				view.Assignment = &usecase.TournamentParticipantAssignmentView{
					Context: usecase.ParticipantAssignmentContextView{GameState: domain.GameStateCompleted},
				}
				view.Series = &domain.Series{State: domain.SeriesStateReady, Format: domain.SeriesFormatBO3}
				view.Wave = &usecase.WaveView{Wave: domain.Wave{
					Members:     []domain.WaveMember{{ParticipantID: participantID}},
					ReadyWindow: &domain.ReadyWindow{State: domain.ReadyWindowStateOpen},
				}}
			},
			status: usecase.LobbyStatusAssigned, action: usecase.LobbyRequiredActionReady,
		},
		{
			name: "assigned draft",
			mutate: func(view *usecase.RecoveryView) {
				actor := participantID
				view.Draft = &usecase.DraftView{Execution: usecase.DraftExecutionView{
					State: usecase.DraftExecutionState("active"), CurrentActorID: &actor,
				}}
			},
			status: usecase.LobbyStatusAssigned, action: usecase.LobbyRequiredActionDraft,
		},
		{
			name: "assigned play",
			mutate: func(view *usecase.RecoveryView) {
				view.Assignment = &usecase.TournamentParticipantAssignmentView{}
			},
			status: usecase.LobbyStatusAssigned, action: usecase.LobbyRequiredActionPlay,
		},
		{
			name: "bye waits for next round",
			mutate: func(view *usecase.RecoveryView) {
				view.Wave = &usecase.WaveView{ByeParticipantID: &participantID}
			},
			status: usecase.LobbyStatusBye, action: usecase.LobbyRequiredActionWait,
		},
		{
			name: "completed Swiss result is reviewable",
			mutate: func(view *usecase.RecoveryView) {
				winner := opponentID
				view.Series = &domain.Series{State: domain.SeriesStateCompleted, WinnerID: &winner}
			},
			status: usecase.LobbyStatusAssigned, action: usecase.LobbyRequiredActionReviewResult,
		},
		{
			name: "eliminated playoff loser",
			mutate: func(view *usecase.RecoveryView) {
				view.Lobby.State = domain.TournamentStatePlayoffs
				winner := opponentID
				view.Series = &domain.Series{State: domain.SeriesStateCompleted, WinnerID: &winner}
			},
			status: usecase.LobbyStatusEliminated, action: usecase.LobbyRequiredActionNone,
		},
		{
			name: "completed tournament",
			mutate: func(view *usecase.RecoveryView) {
				view.Lobby.State = domain.TournamentStateCompleted
			},
			status: usecase.LobbyStatusCompleted, action: usecase.LobbyRequiredActionNone,
		},
		{
			name: "cancelled tournament",
			mutate: func(view *usecase.RecoveryView) {
				view.Lobby.State = domain.TournamentStateCancelled
			},
			status: usecase.LobbyStatusEliminated, action: usecase.LobbyRequiredActionNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			view := base
			view.Lobby.Series = nil
			view.Assignment = nil
			view.Draft = nil
			view.Series = nil
			view.Wave = nil
			tt.mutate(&view)

			status, action := deriveParticipantLobbyState(view)

			require.Equal(t, tt.status, status)
			require.Equal(t, tt.action, action)
		})
	}
}

func TestDeriveParticipantLobbyPreservesAuthoritativeIdentityAndScore(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.New()
	participantID := uuid.New()
	currentRound := 3
	view := usecase.RecoveryView{
		TournamentID:       tournamentID,
		ParticipantID:      participantID,
		ProjectionRevision: 7,
		Lobby: usecase.LobbyView{
			TournamentID:       tournamentID,
			ParticipantID:      participantID,
			Attendance:         domain.AttendanceStateCheckedIn,
			CurrentSwissRound:  &currentRound,
			SwissPoints:        2,
			ProjectionRevision: 7,
			State:              domain.TournamentStateSwiss,
		},
	}

	lobby, err := deriveParticipantLobby(view)

	require.NoError(t, err)
	require.Equal(t, participantID, lobby.ParticipantID)
	require.Equal(t, domain.AttendanceStateCheckedIn, lobby.Attendance)
	require.Equal(t, currentRound, *lobby.CurrentSwissRound)
	require.Equal(t, 2, lobby.SwissPoints)
	require.Equal(t, usecase.LobbyStatusWaiting, lobby.Status)
	require.Equal(t, usecase.LobbyRequiredActionWait, lobby.RequiredAction)
}

func intPointer(value int) *int {
	return &value
}
