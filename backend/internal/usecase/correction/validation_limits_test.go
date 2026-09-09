package correction_test

import (
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	correctionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/correction"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/result"
)

func TestCorrectionValidationBounds(t *testing.T) {
	t.Run("rejects oversized nested authority before clone allocation", func(t *testing.T) {
		command, authority := task056CorrectionFixture(t)
		oversized := make([]domain.Game, 1<<16)
		authority.Series.Slots[0].Attempts = oversized
		authority.Score.Attempts = make([]resultusecase.SeriesScoreAttemptReference, 1<<16)
		authority.Readiness.ParticipantIDs = make([]uuid.UUID, 1<<16)

		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)
		for range 4 {
			validation, err := correctionusecase.Validate(command, authority)
			require.ErrorIs(t, err, correctionusecase.ErrInvalid)
			require.Equal(t, correctionusecase.RejectionMalformed, correctionusecase.Code(err))
			require.Equal(t, correctionusecase.Validation{}, validation)
		}
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(8<<20))
	})

	t.Run("rejects max plus one correction intents before clone allocation", func(t *testing.T) {
		command, authority := task056CorrectionFixture(t)
		seed := command.ProjectionIntents[0].ExpectedRevision
		command.ProjectionIntents = make([]correctionusecase.ProjectionIntent, 513)
		for index := range command.ProjectionIntents {
			expected, err := domain.NewProjectionRevision(
				domain.DerivedRevisionID(correctionDAGTestID(70_000+index)),
				seed.TournamentID(), seed.Artifact(), seed.RevisionNo(), seed.PreviousRevisionID(),
				seed.CreatedAt(), []byte{byte(index)},
			)
			require.NoError(t, err)
			command.ProjectionIntents[index] = correctionusecase.NewProjectionIntent(
				expected.Revision(), domain.DerivedRevisionID(correctionDAGTestID(71_000+index)),
				correctionDAGTestID(72_000+index), []byte{byte(index)},
			)
		}
		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)
		validation, err := correctionusecase.Validate(command, authority)
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		require.ErrorIs(t, err, correctionusecase.ErrInvalid)
		require.Equal(t, correctionusecase.RejectionMalformed, correctionusecase.Code(err))
		require.Equal(t, correctionusecase.Validation{}, validation)
		require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(1<<20))

		command, authority = task056CorrectionFixture(t)
		seed = command.ProjectionIntents[0].ExpectedRevision
		command.ProjectionIntents = make([]correctionusecase.ProjectionIntent, 9)
		for index := range command.ProjectionIntents {
			expected, constructErr := domain.NewProjectionRevision(
				domain.DerivedRevisionID(correctionDAGTestID(73_000+index)),
				seed.TournamentID(), seed.Artifact(), seed.RevisionNo(), seed.PreviousRevisionID(),
				seed.CreatedAt(), []byte{byte(index)},
			)
			require.NoError(t, constructErr)
			command.ProjectionIntents[index] = correctionusecase.NewProjectionIntent(
				expected.Revision(), domain.DerivedRevisionID(correctionDAGTestID(74_000+index)),
				correctionDAGTestID(75_000+index), make([]byte, 60<<10),
			)
		}
		runtime.GC()
		runtime.ReadMemStats(&before)
		validation, err = correctionusecase.Validate(command, authority)
		runtime.ReadMemStats(&after)
		require.ErrorIs(t, err, correctionusecase.ErrInvalid)
		require.Equal(t, correctionusecase.RejectionMalformed, correctionusecase.Code(err))
		require.Equal(t, correctionusecase.Validation{}, validation)
		require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(1<<20))
	})

	t.Run("bounds correction explanation bytes before content validation", func(t *testing.T) {
		command, authority := task056CorrectionFixture(t)
		command.Explanation = strings.Repeat("x", 512)
		validation, err := correctionusecase.Validate(command, authority)
		require.NoError(t, err)
		require.NoError(t, validation.Validate())

		command.Explanation += "x"
		validation, err = correctionusecase.Validate(command, authority)
		require.ErrorIs(t, err, correctionusecase.ErrInvalid)
		require.Equal(t, correctionusecase.RejectionMalformed, correctionusecase.Code(err))
		require.Equal(t, correctionusecase.Validation{}, validation)
	})
}
