package enter_test

import (
	"context"
	"errors"
	"testing"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	enterusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause/enter"
)

func TestNormalPauseGraphUseCaseRejectsMissingDependencies(t *testing.T) {
	useCase := enterusecase.NewNormalPauseGraphUseCase(nil, nil, nil)
	_, changed, err := useCase.Enter(context.Background(), enterusecase.NormalPauseCommand{})
	if !errors.Is(err, domain.ErrValidation) || changed {
		t.Fatalf("Enter() error = %v, changed = %v", err, changed)
	}
}
