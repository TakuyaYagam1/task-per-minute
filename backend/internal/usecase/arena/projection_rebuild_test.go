package arena_test

import (
	"bytes"
	"crypto/sha256"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestPureProjectionRebuild(t *testing.T) {
	t.Parallel()

	t.Run("repeats byte-equivalently without changing graph or decisions", func(t *testing.T) {
		t.Parallel()

		dag, err := arena.BuildRevisionDAG(task055RevisionDAGFixture(t))
		require.NoError(t, err)
		before := dag.Snapshot()
		decisions := []arena.RecordedProjectionDecision{
			{
				ID: task055ID(2002), Sequence: 2,
				ProjectionRevisionID: task055CurrentProjectionID(t, task055RevisionDAGFixture(t).Graph, domain.ArenaArtifactKindChampion),
				RecordedAt:           time.Date(2026, time.September, 1, 11, 0, 10, 0, time.UTC),
				Payload:              []byte(`{"winner":"recorded"}`),
			},
			{
				ID: task055ID(2001), Sequence: 1,
				ProjectionRevisionID: task055CurrentProjectionID(t, task055RevisionDAGFixture(t).Graph, domain.ArenaArtifactKindTopFour),
				RecordedAt:           time.Date(2026, time.September, 1, 11, 0, 8, 0, time.UTC),
				Payload:              []byte(`{"order":[1,2,3,4]}`),
			},
		}
		for index := range decisions {
			decisions[index].PayloadDigest = sha256.Sum256(decisions[index].Payload)
		}
		decisionCount := len(decisions)

		first, err := arena.RebuildOfficialProjections(arena.ProjectionRebuildInput{
			DAG: dag, Decisions: decisions,
		})
		require.NoError(t, err)
		second, err := arena.RebuildOfficialProjections(arena.ProjectionRebuildInput{
			DAG: dag, Decisions: decisions,
		})
		require.NoError(t, err)

		require.Equal(t, first.Bytes(), second.Bytes())
		require.Equal(t, decisionCount, first.DecisionCount())
		require.Equal(t, decisionCount, second.DecisionCount())
		require.Equal(t, before, dag.Snapshot())
		require.Len(t, decisions, decisionCount)
		require.False(t, bytes.Contains(first.Bytes(), []byte("game-1100")), "graph payload leaked")

		mutated := first.Bytes()
		mutated[0] ^= 0xff
		decisions[0].Payload[0] ^= 0xff
		require.Equal(t, second.Bytes(), first.Bytes())
		require.True(t, bytes.Equal([]byte(`{"winner":"recorded"}`), first.Decisions()[1].Payload))
		returnedDecisions := first.Decisions()
		returnedDecisions[0].Payload[0] ^= 0xff
		require.True(t, bytes.Equal([]byte(`{"order":[1,2,3,4]}`), first.Decisions()[0].Payload))
	})

	t.Run("is safe for concurrent rebuilds of one immutable input", func(t *testing.T) {
		t.Parallel()

		dag, err := arena.BuildRevisionDAG(task055RevisionDAGFixture(t))
		require.NoError(t, err)
		decision := arena.RecordedProjectionDecision{
			ID: task055ID(2100), Sequence: 1,
			ProjectionRevisionID: task055CurrentProjectionID(t, task055RevisionDAGFixture(t).Graph, domain.ArenaArtifactKindChampion),
			RecordedAt:           time.Date(2026, time.September, 1, 11, 0, 10, 0, time.UTC),
			Payload:              []byte(`{"champion":"fixed"}`),
		}
		decision.PayloadDigest = sha256.Sum256(decision.Payload)
		input := arena.ProjectionRebuildInput{DAG: dag, Decisions: []arena.RecordedProjectionDecision{decision}}
		baseline, err := arena.RebuildOfficialProjections(input)
		require.NoError(t, err)

		const workers = 16
		var wait sync.WaitGroup
		errors := make(chan error, workers)
		for range workers {
			wait.Add(1)
			go func() {
				defer wait.Done()
				rebuilt, rebuildErr := arena.RebuildOfficialProjections(input)
				if rebuildErr != nil {
					errors <- rebuildErr
					return
				}
				if !bytes.Equal(rebuilt.Bytes(), baseline.Bytes()) {
					errors <- arena.ErrProjectionRebuildChanged
				}
			}()
		}
		wait.Wait()
		close(errors)
		for rebuildErr := range errors {
			require.NoError(t, rebuildErr)
		}
	})

	t.Run("rejects missing duplicated or oversized recorded decisions", func(t *testing.T) {
		t.Parallel()

		dag, err := arena.BuildRevisionDAG(task055RevisionDAGFixture(t))
		require.NoError(t, err)
		valid := arena.RecordedProjectionDecision{
			ID: task055ID(2200), Sequence: 1,
			ProjectionRevisionID: task055CurrentProjectionID(t, task055RevisionDAGFixture(t).Graph, domain.ArenaArtifactKindChampion),
			RecordedAt:           time.Date(2026, time.September, 1, 11, 0, 10, 0, time.UTC),
			Payload:              []byte("recorded"),
		}
		valid.PayloadDigest = sha256.Sum256(valid.Payload)
		_, err = arena.RebuildOfficialProjections(arena.ProjectionRebuildInput{DAG: dag})
		require.ErrorIs(t, err, arena.ErrInvalidProjectionRebuild)

		duplicate := valid
		duplicate.Sequence = 2
		_, err = arena.RebuildOfficialProjections(arena.ProjectionRebuildInput{
			DAG: dag, Decisions: []arena.RecordedProjectionDecision{valid, duplicate},
		})
		require.ErrorIs(t, err, arena.ErrInvalidProjectionRebuild)

		unknown := valid
		unknown.ProjectionRevisionID = domain.ArenaDerivedRevisionID(task055ID(2299))
		_, err = arena.RebuildOfficialProjections(arena.ProjectionRebuildInput{
			DAG: dag, Decisions: []arena.RecordedProjectionDecision{unknown},
		})
		require.ErrorIs(t, err, arena.ErrInvalidProjectionRebuild)

		oversized := valid
		oversized.Payload = make([]byte, arena.MaxRecordedProjectionDecisionBytes+1)
		oversized.PayloadDigest = sha256.Sum256(oversized.Payload)
		_, err = arena.RebuildOfficialProjections(arena.ProjectionRebuildInput{
			DAG: dag, Decisions: []arena.RecordedProjectionDecision{oversized},
		})
		require.ErrorIs(t, err, arena.ErrInvalidProjectionRebuild)

		digestMismatch := valid
		digestMismatch.Payload = []byte("changed-after-recording")
		_, err = arena.RebuildOfficialProjections(arena.ProjectionRebuildInput{
			DAG: dag, Decisions: []arena.RecordedProjectionDecision{digestMismatch},
		})
		require.ErrorIs(t, err, arena.ErrInvalidProjectionRebuild)

		gapFirst := valid
		gapFirst.ProjectionRevisionID = task055CurrentProjectionID(
			t, task055RevisionDAGFixture(t).Graph, domain.ArenaArtifactKindTopFour,
		)
		gapFirst.RecordedAt = time.Date(2026, time.September, 1, 11, 0, 8, 0, time.UTC)
		gapSecond := valid
		gapSecond.ID = task055ID(2201)
		gapSecond.Sequence = 3
		_, err = arena.RebuildOfficialProjections(arena.ProjectionRebuildInput{
			DAG: dag, Decisions: []arena.RecordedProjectionDecision{gapSecond, gapFirst},
		})
		require.ErrorIs(t, err, arena.ErrInvalidProjectionRebuild)

		duplicateTarget := gapSecond
		duplicateTarget.Sequence = 2
		duplicateTarget.ProjectionRevisionID = gapFirst.ProjectionRevisionID
		_, err = arena.RebuildOfficialProjections(arena.ProjectionRebuildInput{
			DAG: dag, Decisions: []arena.RecordedProjectionDecision{gapFirst, duplicateTarget},
		})
		require.ErrorIs(t, err, arena.ErrInvalidProjectionRebuild)

		postdated := valid
		postdated.RecordedAt = time.Date(2026, time.September, 1, 11, 0, 12, 0, time.UTC)
		_, err = arena.RebuildOfficialProjections(arena.ProjectionRebuildInput{
			DAG: dag, Decisions: []arena.RecordedProjectionDecision{postdated},
		})
		require.ErrorIs(t, err, arena.ErrInvalidProjectionRebuild)

		predated := valid
		predated.RecordedAt = time.Date(2026, time.September, 1, 11, 0, 9, 0, time.UTC)
		_, err = arena.RebuildOfficialProjections(arena.ProjectionRebuildInput{
			DAG: dag, Decisions: []arena.RecordedProjectionDecision{predated},
		})
		require.ErrorIs(t, err, arena.ErrInvalidProjectionRebuild)

		aliased := valid
		aliased.ID = valid.ProjectionRevisionID.UUID()
		_, err = arena.RebuildOfficialProjections(arena.ProjectionRebuildInput{
			DAG: dag, Decisions: []arena.RecordedProjectionDecision{aliased},
		})
		require.ErrorIs(t, err, arena.ErrInvalidProjectionRebuild)
	})

	t.Run("accepts exact payload bounds and rejects a stale current target", func(t *testing.T) {
		t.Parallel()

		input := task055RevisionDAGFixture(t)
		dag, err := arena.BuildRevisionDAG(input)
		require.NoError(t, err)
		championID := task055CurrentProjectionID(t, input.Graph, domain.ArenaArtifactKindChampion)
		for index, payload := range [][]byte{{0x01}, bytes.Repeat([]byte{0x5a}, arena.MaxRecordedProjectionDecisionBytes)} {
			decision := arena.RecordedProjectionDecision{
				ID: task055ID(2250 + index), Sequence: 1,
				ProjectionRevisionID: championID,
				RecordedAt:           time.Date(2026, time.September, 1, 11, 0, 10, 0, time.UTC),
				Payload:              payload,
			}
			decision.PayloadDigest = sha256.Sum256(decision.Payload)
			_, err = arena.RebuildOfficialProjections(arena.ProjectionRebuildInput{
				DAG: dag, Decisions: []arena.RecordedProjectionDecision{decision},
			})
			require.NoError(t, err)
		}

		champion, exists := input.Graph.CurrentRevision(domain.ArenaArtifactRef{
			Kind: domain.ArenaArtifactKindChampion, EntityID: input.Results[0].Result.Scope.TournamentID,
		})
		require.True(t, exists)
		successor := task055SuccessorProjection(
			t, champion, 2260, champion.Revision().CreatedAt().Add(time.Second), "new-current-champion",
		)
		bracketID := task055CurrentProjectionID(t, input.Graph, domain.ArenaArtifactKindBracket)
		dependencies := append([]domain.ArenaRevisionDependency(nil), input.Graph.Dependencies()...)
		dependencies = append(dependencies,
			domain.ArenaRevisionDependency{
				SourceRevisionID: championID, DerivedRevisionID: successor.Revision().ID(),
			},
			domain.ArenaRevisionDependency{
				SourceRevisionID: bracketID, DerivedRevisionID: successor.Revision().ID(),
			},
		)
		graph, err := domain.NewArenaRevisionGraph(
			append(input.Graph.Projections(), successor), dependencies,
		)
		require.NoError(t, err)
		input.Graph = graph
		dag, err = arena.BuildRevisionDAG(input)
		require.NoError(t, err)
		stale := arena.RecordedProjectionDecision{
			ID: task055ID(2261), Sequence: 1, ProjectionRevisionID: championID,
			RecordedAt: time.Date(2026, time.September, 1, 11, 0, 10, 0, time.UTC),
			Payload:    []byte("stale"),
		}
		stale.PayloadDigest = sha256.Sum256(stale.Payload)
		_, err = arena.RebuildOfficialProjections(arena.ProjectionRebuildInput{
			DAG: dag, Decisions: []arena.RecordedProjectionDecision{stale},
		})
		require.ErrorIs(t, err, arena.ErrInvalidProjectionRebuild)
	})

	t.Run("canonical bytes bind graph metadata and verified payload digests", func(t *testing.T) {
		t.Parallel()

		input := task055RevisionDAGFixture(t)
		firstDAG, err := arena.BuildRevisionDAG(input)
		require.NoError(t, err)
		decision := arena.RecordedProjectionDecision{
			ID: task055ID(2300), Sequence: 1,
			ProjectionRevisionID: task055CurrentProjectionID(t, input.Graph, domain.ArenaArtifactKindChampion),
			RecordedAt:           time.Date(2026, time.September, 1, 11, 0, 10, 0, time.UTC),
			Payload:              []byte{0xff},
		}
		decision.PayloadDigest = sha256.Sum256(decision.Payload)
		first, err := arena.RebuildOfficialProjections(arena.ProjectionRebuildInput{
			DAG: firstDAG, Decisions: []arena.RecordedProjectionDecision{decision},
		})
		require.NoError(t, err)

		projections := input.Graph.Projections()
		for index := range projections {
			revision := projections[index].Revision()
			if revision.ID() != decision.ProjectionRevisionID {
				continue
			}
			changed, changeErr := domain.NewArenaProjectionRevision(
				revision.ID(), revision.TournamentID(), revision.Artifact(), revision.RevisionNo(),
				revision.PreviousRevisionID(), revision.CreatedAt(), []byte("changed-champion-payload"),
			)
			require.NoError(t, changeErr)
			projections[index] = changed
		}
		graph, err := domain.NewArenaRevisionGraph(projections, input.Graph.Dependencies())
		require.NoError(t, err)
		input.Graph = graph
		secondDAG, err := arena.BuildRevisionDAG(input)
		require.NoError(t, err)
		second, err := arena.RebuildOfficialProjections(arena.ProjectionRebuildInput{
			DAG: secondDAG, Decisions: []arena.RecordedProjectionDecision{decision},
		})
		require.NoError(t, err)
		require.NotEqual(t, first.Bytes(), second.Bytes())
	})

	t.Run("rejects a non-adjacent descendant before its source decision", func(t *testing.T) {
		t.Parallel()

		input, commonTime, unrelatedGameID := task055TwoSeriesDecisionFixture(t)
		dag, err := arena.BuildRevisionDAG(input)
		require.NoError(t, err)
		decisions := []arena.RecordedProjectionDecision{
			{
				ID: task055ID(2401), Sequence: 1,
				ProjectionRevisionID: input.Results[2].ScoreProjection.Revision().ID(),
				RecordedAt:           commonTime, Payload: []byte("descendant-first"),
			},
			{
				ID: task055ID(2402), Sequence: 2,
				ProjectionRevisionID: unrelatedGameID,
				RecordedAt:           commonTime, Payload: []byte("unrelated-middle"),
			},
			{
				ID: task055ID(2403), Sequence: 3,
				ProjectionRevisionID: input.Results[0].ResultProjection.Revision().ID(),
				RecordedAt:           commonTime, Payload: []byte("ancestor-last"),
			},
		}
		for index := range decisions {
			decisions[index].PayloadDigest = sha256.Sum256(decisions[index].Payload)
		}

		_, err = arena.RebuildOfficialProjections(arena.ProjectionRebuildInput{
			DAG: dag, Decisions: decisions,
		})
		require.ErrorIs(t, err, arena.ErrInvalidProjectionRebuild)
	})
}

func task055TwoSeriesDecisionFixture(
	t *testing.T,
) (arena.RevisionDAGInput, time.Time, domain.ArenaDerivedRevisionID) {
	t.Helper()
	input := task055RevisionDAGFixture(t)
	baseTime := time.Date(2026, time.September, 1, 11, 0, 0, 0, time.UTC)
	commonTime := baseTime.Add(-57 * time.Second)

	projections := input.Graph.Projections()
	firstGameID := input.Results[0].ResultProjection.Revision().ID()
	for index := range projections {
		revision := projections[index].Revision()
		if revision.ID() != firstGameID {
			continue
		}
		changed, err := domain.NewArenaProjectionRevision(
			revision.ID(), revision.TournamentID(), revision.Artifact(), revision.RevisionNo(),
			revision.PreviousRevisionID(), commonTime, projections[index].Payload(),
		)
		require.NoError(t, err)
		projections[index] = changed
		input.Results[0].ResultProjection = changed
		input.Results[0].Result.SourceProjection = changed.Revision()
	}

	tournamentID := input.Results[0].Result.Scope.TournamentID
	secondSeriesID := task055ID(2501)
	secondGameEntityID := task055ID(2502)
	firstParticipantID := task055ID(2503)
	secondParticipantID := task055ID(2504)
	secondGame := task055GameResultHead(
		t, tournamentID, secondSeriesID, secondGameEntityID, firstParticipantID,
		domain.ArenaGameStateCompleted, domain.ArenaGameResultReasonSolved,
		task055UUIDPointer(firstParticipantID), baseTime.Add(time.Second), 2510,
	)
	secondGameProjection, err := domain.NewArenaProjectionRevision(
		secondGame.SourceProjection.ID(), tournamentID,
		domain.ArenaArtifactRef{Kind: domain.ArenaArtifactKindGameResult, EntityID: secondGameEntityID},
		1, nil, commonTime, []byte("second-series-game"),
	)
	require.NoError(t, err)
	secondGame.SourceProjection = secondGameProjection.Revision()
	secondScore := task055ScoreHead(
		t, tournamentID, secondSeriesID, firstParticipantID, secondParticipantID,
		domain.ArenaSeriesFormatBO1, domain.ArenaSeriesScore{FirstParticipantWins: 1},
		1, baseTime.Add(4*time.Second), 2530,
	)
	secondScore.Attempts[0].CurrentGameResultRevisionID = secondGame.ID
	secondScore.Attempts[0].GameID = secondGameEntityID
	secondScore.CommandAttempt = func() *arena.SeriesScoreAttemptReference {
		attempt := secondScore.Attempts[0]
		return &attempt
	}()
	secondScore.CommandID = secondGame.CommandID
	require.NoError(t, secondScore.Validate())
	secondScoreProjection := task055ExactProjection(t, secondScore.SourceProjection, "score-2530")
	secondSeries := task055SeriesResultHead(
		t, tournamentID, secondSeriesID, secondScore.ID, domain.ArenaSeriesStateCompleted,
		arena.ArenaSeriesResultReasonScoreComplete, task055UUIDPointer(firstParticipantID),
		baseTime.Add(6*time.Second), 2550,
	)
	secondSeriesProjection := task055ExactProjection(t, secondSeries.SourceProjection, "series-2550")
	projections = append(
		projections, secondGameProjection, secondScoreProjection, secondSeriesProjection,
	)
	dependencies := append([]domain.ArenaRevisionDependency(nil), input.Graph.Dependencies()...)
	standingsID := task055CurrentProjectionID(t, input.Graph, domain.ArenaArtifactKindStandings)
	dependencies = append(dependencies,
		domain.ArenaRevisionDependency{
			SourceRevisionID:  secondGameProjection.Revision().ID(),
			DerivedRevisionID: secondScoreProjection.Revision().ID(),
		},
		domain.ArenaRevisionDependency{
			SourceRevisionID:  secondScoreProjection.Revision().ID(),
			DerivedRevisionID: secondSeriesProjection.Revision().ID(),
		},
		domain.ArenaRevisionDependency{
			SourceRevisionID:  secondSeriesProjection.Revision().ID(),
			DerivedRevisionID: standingsID,
		},
	)
	graph, err := domain.NewArenaRevisionGraph(projections, dependencies)
	require.NoError(t, err)
	input.Graph = graph
	input.Results = append(input.Results,
		arena.OfficialResultProjectionInput{
			Result: secondGame, ResultProjection: secondGameProjection,
		},
		arena.OfficialResultProjectionInput{
			Result: secondSeries, ResultProjection: secondSeriesProjection,
			Score:           task055ScoreHeadPointer(secondScore),
			ScoreProjection: task055ProjectionPointer(secondScoreProjection),
		},
	)
	return input, commonTime, secondGameProjection.Revision().ID()
}
