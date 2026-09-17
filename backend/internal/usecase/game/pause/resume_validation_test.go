package pause_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause"
	"github.com/google/uuid"
)

func testPauseResumeValidation(t *testing.T, resumedAt time.Time) {
	t.Helper()

	t.Run("requires complete connected current evidence", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			mutate func(*gameusecase.PauseResumeAuthority, *gameusecase.PauseResumeCommand)
			want   error
		}{
			{name: "stale Game revision", mutate: func(_ *gameusecase.PauseResumeAuthority, c *gameusecase.PauseResumeCommand) {
				c.Expected.Games[0].Revision++
			}, want: gameusecase.ErrPauseResumeConflict},
			{name: "stale graph revision", mutate: func(_ *gameusecase.PauseResumeAuthority, c *gameusecase.PauseResumeCommand) {
				c.Expected.GraphRevision++
			}, want: gameusecase.ErrPauseResumeConflict},
			{name: "stale pause revision", mutate: func(_ *gameusecase.PauseResumeAuthority, c *gameusecase.PauseResumeCommand) {
				c.Expected.PauseRevision++
			}, want: gameusecase.ErrPauseResumeConflict},
			{name: "wrong pause identity", mutate: func(_ *gameusecase.PauseResumeAuthority, c *gameusecase.PauseResumeCommand) {
				c.Expected.PauseID = uuid.New()
			}, want: gameusecase.ErrInvalidPauseResume},
			{name: "stale Tournament revision", mutate: func(_ *gameusecase.PauseResumeAuthority, c *gameusecase.PauseResumeCommand) {
				c.Expected.TournamentRevision++
			}, want: gameusecase.ErrPauseResumeConflict},
			{name: "wrong Tournament resume state", mutate: func(_ *gameusecase.PauseResumeAuthority, c *gameusecase.PauseResumeCommand) {
				c.Expected.TournamentState = domain.TournamentStatePlayoffs
			}, want: gameusecase.ErrPauseResumeConflict},
			{name: "stale Wave revision", mutate: func(_ *gameusecase.PauseResumeAuthority, c *gameusecase.PauseResumeCommand) {
				c.Expected.WaveRevision++
			}, want: gameusecase.ErrPauseResumeConflict},
			{name: "stale Series revision", mutate: func(_ *gameusecase.PauseResumeAuthority, c *gameusecase.PauseResumeCommand) {
				c.Expected.Series[0].Revision++
			}, want: gameusecase.ErrPauseResumeConflict},
			{name: "stale Presence epoch", mutate: func(_ *gameusecase.PauseResumeAuthority, c *gameusecase.PauseResumeCommand) {
				c.Expected.Presence[0].PresenceEpoch++
			}, want: gameusecase.ErrPauseResumeConflict},
			{name: "incomplete expected Presence identity", mutate: func(_ *gameusecase.PauseResumeAuthority, c *gameusecase.PauseResumeCommand) {
				c.Expected.Presence[0].ID = uuid.Nil
			}, want: gameusecase.ErrInvalidPauseResume},
			{name: "stale Reconnect revision", mutate: func(_ *gameusecase.PauseResumeAuthority, c *gameusecase.PauseResumeCommand) {
				c.Expected.Reconnect[0].Revision++
			}, want: gameusecase.ErrPauseResumeConflict},
			{name: "stale counter revision", mutate: func(_ *gameusecase.PauseResumeAuthority, c *gameusecase.PauseResumeCommand) {
				c.Expected.Counters[0].Revision++
			}, want: gameusecase.ErrPauseResumeConflict},
			{name: "stale frozen deadline revision", mutate: func(_ *gameusecase.PauseResumeAuthority, c *gameusecase.PauseResumeCommand) {
				c.Expected.FrozenDeadlines[0].Revision++
			}, want: gameusecase.ErrPauseResumeConflict},
			{name: "stale terminal action revision", mutate: func(_ *gameusecase.PauseResumeAuthority, c *gameusecase.PauseResumeCommand) {
				c.Expected.TerminalActionRevision++
			}, want: gameusecase.ErrPauseResumeConflict},
			{name: "missing participant", mutate: func(a *gameusecase.PauseResumeAuthority, _ *gameusecase.PauseResumeCommand) {
				a.Presence = a.Presence[:1]
			}, want: gameusecase.ErrPauseResumeIncomplete},
			{name: "foreign Presence identity", mutate: func(a *gameusecase.PauseResumeAuthority, _ *gameusecase.PauseResumeCommand) {
				a.Presence[0].ID = uuid.New()
			}, want: gameusecase.ErrPauseResumeIncomplete},
			{name: "foreign Presence Series", mutate: func(a *gameusecase.PauseResumeAuthority, _ *gameusecase.PauseResumeCommand) {
				a.Presence[0].SeriesID = uuid.New()
			}, want: gameusecase.ErrPauseResumeIncomplete},
			{name: "disconnected participant", mutate: func(a *gameusecase.PauseResumeAuthority, c *gameusecase.PauseResumeCommand) {
				a.Presence[0].State = pausedomain.PresenceStateDisconnected
				at := resumedAt.Add(-time.Second)
				a.Presence[0].DisconnectedAt = &at
				a.Presence[0].UpdatedAt = at
				a.Presence[0].PresenceEpoch++
				a.Presence[0].Revision++
				c.Expected = gameusecase.PauseResumeExpectationFrom(*a)
			}, want: gameusecase.ErrPauseResumePresence},
			{name: "open reconnect interval", mutate: func(a *gameusecase.PauseResumeAuthority, c *gameusecase.PauseResumeCommand) {
				a.Reconnect[0].State = pausedomain.ReconnectStateOpen
				a.Reconnect[0].ClosedAt = nil
				a.Reconnect[0].Deadline = resumedAt.Add(time.Minute)
				a.Pause.Graph.Reconnect[0] = a.Reconnect[0]
				c.Expected = gameusecase.PauseResumeExpectationFrom(*a)
			}, want: gameusecase.ErrPauseResumePresence},
			{name: "terminal Reconnect update precedes its close", mutate: func(a *gameusecase.PauseResumeAuthority, c *gameusecase.PauseResumeCommand) {
				updatedAt := a.Reconnect[0].ClosedAt.Add(-time.Second)
				a.Reconnect[0].UpdatedAt = updatedAt
				a.Pause.Graph.Reconnect[0].UpdatedAt = updatedAt
				c.Expected = gameusecase.PauseResumeExpectationFrom(*a)
			}, want: gameusecase.ErrInvalidPauseResume},
			{name: "missing terminal Reconnect row", mutate: func(a *gameusecase.PauseResumeAuthority, c *gameusecase.PauseResumeCommand) {
				a.Reconnect = nil
				c.Expected = gameusecase.PauseResumeExpectationFrom(*a)
			}, want: gameusecase.ErrPauseResumeIncomplete},
			{name: "extra Reconnect row", mutate: func(a *gameusecase.PauseResumeAuthority, c *gameusecase.PauseResumeCommand) {
				extra := a.Reconnect[0]
				extra.ID = uuid.New()
				a.Reconnect = append(a.Reconnect, extra)
				c.Expected = gameusecase.PauseResumeExpectationFrom(*a)
			}, want: gameusecase.ErrPauseResumeIncomplete},
			{name: "duplicate Reconnect row", mutate: func(a *gameusecase.PauseResumeAuthority, c *gameusecase.PauseResumeCommand) {
				a.Reconnect = append(a.Reconnect, a.Reconnect[0])
				c.Expected = gameusecase.PauseResumeExpectationFrom(*a)
			}, want: gameusecase.ErrInvalidPauseResume},
			{name: "foreign Reconnect owner", mutate: func(a *gameusecase.PauseResumeAuthority, c *gameusecase.PauseResumeCommand) {
				a.Reconnect[0].ParticipantID = uuid.New()
				c.Expected = gameusecase.PauseResumeExpectationFrom(*a)
			}, want: gameusecase.ErrPauseResumeIncomplete},
			{name: "missing frozen deadline", mutate: func(a *gameusecase.PauseResumeAuthority, c *gameusecase.PauseResumeCommand) {
				a.FrozenDeadlines = nil
				c.Expected = gameusecase.PauseResumeExpectationFrom(*a)
			}, want: gameusecase.ErrPauseResumeIncomplete},
			{name: "frozen deadline revision overflow", mutate: func(a *gameusecase.PauseResumeAuthority, c *gameusecase.PauseResumeCommand) {
				a.Pause.Graph.FrozenDeadlines[0].Revision = math.MaxInt64
				a.FrozenDeadlines[0].Revision = math.MaxInt64
				c.Expected = gameusecase.PauseResumeExpectationFrom(*a)
			}, want: gameusecase.ErrPauseResumeOverflow},
			{name: "Game revision overflow", mutate: func(a *gameusecase.PauseResumeAuthority, c *gameusecase.PauseResumeCommand) {
				a.Pause.Expected.Games[0].Revision = math.MaxInt64 - 1
				a.Pause.Graph.Games[0].Revision = math.MaxInt64
				c.Expected = gameusecase.PauseResumeExpectationFrom(*a)
			}, want: gameusecase.ErrPauseResumeOverflow},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()
				authority, command := pauseResumeFixture(t, resumedAt)
				test.mutate(&authority, &command)
				repository := newPauseResumeRepositoryHarness(t, authority)
				useCase := gameusecase.NewPauseResumeUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, resumedAt))
				if _, changed, err := useCase.Resume(t.Context(), command); !errors.Is(err, test.want) || changed || repository.writeCount() != 0 {
					t.Fatalf("Resume() error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
				}
			})
		}
	})

	t.Run("rejects shifted deadline Unix-nanosecond overflow", func(t *testing.T) {
		t.Parallel()

		boundary := time.Unix(0, math.MaxInt64-int64(30*time.Second)).UTC()
		authority, command := pauseResumeFixture(t, boundary)
		repository := newPauseResumeRepositoryHarness(t, authority)
		useCase := gameusecase.NewPauseResumeUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, boundary))
		if _, changed, err := useCase.Resume(t.Context(), command); !errors.Is(err, gameusecase.ErrPauseResumeOverflow) || changed || repository.writeCount() != 0 {
			t.Fatalf("Resume() error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
	})

	t.Run("rejects resume time before durable history", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			mutate func(*gameusecase.PauseResumeAuthority)
			now    func(gameusecase.PauseResumeAuthority) time.Time
		}{
			{name: "active pause and Tournament", now: func(a gameusecase.PauseResumeAuthority) time.Time {
				return a.Pause.Graph.Tournament.UpdatedAt.Add(-time.Second)
			}},
			{name: "Reconnect update", mutate: func(a *gameusecase.PauseResumeAuthority) {
				future := resumedAt.Add(time.Second)
				a.Reconnect[0].ClosedAt = &future
				a.Reconnect[0].UpdatedAt = future
				a.Pause.Graph.Reconnect[0] = a.Reconnect[0]
			}, now: func(gameusecase.PauseResumeAuthority) time.Time { return resumedAt }},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()
				authority, command := pauseResumeFixture(t, resumedAt)
				if test.mutate != nil {
					test.mutate(&authority)
					command.Expected = gameusecase.PauseResumeExpectationFrom(authority)
				}
				repository := newPauseResumeRepositoryHarness(t, authority)
				useCase := gameusecase.NewPauseResumeUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, test.now(authority)))
				if _, changed, err := useCase.Resume(t.Context(), command); !errors.Is(err, gameusecase.ErrInvalidPauseResume) || changed || repository.writeCount() != 0 {
					t.Fatalf("Resume() error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
				}
			})
		}
	})
}
