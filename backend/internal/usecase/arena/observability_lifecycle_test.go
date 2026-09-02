package arena_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestArenaLifecycleAndWaveLogging(t *testing.T) {
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)

	t.Run("lifecycle success preserves the business timestamp", func(t *testing.T) {
		tournamentID := uuid.MustParse("72000000-0000-0000-0000-000000000001")
		repository := newLifecycleRepositoryFake(lifecycleTournamentRecord(
			tournamentID,
			domain.ArenaTournamentStateRosterLocked,
			3,
			now,
		))
		clock := &sequenceArenaClock{times: []time.Time{now.Add(-2 * time.Second), now, now.Add(3 * time.Second)}}
		capture := &arenaLifecycleEventCapture{}
		unused := &arenaLifecycleEventCapture{}

		record, changed, err := arena.NewTournamentLifecycleUseCase(
			repository,
			clock,
			capture,
			unused,
		).Transition(t.Context(), arena.TournamentLifecycleCommand{
			TournamentID: tournamentID, ExpectedRevision: 3,
			NextState: domain.ArenaTournamentStateSwiss,
		})

		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, now, record.UpdatedAt)
		require.Equal(t, 3, clock.count())
		require.Empty(t, unused.snapshot())
		require.Equal(t, observability.ArenaEvent{
			Event: "arena.lifecycle.transition", Outcome: observability.ArenaOutcomeSuccess,
			CorrelationID: tournamentID.String(), TournamentID: tournamentID.String(),
			EntityKind: "tournament", EntityID: tournamentID.String(), Stage: "tournament_lifecycle",
			Transition: "roster_locked_to_swiss", Duration: 5 * time.Second,
			ReasonCode: "transitioned", Revision: 4,
		}, capture.one(t))
	})

	t.Run("lifecycle replay emits one idempotent result", func(t *testing.T) {
		tournamentID := uuid.MustParse("72000000-0000-0000-0000-000000000002")
		repository := newLifecycleRepositoryFake(lifecycleTournamentRecord(
			tournamentID,
			domain.ArenaTournamentStateSwiss,
			4,
			now,
		))
		capture := &arenaLifecycleEventCapture{}

		record, changed, err := arena.NewTournamentLifecycleUseCase(
			repository,
			fixedArenaClock{now: now},
			capture,
		).Transition(t.Context(), arena.TournamentLifecycleCommand{
			TournamentID: tournamentID, ExpectedRevision: 3,
			NextState: domain.ArenaTournamentStateSwiss,
		})

		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, int64(4), record.Revision)
		event := capture.one(t)
		require.Equal(t, observability.ArenaOutcomeSuccess, event.Outcome)
		require.Equal(t, "idempotent_replay", event.ReasonCode)
		require.Equal(t, "swiss_to_swiss", event.Transition)
		require.Equal(t, int64(4), event.Revision)
		require.GreaterOrEqual(t, event.Duration, time.Duration(0))
	})

	t.Run("lifecycle rejection and failure use safe reasons", func(t *testing.T) {
		tournamentID := uuid.MustParse("72000000-0000-0000-0000-000000000003")
		rejected := &arenaLifecycleEventCapture{}
		_, changed, err := arena.NewTournamentLifecycleUseCase(
			newLifecycleRepositoryFake(lifecycleTournamentRecord(
				tournamentID,
				domain.ArenaTournamentStateSwiss,
				2,
				now,
			)),
			fixedArenaClock{now: now},
			rejected,
		).Transition(t.Context(), arena.TournamentLifecycleCommand{
			TournamentID: tournamentID, ExpectedRevision: 2,
			NextState: domain.ArenaTournamentStateTechnicalPause,
		})
		require.ErrorIs(t, err, arena.ErrTournamentGuardedTransition)
		require.False(t, changed)
		require.Equal(t, observability.ArenaOutcomeRejected, rejected.one(t).Outcome)
		require.Equal(t, "guarded_transition", rejected.one(t).ReasonCode)

		privateError := errors.New("credential=request-body flag=private-snapshot")
		failed := &arenaLifecycleEventCapture{}
		_, changed, err = arena.NewTournamentLifecycleUseCase(
			lifecycleLoggingFailureRepository{err: privateError},
			fixedArenaClock{now: now},
			failed,
		).Transition(t.Context(), arena.TournamentLifecycleCommand{
			TournamentID: tournamentID, ExpectedRevision: 2,
			NextState: domain.ArenaTournamentStatePlayoffs,
		})
		require.ErrorIs(t, err, privateError)
		require.False(t, changed)
		failureEvent := failed.one(t)
		require.Equal(t, observability.ArenaOutcomeFailure, failureEvent.Outcome)
		require.Equal(t, "repository_lookup_failed", failureEvent.ReasonCode)
		require.NotContains(t, fmt.Sprint(failureEvent), privateError.Error())
	})

	t.Run("Wave success after a conflict emits one retry result", func(t *testing.T) {
		authority, command := waveStartFixture(t, now)
		repository := &waveStartLoggingRetryRepository{
			delegate: &waveStartRepositoryFake{authority: authority},
		}
		clock := &sequenceArenaClock{times: []time.Time{now.Add(-2 * time.Second), now, now.Add(3 * time.Second)}}
		capture := &arenaLifecycleEventCapture{}

		record, changed, err := arena.NewWaveStartUseCase(repository, clock, capture).
			Start(t.Context(), command)

		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, now, record.StartedAt)
		require.Equal(t, 3, clock.count())
		require.Equal(t, observability.ArenaEvent{
			Event: "arena.command.wave_start", Outcome: observability.ArenaOutcomeRetry,
			CorrelationID: command.CommandID.String(), CommandID: command.CommandID.String(),
			TournamentID: command.Scope.TournamentID.String(), EntityKind: "wave",
			EntityID: command.Scope.WaveID.String(), Stage: "wave_start",
			Transition: "ready_to_active", Duration: 5 * time.Second,
			ReasonCode: "conflict_retried", Revision: authority.Revision,
		}, capture.one(t))
	})

	t.Run("Wave replay emits one idempotent result", func(t *testing.T) {
		authority, command := waveStartFixture(t, now)
		repository := &waveStartRepositoryFake{authority: authority}
		_, changed, err := arena.NewWaveStartUseCase(repository, fixedArenaClock{now: now}).
			Start(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)

		capture := &arenaLifecycleEventCapture{}
		clock := &sequenceArenaClock{times: []time.Time{now, now, now.Add(-time.Second)}}
		record, changed, err := arena.NewWaveStartUseCase(
			repository,
			clock,
			capture,
		).Start(t.Context(), command)

		require.NoError(t, err)
		require.False(t, changed)
		event := capture.one(t)
		require.Equal(t, observability.ArenaOutcomeSuccess, event.Outcome)
		require.Equal(t, "idempotent_replay", event.ReasonCode)
		require.Equal(t, command.CommandID.String(), event.CorrelationID)
		require.Equal(t, record.ExpectedAuthorityRevision, event.Revision)
		require.Zero(t, event.Duration)
		require.Equal(t, 3, clock.count())
	})

	t.Run("Wave rejection and failure use safe reasons", func(t *testing.T) {
		authority, command := waveStartFixture(t, now)
		authority.Revisions.PlanRevision++
		rejected := &arenaLifecycleEventCapture{}
		_, changed, err := arena.NewWaveStartUseCase(
			&waveStartRepositoryFake{authority: authority},
			fixedArenaClock{now: now},
			rejected,
		).Start(t.Context(), command)
		require.ErrorIs(t, err, arena.ErrWaveStartAuthorityConflict)
		require.False(t, changed)
		require.Equal(t, observability.ArenaOutcomeRejected, rejected.one(t).Outcome)
		require.Equal(t, "authority_conflict", rejected.one(t).ReasonCode)

		privateError := errors.New("credential=request-body flag=private-snapshot")
		failed := &arenaLifecycleEventCapture{}
		_, changed, err = arena.NewWaveStartUseCase(
			waveStartLoggingFailureRepository{err: privateError},
			fixedArenaClock{now: now},
			failed,
		).Start(t.Context(), command)
		require.ErrorIs(t, err, privateError)
		require.False(t, changed)
		failureEvent := failed.one(t)
		require.Equal(t, observability.ArenaOutcomeFailure, failureEvent.Outcome)
		require.Equal(t, "authority_lookup_failed", failureEvent.ReasonCode)
		require.NotContains(t, fmt.Sprint(failureEvent), privateError.Error())
	})
}

