package game_test

import (
	"errors"
	"math"
	"testing"
	"time"

	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	"github.com/google/uuid"
)

func testPauseResumeSinglePresenceValidation(t *testing.T, decidedAt time.Time) {
	t.Helper()

	t.Run("fails closed on missing stale mismatched ineligible and overflow evidence", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name        string
			preexisting [2]bool
			mutate      func(*gameusecase.PauseResumePresenceAuthority, *gameusecase.PauseResumePresenceCommand)
			now         time.Time
			want        error
		}{
			{name: "missing interval", now: decidedAt, want: gameusecase.ErrInvalidPauseResumePresence, mutate: func(a *gameusecase.PauseResumePresenceAuthority, c *gameusecase.PauseResumePresenceCommand) {
				disconnectPausePresence(&a.Resume.Presence[0], a.Resume.Pause.PausedAt.Add(time.Second), 1)
				refreshPauseResumePresenceExpectation(a, c)
			}},
			{name: "stale decision head", now: decidedAt, want: gameusecase.ErrPauseResumePresenceConflict, mutate: func(_ *gameusecase.PauseResumePresenceAuthority, c *gameusecase.PauseResumePresenceCommand) {
				c.GameExpected.DecisionNumber++
			}},
			{name: "stale Series decision head", now: decidedAt, want: gameusecase.ErrPauseResumePresenceConflict, mutate: func(_ *gameusecase.PauseResumePresenceAuthority, c *gameusecase.PauseResumePresenceCommand) {
				c.SeriesExpected.DecisionNumber++
			}},
			{name: "wrong child scope kind", now: decidedAt, want: gameusecase.ErrInvalidPauseResumePresence, mutate: func(a *gameusecase.PauseResumePresenceAuthority, c *gameusecase.PauseResumePresenceCommand) {
				a.GameDecision.ScopeKind = gameusecase.PauseResumeDecisionScopeSeries
				refreshPauseResumePresenceExpectation(a, c)
			}},
			{name: "terminal child pause", now: decidedAt, want: gameusecase.ErrInvalidPauseResumePresence, mutate: func(a *gameusecase.PauseResumePresenceAuthority, c *gameusecase.PauseResumePresenceCommand) {
				a.GameDecision.State = gameusecase.PauseStateResumed
				refreshPauseResumePresenceExpectation(a, c)
			}},
			{name: "stale child revision identity", now: decidedAt, want: gameusecase.ErrPauseResumePresenceConflict, mutate: func(_ *gameusecase.PauseResumePresenceAuthority, c *gameusecase.PauseResumePresenceCommand) {
				c.GameExpected.CurrentRevisionID = uuid.New()
			}},
			{name: "stale child revision", now: decidedAt, want: gameusecase.ErrPauseResumePresenceConflict, mutate: func(_ *gameusecase.PauseResumePresenceAuthority, c *gameusecase.PauseResumePresenceCommand) {
				c.GameExpected.Revision++
			}},
			{name: "Game decision ID is current revision ID", now: decidedAt, want: gameusecase.ErrInvalidPauseResumePresence, mutate: func(_ *gameusecase.PauseResumePresenceAuthority, c *gameusecase.PauseResumePresenceCommand) {
				c.GameDecisionID = c.GameExpected.CurrentRevisionID
			}},
			{name: "Series decision ID is current revision ID", now: decidedAt, want: gameusecase.ErrInvalidPauseResumePresence, mutate: func(_ *gameusecase.PauseResumePresenceAuthority, c *gameusecase.PauseResumePresenceCommand) {
				c.SeriesDecisionID = c.SeriesExpected.CurrentRevisionID
			}},
			{name: "Game decision ID is Series revision ID", now: decidedAt, want: gameusecase.ErrInvalidPauseResumePresence, mutate: func(_ *gameusecase.PauseResumePresenceAuthority, c *gameusecase.PauseResumePresenceCommand) {
				c.GameDecisionID = c.SeriesExpected.CurrentRevisionID
			}},
			{name: "Series decision ID is Game pause ID", now: decidedAt, want: gameusecase.ErrInvalidPauseResumePresence, mutate: func(_ *gameusecase.PauseResumePresenceAuthority, c *gameusecase.PauseResumePresenceCommand) {
				c.SeriesDecisionID = c.GameExpected.PauseID
			}},
			{name: "stale Game start", now: decidedAt, want: gameusecase.ErrPauseResumePresenceConflict, mutate: func(_ *gameusecase.PauseResumePresenceAuthority, c *gameusecase.PauseResumePresenceCommand) {
				c.GameExpected.StartedAt = c.GameExpected.StartedAt.Add(time.Second)
			}},
			{name: "stale Series start", now: decidedAt, want: gameusecase.ErrPauseResumePresenceConflict, mutate: func(_ *gameusecase.PauseResumePresenceAuthority, c *gameusecase.PauseResumePresenceCommand) {
				c.SeriesExpected.StartedAt = c.SeriesExpected.StartedAt.Add(time.Second)
			}},
			{name: "stale Game clock revision", now: decidedAt, want: gameusecase.ErrPauseResumePresenceConflict, mutate: func(_ *gameusecase.PauseResumePresenceAuthority, c *gameusecase.PauseResumePresenceCommand) {
				c.GameExpected.GameClock.Revision++
			}},
			{name: "stale Game clock value", now: decidedAt, want: gameusecase.ErrPauseResumePresenceConflict, mutate: func(_ *gameusecase.PauseResumePresenceAuthority, c *gameusecase.PauseResumePresenceCommand) {
				c.GameExpected.GameClock.Remaining++
			}},
			{name: "stale frozen deadline value", now: decidedAt, want: gameusecase.ErrPauseResumePresenceConflict, mutate: func(_ *gameusecase.PauseResumePresenceAuthority, c *gameusecase.PauseResumePresenceCommand) {
				c.FrozenDeadlines[0].OriginalDeadline = c.FrozenDeadlines[0].OriginalDeadline.Add(time.Second)
				c.FrozenDeadlines[0].Remaining += time.Second
			}},
			{name: "Game clock belongs to another pause", now: decidedAt, want: gameusecase.ErrInvalidPauseResumePresence, mutate: func(_ *gameusecase.PauseResumePresenceAuthority, c *gameusecase.PauseResumePresenceCommand) {
				c.GameExpected.GameClock.PauseID = uuid.New()
			}},
			{name: "Game clock belongs to another Game", now: decidedAt, want: gameusecase.ErrInvalidPauseResumePresence, mutate: func(_ *gameusecase.PauseResumePresenceAuthority, c *gameusecase.PauseResumePresenceCommand) {
				c.GameExpected.GameClock.GameID = uuid.New()
			}},
			{name: "stale source reconnect revision", preexisting: [2]bool{true, false}, now: decidedAt, want: gameusecase.ErrPauseResumePresenceConflict, mutate: func(_ *gameusecase.PauseResumePresenceAuthority, c *gameusecase.PauseResumePresenceCommand) {
				c.Resume.Expected.Reconnect[0].Revision++
			}},
			{name: "source suspension missing", preexisting: [2]bool{true, false}, now: decidedAt, want: gameusecase.ErrInvalidPauseResumePresence, mutate: func(a *gameusecase.PauseResumePresenceAuthority, _ *gameusecase.PauseResumePresenceCommand) {
				participantID := a.Resume.Pause.Graph.Series[0].Execution.Series.FirstParticipantID
				source := suspendedReconnectForParticipant(t, a.Resume.Pause, participantID)
				reconnectIntervalPointerByID(t, a.Resume.Pause.Graph.Reconnect, source.ID).SuspendedByPauseID = nil
				reconnectIntervalPointerByID(t, a.Resume.Reconnect, source.ID).SuspendedByPauseID = nil
			}},
			{name: "source suspension names another normal pause", preexisting: [2]bool{true, false}, now: decidedAt, want: gameusecase.ErrInvalidPauseResumePresence, mutate: func(a *gameusecase.PauseResumePresenceAuthority, _ *gameusecase.PauseResumePresenceCommand) {
				participantID := a.Resume.Pause.Graph.Series[0].Execution.Series.FirstParticipantID
				source := suspendedReconnectForParticipant(t, a.Resume.Pause, participantID)
				wrong := uuid.New()
				reconnectIntervalPointerByID(t, a.Resume.Pause.Graph.Reconnect, source.ID).SuspendedByPauseID = &wrong
				reconnectIntervalPointerByID(t, a.Resume.Reconnect, source.ID).SuspendedByPauseID = &wrong
			}},
			{name: "stale Presence revision", now: decidedAt, want: gameusecase.ErrPauseResumePresenceConflict, mutate: func(_ *gameusecase.PauseResumePresenceAuthority, c *gameusecase.PauseResumePresenceCommand) {
				c.Resume.Expected.Presence[0].Revision++
			}},
			{name: "stale Presence epoch", now: decidedAt, want: gameusecase.ErrPauseResumePresenceConflict, mutate: func(_ *gameusecase.PauseResumePresenceAuthority, c *gameusecase.PauseResumePresenceCommand) {
				c.Resume.Expected.Presence[0].PresenceEpoch++
			}},
			{name: "stale counter revision", now: decidedAt, want: gameusecase.ErrPauseResumePresenceConflict, mutate: func(_ *gameusecase.PauseResumePresenceAuthority, c *gameusecase.PauseResumePresenceCommand) {
				c.Resume.Expected.Counters[0].Revision++
			}},
			{name: "stale counter Used", now: decidedAt, want: gameusecase.ErrPauseResumePresenceIncomplete, mutate: func(a *gameusecase.PauseResumePresenceAuthority, _ *gameusecase.PauseResumePresenceCommand) {
				a.Resume.Counters[0].Used++
			}},
			{name: "normal pause substituted for child pause", now: decidedAt, want: gameusecase.ErrInvalidPauseResumePresence, mutate: func(_ *gameusecase.PauseResumePresenceAuthority, c *gameusecase.PauseResumePresenceCommand) {
				c.GameExpected.PauseID = c.Resume.PauseID
			}},
			{name: "old Series accidentally reparented to normal Wave pause", now: decidedAt, want: gameusecase.ErrInvalidPauseResumePresence, mutate: func(a *gameusecase.PauseResumePresenceAuthority, c *gameusecase.PauseResumePresenceCommand) {
				a.SeriesDecision.ParentPauseID = uuidPointer(c.Resume.PauseID)
				refreshPauseResumePresenceExpectation(a, c)
			}},
			{name: "wrong old Series Game edge", now: decidedAt, want: gameusecase.ErrInvalidPauseResumePresence, mutate: func(a *gameusecase.PauseResumePresenceAuthority, c *gameusecase.PauseResumePresenceCommand) {
				a.GameDecision.ParentPauseID = uuidPointer(c.Resume.PauseID)
				refreshPauseResumePresenceExpectation(a, c)
			}},
			{name: "wrong Series depth", now: decidedAt, want: gameusecase.ErrInvalidPauseResumePresence, mutate: func(a *gameusecase.PauseResumePresenceAuthority, c *gameusecase.PauseResumePresenceCommand) {
				a.SeriesDecision.Depth++
				refreshPauseResumePresenceExpectation(a, c)
			}},
			{name: "wrong Game depth", now: decidedAt, want: gameusecase.ErrInvalidPauseResumePresence, mutate: func(a *gameusecase.PauseResumePresenceAuthority, c *gameusecase.PauseResumePresenceCommand) {
				a.GameDecision.Depth++
				refreshPauseResumePresenceExpectation(a, c)
			}},
			{name: "exhausted fresh slot", now: decidedAt, want: gameusecase.ErrPauseResumePresenceIneligible, mutate: func(a *gameusecase.PauseResumePresenceAuthority, c *gameusecase.PauseResumePresenceCommand) {
				presence := &a.Resume.Presence[0]
				disconnectPausePresence(presence, a.Resume.Pause.PausedAt.Add(time.Second), 1)
				counter := reconnectCounterForParticipant(t, a.Resume.Counters, presence.ParticipantID)
				counter.Limit = counter.Used
				refreshPauseResumePresenceExpectation(a, c)
				c.FirstInterval = &gameusecase.PauseResumeIntervalInput{ParticipantID: presence.ParticipantID, IntervalID: uuid.New(), Window: time.Minute}
			}},
			{name: "fresh deadline overflow", now: time.Unix(0, math.MaxInt64-int64(30*time.Second)).UTC(), want: gameusecase.ErrPauseResumePresenceOverflow, mutate: func(a *gameusecase.PauseResumePresenceAuthority, c *gameusecase.PauseResumePresenceCommand) {
				disconnectPausePresence(&a.Resume.Presence[0], a.Resume.Pause.PausedAt.Add(time.Second), 1)
				refreshPauseResumePresenceExpectation(a, c)
				c.FirstInterval = &gameusecase.PauseResumeIntervalInput{ParticipantID: a.Resume.Presence[0].ParticipantID, IntervalID: uuid.New(), Window: time.Minute}
			}},
			{name: "decision number overflow", now: decidedAt, want: gameusecase.ErrPauseResumePresenceOverflow, mutate: func(a *gameusecase.PauseResumePresenceAuthority, c *gameusecase.PauseResumePresenceCommand) {
				a.GameDecision.DecisionNumber = math.MaxInt64
				refreshPauseResumePresenceExpectation(a, c)
			}},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				authority, command := pauseResumePresenceFixture(t, decidedAt, test.preexisting)
				test.mutate(&authority, &command)
				repository := newPauseResumePresenceRepositoryHarness(t, authority)
				_, changed, err := gameusecase.NewPauseResumePresenceUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, test.now)).Resume(t.Context(), command)
				if !errors.Is(err, test.want) || changed || repository.writeCount() != 0 {
					t.Fatalf("Resume() error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
				}
			})
		}
	})
}
