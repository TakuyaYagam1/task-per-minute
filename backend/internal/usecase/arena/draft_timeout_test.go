package arena_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	arena "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestDraftTimeout(t *testing.T) {
	t.Parallel()

	t.Run("commits one deterministic action at the exact deadline", func(t *testing.T) {
		t.Parallel()

		startedAt := time.Date(2026, time.August, 30, 14, 0, 0, 0, time.UTC)
		initial := task030Draft(t, startedAt)
		repository := newDraftRepositoryFake(initial)
		useCase := arena.NewDraftTimeoutUseCase(
			repository,
			task030Clock{at: startedAt.Add(arena.DraftTurnDuration)},
		)
		command := task030TimeoutCommand(initial, initial.ServiceEpoch, task030ID(70))

		start := make(chan struct{})
		var changed atomic.Int32
		var wait sync.WaitGroup
		results := make(chan arena.DraftTimeoutResult, 12)
		for range 12 {
			wait.Add(1)
			go func() {
				defer wait.Done()
				<-start
				result, err := useCase.Resolve(context.Background(), command)
				if err != nil {
					t.Errorf("Resolve() error = %v", err)
					return
				}
				if result.Changed {
					changed.Add(1)
				}
				results <- result
			}()
		}
		close(start)
		wait.Wait()
		close(results)

		var selected domain.Category
		var committed arena.DraftExecution
		for result := range results {
			if result.Route != arena.DraftRouteNone || result.Draft.Revision != 2 ||
				len(result.Draft.Actions) != 1 || !result.Draft.Actions[0].Automatic ||
				result.Draft.Actions[0].DecisionEvidence == nil || result.NextTimeout == nil {
				t.Fatalf("timeout result = %+v", result)
			}
			action := result.Draft.Actions[0]
			if action.DecisionEvidence.ID != command.DecisionEvidenceID ||
				action.OccurredAt != initial.TurnDeadline ||
				action.ScheduledDeadline != initial.TurnDeadline {
				t.Fatalf("automatic action evidence = %+v", action)
			}
			replayed, err := action.DecisionEvidence.Replay()
			if err != nil || len(replayed) == 0 || domain.Category(replayed[0]) != action.Category {
				t.Fatalf("Replay() = %v, error = %v, action = %+v", replayed, err, action)
			}
			if selected == "" {
				selected = action.Category
				committed = cloneDraftExecution(result.Draft)
			} else if selected != action.Category {
				t.Fatalf("selected category changed across retries: %s then %s", selected, action.Category)
			}
		}
		if changed.Load() != 1 {
			t.Fatalf("changed commits = %d, want 1", changed.Load())
		}
		for _, category := range committed.Pool {
			if category == committed.Actions[0].Category {
				continue
			}
			committed.Actions[0].Category = category
			committed.LegalCategories = categoriesExcept(committed.Pool, category)
			break
		}
		if err := committed.Validate(); !errors.Is(err, arena.ErrInvalidDraftExecution) {
			t.Fatalf("tampered automatic category error = %v", err)
		}
		current, err := repository.LoadDraft(t.Context(), initial.ID)
		if err != nil || current.Revision != 2 || len(current.Actions) != 1 {
			t.Fatalf("stored draft = %+v, error = %v", current, err)
		}
	})

	t.Run("rejects an early timeout", func(t *testing.T) {
		t.Parallel()

		startedAt := time.Date(2026, time.August, 30, 14, 30, 0, 0, time.UTC)
		initial := task030Draft(t, startedAt)
		repository := newDraftRepositoryFake(initial)
		useCase := arena.NewDraftTimeoutUseCase(
			repository,
			task030Clock{at: initial.TurnDeadline.Add(-time.Nanosecond)},
		)
		result, err := useCase.Resolve(
			t.Context(),
			task030TimeoutCommand(initial, initial.ServiceEpoch, task030ID(80)),
		)
		if !errors.Is(err, arena.ErrDraftTimeoutEarly) || result.Changed {
			t.Fatalf("Resolve() result = %+v, error = %v", result, err)
		}
		current, loadErr := repository.LoadDraft(t.Context(), initial.ID)
		if loadErr != nil || current.Revision != 1 || len(current.Actions) != 0 {
			t.Fatalf("early timeout changed draft = %+v, error = %v", current, loadErr)
		}
	})

	t.Run("routes an epoch mismatch without applying an action", func(t *testing.T) {
		t.Parallel()

		startedAt := time.Date(2026, time.August, 30, 15, 0, 0, 0, time.UTC)
		initial := task030Draft(t, startedAt)
		repository := newDraftRepositoryFake(initial)
		newEpoch := task030ID(90)
		useCase := arena.NewDraftTimeoutUseCase(
			repository,
			task030Clock{at: initial.TurnDeadline.Add(time.Second)},
		)
		result, err := useCase.Resolve(
			t.Context(),
			task030TimeoutCommand(initial, newEpoch, task030ID(91)),
		)
		if err != nil || result.Changed || result.Route != arena.DraftRouteRecovery ||
			result.Draft.Revision != initial.Revision || len(result.Draft.Actions) != 0 {
			t.Fatalf("Resolve() result = %+v, error = %v", result, err)
		}
		current, loadErr := repository.LoadDraft(t.Context(), initial.ID)
		if loadErr != nil || current.Revision != 1 || len(current.Actions) != 0 {
			t.Fatalf("epoch mismatch changed draft = %+v, error = %v", current, loadErr)
		}
	})
}

func task030TimeoutCommand(
	draft arena.DraftExecution,
	currentServiceEpoch uuid.UUID,
	commandID uuid.UUID,
) arena.DraftTimeoutCommand {
	return arena.DraftTimeoutCommand{
		DraftID:              draft.ID,
		ExpectedRevisionID:   draft.RevisionID,
		ExpectedRevision:     draft.Revision,
		ExpectedServiceEpoch: draft.ServiceEpoch,
		CurrentServiceEpoch:  currentServiceEpoch,
		ExpectedTurn:         draft.Turn,
		ExpectedDeadline:     draft.TurnDeadline,
		CommandID:            commandID,
		ResultRevisionID:     uuid.New(),
		ActionID:             uuid.New(),
		DecisionEvidenceID:   uuid.New(),
	}
}
