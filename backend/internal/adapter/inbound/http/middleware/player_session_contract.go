package middleware

import (
	"context"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type PlayerSessionReader interface {
	GetBySessionToken(ctx context.Context, token uuid.UUID) (*domain.Player, error)
}
