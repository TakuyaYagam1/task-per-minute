package correction_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	correctionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/correction"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection/revision"
)

func TestCorrectionPlanPayloadBounds(t *testing.T) {
	t.Run("enforces the combined old and successor payload cap", func(t *testing.T) {
		command, authority := task056CorrectionFixture(t)
		oldPayloadBytes := 0
		for _, projection := range authority.DAG.Snapshot().Projections {
			oldPayloadBytes += len(projection.Payload())
		}
		remaining := (512 << 10) - oldPayloadBytes
		require.Positive(t, remaining)
		for index, intent := range command.ProjectionIntents {
			size := remaining / len(command.ProjectionIntents)
			if index < remaining%len(command.ProjectionIntents) {
				size++
			}
			require.LessOrEqual(t, size, resultprojection.MaxRecordedProjectionDecisionBytes)
			command.ProjectionIntents[index] = correctionusecase.NewProjectionIntent(
				intent.ExpectedRevision, intent.NextRevisionID, intent.DecisionID, make([]byte, size),
			)
		}
		exact, err := correctionusecase.BuildPlan(command, authority)
		require.NoError(t, err)
		require.NoError(t, exact.Validate())

		over := task056CloneCommand(command)
		last := len(over.ProjectionIntents) - 1
		intent := over.ProjectionIntents[last]
		payload := append([]byte(nil), intent.Payload...)
		payload = append(payload, 0x01)
		over.ProjectionIntents[last] = correctionusecase.NewProjectionIntent(
			intent.ExpectedRevision, intent.NextRevisionID, intent.DecisionID, payload,
		)
		rejected, err := correctionusecase.BuildPlan(over, authority)
		require.ErrorIs(t, err, correctionusecase.ErrInvalid)
		require.Equal(t, correctionusecase.RejectionMalformed, correctionusecase.Code(err))
		require.Equal(t, correctionusecase.Plan{}, rejected)
	})
}