type arenaLifecycleEventCapture struct {
	mu     sync.Mutex
	events []observability.ArenaEvent
}

func (c *arenaLifecycleEventCapture) ObserveArenaEvent(_ context.Context, event observability.ArenaEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, event)
}

func (c *arenaLifecycleEventCapture) snapshot() []observability.ArenaEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]observability.ArenaEvent(nil), c.events...)
}

func (c *arenaLifecycleEventCapture) one(t *testing.T) observability.ArenaEvent {
	t.Helper()
	events := c.snapshot()
	require.Len(t, events, 1)
	return events[0]
}

type lifecycleLoggingFailureRepository struct {
	err error
}

func (r lifecycleLoggingFailureRepository) GetTournament(context.Context, uuid.UUID) (*arena.TournamentRecord, error) {
	return nil, r.err
}

func (r lifecycleLoggingFailureRepository) TransitionTournament(
	context.Context,
	arena.TournamentLifecycleTransitionInput,
) (*arena.TournamentRecord, bool, error) {
	return nil, false, r.err
}

type waveStartLoggingRetryRepository struct {
	delegate *waveStartRepositoryFake
	conflict bool
}

func (r *waveStartLoggingRetryRepository) LoadWaveStartAuthority(
	ctx context.Context,
	scope arena.WaveStartScope,
) (arena.WaveStartAuthority, error) {
	return r.delegate.LoadWaveStartAuthority(ctx, scope)
}

func (r *waveStartLoggingRetryRepository) CommitWaveStart(
	ctx context.Context,
	record arena.WaveStartRecord,
) (*arena.WaveStartRecord, bool, error) {
	if !r.conflict {
		r.conflict = true
		return nil, false, domain.ErrConflict
	}
	return r.delegate.CommitWaveStart(ctx, record)
}

type waveStartLoggingFailureRepository struct {
	err error
}

func (r waveStartLoggingFailureRepository) LoadWaveStartAuthority(
	context.Context,
	arena.WaveStartScope,
) (arena.WaveStartAuthority, error) {
	return arena.WaveStartAuthority{}, r.err
}

func (r waveStartLoggingFailureRepository) CommitWaveStart(
	context.Context,
	arena.WaveStartRecord,
) (*arena.WaveStartRecord, bool, error) {
	return nil, false, r.err
}
