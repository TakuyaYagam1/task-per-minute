package draft_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	draftmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft/mocks"
)

type deadlineRepository struct {
	*draftmocks.MockRepository

	due    []draft.SwissDraftDeadline
	onList func()
}

func (repository *deadlineRepository) ListDueSwissDrafts(
	context.Context,
	time.Time,
	int32,
) ([]draft.SwissDraftDeadline, error) {
	if repository.onList != nil {
		repository.onList()
	}
	return append([]draft.SwissDraftDeadline(nil), repository.due...), nil
}

func TestDeadlineWorkerRunsBoundedStartupAndPollingScans(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 30, 15, 0, 0, 0, time.UTC)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := atomic.Int32{}
	repository := &deadlineRepository{
		MockRepository: &draftmocks.MockRepository{},
		onList: func() {
			if calls.Add(1) >= 2 {
				cancel()
			}
		},
	}
	worker, err := draft.NewDeadlineWorker(
		repository,
		newDraftClock(t, now),
		draft.DeadlineWorkerConfig{BatchSize: 1, PollInterval: time.Millisecond, ScanTimeout: time.Second, StaleAfter: time.Minute},
	)
	require.NoError(t, err)

	require.NoError(t, worker.Run(ctx))
	require.GreaterOrEqual(t, calls.Load(), int32(2))
	health := worker.Health(now)
	require.True(t, health.Started)
	require.False(t, health.Running)
	require.True(t, health.InitialScanComplete)
	require.NotNil(t, health.LastSuccessAt)
}

func TestDeadlineWorkerRearmsAfterRestart(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 30, 15, 30, 0, 0, time.UTC)
	var stop context.CancelFunc
	calls := atomic.Int32{}
	repository := &deadlineRepository{
		MockRepository: &draftmocks.MockRepository{},
		onList: func() {
			calls.Add(1)
			stop()
		},
	}
	worker, err := draft.NewDeadlineWorker(
		repository,
		newDraftClock(t, now),
		draft.DeadlineWorkerConfig{BatchSize: 1, PollInterval: time.Millisecond, ScanTimeout: time.Second, StaleAfter: time.Minute},
	)
	require.NoError(t, err)

	for range 2 {
		ctx, cancel := context.WithCancel(context.Background())
		stop = cancel
		require.NoError(t, worker.Run(ctx))
		cancel()
	}
	require.Equal(t, int32(2), calls.Load())
}

func TestDeadlineWorkerResolvesAndReplaysSwissDraftTimeout(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, time.August, 30, 16, 0, 0, 0, time.UTC)
	initial := task030Draft(t, startedAt)
	harness := newDraftRepositoryHarness(t, initial)
	repository := &deadlineRepository{
		MockRepository: harness.mock,
		due: []draft.SwissDraftDeadline{{
			DraftID: initial.ID, RevisionID: initial.RevisionID, Revision: initial.Revision,
			ServiceEpoch: initial.ServiceEpoch, Turn: initial.Turn, Deadline: initial.TurnDeadline,
		}},
	}
	worker, err := draft.NewDeadlineWorker(
		repository,
		newDraftClock(t, initial.TurnDeadline),
		draft.DeadlineWorkerConfig{BatchSize: 1, PollInterval: time.Millisecond, ScanTimeout: time.Second, StaleAfter: time.Minute},
	)
	require.NoError(t, err)

	first, err := worker.Process(context.Background())
	require.NoError(t, err)
	require.Equal(t, draft.DeadlineProcessResult{Scanned: 1, Changed: 1}, first)

	second, err := worker.Process(context.Background())
	require.NoError(t, err)
	require.Equal(t, draft.DeadlineProcessResult{Scanned: 1, Changed: 0}, second)

	current, err := harness.mock.LoadDraft(t.Context(), initial.ID)
	require.NoError(t, err)
	require.Equal(t, int64(2), current.Revision)
	require.Len(t, current.Actions, 1)
	require.True(t, current.Actions[0].Automatic)
	require.Equal(t, initial.TurnDeadline, current.Actions[0].OccurredAt)
}

func TestDeadlineWorkerResolvesBO3FinalTurnAndCompletesDraft(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, time.August, 30, 16, 30, 0, 0, time.UTC)
	revision := task029CategoryRevision(t, domain.TournamentStageFinal, true, task030ID(101), startedAt.Add(-time.Minute))
	initial, err := draft.StartExecution(draft.ExecutionStartCommand{
		CategoryRevision:   revision,
		DraftID:            task030ID(102),
		InitialRevisionID:  task030ID(103),
		DecisionEvidenceID: task030ID(104),
		ParticipantIDs:     [2]uuid.UUID{task030ID(105), task030ID(106)},
		ServiceEpoch:       task030ID(107),
		CommandID:          task030ID(108),
		StartedAt:          startedAt,
	})
	require.NoError(t, err)

	harness := newDraftRepositoryHarness(t, initial)
	current := initial
	for index, category := range []domain.Category{domain.CategoryWeb, domain.CategoryCrypto, domain.CategoryReverse} {
		useCase := draft.NewActionUseCase(harness.mock, newDraftClock(t, current.TurnDeadline))
		result, applyErr := useCase.Apply(t.Context(), task030ActionCommand(current, task030ID(120+index), category))
		require.NoError(t, applyErr)
		current = result.Draft
	}
	require.Equal(t, draft.ExecutionStateActive, current.State)
	require.Equal(t, 4, current.Turn)
	require.NotNil(t, current.AbsoluteDeadline)

	repository := &deadlineRepository{
		MockRepository: harness.mock,
		due: []draft.SwissDraftDeadline{{
			DraftID: current.ID, RevisionID: current.RevisionID, Revision: current.Revision,
			ServiceEpoch: current.ServiceEpoch, Turn: current.Turn, Deadline: *current.AbsoluteDeadline,
		}},
	}
	worker, err := draft.NewDeadlineWorker(
		repository,
		newDraftClock(t, *current.AbsoluteDeadline),
		draft.DeadlineWorkerConfig{BatchSize: 1, PollInterval: time.Millisecond, ScanTimeout: time.Second, StaleAfter: time.Minute},
	)
	require.NoError(t, err)

	result, err := worker.Process(context.Background())
	require.NoError(t, err)
	require.Equal(t, draft.DeadlineProcessResult{Scanned: 1, Changed: 1}, result)

	completed, err := harness.mock.LoadDraft(t.Context(), initial.ID)
	require.NoError(t, err)
	require.Equal(t, draft.ExecutionStateCompleted, completed.State)
	require.Len(t, completed.Actions, 4)
	require.True(t, completed.Actions[3].Automatic)
	require.Len(t, completed.SelectedCategories, 3)
}

func TestDeadlineWorkerRejectsInvalidDeadlineSnapshot(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, time.August, 30, 17, 0, 0, 0, time.UTC)
	initial := task030Draft(t, startedAt)
	harness := newDraftRepositoryHarness(t, initial)
	repository := &deadlineRepository{
		MockRepository: harness.mock,
		due:            []draft.SwissDraftDeadline{{DraftID: initial.ID}},
	}
	worker, err := draft.NewDeadlineWorker(
		repository,
		newDraftClock(t, initial.TurnDeadline),
		draft.DeadlineWorkerConfig{BatchSize: 1, PollInterval: time.Millisecond, ScanTimeout: time.Second, StaleAfter: time.Minute},
	)
	require.NoError(t, err)

	result, err := worker.Process(context.Background())
	require.Error(t, err)
	require.Equal(t, draft.DeadlineProcessResult{Scanned: 1}, result)
	require.Len(t, harness.state.history, 1)
}
