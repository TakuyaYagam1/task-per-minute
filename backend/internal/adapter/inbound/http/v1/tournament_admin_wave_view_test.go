package v1

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

func TestTournamentWaveResponseUsesNullSeriesForSwissBye(t *testing.T) {
	tournamentID := uuid.MustParse("72000000-0000-4000-8000-000000000001")
	waveID := uuid.MustParse("72000000-0000-4000-8000-000000000002")
	revisionID := uuid.MustParse("72000000-0000-4000-8000-000000000003")
	seriesID := uuid.MustParse("72000000-0000-4000-8000-000000000004")
	firstID := uuid.MustParse("72000000-0000-4000-8000-000000000011")
	secondID := uuid.MustParse("72000000-0000-4000-8000-000000000012")
	byeID := uuid.MustParse("72000000-0000-4000-8000-000000000013")
	view := inbound.AdminWaveView{
		Wave: domain.Wave{
			ID: waveID, TournamentID: tournamentID, RevisionID: domain.WaveRevisionID(revisionID),
			State: domain.WaveStatePlanned,
			Members: []domain.WaveMember{
				{ParticipantID: firstID}, {ParticipantID: secondID}, {ParticipantID: byeID},
			},
		},
		Revision:           1,
		ReadinessRevisions: map[uuid.UUID]int64{firstID: 1, secondID: 1, byeID: 1},
		SeriesIDs:          map[uuid.UUID]uuid.UUID{firstID: seriesID, secondID: seriesID},
		ByeParticipantID:   &byeID,
	}

	response, err := tournamentWaveResponse(view)

	require.NoError(t, err)
	require.Len(t, response.Members, 3)
	require.Equal(t, seriesID, *response.Members[0].SeriesId)
	require.Equal(t, seriesID, *response.Members[1].SeriesId)
	require.Nil(t, response.Members[2].SeriesId)
}
