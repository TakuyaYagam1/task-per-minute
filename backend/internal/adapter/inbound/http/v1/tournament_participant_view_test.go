package v1

import (
	"testing"
	"time"

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

func TestParticipantAssignmentResponseIncludesAuthoritativeContext(t *testing.T) {
	t.Parallel()

	assignmentID := uuid.New()
	participantID := uuid.New()
	waveID := uuid.New()
	seriesID := uuid.New()
	gameID := uuid.New()
	startedAt := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	deadline := startedAt.Add(120 * time.Second)
	view := usecase.TournamentParticipantAssignmentView{
		AssignmentID:  assignmentID,
		AttemptID:     uuid.New(),
		ParticipantID: participantID,
		SeriesID:      seriesID,
		GameID:        gameID,
		WaveID:        waveID,
		Context: usecase.ParticipantAssignmentContextView{
			WaveID:            waveID,
			SeriesID:          seriesID,
			SlotID:            uuid.New(),
			GameID:            gameID,
			Stage:             domain.TournamentStageFinal,
			GameNumber:        2,
			SeriesScore:       domain.SeriesScore{FirstParticipantWins: 1, SecondParticipantWins: 0},
			GameState:         domain.GameStateActive,
			StartedAt:         &startedAt,
			EffectiveDeadline: &deadline,
		},
		ActiveSnapshot: usecase.TaskSnapshotView{
			SnapshotID: uuid.New(), TaskID: uuid.New(), Version: 3,
			Kind: domain.AssignmentTaskKindNormal, Title: "web task",
			Description: "solve the disclosed service", Category: domain.CategoryWeb,
			Difficulty: domain.DifficultyMedium, TimeLimit: 120,
		},
	}

	assignment, err := participantAssignment(view)

	require.NoError(t, err)
	require.Equal(t, view.Context.WaveID, assignment.Context.WaveId)
	require.Equal(t, view.Context.SeriesID, assignment.Context.SeriesId)
	require.Equal(t, view.Context.SlotID, assignment.Context.SlotId)
	require.Equal(t, view.Context.GameID, assignment.Context.GameId)
	require.Equal(t, api.ParticipantAssignmentContextStageFinal, assignment.Context.Stage)
	require.Equal(t, int32(view.Context.GameNumber), assignment.Context.GameNumber)
	require.Equal(t, int32(1), assignment.Context.SeriesScore.FirstParticipantWins)
	require.Equal(t, int32(0), assignment.Context.SeriesScore.SecondParticipantWins)
	require.Equal(t, api.GameStateActive, assignment.Context.GameState)
	require.Equal(t, startedAt, *assignment.Context.StartedAt)
	require.Equal(t, deadline, *assignment.Context.EffectiveDeadline)
	require.Nil(t, assignment.Context.SwissRound)
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
