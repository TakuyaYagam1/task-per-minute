package middleware

import (
	"context"

	authusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/auth"
)

type AdminAccessVerifier interface {
	VerifyAccess(ctx context.Context, token string) (*authusecase.Claims, error)
}
