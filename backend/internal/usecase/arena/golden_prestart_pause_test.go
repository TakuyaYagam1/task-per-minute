package arena_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestRetainedGoldenPrestartPause(t *testing.T) {
	t.Parallel()

	openedAt := time.Date(2026, 9, 1, 21, 0, 0, 0, time.UTC)
	state := task048GoldenState(t, openedAt)
	waveRepository := &goldenWaveRepositoryFake{state: state}
	execution := task048OpenGoldenExecution(t, waveRepository, state, openedAt, 24000)
	participantID := execution.Membership.ParticipantIDs[0]
	ready, changed, err := arena.NewGoldenReadinessUseCase(
		waveRepository,
		fixedArenaClock{now: openedAt.Add(time.Second)},
	).MarkReady(t.Context(), task048ReadyCommand(execution, participantID, 24100))
	require.NoError(t, err)
	require.True(t, changed)
	execution = *ready
	actorID := task049ID(24190)
	authorization, err := arena.NewGoldenPrestartOperatorAuthorization(
		execution.Scope.TournamentID, actorID, task049ID(24191), 1,
	)
	require.NoError(t, err)
	repository := newTask050RetainedPrestartRepository(execution, authorization)
	pause := arena.RetainedGoldenPrestartPauseCommand{
		Scope: execution.Scope, CommandID: task049ID(24200), SessionID: task049ID(24201),
		ActorID: actorID, Reason: arena.GoldenPrestartPauseOperatorManual,
		ExpectedExecution: execution.Expectation(), ExpectedAuthorization: authorization.Expectation(),
		NextSessionRevisionID: task049ID(24202),
	}

	paused, changed, err := arena.NewRetainedGoldenPrestartPauseUseCase(
		repository,
		fixedArenaClock{now: openedAt.Add(5 * time.Second)},
	).Pause(t.Context(), pause)
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, paused.Validate())
	require.Equal(t, arena.RetainedGoldenPrestartPaused, paused.State)
	require.Equal(t, execution.Attempt.ID, paused.Attempt.ID)
	require.Equal(t, execution.Wave.ID, paused.WaveID)
	require.Equal(t, execution.Assignment.ID, paused.Assignment.ID)
	require.Equal(t, execution.Assignment.PayloadDigest, paused.Assignment.ExecutionPayloadDigest)
	require.Equal(t, execution.Assignment.Private, paused.Assignment.Private)
	require.Equal(t, execution.Membership, paused.Membership)
	require.Equal(t, execution.Window.ID, paused.SupersededWindow.ID)
	require.Equal(t, execution.Window.ReadyParticipantIDs, paused.SupersededWindow.ReadyParticipantIDs)
	require.Nil(t, paused.FreshWindow)
	require.NotNil(t, paused.Attempt.RetainedAt)
	require.Nil(t, paused.Attempt.StartedAt)
	require.NotContains(t, task049Encode(t, paused), execution.Assignment.Snapshot.Flag)
	require.Nil(t, repository.liveSnapshot())
	require.Equal(t, execution.Expectation(), repository.archivedSnapshot().Expectation())

	replayedPause, pauseReplayChanged, pauseReplayErr := arena.NewRetainedGoldenPrestartPauseUseCase(
		repository,
		fixedArenaClock{now: openedAt.Add(time.Hour)},
	).Pause(t.Context(), pause)
	require.NoError(t, pauseReplayErr)
	require.False(t, pauseReplayChanged)
	require.Equal(t, paused.PayloadDigest, replayedPause.PayloadDigest)

	resume := arena.RetainedGoldenPrestartResumeCommand{
		Scope: execution.Scope, CommandID: task049ID(24300), SessionID: pause.SessionID,
		ActorID: actorID, ExpectedSession: paused.Expectation(),
		ExpectedAuthorization: authorization.Expectation(), NextSessionRevisionID: task049ID(24301),
		NextExecutionRevisionID:  task049ID(24302),
		NextWaveRevisionID:       domain.ArenaWaveRevisionID(task049ID(24303)),
		NextWindowID:             task049ID(24304),
		NextWaveWindowRevisionID: domain.ArenaReadyWindowRevisionID(task049ID(24305)),
		NextWindowRevisionID:     task049ID(24306), NextReadinessRevisionID: task049ID(24307),
		NextPresenceRevisionID: task049ID(24308),
	}
	resumedAt := openedAt.Add(10 * time.Second)
	resumed, changed, err := arena.NewRetainedGoldenPrestartPauseUseCase(
		repository,
		fixedArenaClock{now: resumedAt},
	).Resume(t.Context(), resume)
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, resumed.Validate())
	require.Equal(t, arena.RetainedGoldenPrestartReady, resumed.State)
	require.Equal(t, paused.Attempt.ID, resumed.Attempt.ID)
	require.Equal(t, paused.WaveID, resumed.WaveID)
	require.Equal(t, paused.Assignment, resumed.Assignment)
	require.Equal(t, paused.Membership, resumed.Membership)
	require.NotNil(t, resumed.FreshWindow)
	require.NotEqual(t, paused.SupersededWindow.ID, resumed.FreshWindow.ID)
	require.Equal(t, resumedAt, resumed.FreshWindow.OpenedAt)
	require.Equal(t, resumedAt.Add(30*time.Second), resumed.FreshWindow.Deadline)
	require.Empty(t, resumed.FreshWindow.ReadyParticipantIDs)
	require.Equal(t, execution.Membership.ParticipantIDs, resumed.FreshWindow.PresentParticipantIDs)
	require.Equal(t, resume.NextExecutionRevisionID, resumed.FreshExecutionRevisionID)
	require.NotNil(t, resumed.FreshExecution)
	require.Equal(t, *resumed.FreshExecution, repository.liveSnapshot().Expectation())
	require.NotContains(t, task049Encode(t, resumed), execution.Assignment.Snapshot.Flag)
	for _, member := range repository.liveSnapshot().Wave.Members {
		require.False(t, member.Ready)
	}

	replayedResume, resumeReplayChanged, resumeReplayErr := arena.NewRetainedGoldenPrestartPauseUseCase(
		repository,
		fixedArenaClock{now: resumedAt.Add(time.Hour)},
	).Resume(t.Context(), resume)
	require.NoError(t, resumeReplayErr)
	require.False(t, resumeReplayChanged)
	require.Equal(t, resumed.PayloadDigest, replayedResume.PayloadDigest)

	archivedPause, archivedPauseChanged, archivedPauseErr := arena.NewRetainedGoldenPrestartPauseUseCase(
		repository,
		fixedArenaClock{now: resumedAt.Add(2 * time.Hour)},
	).Pause(t.Context(), pause)
	require.NoError(t, archivedPauseErr)
	require.False(t, archivedPauseChanged)
	require.Equal(t, arena.RetainedGoldenPrestartPaused, archivedPause.State)
	require.Equal(t, paused.PayloadDigest, archivedPause.PayloadDigest)

	started := task049StartedExecution(t, openedAt.Add(time.Minute))
	startedAuthorization, err := arena.NewGoldenPrestartOperatorAuthorization(
		started.Scope.TournamentID, actorID, task049ID(24410), 1,
	)
	require.NoError(t, err)
	startedRepository := newTask050RetainedPrestartRepository(started, startedAuthorization)
	startedPause := pause
	startedPause.Scope = started.Scope
	startedPause.CommandID = task049ID(24400)
	startedPause.SessionID = task049ID(24401)
	startedPause.ExpectedExecution = started.Expectation()
	startedPause.ExpectedAuthorization = startedAuthorization.Expectation()
	startedPause.NextSessionRevisionID = task049ID(24402)
	result, startedChanged, startedErr := arena.NewRetainedGoldenPrestartPauseUseCase(
		startedRepository,
		fixedArenaClock{now: openedAt.Add(2 * time.Minute)},
	).Pause(t.Context(), startedPause)
	require.Nil(t, result)
	require.False(t, startedChanged)
	require.ErrorIs(t, startedErr, arena.ErrGoldenPrestartAlreadyStarted)

	concurrentRepository := newTask050RetainedPrestartRepository(execution, authorization)
	concurrentRepository.beforeCommit = task049TwoPartyBarrier(t)
	concurrentPause := arena.RetainedGoldenPrestartPauseCommand{
		Scope: execution.Scope, CommandID: task049ID(24800), SessionID: task049ID(24801),
		ActorID: actorID, Reason: arena.GoldenPrestartPauseOperatorManual,
		ExpectedExecution: execution.Expectation(), ExpectedAuthorization: authorization.Expectation(),
		NextSessionRevisionID: task049ID(24802),
	}
	type concurrentPrestartResult struct {
		record  *arena.RetainedGoldenPrestartRecord
		changed bool
		err     error
	}
	results := make(chan concurrentPrestartResult, 2)
	for range 2 {
		go func() {
			record, changed, err := arena.NewRetainedGoldenPrestartPauseUseCase(
				concurrentRepository, fixedArenaClock{now: openedAt.Add(6 * time.Second)},
			).Pause(t.Context(), concurrentPause)
			results <- concurrentPrestartResult{record: record, changed: changed, err: err}
		}()
	}
	changedCount := 0
	for range 2 {
		result := <-results
		require.NoError(t, result.err)
		require.NotNil(t, result.record)
		if result.changed {
			changedCount++
		}
	}
	require.Equal(t, 1, changedCount)

	paused.Attempt.ParticipantIDs[0] = task049ID(24999)
	stored := repository.currentSnapshot()
	require.NotEqual(t, task049ID(24999), stored.Attempt.ParticipantIDs[0])
}

