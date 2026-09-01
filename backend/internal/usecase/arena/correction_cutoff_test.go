package arena_test

import (
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestCorrectionCutoffTraversal(t *testing.T) {
	fixture := task055RevisionDAGFixture(t)
	dag, err := arena.BuildRevisionDAG(fixture)
	require.NoError(t, err)
	before := dag.Snapshot()
	target := fixture.Results[0].ResultProjection.Revision().ID()

	t.Run("returns every descendant once in causal order", func(t *testing.T) {
		t.Parallel()

		first, err := arena.EvaluateCorrectionCutoff(arena.CorrectionCutoffInput{
			DAG: dag, TournamentID: fixture.Results[0].Result.Scope.TournamentID,
			TargetRevisionID: target, TournamentState: domain.ArenaTournamentStatePlayoffs,
		})
		require.NoError(t, err)
		second, err := arena.EvaluateCorrectionCutoff(arena.CorrectionCutoffInput{
			DAG: dag, TournamentID: fixture.Results[0].Result.Scope.TournamentID,
			TargetRevisionID: target, TournamentState: domain.ArenaTournamentStatePlayoffs,
		})
		require.NoError(t, err)

		wantKinds := []domain.ArenaArtifactKind{
			domain.ArenaArtifactKindSeriesScore,
			domain.ArenaArtifactKindSeriesResult,
			domain.ArenaArtifactKindStandings,
			domain.ArenaArtifactKindGoldenGroup,
			domain.ArenaArtifactKindTopFour,
			domain.ArenaArtifactKindBracket,
			domain.ArenaArtifactKindChampion,
		}
		require.Equal(t, wantKinds, correctionArtifactKinds(first.Descendants()))
		require.Equal(t, first.Descendants(), second.Descendants())

		returned := first.Descendants()
		returned[0] = domain.ArenaDerivedRevision{}
		require.Equal(t, wantKinds, correctionArtifactKinds(first.Descendants()))
		require.Equal(t, before, dag.Snapshot())
	})

	t.Run("rejects every irreversible event with one stable code", func(t *testing.T) {
		t.Parallel()

		seriesProjection := fixture.Results[2].ResultProjection.Revision()
		kinds := []arena.CorrectionCutoffKind{
			arena.CorrectionCutoffWaveStarted,
			arena.CorrectionCutoffTaskDelivered,
			arena.CorrectionCutoffNoShowRecorded,
			arena.CorrectionCutoffForfeitRecorded,
			arena.CorrectionCutoffGoldenAllocated,
		}
		for index, kind := range kinds {
			t.Run(string(kind), func(t *testing.T) {
				event := arena.CorrectionCutoffEvent{
					ID: task055ID(30_000 + index), Kind: kind,
					TournamentID:     fixture.Results[0].Result.Scope.TournamentID,
					SourceRevisionID: seriesProjection.ID(),
					OccurredAt:       seriesProjection.CreatedAt().Add(time.Second),
				}
				events := []arena.CorrectionCutoffEvent{event}
				_, err := arena.EvaluateCorrectionCutoff(arena.CorrectionCutoffInput{
					DAG: dag, TournamentID: event.TournamentID, TargetRevisionID: target,
					TournamentState: domain.ArenaTournamentStatePlayoffs, Events: events,
				})
				require.ErrorIs(t, err, arena.ErrCorrectionCutoff)
				require.Equal(t, arena.CorrectionRejectionCutoff, arena.CorrectionCode(err))
				require.Equal(t, []arena.CorrectionCutoffEvent{event}, events)
				require.Equal(t, before, dag.Snapshot())
			})
		}

		_, err := arena.EvaluateCorrectionCutoff(arena.CorrectionCutoffInput{
			DAG: dag, TournamentID: fixture.Results[0].Result.Scope.TournamentID,
			TargetRevisionID: target, TournamentState: domain.ArenaTournamentStateCompleted,
		})
		require.ErrorIs(t, err, arena.ErrCorrectionCutoff)
		require.Equal(t, arena.CorrectionRejectionCutoff, arena.CorrectionCode(err))
	})

	t.Run("rejects malformed and cross-tournament evidence stably", func(t *testing.T) {
		t.Parallel()

		_, err := arena.EvaluateCorrectionCutoff(arena.CorrectionCutoffInput{
			DAG: dag, TournamentID: task055ID(31_000), TargetRevisionID: target,
			TournamentState: domain.ArenaTournamentStatePlayoffs,
		})
		require.ErrorIs(t, err, arena.ErrInvalidCorrection)
		require.Equal(t, arena.CorrectionRejectionCrossTournament, arena.CorrectionCode(err))

		_, err = arena.EvaluateCorrectionCutoff(arena.CorrectionCutoffInput{
			DAG: dag, TournamentID: fixture.Results[0].Result.Scope.TournamentID,
			TargetRevisionID: target, TournamentState: domain.ArenaTournamentStatePlayoffs,
			Events: []arena.CorrectionCutoffEvent{{
				ID: task055ID(31_001), Kind: arena.CorrectionCutoffTaskDelivered,
				TournamentID:     fixture.Results[0].Result.Scope.TournamentID,
				SourceRevisionID: target,
			}},
		})
		require.ErrorIs(t, err, arena.ErrInvalidCorrection)
		require.Equal(t, arena.CorrectionRejectionMalformed, arena.CorrectionCode(err))
		require.Equal(t, before, dag.Snapshot())
	})

	t.Run("scopes cutoff events to the full descendant closure", func(t *testing.T) {
		t.Parallel()

		secondGame := fixture.Results[1].Result.SourceProjection
		unrelated := arena.CorrectionCutoffEvent{
			ID: task055ID(32_001), Kind: arena.CorrectionCutoffTaskDelivered,
			TournamentID:     fixture.Results[0].Result.Scope.TournamentID,
			SourceRevisionID: secondGame.ID(), OccurredAt: secondGame.CreatedAt(),
		}
		cutoff, err := arena.EvaluateCorrectionCutoff(arena.CorrectionCutoffInput{
			DAG: dag, TournamentID: unrelated.TournamentID, TargetRevisionID: target,
			TournamentState: domain.ArenaTournamentStatePlayoffs,
			Events:          []arena.CorrectionCutoffEvent{unrelated},
		})
		require.NoError(t, err)
		require.Len(t, cutoff.Descendants(), 7)

		champion := task055CurrentProjectionID(t, fixture.Graph, domain.ArenaArtifactKindChampion)
		var championRevision domain.ArenaDerivedRevision
		for _, projection := range fixture.Graph.Projections() {
			if projection.Revision().ID() == champion {
				championRevision = projection.Revision()
				break
			}
		}
		equalBoundary := arena.CorrectionCutoffEvent{
			ID: task055ID(32_002), Kind: arena.CorrectionCutoffGoldenAllocated,
			TournamentID: unrelated.TournamentID, SourceRevisionID: champion,
			OccurredAt: championRevision.CreatedAt(),
		}
		_, err = arena.EvaluateCorrectionCutoff(arena.CorrectionCutoffInput{
			DAG: dag, TournamentID: unrelated.TournamentID, TargetRevisionID: target,
			TournamentState: domain.ArenaTournamentStatePlayoffs,
			Events:          []arena.CorrectionCutoffEvent{equalBoundary},
		})
		require.ErrorIs(t, err, arena.ErrCorrectionCutoff)
		require.Equal(t, arena.CorrectionRejectionCutoff, arena.CorrectionCode(err))
	})

	t.Run("rejects stale aliased and over-bound cutoff evidence", func(t *testing.T) {
		t.Parallel()

		tournamentID := fixture.Results[0].Result.Scope.TournamentID
		_, err := arena.EvaluateCorrectionCutoff(arena.CorrectionCutoffInput{
			DAG: dag, TournamentID: tournamentID,
			TargetRevisionID: domain.ArenaDerivedRevisionID(task055ID(33_001)),
			TournamentState:  domain.ArenaTournamentStatePlayoffs,
		})
		require.ErrorIs(t, err, arena.ErrInvalidCorrection)
		require.Equal(t, arena.CorrectionRejectionStale, arena.CorrectionCode(err))

		secondGame := fixture.Results[1].Result.SourceProjection
		duplicate := arena.CorrectionCutoffEvent{
			ID: task055ID(33_002), Kind: arena.CorrectionCutoffTaskDelivered,
			TournamentID: tournamentID, SourceRevisionID: secondGame.ID(),
			OccurredAt: secondGame.CreatedAt(),
		}
		_, err = arena.EvaluateCorrectionCutoff(arena.CorrectionCutoffInput{
			DAG: dag, TournamentID: tournamentID, TargetRevisionID: target,
			TournamentState: domain.ArenaTournamentStatePlayoffs,
			Events:          []arena.CorrectionCutoffEvent{duplicate, duplicate},
		})
		require.Equal(t, arena.CorrectionRejectionIdentityAlias, arena.CorrectionCode(err))

		aliased := duplicate
		aliased.ID = target.UUID()
		_, err = arena.EvaluateCorrectionCutoff(arena.CorrectionCutoffInput{
			DAG: dag, TournamentID: tournamentID, TargetRevisionID: target,
			TournamentState: domain.ArenaTournamentStatePlayoffs,
			Events:          []arena.CorrectionCutoffEvent{aliased},
		})
		require.Equal(t, arena.CorrectionRejectionIdentityAlias, arena.CorrectionCode(err))

		events := make([]arena.CorrectionCutoffEvent, 4096)
		for index := range events {
			events[index] = arena.CorrectionCutoffEvent{
				ID: task055ID(50_000 + index), Kind: arena.CorrectionCutoffWaveStarted,
				TournamentID: tournamentID, SourceRevisionID: secondGame.ID(),
				OccurredAt: secondGame.CreatedAt(),
			}
		}
		_, err = arena.EvaluateCorrectionCutoff(arena.CorrectionCutoffInput{
			DAG: dag, TournamentID: tournamentID, TargetRevisionID: target,
			TournamentState: domain.ArenaTournamentStatePlayoffs, Events: events,
		})
		require.NoError(t, err)
		overEvents := make([]arena.CorrectionCutoffEvent, len(events)+1)
		copy(overEvents, events)
		_, err = arena.EvaluateCorrectionCutoff(arena.CorrectionCutoffInput{
			DAG: dag, TournamentID: tournamentID, TargetRevisionID: target,
			TournamentState: domain.ArenaTournamentStatePlayoffs, Events: overEvents,
		})
		require.Equal(t, arena.CorrectionRejectionMalformed, arena.CorrectionCode(err))
		require.Equal(t, before, dag.Snapshot())
	})

	t.Run("enforces correction graph caps at deterministic boundaries", func(t *testing.T) {
		tournamentID := fixture.Results[0].Result.Scope.TournamentID
		input := func(candidate arena.RevisionDAG) arena.CorrectionCutoffInput {
			return arena.CorrectionCutoffInput{
				DAG: candidate, TournamentID: tournamentID, TargetRevisionID: target,
				TournamentState: domain.ArenaTournamentStatePlayoffs,
			}
		}

		atProjectionCap := task056DAGWithProjectionCount(t, fixture, 512)
		_, err := arena.EvaluateCorrectionCutoff(input(atProjectionCap))
		require.NoError(t, err)
		overProjectionCap := task056DAGWithProjectionCount(t, fixture, 513)
		_, err = arena.EvaluateCorrectionCutoff(input(overProjectionCap))
		require.Equal(t, arena.CorrectionRejectionMalformed, arena.CorrectionCode(err))

		atDependencyCap := task056DAGWithDependencyCount(t, fixture, 2048)
		_, err = arena.EvaluateCorrectionCutoff(input(atDependencyCap))
		require.NoError(t, err)
		overDependencyCap := task056DAGWithDependencyCount(t, fixture, 2049)
		_, err = arena.EvaluateCorrectionCutoff(input(overDependencyCap))
		require.Equal(t, arena.CorrectionRejectionMalformed, arena.CorrectionCode(err))

		atPayloadCap := task056DAGWithPayloadBytes(t, fixture, 512<<10)
		_, err = arena.EvaluateCorrectionCutoff(input(atPayloadCap))
		require.NoError(t, err)
		overPayloadCap := task056DAGWithPayloadBytes(t, fixture, (512<<10)+1)
		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)
		for range 4 {
			_, err = arena.EvaluateCorrectionCutoff(input(overPayloadCap))
			require.Equal(t, arena.CorrectionRejectionMalformed, arena.CorrectionCode(err))
		}
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		allocated := after.TotalAlloc - before.TotalAlloc
		t.Logf("four over-cap graph checks allocated %d bytes", allocated)
		require.Less(t, allocated, uint64(16<<20))
	})
}

