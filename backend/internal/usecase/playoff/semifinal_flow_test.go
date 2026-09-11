package playoff_test

import (
	"crypto/sha256"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/seriesgraph"
)

func TestMaterializeSemifinalsMaterializesBothPositionsAndReplaysDetached(t *testing.T) {
	fixture := newSemifinalFlowFixture(t)
	bracketBefore := fixture.Bracket.Snapshot()

	flow, changed, err := playoff.MaterializeSemifinals(nil, fixture.Input)
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, flow.Validate())
	for index, graph := range flow.Graphs {
		require.Equal(t, index+1, fixture.Matches[index].Position)
		require.Equal(t, fixture.Matches[index].Series.ID, graph.Series.ID)
		require.Equal(t, domain.SeriesStatePlanned, graph.Series.State)
		require.Len(t, graph.Series.Slots, 1)
		require.Len(t, graph.Assignments, 1)
		require.Equal(t, domain.TournamentStageSemifinal, graph.CategoryRevision.Stage)
		require.Equal(t, domain.SeriesFormatBO1, graph.CategoryRevision.Format)
	}
	for _, match := range fixture.Bracket.Semifinals() {
		require.Equal(t, domain.SeriesStateLocked, match.Series.State)
		require.Empty(t, match.Series.Slots)
	}
	require.Equal(t, bracketBefore, fixture.Bracket)

	replayed, changed, err := playoff.MaterializeSemifinals(&flow, fixture.Input)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, flow.Authority, replayed.Authority)
	require.Equal(t, flow.RosterID, replayed.RosterID)
	for index := range flow.Graphs {
		require.Equal(t, flow.Graphs[index].Proof, replayed.Graphs[index].Proof)
		require.Equal(t, flow.Graphs[index].Series, replayed.Graphs[index].Series)
	}

	replayed.Graphs[0].SelectedCategories[0] = domain.CategoryMisc
	replayed.Graphs[0].TaskReservations[0].ContentDigest = sha256.Sum256([]byte("detached"))
	replayed.Authority.Semifinals[0].Series.FirstParticipantID = semifinalFlowID(99999)
	require.NoError(t, flow.Validate())
	require.Equal(t, domain.CategoryWeb, flow.Graphs[0].SelectedCategories[0])
	require.NotEqual(t, replayed.Graphs[0].TaskReservations[0].ContentDigest, flow.Graphs[0].TaskReservations[0].ContentDigest)
	require.NotEqual(t, replayed.Authority.Semifinals[0].Series.FirstParticipantID, flow.Authority.Semifinals[0].Series.FirstParticipantID)
}

func TestMaterializeSemifinalsRejectsDriftAndTampering(t *testing.T) {
	base := newSemifinalFlowFixture(t)
	flow, changed, err := playoff.MaterializeSemifinals(nil, base.Input)
	require.NoError(t, err)
	require.True(t, changed)

	tests := map[string]struct {
		mutate func(*playoff.SemifinalFlowInput)
		check  func(error)
		fresh  bool
	}{
		"command drift": {
			mutate: func(input *playoff.SemifinalFlowInput) {
				input.Materialize[0].CommandID = semifinalFlowID(11000)
			},
			check: func(err error) {
				require.ErrorIs(t, err, playoff.ErrSemifinalFlowConflict)
				require.ErrorIs(t, err, seriesgraph.ErrSeriesGraphConflict)
			},
		},
		"source drift": {
			mutate: func(input *playoff.SemifinalFlowInput) {
				input.Materialize[0].CategoryRevision.SourceContentRevision++
			},
			check: func(err error) {
				require.ErrorIs(t, err, playoff.ErrSemifinalFlowCommandReuse)
				require.ErrorIs(t, err, seriesgraph.ErrSeriesGraphCommandReuse)
			},
		},
		"authority mismatch": {
			mutate: func(input *playoff.SemifinalFlowInput) {
				input.Authority.BracketRevisionID = playoffRevisionID(12000)
			},
			check: func(err error) { require.ErrorIs(t, err, playoff.ErrSemifinalFlowConflict) },
		},
		"stage mismatch": {
			mutate: func(input *playoff.SemifinalFlowInput) {
				input.Materialize[0].CategoryRevision.Stage = domain.TournamentStageSwiss
			},
			check: func(err error) { require.ErrorIs(t, err, playoff.ErrInvalidSemifinalFlow) },
		},
		"format mismatch": {
			mutate: func(input *playoff.SemifinalFlowInput) {
				input.Authority.Semifinals[1].Series.Format = domain.SeriesFormatBO3
			},
			check: func(err error) { require.ErrorIs(t, err, playoff.ErrInvalidSemifinalFlow) },
		},
		"topology mismatch": {
			mutate: func(input *playoff.SemifinalFlowInput) {
				input.Materialize[1].Series.FirstParticipantID = input.Materialize[1].Series.SecondParticipantID
			},
			check: func(err error) { require.ErrorIs(t, err, playoff.ErrInvalidSemifinalFlow) },
		},
		"cross-graph owned identity": {
			mutate: func(input *playoff.SemifinalFlowInput) {
				input.Materialize[1].CommandID = input.Materialize[0].CommandID
			},
			check: func(err error) { require.ErrorIs(t, err, playoff.ErrInvalidSemifinalFlow) },
			fresh: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			input := cloneSemifinalFlowInput(base.Input)
			test.mutate(&input)
			var retained *playoff.SemifinalFlow
			if !test.fresh {
				retained = &flow
			}
			_, changed, err := playoff.MaterializeSemifinals(retained, input)
			require.False(t, changed)
			require.Error(t, err)
			test.check(err)
		})
	}

	tampered := flow
	tampered.Graphs[0].TaskReservations[0].ContentDigest = sha256.Sum256([]byte("tampered"))
	_, changed, err = playoff.MaterializeSemifinals(&tampered, base.Input)
	require.False(t, changed)
	require.ErrorIs(t, err, playoff.ErrInvalidSemifinalFlow)
	require.ErrorIs(t, err, seriesgraph.ErrInvalidSeriesGraph)
}

