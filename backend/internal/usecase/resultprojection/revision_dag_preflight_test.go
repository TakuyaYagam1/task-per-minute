package resultprojection_test

import (
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/result"
	projection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
)

func TestRevisionDAGPreflight(t *testing.T) {
	t.Run("admits a private correction predecessor node identity", func(t *testing.T) {
		input := task055RevisionDAGFixture(t)
		ordinary := input.Results[0].Result
		artifact := ordinary.SourceProjection.Artifact()
		createdAt := ordinary.SourceProjection.CreatedAt()
		previousSourceID := ordinary.ID
		previousNode, err := domain.NewProjectionRevision(
			domain.DerivedRevisionID(previousSourceID.UUID()), ordinary.Scope.TournamentID,
			artifact, 1, nil, createdAt, []byte("ordinary predecessor"),
		)
		require.NoError(t, err)
		previousNodeID := previousNode.Revision().ID()
		currentNodeID := input.Results[0].ResultProjection.Revision().ID()
		currentNode, err := domain.NewProjectionRevision(
			currentNodeID, ordinary.Scope.TournamentID,
			artifact, 2, &previousNodeID, createdAt.Add(time.Second), []byte("correction node"),
		)
		require.NoError(t, err)

		head := ordinary.Clone()
		head.ID = domain.OfficialResultRevisionID(task055ID(25_001))
		head.PreviousRevisionID = &previousSourceID
		head.Ordinal = 2
		head.CommandID = task055ID(25_002)
		head.SourceProjection = currentNode.Revision()
		head.RecordedAt = createdAt.Add(2 * time.Second)
		binding := resultusecase.PersistedCorrectionSourceBinding{
			TournamentID: head.Scope.TournamentID, RosterID: task055ID(25_003),
			SeriesID: head.Scope.SeriesID, EntityID: head.Scope.GameID,
			ArtifactKind:        domain.ArtifactKindGameResult,
			CorrectionCommandID: task055ID(25_004), HeadCommandID: head.CommandID,
			SourceID: head.ID.UUID(), NodeID: currentNode.Revision().ID().UUID(),
			PreviousSourceID: previousSourceID.UUID(), PreviousNodeID: previousNodeID.UUID(), NodeRevision: 2,
		}
		restored, err := resultusecase.RestoreCorrectionOfficialResultHead(head, binding)
		require.NoError(t, err)
		projections := input.Graph.Projections()
		for index := range projections {
			if projections[index].Revision().ID() == currentNodeID {
				projections[index] = currentNode
				break
			}
		}
		projections = append(projections, previousNode)
		dependencies := append(input.Graph.Dependencies(), domain.RevisionDependency{
			SourceRevisionID: previousNodeID, DerivedRevisionID: currentNodeID,
		})
		graph, err := domain.NewRevisionGraph(
			projections, dependencies,
		)
		require.NoError(t, err)
		input.Graph = graph
		input.Results[0].Result = restored
		input.Results[0].ResultProjection = currentNode
		score := input.Results[2].Score.Clone()
		for index := range score.Attempts {
			if score.Attempts[index].GameID == head.Scope.GameID {
				score.Attempts[index].CurrentGameResultRevisionID = head.ID
			}
		}
		input.Results[2].Score = &score
		_, err = projection.BuildRevisionDAG(input)
		require.NoError(t, err)

		input.Results[0].Result = head
		_, err = projection.BuildRevisionDAG(input)
		require.ErrorIs(t, err, projection.ErrInvalidRevisionDAG)
		require.ErrorContains(t, err, "identity aliases")
	})

	t.Run("returns detached projection inputs", func(t *testing.T) {
		dag, err := projection.BuildRevisionDAG(task055RevisionDAGFixture(t))
		require.NoError(t, err)
		expected := dag.Inputs()
		require.NotEmpty(t, expected)

		returned := dag.Inputs()
		returned[0].Result.CommandID = idForTask055(99_001)
		returned[0].Result.Outcome.WinnerID = nil
		if returned[0].Score != nil && len(returned[0].Score.Attempts) > 0 {
			returned[0].Score.Attempts[0].WinnerID = nil
		}

		require.Equal(t, expected, dag.Inputs())
	})

	t.Run("indexes a long revision chain in linear graph bytes", func(t *testing.T) {
		input := task055LongCurrentPayloadFixture(t)
		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)
		dag, err := projection.BuildRevisionDAG(input)
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		runtime.KeepAlive(dag)
		require.NoError(t, err)

		const maxLinearAllocation = 6 << 20
		allocated := after.TotalAlloc - before.TotalAlloc
		t.Logf("revision index allocated %d bytes", allocated)
		require.Less(t, allocated, uint64(maxLinearAllocation), "revision index cloned the current payload per historical revision")
	})

	t.Run("rejects duplicate result ownership before cloning projection payloads", func(t *testing.T) {
		input := task055RevisionDAGFixture(t)
		game := input.Results[0].Result.Clone()
		largePayload := make([]byte, 128<<10)
		revision, err := domain.NewProjectionRevision(
			game.SourceProjection.ID(), game.SourceProjection.TournamentID(),
			game.SourceProjection.Artifact(), game.SourceProjection.RevisionNo(),
			game.SourceProjection.PreviousRevisionID(), game.SourceProjection.CreatedAt(), largePayload,
		)
		require.NoError(t, err)
		game.SourceProjection = revision.Revision()
		duplicate := projection.OfficialResultProjectionInput{
			Result: game, ResultProjection: revision,
		}
		input.Results = make([]projection.OfficialResultProjectionInput, 64)
		for index := range input.Results {
			input.Results[index] = duplicate
		}

		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)
		_, err = projection.BuildRevisionDAG(input)
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		require.ErrorIs(t, err, projection.ErrInvalidRevisionDAG)

		const maxPreflightAllocation = 2 << 20
		allocated := after.TotalAlloc - before.TotalAlloc
		t.Logf("duplicate result preflight allocated %d bytes", allocated)
		require.Less(t, allocated, uint64(maxPreflightAllocation), "duplicate results cloned projection payloads before rejection")
	})

	t.Run("rejects oversized nested evidence before cloning", func(t *testing.T) {
		cases := []struct {
			name  string
			input func() projection.RevisionDAGInput
			want  string
		}{
			{
				name: "score attempts",
				want: "score preflight has too many attempts",
				input: func() projection.RevisionDAGInput {
					input := task055RevisionDAGFixture(t)
					score := input.Results[2].Score.Clone()
					score.Attempts = make([]resultusecase.SeriesScoreAttemptReference, 1<<16)
					input.Results[2].Score = &score
					return input
				},
			},
			{
				name: "no-game topology",
				want: "invalid no-game preflight shape",
				input: func() projection.RevisionDAGInput {
					input := task055NoGameRevisionDAGFixture(t)
					recorded := *input.Results[0].NoGame
					recorded.Topology = make([]projection.RecordedNoGameAttempt, 1<<16)
					input.Results[0].NoGame = &recorded
					return input
				},
			},
		}
		for _, test := range cases {
			t.Run(test.name, func(t *testing.T) {
				input := test.input()
				runtime.GC()
				var before runtime.MemStats
				runtime.ReadMemStats(&before)
				_, err := projection.BuildRevisionDAG(input)
				var after runtime.MemStats
				runtime.ReadMemStats(&after)
				require.ErrorIs(t, err, projection.ErrInvalidRevisionDAG)
				require.ErrorContains(t, err, test.want)
				allocated := after.TotalAlloc - before.TotalAlloc
				t.Logf("nested evidence preflight allocated %d bytes", allocated)
				require.Less(t, allocated, uint64(2<<20))
			})
		}
	})

	t.Run("rejects repeated no-game projection owners before cloning payloads", func(t *testing.T) {
		input := task055RepeatedNoGameProjectionFixture(t)
		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)
		_, err := projection.BuildRevisionDAG(input)
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		require.ErrorIs(t, err, projection.ErrInvalidRevisionDAG)

		const maxOwnerPreflightAllocation = 1 << 20
		allocated := after.TotalAlloc - before.TotalAlloc
		t.Logf("repeated no-game owner preflight allocated %d bytes", allocated)
		require.Less(t, allocated, uint64(maxOwnerPreflightAllocation), "no-game projection payload was cloned per repeated owner")
	})

	t.Run("rejects an orphan score projection before cloning its payload", func(t *testing.T) {
		input := task055RevisionDAGFixture(t)
		game := input.Results[0]
		orphan := task055Projection(
			t, 19_500, game.Result.Scope.TournamentID,
			domain.ArtifactKindSeriesScore, game.Result.Scope.SeriesID,
			game.Result.RecordedAt, string(make([]byte, 128<<10)),
		)
		game.ScoreProjection = &orphan
		input.Results = []projection.OfficialResultProjectionInput{game}

		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)
		_, err := projection.BuildRevisionDAG(input)
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		require.ErrorIs(t, err, projection.ErrInvalidRevisionDAG)
		require.ErrorContains(t, err, "Game result preflight has Series score evidence")

		const maxShapePreflightAllocation = 64 << 10
		allocated := after.TotalAlloc - before.TotalAlloc
		t.Logf("orphan score projection preflight allocated %d bytes", allocated)
		require.Less(t, allocated, uint64(maxShapePreflightAllocation), "orphan score projection payload was cloned")
	})

	t.Run("preflights result subject score evidence shape", func(t *testing.T) {
		fixture := task055RevisionDAGFixture(t)
		cases := []struct {
			name  string
			input projection.OfficialResultProjectionInput
			want  string
		}{
			{
				name: "Series score without projection",
				input: func() projection.OfficialResultProjectionInput {
					series := fixture.Results[2]
					series.ScoreProjection = nil
					return series
				}(),
				want: "Series result preflight is missing score evidence",
			},
			{
				name: "unknown subject",
				input: func() projection.OfficialResultProjectionInput {
					game := fixture.Results[0]
					game.Result.Scope.Kind = resultusecase.OfficialResultSubjectKind("unknown")
					return game
				}(),
				want: "unknown result subject in preflight",
			},
		}
		for _, test := range cases {
			t.Run(test.name, func(t *testing.T) {
				input := fixture
				input.Graph = domain.RevisionGraph{}
				input.Results = []projection.OfficialResultProjectionInput{test.input}

				_, err := projection.BuildRevisionDAG(input)
				require.ErrorIs(t, err, projection.ErrInvalidRevisionDAG)
				require.ErrorContains(t, err, test.want)
			})
		}
	})

	t.Run("retains the explicit current result chain through champion", func(t *testing.T) {
		t.Parallel()

		input := task055RevisionDAGFixture(t)
		dag, err := projection.BuildRevisionDAG(input)
		require.NoError(t, err)
		require.NoError(t, dag.Validate())

		snapshot := dag.Snapshot()
		require.Len(t, snapshot.Projections, 9)
		require.Len(t, snapshot.Dependencies, 8)
		require.Len(t, dag.PublicResults(), 3)
		require.Len(t, dag.OperatorResults(), 3)

		gameSource := input.Results[0].Result.SourceProjection.ID()
		champion := task055CurrentProjectionID(t, input.Graph, domain.ArtifactKindChampion)
		require.True(t, input.Graph.DependsOn(champion, gameSource))

		snapshot.Projections[0] = domain.ProjectionRevision{}
		snapshot.Dependencies[0] = domain.RevisionDependency{}
		public := dag.PublicResults()
		*public[0].GameID = task055ID(9999)
		require.NoError(t, dag.Validate())
		require.NotEqual(t, task055ID(9999), *dag.PublicResults()[0].GameID)
	})

	t.Run("preflights duplicate scope identity role and command ownership", func(t *testing.T) {
		t.Parallel()

		fixture := task055RevisionDAGFixture(t)
		first := fixture.Results[0]
		second := task055DistinctGameProjectionInput(t, first, 18_000)
		cases := []struct {
			name    string
			results []projection.OfficialResultProjectionInput
			want    string
		}{
			{
				name: "scope", results: []projection.OfficialResultProjectionInput{first, first},
				want: "duplicate result scope",
			},
			{
				name: "official identity",
				want: "duplicate official result identity",
				results: func() []projection.OfficialResultProjectionInput {
					aliased := second
					aliased.Result.ID = first.Result.ID
					return []projection.OfficialResultProjectionInput{first, aliased}
				}(),
			},
			{
				name: "cross role",
				want: "identity aliases",
				results: func() []projection.OfficialResultProjectionInput {
					aliased := first
					aliased.Result.CommandID = first.Result.ID.UUID()
					return []projection.OfficialResultProjectionInput{aliased}
				}(),
			},
			{
				name: "actor aliases official revision",
				want: "identity aliases",
				results: func() []projection.OfficialResultProjectionInput {
					aliased := first
					principalID := first.Result.ID.UUID()
					aliased.Result.Actor = domain.ResultActor{
						Kind: domain.ResultActorOperator, PrincipalID: &principalID,
					}
					return []projection.OfficialResultProjectionInput{aliased}
				}(),
			},
			{
				name: "official revision aliases graph projection",
				want: "identity aliases",
				results: func() []projection.OfficialResultProjectionInput {
					aliased := first
					aliased.Result.ID = domain.OfficialResultRevisionID(
						first.Result.SourceProjection.ID().UUID(),
					)
					return []projection.OfficialResultProjectionInput{aliased}
				}(),
			},
			{
				name: "command owner",
				want: "command identity is not an exact Game result to score cascade",
				results: func() []projection.OfficialResultProjectionInput {
					aliased := second
					aliased.Result.CommandID = first.Result.CommandID
					return []projection.OfficialResultProjectionInput{first, aliased}
				}(),
			},
		}
		for _, test := range cases {
			t.Run(test.name, func(t *testing.T) {
				input := fixture
				input.Graph = domain.RevisionGraph{}
				input.Results = test.results
				_, err := projection.BuildRevisionDAG(input)
				require.ErrorIs(t, err, projection.ErrInvalidRevisionDAG)
				require.ErrorContains(t, err, test.want)
			})
		}
	})

	t.Run("preflights score attempt ownership across Series", func(t *testing.T) {
		t.Parallel()

		input, _, _ := task055TwoSeriesDecisionFixture(t)
		secondSeriesScore := input.Results[len(input.Results)-1].Score.Clone()
		secondSeriesScore.Attempts[0].GameID = input.Results[0].Result.Scope.GameID
		input.Results[len(input.Results)-1].Score = &secondSeriesScore

		_, err := projection.BuildRevisionDAG(input)
		require.ErrorIs(t, err, projection.ErrInvalidRevisionDAG)
	})

	t.Run("preflights distinct ordinary projection and score revision owners", func(t *testing.T) {
		t.Parallel()

		t.Run("result and score projection", func(t *testing.T) {
			input := task055RevisionDAGFixture(t)
			series := input.Results[2]
			score := series.Score.Clone()
			score.SourceProjection = series.Result.SourceProjection
			series.Score = &score
			series.ScoreProjection = task055ProjectionPointer(series.ResultProjection)
			input.Graph = domain.RevisionGraph{}
			input.Results = []projection.OfficialResultProjectionInput{series}

			_, err := projection.BuildRevisionDAG(input)
			require.ErrorContains(t, err, "projection metadata does not match its expected owner")
		})

		t.Run("score revision across Series", func(t *testing.T) {
			input, _, _ := task055TwoSeriesDecisionFixture(t)
			firstScore := input.Results[2].Score.ID
			secondIndex := len(input.Results) - 1
			secondScore := input.Results[secondIndex].Score.Clone()
			secondScore.ID = firstScore
			input.Results[secondIndex].Score = &secondScore
			input.Graph = domain.RevisionGraph{}

			_, err := projection.BuildRevisionDAG(input)
			require.ErrorContains(t, err, "duplicate score identity")
		})
	})
}
