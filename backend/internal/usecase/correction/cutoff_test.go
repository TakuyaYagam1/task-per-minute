package correction_test

import (
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	correctionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/correction"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection/revision"
)

func TestCorrectionCutoffTraversal(t *testing.T) {
	fixture := correctionDAGTestRevisionDAGFixture(t)
	dag, err := resultprojection.BuildRevisionDAG(fixture)
	require.NoError(t, err)
	before := dag.Snapshot()
	target := fixture.Results[0].ResultProjection.Revision().ID()

	t.Run("returns every descendant once in causal order", func(t *testing.T) {
		t.Parallel()

		first, err := correctionusecase.EvaluateCutoff(correctionusecase.CutoffInput{
			DAG: dag, TournamentID: fixture.Results[0].Result.Scope.TournamentID,
			TargetRevisionID: target, TournamentState: domain.TournamentStatePlayoffs,
		})
		require.NoError(t, err)
		second, err := correctionusecase.EvaluateCutoff(correctionusecase.CutoffInput{
			DAG: dag, TournamentID: fixture.Results[0].Result.Scope.TournamentID,
			TargetRevisionID: target, TournamentState: domain.TournamentStatePlayoffs,
		})
		require.NoError(t, err)

		wantKinds := []domain.ArtifactKind{
			domain.ArtifactKindSeriesScore,
			domain.ArtifactKindSeriesResult,
			domain.ArtifactKindStandings,
			domain.ArtifactKindGoldenGroup,
			domain.ArtifactKindTopFour,
			domain.ArtifactKindBracket,
			domain.ArtifactKindChampion,
		}
		require.Equal(t, wantKinds, correctionArtifactKinds(first.Descendants()))
		require.Equal(t, first.Descendants(), second.Descendants())

		returned := first.Descendants()
		returned[0] = domain.DerivedRevision{}
		require.Equal(t, wantKinds, correctionArtifactKinds(first.Descendants()))
		require.Equal(t, before, dag.Snapshot())
	})

	t.Run("rejects every irreversible event with one stable code", func(t *testing.T) {
		t.Parallel()

		seriesProjection := fixture.Results[2].ResultProjection.Revision()
		kinds := []correctionusecase.CutoffKind{
			correctionusecase.CutoffWaveStarted,
			correctionusecase.CutoffTaskDelivered,
			correctionusecase.CutoffNoShowRecorded,
			correctionusecase.CutoffForfeitRecorded,
			correctionusecase.CutoffGoldenAllocated,
		}
		for index, kind := range kinds {
			t.Run(string(kind), func(t *testing.T) {
				event := correctionusecase.CutoffEvent{
					ID: correctionDAGTestID(30_000 + index), Kind: kind,
					TournamentID:     fixture.Results[0].Result.Scope.TournamentID,
					SourceRevisionID: seriesProjection.ID(),
					OccurredAt:       seriesProjection.CreatedAt().Add(time.Second),
				}
				events := []correctionusecase.CutoffEvent{event}
				_, err := correctionusecase.EvaluateCutoff(correctionusecase.CutoffInput{
					DAG: dag, TournamentID: event.TournamentID, TargetRevisionID: target,
					TournamentState: domain.TournamentStatePlayoffs, Events: events,
				})
				require.ErrorIs(t, err, correctionusecase.ErrCutoff)
				require.Equal(t, correctionusecase.RejectionCutoff, correctionusecase.Code(err))
				require.Equal(t, kind, correctionusecase.CutoffKindOf(err))
				require.Equal(t, []correctionusecase.CutoffEvent{event}, events)
				require.Equal(t, before, dag.Snapshot())
			})
		}

		_, err := correctionusecase.EvaluateCutoff(correctionusecase.CutoffInput{
			DAG: dag, TournamentID: fixture.Results[0].Result.Scope.TournamentID,
			TargetRevisionID: target, TournamentState: domain.TournamentStateCompleted,
		})
		require.ErrorIs(t, err, correctionusecase.ErrCutoff)
		require.Equal(t, correctionusecase.RejectionCutoff, correctionusecase.Code(err))
		require.Empty(t, correctionusecase.CutoffKindOf(err))
	})

	t.Run("rejects malformed and cross-tournament evidence stably", func(t *testing.T) {
		t.Parallel()

		_, err := correctionusecase.EvaluateCutoff(correctionusecase.CutoffInput{
			DAG: dag, TournamentID: correctionDAGTestID(31_000), TargetRevisionID: target,
			TournamentState: domain.TournamentStatePlayoffs,
		})
		require.ErrorIs(t, err, correctionusecase.ErrInvalid)
		require.Equal(t, correctionusecase.RejectionCrossTournament, correctionusecase.Code(err))

		_, err = correctionusecase.EvaluateCutoff(correctionusecase.CutoffInput{
			DAG: dag, TournamentID: fixture.Results[0].Result.Scope.TournamentID,
			TargetRevisionID: target, TournamentState: domain.TournamentStatePlayoffs,
			Events: []correctionusecase.CutoffEvent{{
				ID: correctionDAGTestID(31_001), Kind: correctionusecase.CutoffTaskDelivered,
				TournamentID:     fixture.Results[0].Result.Scope.TournamentID,
				SourceRevisionID: target,
			}},
		})
		require.ErrorIs(t, err, correctionusecase.ErrInvalid)
		require.Equal(t, correctionusecase.RejectionMalformed, correctionusecase.Code(err))
		require.Equal(t, before, dag.Snapshot())
	})

	t.Run("scopes cutoff events to the full descendant closure", func(t *testing.T) {
		t.Parallel()

		secondGame := fixture.Results[1].Result.SourceProjection
		unrelated := correctionusecase.CutoffEvent{
			ID: correctionDAGTestID(32_001), Kind: correctionusecase.CutoffTaskDelivered,
			TournamentID:     fixture.Results[0].Result.Scope.TournamentID,
			SourceRevisionID: secondGame.ID(), OccurredAt: secondGame.CreatedAt(),
		}
		cutoff, err := correctionusecase.EvaluateCutoff(correctionusecase.CutoffInput{
			DAG: dag, TournamentID: unrelated.TournamentID, TargetRevisionID: target,
			TournamentState: domain.TournamentStatePlayoffs,
			Events:          []correctionusecase.CutoffEvent{unrelated},
		})
		require.NoError(t, err)
		require.Len(t, cutoff.Descendants(), 7)

		champion := correctionDAGTestCurrentProjectionID(t, fixture.Graph, domain.ArtifactKindChampion)
		var championRevision domain.DerivedRevision
		for _, projection := range fixture.Graph.Projections() {
			if projection.Revision().ID() == champion {
				championRevision = projection.Revision()
				break
			}
		}
		equalBoundary := correctionusecase.CutoffEvent{
			ID: correctionDAGTestID(32_002), Kind: correctionusecase.CutoffGoldenAllocated,
			TournamentID: unrelated.TournamentID, SourceRevisionID: champion,
			OccurredAt: championRevision.CreatedAt(),
		}
		_, err = correctionusecase.EvaluateCutoff(correctionusecase.CutoffInput{
			DAG: dag, TournamentID: unrelated.TournamentID, TargetRevisionID: target,
			TournamentState: domain.TournamentStatePlayoffs,
			Events:          []correctionusecase.CutoffEvent{equalBoundary},
		})
		require.ErrorIs(t, err, correctionusecase.ErrCutoff)
		require.Equal(t, correctionusecase.RejectionCutoff, correctionusecase.Code(err))
	})

	t.Run("rejects stale aliased and over-bound cutoff evidence", func(t *testing.T) {
		t.Parallel()

		tournamentID := fixture.Results[0].Result.Scope.TournamentID
		_, err := correctionusecase.EvaluateCutoff(correctionusecase.CutoffInput{
			DAG: dag, TournamentID: tournamentID,
			TargetRevisionID: domain.DerivedRevisionID(correctionDAGTestID(33_001)),
			TournamentState:  domain.TournamentStatePlayoffs,
		})
		require.ErrorIs(t, err, correctionusecase.ErrInvalid)
		require.Equal(t, correctionusecase.RejectionStale, correctionusecase.Code(err))

		secondGame := fixture.Results[1].Result.SourceProjection
		duplicate := correctionusecase.CutoffEvent{
			ID: correctionDAGTestID(33_002), Kind: correctionusecase.CutoffTaskDelivered,
			TournamentID: tournamentID, SourceRevisionID: secondGame.ID(),
			OccurredAt: secondGame.CreatedAt(),
		}
		_, err = correctionusecase.EvaluateCutoff(correctionusecase.CutoffInput{
			DAG: dag, TournamentID: tournamentID, TargetRevisionID: target,
			TournamentState: domain.TournamentStatePlayoffs,
			Events:          []correctionusecase.CutoffEvent{duplicate, duplicate},
		})
		require.Equal(t, correctionusecase.RejectionIdentityAlias, correctionusecase.Code(err))

		aliased := duplicate
		aliased.ID = target.UUID()
		_, err = correctionusecase.EvaluateCutoff(correctionusecase.CutoffInput{
			DAG: dag, TournamentID: tournamentID, TargetRevisionID: target,
			TournamentState: domain.TournamentStatePlayoffs,
			Events:          []correctionusecase.CutoffEvent{aliased},
		})
		require.Equal(t, correctionusecase.RejectionIdentityAlias, correctionusecase.Code(err))

		events := make([]correctionusecase.CutoffEvent, 4096)
		for index := range events {
			events[index] = correctionusecase.CutoffEvent{
				ID: correctionDAGTestID(50_000 + index), Kind: correctionusecase.CutoffWaveStarted,
				TournamentID: tournamentID, SourceRevisionID: secondGame.ID(),
				OccurredAt: secondGame.CreatedAt(),
			}
		}
		_, err = correctionusecase.EvaluateCutoff(correctionusecase.CutoffInput{
			DAG: dag, TournamentID: tournamentID, TargetRevisionID: target,
			TournamentState: domain.TournamentStatePlayoffs, Events: events,
		})
		require.NoError(t, err)
		overEvents := make([]correctionusecase.CutoffEvent, len(events)+1)
		copy(overEvents, events)
		_, err = correctionusecase.EvaluateCutoff(correctionusecase.CutoffInput{
			DAG: dag, TournamentID: tournamentID, TargetRevisionID: target,
			TournamentState: domain.TournamentStatePlayoffs, Events: overEvents,
		})
		require.Equal(t, correctionusecase.RejectionMalformed, correctionusecase.Code(err))
		require.Equal(t, before, dag.Snapshot())
	})

	t.Run("enforces correction graph caps at deterministic boundaries", func(t *testing.T) {
		tournamentID := fixture.Results[0].Result.Scope.TournamentID
		input := func(candidate resultprojection.RevisionDAG) correctionusecase.CutoffInput {
			return correctionusecase.CutoffInput{
				DAG: candidate, TournamentID: tournamentID, TargetRevisionID: target,
				TournamentState: domain.TournamentStatePlayoffs,
			}
		}

		atProjectionCap := task056DAGWithProjectionCount(t, fixture, 512)
		_, err := correctionusecase.EvaluateCutoff(input(atProjectionCap))
		require.NoError(t, err)
		overProjectionCap := task056DAGWithProjectionCount(t, fixture, 513)
		_, err = correctionusecase.EvaluateCutoff(input(overProjectionCap))
		require.Equal(t, correctionusecase.RejectionMalformed, correctionusecase.Code(err))

		atDependencyCap := task056DAGWithDependencyCount(t, fixture, 2048)
		_, err = correctionusecase.EvaluateCutoff(input(atDependencyCap))
		require.NoError(t, err)
		overDependencyCap := task056DAGWithDependencyCount(t, fixture, 2049)
		_, err = correctionusecase.EvaluateCutoff(input(overDependencyCap))
		require.Equal(t, correctionusecase.RejectionMalformed, correctionusecase.Code(err))

		atPayloadCap := task056DAGWithPayloadBytes(t, fixture, 512<<10)
		_, err = correctionusecase.EvaluateCutoff(input(atPayloadCap))
		require.NoError(t, err)
		overPayloadCap := task056DAGWithPayloadBytes(t, fixture, (512<<10)+1)
		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)
		for range 4 {
			_, err = correctionusecase.EvaluateCutoff(input(overPayloadCap))
			require.Equal(t, correctionusecase.RejectionMalformed, correctionusecase.Code(err))
		}
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		allocated := after.TotalAlloc - before.TotalAlloc
		t.Logf("four over-cap graph checks allocated %d bytes", allocated)
		require.Less(t, allocated, uint64(16<<20))
	})
}

