package arena_test

import (
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestRevisionDAG(t *testing.T) {
	t.Run("indexes a long revision chain in linear graph bytes", func(t *testing.T) {
		input := task055LongCurrentPayloadFixture(t)
		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)
		dag, err := arena.BuildRevisionDAG(input)
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
		projection, err := domain.NewArenaProjectionRevision(
			game.SourceProjection.ID(), game.SourceProjection.TournamentID(),
			game.SourceProjection.Artifact(), game.SourceProjection.RevisionNo(),
			game.SourceProjection.PreviousRevisionID(), game.SourceProjection.CreatedAt(), largePayload,
		)
		require.NoError(t, err)
		game.SourceProjection = projection.Revision()
		duplicate := arena.OfficialResultProjectionInput{
			Result: game, ResultProjection: projection,
		}
		input.Results = make([]arena.OfficialResultProjectionInput, 64)
		for index := range input.Results {
			input.Results[index] = duplicate
		}

		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)
		_, err = arena.BuildRevisionDAG(input)
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		require.ErrorIs(t, err, arena.ErrInvalidRevisionDAG)

		const maxPreflightAllocation = 2 << 20
		allocated := after.TotalAlloc - before.TotalAlloc
		t.Logf("duplicate result preflight allocated %d bytes", allocated)
		require.Less(t, allocated, uint64(maxPreflightAllocation), "duplicate results cloned projection payloads before rejection")
	})

	t.Run("rejects oversized nested evidence before cloning", func(t *testing.T) {
		cases := []struct {
			name  string
			input func() arena.RevisionDAGInput
			want  string
		}{
			{
				name: "score attempts",
				want: "score preflight has too many attempts",
				input: func() arena.RevisionDAGInput {
					input := task055RevisionDAGFixture(t)
					score := input.Results[2].Score.Clone()
					score.Attempts = make([]arena.SeriesScoreAttemptReference, 1<<16)
					input.Results[2].Score = &score
					return input
				},
			},
			{
				name: "no-game topology",
				want: "invalid no-game preflight shape",
				input: func() arena.RevisionDAGInput {
					input := task055NoGameRevisionDAGFixture(t)
					recorded := *input.Results[0].NoGame
					recorded.Topology = make([]arena.RecordedNoGameAttempt, 1<<16)
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
				_, err := arena.BuildRevisionDAG(input)
				var after runtime.MemStats
				runtime.ReadMemStats(&after)
				require.ErrorIs(t, err, arena.ErrInvalidRevisionDAG)
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
		_, err := arena.BuildRevisionDAG(input)
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		require.ErrorIs(t, err, arena.ErrInvalidRevisionDAG)

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
			domain.ArenaArtifactKindSeriesScore, game.Result.Scope.SeriesID,
			game.Result.RecordedAt, string(make([]byte, 128<<10)),
		)
		game.ScoreProjection = &orphan
		input.Results = []arena.OfficialResultProjectionInput{game}

		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)
		_, err := arena.BuildRevisionDAG(input)
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		require.ErrorIs(t, err, arena.ErrInvalidRevisionDAG)
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
			input arena.OfficialResultProjectionInput
			want  string
		}{
			{
				name: "Series score without projection",
				input: func() arena.OfficialResultProjectionInput {
					series := fixture.Results[2]
					series.ScoreProjection = nil
					return series
				}(),
				want: "Series result preflight is missing score evidence",
			},
			{
				name: "unknown subject",
				input: func() arena.OfficialResultProjectionInput {
					game := fixture.Results[0]
					game.Result.Scope.Kind = arena.OfficialResultSubjectKind("unknown")
					return game
				}(),
				want: "unknown result subject in preflight",
			},
		}
		for _, test := range cases {
			t.Run(test.name, func(t *testing.T) {
				input := fixture
				input.Graph = domain.ArenaRevisionGraph{}
				input.Results = []arena.OfficialResultProjectionInput{test.input}

				_, err := arena.BuildRevisionDAG(input)
				require.ErrorIs(t, err, arena.ErrInvalidRevisionDAG)
				require.ErrorContains(t, err, test.want)
			})
		}
	})

	t.Run("retains the explicit current result chain through champion", func(t *testing.T) {
		t.Parallel()

		input := task055RevisionDAGFixture(t)
		dag, err := arena.BuildRevisionDAG(input)
		require.NoError(t, err)
		require.NoError(t, dag.Validate())

		snapshot := dag.Snapshot()
		require.Len(t, snapshot.Projections, 9)
		require.Len(t, snapshot.Dependencies, 8)
		require.Len(t, dag.PublicResults(), 3)
		require.Len(t, dag.OperatorResults(), 3)

		gameSource := input.Results[0].Result.SourceProjection.ID()
		champion := task055CurrentProjectionID(t, input.Graph, domain.ArenaArtifactKindChampion)
		require.True(t, input.Graph.DependsOn(champion, gameSource))

		snapshot.Projections[0] = domain.ArenaProjectionRevision{}
		snapshot.Dependencies[0] = domain.ArenaRevisionDependency{}
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
			results []arena.OfficialResultProjectionInput
			want    string
		}{
			{
				name: "scope", results: []arena.OfficialResultProjectionInput{first, first},
				want: "duplicate result scope",
			},
			{
				name: "official identity",
				want: "duplicate official result identity",
				results: func() []arena.OfficialResultProjectionInput {
					aliased := second
					aliased.Result.ID = first.Result.ID
					return []arena.OfficialResultProjectionInput{first, aliased}
				}(),
			},
			{
				name: "cross role",
				want: "identity aliases",
				results: func() []arena.OfficialResultProjectionInput {
					aliased := first
					aliased.Result.CommandID = first.Result.ID.UUID()
					return []arena.OfficialResultProjectionInput{aliased}
				}(),
			},
			{
				name: "actor aliases official revision",
				want: "identity aliases",
				results: func() []arena.OfficialResultProjectionInput {
					aliased := first
					principalID := first.Result.ID.UUID()
					aliased.Result.Actor = arena.ArenaResultActor{
						Kind: arena.ArenaResultActorOperator, PrincipalID: &principalID,
					}
					return []arena.OfficialResultProjectionInput{aliased}
				}(),
			},
			{
				name: "official revision aliases graph projection",
				want: "identity aliases",
				results: func() []arena.OfficialResultProjectionInput {
					aliased := first
					aliased.Result.ID = domain.ArenaOfficialResultRevisionID(
						first.Result.SourceProjection.ID().UUID(),
					)
					return []arena.OfficialResultProjectionInput{aliased}
				}(),
			},
			{
				name: "command owner",
				want: "command identity is not an exact Game result to score cascade",
				results: func() []arena.OfficialResultProjectionInput {
					aliased := second
					aliased.Result.CommandID = first.Result.CommandID
					return []arena.OfficialResultProjectionInput{first, aliased}
				}(),
			},
		}
		for _, test := range cases {
			t.Run(test.name, func(t *testing.T) {
				input := fixture
				input.Graph = domain.ArenaRevisionGraph{}
				input.Results = test.results
				_, err := arena.BuildRevisionDAG(input)
				require.ErrorIs(t, err, arena.ErrInvalidRevisionDAG)
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

		_, err := arena.BuildRevisionDAG(input)
		require.ErrorIs(t, err, arena.ErrInvalidRevisionDAG)
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
			input.Graph = domain.ArenaRevisionGraph{}
			input.Results = []arena.OfficialResultProjectionInput{series}

			_, err := arena.BuildRevisionDAG(input)
			require.ErrorContains(t, err, "projection metadata does not match its expected owner")
		})

		t.Run("score revision across Series", func(t *testing.T) {
			input, _, _ := task055TwoSeriesDecisionFixture(t)
			firstScore := input.Results[2].Score.ID
			secondIndex := len(input.Results) - 1
			secondScore := input.Results[secondIndex].Score.Clone()
			secondScore.ID = firstScore
			input.Results[secondIndex].Score = &secondScore
			input.Graph = domain.ArenaRevisionGraph{}

			_, err := arena.BuildRevisionDAG(input)
			require.ErrorContains(t, err, "duplicate score identity")
		})
	})

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
		graph, err := domain.NewArenaRevisionGraph(projections, dependencies)
		require.NoError(t, err)
		input.Graph = graph

		_, err = arena.BuildRevisionDAG(input)
		require.ErrorIs(t, err, arena.ErrInvalidRevisionDAG)
	})

	t.Run("rejects stale current heads duplicate scopes and disconnected descendants", func(t *testing.T) {
		t.Parallel()

		input := task055RevisionDAGFixture(t)
		duplicate := input
		duplicate.Results = append(duplicate.Results, duplicate.Results[0])
		_, err := arena.BuildRevisionDAG(duplicate)
		require.ErrorIs(t, err, arena.ErrInvalidRevisionDAG)

		stale := input
		old := stale.Results[2].ResultProjection
		oldRevision := old.Revision()
		current := task055SuccessorProjection(
			t, old, 9090, oldRevision.CreatedAt().Add(time.Second), "current-series-result",
		)
		projections := append(stale.Graph.Projections(), current)
		dependencies := append(stale.Graph.Dependencies(), domain.ArenaRevisionDependency{
			SourceRevisionID: oldRevision.ID(), DerivedRevisionID: current.Revision().ID(),
		})
		graph, err := domain.NewArenaRevisionGraph(projections, dependencies)
		require.NoError(t, err)
		stale.Graph = graph
		_, err = arena.BuildRevisionDAG(stale)
		require.ErrorIs(t, err, arena.ErrInvalidRevisionDAG)

		disconnected := input
		dependencies = disconnected.Graph.Dependencies()
		standingsID := task055CurrentProjectionID(
			t, disconnected.Graph, domain.ArenaArtifactKindStandings,
		)
		filtered := dependencies[:0]
		for _, dependency := range dependencies {
			if dependency.DerivedRevisionID != standingsID {
				filtered = append(filtered, dependency)
			}
		}
		graph, err = domain.NewArenaRevisionGraph(disconnected.Graph.Projections(), filtered)
		require.NoError(t, err)
		disconnected.Graph = graph
		_, err = arena.BuildRevisionDAG(disconnected)
		require.ErrorIs(t, err, arena.ErrInvalidRevisionDAG)
	})

	t.Run("rejects a score command detached from its exact Game result", func(t *testing.T) {
		t.Parallel()

		input := task055RevisionDAGFixture(t)
		score := input.Results[2].Score.Clone()
		score.CommandID = task055ID(9997)
		input.Results[2].Score = task055ScoreHeadPointer(score)

		_, err := arena.BuildRevisionDAG(input)
		require.ErrorIs(t, err, arena.ErrInvalidRevisionDAG)
	})

	t.Run("rejects a coherent no-show projection and graph payload splice", func(t *testing.T) {
		t.Parallel()

		input := task055NoGameRevisionDAGFixture(t)
		_, err := arena.BuildRevisionDAG(input)
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
		graph, err := domain.NewArenaRevisionGraph(projections, input.Graph.Dependencies())
		require.NoError(t, err)
		input.Graph = graph

		_, err = arena.BuildRevisionDAG(input)
		require.ErrorIs(t, err, arena.ErrInvalidRevisionDAG)
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
			changed, err := domain.NewArenaProjectionRevision(
				gameRevision.ID(), gameRevision.TournamentID(), gameRevision.Artifact(),
				gameRevision.RevisionNo(), gameRevision.PreviousRevisionID(), gameRevision.CreatedAt(),
				[]byte("tampered-current-game"),
			)
			require.NoError(t, err)
			projections[index] = changed
		}
		graph, err := domain.NewArenaRevisionGraph(projections, input.Graph.Dependencies())
		require.NoError(t, err)
		tampered := input
		tampered.Graph = graph
		_, err = arena.BuildRevisionDAG(tampered)
		require.ErrorIs(t, err, arena.ErrInvalidRevisionDAG)

		shortcut := input
		dependencies := append([]domain.ArenaRevisionDependency(nil), input.Graph.Dependencies()...)
		dependencies = append(dependencies, domain.ArenaRevisionDependency{
			SourceRevisionID:  input.Results[0].Result.SourceProjection.ID(),
			DerivedRevisionID: input.Results[2].Result.SourceProjection.ID(),
		})
		graph, err = domain.NewArenaRevisionGraph(input.Graph.Projections(), dependencies)
		require.NoError(t, err)
		shortcut.Graph = graph
		_, err = arena.BuildRevisionDAG(shortcut)
		require.ErrorIs(t, err, arena.ErrInvalidRevisionDAG)
	})

	t.Run("supports the exact no Golden path and canonical input order", func(t *testing.T) {
		t.Parallel()

		input := task055RevisionDAGFixture(t)
		goldenID := task055CurrentProjectionID(t, input.Graph, domain.ArenaArtifactKindGoldenGroup)
		standingsID := task055CurrentProjectionID(t, input.Graph, domain.ArenaArtifactKindStandings)
		topFourID := task055CurrentProjectionID(t, input.Graph, domain.ArenaArtifactKindTopFour)
		projections := make([]domain.ArenaProjectionRevision, 0, len(input.Graph.Projections())-1)
		for _, projection := range input.Graph.Projections() {
			if projection.Revision().ID() != goldenID {
				projections = append(projections, projection)
			}
		}
		dependencies := make([]domain.ArenaRevisionDependency, 0, len(input.Graph.Dependencies())-1)
		for _, dependency := range input.Graph.Dependencies() {
			if dependency.SourceRevisionID != goldenID && dependency.DerivedRevisionID != goldenID {
				dependencies = append(dependencies, dependency)
			}
		}
		dependencies = append(dependencies, domain.ArenaRevisionDependency{
			SourceRevisionID: standingsID, DerivedRevisionID: topFourID,
		})
		graph, err := domain.NewArenaRevisionGraph(projections, dependencies)
		require.NoError(t, err)
		input.Graph = graph
		first, err := arena.BuildRevisionDAG(input)
		require.NoError(t, err)

		for left, right := 0, len(input.Results)-1; left < right; left, right = left+1, right-1 {
			input.Results[left], input.Results[right] = input.Results[right], input.Results[left]
		}
		second, err := arena.BuildRevisionDAG(input)
		require.NoError(t, err)
		require.Equal(t, first.PublicResults(), second.PublicResults())
		require.Equal(t, first.OperatorResults(), second.OperatorResults())
		require.Equal(t, first.Snapshot(), second.Snapshot())
	})

	t.Run("rejects cross-role revision aliases", func(t *testing.T) {
		t.Parallel()

		input := task055RevisionDAGFixture(t)
		score := input.Results[2].Score.Clone()
		aliasedID := domain.ArenaOfficialResultRevisionID(score.ID.UUID())
		input.Results[0].Result.ID = aliasedID
		score.Attempts[0].CurrentGameResultRevisionID = aliasedID
		input.Results[2].Score = task055ScoreHeadPointer(score)

		_, err := arena.BuildRevisionDAG(input)
		require.ErrorIs(t, err, arena.ErrInvalidRevisionDAG)
	})

	t.Run("requires every mandatory downstream kind and exact Golden fan-in", func(t *testing.T) {
		t.Parallel()

		input := task055RevisionDAGFixture(t)
		championID := task055CurrentProjectionID(t, input.Graph, domain.ArenaArtifactKindChampion)
		projections := make([]domain.ArenaProjectionRevision, 0, len(input.Graph.Projections())-1)
		for _, projection := range input.Graph.Projections() {
			if projection.Revision().ID() != championID {
				projections = append(projections, projection)
			}
		}
		dependencies := make([]domain.ArenaRevisionDependency, 0, len(input.Graph.Dependencies())-1)
		for _, dependency := range input.Graph.Dependencies() {
			if dependency.SourceRevisionID != championID && dependency.DerivedRevisionID != championID {
				dependencies = append(dependencies, dependency)
			}
		}
		graph, err := domain.NewArenaRevisionGraph(projections, dependencies)
		require.NoError(t, err)
		missing := input
		missing.Graph = graph
		_, err = arena.BuildRevisionDAG(missing)
		require.ErrorIs(t, err, arena.ErrInvalidRevisionDAG)

		standingsID := task055CurrentProjectionID(t, input.Graph, domain.ArenaArtifactKindStandings)
		topFourID := task055CurrentProjectionID(t, input.Graph, domain.ArenaArtifactKindTopFour)
		secondGolden := task055Projection(
			t, 9700, input.Results[0].Result.Scope.TournamentID,
			domain.ArenaArtifactKindGoldenGroup, task055ID(9701),
			time.Date(2026, time.September, 1, 11, 0, 8, 500_000_000, time.UTC),
			"second-golden",
		)
		projections = append(input.Graph.Projections(), secondGolden)
		dependencies = append([]domain.ArenaRevisionDependency(nil), input.Graph.Dependencies()...)
		dependencies = append(dependencies,
			domain.ArenaRevisionDependency{
				SourceRevisionID: standingsID, DerivedRevisionID: secondGolden.Revision().ID(),
			},
			domain.ArenaRevisionDependency{
				SourceRevisionID: secondGolden.Revision().ID(), DerivedRevisionID: topFourID,
			},
		)
		graph, err = domain.NewArenaRevisionGraph(projections, dependencies)
		require.NoError(t, err)
		input.Graph = graph
		_, err = arena.BuildRevisionDAG(input)
		require.NoError(t, err)
	})

	t.Run("rejects stale current Game and score projections", func(t *testing.T) {
		t.Parallel()

		for _, subject := range []string{"Game", "score"} {
			t.Run(subject, func(t *testing.T) {
				t.Parallel()
				input := task055RevisionDAGFixture(t)
				var old domain.ArenaProjectionRevision
				if subject == "Game" {
					old = input.Results[0].ResultProjection
				} else {
					old = *input.Results[2].ScoreProjection
				}
				successor := task055SuccessorProjection(
					t, old, 9800+len(subject), old.Revision().CreatedAt().Add(time.Second),
					"new-current-"+subject,
				)
				dependencies := append([]domain.ArenaRevisionDependency(nil), input.Graph.Dependencies()...)
				dependencies = append(dependencies, domain.ArenaRevisionDependency{
					SourceRevisionID: old.Revision().ID(), DerivedRevisionID: successor.Revision().ID(),
				})
				graph, err := domain.NewArenaRevisionGraph(
					append(input.Graph.Projections(), successor), dependencies,
				)
				require.NoError(t, err)
				input.Graph = graph
				_, err = arena.BuildRevisionDAG(input)
				require.ErrorIs(t, err, arena.ErrInvalidRevisionDAG)
			})
		}
	})
}

