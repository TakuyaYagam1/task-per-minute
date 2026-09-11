package swiss_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
	seriesgraph "github.com/TakuyaYagam1/task-per-minute/internal/usecase/seriesgraph"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

func TestMaterializeSeriesFlowModes(t *testing.T) {
	t.Parallel()

	modes := []domain.CategoryMode{
		domain.CategoryModeRandom,
		domain.CategoryModeAdmin,
		domain.CategoryModeDraft,
	}
	for _, mode := range modes {
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()

			input, selected := seriesFlowInput(t, mode)
			flow, changed, err := swissusecase.MaterializeSeries(nil, input)
			require.NoError(t, err)
			require.True(t, changed)
			require.NoError(t, flow.Validate())
			require.NoError(t, flow.Graph.Validate())
			require.Len(t, flow.Graph.Assignments, 1)
			require.Len(t, flow.Graph.Series.Slots, 1)
			require.Equal(t, selected, flow.Graph.SelectedCategories[0])
			require.Contains(t, input.CategoryRevision.CategoryPool.Categories, selected)
			require.Equal(t, flow.Graph.Series.ID, flow.LockedSeries.SeriesID)
			require.Equal(t, flow.Graph.Series.FirstParticipantID, flow.LockedSeries.FirstParticipantID)
			require.Equal(t, flow.Graph.Series.SecondParticipantID, flow.LockedSeries.SecondParticipantID)
			require.Equal(t, flow.Graph.CategoryRevision.ID, flow.LockedSeries.CategoryRevisionID)
			require.Equal(t, flow.Graph.CategoryRevision.Revision, flow.LockedSeries.CategoryRevision)
			require.Equal(t, flow.Graph.Assignments[0].ID, flow.LockedSeries.AssignmentID)
			require.Equal(t, flow.Graph.Assignments[0].Plan.PlanID, flow.LockedSeries.AssignmentPlanID)
			require.Equal(t, flow.Graph.Assignments[0].Plan.PlanRevisionID, flow.LockedSeries.AssignmentPlanRevisionID)
			require.Equal(t, flow.Graph.Assignments[0].Plan.SelectedEdges[0].ReservationID, flow.LockedSeries.ReservationID)
			require.Equal(t, input.PairingID, flow.LockedSeries.PairingID)
			require.Equal(t, input.AssignmentRevision, flow.LockedSeries.AssignmentRevision)
			require.Equal(t, input.ReservationRevision, flow.LockedSeries.ReservationRevision)

			replayed, replayChanged, err := swissusecase.MaterializeSeries(&flow, input)
			require.NoError(t, err)
			require.False(t, replayChanged)
			require.Equal(t, flow, replayed)

			replayed.Graph.SelectedCategories[0] = domain.CategoryMisc
			replayed.Graph.Snapshots[0].Snapshot.Hints[0] = "detached"
			replayed.Graph.Series.Slots[0].Attempts[0].State = domain.GameStateActive
			require.NotEqual(t, flow.Graph.SelectedCategories, replayed.Graph.SelectedCategories)
			require.NoError(t, flow.Validate())
		})
	}
}

func TestMaterializeSeriesFlowRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	base, _ := seriesFlowInput(t, domain.CategoryModeRandom)
	tests := []struct {
		name   string
		mutate func(*swissusecase.SeriesFlowInput)
	}{
		{
			name: "non Swiss stage",
			mutate: func(input *swissusecase.SeriesFlowInput) {
				input.CategoryRevision.Stage = domain.TournamentStageFinal
			},
		},
		{
			name: "BO3 Series",
			mutate: func(input *swissusecase.SeriesFlowInput) {
				input.Series.Format = domain.SeriesFormatBO3
			},
		},
		{
			name: "invalid mode",
			mutate: func(input *swissusecase.SeriesFlowInput) {
				input.CategoryRevision.Mode = domain.CategoryMode("operator")
			},
		},
		{
			name: "mismatched assignment evidence",
			mutate: func(input *swissusecase.SeriesFlowInput) {
				input.AssignmentPlans[0].Scope.SeriesID = seriesFlowID(907)
			},
		},
		{
			name: "mismatched category evidence",
			mutate: func(input *swissusecase.SeriesFlowInput) {
				input.CategoryRevision.ID = seriesFlowID(908)
			},
		},
		{
			name: "missing pairing",
			mutate: func(input *swissusecase.SeriesFlowInput) {
				input.PairingID = uuid.Nil
			},
		},
		{
			name: "missing assignment revision",
			mutate: func(input *swissusecase.SeriesFlowInput) {
				input.AssignmentRevision = 0
			},
		},
		{
			name: "missing reservation revision",
			mutate: func(input *swissusecase.SeriesFlowInput) {
				input.ReservationRevision = 0
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := cloneSeriesFlowInput(base)
			test.mutate(&input)
			flow, changed, err := swissusecase.MaterializeSeries(nil, input)
			require.Zero(t, flow)
			require.False(t, changed)
			require.ErrorIs(t, err, swissusecase.ErrInvalidSeriesFlow)
		})
	}
}

func TestMaterializeSeriesFlowRejectsTamperedDerivedLock(t *testing.T) {
	t.Parallel()

	input, _ := seriesFlowInput(t, domain.CategoryModeRandom)
	flow, changed, err := swissusecase.MaterializeSeries(nil, input)
	require.NoError(t, err)
	require.True(t, changed)

	tests := []struct {
		name   string
		mutate func(*swissusecase.SeriesFlow)
	}{
		{
			name: "foreign series",
			mutate: func(value *swissusecase.SeriesFlow) {
				value.LockedSeries.SeriesID = seriesFlowID(900)
			},
		},
		{
			name: "foreign participant",
			mutate: func(value *swissusecase.SeriesFlow) {
				value.LockedSeries.FirstParticipantID = seriesFlowID(901)
			},
		},
		{
			name: "foreign category revision",
			mutate: func(value *swissusecase.SeriesFlow) {
				value.LockedSeries.CategoryRevisionID = seriesFlowID(902)
			},
		},
		{
			name: "foreign assignment plan",
			mutate: func(value *swissusecase.SeriesFlow) {
				value.LockedSeries.AssignmentPlanID = seriesFlowID(903)
			},
		},
		{
			name: "foreign primary reservation",
			mutate: func(value *swissusecase.SeriesFlow) {
				value.LockedSeries.ReservationID = seriesFlowID(904)
			},
		},
		{
			name: "nonpositive supplied revision",
			mutate: func(value *swissusecase.SeriesFlow) {
				value.LockedSeries.AssignmentRevision = 0
			},
		},
		{
			name: "tampered graph proof",
			mutate: func(value *swissusecase.SeriesFlow) {
				value.Graph.Proof.ProofHash = "tampered"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tampered := flow
			test.mutate(&tampered)
			require.ErrorIs(t, tampered.Validate(), swissusecase.ErrInvalidSeriesFlow)
		})
	}
}

