package game_test

import (
	"errors"
	"testing"
	"time"

	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	"github.com/google/uuid"
)

func testPauseResumeErrors(t *testing.T, resumedAt time.Time) {
	t.Helper()

	t.Run("retries fresh authority and rejects command reuse", func(t *testing.T) {
		t.Parallel()

		authority, command := pauseResumeFixture(t, resumedAt)
		repository := newPauseResumeRepositoryHarness(t, authority)
		repository.conflictOnce = true
		useCase := gameusecase.NewPauseResumeUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, resumedAt))
		if _, changed, err := useCase.Resume(t.Context(), command); err != nil || !changed || repository.loadCount != 2 {
			t.Fatalf("retry error = %v, changed = %v, loads = %d", err, changed, repository.loadCount)
		}
		reused := command
		reused.ActorID = uuid.New()
		if _, changed, err := useCase.Resume(t.Context(), reused); !errors.Is(err, gameusecase.ErrPauseResumeCommandReuse) || changed {
			t.Fatalf("reuse error = %v, changed = %v", err, changed)
		}
	})

	t.Run("wraps repository errors", func(t *testing.T) {
		t.Parallel()

		authority, command := pauseResumeFixture(t, resumedAt)
		cause := errors.New("resume storage unavailable")
		repository := newPauseResumeRepositoryHarness(t, authority)
		repository.loadErr = cause
		useCase := gameusecase.NewPauseResumeUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, resumedAt))
		if _, _, err := useCase.Resume(t.Context(), command); !errors.Is(err, cause) {
			t.Fatalf("Resume() error = %v, want wrapped cause", err)
		}
	})
}
