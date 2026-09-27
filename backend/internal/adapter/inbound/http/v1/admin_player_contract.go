package v1

import (
	"context"

	playerusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player"
	"github.com/google/uuid"
)

type AdminPlayerService interface {
	ListPlayers(ctx context.Context, includeDeleted bool) ([]playerusecase.PlayerRecord, error)
	CreatePlayer(ctx context.Context, username string, actor playerusecase.Actor) (*playerusecase.PlayerRecord, error)
	ListPlayerAudit(ctx context.Context, id uuid.UUID, limit int32) ([]playerusecase.AuditEvent, error)
	UpdatePlayer(
		ctx context.Context,
		id uuid.UUID,
		in playerusecase.PlayerInput,
		actor playerusecase.Actor,
	) (*playerusecase.PlayerRecord, error)
	DeletePlayer(ctx context.Context, id uuid.UUID, actor playerusecase.Actor) error
}

type AdminPlayerEventSubscriber interface {
	SubscribeAdminPlayerChanges(ctx context.Context) (<-chan struct{}, func(), error)
}
