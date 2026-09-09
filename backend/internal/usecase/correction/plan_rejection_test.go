package correction_test

import (
	"crypto/sha256"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	correctionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/correction"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
)

func TestCorrectionPlanRejections(t *testing.T) {
	t.Run("rejects an affected decision set that is not a causal suffix", func(t *testing.T) {
		t.Parallel()

		command, authority := task056CorrectionFixture(t)
		var sibling domain.DerivedRevision
		for _, projection := range authority.DAG.Snapshot().Projections {
			artifact := projection.Revision().Artifact()
			if artifact.Kind == domain.ArtifactKindGameResult && artifact.EntityID != command.GameID {
				sibling = projection.Revision()
				break
			}
		}
		require.False(t, sibling.ID().IsZero())
		for index := range authority.Decisions {
			authority.Decisions[index].Sequence += 2
		}
		targetPayload := []byte(`{"target":true}`)
		siblingPayload := []byte(`{"sibling":true}`)
		authority.Decisions = append([]resultprojection.RecordedProjectionDecision{
			{
				ID: correctionDAGTestID(76_000), Sequence: 1,
				ProjectionRevisionID: command.Expected.TargetProjection.ID(),
				RecordedAt:           command.Expected.TargetProjection.CreatedAt(),
				Payload:              targetPayload, PayloadDigest: sha256.Sum256(targetPayload),
			},
			{
				ID: correctionDAGTestID(76_001), Sequence: 2, ProjectionRevisionID: sibling.ID(),
				RecordedAt: sibling.CreatedAt(), Payload: siblingPayload,
				PayloadDigest: sha256.Sum256(siblingPayload),
			},
		}, authority.Decisions...)
		var err error
		_, err = resultprojection.RebuildOfficialProjections(resultprojection.ProjectionRebuildInput{
			DAG: authority.DAG, Decisions: authority.Decisions,
		})
		require.NoError(t, err)
		command.Expected, err = correctionusecase.NewExpectation(authority, command.Expected.TargetProjection.ID())
		require.NoError(t, err)
		plan, err := correctionusecase.BuildPlan(command, authority)
		require.ErrorIs(t, err, correctionusecase.ErrInvalid)
		require.Equal(t, correctionusecase.RejectionIncomplete, correctionusecase.Code(err))
		require.Equal(t, correctionusecase.Plan{}, plan)
	})

	t.Run("rejects reapplication after authority advanced", func(t *testing.T) {
		t.Parallel()

		command, authority := task056CorrectionFixture(t)
		first, err := correctionusecase.BuildPlan(command, authority)
		require.NoError(t, err)
		advanced := authority
		advanced.DAG = task056DAGFromCorrectionPlan(t, command, first)
		second, err := correctionusecase.BuildPlan(command, advanced)
		require.ErrorIs(t, err, correctionusecase.ErrInvalid)
		require.Equal(t, correctionusecase.RejectionStale, correctionusecase.Code(err))
		require.Equal(t, correctionusecase.Plan{}, second)
	})

	t.Run("returns zero plan on rejection without partial mutation", func(t *testing.T) {
		t.Parallel()

		command, authority := task056CorrectionFixture(t)
		authority.CutoffEvents = []correctionusecase.CutoffEvent{{
			ID: correctionDAGTestID(43_000), Kind: correctionusecase.CutoffWaveStarted,
			TournamentID:     command.TournamentID,
			SourceRevisionID: command.Expected.SeriesProjection.ID(),
			OccurredAt:       command.RequestedAt,
		}}
		beforeCommand := task056CloneCommand(command)
		beforeAuthority := task056CloneAuthority(authority)

		plan, err := correctionusecase.BuildPlan(command, authority)
		require.ErrorIs(t, err, correctionusecase.ErrCutoff)
		require.Equal(t, correctionusecase.RejectionCutoff, correctionusecase.Code(err))
		require.Equal(t, correctionusecase.Plan{}, plan)
		require.Equal(t, beforeCommand, command)
		require.Equal(t, beforeAuthority.DAG.Snapshot(), authority.DAG.Snapshot())
		require.Equal(t, beforeAuthority.Reservations, authority.Reservations)
	})
}
