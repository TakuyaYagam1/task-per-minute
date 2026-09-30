package usecase

import (
	"context"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type RegisterPlayerCommand struct {
	Username string
	Email    string
	Password string
}

type LoginPlayerCommand struct {
	Login    string
	Password string
}

type PlayerAccountService interface {
	Register(ctx context.Context, command RegisterPlayerCommand) error
	Login(ctx context.Context, command LoginPlayerCommand) (*domain.Player, error)
	VerifyEmail(ctx context.Context, token string) error
	ResendVerification(ctx context.Context, email string) error
}
