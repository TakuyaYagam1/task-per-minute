package admin

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestValidWaveViewSupportsSwissBye(t *testing.T) {
	tournamentID := uuid.MustParse("71000000-0000-4000-8000-000000000001")
	waveID := uuid.MustParse("71000000-0000-4000-8000-000000000002")
	revisionID := uuid.MustParse("71000000-0000-4000-8000-000000000003")
	firstSeriesID := uuid.MustParse("71000000-0000-4000-8000-000000000004")
	secondSeriesID := uuid.MustParse("71000000-0000-4000-8000-000000000005")
	participants := []uuid.UUID{
		uuid.MustParse("71000000-0000-4000-8000-000000000011"),
		uuid.MustParse("71000000-0000-4000-8000-000000000012"),
		uuid.MustParse("71000000-0000-4000-8000-000000000013"),
		uuid.MustParse("71000000-0000-4000-8000-000000000014"),
		uuid.MustParse("71000000-0000-4000-8000-000000000015"),
	}
	byeID := participants[4]
	view := WaveView{
		Wave: domain.Wave{
			ID: waveID, TournamentID: tournamentID, RevisionID: domain.WaveRevisionID(revisionID),
			State: domain.WaveStatePlanned,
			Members: []domain.WaveMember{
				{ParticipantID: participants[0]}, {ParticipantID: participants[1]},
				{ParticipantID: participants[2]}, {ParticipantID: participants[3]},
				{ParticipantID: participants[4]},
			},
		},
		Revision: 1,
		ReadinessRevisions: map[uuid.UUID]int64{
			participants[0]: 1, participants[1]: 1, participants[2]: 1,
			participants[3]: 1, participants[4]: 1,
		},
		SeriesIDs: map[uuid.UUID]uuid.UUID{
			participants[0]: firstSeriesID, participants[1]: firstSeriesID,
			participants[2]: secondSeriesID, participants[3]: secondSeriesID,
		},
		ByeParticipantID: &byeID,
	}

	require.True(t, validWaveView(view, tournamentID, waveID))

	view.SeriesIDs[byeID] = firstSeriesID
	require.False(t, validWaveView(view, tournamentID, waveID))
	delete(view.SeriesIDs, byeID)

	foreignBye := uuid.MustParse("71000000-0000-4000-8000-000000000099")
	view.ByeParticipantID = &foreignBye
	require.False(t, validWaveView(view, tournamentID, waveID))
}
