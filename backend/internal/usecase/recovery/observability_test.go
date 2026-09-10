package recovery_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery"
	recoverymocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery/mocks"
)

func TestSubmissionAndRecoveryLogging(t *testing.T) {
	now := time.Date(2026, 9, 2, 13, 30, 0, 0, time.UTC)

	t.Run("emits rearm success", func(t *testing.T) {
		graph, _, _ := recoveryFixture(t, now)
		rearmer := newRecoveryRearmerHarness(t, nil)
		capture := newRecoveryEventHarness(t)

		result, err := recovery.NewRecoveryReconciler(
			rearmer.rearmer,
			newRecoveryClock(t, now),
			capture.observer,
		).Reconcile(t.Context(), graph)

		require.NoError(t, err)
		require.Equal(t, 2, result.Rearmed)
		require.Len(t, rearmer.plansSnapshot(), 1)
		events := capture.eventsSnapshot()
		require.Len(t, events, 1)
		event := events[0]
		require.Equal(t, recovery.RecoveryOutcomeSuccess, event.Outcome)
		require.Equal(t, recovery.RecoveryStageStartup, event.Stage)
		require.Equal(t, "rearm_succeeded", event.Transition)
		require.Equal(t, "work_rearmed", event.ReasonCode)
		require.Equal(t, graph.Cursor.ProjectionRevision, event.Revision)
		require.Equal(t, graph.TournamentID, event.TournamentID)
		requireRecoveryEventsSanitized(t, events, "")
	})

	t.Run("emits deterministic no-work recovery", func(t *testing.T) {
		graph, _, _ := recoveryFixture(t, now)
		makeRecoveryNoWork(&graph)
		firstCapture := newRecoveryEventHarness(t)
		secondCapture := newRecoveryEventHarness(t)
		firstRearmer := newRecoveryRearmerHarness(t, nil)
		secondRearmer := newRecoveryRearmerHarness(t, nil)

		first, firstErr := recovery.NewRecoveryReconciler(
			firstRearmer.rearmer, newRecoveryClock(t, now), firstCapture.observer,
		).Reconcile(t.Context(), graph)
		second, secondErr := recovery.NewRecoveryReconciler(
			secondRearmer.rearmer, newRecoveryClock(t, now), secondCapture.observer,
		).Reconcile(t.Context(), graph)

		require.NoError(t, firstErr)
		require.NoError(t, secondErr)
		require.Equal(t, first, second)
		firstEvents := firstCapture.eventsSnapshot()
		secondEvents := secondCapture.eventsSnapshot()
		require.Len(t, firstEvents, 1)
		require.Len(t, secondEvents, 1)
		firstEvent := firstEvents[0]
		require.Equal(t, recovery.RecoveryOutcomeSuccess, firstEvent.Outcome)
		require.Equal(t, recovery.RecoveryStageStartup, firstEvent.Stage)
		require.Equal(t, "no_work", firstEvent.Transition)
		require.Equal(t, "nothing_to_rearm", firstEvent.ReasonCode)
		require.Equal(t, firstEvent.CorrelationID, secondEvents[0].CorrelationID)
		require.True(t, strings.HasPrefix(firstEvent.CorrelationID, "recovery-"))
		require.NotEqual(t, graph.TournamentID.String(), firstEvent.CorrelationID)
		requireRecoveryEventsSanitized(t, firstEvents, "")
	})

	t.Run("emits the fail-closed reason", func(t *testing.T) {
		graph, _, _ := recoveryFixture(t, now)
		graph.Lease = nil
		rearmer := newRecoveryRearmerHarness(t, nil)
		capture := newRecoveryEventHarness(t)

		result, err := recovery.NewRecoveryReconciler(
			rearmer.rearmer, newRecoveryClock(t, now), capture.observer,
		).Reconcile(t.Context(), graph)

		require.NoError(t, err)
		require.Equal(t, recovery.RecoveryFailReasonMissingLease, result.FailReason)
		require.Empty(t, rearmer.plansSnapshot())
		events := capture.eventsSnapshot()
		require.Len(t, events, 1)
		require.Equal(t, recovery.RecoveryOutcomeRejected, events[0].Outcome)
		require.Equal(t, recovery.RecoveryStageStartup, events[0].Stage)
		require.Equal(t, "fail_closed", events[0].Transition)
		require.Equal(t, "missing_lease", events[0].ReasonCode)
		requireRecoveryEventsSanitized(t, events, "")
	})

	t.Run("emits sanitized rearm failure without changing the error", func(t *testing.T) {
		graph, _, _ := recoveryFixture(t, now)
		rearmErr := errors.New("timer credential payload")
		capture := newRecoveryEventHarness(t)
		rearmer := newRecoveryRearmerHarness(t, rearmErr)

		result, err := recovery.NewRecoveryReconciler(
			rearmer.rearmer, newRecoveryClock(t, now), capture.observer,
		).Reconcile(t.Context(), graph)

		require.Zero(t, result)
		require.ErrorIs(t, err, rearmErr)
		events := capture.eventsSnapshot()
		require.Len(t, events, 1)
		require.Equal(t, recovery.RecoveryOutcomeFailure, events[0].Outcome)
		require.Equal(t, recovery.RecoveryStageStartup, events[0].Stage)
		require.Equal(t, "rearm_failed", events[0].Transition)
		require.Equal(t, "rearmer_failed", events[0].ReasonCode)
		requireRecoveryEventsSanitized(t, events, rearmErr.Error())
	})

	t.Run("keeps the existing constructor form", func(t *testing.T) {
		graph, _, _ := recoveryFixture(t, now)
		rearmer := newRecoveryRearmerHarness(t, nil)

		result, err := recovery.NewRecoveryReconciler(
			rearmer.rearmer, newRecoveryClock(t, now),
		).Reconcile(t.Context(), graph)

		require.NoError(t, err)
		require.Equal(t, 2, result.Rearmed)
	})

	t.Run("skips a typed nil observer", func(t *testing.T) {
		graph, _, _ := recoveryFixture(t, now)
		rearmer := newRecoveryRearmerHarness(t, nil)
		capture := newRecoveryEventHarness(t)
		var typedNil *recoverymocks.MockRecoveryObserver

		result, err := recovery.NewRecoveryReconciler(
			rearmer.rearmer,
			newRecoveryClock(t, now),
			typedNil,
			capture.observer,
		).Reconcile(t.Context(), graph)

		require.NoError(t, err)
		require.Equal(t, 2, result.Rearmed)
		require.Len(t, capture.eventsSnapshot(), 1)
	})
}

