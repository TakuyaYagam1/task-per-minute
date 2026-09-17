package resume_test

import (
	"errors"
	"testing"

	resumeusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause/resume"
)

func TestValidatePauseResumeCommandRejectsZeroIdentity(t *testing.T) {
	if err := resumeusecase.ValidatePauseResumeCommand(resumeusecase.PauseResumeCommand{}); !errors.Is(err, resumeusecase.ErrInvalidPauseResume) {
		t.Fatalf("ValidatePauseResumeCommand() error = %v", err)
	}
}