func task055LongCurrentPayloadFixture(t *testing.T) arena.RevisionDAGInput {
	t.Helper()
	input := task055RevisionDAGFixture(t)
	tournamentID := input.Results[0].Result.Scope.TournamentID
	artifact := domain.ArenaArtifactRef{
		Kind: domain.ArenaArtifactKindChampion, EntityID: tournamentID,
	}
	previous, exists := input.Graph.CurrentRevision(artifact)
	require.True(t, exists)
	projections := input.Graph.Projections()
	dependencies := input.Graph.Dependencies()

	const revisionCount = 128
	for index := 0; index < revisionCount; index++ {
		previousRevision := previous.Revision()
		previousID := previousRevision.ID()
		payload := []byte{byte(index)}
		if index == revisionCount-1 {
			payload = make([]byte, 128<<10)
		}
		next, err := domain.NewArenaProjectionRevision(
			domain.ArenaDerivedRevisionID(task055ID(10_000+index)), tournamentID,
			artifact, previousRevision.RevisionNo()+1, &previousID,
			previousRevision.CreatedAt().Add(time.Nanosecond), payload,
		)
		require.NoError(t, err)
		projections = append(projections, next)
		dependencies = append(dependencies, domain.ArenaRevisionDependency{
			SourceRevisionID: previousID, DerivedRevisionID: next.Revision().ID(),
		})
		previous = next
	}
	dependencies = append(dependencies, domain.ArenaRevisionDependency{
		SourceRevisionID:  task055CurrentProjectionID(t, input.Graph, domain.ArenaArtifactKindBracket),
		DerivedRevisionID: previous.Revision().ID(),
	})
	graph, err := domain.NewArenaRevisionGraph(projections, dependencies)
	require.NoError(t, err)
	input.Graph = graph
	return input
}

