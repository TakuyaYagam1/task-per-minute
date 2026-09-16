package pause_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	gamemocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/mocks"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause"
	"github.com/google/uuid"
)

func testNormalPauseGraphEntryErrors(t *testing.T, pausedAt time.Time) {
	t.Helper()

	t.Run("retries one fresh authority conflict and rejects command reuse", func(t *testing.T) {
		t.Parallel()

		authority, command := normalPauseFixture(pausedAt)
		repository := newNormalPauseRepositoryHarness(t, authority)
		repository.conflictOnce = true
		useCase := gameusecase.NewNormalPauseGraphUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, pausedAt))
		if _, changed, err := useCase.Enter(t.Context(), command); err != nil || !changed || repository.loadCount != 2 {
			t.Fatalf("retry error = %v, changed = %v, loads = %d", err, changed, repository.loadCount)
		}
		reused := command
		reused.PauseID = uuid.New()
		if _, changed, err := useCase.Enter(t.Context(), reused); !errors.Is(err, gameusecase.ErrNormalPauseCommandReuse) || changed {
			t.Fatalf("reuse error = %v, changed = %v", err, changed)
		}
	})

	t.Run("wraps repository failures", func(t *testing.T) {
		t.Parallel()

		authority, command := normalPauseFixture(pausedAt)
		cause := errors.New("storage unavailable")
		repository := newNormalPauseRepositoryHarness(t, authority)
		repository.loadErr = cause
		useCase := gameusecase.NewNormalPauseGraphUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, pausedAt))
		if _, _, err := useCase.Enter(t.Context(), command); !errors.Is(err, cause) {
			t.Fatalf("Enter() error = %v, want wrapped cause", err)
		}
	})
}

type normalPauseRepositoryHarness struct {
	*gamemocks.MockNormalPauseRepository

	mu                  sync.Mutex
	authority           gameusecase.NormalPauseAuthority
	commands            map[uuid.UUID]gameusecase.NormalPauseRecord
	conflictOnce        bool
	conflictsRemaining  int
	loadErr             error
	commitErr           error
	loadCount           int
	writes              int
	commitCalls         int
	loadStarted         chan struct{}
	loadRelease         chan struct{}
	loadOnce            sync.Once
	mutateCommitInPlace bool
}
