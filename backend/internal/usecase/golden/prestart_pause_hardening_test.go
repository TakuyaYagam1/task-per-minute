package golden_test

import (
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden"

	"github.com/stretchr/testify/require"
)

func TestRetainedGoldenPrestartPauseHardening(t *testing.T) {
	t.Parallel()

	openedAt := time.Date(2026, 9, 1, 21, 30, 0, 0, time.UTC)
	execution, authorization, pause := task050PrestartFixture(t, openedAt, 28100)
	pausedAt := openedAt.Add(5 * time.Second)

	t.Run("resume time cannot precede pause", func(t *testing.T) {
		repository := newTask050RetainedPrestartRepositoryHarness(t, execution, authorization)
		paused, changed, err := goldenusecase.NewRetainedGoldenPrestartPauseUseCase(
			repository, prestartNewGoldenClock(t, pausedAt),
		).Pause(t.Context(), pause)
		require.NoError(t, err)
		require.True(t, changed)
		resume := task050ResumeCommand(*paused, authorization, pause.ActorID, 28200)
		beforeWrites := repository.writeCount()
		result, resultChanged, resultErr := goldenusecase.NewRetainedGoldenPrestartPauseUseCase(
			repository, prestartNewGoldenClock(t, pausedAt.Add(-time.Nanosecond)),
		).Resume(t.Context(), resume)
		require.Nil(t, result)
		require.False(t, resultChanged)
		require.Error(t, resultErr)
		require.Equal(t, beforeWrites, repository.writeCount())
		require.Nil(t, repository.liveSnapshot())
	})

	t.Run("stale expected session is no write", func(t *testing.T) {
		repository := newTask050RetainedPrestartRepositoryHarness(t, execution, authorization)
		paused, _, err := goldenusecase.NewRetainedGoldenPrestartPauseUseCase(
			repository, prestartNewGoldenClock(t, pausedAt),
		).Pause(t.Context(), pause)
		require.NoError(t, err)
		resume := task050ResumeCommand(*paused, authorization, pause.ActorID, 28300)
		resume.ExpectedSession.RevisionID = prestartTask049ID(28399)
		beforeWrites := repository.writeCount()
		result, changed, resumeErr := goldenusecase.NewRetainedGoldenPrestartPauseUseCase(
			repository, prestartNewGoldenClock(t, pausedAt.Add(time.Second)),
		).Resume(t.Context(), resume)
		require.Nil(t, result)
		require.False(t, changed)
		require.ErrorIs(t, resumeErr, goldenusecase.ErrGoldenPrestartAuthorityConflict)
		require.Equal(t, beforeWrites, repository.writeCount())
		require.Nil(t, repository.liveSnapshot())
	})

	t.Run("concurrent identical resume", func(t *testing.T) {
		repository := newTask050RetainedPrestartRepositoryHarness(t, execution, authorization)
		paused, _, err := goldenusecase.NewRetainedGoldenPrestartPauseUseCase(
			repository, prestartNewGoldenClock(t, pausedAt),
		).Pause(t.Context(), pause)
		require.NoError(t, err)
		resume := task050ResumeCommand(*paused, authorization, pause.ActorID, 28400)
		repository.beforeCommit = prestartTask049TwoPartyBarrier(t)
		type result struct {
			record  *goldenusecase.RetainedGoldenPrestartRecord
			changed bool
			err     error
		}
		results := make(chan result, 2)
		for range 2 {
			go func() {
				record, changed, err := goldenusecase.NewRetainedGoldenPrestartPauseUseCase(
					repository, prestartNewGoldenClock(t, pausedAt.Add(time.Second)),
				).Resume(t.Context(), resume)
				results <- result{record: record, changed: changed, err: err}
			}()
		}
		changedCount := 0
		for range 2 {
			value := <-results
			require.NoError(t, value.err)
			require.NotNil(t, value.record)
			if value.changed {
				changedCount++
			}
		}
		require.Equal(t, 1, changedCount)
	})

	t.Run("dishonest unchanged return", func(t *testing.T) {
		source := newTask050RetainedPrestartRepositoryHarness(t, execution, authorization)
		oldRecord, changed, err := goldenusecase.NewRetainedGoldenPrestartPauseUseCase(
			source, prestartNewGoldenClock(t, pausedAt),
		).Pause(t.Context(), pause)
		require.NoError(t, err)
		require.True(t, changed)

		repository := newTask050RetainedPrestartRepositoryHarness(t, execution, authorization)
		repository.returnRecord = oldRecord
		other := pause
		other.CommandID = prestartTask049ID(28500)
		other.SessionID = prestartTask049ID(28501)
		other.NextSessionRevisionID = prestartTask049ID(28502)
		result, resultChanged, resultErr := goldenusecase.NewRetainedGoldenPrestartPauseUseCase(
			repository, prestartNewGoldenClock(t, pausedAt),
		).Pause(t.Context(), other)
		require.Nil(t, result)
		require.False(t, resultChanged)
		require.ErrorIs(t, resultErr, domain.ErrInternal)
		require.Equal(t, 0, repository.writeCount())
	})

	t.Run("unchanged concurrent winner may have another captured time", func(t *testing.T) {
		source := newTask050RetainedPrestartRepositoryHarness(t, execution, authorization)
		winner, changed, err := goldenusecase.NewRetainedGoldenPrestartPauseUseCase(
			source, prestartNewGoldenClock(t, pausedAt),
		).Pause(t.Context(), pause)
		require.NoError(t, err)
		require.True(t, changed)

		repository := newTask050RetainedPrestartRepositoryHarness(t, execution, authorization)
		repository.returnRecord = winner
		result, resultChanged, resultErr := goldenusecase.NewRetainedGoldenPrestartPauseUseCase(
			repository, prestartNewGoldenClock(t, pausedAt.Add(time.Nanosecond)),
		).Pause(t.Context(), pause)
		require.NoError(t, resultErr)
		require.False(t, resultChanged)
		require.Equal(t, winner.PayloadDigest, result.PayloadDigest)
		require.Equal(t, 0, repository.writeCount())
	})

	t.Run("authority generation combinations", func(t *testing.T) {
		repository := newTask050RetainedPrestartRepositoryHarness(t, execution, authorization)
		paused, _, err := goldenusecase.NewRetainedGoldenPrestartPauseUseCase(
			repository, prestartNewGoldenClock(t, pausedAt),
		).Pause(t.Context(), pause)
		require.NoError(t, err)
		pausedAuthority, loadErr := repository.LoadRetainedGoldenPrestartAuthority(t.Context(), execution.Scope)
		require.NoError(t, loadErr)
		require.NoError(t, pausedAuthority.Validate())
		live := execution.Snapshot()
		pausedAuthority.Execution = &live
		require.Error(t, pausedAuthority.Validate())

		resume := task050ResumeCommand(*paused, authorization, pause.ActorID, 28520)
		_, _, resumeErr := goldenusecase.NewRetainedGoldenPrestartPauseUseCase(
			repository, prestartNewGoldenClock(t, pausedAt.Add(time.Second)),
		).Resume(t.Context(), resume)
		require.NoError(t, resumeErr)
		readyAuthority, readyLoadErr := repository.LoadRetainedGoldenPrestartAuthority(t.Context(), execution.Scope)
		require.NoError(t, readyLoadErr)
		require.NoError(t, readyAuthority.Validate())
		readyAuthority.ArchivedExecution = nil
		require.Error(t, readyAuthority.Validate())
	})

	pauseAliasCases := []struct {
		name   string
		mutate func(*goldenusecase.RetainedGoldenPrestartPauseCommand)
	}{
		{name: "command actor", mutate: func(command *goldenusecase.RetainedGoldenPrestartPauseCommand) { command.CommandID = command.ActorID }},
		{name: "session actor", mutate: func(command *goldenusecase.RetainedGoldenPrestartPauseCommand) { command.SessionID = command.ActorID }},
		{name: "revision authorization", mutate: func(command *goldenusecase.RetainedGoldenPrestartPauseCommand) {
			command.NextSessionRevisionID = command.ExpectedAuthorization.RevisionID
		}},
	}
	for _, test := range pauseAliasCases {
		t.Run("pause alias "+test.name, func(t *testing.T) {
			repository := newTask050RetainedPrestartRepositoryHarness(t, execution, authorization)
			command := pause
			test.mutate(&command)
			result, changed, err := goldenusecase.NewRetainedGoldenPrestartPauseUseCase(
				repository, prestartNewGoldenClock(t, pausedAt),
			).Pause(t.Context(), command)
			require.Nil(t, result)
			require.False(t, changed)
			require.Error(t, err)
			require.Equal(t, 0, repository.writeCount())
		})
	}

	resumeAliasCases := []struct {
		name   string
		mutate func(*goldenusecase.RetainedGoldenPrestartResumeCommand, goldenusecase.GoldenWaveExecution)
	}{
		{name: "command actor", mutate: func(command *goldenusecase.RetainedGoldenPrestartResumeCommand, _ goldenusecase.GoldenWaveExecution) {
			command.CommandID = command.ActorID
		}},
		{name: "revision authorization", mutate: func(command *goldenusecase.RetainedGoldenPrestartResumeCommand, _ goldenusecase.GoldenWaveExecution) {
			command.NextSessionRevisionID = command.ExpectedAuthorization.RevisionID
		}},
		{name: "execution scope", mutate: func(command *goldenusecase.RetainedGoldenPrestartResumeCommand, execution goldenusecase.GoldenWaveExecution) {
			command.NextExecutionRevisionID = execution.Scope.GroupID
		}},
		{name: "window participant", mutate: func(command *goldenusecase.RetainedGoldenPrestartResumeCommand, execution goldenusecase.GoldenWaveExecution) {
			command.NextWindowID = execution.Membership.ParticipantIDs[0]
		}},
	}
	for index, test := range resumeAliasCases {
		t.Run("resume alias "+test.name, func(t *testing.T) {
			repository := newTask050RetainedPrestartRepositoryHarness(t, execution, authorization)
			paused, _, err := goldenusecase.NewRetainedGoldenPrestartPauseUseCase(
				repository, prestartNewGoldenClock(t, pausedAt),
			).Pause(t.Context(), pause)
			require.NoError(t, err)
			command := task050ResumeCommand(*paused, authorization, pause.ActorID, 28600+index*20)
			test.mutate(&command, execution)
			beforeWrites := repository.writeCount()
			result, changed, resumeErr := goldenusecase.NewRetainedGoldenPrestartPauseUseCase(
				repository, prestartNewGoldenClock(t, pausedAt.Add(time.Second)),
			).Resume(t.Context(), command)
			require.Nil(t, result)
			require.False(t, changed)
			require.Error(t, resumeErr)
			require.Equal(t, beforeWrites, repository.writeCount())
			require.Nil(t, repository.liveSnapshot())
		})
	}
}
