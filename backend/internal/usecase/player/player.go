package player

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
	"github.com/TakuyaYagam1/task-per-minute/pkg/clock"
)

var usernameRE = regexp.MustCompile(`^[a-zA-Z0-9_-]{2,50}$`)

const defaultSessionTTL = 24 * time.Hour

type PlayerUsecase struct {
	tx         usecase.TxManager
	players    usecase.PlayerRepo
	duels      usecase.DuelRepo
	sessionTTL time.Duration
	clock      clock.Clock
}

type Option func(*PlayerUsecase)

func WithSessionTTL(ttl time.Duration) Option {
	return func(u *PlayerUsecase) {
		if ttl > 0 {
			u.sessionTTL = ttl
		}
	}
}

func WithClock(clk clock.Clock) Option {
	return func(u *PlayerUsecase) {
		if clk != nil {
			u.clock = clk
		}
	}
}

// PlayerWithActiveDuel aliases the canonical declaration in
// internal/usecase/contracts.go.
type PlayerWithActiveDuel = usecase.PlayerWithActiveDuel

func NewPlayerUsecase(tx usecase.TxManager, players usecase.PlayerRepo, duels usecase.DuelRepo, options ...Option) *PlayerUsecase {
	u := &PlayerUsecase{
		tx:         tx,
		players:    players,
		duels:      duels,
		sessionTTL: defaultSessionTTL,
		clock:      clock.Real{},
	}
	for _, opt := range options {
		if opt != nil {
			opt(u)
		}
	}
	return u
}

func (u *PlayerUsecase) Join(ctx context.Context, username string) (*domain.Player, error) {
	if !usernameRE.MatchString(username) {
		return nil, apperr.ErrUsernameInvalid
	}

	sessionToken := uuid.New()
	sessionExpiresAt := u.clock.Now().Add(u.sessionTTL)
	var joined *domain.Player

	if err := u.tx.Do(ctx, func(txCtx context.Context) error {
		updated, err := u.players.JoinByUsername(txCtx, username, sessionToken, sessionExpiresAt)
		if err != nil {
			return fmt.Errorf("PlayerUsecase - Join - PlayerRepo.JoinByUsername: %w", err)
		}
		if updated.Status == domain.PlayerStatusInDuel {
			return apperr.ErrPlayerInDuel
		}
		joined = updated
		return nil
	}); err != nil {
		return nil, err
	}

	return joined, nil
}

func (u *PlayerUsecase) GetMe(ctx context.Context, sessionToken uuid.UUID) (*PlayerWithActiveDuel, error) {
	player, err := u.players.GetBySessionToken(ctx, sessionToken)
	if err != nil {
		if errors.Is(err, apperr.ErrPlayerNotFound) {
			return nil, apperr.ErrInvalidSession
		}
		return nil, fmt.Errorf("PlayerUsecase - GetMe - PlayerRepo.GetBySessionToken: %w", err)
	}

	activeDuel, err := u.duels.GetActiveByPlayerID(ctx, player.ID)
	if err != nil {
		return nil, fmt.Errorf("PlayerUsecase - GetMe - DuelRepo.GetActiveByPlayerID: %w", err)
	}

	return &PlayerWithActiveDuel{Player: player, ActiveDuel: activeDuel}, nil
}

func (u *PlayerUsecase) Logout(ctx context.Context, sessionToken uuid.UUID) error {
	return u.tx.Do(ctx, func(txCtx context.Context) error {
		player, err := u.players.GetBySessionToken(txCtx, sessionToken)
		if err != nil {
			if errors.Is(err, apperr.ErrPlayerNotFound) {
				return nil
			}
			return fmt.Errorf("PlayerUsecase - Logout - PlayerRepo.GetBySessionToken: %w", err)
		}
		if player == nil {
			return nil
		}
		if _, err := u.players.UpdateSessionToken(txCtx, player.ID, nil, nil); err != nil {
			return fmt.Errorf("PlayerUsecase - Logout - PlayerRepo.UpdateSessionToken: %w", err)
		}
		return nil
	})
}
