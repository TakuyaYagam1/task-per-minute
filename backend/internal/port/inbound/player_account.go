package usecase

import (
	"context"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/google/uuid"
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
	ResendVerificationForLogin(ctx context.Context, command LoginPlayerCommand) error
}

type ChangeUsernameCommand struct {
	CurrentPassword string
	Username        string
}

type ChangePasswordCommand struct {
	CurrentPassword string
	NewPassword     string
}

type BeginEmailChangeCommand struct {
	CurrentPassword string
	NewEmail        string
}

type ConfirmEmailChangeCommand struct {
	Code string
}

type AccountSettingsService interface {
	GetAccountSettings(ctx context.Context, playerID, sessionToken uuid.UUID) (*domain.AccountSettings, error)
	ChangeUsername(ctx context.Context, playerID, sessionToken uuid.UUID, command ChangeUsernameCommand) (*domain.Player, error)
	ChangePassword(ctx context.Context, playerID, sessionToken uuid.UUID, command ChangePasswordCommand) (*domain.Player, error)
	BeginEmailChange(ctx context.Context, playerID, sessionToken uuid.UUID, command BeginEmailChangeCommand) (*domain.AccountSettings, error)
	ResendEmailChange(ctx context.Context, playerID, sessionToken uuid.UUID) (*domain.AccountSettings, error)
	CancelEmailChange(ctx context.Context, playerID, sessionToken uuid.UUID) (*domain.AccountSettings, error)
	ConfirmEmailChange(ctx context.Context, playerID, sessionToken uuid.UUID, command ConfirmEmailChangeCommand) (*domain.EmailChangeResult, error)
}