func correctionArtifactKinds(revisions []domain.DerivedRevision) []domain.ArtifactKind {
	kinds := make([]domain.ArtifactKind, len(revisions))
	for index := range revisions {
		kinds[index] = revisions[index].Artifact().Kind
	}
	return kinds
}

func task056DAGWithProjectionCount(
	t *testing.T,
	fixture resultprojection.RevisionDAGInput,
	count int,
) resultprojection.RevisionDAG {
	t.Helper()
	projections := fixture.Graph.Projections()
	dependencies := fixture.Graph.Dependencies()
	var previous domain.ProjectionRevision
	var bracketID domain.DerivedRevisionID
	for _, projection := range projections {
		//nolint:exhaustive // The fixture lookup needs only Champion and Bracket artifacts.
		switch projection.Revision().Artifact().Kind {
		case domain.ArtifactKindChampion:
			previous = projection
		case domain.ArtifactKindBracket:
			bracketID = projection.Revision().ID()
		default:
		}
	}
	require.False(t, bracketID.IsZero())
	for len(projections) < count {
		previousRevision := previous.Revision()
		previousID := previousRevision.ID()
		projection, err := domain.NewProjectionRevision(
			domain.DerivedRevisionID(correctionDAGTestID(60_000+len(projections))),
			previousRevision.TournamentID(), previousRevision.Artifact(),
			previousRevision.RevisionNo()+1, &previousID,
			previousRevision.CreatedAt().Add(time.Nanosecond), []byte{byte(len(projections))},
		)
		require.NoError(t, err)
		projections = append(projections, projection)
		dependencies = append(dependencies, domain.RevisionDependency{
			SourceRevisionID: previousID, DerivedRevisionID: projection.Revision().ID(),
		}, domain.RevisionDependency{
			SourceRevisionID: bracketID, DerivedRevisionID: projection.Revision().ID(),
		})
		previous = projection
	}
	graph, err := domain.NewRevisionGraph(projections, dependencies)
	require.NoError(t, err)
	dag, err := resultprojection.BuildRevisionDAG(resultprojection.RevisionDAGInput{Graph: graph, Results: fixture.Results})
	require.NoError(t, err)
	return dag
}

