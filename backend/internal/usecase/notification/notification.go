package notification

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

const (
	NotificationRetention = 24 * time.Hour
	expiredCleanupBatch   = 100
)

type Type string

const TypeTournamentPlayerRemoved Type = "tournament_player_removed"

type Notification struct {
	ID             uuid.UUID
	Type           Type
	TournamentID   uuid.UUID
	TournamentName string
	CreatedAt      time.Time
	ExpiresAt      time.Time
}

type Repository interface {
	ListActive(ctx context.Context, playerID uuid.UUID) ([]Notification, error)
	DeleteExpired(ctx context.Context, batchSize int32) error
	CreatePlayerRemoved(ctx context.Context, playerID, tournamentID uuid.UUID) error
}

type TransactionManager interface {
	Do(ctx context.Context, fn func(context.Context) error) error
}

type Subscriber interface {
	SubscribePlayerNotifications(ctx context.Context, playerID uuid.UUID) (<-chan struct{}, func(), error)
}

type PlayerNotifications interface {
	List(ctx context.Context, playerID uuid.UUID) ([]Notification, error)
	Subscribe(ctx context.Context, playerID uuid.UUID) (<-chan struct{}, func(), error)
}

type RemovalRecorder interface {
	RecordTournamentPlayerRemoved(ctx context.Context, playerID, tournamentID uuid.UUID) error
}

type Service struct {
	transactions TransactionManager
	repository   Repository
	subscriber   Subscriber
}

func New(transactions TransactionManager, repository Repository, subscriber Subscriber) *Service {
	return &Service{
		transactions: transactions,
		repository:   repository,
		subscriber:   subscriber,
	}
}

func (s *Service) List(ctx context.Context, playerID uuid.UUID) ([]Notification, error) {
	if s == nil || s.repository == nil {
		return nil, errors.New("player notifications: repository unavailable")
	}
	return s.repository.ListActive(ctx, playerID)
}

func (s *Service) Subscribe(
	ctx context.Context,
	playerID uuid.UUID,
) (<-chan struct{}, func(), error) {
	if s == nil || s.subscriber == nil {
		return nil, nil, errors.New("player notifications: subscriber unavailable")
	}
	return s.subscriber.SubscribePlayerNotifications(ctx, playerID)
}

// RecordTournamentPlayerRemoved inserts inside the caller's active transaction
// when one is present. The PostgreSQL transaction manager reuses its context,
// and the repository snapshots the tournament name before the transaction ends.
func (s *Service) RecordTournamentPlayerRemoved(
	ctx context.Context,
	playerID, tournamentID uuid.UUID,
) error {
	if s == nil || s.transactions == nil || s.repository == nil {
		return errors.New("player notifications: writer unavailable")
	}
	return s.transactions.Do(ctx, func(txCtx context.Context) error {
		return s.repository.CreatePlayerRemoved(txCtx, playerID, tournamentID)
	})
}