func TestSemifinalFlowAdvanceOrdersReversedSettlementsByPosition(t *testing.T) {
	fixture := newSemifinalFlowFixture(t)
	flow, changed, err := playoff.MaterializeSemifinals(nil, fixture.Input)
	require.NoError(t, err)
	require.True(t, changed)

	matches := fixture.Matches
	positionOne := completedSemifinal(matches[0].Series, true, 71)
	positionTwo := completedSemifinal(matches[1].Series, false, 72)
	advanced, changed, err := flow.Advance(playoff.SemifinalAdvancement{}, []domain.Series{positionTwo, positionOne})
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, []uuid.UUID{
		matches[0].Series.FirstParticipantID, matches[1].Series.SecondParticipantID,
	}, advanced.FinalParticipants())

	replayed, changed, err := flow.Advance(advanced, []domain.Series{positionOne, positionTwo})
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, advanced.FinalParticipants(), replayed.FinalParticipants())
}

func TestMaterializedSemifinalsPassRealExecutionFlow(t *testing.T) {
	fixture := newSemifinalFlowFixture(t)
	flow, changed, err := playoff.MaterializeSemifinals(nil, fixture.Input)
	require.NoError(t, err)
	require.True(t, changed)

	settledSeries := make([]domain.Series, 0, len(flow.Graphs))
	for index := range flow.Graphs {
		execution := newSemifinalExecutionFixture(t, index, flow)
		opened, changed, err := execution.readyWindow.Open(t.Context(), execution.openCommand)
		require.NoError(t, err)
		require.True(t, changed)
		execution.readinessRepo.setWave(opened.Wave)

		_, changed, err = execution.readiness.MarkReady(t.Context(), execution.readyCommands[0])
		require.NoError(t, err)
		require.True(t, changed)
		ready, changed, err := execution.readiness.MarkReady(t.Context(), execution.readyCommands[1])
		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, domain.WaveStateReady, ready.Wave.State)
		execution.startRepo.setWave(ready.Wave)

		started, changed, err := execution.start.Start(t.Context(), execution.startCommand)
		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, domain.WaveStateActive, started.Wave.State)
		require.Len(t, started.Games, 1)
		startedGame := started.Games[0]
		require.Equal(t, flow.Graphs[index].Series.ID, startedGame.Scope.SeriesID)
		require.Equal(t, flow.Graphs[index].Assignments[0].ID, startedGame.AssignmentID)
		require.Equal(t, flow.Graphs[index].Assignments[0].Plan.PlanRevisionID, startedGame.PlanRevisionID)

		execution.submissionRepo.setStartedGame(startedGame, startedGame.StartedAt.Add(time.Second))
		submission, changed, err := execution.submission.Submit(t.Context(), execution.submissionCommand)
		require.NoError(t, err)
		require.True(t, changed)
		require.True(t, submission.Correct)
		require.Equal(t, startedGame.SnapshotID, submission.SnapshotID)

		execution.settlementRepo.setAuthority(startedGame, []gamedomain.Submission{submission})
		settled, changed, err := execution.settlement.Settle(t.Context(), execution.settlementCommand)
		require.NoError(t, err)
		require.True(t, changed)
		require.NotNil(t, settled)
		require.NoError(t, settled.Validate())
		require.Equal(t, domain.SeriesStateCompleted, settled.Series.State)
		require.Equal(t, submission.ParticipantID, *settled.Series.WinnerID)

		replayed, changed, err := execution.settlement.Settle(t.Context(), execution.settlementCommand)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, settled, replayed)
		settledSeries = append(settledSeries, settled.Series)
	}

	advanced, changed, err := flow.Advance(playoff.SemifinalAdvancement{}, []domain.Series{settledSeries[1], settledSeries[0]})
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, []uuid.UUID{
		flow.Authority.Semifinals[0].Series.FirstParticipantID,
		flow.Authority.Semifinals[1].Series.FirstParticipantID,
	}, advanced.FinalParticipants())
	replayed, changed, err := flow.Advance(advanced, []domain.Series{settledSeries[0], settledSeries[1]})
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, advanced.FinalParticipants(), replayed.FinalParticipants())
}