func correctionArtifactKinds(revisions []domain.ArenaDerivedRevision) []domain.ArenaArtifactKind {
	kinds := make([]domain.ArenaArtifactKind, len(revisions))
	for index := range revisions {
		kinds[index] = revisions[index].Artifact().Kind
	}
	return kinds
}

func task056DAGWithProjectionCount(
	t *testing.T,
	fixture arena.RevisionDAGInput,
	count int,
) arena.RevisionDAG {
	t.Helper()
	projections := fixture.Graph.Projections()
	dependencies := fixture.Graph.Dependencies()
	var previous domain.ArenaProjectionRevision
	var bracketID domain.ArenaDerivedRevisionID
	for _, projection := range projections {
		//nolint:exhaustive // The fixture lookup needs only Champion and Bracket artifacts.
		switch projection.Revision().Artifact().Kind {
		case domain.ArenaArtifactKindChampion:
			previous = projection
		case domain.ArenaArtifactKindBracket:
			bracketID = projection.Revision().ID()
		default:
		}
	}
	require.False(t, bracketID.IsZero())
	for len(projections) < count {
		previousRevision := previous.Revision()
		previousID := previousRevision.ID()
		projection, err := domain.NewArenaProjectionRevision(
			domain.ArenaDerivedRevisionID(task055ID(60_000+len(projections))),
			previousRevision.TournamentID(), previousRevision.Artifact(),
			previousRevision.RevisionNo()+1, &previousID,
			previousRevision.CreatedAt().Add(time.Nanosecond), []byte{byte(len(projections))},
		)
		require.NoError(t, err)
		projections = append(projections, projection)
		dependencies = append(dependencies, domain.ArenaRevisionDependency{
			SourceRevisionID: previousID, DerivedRevisionID: projection.Revision().ID(),
		}, domain.ArenaRevisionDependency{
			SourceRevisionID: bracketID, DerivedRevisionID: projection.Revision().ID(),
		})
		previous = projection
	}
	graph, err := domain.NewArenaRevisionGraph(projections, dependencies)
	require.NoError(t, err)
	dag, err := arena.BuildRevisionDAG(arena.RevisionDAGInput{Graph: graph, Results: fixture.Results})
	require.NoError(t, err)
	return dag
}

