package draft_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecasedraft "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	draftmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft/mocks"
)

func TestDraftActionValidation(t *testing.T) {
	t.Parallel()

	t.Run("records reproducible actor order and advances one legal turn", func(t *testing.T) {
		t.Parallel()

		startedAt := time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)
		initial := task030Draft(t, startedAt)
		replayed, err := initial.FirstActorDecision.Replay()
		if err != nil {
			t.Fatalf("Replay() error = %v", err)
		}
		if initial.Revision != 1 || initial.Turn != 1 || initial.CommandID != task030ID(8) ||
			initial.ServiceEpoch != task030ID(7) || initial.CurrentActorID == nil ||
			initial.CurrentAction == nil || *initial.CurrentAction != domain.DraftActionBan ||
			initial.FirstParticipantID.String() != replayed[0] || len(initial.LegalCategories) != 3 {
			t.Fatalf("initial draft evidence = %+v, replay = %v", initial, replayed)
		}

		repository := newDraftRepositoryHarness(t, initial)
		useCase := usecasedraft.NewActionUseCase(repository.mock, newDraftClock(t, startedAt.Add(2*time.Second)))
		command := task030ActionCommand(initial, task030ID(10), domain.CategoryWeb)
		result, err := useCase.Apply(t.Context(), command)
		if err != nil {
			t.Fatalf("Apply() error = %v", err)
		}
		if !result.Changed || result.Draft.Revision != 2 || result.Draft.Turn != 2 ||
			result.Draft.CommandID != command.CommandID || len(result.Draft.Actions) != 1 ||
			result.Draft.Actions[0].CommandID != command.CommandID || result.Draft.Actions[0].Automatic ||
			result.NextTimeout == nil || !result.NextTimeout.Deadline.Equal(startedAt.Add(17*time.Second)) {
			t.Fatalf("action result = %+v", result)
		}

		retried, err := useCase.Apply(t.Context(), command)
		if err != nil || retried.Changed || retried.Draft.RevisionID != result.Draft.RevisionID {
			t.Fatalf("idempotent retry = %+v, error = %v", retried, err)
		}
	})

	t.Run("rejects wrong owner turn revision category and terminal state", func(t *testing.T) {
		t.Parallel()

		startedAt := time.Date(2026, time.August, 30, 12, 30, 0, 0, time.UTC)
		initial := task030Draft(t, startedAt)
		completed := completeTask030Draft(t, initial, startedAt)
		base := task030ActionCommand(initial, task030ID(20), domain.CategoryWeb)
		cases := []struct {
			name   string
			mutate func(*usecasedraft.Execution, *usecasedraft.PlayerActionCommand)
			want   error
		}{
			{
				name: "wrong owner",
				mutate: func(_ *usecasedraft.Execution, command *usecasedraft.PlayerActionCommand) {
					command.ActorID = initial.SecondParticipantID
				},
				want: domain.ErrDraftIllegalAction,
			},
			{
				name: "wrong turn",
				mutate: func(_ *usecasedraft.Execution, command *usecasedraft.PlayerActionCommand) {
					command.ExpectedTurn = 2
				},
				want: domain.ErrDraftStaleTurn,
			},
			{
				name: "stale revision",
				mutate: func(_ *usecasedraft.Execution, command *usecasedraft.PlayerActionCommand) {
					command.ExpectedRevision++
				},
				want: usecasedraft.ErrActionConflict,
			},
			{
				name: "ineligible category",
				mutate: func(_ *usecasedraft.Execution, command *usecasedraft.PlayerActionCommand) {
					command.Category = domain.CategoryPwn
				},
				want: domain.ErrDraftIllegalAction,
			},
			{
				name: "duplicate category",
				mutate: func(draft *usecasedraft.Execution, command *usecasedraft.PlayerActionCommand) {
					applied := task030ActionCommand(*draft, task030ID(30), domain.CategoryWeb)
					repository := newDraftRepositoryHarness(t, *draft)
					useCase := usecasedraft.NewActionUseCase(
						repository.mock,
						newDraftClock(t, startedAt.Add(time.Second)),
					)
					first, applyErr := useCase.Apply(t.Context(), applied)
					if applyErr != nil {
						t.Fatalf("prepare duplicate: %v", applyErr)
					}
					*draft = first.Draft
					command.ExpectedRevisionID = draft.RevisionID
					command.ExpectedRevision = draft.Revision
					command.ExpectedServiceEpoch = draft.ServiceEpoch
					command.ExpectedTurn = draft.Turn
					command.ActorID = *draft.CurrentActorID
					command.Category = domain.CategoryWeb
				},
				want: domain.ErrDraftCategoryUsed,
			},
			{
				name: "completed draft",
				mutate: func(draft *usecasedraft.Execution, command *usecasedraft.PlayerActionCommand) {
					*draft = cloneDraftExecution(completed)
					command.ExpectedRevisionID = draft.RevisionID
					command.ExpectedRevision = draft.Revision
					command.ExpectedServiceEpoch = draft.ServiceEpoch
				},
				want: domain.ErrDraftCompleted,
			},
		}

		for _, testCase := range cases {
			t.Run(testCase.name, func(t *testing.T) {
				draft := cloneDraftExecution(initial)
				command := base
				command.CommandID = uuid.New()
				command.ResultRevisionID = uuid.New()
				command.ActionID = uuid.New()
				testCase.mutate(&draft, &command)
				repository := newDraftRepositoryHarness(t, draft)
				useCase := usecasedraft.NewActionUseCase(
					repository.mock,
					newDraftClock(t, startedAt.Add(2*time.Second)),
				)
				if _, err := useCase.Apply(t.Context(), command); !errors.Is(err, testCase.want) {
					t.Fatalf("Apply() error = %v, want %v", err, testCase.want)
				}
			})
		}
	})

	t.Run("commits at most one competing command", func(t *testing.T) {
		t.Parallel()

		startedAt := time.Date(2026, time.August, 30, 13, 0, 0, 0, time.UTC)
		initial := task030Draft(t, startedAt)
		repository := newDraftRepositoryHarness(t, initial)
		useCase := usecasedraft.NewActionUseCase(repository.mock, newDraftClock(t, startedAt.Add(time.Second)))
		commands := []usecasedraft.PlayerActionCommand{
			task030ActionCommand(initial, task030ID(40), domain.CategoryWeb),
			task030ActionCommand(initial, task030ID(50), domain.CategoryCrypto),
		}

		start := make(chan struct{})
		var changed atomic.Int32
		var conflicts atomic.Int32
		var wait sync.WaitGroup
		for _, command := range commands {
			wait.Add(1)
			go func(command usecasedraft.PlayerActionCommand) {
				defer wait.Done()
				<-start
				result, err := useCase.Apply(context.Background(), command)
				switch {
				case err == nil && result.Changed:
					changed.Add(1)
				case errors.Is(err, usecasedraft.ErrActionConflict):
					conflicts.Add(1)
				default:
					t.Errorf("Apply() result = %+v, error = %v", result, err)
				}
			}(command)
		}
		close(start)
		wait.Wait()

		current, err := repository.mock.LoadDraft(t.Context(), initial.ID)
		if err != nil {
			t.Fatalf("LoadDraft() error = %v", err)
		}
		if changed.Load() != 1 || conflicts.Load() != 1 || current.Revision != 2 || len(current.Actions) != 1 {
			t.Fatalf("changed = %d, conflicts = %d, draft = %+v", changed.Load(), conflicts.Load(), current)
		}
	})
}

