package notification

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	postgresqlc "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/notification"
)

type Repository struct {
	transactions *db.TxManager
}

func NewRepository(transactions *db.TxManager) *Repository {
	return &Repository{transactions: transactions}
}

func (r *Repository) ListActive(ctx context.Context, playerID uuid.UUID) ([]notification.Notification, error) {
	if r == nil || r.transactions == nil {
		return nil, errors.New("player notifications postgres: transaction manager unavailable")
	}
	rows, err := r.transactions.Querier(ctx).ListActivePlayerNotifications(ctx, playerID)
	if err != nil {
		return nil, err
	}
	items := make([]notification.Notification, 0, len(rows))
	for _, row := range rows {
		items = append(items, notification.Notification{
			ID:             row.ID,
			Type:           notification.Type(row.NotificationType),
			TournamentID:   row.TournamentID,
			TournamentName: row.TournamentName,
			CreatedAt:      row.CreatedAt.Time.UTC(),
			ExpiresAt:      row.ExpiresAt.Time.UTC(),
		})
	}
	return items, nil
}

func (r *Repository) DeleteExpired(ctx context.Context, batchSize int32) error {
	if r == nil || r.transactions == nil {
		return errors.New("player notifications postgres: transaction manager unavailable")
	}
	_, err := r.transactions.Querier(ctx).DeleteExpiredPlayerNotifications(ctx, batchSize)
	return err
}

func (r *Repository) CreatePlayerRemoved(
	ctx context.Context,
	playerID, tournamentID uuid.UUID,
) error {
	if r == nil || r.transactions == nil {
		return errors.New("player notifications postgres: transaction manager unavailable")
	}
	_, err := r.transactions.Querier(ctx).CreatePlayerRemovedNotification(
		ctx,
		postgresqlc.CreatePlayerRemovedNotificationParams{
			TournamentID: tournamentID,
			PlayerID:     playerID,
		},
	)
	return err
}
