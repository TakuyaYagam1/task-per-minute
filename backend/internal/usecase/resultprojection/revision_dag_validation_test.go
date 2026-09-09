package resultprojection_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	projection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
)

func TestRevisionDAGValidation(t *testing.T) {
	t.Run("rejects a result edge that skips the current score", func(t *testing.T) {
		t.Parallel()

		input := task055RevisionDAGFixture(t)
		projections := input.Graph.Projections()
		dependencies := input.Graph.Dependencies()
		gameID := input.Results[0].Result.SourceProjection.ID()
		seriesID := input.Results[2].Result.SourceProjection.ID()
		for index, dependency := range dependencies {
			if dependency.DerivedRevisionID == seriesID {
				dependencies[index].SourceRevisionID = gameID
				break
			}
		}
		graph, err := domain.NewRevisionGraph(projections, dependencies)
		require.NoError(t, err)
		input.Graph = graph

		_, err = projection.BuildRevisionDAG(input)
		require.ErrorIs(t, err, projection.ErrInvalidRevisionDAG)
	})

	t.Run("rejects stale current heads duplicate scopes and disconnected descendants", func(t *testing.T) {
		t.Parallel()

		input := task055RevisionDAGFixture(t)
		duplicate := input
		duplicate.Results = append(duplicate.Results, duplicate.Results[0])
		_, err := projection.BuildRevisionDAG(duplicate)
		require.ErrorIs(t, err, projection.ErrInvalidRevisionDAG)

		stale := input
		old := stale.Results[2].ResultProjection
		oldRevision := old.Revision()
		current := task055SuccessorProjection(
			t, old, 9090, oldRevision.CreatedAt().Add(time.Second), "current-series-result",
		)
		projections := append(stale.Graph.Projections(), current)
		dependencies := append(stale.Graph.Dependencies(), domain.RevisionDependency{
			SourceRevisionID: oldRevision.ID(), DerivedRevisionID: current.Revision().ID(),
		})
		graph, err := domain.NewRevisionGraph(projections, dependencies)
		require.NoError(t, err)
		stale.Graph = graph
		_, err = projection.BuildRevisionDAG(stale)
		require.ErrorIs(t, err, projection.ErrInvalidRevisionDAG)

		disconnected := input
		dependencies = disconnected.Graph.Dependencies()
		standingsID := task055CurrentProjectionID(
			t, disconnected.Graph, domain.ArtifactKindStandings,
		)
		filtered := dependencies[:0]
		for _, dependency := range dependencies {
			if dependency.DerivedRevisionID != standingsID {
				filtered = append(filtered, dependency)
			}
		}
		graph, err = domain.NewRevisionGraph(disconnected.Graph.Projections(), filtered)
		require.NoError(t, err)
		disconnected.Graph = graph
		_, err = projection.BuildRevisionDAG(disconnected)
		require.ErrorIs(t, err, projection.ErrInvalidRevisionDAG)
	})

	t.Run("rejects a score command detached from its exact Game result", func(t *testing.T) {
		t.Parallel()

		input := task055RevisionDAGFixture(t)
		score := input.Results[2].Score.Clone()
		score.CommandID = task055ID(9997)
		input.Results[2].Score = task055ScoreHeadPointer(score)

		_, err := projection.BuildRevisionDAG(input)
		require.ErrorIs(t, err, projection.ErrInvalidRevisionDAG)
	})

	t.Run("rejects a coherent no-show projection and graph payload splice", func(t *testing.T) {
		t.Parallel()

		input := task055NoGameRevisionDAGFixture(t)
		_, err := projection.BuildRevisionDAG(input)
		require.NoError(t, err)
		recorded := input.Results[0].NoGame
		forged := task055ProjectionWithPayload(
			t, recorded.ScoreSourceRevision, "coherently-forged-no-show-score",
		)
		recorded.ScoreProjection = forged
		projections := input.Graph.Projections()
		for index := range projections {
			if projections[index].Revision().ID() == forged.Revision().ID() {
				projections[index] = forged
			}
		}
		graph, err := domain.NewRevisionGraph(projections, input.Graph.Dependencies())
		require.NoError(t, err)
		input.Graph = graph

		_, err = projection.BuildRevisionDAG(input)
		require.ErrorIs(t, err, projection.ErrInvalidRevisionDAG)
	})

	t.Run("binds exact graph payloads and rejects shortcut edges", func(t *testing.T) {
		t.Parallel()

		input := task055RevisionDAGFixture(t)
		projections := input.Graph.Projections()
		for index := range projections {
			if projections[index].Revision().ID() != input.Results[0].Result.SourceProjection.ID() {
				continue
			}
			gameRevision := projections[index].Revision()
			changed, err := domain.NewProjectionRevision(
				gameRevision.ID(), gameRevision.TournamentID(), gameRevision.Artifact(),
				gameRevision.RevisionNo(), gameRevision.PreviousRevisionID(), gameRevision.CreatedAt(),
				[]byte("tampered-current-game"),
			)
			require.NoError(t, err)
			projections[index] = changed
		}
		graph, err := domain.NewRevisionGraph(projections, input.Graph.Dependencies())
		require.NoError(t, err)
		tampered := input
		tampered.Graph = graph
		_, err = projection.BuildRevisionDAG(tampered)
		require.ErrorIs(t, err, projection.ErrInvalidRevisionDAG)

		shortcut := input
		dependencies := append([]domain.RevisionDependency(nil), input.Graph.Dependencies()...)
		dependencies = append(dependencies, domain.RevisionDependency{
			SourceRevisionID:  input.Results[0].Result.SourceProjection.ID(),
			DerivedRevisionID: input.Results[2].Result.SourceProjection.ID(),
		})
		graph, err = domain.NewRevisionGraph(input.Graph.Projections(), dependencies)
		require.NoError(t, err)
		shortcut.Graph = graph
		_, err = projection.BuildRevisionDAG(shortcut)
		require.ErrorIs(t, err, projection.ErrInvalidRevisionDAG)
	})

	t.Run("supports the exact no Golden path and canonical input order", func(t *testing.T) {
		t.Parallel()

		input := task055RevisionDAGFixture(t)
		goldenID := task055CurrentProjectionID(t, input.Graph, domain.ArtifactKindGoldenGroup)
		standingsID := task055CurrentProjectionID(t, input.Graph, domain.ArtifactKindStandings)
		topFourID := task055CurrentProjectionID(t, input.Graph, domain.ArtifactKindTopFour)
		projections := make([]domain.ProjectionRevision, 0, len(input.Graph.Projections())-1)
		for _, projection := range input.Graph.Projections() {
			if projection.Revision().ID() != goldenID {
				projections = append(projections, projection)
			}
		}
		dependencies := make([]domain.RevisionDependency, 0, len(input.Graph.Dependencies())-1)
		for _, dependency := range input.Graph.Dependencies() {
			if dependency.SourceRevisionID != goldenID && dependency.DerivedRevisionID != goldenID {
				dependencies = append(dependencies, dependency)
			}
		}
		dependencies = append(dependencies, domain.RevisionDependency{
			SourceRevisionID: standingsID, DerivedRevisionID: topFourID,
		})
		graph, err := domain.NewRevisionGraph(projections, dependencies)
		require.NoError(t, err)
		input.Graph = graph
		first, err := projection.BuildRevisionDAG(input)
		require.NoError(t, err)

		for left, right := 0, len(input.Results)-1; left < right; left, right = left+1, right-1 {
			input.Results[left], input.Results[right] = input.Results[right], input.Results[left]
		}
		second, err := projection.BuildRevisionDAG(input)
		require.NoError(t, err)
		require.Equal(t, first.PublicResults(), second.PublicResults())
		require.Equal(t, first.OperatorResults(), second.OperatorResults())
		require.Equal(t, first.Snapshot(), second.Snapshot())
	})

	t.Run("rejects cross-role revision aliases", func(t *testing.T) {
		t.Parallel()

		input := task055RevisionDAGFixture(t)
		score := input.Results[2].Score.Clone()
		aliasedID := domain.OfficialResultRevisionID(score.ID.UUID())
		input.Results[0].Result.ID = aliasedID
		score.Attempts[0].CurrentGameResultRevisionID = aliasedID
		input.Results[2].Score = task055ScoreHeadPointer(score)

		_, err := projection.BuildRevisionDAG(input)
		require.ErrorIs(t, err, projection.ErrInvalidRevisionDAG)
	})

	t.Run("allows an unpublished champion but requires the base downstream path", func(t *testing.T) {
		t.Parallel()

		input := task055RevisionDAGFixture(t)
		championID := task055CurrentProjectionID(t, input.Graph, domain.ArtifactKindChampion)
		projections := make([]domain.ProjectionRevision, 0, len(input.Graph.Projections())-1)
		for _, projection := range input.Graph.Projections() {
			if projection.Revision().ID() != championID {
				projections = append(projections, projection)
			}
		}
		dependencies := make([]domain.RevisionDependency, 0, len(input.Graph.Dependencies())-1)
		for _, dependency := range input.Graph.Dependencies() {
			if dependency.SourceRevisionID != championID && dependency.DerivedRevisionID != championID {
				dependencies = append(dependencies, dependency)
			}
		}
		graph, err := domain.NewRevisionGraph(projections, dependencies)
		require.NoError(t, err)
		missing := input
		missing.Graph = graph
		_, err = projection.BuildRevisionDAG(missing)
		require.NoError(t, err)

		topFourID := task055CurrentProjectionID(t, graph, domain.ArtifactKindTopFour)
		withoutTopFour := make([]domain.ProjectionRevision, 0, len(projections)-1)
		for _, projection := range projections {
			if projection.Revision().ID() != topFourID {
				withoutTopFour = append(withoutTopFour, projection)
			}
		}
		withoutTopFourDependencies := make([]domain.RevisionDependency, 0, len(dependencies)-2)
		for _, dependency := range dependencies {
			if dependency.SourceRevisionID != topFourID && dependency.DerivedRevisionID != topFourID {
				withoutTopFourDependencies = append(withoutTopFourDependencies, dependency)
			}
		}
		graph, err = domain.NewRevisionGraph(withoutTopFour, withoutTopFourDependencies)
		require.NoError(t, err)
		missing.Graph = graph
		_, err = projection.BuildRevisionDAG(missing)
		require.ErrorIs(t, err, projection.ErrInvalidRevisionDAG)

		standingsID := task055CurrentProjectionID(t, input.Graph, domain.ArtifactKindStandings)
		topFourID = task055CurrentProjectionID(t, input.Graph, domain.ArtifactKindTopFour)
		secondGolden := task055Projection(
			t, 9700, input.Results[0].Result.Scope.TournamentID,
			domain.ArtifactKindGoldenGroup, task055ID(9701),
			time.Date(2026, time.September, 1, 11, 0, 8, 500_000_000, time.UTC),
			"second-golden",
		)
		projections = append(input.Graph.Projections(), secondGolden)
		dependencies = append([]domain.RevisionDependency(nil), input.Graph.Dependencies()...)
		dependencies = append(dependencies,
			domain.RevisionDependency{
				SourceRevisionID: standingsID, DerivedRevisionID: secondGolden.Revision().ID(),
			},
			domain.RevisionDependency{
				SourceRevisionID: secondGolden.Revision().ID(), DerivedRevisionID: topFourID,
			},
		)
		graph, err = domain.NewRevisionGraph(projections, dependencies)
		require.NoError(t, err)
		input.Graph = graph
		_, err = projection.BuildRevisionDAG(input)
		require.NoError(t, err)
	})

	t.Run("rejects stale current Game and score projections", func(t *testing.T) {
		t.Parallel()

		for _, subject := range []string{"Game", "score"} {
			t.Run(subject, func(t *testing.T) {
				t.Parallel()
				input := task055RevisionDAGFixture(t)
				var old domain.ProjectionRevision
				if subject == "Game" {
					old = input.Results[0].ResultProjection
				} else {
					old = *input.Results[2].ScoreProjection
				}
				successor := task055SuccessorProjection(
					t, old, 9800+len(subject), old.Revision().CreatedAt().Add(time.Second),
					"new-current-"+subject,
				)
				dependencies := append([]domain.RevisionDependency(nil), input.Graph.Dependencies()...)
				dependencies = append(dependencies, domain.RevisionDependency{
					SourceRevisionID: old.Revision().ID(), DerivedRevisionID: successor.Revision().ID(),
				})
				graph, err := domain.NewRevisionGraph(
					append(input.Graph.Projections(), successor), dependencies,
				)
				require.NoError(t, err)
				input.Graph = graph
				_, err = projection.BuildRevisionDAG(input)
				require.ErrorIs(t, err, projection.ErrInvalidRevisionDAG)
			})
		}
	})
}
