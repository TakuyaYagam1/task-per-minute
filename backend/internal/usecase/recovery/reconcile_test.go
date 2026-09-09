package recovery_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery"
)

func TestRecoveryReconciler(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

	t.Run("validates the full graph before one deterministic rearm", func(t *testing.T) {
		graph, gameDeadline, windowDeadline := recoveryFixture(t, now)
		rearmer := newRecoveryRearmerHarness(t, nil)

		result, err := recovery.NewRecoveryReconciler(rearmer.rearmer, newRecoveryClock(t, now)).Reconcile(
			t.Context(),
			graph,
		)

		require.NoError(t, err)
		wantPlan := recovery.RecoveryRearmPlan{
			TournamentID: graph.TournamentID,
			Cursor:       graph.Cursor,
			Lease:        *graph.Lease,
			Work:         []recovery.RecoveryDeadlineEvidence{gameDeadline, windowDeadline},
		}
		require.Equal(t, recovery.RecoveryResult{Plan: wantPlan, Rearmed: 2}, result)
		require.Equal(t, []recovery.RecoveryRearmPlan{wantPlan}, rearmer.plansSnapshot())
	})

	t.Run("requires reservations only for checked-in roster members", func(t *testing.T) {
		graph, _, _ := recoveryFixture(t, now)
		graph.Roster.Participants = append(graph.Roster.Participants, domain.Participant{
			ID: recoveryID(6), TournamentID: graph.TournamentID,
			PlayerID: recoveryID(7), Seed: 3,
			Attendance: domain.AttendanceStateWithdrawn,
		})
		rearmer := newRecoveryRearmerHarness(t, nil)

		result, err := recovery.NewRecoveryReconciler(rearmer.rearmer, newRecoveryClock(t, now)).Reconcile(
			t.Context(), graph,
		)

		require.NoError(t, err)
		require.Empty(t, result.FailReason)
		require.Equal(t, 2, result.Rearmed)
		require.Len(t, rearmer.plansSnapshot(), 1)
	})

	t.Run("returns an empty plan when no work needs rearming", func(t *testing.T) {
		graph, _, _ := recoveryFixture(t, now)
		graph.Series[0].Series.Slots[0].Attempts[0].State = domain.GameStatePlanned
		graph.Waves[1].State = domain.WaveStatePlanned
		graph.Waves[1].ReadyWindow = nil
		graph.Deadlines = nil
		graph.Lease = nil
		rearmer := newRecoveryRearmerHarness(t, nil)

		result, err := recovery.NewRecoveryReconciler(rearmer.rearmer, newRecoveryClock(t, now)).Reconcile(
			t.Context(),
			graph,
		)

		require.NoError(t, err)
		require.Equal(t, recovery.RecoveryResult{Plan: recovery.RecoveryRearmPlan{
			TournamentID: graph.TournamentID,
			Cursor:       graph.Cursor,
			Work:         []recovery.RecoveryDeadlineEvidence{},
		}}, result)
		require.Empty(t, rearmer.plansSnapshot())
	})

	t.Run("preserves a rearmer failure", func(t *testing.T) {
		graph, _, _ := recoveryFixture(t, now)
		wantErr := errors.New("timer store unavailable")
		rearmer := newRecoveryRearmerHarness(t, wantErr)

		result, err := recovery.NewRecoveryReconciler(rearmer.rearmer, newRecoveryClock(t, now)).Reconcile(
			t.Context(),
			graph,
		)

		require.Zero(t, result)
		require.ErrorIs(t, err, wantErr)
		require.Len(t, rearmer.plansSnapshot(), 1)
	})
}
