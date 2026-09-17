package correction_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	correctionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/correction"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection/revision"
)

func TestCorrectionPlanLineage(t *testing.T) {
	t.Run("rebuilds only the current head of an affected artifact lineage", func(t *testing.T) {
		t.Parallel()

		fixture := correctionDAGTestRevisionDAGFixture(t)
		var standings domain.ProjectionRevision
		var goldenID domain.DerivedRevisionID
		for _, projection := range fixture.Graph.Projections() {
			//nolint:exhaustive // The lineage fixture lookup needs only these two artifacts.
			switch projection.Revision().Artifact().Kind {
			case domain.ArtifactKindStandings:
				standings = projection
			case domain.ArtifactKindGoldenGroup:
				goldenID = projection.Revision().ID()
			default:
			}
		}
		standingsHead := correctionDAGTestSuccessorProjection(
			t, standings, 43_010, standings.Revision().CreatedAt().Add(500*time.Millisecond),
			"standings-current",
		)
		projections := append(fixture.Graph.Projections(), standingsHead)
		dependencies := make([]domain.RevisionDependency, 0, len(fixture.Graph.Dependencies())+1)
		for _, dependency := range fixture.Graph.Dependencies() {
			if dependency.DerivedRevisionID == standings.Revision().ID() {
				dependencies = append(dependencies, dependency, domain.RevisionDependency{
					SourceRevisionID:  dependency.SourceRevisionID,
					DerivedRevisionID: standingsHead.Revision().ID(),
				})
				continue
			}
			if dependency.SourceRevisionID == standings.Revision().ID() &&
				dependency.DerivedRevisionID == goldenID {
				dependencies = append(dependencies, domain.RevisionDependency{
					SourceRevisionID: standingsHead.Revision().ID(), DerivedRevisionID: goldenID,
				})
				continue
			}
			dependencies = append(dependencies, dependency)
		}
		dependencies = append(dependencies, domain.RevisionDependency{
			SourceRevisionID:  standings.Revision().ID(),
			DerivedRevisionID: standingsHead.Revision().ID(),
		})
		graph, err := domain.NewRevisionGraph(projections, dependencies)
		require.NoError(t, err)
		dag, err := resultprojection.BuildRevisionDAG(resultprojection.RevisionDAGInput{Graph: graph, Results: fixture.Results})
		require.NoError(t, err)

		command, authority := task056CorrectionFixture(t)
		authority.DAG = dag
		command.Expected, err = correctionusecase.NewExpectation(authority, command.Expected.TargetProjection.ID())
		require.NoError(t, err)
		cutoff, err := correctionusecase.EvaluateCutoff(correctionusecase.CutoffInput{
			DAG: dag, TournamentID: command.TournamentID,
			TargetRevisionID: command.Expected.TargetProjection.ID(),
			TournamentState:  authority.TournamentState,
		})
		require.NoError(t, err)
		require.Len(t, cutoff.Descendants(), 8)
		current := make(map[domain.ArtifactRef]domain.DerivedRevision)
		for _, projection := range dag.Snapshot().Projections {
			revision := projection.Revision()
			if prior, exists := current[revision.Artifact()]; !exists || prior.RevisionNo() < revision.RevisionNo() {
				current[revision.Artifact()] = revision
			}
		}
		rebuild := []domain.DerivedRevision{command.Expected.TargetProjection}
		for _, descendant := range cutoff.Descendants() {
			if head := current[descendant.Artifact()]; head.ID() == descendant.ID() {
				rebuild = append(rebuild, descendant)
			}
		}
		command.ProjectionIntents = make([]correctionusecase.ProjectionIntent, len(rebuild))
		for index, revision := range rebuild {
			command.ProjectionIntents[index] = correctionusecase.NewProjectionIntent(
				revision, domain.DerivedRevisionID(correctionDAGTestID(43_100+index)),
				correctionDAGTestID(43_200+index), fmt.Appendf(nil, "lineage-%02d", index),
			)
		}

		plan, err := correctionusecase.BuildPlan(command, authority)
		require.NoError(t, err)
		require.Len(t, plan.ProjectionRevisions(), 8)
		require.Len(t, plan.Supersessions(), 7)
		for _, supersession := range plan.Supersessions() {
			require.False(t, supersession.PreviousRevisionID.IsZero())
			require.False(t, supersession.SuccessorRevisionID.IsZero())
			require.NotEqual(t, uuid.Nil, supersession.ReplacementDecisionID)
			if supersession.Artifact.Kind == domain.ArtifactKindStandings {
				require.Equal(t, standingsHead.Revision().ID(), supersession.PreviousRevisionID)
				require.NotEqual(t, standings.Revision().ID(), supersession.PreviousRevisionID)
			}
		}
		standingsSuccessors := 0
		var standingsSuccessorID domain.DerivedRevisionID
		var seriesResultSuccessorID domain.DerivedRevisionID
		for _, projection := range plan.ProjectionRevisions() {
			revision := projection.Revision()
			if revision.Artifact().Kind == domain.ArtifactKindSeriesResult {
				seriesResultSuccessorID = revision.ID()
			}
			if revision.Artifact().Kind != domain.ArtifactKindStandings {
				continue
			}
			standingsSuccessors++
			standingsSuccessorID = revision.ID()
			require.Equal(t, standingsHead.Revision().ID(), *revision.PreviousRevisionID())
			require.NotEqual(t, standings.Revision().ID(), *revision.PreviousRevisionID())
		}
		require.Equal(t, 1, standingsSuccessors)
		require.False(t, standingsSuccessorID.IsZero())
		require.False(t, seriesResultSuccessorID.IsZero())
		standingsSources := make([]domain.DerivedRevisionID, 0, 2)
		for _, dependency := range plan.DAGSnapshot().Dependencies {
			if dependency.DerivedRevisionID == standingsSuccessorID {
				standingsSources = append(standingsSources, dependency.SourceRevisionID)
			}
		}
		require.ElementsMatch(t, []domain.DerivedRevisionID{
			standingsHead.Revision().ID(), seriesResultSuccessorID,
		}, standingsSources)
		require.NotContains(t, standingsSources, standings.Revision().ID())
	})
}
