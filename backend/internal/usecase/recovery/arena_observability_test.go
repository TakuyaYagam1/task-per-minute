package recovery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
)

func TestArenaSubmissionAndRecoveryLogging(t *testing.T) {
	now := time.Date(2026, 9, 2, 13, 30, 0, 0, time.UTC)

	t.Run("emits rearm success", func(t *testing.T) {
		graph, _, _ := arenaRecoveryFixture(t, now)
		rearmer := &arenaRecoveryRearmerFake{}
		capture := &recoveryEventCapture{}

		result, err := NewArenaRecoveryReconciler(
			rearmer,
			recoveryClock{now: now},
			capture,
		).Reconcile(t.Context(), graph)

		require.NoError(t, err)
		require.Equal(t, 2, result.Rearmed)
		require.Len(t, rearmer.plansSnapshot(), 1)
		require.Len(t, capture.events, 1)
		event := capture.events[0]
		require.Equal(t, "arena.recovery", event.Event)
		require.Equal(t, observability.ArenaOutcomeSuccess, event.Outcome)
		require.Equal(t, "rearm_succeeded", event.Transition)
		require.Equal(t, "work_rearmed", event.ReasonCode)
		require.Equal(t, graph.Cursor.ProjectionRevision, event.Revision)
		require.Equal(t, "tournament", event.EntityKind)
		require.Equal(t, "startup_recovery", event.Stage)
		requireRecoveryEventsSanitized(t, capture.events, "")
	})

	t.Run("emits deterministic no-work recovery", func(t *testing.T) {
		graph, _, _ := arenaRecoveryFixture(t, now)
		makeRecoveryNoWork(&graph)
		firstCapture := &recoveryEventCapture{}
		secondCapture := &recoveryEventCapture{}

		first, firstErr := NewArenaRecoveryReconciler(
			&arenaRecoveryRearmerFake{}, recoveryClock{now: now}, firstCapture,
		).Reconcile(t.Context(), graph)
		second, secondErr := NewArenaRecoveryReconciler(
			&arenaRecoveryRearmerFake{}, recoveryClock{now: now}, secondCapture,
		).Reconcile(t.Context(), graph)

		require.NoError(t, firstErr)
		require.NoError(t, secondErr)
		require.Equal(t, first, second)
		require.Len(t, firstCapture.events, 1)
		require.Len(t, secondCapture.events, 1)
		firstEvent := firstCapture.events[0]
		require.Equal(t, observability.ArenaOutcomeSuccess, firstEvent.Outcome)
		require.Equal(t, "no_work", firstEvent.Transition)
		require.Equal(t, "nothing_to_rearm", firstEvent.ReasonCode)
		require.Equal(t, firstEvent.CorrelationID, secondCapture.events[0].CorrelationID)
		require.True(t, strings.HasPrefix(firstEvent.CorrelationID, "recovery-"))
		require.NotEqual(t, graph.TournamentID.String(), firstEvent.CorrelationID)
		requireRecoveryEventsSanitized(t, firstCapture.events, "")
	})

	t.Run("emits the fail-closed reason", func(t *testing.T) {
		graph, _, _ := arenaRecoveryFixture(t, now)
		graph.Lease = nil
		rearmer := &arenaRecoveryRearmerFake{}
		capture := &recoveryEventCapture{}

		result, err := NewArenaRecoveryReconciler(
			rearmer, recoveryClock{now: now}, capture,
		).Reconcile(t.Context(), graph)

		require.NoError(t, err)
		require.Equal(t, ArenaRecoveryFailReasonMissingLease, result.FailReason)
		require.Empty(t, rearmer.plansSnapshot())
		require.Len(t, capture.events, 1)
		require.Equal(t, observability.ArenaOutcomeRejected, capture.events[0].Outcome)
		require.Equal(t, "fail_closed", capture.events[0].Transition)
		require.Equal(t, "missing_lease", capture.events[0].ReasonCode)
		requireRecoveryEventsSanitized(t, capture.events, "")
	})

	t.Run("emits sanitized rearm failure without changing the error", func(t *testing.T) {
		graph, _, _ := arenaRecoveryFixture(t, now)
		rearmErr := errors.New("timer credential payload")
		capture := &recoveryEventCapture{}

		result, err := NewArenaRecoveryReconciler(
			&arenaRecoveryRearmerFake{err: rearmErr}, recoveryClock{now: now}, capture,
		).Reconcile(t.Context(), graph)

		require.Zero(t, result)
		require.ErrorIs(t, err, rearmErr)
		require.Len(t, capture.events, 1)
		require.Equal(t, observability.ArenaOutcomeFailure, capture.events[0].Outcome)
		require.Equal(t, "rearm_failed", capture.events[0].Transition)
		require.Equal(t, "rearmer_failed", capture.events[0].ReasonCode)
		requireRecoveryEventsSanitized(t, capture.events, rearmErr.Error())
	})

	t.Run("keeps the existing constructor form", func(t *testing.T) {
		graph, _, _ := arenaRecoveryFixture(t, now)

		result, err := NewArenaRecoveryReconciler(
			&arenaRecoveryRearmerFake{}, recoveryClock{now: now},
		).Reconcile(t.Context(), graph)

		require.NoError(t, err)
		require.Equal(t, 2, result.Rearmed)
	})
}

type recoveryEventCapture struct {
	events []observability.ArenaEvent
}

func (c *recoveryEventCapture) ObserveArenaEvent(_ context.Context, event observability.ArenaEvent) {
	c.events = append(c.events, event)
}

func makeRecoveryNoWork(graph *ArenaRecoveryGraph) {
	graph.Series[0].Series.Slots[0].Attempts[0].State = domain.ArenaGameStatePlanned
	graph.Waves[1].State = domain.ArenaWaveStatePlanned
	graph.Waves[1].ReadyWindow = nil
	graph.Deadlines = nil
	graph.Lease = nil
}

func requireRecoveryEventsSanitized(
	t *testing.T,
	events []observability.ArenaEvent,
	rawError string,
) {
	t.Helper()

	rendered := fmt.Sprintf("%#v", events)
	require.NotContains(t, rendered, "fixture-flag")
	require.NotContains(t, rendered, arenaRecoveryID(100).String())
	require.NotContains(t, rendered, arenaRecoveryID(110).String())
	if rawError != "" {
		require.NotContains(t, rendered, rawError)
	}
}

var _ observability.ArenaEventObserver = (*recoveryEventCapture)(nil)
