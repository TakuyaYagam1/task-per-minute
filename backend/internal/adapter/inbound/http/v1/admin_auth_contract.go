package v1

import (
	"context"

	authusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/auth"
)

type AdminAuthService interface {
	Login(ctx context.Context, password string) (*authusecase.TokenPair, error)
	Refresh(ctx context.Context, refreshToken string) (*authusecase.TokenPair, error)
	Logout(ctx context.Context, refreshToken string, accessTokens ...string) error
}
