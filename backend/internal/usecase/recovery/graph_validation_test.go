package recovery_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery"
)

func TestRecoveryReconcilerMissingEvidence(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*recovery.RecoveryGraph)
		want   recovery.RecoveryFailReason
	}{
		{
			name: "result",
			mutate: func(graph *recovery.RecoveryGraph) {
				game := &graph.Series[0].Series.Slots[0].Attempts[0]
				winnerID := graph.Series[0].Series.FirstParticipantID
				revisionID := domain.OfficialResultRevisionID(recoveryID(95))
				game.State = domain.GameStateCompleted
				game.ResultReason = domain.GameResultReasonSolved
				game.WinnerID = &winnerID
				game.ResultRevisionID = &revisionID
			},
			want: recovery.RecoveryFailReasonMissingResult,
		},
		{
			name: "score",
			mutate: func(graph *recovery.RecoveryGraph) {
				graph.Scores = nil
			},
			want: recovery.RecoveryFailReasonMissingScore,
		},
		{
			name: "pause",
			mutate: func(graph *recovery.RecoveryGraph) {
				graph.Series[0].Series.Slots[0].Attempts[0].State = domain.GameStatePaused
			},
			want: recovery.RecoveryFailReasonMissingPause,
		},
		{
			name: "revision",
			mutate: func(graph *recovery.RecoveryGraph) {
				graph.Scores[0].RevisionID = domain.SeriesScoreRevisionID{}
			},
			want: recovery.RecoveryFailReasonMissingRevision,
		},
		{
			name: "reservation",
			mutate: func(graph *recovery.RecoveryGraph) {
				graph.Reservations = graph.Reservations[:1]
			},
			want: recovery.RecoveryFailReasonMissingReservation,
		},
		{
			name: "lease",
			mutate: func(graph *recovery.RecoveryGraph) {
				graph.Lease = nil
			},
			want: recovery.RecoveryFailReasonMissingLease,
		},
		{
			name: "deadline",
			mutate: func(graph *recovery.RecoveryGraph) {
				graph.Deadlines = graph.Deadlines[:1]
			},
			want: recovery.RecoveryFailReasonMissingDeadline,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			graph, _, _ := recoveryFixture(t, now)
			test.mutate(&graph)
			rearmer := newRecoveryRearmerHarness(t, nil)

			result, err := recovery.NewRecoveryReconciler(
				rearmer.rearmer,
				newRecoveryClock(t, now),
			).Reconcile(t.Context(), graph)

			require.NoError(t, err)
			require.Equal(t, recovery.RecoveryResult{FailReason: test.want}, result)
			require.Empty(t, rearmer.plansSnapshot())
		})
	}
}

func TestRecoveryReconcilerInvalidGraph(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*recovery.RecoveryGraph)
		want   recovery.RecoveryFailReason
	}{
		{
			name: "child graph",
			mutate: func(graph *recovery.RecoveryGraph) {
				graph.Waves[0].Members[0].ParticipantID = recoveryID(96)
			},
			want: recovery.RecoveryFailReasonInvalidGraph,
		},
		{
			name: "assignment attempt",
			mutate: func(graph *recovery.RecoveryGraph) {
				series := graph.Series[0].Series
				graph.Assignments[0].Assignment = recoveryAssignment(
					t, series.FirstParticipantID, series.SecondParticipantID, recoveryID(97),
				)
			},
			want: recovery.RecoveryFailReasonInvalidGraph,
		},
		{
			name: "DAG",
			mutate: func(graph *recovery.RecoveryGraph) {
				graph.Revisions = domain.RevisionGraph{}
			},
			want: recovery.RecoveryFailReasonInvalidDAG,
		},
		{
			name: "cursor",
			mutate: func(graph *recovery.RecoveryGraph) {
				graph.Cursor.LastSequence++
			},
			want: recovery.RecoveryFailReasonInvalidCursor,
		},
		{
			name: "realtime cursor projection",
			mutate: func(graph *recovery.RecoveryGraph) {
				graph.Cursor.ProjectionRevision++
			},
			want: recovery.RecoveryFailReasonInvalidCursor,
		},
		{
			name: "derived cursor revision",
			mutate: func(graph *recovery.RecoveryGraph) {
				graph.Cursor.DerivedRevisionID = domain.DerivedRevisionID(recoveryID(98))
			},
			want: recovery.RecoveryFailReasonInvalidCursor,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			graph, _, _ := recoveryFixture(t, now)
			test.mutate(&graph)
			rearmer := newRecoveryRearmerHarness(t, nil)

			result, err := recovery.NewRecoveryReconciler(
				rearmer.rearmer,
				newRecoveryClock(t, now),
			).Reconcile(t.Context(), graph)

			require.NoError(t, err)
			require.Equal(t, recovery.RecoveryResult{FailReason: test.want}, result)
			require.Empty(t, rearmer.plansSnapshot())
		})
	}
}