func task056DAGWithDependencyCount(
	t *testing.T,
	fixture arena.RevisionDAGInput,
	count int,
) arena.RevisionDAG {
	t.Helper()
	dag := task056DAGWithProjectionCount(t, fixture, 512)
	snapshot := dag.Snapshot()
	dependencies := append([]domain.ArenaRevisionDependency(nil), snapshot.Dependencies...)
	seen := make(map[[2]domain.ArenaDerivedRevisionID]struct{}, count)
	for _, dependency := range dependencies {
		seen[[2]domain.ArenaDerivedRevisionID{
			dependency.SourceRevisionID, dependency.DerivedRevisionID,
		}] = struct{}{}
	}
	champion := make([]domain.ArenaDerivedRevisionID, 0, len(snapshot.Projections))
	for _, projection := range snapshot.Projections {
		if projection.Revision().Artifact().Kind == domain.ArenaArtifactKindChampion {
			champion = append(champion, projection.Revision().ID())
		}
	}
	for derived := 1; derived < len(champion) && len(dependencies) < count; derived++ {
		for source := 0; source < derived && len(dependencies) < count; source++ {
			key := [2]domain.ArenaDerivedRevisionID{champion[source], champion[derived]}
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			dependencies = append(dependencies, domain.ArenaRevisionDependency{
				SourceRevisionID: key[0], DerivedRevisionID: key[1],
			})
		}
	}
	require.Len(t, dependencies, count)
	graph, err := domain.NewArenaRevisionGraph(snapshot.Projections, dependencies)
	require.NoError(t, err)
	bounded, err := arena.BuildRevisionDAG(arena.RevisionDAGInput{Graph: graph, Results: fixture.Results})
	require.NoError(t, err)
	return bounded
}

func task056DAGWithPayloadBytes(
	t *testing.T,
	fixture arena.RevisionDAGInput,
	totalBytes int,
) arena.RevisionDAG {
	t.Helper()
	projections := fixture.Graph.Projections()
	replace := -1
	otherBytes := 0
	for index, projection := range projections {
		if projection.Revision().Artifact().Kind == domain.ArenaArtifactKindChampion {
			replace = index
			continue
		}
		otherBytes += len(projection.Payload())
	}
	require.NotEqual(t, -1, replace)
	payloadBytes := totalBytes - otherBytes
	require.Positive(t, payloadBytes)
	revision := projections[replace].Revision()
	projection, err := domain.NewArenaProjectionRevision(
		revision.ID(), revision.TournamentID(), revision.Artifact(), revision.RevisionNo(),
		revision.PreviousRevisionID(), revision.CreatedAt(), make([]byte, payloadBytes),
	)
	require.NoError(t, err)
	projections[replace] = projection
	graph, err := domain.NewArenaRevisionGraph(projections, fixture.Graph.Dependencies())
	require.NoError(t, err)
	dag, err := arena.BuildRevisionDAG(arena.RevisionDAGInput{Graph: graph, Results: fixture.Results})
	require.NoError(t, err)
	return dag
}