func task056DAGWithDependencyCount(
	t *testing.T,
	fixture resultprojection.RevisionDAGInput,
	count int,
) resultprojection.RevisionDAG {
	t.Helper()
	dag := task056DAGWithProjectionCount(t, fixture, 512)
	snapshot := dag.Snapshot()
	dependencies := append([]domain.RevisionDependency(nil), snapshot.Dependencies...)
	seen := make(map[[2]domain.DerivedRevisionID]struct{}, count)
	for _, dependency := range dependencies {
		seen[[2]domain.DerivedRevisionID{
			dependency.SourceRevisionID, dependency.DerivedRevisionID,
		}] = struct{}{}
	}
	champion := make([]domain.DerivedRevisionID, 0, len(snapshot.Projections))
	for _, projection := range snapshot.Projections {
		if projection.Revision().Artifact().Kind == domain.ArtifactKindChampion {
			champion = append(champion, projection.Revision().ID())
		}
	}
	for derived := 1; derived < len(champion) && len(dependencies) < count; derived++ {
		for source := 0; source < derived && len(dependencies) < count; source++ {
			key := [2]domain.DerivedRevisionID{champion[source], champion[derived]}
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			dependencies = append(dependencies, domain.RevisionDependency{
				SourceRevisionID: key[0], DerivedRevisionID: key[1],
			})
		}
	}
	require.Len(t, dependencies, count)
	graph, err := domain.NewRevisionGraph(snapshot.Projections, dependencies)
	require.NoError(t, err)
	bounded, err := resultprojection.BuildRevisionDAG(resultprojection.RevisionDAGInput{Graph: graph, Results: fixture.Results})
	require.NoError(t, err)
	return bounded
}

