package resultprojection_test

import (
	"bytes"
	"crypto/sha256"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/result"
	projection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
)

func TestPureProjectionRebuild(t *testing.T) {
	t.Parallel()

	t.Run("repeats byte-equivalently without changing graph or decisions", func(t *testing.T) {
		t.Parallel()

		dag, err := projection.BuildRevisionDAG(task055RevisionDAGFixture(t))
		require.NoError(t, err)
		before := dag.Snapshot()
		decisions := []projection.RecordedProjectionDecision{
			{
				ID: task055ID(2002), Sequence: 2,
				ProjectionRevisionID: task055CurrentProjectionID(t, task055RevisionDAGFixture(t).Graph, domain.ArtifactKindChampion),
				RecordedAt:           time.Date(2026, time.September, 1, 11, 0, 10, 0, time.UTC),
				Payload:              []byte(`{"winner":"recorded"}`),
			},
			{
				ID: task055ID(2001), Sequence: 1,
				ProjectionRevisionID: task055CurrentProjectionID(t, task055RevisionDAGFixture(t).Graph, domain.ArtifactKindTopFour),
				RecordedAt:           time.Date(2026, time.September, 1, 11, 0, 8, 0, time.UTC),
				Payload:              []byte(`{"order":[1,2,3,4]}`),
			},
		}
		for index := range decisions {
			decisions[index].PayloadDigest = sha256.Sum256(decisions[index].Payload)
		}
		decisionCount := len(decisions)

		first, err := projection.RebuildOfficialProjections(projection.ProjectionRebuildInput{
			DAG: dag, Decisions: decisions,
		})
		require.NoError(t, err)
		second, err := projection.RebuildOfficialProjections(projection.ProjectionRebuildInput{
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

		dag, err := projection.BuildRevisionDAG(task055RevisionDAGFixture(t))
		require.NoError(t, err)
		decision := projection.RecordedProjectionDecision{
			ID: task055ID(2100), Sequence: 1,
			ProjectionRevisionID: task055CurrentProjectionID(t, task055RevisionDAGFixture(t).Graph, domain.ArtifactKindChampion),
			RecordedAt:           time.Date(2026, time.September, 1, 11, 0, 10, 0, time.UTC),
			Payload:              []byte(`{"champion":"fixed"}`),
		}
		decision.PayloadDigest = sha256.Sum256(decision.Payload)
		input := projection.ProjectionRebuildInput{DAG: dag, Decisions: []projection.RecordedProjectionDecision{decision}}
		baseline, err := projection.RebuildOfficialProjections(input)
		require.NoError(t, err)

		const workers = 16
		var wait sync.WaitGroup
		errors := make(chan error, workers)
		for range workers {
			wait.Add(1)
			go func() {
				defer wait.Done()
				rebuilt, rebuildErr := projection.RebuildOfficialProjections(input)
				if rebuildErr != nil {
					errors <- rebuildErr
					return
				}
				if !bytes.Equal(rebuilt.Bytes(), baseline.Bytes()) {
					errors <- projection.ErrProjectionRebuildChanged
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

		dag, err := projection.BuildRevisionDAG(task055RevisionDAGFixture(t))
		require.NoError(t, err)
		valid := projection.RecordedProjectionDecision{
			ID: task055ID(2200), Sequence: 1,
			ProjectionRevisionID: task055CurrentProjectionID(t, task055RevisionDAGFixture(t).Graph, domain.ArtifactKindChampion),
			RecordedAt:           time.Date(2026, time.September, 1, 11, 0, 10, 0, time.UTC),
			Payload:              []byte("recorded"),
		}
		valid.PayloadDigest = sha256.Sum256(valid.Payload)
		_, err = projection.RebuildOfficialProjections(projection.ProjectionRebuildInput{DAG: dag})
		require.ErrorIs(t, err, projection.ErrInvalidProjectionRebuild)

		duplicate := valid
		duplicate.Sequence = 2
		_, err = projection.RebuildOfficialProjections(projection.ProjectionRebuildInput{
			DAG: dag, Decisions: []projection.RecordedProjectionDecision{valid, duplicate},
		})
		require.ErrorIs(t, err, projection.ErrInvalidProjectionRebuild)

		unknown := valid
		unknown.ProjectionRevisionID = domain.DerivedRevisionID(task055ID(2299))
		_, err = projection.RebuildOfficialProjections(projection.ProjectionRebuildInput{
			DAG: dag, Decisions: []projection.RecordedProjectionDecision{unknown},
		})
		require.ErrorIs(t, err, projection.ErrInvalidProjectionRebuild)

		oversized := valid
		oversized.Payload = make([]byte, projection.MaxRecordedProjectionDecisionBytes+1)
		oversized.PayloadDigest = sha256.Sum256(oversized.Payload)
		_, err = projection.RebuildOfficialProjections(projection.ProjectionRebuildInput{
			DAG: dag, Decisions: []projection.RecordedProjectionDecision{oversized},
		})
		require.ErrorIs(t, err, projection.ErrInvalidProjectionRebuild)

		digestMismatch := valid
		digestMismatch.Payload = []byte("changed-after-recording")
		_, err = projection.RebuildOfficialProjections(projection.ProjectionRebuildInput{
			DAG: dag, Decisions: []projection.RecordedProjectionDecision{digestMismatch},
		})
		require.ErrorIs(t, err, projection.ErrInvalidProjectionRebuild)

		gapFirst := valid
		gapFirst.ProjectionRevisionID = task055CurrentProjectionID(
			t, task055RevisionDAGFixture(t).Graph, domain.ArtifactKindTopFour,
		)
		gapFirst.RecordedAt = time.Date(2026, time.September, 1, 11, 0, 8, 0, time.UTC)
		gapSecond := valid
		gapSecond.ID = task055ID(2201)
		gapSecond.Sequence = 3
		_, err = projection.RebuildOfficialProjections(projection.ProjectionRebuildInput{
			DAG: dag, Decisions: []projection.RecordedProjectionDecision{gapSecond, gapFirst},
		})
		require.ErrorIs(t, err, projection.ErrInvalidProjectionRebuild)

		duplicateTarget := gapSecond
		duplicateTarget.Sequence = 2
		duplicateTarget.ProjectionRevisionID = gapFirst.ProjectionRevisionID
		_, err = projection.RebuildOfficialProjections(projection.ProjectionRebuildInput{
			DAG: dag, Decisions: []projection.RecordedProjectionDecision{gapFirst, duplicateTarget},
		})
		require.ErrorIs(t, err, projection.ErrInvalidProjectionRebuild)

		postdated := valid
		postdated.RecordedAt = time.Date(2026, time.September, 1, 11, 0, 12, 0, time.UTC)
		_, err = projection.RebuildOfficialProjections(projection.ProjectionRebuildInput{
			DAG: dag, Decisions: []projection.RecordedProjectionDecision{postdated},
		})
		require.ErrorIs(t, err, projection.ErrInvalidProjectionRebuild)

		predated := valid
		predated.RecordedAt = time.Date(2026, time.September, 1, 11, 0, 9, 0, time.UTC)
		_, err = projection.RebuildOfficialProjections(projection.ProjectionRebuildInput{
			DAG: dag, Decisions: []projection.RecordedProjectionDecision{predated},
		})
		require.ErrorIs(t, err, projection.ErrInvalidProjectionRebuild)

		aliased := valid
		aliased.ID = valid.ProjectionRevisionID.UUID()
		_, err = projection.RebuildOfficialProjections(projection.ProjectionRebuildInput{
			DAG: dag, Decisions: []projection.RecordedProjectionDecision{aliased},
		})
		require.ErrorIs(t, err, projection.ErrInvalidProjectionRebuild)
	})

	t.Run("accepts exact payload bounds and rejects a stale current target", func(t *testing.T) {
		t.Parallel()

		input := task055RevisionDAGFixture(t)
		dag, err := projection.BuildRevisionDAG(input)
		require.NoError(t, err)
		championID := task055CurrentProjectionID(t, input.Graph, domain.ArtifactKindChampion)
		for index, payload := range [][]byte{{0x01}, bytes.Repeat([]byte{0x5a}, projection.MaxRecordedProjectionDecisionBytes)} {
			decision := projection.RecordedProjectionDecision{
				ID: task055ID(2250 + index), Sequence: 1,
				ProjectionRevisionID: championID,
				RecordedAt:           time.Date(2026, time.September, 1, 11, 0, 10, 0, time.UTC),
				Payload:              payload,
			}
			decision.PayloadDigest = sha256.Sum256(decision.Payload)
			_, err = projection.RebuildOfficialProjections(projection.ProjectionRebuildInput{
				DAG: dag, Decisions: []projection.RecordedProjectionDecision{decision},
			})
			require.NoError(t, err)
		}

		champion, exists := input.Graph.CurrentRevision(domain.ArtifactRef{
			Kind: domain.ArtifactKindChampion, EntityID: input.Results[0].Result.Scope.TournamentID,
		})
		require.True(t, exists)
		successor := task055SuccessorProjection(
			t, champion, 2260, champion.Revision().CreatedAt().Add(time.Second), "new-current-champion",
		)
		bracketID := task055CurrentProjectionID(t, input.Graph, domain.ArtifactKindBracket)
		dependencies := append([]domain.RevisionDependency(nil), input.Graph.Dependencies()...)
		dependencies = append(dependencies,
			domain.RevisionDependency{
				SourceRevisionID: championID, DerivedRevisionID: successor.Revision().ID(),
			},
			domain.RevisionDependency{
				SourceRevisionID: bracketID, DerivedRevisionID: successor.Revision().ID(),
			},
		)
		graph, err := domain.NewRevisionGraph(
			append(input.Graph.Projections(), successor), dependencies,
		)
		require.NoError(t, err)
		input.Graph = graph
		dag, err = projection.BuildRevisionDAG(input)
		require.NoError(t, err)
		stale := projection.RecordedProjectionDecision{
			ID: task055ID(2261), Sequence: 1, ProjectionRevisionID: championID,
			RecordedAt: time.Date(2026, time.September, 1, 11, 0, 10, 0, time.UTC),
			Payload:    []byte("stale"),
		}
		stale.PayloadDigest = sha256.Sum256(stale.Payload)
		_, err = projection.RebuildOfficialProjections(projection.ProjectionRebuildInput{
			DAG: dag, Decisions: []projection.RecordedProjectionDecision{stale},
		})
		require.ErrorIs(t, err, projection.ErrInvalidProjectionRebuild)
	})

	t.Run("canonical bytes bind graph metadata and verified payload digests", func(t *testing.T) {
		t.Parallel()

		input := task055RevisionDAGFixture(t)
		firstDAG, err := projection.BuildRevisionDAG(input)
		require.NoError(t, err)
		decision := projection.RecordedProjectionDecision{
			ID: task055ID(2300), Sequence: 1,
			ProjectionRevisionID: task055CurrentProjectionID(t, input.Graph, domain.ArtifactKindChampion),
			RecordedAt:           time.Date(2026, time.September, 1, 11, 0, 10, 0, time.UTC),
			Payload:              []byte{0xff},
		}
		decision.PayloadDigest = sha256.Sum256(decision.Payload)
		first, err := projection.RebuildOfficialProjections(projection.ProjectionRebuildInput{
			DAG: firstDAG, Decisions: []projection.RecordedProjectionDecision{decision},
		})
		require.NoError(t, err)

		projections := input.Graph.Projections()
		for index := range projections {
			revision := projections[index].Revision()
			if revision.ID() != decision.ProjectionRevisionID {
				continue
			}
			changed, changeErr := domain.NewProjectionRevision(
				revision.ID(), revision.TournamentID(), revision.Artifact(), revision.RevisionNo(),
				revision.PreviousRevisionID(), revision.CreatedAt(), []byte("changed-champion-payload"),
			)
			require.NoError(t, changeErr)
			projections[index] = changed
		}
		graph, err := domain.NewRevisionGraph(projections, input.Graph.Dependencies())
		require.NoError(t, err)
		input.Graph = graph
		secondDAG, err := projection.BuildRevisionDAG(input)
		require.NoError(t, err)
		second, err := projection.RebuildOfficialProjections(projection.ProjectionRebuildInput{
			DAG: secondDAG, Decisions: []projection.RecordedProjectionDecision{decision},
		})
		require.NoError(t, err)
		require.NotEqual(t, first.Bytes(), second.Bytes())
	})

	t.Run("rejects a non-adjacent descendant before its source decision", func(t *testing.T) {
		t.Parallel()

		input, commonTime, unrelatedGameID := task055TwoSeriesDecisionFixture(t)
		dag, err := projection.BuildRevisionDAG(input)
		require.NoError(t, err)
		decisions := []projection.RecordedProjectionDecision{
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

		_, err = projection.RebuildOfficialProjections(projection.ProjectionRebuildInput{
			DAG: dag, Decisions: decisions,
		})
		require.ErrorIs(t, err, projection.ErrInvalidProjectionRebuild)
	})
}

func task055TwoSeriesDecisionFixture(
	t *testing.T,
) (projection.RevisionDAGInput, time.Time, domain.DerivedRevisionID) {
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
		changed, err := domain.NewProjectionRevision(
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
		domain.GameStateCompleted, domain.GameResultReasonSolved,
		task055UUIDPointer(firstParticipantID), baseTime.Add(time.Second), 2510,
	)
	secondGameProjection, err := domain.NewProjectionRevision(
		secondGame.SourceProjection.ID(), tournamentID,
		domain.ArtifactRef{Kind: domain.ArtifactKindGameResult, EntityID: secondGameEntityID},
		1, nil, commonTime, []byte("second-series-game"),
	)
	require.NoError(t, err)
	secondGame.SourceProjection = secondGameProjection.Revision()
	secondScore := task055ScoreHead(
		t, tournamentID, secondSeriesID, firstParticipantID, secondParticipantID,
		domain.SeriesFormatBO1, domain.SeriesScore{FirstParticipantWins: 1},
		1, baseTime.Add(4*time.Second), 2530,
	)
	secondScore.Attempts[0].CurrentGameResultRevisionID = secondGame.ID
	secondScore.Attempts[0].GameID = secondGameEntityID
	secondScore.CommandAttempt = func() *resultusecase.SeriesScoreAttemptReference {
		attempt := secondScore.Attempts[0]
		return &attempt
	}()
	secondScore.CommandID = secondGame.CommandID
	require.NoError(t, secondScore.Validate())
	secondScoreProjection := task055ExactProjection(t, secondScore.SourceProjection, "score-2530")
	secondSeries := task055SeriesResultHead(
		t, tournamentID, secondSeriesID, secondScore.ID, domain.SeriesStateCompleted,
		domain.SeriesResultReasonScoreComplete, task055UUIDPointer(firstParticipantID),
		baseTime.Add(6*time.Second), 2550,
	)
	secondSeriesProjection := task055ExactProjection(t, secondSeries.SourceProjection, "series-2550")
	projections = append(
		projections, secondGameProjection, secondScoreProjection, secondSeriesProjection,
	)
	dependencies := append([]domain.RevisionDependency(nil), input.Graph.Dependencies()...)
	standingsID := task055CurrentProjectionID(t, input.Graph, domain.ArtifactKindStandings)
	dependencies = append(dependencies,
		domain.RevisionDependency{
			SourceRevisionID:  secondGameProjection.Revision().ID(),
			DerivedRevisionID: secondScoreProjection.Revision().ID(),
		},
		domain.RevisionDependency{
			SourceRevisionID:  secondScoreProjection.Revision().ID(),
			DerivedRevisionID: secondSeriesProjection.Revision().ID(),
		},
		domain.RevisionDependency{
			SourceRevisionID:  secondSeriesProjection.Revision().ID(),
			DerivedRevisionID: standingsID,
		},
	)
	graph, err := domain.NewRevisionGraph(projections, dependencies)
	require.NoError(t, err)
	input.Graph = graph
	input.Results = append(input.Results,
		projection.OfficialResultProjectionInput{
			TerminalSource: projection.TerminalResultSourcePlayed,
			Result:         secondGame, ResultProjection: secondGameProjection,
		},
		projection.OfficialResultProjectionInput{
			TerminalSource: projection.TerminalResultSourcePlayed,
			Result:         secondSeries, ResultProjection: secondSeriesProjection,
			Score:           task055ScoreHeadPointer(secondScore),
			ScoreProjection: task055ProjectionPointer(secondScoreProjection),
		},
	)
	return input, commonTime, secondGameProjection.Revision().ID()
}
