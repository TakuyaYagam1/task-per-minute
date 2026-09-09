package game_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	draftmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft/mocks"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
)

func task029CategoryRevision(
	t *testing.T,
	stage domain.TournamentStage,
	draftMode bool,
	id uuid.UUID,
	createdAt time.Time,
) draftusecase.CategoryRevision {
	t.Helper()

	format := domain.SeriesFormatBO1
	mode := domain.CategoryModeRandom
	categories := []domain.Category{
		domain.CategoryCrypto,
		domain.CategoryReverse,
		domain.CategoryWeb,
	}
	if draftMode {
		mode = domain.CategoryModeDraft
	}
	if stage == domain.TournamentStageFinal {
		format = domain.SeriesFormatBO3
		mode = domain.CategoryModeDraft
		categories = []domain.Category{
			domain.CategoryCrypto,
			domain.CategoryForensics,
			domain.CategoryPwn,
			domain.CategoryReverse,
			domain.CategoryWeb,
		}
	}
	revision := draftusecase.CategoryRevision{
		ID: id, TournamentID: task029FixtureID(1), SeriesID: task029FixtureID(100),
		RosterID: task029FixtureID(101), Revision: 1, Stage: stage, Format: format,
		Mode: mode, SourceContentRevision: 1,
		CategoryPool: domain.CategoryPoolRevision{
			ID: task029FixtureID(21), Revision: 1, Format: format, Categories: categories,
		},
		CreatedAt: createdAt,
	}
	if err := revision.Validate(); err != nil {
		t.Fatalf("build category revision fixture: %v", err)
	}
	return revision
}

func task030Draft(t *testing.T, startedAt time.Time) draftusecase.Execution {
	t.Helper()

	revision := task029CategoryRevision(
		t,
		domain.TournamentStageSwiss,
		true,
		task030ID(1),
		startedAt.Add(-time.Minute),
	)
	draft, err := draftusecase.StartExecution(draftusecase.ExecutionStartCommand{
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
	draft draftusecase.Execution,
	baseID uuid.UUID,
	category domain.Category,
) draftusecase.PlayerActionCommand {
	return draftusecase.PlayerActionCommand{
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
	initial draftusecase.Execution,
	startedAt time.Time,
) draftusecase.Execution {
	t.Helper()

	repository := newPauseDraftRepositoryHarness(t, initial)
	firstUseCase := draftusecase.NewActionUseCase(
		repository.mock,
		newPauseDraftClock(t, startedAt.Add(time.Second)),
	)
	first, err := firstUseCase.Apply(
		t.Context(),
		task030ActionCommand(initial, task030ID(61), domain.CategoryWeb),
	)
	if err != nil {
		t.Fatalf("complete first draft turn: %v", err)
	}
	secondUseCase := draftusecase.NewActionUseCase(
		repository.mock,
		newPauseDraftClock(t, startedAt.Add(2*time.Second)),
	)
	second, err := secondUseCase.Apply(
		t.Context(),
		task030ActionCommand(first.Draft, task030ID(62), domain.CategoryCrypto),
	)
	if err != nil {
		t.Fatalf("complete second draft turn: %v", err)
	}
	return second.Draft
}

type pauseDraftRepositoryState struct {
	mu       sync.Mutex
	current  draftusecase.Execution
	commands map[uuid.UUID]draftusecase.Execution
}

type pauseDraftRepositoryHarness struct {
	mock  *draftmocks.MockRepository
	state *pauseDraftRepositoryState
}

func newPauseDraftRepositoryHarness(
	t *testing.T,
	initial draftusecase.Execution,
) *pauseDraftRepositoryHarness {
	t.Helper()

	cloned := draftusecase.CloneExecution(initial)
	state := &pauseDraftRepositoryState{
		current: cloned,
		commands: map[uuid.UUID]draftusecase.Execution{
			initial.CommandID: cloned,
		},
	}
	repository := draftmocks.NewMockRepository(t)
	repository.EXPECT().LoadDraft(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, draftID uuid.UUID) (*draftusecase.Execution, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			if state.current.ID != draftID {
				return nil, nil
			}
			current := draftusecase.CloneExecution(state.current)
			return &current, nil
		}).Maybe()
	repository.EXPECT().FindDraftCommand(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(
			_ context.Context,
			draftID uuid.UUID,
			commandID uuid.UUID,
		) (*draftusecase.Execution, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			recorded, ok := state.commands[commandID]
			if !ok || recorded.ID != draftID {
				return nil, nil
			}
			result := draftusecase.CloneExecution(recorded)
			return &result, nil
		}).Maybe()
	repository.EXPECT().CommitDraftRevisions(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(
			_ context.Context,
			expected draftusecase.RevisionExpectation,
			revisions []draftusecase.Execution,
		) (*draftusecase.Execution, bool, error) {
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
				if recorded, ok := state.commands[revision.CommandID]; ok {
					result := draftusecase.CloneExecution(recorded)
					return &result, false, nil
				}
				if revision.ID != current.ID || revision.PreviousRevisionID != current.RevisionID ||
					revision.Revision != current.Revision+1 {
					return nil, false, domain.ErrConflict
				}
				current = draftusecase.CloneExecution(revision)
				state.commands[revision.CommandID] = current
			}
			state.current = current
			result := draftusecase.CloneExecution(current)
			return &result, true, nil
		}).Maybe()
	return &pauseDraftRepositoryHarness{mock: repository, state: state}
}

func newPauseDraftClock(t *testing.T, at time.Time) *draftmocks.MockClock {
	t.Helper()

	clock := draftmocks.NewMockClock(t)
	clock.EXPECT().Now().Return(at).Maybe()
	return clock
}

func task029FixtureID(number int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("29000000-0000-0000-0000-%012d", number))
}

func task030ID(number int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("30000000-0000-0000-0000-%012d", number))
}
