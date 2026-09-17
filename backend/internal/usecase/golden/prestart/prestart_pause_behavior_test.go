package golden_test

import (
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/prestart"
	goldenwave "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/wave"

	"github.com/stretchr/testify/require"
)

func TestRetainedGoldenPrestartPause(t *testing.T) {
	t.Parallel()

	openedAt := time.Date(2026, 9, 1, 21, 0, 0, 0, time.UTC)
	state := prestartTask048GoldenState(t, openedAt)
	waveRepository := prestartNewGoldenWaveRepositoryHarness(t, state)
	execution := prestartTask048OpenGoldenExecution(t, waveRepository, state, openedAt, 24000)
	participantID := execution.Membership.ParticipantIDs[0]
	ready, changed, err := goldenwave.NewGoldenReadinessUseCase(
		waveRepository,
		prestartNewGoldenClock(t, openedAt.Add(time.Second)),
	).MarkReady(t.Context(), prestartTask048ReadyCommand(execution, participantID, 24100))
	require.NoError(t, err)
	require.True(t, changed)
	execution = *ready
	actorID := prestartTask049ID(24190)
	authorization, err := goldenusecase.NewGoldenPrestartOperatorAuthorization(
		execution.Scope.TournamentID, actorID, prestartTask049ID(24191), 1,
	)
	require.NoError(t, err)
	repository := newTask050RetainedPrestartRepositoryHarness(t, execution, authorization)
	pause := goldenusecase.RetainedGoldenPrestartPauseCommand{
		Scope: execution.Scope, CommandID: prestartTask049ID(24200), SessionID: prestartTask049ID(24201),
		ActorID: actorID, Reason: goldenusecase.GoldenPrestartPauseOperatorManual,
		ExpectedExecution: execution.Expectation(), ExpectedAuthorization: authorization.Expectation(),
		NextSessionRevisionID: prestartTask049ID(24202),
	}

	paused, changed, err := goldenusecase.NewRetainedGoldenPrestartPauseUseCase(
		repository,
		prestartNewGoldenClock(t, openedAt.Add(5*time.Second)),
	).Pause(t.Context(), pause)
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, paused.Validate())
	require.Equal(t, goldenusecase.RetainedGoldenPrestartPaused, paused.State)
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
	require.NotContains(t, prestartTask049Encode(t, paused), execution.Assignment.Snapshot.Flag)
	require.Nil(t, repository.liveSnapshot())
	require.Equal(t, execution.Expectation(), repository.archivedSnapshot().Expectation())

	replayedPause, pauseReplayChanged, pauseReplayErr := goldenusecase.NewRetainedGoldenPrestartPauseUseCase(
		repository,
		prestartNewGoldenClock(t, openedAt.Add(time.Hour)),
	).Pause(t.Context(), pause)
	require.NoError(t, pauseReplayErr)
	require.False(t, pauseReplayChanged)
	require.Equal(t, paused.PayloadDigest, replayedPause.PayloadDigest)

	resume := goldenusecase.RetainedGoldenPrestartResumeCommand{
		Scope: execution.Scope, CommandID: prestartTask049ID(24300), SessionID: pause.SessionID,
		ActorID: actorID, ExpectedSession: paused.Expectation(),
		ExpectedAuthorization: authorization.Expectation(), NextSessionRevisionID: prestartTask049ID(24301),
		NextExecutionRevisionID:  prestartTask049ID(24302),
		NextWaveRevisionID:       domain.WaveRevisionID(prestartTask049ID(24303)),
		NextWindowID:             prestartTask049ID(24304),
		NextWaveWindowRevisionID: domain.ReadyWindowRevisionID(prestartTask049ID(24305)),
		NextWindowRevisionID:     prestartTask049ID(24306), NextReadinessRevisionID: prestartTask049ID(24307),
		NextPresenceRevisionID: prestartTask049ID(24308),
	}
	resumedAt := openedAt.Add(10 * time.Second)
	resumed, changed, err := goldenusecase.NewRetainedGoldenPrestartPauseUseCase(
		repository,
		prestartNewGoldenClock(t, resumedAt),
	).Resume(t.Context(), resume)
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, resumed.Validate())
	require.Equal(t, goldenusecase.RetainedGoldenPrestartReady, resumed.State)
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
	require.NotContains(t, prestartTask049Encode(t, resumed), execution.Assignment.Snapshot.Flag)
	for _, member := range repository.liveSnapshot().Wave.Members {
		require.False(t, member.Ready)
	}

	replayedResume, resumeReplayChanged, resumeReplayErr := goldenusecase.NewRetainedGoldenPrestartPauseUseCase(
		repository,
		prestartNewGoldenClock(t, resumedAt.Add(time.Hour)),
	).Resume(t.Context(), resume)
	require.NoError(t, resumeReplayErr)
	require.False(t, resumeReplayChanged)
	require.Equal(t, resumed.PayloadDigest, replayedResume.PayloadDigest)

	archivedPause, archivedPauseChanged, archivedPauseErr := goldenusecase.NewRetainedGoldenPrestartPauseUseCase(
		repository,
		prestartNewGoldenClock(t, resumedAt.Add(2*time.Hour)),
	).Pause(t.Context(), pause)
	require.NoError(t, archivedPauseErr)
	require.False(t, archivedPauseChanged)
	require.Equal(t, goldenusecase.RetainedGoldenPrestartPaused, archivedPause.State)
	require.Equal(t, paused.PayloadDigest, archivedPause.PayloadDigest)

	started := prestartTask049StartedExecution(t, openedAt.Add(time.Minute))
	startedAuthorization, err := goldenusecase.NewGoldenPrestartOperatorAuthorization(
		started.Scope.TournamentID, actorID, prestartTask049ID(24410), 1,
	)
	require.NoError(t, err)
	startedRepository := newTask050RetainedPrestartRepositoryHarness(t, started, startedAuthorization)
	startedPause := pause
	startedPause.Scope = started.Scope
	startedPause.CommandID = prestartTask049ID(24400)
	startedPause.SessionID = prestartTask049ID(24401)
	startedPause.ExpectedExecution = started.Expectation()
	startedPause.ExpectedAuthorization = startedAuthorization.Expectation()
	startedPause.NextSessionRevisionID = prestartTask049ID(24402)
	result, startedChanged, startedErr := goldenusecase.NewRetainedGoldenPrestartPauseUseCase(
		startedRepository,
		prestartNewGoldenClock(t, openedAt.Add(2*time.Minute)),
	).Pause(t.Context(), startedPause)
	require.Nil(t, result)
	require.False(t, startedChanged)
	require.ErrorIs(t, startedErr, goldenusecase.ErrGoldenPrestartAlreadyStarted)

	concurrentRepository := newTask050RetainedPrestartRepositoryHarness(t, execution, authorization)
	concurrentRepository.beforeCommit = prestartTask049TwoPartyBarrier(t)
	concurrentPause := goldenusecase.RetainedGoldenPrestartPauseCommand{
		Scope: execution.Scope, CommandID: prestartTask049ID(24800), SessionID: prestartTask049ID(24801),
		ActorID: actorID, Reason: goldenusecase.GoldenPrestartPauseOperatorManual,
		ExpectedExecution: execution.Expectation(), ExpectedAuthorization: authorization.Expectation(),
		NextSessionRevisionID: prestartTask049ID(24802),
	}
	type concurrentPrestartResult struct {
		record  *goldenusecase.RetainedGoldenPrestartRecord
		changed bool
		err     error
	}
	clock := prestartNewGoldenClock(t, openedAt.Add(6*time.Second))
	results := make(chan concurrentPrestartResult, 2)
	for range 2 {
		go func() {
			record, changed, err := goldenusecase.NewRetainedGoldenPrestartPauseUseCase(
				concurrentRepository, clock,
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

	paused.Attempt.ParticipantIDs[0] = prestartTask049ID(24999)
	stored := repository.currentSnapshot()
	require.NotEqual(t, prestartTask049ID(24999), stored.Attempt.ParticipantIDs[0])
}