func task056DAGWithPayloadBytes(
	t *testing.T,
	fixture resultprojection.RevisionDAGInput,
	totalBytes int,
) resultprojection.RevisionDAG {
	t.Helper()
	projections := fixture.Graph.Projections()
	replace := -1
	otherBytes := 0
	for index, projection := range projections {
		if projection.Revision().Artifact().Kind == domain.ArtifactKindChampion {
			replace = index
			continue
		}
		otherBytes += len(projection.Payload())
	}
	require.NotEqual(t, -1, replace)
	payloadBytes := totalBytes - otherBytes
	require.Positive(t, payloadBytes)
	revision := projections[replace].Revision()
	projection, err := domain.NewProjectionRevision(
		revision.ID(), revision.TournamentID(), revision.Artifact(), revision.RevisionNo(),
		revision.PreviousRevisionID(), revision.CreatedAt(), make([]byte, payloadBytes),
	)
	require.NoError(t, err)
	projections[replace] = projection
	graph, err := domain.NewRevisionGraph(projections, fixture.Graph.Dependencies())
	require.NoError(t, err)
	dag, err := resultprojection.BuildRevisionDAG(resultprojection.RevisionDAGInput{Graph: graph, Results: fixture.Results})
	require.NoError(t, err)
	return dag
}
