package admin

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/google/uuid"
)

var adminUsernameRE = regexp.MustCompile(`^[a-zA-Z0-9_-]{2,50}$`)

const (
	defaultAdminPlayerAuditLimit = int32(50)
	maxAdminPlayerAuditLimit     = int32(200)
)

type PlayerUseCase struct {
	tx          TransactionManager
	players     PlayerRepository
	leaderboard LeaderboardInvalidator
	clock       Clock
}

func NewPlayerUseCase(
	tx TransactionManager,
	players PlayerRepository,
	leaderboard LeaderboardInvalidator,
	clk Clock,
) *PlayerUseCase {
	return &PlayerUseCase{
		tx:          tx,
		players:     players,
		leaderboard: leaderboard,
		clock:       clk,
	}
}

func (u *PlayerUseCase) ListPlayers(ctx context.Context, includeDeleted bool) ([]PlayerRecord, error) {
	players, err := u.players.ListAdminPlayers(ctx, includeDeleted)
	if err != nil {
		return nil, fmt.Errorf("AdminPlayerUsecase - ListPlayers - AdminPlayerRepo.ListAdminPlayers: %w", err)
	}
	return players, nil
}

func (u *PlayerUseCase) ListPlayerAudit(
	ctx context.Context,
	id uuid.UUID,
	limit int32,
) ([]PlayerAuditEvent, error) {
	if limit <= 0 {
		limit = defaultAdminPlayerAuditLimit
	}
	if limit > maxAdminPlayerAuditLimit {
		limit = maxAdminPlayerAuditLimit
	}
	if _, err := u.players.GetAdminPlayerIncludingDeleted(ctx, id); err != nil {
		return nil, fmt.Errorf("AdminPlayerUsecase - ListPlayerAudit - AdminPlayerRepo.GetAdminPlayerIncludingDeleted: %w", err)
	}
	events, err := u.players.ListAdminPlayerAudit(ctx, id, limit)
	if err != nil {
		return nil, fmt.Errorf("AdminPlayerUsecase - ListPlayerAudit - AdminPlayerRepo.ListAdminPlayerAudit: %w", err)
	}
	return events, nil
}

func (u *PlayerUseCase) UpdatePlayer(
	ctx context.Context,
	id uuid.UUID,
	in PlayerInput,
	actor Actor,
) (*PlayerRecord, error) {
	if err := validateAdminPlayerInput(in); err != nil {
		return nil, err
	}
	if err := validateAdminActor(actor); err != nil {
		return nil, err
	}

	var updated *PlayerRecord
	now := u.clock.Now()
	if err := u.tx.Do(ctx, func(txCtx context.Context) error {
		before, err := u.players.GetAdminPlayer(txCtx, id)
		if err != nil {
			return fmt.Errorf("AdminPlayerUsecase - UpdatePlayer - AdminPlayerRepo.GetAdminPlayer: %w", err)
		}
		if err := u.players.UpdateAdminPlayerUsername(txCtx, id, in.Username); err != nil {
			return fmt.Errorf("AdminPlayerUsecase - UpdatePlayer - AdminPlayerRepo.UpdateAdminPlayerUsername: %w", err)
		}
		if err := u.players.UpsertAdminPlayerStats(txCtx, id, PlayerStatsInput{
			Wins:               in.Wins,
			AverageSolveTimeMs: in.AverageSolveTimeMs,
		}, now); err != nil {
			return fmt.Errorf("AdminPlayerUsecase - UpdatePlayer - AdminPlayerRepo.UpsertAdminPlayerStats: %w", err)
		}

		player, err := u.players.GetAdminPlayer(txCtx, id)
		if err != nil {
			return fmt.Errorf("AdminPlayerUsecase - UpdatePlayer - AdminPlayerRepo.GetAdminPlayer updated: %w", err)
		}
		if err := u.players.CreateAdminPlayerAudit(txCtx, PlayerAuditInput{
			Actor:       actor,
			Action:      PlayerAuditActionUpdate,
			PlayerID:    id,
			BeforeState: adminPlayerAuditState(*before, false),
			AfterState:  adminPlayerAuditState(*player, false),
			CreatedAt:   now,
		}); err != nil {
			return fmt.Errorf("AdminPlayerUsecase - UpdatePlayer - AdminPlayerRepo.CreateAdminPlayerAudit: %w", err)
		}
		updated = player
		return nil
	}); err != nil {
		return nil, err
	}

	u.invalidateLeaderboard()
	return updated, nil
}

func (u *PlayerUseCase) DeletePlayer(ctx context.Context, id uuid.UUID, actor Actor) error {
	if err := validateAdminActor(actor); err != nil {
		return err
	}

	deletedAt := u.clock.Now()
	deletedUsername := "deleted_" + strings.ReplaceAll(uuid.NewString(), "-", "")

	if err := u.tx.Do(ctx, func(txCtx context.Context) error {
		player, err := u.players.GetAdminPlayer(txCtx, id)
		if err != nil {
			return fmt.Errorf("AdminPlayerUsecase - DeletePlayer - AdminPlayerRepo.GetAdminPlayer: %w", err)
		}
		if player.Status != domain.PlayerStatusIdle {
			return domain.ErrConflict
		}
		afterState := adminPlayerAuditState(*player, true)
		afterState.Username = deletedUsername
		if err := u.players.SoftDeleteAdminPlayer(txCtx, id, deletedUsername, deletedAt); err != nil {
			return fmt.Errorf("AdminPlayerUsecase - DeletePlayer - AdminPlayerRepo.SoftDeleteAdminPlayer: %w", err)
		}
		if err := u.players.CreateAdminPlayerAudit(txCtx, PlayerAuditInput{
			Actor:       actor,
			Action:      PlayerAuditActionDelete,
			PlayerID:    id,
			BeforeState: adminPlayerAuditState(*player, false),
			AfterState:  afterState,
			CreatedAt:   deletedAt,
		}); err != nil {
			return fmt.Errorf("AdminPlayerUsecase - DeletePlayer - AdminPlayerRepo.CreateAdminPlayerAudit: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}

	u.invalidateLeaderboard()
	return nil
}

func validateAdminActor(actor Actor) error {
	if strings.TrimSpace(actor.Subject) == "" || strings.TrimSpace(actor.JTI) == "" {
		return domain.ErrInvalidCredentials
	}
	return nil
}

func adminPlayerAuditState(player PlayerRecord, deleted bool) PlayerAuditState {
	return PlayerAuditState{
		Username:           player.Username,
		Status:             string(player.Status),
		Wins:               player.Wins,
		AverageSolveTimeMs: player.AverageSolveTimeMs,
		StatsOverridden:    player.StatsOverridden,
		Deleted:            deleted,
	}
}

func (u *PlayerUseCase) invalidateLeaderboard() {
	if u.leaderboard != nil {
		u.leaderboard.Invalidate()
	}
}

func validateAdminPlayerInput(in PlayerInput) error {
	if !adminUsernameRE.MatchString(in.Username) {
		return domain.ErrUsernameInvalid
	}
	if in.Wins < 0 || in.Wins > math.MaxInt32 {
		return domain.ErrValidation
	}
	if in.AverageSolveTimeMs < 0 {
		return domain.ErrValidation
	}
	if in.Wins == 0 && in.AverageSolveTimeMs != 0 {
		return domain.ErrValidation
	}
	if in.Wins > 0 && in.AverageSolveTimeMs == 0 {
		return domain.ErrValidation
	}
	return nil
}
