package arena_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	arena "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
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
			initial.CurrentAction == nil || *initial.CurrentAction != domain.ArenaDraftActionBan ||
			initial.FirstParticipantID.String() != replayed[0] || len(initial.LegalCategories) != 3 {
			t.Fatalf("initial draft evidence = %+v, replay = %v", initial, replayed)
		}

		repository := newDraftRepositoryFake(initial)
		useCase := arena.NewDraftActionUseCase(repository, task030Clock{at: startedAt.Add(2 * time.Second)})
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
			mutate func(*arena.DraftExecution, *arena.DraftPlayerActionCommand)
			want   error
		}{
			{
				name: "wrong owner",
				mutate: func(_ *arena.DraftExecution, command *arena.DraftPlayerActionCommand) {
					command.ActorID = initial.SecondParticipantID
				},
				want: domain.ErrArenaDraftIllegalAction,
			},
			{
				name: "wrong turn",
				mutate: func(_ *arena.DraftExecution, command *arena.DraftPlayerActionCommand) {
					command.ExpectedTurn = 2
				},
				want: domain.ErrArenaDraftStaleTurn,
			},
			{
				name: "stale revision",
				mutate: func(_ *arena.DraftExecution, command *arena.DraftPlayerActionCommand) {
					command.ExpectedRevision++
				},
				want: arena.ErrDraftActionConflict,
			},
			{
				name: "ineligible category",
				mutate: func(_ *arena.DraftExecution, command *arena.DraftPlayerActionCommand) {
					command.Category = domain.CategoryPwn
				},
				want: domain.ErrArenaDraftIllegalAction,
			},
			{
				name: "duplicate category",
				mutate: func(draft *arena.DraftExecution, command *arena.DraftPlayerActionCommand) {
					applied := task030ActionCommand(*draft, task030ID(30), domain.CategoryWeb)
					useCase := arena.NewDraftActionUseCase(
						newDraftRepositoryFake(*draft),
						task030Clock{at: startedAt.Add(time.Second)},
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
				want: domain.ErrArenaDraftCategoryUsed,
			},
			{
				name: "completed draft",
				mutate: func(draft *arena.DraftExecution, command *arena.DraftPlayerActionCommand) {
					*draft = cloneDraftExecution(completed)
					command.ExpectedRevisionID = draft.RevisionID
					command.ExpectedRevision = draft.Revision
					command.ExpectedServiceEpoch = draft.ServiceEpoch
				},
				want: domain.ErrArenaDraftCompleted,
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
				useCase := arena.NewDraftActionUseCase(
					newDraftRepositoryFake(draft),
					task030Clock{at: startedAt.Add(2 * time.Second)},
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
		repository := newDraftRepositoryFake(initial)
		useCase := arena.NewDraftActionUseCase(repository, task030Clock{at: startedAt.Add(time.Second)})
		commands := []arena.DraftPlayerActionCommand{
			task030ActionCommand(initial, task030ID(40), domain.CategoryWeb),
			task030ActionCommand(initial, task030ID(50), domain.CategoryCrypto),
		}

		start := make(chan struct{})
		var changed atomic.Int32
		var conflicts atomic.Int32
		var wait sync.WaitGroup
		for _, command := range commands {
			wait.Add(1)
			go func(command arena.DraftPlayerActionCommand) {
				defer wait.Done()
				<-start
				result, err := useCase.Apply(context.Background(), command)
				switch {
				case err == nil && result.Changed:
					changed.Add(1)
				case errors.Is(err, arena.ErrDraftActionConflict):
					conflicts.Add(1)
				default:
					t.Errorf("Apply() result = %+v, error = %v", result, err)
				}
			}(command)
		}
		close(start)
		wait.Wait()

		current, err := repository.LoadDraft(t.Context(), initial.ID)
		if err != nil {
			t.Fatalf("LoadDraft() error = %v", err)
		}
		if changed.Load() != 1 || conflicts.Load() != 1 || current.Revision != 2 || len(current.Actions) != 1 {
			t.Fatalf("changed = %d, conflicts = %d, draft = %+v", changed.Load(), conflicts.Load(), current)
		}
	})
}

type task030Clock struct {
	at time.Time
}

func (c task030Clock) Now() time.Time {
	return c.at
}

type draftRepositoryFake struct {
	mu       sync.Mutex
	current  arena.DraftExecution
	commands map[uuid.UUID]arena.DraftExecution
	history  []arena.DraftExecution
}

func newDraftRepositoryFake(initial arena.DraftExecution) *draftRepositoryFake {
	cloned := cloneDraftExecution(initial)
	return &draftRepositoryFake{
		current: cloned,
		commands: map[uuid.UUID]arena.DraftExecution{
			initial.CommandID: cloned,
		},
		history: []arena.DraftExecution{cloned},
	}
}

func (f *draftRepositoryFake) LoadDraft(_ context.Context, draftID uuid.UUID) (*arena.DraftExecution, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.current.ID != draftID {
		return nil, nil
	}
	cloned := cloneDraftExecution(f.current)
	return &cloned, nil
}

func (f *draftRepositoryFake) FindDraftCommand(
	_ context.Context,
	draftID uuid.UUID,
	commandID uuid.UUID,
) (*arena.DraftExecution, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	record, ok := f.commands[commandID]
	if !ok || record.ID != draftID {
		return nil, nil
	}
	cloned := cloneDraftExecution(record)
	return &cloned, nil
}

func (f *draftRepositoryFake) CommitDraftRevisions(
	_ context.Context,
	expected arena.DraftRevisionExpectation,
	revisions []arena.DraftExecution,
) (*arena.DraftExecution, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.current.RevisionID != expected.RevisionID || f.current.Revision != expected.Revision ||
		f.current.ServiceEpoch != expected.ServiceEpoch {
		return nil, false, domain.ErrConflict
	}
	if len(revisions) == 0 {
		return nil, false, domain.ErrValidation
	}
	current := f.current
	for _, revision := range revisions {
		if existing, ok := f.commands[revision.CommandID]; ok {
			cloned := cloneDraftExecution(existing)
			return &cloned, false, nil
		}
		if revision.ID != current.ID || revision.PreviousRevisionID != current.RevisionID ||
			revision.Revision != current.Revision+1 {
			return nil, false, domain.ErrConflict
		}
		current = cloneDraftExecution(revision)
		f.commands[revision.CommandID] = current
		f.history = append(f.history, current)
	}
	f.current = current
	cloned := cloneDraftExecution(current)
	return &cloned, true, nil
}

func task030Draft(t *testing.T, startedAt time.Time) arena.DraftExecution {
	t.Helper()
	revision := task029CategoryRevision(t, arena.ArenaStageSwiss, true, task030ID(1), startedAt.Add(-time.Minute))
	draft, err := arena.StartDraftExecution(arena.DraftExecutionStartCommand{
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
	draft arena.DraftExecution,
	baseID uuid.UUID,
	category domain.Category,
) arena.DraftPlayerActionCommand {
	return arena.DraftPlayerActionCommand{
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
	initial arena.DraftExecution,
	startedAt time.Time,
) arena.DraftExecution {
	t.Helper()
	repository := newDraftRepositoryFake(initial)
	firstUseCase := arena.NewDraftActionUseCase(repository, task030Clock{at: startedAt.Add(time.Second)})
	first, err := firstUseCase.Apply(
		t.Context(),
		task030ActionCommand(initial, task030ID(61), domain.CategoryWeb),
	)
	if err != nil {
		t.Fatalf("complete first draft turn: %v", err)
	}
	secondUseCase := arena.NewDraftActionUseCase(repository, task030Clock{at: startedAt.Add(2 * time.Second)})
	second, err := secondUseCase.Apply(
		t.Context(),
		task030ActionCommand(first.Draft, task030ID(62), domain.CategoryCrypto),
	)
	if err != nil {
		t.Fatalf("complete second draft turn: %v", err)
	}
	return second.Draft
}

func cloneDraftExecution(draft arena.DraftExecution) arena.DraftExecution {
	cloned := draft
	cloned.Pool = append([]domain.Category(nil), draft.Pool...)
	cloned.LegalCategories = append([]domain.Category(nil), draft.LegalCategories...)
	cloned.Actions = append([]arena.DraftActionRecord(nil), draft.Actions...)
	for index := range cloned.Actions {
		if draft.Actions[index].DecisionEvidence == nil {
			continue
		}
		evidence := *draft.Actions[index].DecisionEvidence
		evidence.NormalizedInputs = append([]string(nil), evidence.NormalizedInputs...)
		evidence.Result = append([]string(nil), evidence.Result...)
		cloned.Actions[index].DecisionEvidence = &evidence
	}
	cloned.SelectedCategories = append([]domain.Category(nil), draft.SelectedCategories...)
	cloned.CurrentActorID = cloneTask030UUID(draft.CurrentActorID)
	cloned.CurrentAction = cloneTask030Action(draft.CurrentAction)
	cloned.AbsoluteDeadline = cloneTask030Time(draft.AbsoluteDeadline)
	cloned.FirstActorDecision.NormalizedInputs = append(
		[]string(nil),
		draft.FirstActorDecision.NormalizedInputs...,
	)
	cloned.FirstActorDecision.Result = append([]string(nil), draft.FirstActorDecision.Result...)
	if draft.Recovery != nil {
		recovery := *draft.Recovery
		cloned.Recovery = &recovery
	}
	if draft.Transition != nil {
		transition := *draft.Transition
		cloned.Transition = &transition
	}
	return cloned
}

func cloneTask030UUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneTask030Action(value *domain.ArenaDraftActionType) *domain.ArenaDraftActionType {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneTask030Time(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func task030ID(number int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("30000000-0000-0000-0000-%012d", number))
}
