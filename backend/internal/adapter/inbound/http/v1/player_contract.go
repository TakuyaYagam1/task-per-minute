package v1

import (
	"context"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type PlayerService interface {
	Join(ctx context.Context, username string) (*domain.Player, error)
	GetCurrentPlayer(ctx context.Context, sessionToken uuid.UUID) (*domain.Player, error)
	Logout(ctx context.Context, sessionToken uuid.UUID) error
}