func TestRetainedGoldenPrestartPauseHardening(t *testing.T) {
	t.Parallel()

	openedAt := time.Date(2026, 9, 1, 21, 30, 0, 0, time.UTC)
	execution, authorization, pause := task050PrestartFixture(t, openedAt, 28100)
	pausedAt := openedAt.Add(5 * time.Second)

	t.Run("resume time cannot precede pause", func(t *testing.T) {
		repository := newTask050RetainedPrestartRepository(execution, authorization)
		paused, changed, err := arena.NewRetainedGoldenPrestartPauseUseCase(
			repository, fixedArenaClock{now: pausedAt},
		).Pause(t.Context(), pause)
		require.NoError(t, err)
		require.True(t, changed)
		resume := task050ResumeCommand(*paused, authorization, pause.ActorID, 28200)
		beforeWrites := repository.writeCount()
		result, resultChanged, resultErr := arena.NewRetainedGoldenPrestartPauseUseCase(
			repository, fixedArenaClock{now: pausedAt.Add(-time.Nanosecond)},
		).Resume(t.Context(), resume)
		require.Nil(t, result)
		require.False(t, resultChanged)
		require.Error(t, resultErr)
		require.Equal(t, beforeWrites, repository.writeCount())
		require.Nil(t, repository.liveSnapshot())
	})

	t.Run("stale expected session is no write", func(t *testing.T) {
		repository := newTask050RetainedPrestartRepository(execution, authorization)
		paused, _, err := arena.NewRetainedGoldenPrestartPauseUseCase(
			repository, fixedArenaClock{now: pausedAt},
		).Pause(t.Context(), pause)
		require.NoError(t, err)
		resume := task050ResumeCommand(*paused, authorization, pause.ActorID, 28300)
		resume.ExpectedSession.RevisionID = task049ID(28399)
		beforeWrites := repository.writeCount()
		result, changed, resumeErr := arena.NewRetainedGoldenPrestartPauseUseCase(
			repository, fixedArenaClock{now: pausedAt.Add(time.Second)},
		).Resume(t.Context(), resume)
		require.Nil(t, result)
		require.False(t, changed)
		require.ErrorIs(t, resumeErr, arena.ErrGoldenPrestartAuthorityConflict)
		require.Equal(t, beforeWrites, repository.writeCount())
		require.Nil(t, repository.liveSnapshot())
	})

	t.Run("concurrent identical resume", func(t *testing.T) {
		repository := newTask050RetainedPrestartRepository(execution, authorization)
		paused, _, err := arena.NewRetainedGoldenPrestartPauseUseCase(
			repository, fixedArenaClock{now: pausedAt},
		).Pause(t.Context(), pause)
		require.NoError(t, err)
		resume := task050ResumeCommand(*paused, authorization, pause.ActorID, 28400)
		repository.beforeCommit = task049TwoPartyBarrier(t)
		type result struct {
			record  *arena.RetainedGoldenPrestartRecord
			changed bool
			err     error
		}
		results := make(chan result, 2)
		for range 2 {
			go func() {
				record, changed, err := arena.NewRetainedGoldenPrestartPauseUseCase(
					repository, fixedArenaClock{now: pausedAt.Add(time.Second)},
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
		source := newTask050RetainedPrestartRepository(execution, authorization)
		oldRecord, changed, err := arena.NewRetainedGoldenPrestartPauseUseCase(
			source, fixedArenaClock{now: pausedAt},
		).Pause(t.Context(), pause)
		require.NoError(t, err)
		require.True(t, changed)

		repository := newTask050RetainedPrestartRepository(execution, authorization)
		repository.returnRecord = oldRecord
		other := pause
		other.CommandID = task049ID(28500)
		other.SessionID = task049ID(28501)
		other.NextSessionRevisionID = task049ID(28502)
		result, resultChanged, resultErr := arena.NewRetainedGoldenPrestartPauseUseCase(
			repository, fixedArenaClock{now: pausedAt},
		).Pause(t.Context(), other)
		require.Nil(t, result)
		require.False(t, resultChanged)
		require.ErrorIs(t, resultErr, domain.ErrInternal)
		require.Equal(t, 0, repository.writeCount())
	})

	t.Run("unchanged concurrent winner may have another captured time", func(t *testing.T) {
		source := newTask050RetainedPrestartRepository(execution, authorization)
		winner, changed, err := arena.NewRetainedGoldenPrestartPauseUseCase(
			source, fixedArenaClock{now: pausedAt},
		).Pause(t.Context(), pause)
		require.NoError(t, err)
		require.True(t, changed)

		repository := newTask050RetainedPrestartRepository(execution, authorization)
		repository.returnRecord = winner
		result, resultChanged, resultErr := arena.NewRetainedGoldenPrestartPauseUseCase(
			repository, fixedArenaClock{now: pausedAt.Add(time.Nanosecond)},
		).Pause(t.Context(), pause)
		require.NoError(t, resultErr)
		require.False(t, resultChanged)
		require.Equal(t, winner.PayloadDigest, result.PayloadDigest)
		require.Equal(t, 0, repository.writeCount())
	})

	t.Run("authority generation combinations", func(t *testing.T) {
		repository := newTask050RetainedPrestartRepository(execution, authorization)
		paused, _, err := arena.NewRetainedGoldenPrestartPauseUseCase(
			repository, fixedArenaClock{now: pausedAt},
		).Pause(t.Context(), pause)
		require.NoError(t, err)
		pausedAuthority, loadErr := repository.LoadRetainedGoldenPrestartAuthority(t.Context(), execution.Scope)
		require.NoError(t, loadErr)
		require.NoError(t, pausedAuthority.Validate())
		live := execution.Snapshot()
		pausedAuthority.Execution = &live
		require.Error(t, pausedAuthority.Validate())

		resume := task050ResumeCommand(*paused, authorization, pause.ActorID, 28520)
		_, _, resumeErr := arena.NewRetainedGoldenPrestartPauseUseCase(
			repository, fixedArenaClock{now: pausedAt.Add(time.Second)},
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
		mutate func(*arena.RetainedGoldenPrestartPauseCommand)
	}{
		{name: "command actor", mutate: func(command *arena.RetainedGoldenPrestartPauseCommand) { command.CommandID = command.ActorID }},
		{name: "session actor", mutate: func(command *arena.RetainedGoldenPrestartPauseCommand) { command.SessionID = command.ActorID }},
		{name: "revision authorization", mutate: func(command *arena.RetainedGoldenPrestartPauseCommand) {
			command.NextSessionRevisionID = command.ExpectedAuthorization.RevisionID
		}},
	}
	for _, test := range pauseAliasCases {
		t.Run("pause alias "+test.name, func(t *testing.T) {
			repository := newTask050RetainedPrestartRepository(execution, authorization)
			command := pause
			test.mutate(&command)
			result, changed, err := arena.NewRetainedGoldenPrestartPauseUseCase(
				repository, fixedArenaClock{now: pausedAt},
			).Pause(t.Context(), command)
			require.Nil(t, result)
			require.False(t, changed)
			require.Error(t, err)
			require.Equal(t, 0, repository.writeCount())
		})
	}

	resumeAliasCases := []struct {
		name   string
		mutate func(*arena.RetainedGoldenPrestartResumeCommand, arena.GoldenWaveExecution)
	}{
		{name: "command actor", mutate: func(command *arena.RetainedGoldenPrestartResumeCommand, _ arena.GoldenWaveExecution) {
			command.CommandID = command.ActorID
		}},
		{name: "revision authorization", mutate: func(command *arena.RetainedGoldenPrestartResumeCommand, _ arena.GoldenWaveExecution) {
			command.NextSessionRevisionID = command.ExpectedAuthorization.RevisionID
		}},
		{name: "execution scope", mutate: func(command *arena.RetainedGoldenPrestartResumeCommand, execution arena.GoldenWaveExecution) {
			command.NextExecutionRevisionID = execution.Scope.GroupID
		}},
		{name: "window participant", mutate: func(command *arena.RetainedGoldenPrestartResumeCommand, execution arena.GoldenWaveExecution) {
			command.NextWindowID = execution.Membership.ParticipantIDs[0]
		}},
	}
	for index, test := range resumeAliasCases {
		t.Run("resume alias "+test.name, func(t *testing.T) {
			repository := newTask050RetainedPrestartRepository(execution, authorization)
			paused, _, err := arena.NewRetainedGoldenPrestartPauseUseCase(
				repository, fixedArenaClock{now: pausedAt},
			).Pause(t.Context(), pause)
			require.NoError(t, err)
			command := task050ResumeCommand(*paused, authorization, pause.ActorID, 28600+index*20)
			test.mutate(&command, execution)
			beforeWrites := repository.writeCount()
			result, changed, resumeErr := arena.NewRetainedGoldenPrestartPauseUseCase(
				repository, fixedArenaClock{now: pausedAt.Add(time.Second)},
			).Resume(t.Context(), command)
			require.Nil(t, result)
			require.False(t, changed)
			require.Error(t, resumeErr)
			require.Equal(t, beforeWrites, repository.writeCount())
			require.Nil(t, repository.liveSnapshot())
		})
	}
}

func task050PrestartFixture(
	t *testing.T,
	openedAt time.Time,
	base int,
) (arena.GoldenWaveExecution, arena.GoldenPrestartOperatorAuthorization, arena.RetainedGoldenPrestartPauseCommand) {
	t.Helper()
	state := task048GoldenState(t, openedAt)
	waveRepository := &goldenWaveRepositoryFake{state: state}
	execution := task048OpenGoldenExecution(t, waveRepository, state, openedAt, base)
	actorID := task049ID(base + 90)
	authorization, err := arena.NewGoldenPrestartOperatorAuthorization(
		execution.Scope.TournamentID, actorID, task049ID(base+91), 1,
	)
	require.NoError(t, err)
	command := arena.RetainedGoldenPrestartPauseCommand{
		Scope: execution.Scope, CommandID: task049ID(base + 92), SessionID: task049ID(base + 93),
		ActorID: actorID, Reason: arena.GoldenPrestartPauseOperatorManual,
		ExpectedExecution: execution.Expectation(), ExpectedAuthorization: authorization.Expectation(),
		NextSessionRevisionID: task049ID(base + 94),
	}
	return execution, authorization, command
}

func task050ResumeCommand(
	paused arena.RetainedGoldenPrestartRecord,
	authorization arena.GoldenPrestartOperatorAuthorization,
	actorID uuid.UUID,
	base int,
) arena.RetainedGoldenPrestartResumeCommand {
	return arena.RetainedGoldenPrestartResumeCommand{
		Scope: paused.Scope, CommandID: task049ID(base), SessionID: paused.SessionID,
		ActorID: actorID, ExpectedSession: paused.Expectation(), ExpectedAuthorization: authorization.Expectation(),
		NextSessionRevisionID: task049ID(base + 1), NextExecutionRevisionID: task049ID(base + 2),
		NextWaveRevisionID: domain.ArenaWaveRevisionID(task049ID(base + 3)), NextWindowID: task049ID(base + 4),
		NextWaveWindowRevisionID: domain.ArenaReadyWindowRevisionID(task049ID(base + 5)),
		NextWindowRevisionID:     task049ID(base + 6), NextReadinessRevisionID: task049ID(base + 7),
		NextPresenceRevisionID: task049ID(base + 8),
	}
}

type task050RetainedPrestartRepository struct {
	mu            sync.Mutex
	execution     *arena.GoldenWaveExecution
	authorization arena.GoldenPrestartOperatorAuthorization
	archived      []arena.GoldenWaveExecution
	current       *arena.RetainedGoldenPrestartRecord
	replays       map[uuid.UUID]arena.RetainedGoldenPrestartRecord
	beforeCommit  func()
	returnRecord  *arena.RetainedGoldenPrestartRecord
	writes        int
}

func newTask050RetainedPrestartRepository(
	execution arena.GoldenWaveExecution,
	authorization arena.GoldenPrestartOperatorAuthorization,
) *task050RetainedPrestartRepository {
	live := execution.Snapshot()
	return &task050RetainedPrestartRepository{
		execution: &live, authorization: authorization,
		replays: make(map[uuid.UUID]arena.RetainedGoldenPrestartRecord),
	}
}

func (r *task050RetainedPrestartRepository) FindRetainedGoldenPrestartCommand(
	_ context.Context,
	_ uuid.UUID,
	commandID uuid.UUID,
) (*arena.RetainedGoldenPrestartRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	record, found := r.replays[commandID]
	if !found {
		return nil, nil
	}
	clone := record.Snapshot()
	return &clone, nil
}

func (r *task050RetainedPrestartRepository) LoadRetainedGoldenPrestartAuthority(
	_ context.Context,
	_ arena.GoldenStateScope,
) (arena.RetainedGoldenPrestartAuthority, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	authority := arena.RetainedGoldenPrestartAuthority{Authorization: r.authorization}
	if r.execution != nil {
		execution := r.execution.Snapshot()
		authority.Execution = &execution
	}
	if len(r.archived) > 0 {
		execution := r.archived[len(r.archived)-1].Snapshot()
		authority.ArchivedExecution = &execution
	}
	if r.current != nil {
		current := r.current.Snapshot()
		authority.Current = &current
	}
	return authority, nil
}

func (r *task050RetainedPrestartRepository) CommitRetainedGoldenPrestart(
	_ context.Context,
	commit arena.RetainedGoldenPrestartCommit,
) (*arena.RetainedGoldenPrestartRecord, bool, error) {
	if r.beforeCommit != nil {
		r.beforeCommit()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.authorization.Expectation().Equal(commit.ExpectedAuthorization) {
		return nil, false, domain.ErrConflict
	}
	if commit.ExpectedExecution == nil {
		if r.execution != nil {
			return nil, false, domain.ErrConflict
		}
	} else if r.execution == nil || !r.execution.Expectation().Equal(*commit.ExpectedExecution) {
		return nil, false, domain.ErrConflict
	}
	if commit.ExpectedSession == nil {
		if r.current != nil {
			return nil, false, domain.ErrConflict
		}
	} else if r.current == nil || !r.current.Expectation().Equal(*commit.ExpectedSession) {
		return nil, false, domain.ErrConflict
	}
	if commit.ExpectedArchivedExecution != nil {
		if len(r.archived) == 0 ||
			!r.archived[len(r.archived)-1].Expectation().Equal(*commit.ExpectedArchivedExecution) {
			return nil, false, domain.ErrConflict
		}
	} else if commit.PublishedExecution != nil {
		return nil, false, domain.ErrConflict
	}
	if _, duplicate := r.replays[commit.Record.CommandID]; duplicate {
		return nil, false, errors.New("duplicate retained prestart command")
	}
	if r.returnRecord != nil {
		clone := r.returnRecord.Snapshot()
		return &clone, false, nil
	}
	record := commit.Record.Snapshot()
	switch {
	case commit.ArchivedExecution != nil:
		if r.execution == nil || !r.execution.Expectation().Equal(commit.ArchivedExecution.Expectation()) ||
			commit.PublishedExecution != nil {
			return nil, false, errors.New("invalid retained pause transition")
		}
		r.archived = append(r.archived, commit.ArchivedExecution.Snapshot())
		r.execution = nil
	case commit.PublishedExecution != nil:
		if r.execution != nil || commit.PublishedExecution.Validate() != nil {
			return nil, false, errors.New("invalid retained resume transition")
		}
		live := commit.PublishedExecution.Snapshot()
		r.execution = &live
	default:
		return nil, false, errors.New("retained transition has no execution effect")
	}
	r.writes++
	r.current = &record
	r.replays[record.CommandID] = record.Snapshot()
	clone := record.Snapshot()
	return &clone, true, nil
}

func (r *task050RetainedPrestartRepository) writeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.writes
}

func (r *task050RetainedPrestartRepository) liveSnapshot() *arena.GoldenWaveExecution {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.execution == nil {
		return nil
	}
	clone := r.execution.Snapshot()
	return &clone
}

func (r *task050RetainedPrestartRepository) archivedSnapshot() arena.GoldenWaveExecution {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.archived[len(r.archived)-1].Snapshot()
}

func (r *task050RetainedPrestartRepository) currentSnapshot() arena.RetainedGoldenPrestartRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current.Snapshot()
}

var _ arena.RetainedGoldenPrestartRepository = (*task050RetainedPrestartRepository)(nil)
