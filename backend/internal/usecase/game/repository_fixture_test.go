package game_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/mock"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	gamemocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/mocks"
)

type forfeitResult struct {
	resolution *gameusecase.ForfeitResolution
	changed    bool
	err        error
}

type forfeitRepositoryState struct {
	mu        sync.Mutex
	authority gameusecase.ForfeitAuthority
	barrier   *sync.WaitGroup
	writes    int
}

type forfeitRepositoryHarness struct {
	repository *gamemocks.MockForfeitRepository
	state      *forfeitRepositoryState
}

func newForfeitRepositoryHarness(
	t *testing.T,
	authority gameusecase.ForfeitAuthority,
	contenders int,
) *forfeitRepositoryHarness {
	t.Helper()

	state := &forfeitRepositoryState{authority: cloneForfeitAuthority(authority)}
	if contenders > 0 {
		state.barrier = &sync.WaitGroup{}
		state.barrier.Add(contenders)
	}
	repository := gamemocks.NewMockForfeitRepository(t)
	repository.EXPECT().LoadForfeitAuthority(mock.Anything, mock.Anything).
		RunAndReturn(func(context.Context, gameusecase.Scope) (gameusecase.ForfeitAuthority, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			return cloneForfeitAuthority(state.authority), nil
		}).Maybe()
	repository.EXPECT().CommitForfeitResolution(mock.Anything, mock.Anything).
		RunAndReturn(func(
			_ context.Context,
			resolution gameusecase.ForfeitResolution,
		) (*gameusecase.ForfeitResolution, bool, error) {
			if state.barrier != nil {
				state.barrier.Done()
				state.barrier.Wait()
			}
			state.mu.Lock()
			defer state.mu.Unlock()
			if resolution.ExpectedAuthorityRevision != state.authority.Revision ||
				state.authority.Current != nil {
				return nil, false, domain.ErrConflict
			}
			stored := cloneForfeitResolution(resolution)
			state.authority.Revision++
			state.authority.Series = seriesdomain.CloneExecution(resolution.Series)
			state.authority.CurrentSeriesResultOrdinal = resolution.SeriesRevision.Ordinal
			state.authority.Current = &stored
			state.writes++
			result := cloneForfeitResolution(stored)
			return &result, true, nil
		}).Maybe()
	return &forfeitRepositoryHarness{repository: repository, state: state}
}

func (h *forfeitRepositoryHarness) writeCount() int {
	h.state.mu.Lock()
	defer h.state.mu.Unlock()
	return h.state.writes
}
