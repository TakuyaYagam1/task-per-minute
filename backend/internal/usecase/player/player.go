package player

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/google/uuid"
)

var usernameRE = regexp.MustCompile(`^[a-zA-Z0-9_-]{2,50}$`)

const defaultSessionTTL = 24 * time.Hour

type UseCase struct {
	tx         TransactionManager
	players    Repository
	duels      ActiveDuelReader
	sessionTTL time.Duration
	clock      Clock
}

type Option func(*UseCase)

func WithSessionTTL(ttl time.Duration) Option {
	return func(u *UseCase) {
		if ttl > 0 {
			u.sessionTTL = ttl
		}
	}
}

func NewUseCase(tx TransactionManager, players Repository, duels ActiveDuelReader, clk Clock, options ...Option) *UseCase {
	u := &UseCase{
		tx:         tx,
		players:    players,
		duels:      duels,
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

func (u *UseCase) Join(ctx context.Context, username string) (*domain.Player, error) {
	if !usernameRE.MatchString(username) {
		return nil, domain.ErrUsernameInvalid
	}

	sessionToken := uuid.New()
	sessionExpiresAt := u.clock.Now().Add(u.sessionTTL)
	var joined *domain.Player

	if err := u.tx.Do(ctx, func(txCtx context.Context) error {
		updated, err := u.players.JoinByUsername(txCtx, username, sessionToken, sessionExpiresAt)
		if err != nil {
			return fmt.Errorf("UseCase - Join - Repository.JoinByUsername: %w", err)
		}
		if updated.Status == domain.PlayerStatusInDuel {
			return domain.ErrPlayerInDuel
		}
		joined = updated
		return nil
	}); err != nil {
		return nil, err
	}

	return joined, nil
}

func (u *UseCase) GetMe(ctx context.Context, sessionToken uuid.UUID) (*PlayerWithActiveDuel, error) {
	player, err := u.players.GetBySessionToken(ctx, sessionToken)
	if err != nil {
		if errors.Is(err, domain.ErrPlayerNotFound) {
			return nil, domain.ErrInvalidSession
		}
		return nil, fmt.Errorf("UseCase - GetMe - Repository.GetBySessionToken: %w", err)
	}

	activeDuel, err := u.duels.GetActiveByPlayerID(ctx, player.ID)
	if err != nil {
		return nil, fmt.Errorf("UseCase - GetMe - ActiveDuelReader.GetActiveByPlayerID: %w", err)
	}

	return &PlayerWithActiveDuel{Player: player, ActiveDuel: activeDuel}, nil
}

func (u *UseCase) Logout(ctx context.Context, sessionToken uuid.UUID) error {
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
		if _, err := u.players.UpdateSessionToken(txCtx, player.ID, nil, nil); err != nil {
			return fmt.Errorf("UseCase - Logout - Repository.UpdateSessionToken: %w", err)
		}
		return nil
	})
}