func TestRecoveryReconcilerIsolatesObserverPanic(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	graph, _, _ := recoveryFixture(t, now)
	rearmer := newRecoveryRearmerHarness(t, nil)
	observer := recoverymocks.NewMockRecoveryObserver(t)
	observer.EXPECT().ObserveRecovery(mock.Anything, mock.Anything).
		Run(func(context.Context, recovery.RecoveryEvent) { panic("observer failed") }).Once()
	reconciler := recovery.NewRecoveryReconciler(
		rearmer.rearmer,
		newRecoveryClock(t, now),
		observer,
	)

	var (
		result recovery.RecoveryResult
		err    error
	)
	require.NotPanics(t, func() {
		result, err = reconciler.Reconcile(t.Context(), graph)
	})
	require.NoError(t, err)
	require.Equal(t, 2, result.Rearmed)
	require.Len(t, rearmer.plansSnapshot(), 1)
}

type recoveryEventHarness struct {
	observer *recoverymocks.MockRecoveryObserver
	mu       sync.Mutex
	events   []recovery.RecoveryEvent
}

func newRecoveryEventHarness(t *testing.T) *recoveryEventHarness {
	t.Helper()
	harness := &recoveryEventHarness{}
	harness.observer = recoverymocks.NewMockRecoveryObserver(t)
	harness.observer.EXPECT().ObserveRecovery(mock.Anything, mock.Anything).
		Run(harness.capture).Maybe()
	return harness
}

func (h *recoveryEventHarness) capture(_ context.Context, event recovery.RecoveryEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, event)
}

func (h *recoveryEventHarness) eventsSnapshot() []recovery.RecoveryEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]recovery.RecoveryEvent(nil), h.events...)
}

func makeRecoveryNoWork(graph *recovery.RecoveryGraph) {
	graph.Series[0].Series.Slots[0].Attempts[0].State = domain.GameStatePlanned
	graph.Waves[1].State = domain.WaveStatePlanned
	graph.Waves[1].ReadyWindow = nil
	graph.Deadlines = nil
	graph.Lease = nil
}

func requireRecoveryEventsSanitized(
	t *testing.T,
	events []recovery.RecoveryEvent,
	rawError string,
) {
	t.Helper()

	rendered := fmt.Sprintf("%#v", events)
	require.NotContains(t, rendered, "fixture-flag")
	require.NotContains(t, rendered, recoveryID(100).String())
	require.NotContains(t, rendered, recoveryID(110).String())
	if rawError != "" {
		require.NotContains(t, rendered, rawError)
	}
}

var _ recovery.RecoveryObserver = (*recoverymocks.MockRecoveryObserver)(nil)
