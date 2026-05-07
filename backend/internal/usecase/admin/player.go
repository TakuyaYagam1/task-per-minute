package admin

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
	"github.com/TakuyaYagam1/task-per-minute/pkg/clock"
)

var adminUsernameRE = regexp.MustCompile(`^[a-zA-Z0-9_-]{2,50}$`)

const (
	defaultAdminPlayerAuditLimit = int32(50)
	maxAdminPlayerAuditLimit     = int32(200)
)

type PlayerUsecase struct {
	tx          usecase.TxManager
	players     usecase.AdminPlayerRepo
	leaderboard usecase.LeaderboardInvalidator
	clock       clock.Clock
}

func NewPlayerUsecase(
	tx usecase.TxManager,
	players usecase.AdminPlayerRepo,
	leaderboard usecase.LeaderboardInvalidator,
	clk clock.Clock,
) *PlayerUsecase {
	if clk == nil {
		clk = clock.Real{}
	}
	return &PlayerUsecase{
		tx:          tx,
		players:     players,
		leaderboard: leaderboard,
		clock:       clk,
	}
}

func (u *PlayerUsecase) ListPlayers(ctx context.Context, includeDeleted bool) ([]usecase.AdminPlayerRecord, error) {
	players, err := u.players.ListAdminPlayers(ctx, includeDeleted)
	if err != nil {
		return nil, fmt.Errorf("AdminPlayerUsecase - ListPlayers - AdminPlayerRepo.ListAdminPlayers: %w", err)
	}
	return players, nil
}

func (u *PlayerUsecase) ListPlayerAudit(
	ctx context.Context,
	id uuid.UUID,
	limit int32,
) ([]usecase.AdminPlayerAuditEvent, error) {
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

func (u *PlayerUsecase) UpdatePlayer(
	ctx context.Context,
	id uuid.UUID,
	in usecase.AdminPlayerInput,
	actor usecase.AdminActor,
) (*usecase.AdminPlayerRecord, error) {
	if err := validateAdminPlayerInput(in); err != nil {
		return nil, err
	}
	if err := validateAdminActor(actor); err != nil {
		return nil, err
	}

	var updated *usecase.AdminPlayerRecord
	now := u.clock.Now()
	if err := u.tx.Do(ctx, func(txCtx context.Context) error {
		before, err := u.players.GetAdminPlayer(txCtx, id)
		if err != nil {
			return fmt.Errorf("AdminPlayerUsecase - UpdatePlayer - AdminPlayerRepo.GetAdminPlayer: %w", err)
		}
		if err := u.players.UpdateAdminPlayerUsername(txCtx, id, in.Username); err != nil {
			return fmt.Errorf("AdminPlayerUsecase - UpdatePlayer - AdminPlayerRepo.UpdateAdminPlayerUsername: %w", err)
		}
		if err := u.players.UpsertAdminPlayerStats(txCtx, id, usecase.AdminPlayerStatsInput{
			Wins:               in.Wins,
			AverageSolveTimeMs: in.AverageSolveTimeMs,
		}, now); err != nil {
			return fmt.Errorf("AdminPlayerUsecase - UpdatePlayer - AdminPlayerRepo.UpsertAdminPlayerStats: %w", err)
		}

		player, err := u.players.GetAdminPlayer(txCtx, id)
		if err != nil {
			return fmt.Errorf("AdminPlayerUsecase - UpdatePlayer - AdminPlayerRepo.GetAdminPlayer updated: %w", err)
		}
		if err := u.players.CreateAdminPlayerAudit(txCtx, usecase.AdminPlayerAuditInput{
			Actor:       actor,
			Action:      usecase.AdminPlayerAuditActionUpdate,
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

func (u *PlayerUsecase) DeletePlayer(ctx context.Context, id uuid.UUID, actor usecase.AdminActor) error {
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
			return apperr.ErrConflict
		}
		afterState := adminPlayerAuditState(*player, true)
		afterState.Username = deletedUsername
		if err := u.players.SoftDeleteAdminPlayer(txCtx, id, deletedUsername, deletedAt); err != nil {
			return fmt.Errorf("AdminPlayerUsecase - DeletePlayer - AdminPlayerRepo.SoftDeleteAdminPlayer: %w", err)
		}
		if err := u.players.CreateAdminPlayerAudit(txCtx, usecase.AdminPlayerAuditInput{
			Actor:       actor,
			Action:      usecase.AdminPlayerAuditActionDelete,
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

func validateAdminActor(actor usecase.AdminActor) error {
	if strings.TrimSpace(actor.Subject) == "" || strings.TrimSpace(actor.JTI) == "" {
		return apperr.ErrInvalidCredentials
	}
	return nil
}

func adminPlayerAuditState(player usecase.AdminPlayerRecord, deleted bool) usecase.AdminPlayerAuditState {
	return usecase.AdminPlayerAuditState{
		Username:           player.Username,
		Status:             string(player.Status),
		Wins:               player.Wins,
		AverageSolveTimeMs: player.AverageSolveTimeMs,
		StatsOverridden:    player.StatsOverridden,
		Deleted:            deleted,
	}
}

func (u *PlayerUsecase) invalidateLeaderboard() {
	if u.leaderboard != nil {
		u.leaderboard.Invalidate()
	}
}

func validateAdminPlayerInput(in usecase.AdminPlayerInput) error {
	if !adminUsernameRE.MatchString(in.Username) {
		return apperr.ErrUsernameInvalid
	}
	if in.Wins < 0 || in.Wins > math.MaxInt32 {
		return apperr.ErrValidation
	}
	if in.AverageSolveTimeMs < 0 {
		return apperr.ErrValidation
	}
	if in.Wins == 0 && in.AverageSolveTimeMs != 0 {
		return apperr.ErrValidation
	}
	if in.Wins > 0 && in.AverageSolveTimeMs == 0 {
		return apperr.ErrValidation
	}
	return nil
}