func TestMaterializedSemifinalsAdvanceTerminalCoordinatorIntoOrderedFinalDraft(t *testing.T) {
	fixture := newSemifinalFlowFixture(t)
	flow, changed, err := playoff.MaterializeSemifinals(nil, fixture.Input)
	require.NoError(t, err)
	require.True(t, changed)

	settledSeries := make([]domain.Series, 0, len(flow.Graphs))
	for index := range flow.Graphs {
		execution := newSemifinalExecutionFixture(t, index, flow)
		opened, changed, err := execution.readyWindow.Open(t.Context(), execution.openCommand)
		require.NoError(t, err)
		require.True(t, changed)
		execution.readinessRepo.setWave(opened.Wave)
		_, changed, err = execution.readiness.MarkReady(t.Context(), execution.readyCommands[0])
		require.NoError(t, err)
		require.True(t, changed)
		ready, changed, err := execution.readiness.MarkReady(t.Context(), execution.readyCommands[1])
		require.NoError(t, err)
		require.True(t, changed)
		execution.startRepo.setWave(ready.Wave)
		started, changed, err := execution.start.Start(t.Context(), execution.startCommand)
		require.NoError(t, err)
		require.True(t, changed)
		startedGame := started.Games[0]
		execution.submissionRepo.setStartedGame(startedGame, startedGame.StartedAt.Add(time.Second))
		submission, changed, err := execution.submission.Submit(t.Context(), execution.submissionCommand)
		require.NoError(t, err)
		require.True(t, changed)
		require.True(t, submission.Correct)
		execution.settlementRepo.setAuthority(startedGame, []gamedomain.Submission{submission})
		settled, changed, err := execution.settlement.Settle(t.Context(), execution.settlementCommand)
		require.NoError(t, err)
		require.True(t, changed)
		require.NoError(t, settled.Validate())
		settledSeries = append(settledSeries, settled.Series)
	}

	terminalRepository := &semifinalTerminalRepository{stage: &playoff.SemifinalStageAuthority{
		StageCommandID: semifinalFlowID(12600), RosterID: fixture.RosterID, Bracket: fixture.Authority,
		Series:        []domain.Series{settledSeries[1], settledSeries[0]},
		Configuration: semifinalFlowContentConfiguration(t, fixture.Authority.TournamentID),
		RecordedAt:    fixture.CreatedAt.Add(6 * time.Minute),
	}}
	planner := &semifinalDraftPlanner{}
	coordinator := playoff.NewTerminalCoordinator(playoff.TerminalCoordinatorDependencies{
		Repository: terminalRepository, DraftPlanner: planner,
	})
	receipt, err := coordinator.AdvanceAfterSeriesSettlement(t.Context(), playoff.TerminalSeriesCommand{
		TournamentID: fixture.Authority.TournamentID, SeriesID: settledSeries[0].ID,
	})
	require.NoError(t, err)
	require.True(t, receipt.Changed)
	require.NotEqual(t, uuid.Nil, receipt.FinalSeriesID)
	require.NotNil(t, terminalRepository.plan)
	require.NotNil(t, planner.plan)
	require.Equal(t, terminalRepository.plan.Series, planner.plan.Series)
	require.Equal(t, *settledSeries[0].WinnerID, planner.plan.Series.FirstParticipantID)
	require.Equal(t, *settledSeries[1].WinnerID, planner.plan.Series.SecondParticipantID)
	require.Equal(t, []int{1, 2}, []int{planner.plan.Advancement[0].Position, planner.plan.Advancement[1].Position})
	require.Equal(t, *settledSeries[0].WinnerID, planner.plan.Advancement[0].WinnerID)
	require.Equal(t, *settledSeries[1].WinnerID, planner.plan.Advancement[1].WinnerID)
}

func cloneSemifinalFlowInput(input playoff.SemifinalFlowInput) playoff.SemifinalFlowInput {
	clone := input
	clone.Authority.Semifinals = make([]playoff.SemifinalMatch, len(input.Authority.Semifinals))
	for index, match := range input.Authority.Semifinals {
		clone.Authority.Semifinals[index] = match
		clone.Authority.Semifinals[index].Series.Slots = append([]domain.GameSlot(nil), match.Series.Slots...)
	}
	clone.Materialize = input.Materialize
	for index, source := range input.Materialize {
		clone.Materialize[index] = source
		clone.Materialize[index].Series.Slots = append([]domain.GameSlot(nil), source.Series.Slots...)
		clone.Materialize[index].CategoryRevision.CategoryPool.Categories = append([]domain.Category(nil), source.CategoryRevision.CategoryPool.Categories...)
		clone.Materialize[index].SelectedCategories = append([]domain.Category(nil), source.SelectedCategories...)
		clone.Materialize[index].AssignmentPlans = make([]assignment.ExactNormalAssignmentPlan, len(source.AssignmentPlans))
		for planIndex, plan := range source.AssignmentPlans {
			clone.Materialize[index].AssignmentPlans[planIndex] = assignment.CloneExactNormalAssignmentPlan(plan)
		}
	}
	return clone
}
