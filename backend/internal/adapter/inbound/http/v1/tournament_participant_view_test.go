package v1

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

func TestParticipantLobbyResponseIncludesAuthoritativeFields(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.New()
	participantID := uuid.New()
	round := 2
	view := usecase.LobbyView{
		TournamentID:       tournamentID,
		ParticipantID:      participantID,
		Attendance:         domain.AttendanceStateCheckedIn,
		CurrentSwissRound:  &round,
		SwissPoints:        3,
		Status:             usecase.LobbyStatusAssigned,
		RequiredAction:     usecase.LobbyRequiredActionPlay,
		ProjectionRevision: 4,
		State:              domain.TournamentStateSwiss,
	}

	lobby, err := participantLobbyResponse(view)

	require.NoError(t, err)
	require.Equal(t, api.CheckedIn, lobby.Attendance)
	require.Equal(t, participantID, lobby.ParticipantId)
	require.Equal(t, int32(round), *lobby.CurrentSwissRound)
	require.Equal(t, int32(3), lobby.SwissPoints)
	require.Equal(t, api.ParticipantLobbyStatusAssigned, lobby.Status)
	require.Equal(t, api.ParticipantLobbyRequiredActionPlay, lobby.RequiredAction)

	recovery, err := participantRecoveryResponse(usecase.RecoveryView{
		TournamentID:            tournamentID,
		ParticipantID:           participantID,
		ProjectionRevision:      4,
		ParticipantViewRevision: 5,
		EventSequence:           6,
		Lobby:                   view,
	})
	require.NoError(t, err)
	require.Equal(t, lobby, recovery.Lobby)
}

func TestParticipantWaveResponseRedactsSeriesForAuthoritativeBye(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.New()
	waveID := uuid.New()
	byeID := uuid.New()
	pairedID := uuid.New()
	seriesID := uuid.New()
	view := usecase.WaveView{
		Wave: domain.Wave{
			ID:           waveID,
			TournamentID: tournamentID,
			RevisionID:   domain.WaveRevisionID(uuid.New()),
			State:        domain.WaveStatePlanned,
			Members: []domain.WaveMember{
				{ParticipantID: byeID},
				{ParticipantID: pairedID},
			},
		},
		Revision:           1,
		ReadinessRevisions: map[uuid.UUID]int64{byeID: 1, pairedID: 1},
		SeriesIDs:          map[uuid.UUID]uuid.UUID{pairedID: seriesID},
		ByeParticipantID:   &byeID,
	}

	wave, err := participantWaveResponse(view)

	require.NoError(t, err)
	require.Len(t, wave.Members, 2)
	require.Nil(t, wave.Members[0].SeriesId)
	require.Equal(t, seriesID, *wave.Members[1].SeriesId)
}