type draftRepositoryState struct {
	mu       sync.Mutex
	current  usecasedraft.Execution
	commands map[uuid.UUID]usecasedraft.Execution
	history  []usecasedraft.Execution
}

type draftRepositoryHarness struct {
	mock  *draftmocks.MockRepository
	state *draftRepositoryState
}

func newDraftRepositoryHarness(t *testing.T, initial usecasedraft.Execution) *draftRepositoryHarness {
	t.Helper()

	cloned := cloneDraftExecution(initial)
	state := &draftRepositoryState{
		current: cloned,
		commands: map[uuid.UUID]usecasedraft.Execution{
			initial.CommandID: cloned,
		},
		history: []usecasedraft.Execution{cloned},
	}
	repository := draftmocks.NewMockRepository(t)
	repository.EXPECT().LoadDraft(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, draftID uuid.UUID) (*usecasedraft.Execution, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			if state.current.ID != draftID {
				return nil, nil
			}
			cloned := cloneDraftExecution(state.current)
			return &cloned, nil
		}).Maybe()
	repository.EXPECT().FindDraftCommand(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(
			_ context.Context,
			draftID uuid.UUID,
			commandID uuid.UUID,
		) (*usecasedraft.Execution, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			record, ok := state.commands[commandID]
			if !ok || record.ID != draftID {
				return nil, nil
			}
			cloned := cloneDraftExecution(record)
			return &cloned, nil
		}).Maybe()
	repository.EXPECT().CommitDraftRevisions(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(
			_ context.Context,
			expected usecasedraft.RevisionExpectation,
			revisions []usecasedraft.Execution,
		) (*usecasedraft.Execution, bool, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			if state.current.RevisionID != expected.RevisionID || state.current.Revision != expected.Revision ||
				state.current.ServiceEpoch != expected.ServiceEpoch {
				return nil, false, domain.ErrConflict
			}
			if len(revisions) == 0 {
				return nil, false, domain.ErrValidation
			}
			current := state.current
			for _, revision := range revisions {
				if existing, ok := state.commands[revision.CommandID]; ok {
					cloned := cloneDraftExecution(existing)
					return &cloned, false, nil
				}
				if revision.ID != current.ID || revision.PreviousRevisionID != current.RevisionID ||
					revision.Revision != current.Revision+1 {
					return nil, false, domain.ErrConflict
				}
				current = cloneDraftExecution(revision)
				state.commands[revision.CommandID] = current
				state.history = append(state.history, current)
			}
			state.current = current
			cloned := cloneDraftExecution(current)
			return &cloned, true, nil
		}).Maybe()
	return &draftRepositoryHarness{mock: repository, state: state}
}