func TestMaterializeSeriesFlowRejectsReusedAndForeignCommands(t *testing.T) {
	t.Parallel()

	input, _ := seriesFlowInput(t, domain.CategoryModeRandom)
	first, changed, err := swissusecase.MaterializeSeries(nil, input)
	require.NoError(t, err)
	require.True(t, changed)

	t.Run("changed source with retained command", func(t *testing.T) {
		altered := cloneSeriesFlowInput(input)
		altered.CategoryRevision.SourceContentRevision++
		flow, changed, err := swissusecase.MaterializeSeries(&first, altered)
		require.Zero(t, flow)
		require.False(t, changed)
		require.ErrorIs(t, err, swissusecase.ErrSeriesFlowCommandReuse)
		require.ErrorIs(t, err, seriesgraph.ErrSeriesGraphCommandReuse)
	})

	t.Run("changed command identity", func(t *testing.T) {
		altered := cloneSeriesFlowInput(input)
		altered.CommandID = seriesFlowID(905)
		flow, changed, err := swissusecase.MaterializeSeries(&first, altered)
		require.Zero(t, flow)
		require.False(t, changed)
		require.ErrorIs(t, err, swissusecase.ErrSeriesFlowConflict)
		require.ErrorIs(t, err, seriesgraph.ErrSeriesGraphConflict)
	})

	t.Run("foreign existing flow fails closed", func(t *testing.T) {
		foreignInput, _ := seriesFlowInput(t, domain.CategoryModeAdmin)
		foreign, foreignChanged, err := swissusecase.MaterializeSeries(nil, foreignInput)
		require.NoError(t, err)
		require.True(t, foreignChanged)

		foreign.Graph.Series.ID = seriesFlowID(906)
		flow, changed, err := swissusecase.MaterializeSeries(&foreign, input)
		require.Zero(t, flow)
		require.False(t, changed)
		require.ErrorIs(t, err, swissusecase.ErrInvalidSeriesFlow)
	})
}

func TestSeriesFlowCanFeedRoundLockProof(t *testing.T) {
	t.Parallel()

	input, _ := seriesFlowInput(t, domain.CategoryModeAdmin)
	flow, changed, err := swissusecase.MaterializeSeries(nil, input)
	require.NoError(t, err)
	require.True(t, changed)

	proofInput := roundLockProofInputFromSeriesFlow(flow)
	proof, err := swissusecase.NewRoundLockProof(proofInput)
	require.NoError(t, err)
	require.NoError(t, proof.Validate())
	require.Contains(t, proof.Series, flow.LockedSeries)
}

func TestMaterializedSwissSeriesPassesExecutionFlow(t *testing.T) {
	t.Parallel()

	input, _ := seriesFlowInput(t, domain.CategoryModeAdmin)
	flow, changed, err := swissusecase.MaterializeSeries(nil, input)
	require.NoError(t, err)
	require.True(t, changed)

	execution := newSeriesFlowExecutionFixture(t, flow)
	opened, changed, err := execution.readyWindow.Open(t.Context(), execution.openCommand)
	require.NoError(t, err)
	require.True(t, changed)
	execution.readinessRepo.setWave(opened.Wave)

	_, changed, err = execution.readiness.MarkReady(t.Context(), execution.readyCommands[0])
	require.NoError(t, err)
	require.True(t, changed)
	secondReady, changed, err := execution.readiness.MarkReady(t.Context(), execution.readyCommands[1])
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, domain.WaveStateReady, secondReady.Wave.State)
	execution.startRepo.setWave(secondReady.Wave)

	started, changed, err := execution.start.Start(t.Context(), execution.startCommand)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, domain.WaveStateActive, started.Wave.State)
	require.Len(t, started.Games, 1)
	startedGame := started.Games[0]
	require.Equal(t, flow.Graph.Series.ID, startedGame.Scope.SeriesID)
	require.Equal(t, flow.Graph.Assignments[0].ID, startedGame.AssignmentID)
	require.Equal(t, flow.Graph.Assignments[0].Plan.PlanRevisionID, startedGame.PlanRevisionID)
	require.Equal(t, flow.Graph.Snapshots[0].Snapshot.SnapshotID, startedGame.SnapshotID)
	require.Equal(t, flow.Graph.Snapshots[0].ContentDigest, startedGame.ContentDigest)

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
}

func TestSeriesFlowHelpersRemainDetached(t *testing.T) {
	t.Parallel()

	input, _ := seriesFlowInput(t, domain.CategoryModeRandom)
	original := cloneSeriesFlowInput(input)
	input.AssignmentPlans[0].SelectedEdges[0].Snapshot.Hints[0] = "changed"
	require.True(t, reflect.DeepEqual(original.AssignmentPlans[0].SelectedEdges[0].Snapshot.Hints, []string{"hint one", "hint two"}))

	_, _, err := swissusecase.MaterializeSeries(nil, input)
	require.Error(t, err)
}
