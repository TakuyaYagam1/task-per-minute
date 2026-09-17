package observability

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type testClock struct {
	values []time.Time
	calls  int
	panic  bool
}

func (clock *testClock) Now() time.Time {
	clock.calls++
	if clock.panic {
		panic("clock failed")
	}
	value := clock.values[clock.calls-1]
	return value
}

type testObserver struct {
	events []OperationEvent
	panic  bool
}

func (observer *testObserver) ObserveTournamentAdminOperation(_ context.Context, event OperationEvent) {
	if observer.panic {
		panic("observer failed")
	}
	observer.events = append(observer.events, event)
}

func TestOperationMeasurementEmitsBoundedSuccessEvent(t *testing.T) {
	startedAt := time.Date(2026, time.September, 17, 12, 0, 0, 0, time.UTC)
	clock := &testClock{values: []time.Time{startedAt, startedAt.Add(25 * time.Millisecond)}}
	observer := &testObserver{}

	measurement := NewOperationMeasurement(clock, observer)
	measurement.Emit(context.Background(), OperationEvent{
		Operation:  OperationResultCorrect,
		Revision:   -1,
		Outcome:    "client_value",
		ReasonCode: "client_value",
		Duration:   time.Hour,
	}, nil)

	require.Equal(t, 2, clock.calls)
	require.Equal(t, []OperationEvent{{
		Operation:  OperationResultCorrect,
		Outcome:    OperationOutcomeSuccess,
		ReasonCode: "completed",
		Revision:   0,
		Duration:   25 * time.Millisecond,
	}}, observer.events)
}

func TestOperationMeasurementMapsTerminalErrors(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		outcome string
		reason  string
	}{
		{name: "validation", err: domain.ErrValidation, outcome: OperationOutcomeRejected, reason: "invalid_command"},
		{name: "forbidden", err: fmt.Errorf("wrapped: %w", domain.ErrForbidden), outcome: OperationOutcomeRejected, reason: "forbidden"},
		{name: "conflict", err: domain.ErrConflict, outcome: OperationOutcomeRejected, reason: "stale_revision"},
		{name: "rate limited", err: domain.ErrRateLimited, outcome: OperationOutcomeRetry, reason: "rate_limited"},
		{name: "internal", err: errors.New("internal"), outcome: OperationOutcomeFailure, reason: "operation_failed"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			observer := &testObserver{}
			NewOperationMeasurement(nil, observer).Emit(context.Background(), OperationEvent{}, testCase.err)
			require.Equal(t, []OperationEvent{{
				Outcome:    testCase.outcome,
				ReasonCode: testCase.reason,
			}}, observer.events)
		})
	}
}

func TestOperationMeasurementIsolatesNilContextTypedNilObserverAndPanics(t *testing.T) {
	var typedNil *testObserver
	clock := &testClock{values: []time.Time{{}}}
	NewOperationMeasurement(clock, typedNil).Emit(context.Background(), OperationEvent{}, nil)
	require.Equal(t, 0, clock.calls)

	observer := &testObserver{}
	NewOperationMeasurement(nil, observer).Emit(nil, OperationEvent{}, nil)
	require.Empty(t, observer.events)

	observer.panic = true
	require.NotPanics(t, func() {
		NewOperationMeasurement(nil, observer).Emit(context.Background(), OperationEvent{}, nil)
	})
}

func TestOperationConstantsPreserveTelemetryValues(t *testing.T) {
	require.Equal(t, map[string]string{
		"success":  OperationOutcomeSuccess,
		"retry":    OperationOutcomeRetry,
		"rejected": OperationOutcomeRejected,
		"failure":  OperationOutcomeFailure,
	}, map[string]string{
		"success":  "success",
		"retry":    "retry",
		"rejected": "rejected",
		"failure":  "failure",
	})
	require.Equal(t, Operation("roster_replace"), OperationRosterReplace)
	require.Equal(t, Operation("preflight_run"), OperationPreflightRun)
	require.Equal(t, Operation("roster_lock"), OperationRosterLock)
	require.Equal(t, Operation("roster_unlock"), OperationRosterUnlock)
	require.Equal(t, Operation("pairing_configure"), OperationPairingConfigure)
	require.Equal(t, Operation("tournament_action"), OperationTournamentAction)
	require.Equal(t, Operation("wave_control"), OperationWaveControl)
	require.Equal(t, Operation("no_show_resolve"), OperationNoShowResolve)
	require.Equal(t, Operation("reserve_assign"), OperationReserveAssign)
	require.Equal(t, Operation("forfeit_record"), OperationForfeitRecord)
	require.Equal(t, Operation("game_replay"), OperationGameReplay)
	require.Equal(t, Operation("result_correct"), OperationResultCorrect)
}
