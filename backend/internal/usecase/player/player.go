package player

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
)

var usernameRE = regexp.MustCompile(`^[a-zA-Z0-9_-]{2,50}$`)

type PlayerUsecase struct {
	tx      usecase.TxManager
	players usecase.PlayerRepo
	duels   usecase.DuelRepo
}

// PlayerWithActiveDuel aliases the canonical declaration in
// internal/usecase/contracts.go.
type PlayerWithActiveDuel = usecase.PlayerWithActiveDuel

func NewPlayerUsecase(tx usecase.TxManager, players usecase.PlayerRepo, duels usecase.DuelRepo) *PlayerUsecase {
	return &PlayerUsecase{tx: tx, players: players, duels: duels}
}

func (u *PlayerUsecase) Join(ctx context.Context, username string) (*domain.Player, error) {
	if !usernameRE.MatchString(username) {
		return nil, apperr.ErrUsernameInvalid
	}

	sessionToken := uuid.New()
	var joined *domain.Player

	if err := u.tx.Do(ctx, func(txCtx context.Context) error {
		updated, err := u.players.JoinByUsername(txCtx, username, sessionToken)
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
