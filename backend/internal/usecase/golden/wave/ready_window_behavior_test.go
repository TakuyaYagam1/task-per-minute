package golden_test

import (
	"errors"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenstate "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/state"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/wave"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestGoldenReadyWindow(t *testing.T) {
	t.Parallel()

	openedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	state := waveTask048GoldenState(t, openedAt)
	repository := waveNewGoldenWaveRepositoryHarness(t, state)
	command := task048OpenCommand(state, 100)

	execution, changed, err := goldenusecase.NewGoldenReadyWindowUseCase(
		repository,
		waveNewGoldenClock(t, openedAt),
	).Open(t.Context(), command)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, openedAt, execution.OpenedAt)
	require.Equal(t, openedAt.Add(30*time.Second), execution.Deadline)
	require.Equal(t, command.AttemptID, execution.Attempt.ID)
	require.Equal(t, domain.GoldenAttemptStateWaitingReady, execution.Attempt.State)
	require.Equal(t, command.WaveID, execution.Wave.ID)
	require.Equal(t, domain.WaveStateReadyWindowOpen, execution.Wave.State)
	require.Equal(t, command.WindowID, execution.Window.ID)
	require.Equal(t, goldenusecase.GoldenReadyWindowOpen, execution.Window.State)
	require.Equal(t, state.ActiveParticipantIDs(), execution.Attempt.ParticipantIDs)
	require.Equal(t, state.ActiveParticipantIDs(), execution.Membership.ParticipantIDs)
	require.Equal(t, state.ActiveParticipantIDs(), execution.Window.PresentParticipantIDs)
	require.Empty(t, execution.Window.ReadyParticipantIDs)
	require.Len(t, execution.Group.Attempts, 1)
	require.Equal(t, command.AttemptID, execution.Group.Attempts[0].ID)
	require.Equal(t, state.Plan, execution.Assignment.Plan)
	require.Equal(t, state.ExactPlan.Groups[1].Edges[0].ID, execution.Assignment.EdgeID)
	require.Equal(t, state.ExactPlan.Groups[1].Edges[0].ContentDigest, execution.Assignment.ContentDigest)
	require.Len(t, execution.Assignment.Private, len(state.ActiveParticipantIDs()))
	for index, private := range execution.Assignment.Private {
		require.Equal(t, command.PrivateAssignments[index].ParticipantID, private.ParticipantID)
		require.Equal(t, command.PrivateAssignments[index].AssignmentID, private.ID)
		require.Equal(t, execution.Assignment.Snapshot.SnapshotID, private.SnapshotID)
		require.Equal(t, execution.Assignment.ContentDigest, private.ContentDigest)
	}
	require.Len(t, execution.Receipts, 1)
	require.Equal(t, goldenusecase.GoldenWaveCommandOpened, execution.Receipts[0].Kind)
	require.Equal(t, 1, repository.commitCount())

	execution.Group.Members[0].Excluded = true
	execution.Assignment.Snapshot.Title = "tampered"
	execution.Window.PresentParticipantIDs[0] = task048ID(9990)
	reloaded, loadErr := repository.LoadGoldenWaveExecution(t.Context(), state.Scope)
	require.NoError(t, loadErr)
	require.False(t, reloaded.Group.Members[0].Excluded)
	require.NotEqual(t, "tampered", reloaded.Assignment.Snapshot.Title)
	require.NotEqual(t, task048ID(9990), reloaded.Window.PresentParticipantIDs[0])

	t.Run("replays before source authority drift", func(t *testing.T) {
		repository.setStateLoadError(errors.New("source drift must not be read on replay"))
		replayed, replayChanged, replayErr := goldenusecase.NewGoldenReadyWindowUseCase(
			repository,
			waveNewGoldenClock(t, openedAt.Add(time.Minute)),
		).Open(t.Context(), command)
		require.NoError(t, replayErr)
		require.False(t, replayChanged)
		require.Equal(t, command.AttemptID, replayed.Attempt.ID)
		require.Equal(t, 1, repository.commitCount())
		repository.setStateLoadError(nil)

		reused := command
		reused.AttemptID = task048ID(9980)
		result, reusedChanged, reusedErr := goldenusecase.NewGoldenReadyWindowUseCase(
			repository,
			waveNewGoldenClock(t, openedAt),
		).Open(t.Context(), reused)
		require.Nil(t, result)
		require.False(t, reusedChanged)
		require.ErrorIs(t, reusedErr, goldenusecase.ErrGoldenWaveCommandReuse)
	})

	t.Run("reconciles an archived winner after the initial replay lookup", func(t *testing.T) {
		localState := waveTask048GoldenState(t, openedAt)
		localRepository := waveNewGoldenWaveRepositoryHarness(t, localState)
		localCommand := task048OpenCommand(localState, 450)
		opened, localChanged, openErr := goldenusecase.NewGoldenReadyWindowUseCase(
			localRepository,
			waveNewGoldenClock(t, openedAt),
		).Open(t.Context(), localCommand)
		require.NoError(t, openErr)
		require.True(t, localChanged)
		localRepository.archiveBeforeNextLoadAfterHiddenFind()
		replayed, localChanged, replayErr := goldenusecase.NewGoldenReadyWindowUseCase(
			localRepository,
			waveNewGoldenClock(t, openedAt.Add(time.Second)),
		).Open(t.Context(), localCommand)
		require.NoError(t, replayErr)
		require.False(t, localChanged)
		require.Equal(t, opened.Expectation(), replayed.Expectation())
	})

	t.Run("requires two active unresolved members and exact revisions", func(t *testing.T) {
		oneActive := task048SoloGoldenState(t, openedAt)
		localRepository := waveNewGoldenWaveRepositoryHarness(t, oneActive)
		result, localChanged, openErr := goldenusecase.NewGoldenReadyWindowUseCase(
			localRepository,
			waveNewGoldenClock(t, openedAt),
		).Open(t.Context(), task048OpenCommand(oneActive, 500))
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, openErr, goldenusecase.ErrGoldenReadyWindowIneligible)
		require.Zero(t, localRepository.commitCount())

		stale := task048OpenCommand(state, 600)
		stale.ExpectedState.Membership.Revision++
		staleRepository := waveNewGoldenWaveRepositoryHarness(t, state)
		result, localChanged, openErr = goldenusecase.NewGoldenReadyWindowUseCase(
			staleRepository,
			waveNewGoldenClock(t, openedAt),
		).Open(t.Context(), stale)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, openErr, goldenusecase.ErrGoldenWaveAuthorityConflict)
		require.Zero(t, staleRepository.commitCount())

		readyBase := waveGoldenStateFixture(t, openedAt.Add(-5*time.Second))
		readyRepository := waveNewGoldenStateRepository(t, readyBase)
		resolved := waveGoldenAcceptReady(
			t,
			readyRepository,
			readyBase,
			readyBase.Group.Members[0].ParticipantID,
			openedAt.Add(-4*time.Second),
			640,
		)
		participants := resolved.ActiveParticipantIDs()
		firstID := resolved.Group.Attempts[0].ID
		started := openedAt.Add(-2 * time.Minute)
		finished := openedAt.Add(-time.Minute)
		resolved.Group.Attempts = []domain.GoldenAttempt{
			{
				ID: firstID, GroupID: resolved.Group.ID, GroupRevisionID: resolved.Group.RevisionID,
				AttemptNo: 1, State: domain.GoldenAttemptStateCompleted,
				ParticipantIDs: append([]uuid.UUID(nil), participants...),
				StartedAt:      &started, FinishedAt: &finished,
			},
			{
				ID: task048ID(651), GroupID: resolved.Group.ID, GroupRevisionID: resolved.Group.RevisionID,
				AttemptNo: 2, PreviousAttemptID: &firstID, State: domain.GoldenAttemptStateCancelled,
				ParticipantIDs: append([]uuid.UUID(nil), participants...), FinishedAt: &finished,
			},
		}
		resolved.PayloadDigest = [32]byte{}
		resolved, buildErr := goldenstate.BuildGoldenState(resolved)
		require.NoError(t, buildErr)
		resolvedRepository := waveNewGoldenWaveRepositoryHarness(t, resolved)
		result, localChanged, openErr = goldenusecase.NewGoldenReadyWindowUseCase(
			resolvedRepository,
			waveNewGoldenClock(t, openedAt),
		).Open(t.Context(), task048OpenCommand(resolved, 660))
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, openErr, goldenusecase.ErrGoldenReadyWindowIneligible)
		require.Zero(t, resolvedRepository.commitCount())
	})

	t.Run("rejects existing unresolved attempts and identity aliases", func(t *testing.T) {
		withAttempt := waveGoldenStateFixture(t, openedAt)
		attemptRepository := waveNewGoldenWaveRepositoryHarness(t, withAttempt)
		result, localChanged, openErr := goldenusecase.NewGoldenReadyWindowUseCase(
			attemptRepository,
			waveNewGoldenClock(t, openedAt),
		).Open(t.Context(), task048OpenCommand(withAttempt, 700))
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, openErr, goldenusecase.ErrGoldenReadyWindowIneligible)

		aliased := task048OpenCommand(state, 800)
		aliased.AssignmentID = state.Plan.PlanID
		aliasRepository := waveNewGoldenWaveRepositoryHarness(t, state)
		result, localChanged, openErr = goldenusecase.NewGoldenReadyWindowUseCase(
			aliasRepository,
			waveNewGoldenClock(t, openedAt),
		).Open(t.Context(), aliased)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, openErr, goldenusecase.ErrInvalidGoldenWaveExecution)
		require.Zero(t, aliasRepository.commitCount())
	})

	t.Run("rejects an identity reserved by an archived execution", func(t *testing.T) {
		localState := waveTask048GoldenState(t, openedAt)
		localCommand := task048OpenCommand(localState, 850)
		localRepository := waveNewGoldenWaveRepositoryHarness(t, localState)
		localRepository.reserveIdentity(localCommand.WaveID)
		result, localChanged, openErr := goldenusecase.NewGoldenReadyWindowUseCase(
			localRepository,
			waveNewGoldenClock(t, openedAt),
		).Open(t.Context(), localCommand)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, openErr, goldenusecase.ErrGoldenWaveIdentityConflict)
		require.Zero(t, localRepository.commitCount())
	})
}