func task055DistinctGameProjectionInput(
	t *testing.T,
	base arena.OfficialResultProjectionInput,
	idBase int,
) arena.OfficialResultProjectionInput {
	t.Helper()
	clone := base.Result.Clone()
	clone.Scope.GameID = task055ID(idBase)
	clone.ID = domain.ArenaOfficialResultRevisionID(task055ID(idBase + 1))
	clone.CommandID = task055ID(idBase + 2)
	projection := task055Projection(
		t, idBase+3, clone.Scope.TournamentID, domain.ArenaArtifactKindGameResult,
		clone.Scope.GameID, clone.SourceProjection.CreatedAt(), fmt.Sprintf("distinct-game-%d", idBase),
	)
	clone.SourceProjection = projection.Revision()
	require.NoError(t, clone.Validate())
	return arena.OfficialResultProjectionInput{Result: clone, ResultProjection: projection}
}

func task055NoGameRevisionDAGFixture(t *testing.T) arena.RevisionDAGInput {
	t.Helper()
	resolvedAt := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	recorded := task055NoGameEvidence(t, domain.ArenaSeriesFormatBO3, resolvedAt, 14_000)
	projections := append([]domain.ArenaProjectionRevision(nil), recorded.GameProjections...)
	projections = append(projections, recorded.ScoreProjection, recorded.ResultProjection)
	kinds := []domain.ArenaArtifactKind{
		domain.ArenaArtifactKindStandings,
		domain.ArenaArtifactKindGoldenGroup,
		domain.ArenaArtifactKindTopFour,
		domain.ArenaArtifactKindBracket,
		domain.ArenaArtifactKindChampion,
	}
	for index, kind := range kinds {
		projections = append(projections, task055Projection(
			t, 14_100+index, recorded.Scope.TournamentID, kind, recorded.Scope.TournamentID,
			resolvedAt.Add(time.Duration(index)*time.Second), fmt.Sprintf("no-show-downstream-%d", index),
		))
	}
	dependencies := append([]domain.ArenaRevisionDependency(nil), recorded.GameDependencies...)
	dependencies = append(dependencies, recorded.ResultDependency)
	for index := len(recorded.GameProjections) + 2; index < len(projections); index++ {
		sourceID := recorded.ResultProjection.Revision().ID()
		if index > len(recorded.GameProjections)+2 {
			sourceID = projections[index-1].Revision().ID()
		}
		dependencies = append(dependencies, domain.ArenaRevisionDependency{
			SourceRevisionID: sourceID, DerivedRevisionID: projections[index].Revision().ID(),
		})
	}
	graph, err := domain.NewArenaRevisionGraph(projections, dependencies)
	require.NoError(t, err)
	return arena.RevisionDAGInput{
		Graph:   graph,
		Results: []arena.OfficialResultProjectionInput{{NoGame: &recorded}},
	}
}