func newDraftClock(t *testing.T, at time.Time) *draftmocks.MockClock {
	t.Helper()

	clock := draftmocks.NewMockClock(t)
	clock.EXPECT().Now().Return(at).Maybe()
	return clock
}

func task030Draft(t *testing.T, startedAt time.Time) usecasedraft.Execution {
	t.Helper()
	revision := task029CategoryRevision(t, domain.TournamentStageSwiss, true, task030ID(1), startedAt.Add(-time.Minute))
	draft, err := usecasedraft.StartExecution(usecasedraft.ExecutionStartCommand{
		CategoryRevision:   revision,
		DraftID:            task030ID(2),
		InitialRevisionID:  task030ID(3),
		DecisionEvidenceID: task030ID(4),
		ParticipantIDs:     [2]uuid.UUID{task030ID(5), task030ID(6)},
		ServiceEpoch:       task030ID(7),
		CommandID:          task030ID(8),
		StartedAt:          startedAt,
	})
	if err != nil {
		t.Fatalf("StartDraftExecution() error = %v", err)
	}
	return draft
}

func task030ActionCommand(
	draft usecasedraft.Execution,
	baseID uuid.UUID,
	category domain.Category,
) usecasedraft.PlayerActionCommand {
	return usecasedraft.PlayerActionCommand{
		DraftID:              draft.ID,
		ExpectedRevisionID:   draft.RevisionID,
		ExpectedRevision:     draft.Revision,
		ExpectedServiceEpoch: draft.ServiceEpoch,
		ExpectedTurn:         draft.Turn,
		CommandID:            baseID,
		ResultRevisionID:     uuid.New(),
		ActionID:             uuid.New(),
		ActorID:              *draft.CurrentActorID,
		Action:               *draft.CurrentAction,
		Category:             category,
	}
}

func completeTask030Draft(
	t *testing.T,
	initial usecasedraft.Execution,
	startedAt time.Time,
) usecasedraft.Execution {
	t.Helper()
	repository := newDraftRepositoryHarness(t, initial)
	firstUseCase := usecasedraft.NewActionUseCase(repository.mock, newDraftClock(t, startedAt.Add(time.Second)))
	first, err := firstUseCase.Apply(
		t.Context(),
		task030ActionCommand(initial, task030ID(61), domain.CategoryWeb),
	)
	if err != nil {
		t.Fatalf("complete first draft turn: %v", err)
	}
	secondUseCase := usecasedraft.NewActionUseCase(repository.mock, newDraftClock(t, startedAt.Add(2*time.Second)))
	second, err := secondUseCase.Apply(
		t.Context(),
		task030ActionCommand(first.Draft, task030ID(62), domain.CategoryCrypto),
	)
	if err != nil {
		t.Fatalf("complete second draft turn: %v", err)
	}
	return second.Draft
}

func cloneDraftExecution(draft usecasedraft.Execution) usecasedraft.Execution {
	return usecasedraft.CloneExecution(draft)
}

func task030ID(number int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("30000000-0000-0000-0000-%012d", number))
}
