package correction_test

import (
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	correctionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/correction"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

func TestBuildMaterializedProjectionsRequiresCanonicalLedgerAuthority(t *testing.T) {
	t.Parallel()

	_, err := correctionusecase.BuildMaterializedProjections(correctionusecase.MaterializedProjectionState{
		TournamentID: uuid.New(),
	})
	require.ErrorIs(t, err, correctionusecase.ErrInvalidMaterializedProjection)
}

func TestMaterializedProjectionStateDoesNotRetainLegacySeriesInputs(t *testing.T) {
	t.Parallel()

	state := reflect.TypeOf(correctionusecase.MaterializedProjectionState{})
	_, hasParticipants := state.FieldByName("Participants")
	_, hasSeries := state.FieldByName("Series")

	require.False(t, hasParticipants)
	require.False(t, hasSeries)
}

func TestBuildMaterializedProjectionsDelegatesCanonicalSwissMaterializer(t *testing.T) {
	t.Parallel()

	participants := []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	roundID := uuid.New()
	projection, err := correctionusecase.BuildMaterializedProjections(correctionusecase.MaterializedProjectionState{
		TournamentID: uuid.New(),
		CanonicalParticipants: []resultprojection.CanonicalSwissParticipant{
			{ID: participants[0], StableSeed: 1}, {ID: participants[1], StableSeed: 2},
			{ID: participants[2], StableSeed: 3}, {ID: participants[3], StableSeed: 4},
		},
		SwissLedger:   correctionSwissLedger(roundID, uuid.New(), participants),
		ArtifactKinds: []domain.ArtifactKind{domain.ArtifactKindStandings},
	})
	require.NoError(t, err)
	require.Len(t, projection.Artifacts, 1)
	require.Equal(t, domain.ArtifactKindStandings, projection.Artifacts[0].Kind)
	require.NotZero(t, projection.Artifacts[0].PayloadDigest)
}

func correctionSwissLedger(
	roundID, roundRevisionID uuid.UUID,
	participants []uuid.UUID,
) []resultprojection.CanonicalSwissPointLedgerEntry {
	series := func(first, second uuid.UUID, firstSeed, secondSeed int) []resultprojection.CanonicalSwissPointLedgerEntry {
		seriesID, resultRevisionID := uuid.New(), uuid.New()
		return []resultprojection.CanonicalSwissPointLedgerEntry{
			{
				RoundID: roundID, RoundRevisionID: roundRevisionID, RoundNumber: 1,
				SourceKind: swissusecase.PointSourceSeries, SourceSeriesID: seriesID,
				SeriesResultRevisionID: resultRevisionID, ResultLabel: swissusecase.SeriesResultPlayed,
				ParticipantID: first, OpponentID: &second, Points: swissusecase.SeriesWinPoints, StableSeed: firstSeed,
			},
			{
				RoundID: roundID, RoundRevisionID: roundRevisionID, RoundNumber: 1,
				SourceKind: swissusecase.PointSourceSeries, SourceSeriesID: seriesID,
				SeriesResultRevisionID: resultRevisionID, ResultLabel: swissusecase.SeriesResultPlayed,
				ParticipantID: second, OpponentID: &first, Points: 0, StableSeed: secondSeed,
			},
		}
	}
	rows := series(participants[0], participants[1], 1, 2)
	return append(rows, series(participants[2], participants[3], 3, 4)...)
}