func task055RepeatedNoGameProjectionFixture(t *testing.T) arena.RevisionDAGInput {
	t.Helper()
	input := task055NoGameRevisionDAGFixture(t)
	recorded := *input.Results[0].NoGame
	originalProjection := recorded.GameProjections[0]
	originalRevision := originalProjection.Revision()
	largeProjection, err := domain.NewArenaProjectionRevision(
		originalRevision.ID(), originalRevision.TournamentID(), originalRevision.Artifact(),
		originalRevision.RevisionNo(), originalRevision.PreviousRevisionID(),
		originalRevision.CreatedAt(), make([]byte, 128<<10),
	)
	require.NoError(t, err)

	const gameCount = 16
	firstOrdinal := recorded.GameResults[0].Ordinal
	recorded.GameResults = make([]arena.NormalNoShowGameRevision, gameCount)
	recorded.Topology = make([]arena.RecordedNoGameAttempt, gameCount)
	recorded.GameSourceRevisions = make([]domain.ArenaDerivedRevision, gameCount)
	recorded.GameProjections = make([]domain.ArenaProjectionRevision, gameCount)
	recorded.GameDependencies = make([]domain.ArenaRevisionDependency, gameCount)
	recorded.Score.GameResultRevisionIDs = make([]domain.ArenaOfficialResultRevisionID, gameCount)
	for index := 0; index < gameCount; index++ {
		resultID := domain.ArenaOfficialResultRevisionID(task055ID(19_000 + index))
		gameID := task055ID(19_100 + index)
		recorded.GameResults[index] = arena.NormalNoShowGameRevision{
			ID: resultID, Ordinal: firstOrdinal + index, GameID: gameID,
			State:      domain.ArenaGameStateCancelled,
			Reason:     domain.ArenaGameResultReasonSeriesCancelled,
			RecordedAt: recorded.ResolvedAt,
		}
		recorded.Topology[index] = arena.RecordedNoGameAttempt{
			SeriesID: recorded.Scope.SeriesID, SlotID: task055ID(19_200 + index),
			SlotPosition: index + 1, GameID: gameID, AttemptNo: 1,
			ResultRevisionID: resultID,
		}
		recorded.GameSourceRevisions[index] = largeProjection.Revision()
		recorded.GameProjections[index] = largeProjection
		recorded.GameDependencies[index] = domain.ArenaRevisionDependency{
			SourceRevisionID:  largeProjection.Revision().ID(),
			DerivedRevisionID: recorded.ScoreProjection.Revision().ID(),
		}
		recorded.Score.GameResultRevisionIDs[index] = resultID
	}
	recorded.Score.Ordinal = firstOrdinal + gameCount
	recorded.Series.Ordinal = firstOrdinal + gameCount + 1
	input.Results[0].NoGame = &recorded

	projections := input.Graph.Projections()
	for index := range projections {
		if projections[index].Revision().ID() == largeProjection.Revision().ID() {
			projections[index] = largeProjection
		}
	}
	graph, err := domain.NewArenaRevisionGraph(projections, input.Graph.Dependencies())
	require.NoError(t, err)
	input.Graph = graph
	return input
}

