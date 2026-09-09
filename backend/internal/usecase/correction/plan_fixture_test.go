package correction_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	correctionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/correction"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
)

func task056FindGame(series domain.Series, gameID [16]byte) (domain.Game, bool) {
	for _, slot := range series.Slots {
		for _, game := range slot.Attempts {
			if game.ID == gameID {
				return game, true
			}
		}
	}
	return domain.Game{}, false
}

func task056CurrentRevision(
	snapshot resultprojection.RevisionDAGSnapshot,
	artifact domain.ArtifactRef,
) domain.DerivedRevision {
	var current domain.DerivedRevision
	for _, projection := range snapshot.Projections {
		revision := projection.Revision()
		if revision.Artifact() == artifact && revision.RevisionNo() > current.RevisionNo() {
			current = revision
		}
	}
	return current
}

func task056DAGFromCorrectionPlan(
	t *testing.T,
	command correctionusecase.Command,
	plan correctionusecase.Plan,
) resultprojection.RevisionDAG {
	t.Helper()
	snapshot := plan.DAGSnapshot()
	graph, err := domain.NewRevisionGraph(snapshot.Projections, snapshot.Dependencies)
	require.NoError(t, err)
	fixture := correctionDAGTestRevisionDAGFixture(t)
	gameHead := plan.GameResultRevision().Revision().Head()
	scoreHead := plan.ScoreRevision().Revision().Head()
	seriesHead := plan.SeriesResultRevision().Revision().Head()
	var gameProjection, scoreProjection, seriesProjection domain.ProjectionRevision
	for _, projection := range snapshot.Projections {
		artifact := projection.Revision().Artifact()
		switch {
		case artifact.Kind == domain.ArtifactKindGameResult && artifact.EntityID == command.GameID:
			if gameProjection.Revision().RevisionNo() < projection.Revision().RevisionNo() {
				gameProjection = projection
			}
		case artifact.Kind == domain.ArtifactKindSeriesScore && artifact.EntityID == command.SeriesID:
			if scoreProjection.Revision().RevisionNo() < projection.Revision().RevisionNo() {
				scoreProjection = projection
			}
		case artifact.Kind == domain.ArtifactKindSeriesResult && artifact.EntityID == command.SeriesID:
			if seriesProjection.Revision().RevisionNo() < projection.Revision().RevisionNo() {
				seriesProjection = projection
			}
		}
	}
	require.False(t, gameProjection.Revision().ID().IsZero())
	require.False(t, scoreProjection.Revision().ID().IsZero())
	require.False(t, seriesProjection.Revision().ID().IsZero())
	inputs := make([]resultprojection.OfficialResultProjectionInput, len(fixture.Results))
	for index, input := range fixture.Results {
		switch input.Result.Scope {
		case gameHead.Scope:
			input = resultprojection.OfficialResultProjectionInput{
				TerminalSource: resultprojection.TerminalResultSourcePlayed,
				Result:         gameHead, ResultProjection: gameProjection,
			}
		case seriesHead.Scope:
			input = resultprojection.OfficialResultProjectionInput{
				TerminalSource: resultprojection.TerminalResultSourcePlayed,
				Result:         seriesHead, ResultProjection: seriesProjection,
				Score: &scoreHead, ScoreProjection: &scoreProjection,
			}
		}
		inputs[index] = input
	}
	dag, err := resultprojection.BuildRevisionDAG(resultprojection.RevisionDAGInput{Graph: graph, Results: inputs})
	require.NoError(t, err)
	return dag
}
