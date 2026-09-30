package player

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/google/uuid"
)

const defaultSessionTTL = 24 * time.Hour

type SessionUseCase struct {
	tx         SessionTransactionManager
	players    Repository
	sessionTTL time.Duration
	clock      SessionClock
}

type Option func(*SessionUseCase)

func WithSessionTTL(ttl time.Duration) Option {
	return func(u *SessionUseCase) {
		if ttl > 0 {
			u.sessionTTL = ttl
		}
	}
}

func SessionNewUseCase(tx SessionTransactionManager, players Repository, clk SessionClock, options ...Option) *SessionUseCase {
	u := &SessionUseCase{
		tx:         tx,
		players:    players,
		sessionTTL: defaultSessionTTL,
		clock:      clk,
	}
	for _, opt := range options {
		if opt != nil {
			opt(u)
		}
	}
	return u
}

func (u *SessionUseCase) GetCurrentPlayer(ctx context.Context, sessionToken uuid.UUID) (*domain.Player, error) {
	player, err := u.players.GetBySessionToken(ctx, sessionToken)
	if err != nil {
		if errors.Is(err, domain.ErrPlayerNotFound) {
			return nil, domain.ErrInvalidSession
		}
		return nil, fmt.Errorf("player session lookup: %w", err)
	}

	return player, nil
}

func (u *SessionUseCase) Logout(ctx context.Context, sessionToken uuid.UUID) error {
	return u.tx.Do(ctx, func(txCtx context.Context) error {
		player, err := u.players.GetBySessionToken(txCtx, sessionToken)
		if err != nil {
			if errors.Is(err, domain.ErrPlayerNotFound) {
				return nil
			}
			return fmt.Errorf("UseCase - Logout - Repository.GetBySessionToken: %w", err)
		}
		if player == nil {
			return nil
		}
		if _, err := u.players.UpdateSessionToken(txCtx, player.ID, sessionToken, nil, nil); err != nil {
			if errors.Is(err, domain.ErrPlayerNotFound) {
				return nil
			}
			return fmt.Errorf("UseCase - Logout - Repository.UpdateSessionToken: %w", err)
		}
		return nil
	})
}