func task055RevisionDAGFixture(t *testing.T) arena.RevisionDAGInput {
	t.Helper()
	tournamentID := task055ID(1000)
	seriesID := task055ID(1001)
	gameID := task055ID(1002)
	secondGameID := task055ID(1005)
	firstID := task055ID(1003)
	secondID := task055ID(1004)
	baseTime := time.Date(2026, time.September, 1, 11, 0, 0, 0, time.UTC)

	game := task055GameResultHead(
		t, tournamentID, seriesID, gameID, firstID,
		domain.ArenaGameStateCompleted, domain.ArenaGameResultReasonSolved,
		task055UUIDPointer(firstID), baseTime.Add(time.Second), 1100,
	)
	gameProjection := task055ExactProjection(t, game.SourceProjection, "game-1100")
	secondGame := task055GameResultHead(
		t, tournamentID, seriesID, secondGameID, firstID,
		domain.ArenaGameStateCompleted, domain.ArenaGameResultReasonSolved,
		task055UUIDPointer(firstID), baseTime.Add(2*time.Second), 1110,
	)
	secondGameProjection := task055ExactProjection(t, secondGame.SourceProjection, "game-1110")
	score := task055ScoreHead(
		t, tournamentID, seriesID, firstID, secondID,
		domain.ArenaSeriesFormatBO3, domain.ArenaSeriesScore{FirstParticipantWins: 2},
		2, baseTime.Add(3*time.Second), 1120,
	)
	score.Attempts[0].CurrentGameResultRevisionID = game.ID
	score.Attempts[0].GameID = gameID
	score.Attempts[1].CurrentGameResultRevisionID = secondGame.ID
	score.Attempts[1].GameID = secondGameID
	score.CommandID = secondGame.CommandID
	commandAttempt := score.Attempts[1]
	score.CommandAttempt = &commandAttempt
	require.NoError(t, score.Validate())
	scoreProjection := task055ExactProjection(t, score.SourceProjection, "score-1120")
	series := task055SeriesResultHead(
		t, tournamentID, seriesID, score.ID,
		domain.ArenaSeriesStateCompleted, arena.ArenaSeriesResultReasonScoreComplete,
		task055UUIDPointer(firstID), baseTime.Add(5*time.Second), 1140,
	)
	seriesProjection := task055ExactProjection(t, series.SourceProjection, "series-1140")

	projections := make([]domain.ArenaProjectionRevision, 0, 9)
	projections = append(projections, gameProjection, secondGameProjection, scoreProjection, seriesProjection)
	kinds := []domain.ArenaArtifactKind{
		domain.ArenaArtifactKindStandings,
		domain.ArenaArtifactKindGoldenGroup,
		domain.ArenaArtifactKindTopFour,
		domain.ArenaArtifactKindBracket,
		domain.ArenaArtifactKindChampion,
	}
	for index, kind := range kinds {
		projections = append(projections, task055Projection(
			t, 1160+index, tournamentID, kind, tournamentID,
			baseTime.Add(time.Duration(7+index)*time.Second), fmt.Sprintf("downstream-%d", index),
		))
	}
	dependencies := []domain.ArenaRevisionDependency{
		{SourceRevisionID: gameProjection.Revision().ID(), DerivedRevisionID: scoreProjection.Revision().ID()},
		{SourceRevisionID: secondGameProjection.Revision().ID(), DerivedRevisionID: scoreProjection.Revision().ID()},
		{SourceRevisionID: scoreProjection.Revision().ID(), DerivedRevisionID: seriesProjection.Revision().ID()},
	}
	for index := 4; index < len(projections); index++ {
		dependencies = append(dependencies, domain.ArenaRevisionDependency{
			SourceRevisionID:  projections[index-1].Revision().ID(),
			DerivedRevisionID: projections[index].Revision().ID(),
		})
	}
	graph, err := domain.NewArenaRevisionGraph(projections, dependencies)
	require.NoError(t, err)

	return arena.RevisionDAGInput{
		Graph: graph,
		Results: []arena.OfficialResultProjectionInput{
			{Result: game, ResultProjection: gameProjection},
			{Result: secondGame, ResultProjection: secondGameProjection},
			{
				Result: series, ResultProjection: seriesProjection,
				Score: task055ScoreHeadPointer(score), ScoreProjection: task055ProjectionPointer(scoreProjection),
			},
		},
	}
}

func task055SuccessorProjection(
	t *testing.T,
	previous domain.ArenaProjectionRevision,
	id int,
	createdAt time.Time,
	payload string,
) domain.ArenaProjectionRevision {
	t.Helper()
	previousRevision := previous.Revision()
	previousID := previousRevision.ID()
	projection, err := domain.NewArenaProjectionRevision(
		domain.ArenaDerivedRevisionID(task055ID(id)), previousRevision.TournamentID(),
		previousRevision.Artifact(), previousRevision.RevisionNo()+1, &previousID,
		createdAt, []byte(payload),
	)
	require.NoError(t, err)
	return projection
}

func task055CurrentProjectionID(
	t *testing.T,
	graph domain.ArenaRevisionGraph,
	kind domain.ArenaArtifactKind,
) domain.ArenaDerivedRevisionID {
	t.Helper()
	for _, projection := range graph.Projections() {
		if projection.Revision().Artifact().Kind == kind {
			return projection.Revision().ID()
		}
	}
	t.Fatalf("missing %s projection", kind)
	return domain.ArenaDerivedRevisionID{}
}
