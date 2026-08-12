package middleware

import (
	"context"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	adminusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/admin"
)

type AdminAccessVerifier interface {
	VerifyAccess(ctx context.Context, token string) (*adminusecase.Claims, error)
}

type PlayerSessionReader interface {
	GetBySessionToken(ctx context.Context, token uuid.UUID) (*domain.Player, error)
}
