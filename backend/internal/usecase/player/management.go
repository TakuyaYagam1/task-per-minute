package player

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/google/uuid"
)

var managementUsernameRE = regexp.MustCompile(`^[a-zA-Z0-9_-]{2,50}$`)

const (
	defaultAuditLimit = int32(50)
	maxAuditLimit     = int32(200)
)

type ManagementUseCase struct {
	tx          ManagementTransactionManager
	players     PlayerRepository
	leaderboard LeaderboardInvalidator
	clock       ManagementClock
}

func ManagementNewUseCase(
	tx ManagementTransactionManager,
	players PlayerRepository,
	leaderboard LeaderboardInvalidator,
	clk ManagementClock,
) *ManagementUseCase {
	return &ManagementUseCase{
		tx:          tx,
		players:     players,
		leaderboard: leaderboard,
		clock:       clk,
	}
}

func (u *ManagementUseCase) ListPlayers(ctx context.Context, includeDeleted bool) ([]PlayerRecord, error) {
	players, err := u.players.ListPlayers(ctx, includeDeleted)
	if err != nil {
		return nil, fmt.Errorf("PlayerManagement - ListPlayers - Repository.ListPlayers: %w", err)
	}
	return players, nil
}

func (u *ManagementUseCase) ListPlayerAudit(
	ctx context.Context,
	id uuid.UUID,
	limit int32,
) ([]AuditEvent, error) {
	if limit <= 0 {
		limit = defaultAuditLimit
	}
	if limit > maxAuditLimit {
		limit = maxAuditLimit
	}
	if _, err := u.players.GetPlayerIncludingDeleted(ctx, id); err != nil {
		return nil, fmt.Errorf("PlayerManagement - ListPlayerAudit - Repository.GetPlayerIncludingDeleted: %w", err)
	}
	events, err := u.players.ListPlayerAudit(ctx, id, limit)
	if err != nil {
		return nil, fmt.Errorf("PlayerManagement - ListPlayerAudit - Repository.ListPlayerAudit: %w", err)
	}
	return events, nil
}

func (u *ManagementUseCase) UpdatePlayer(
	ctx context.Context,
	id uuid.UUID,
	in PlayerInput,
	actor Actor,
) (*PlayerRecord, error) {
	if err := validatePlayerInput(in); err != nil {
		return nil, err
	}
	if err := validateActor(actor); err != nil {
		return nil, err
	}

	var updated *PlayerRecord
	now := u.clock.Now()
	if err := u.tx.Do(ctx, func(txCtx context.Context) error {
		before, err := u.players.GetPlayer(txCtx, id)
		if err != nil {
			return fmt.Errorf("PlayerManagement - UpdatePlayer - Repository.GetPlayer: %w", err)
		}
		if err := u.players.UpdateUsername(txCtx, id, in.Username); err != nil {
			return fmt.Errorf("PlayerManagement - UpdatePlayer - Repository.UpdateUsername: %w", err)
		}
		if err := u.players.UpsertStats(txCtx, id, StatsInput{
			Wins:               in.Wins,
			AverageSolveTimeMs: in.AverageSolveTimeMs,
		}, now); err != nil {
			return fmt.Errorf("PlayerManagement - UpdatePlayer - Repository.UpsertStats: %w", err)
		}

		player, err := u.players.GetPlayer(txCtx, id)
		if err != nil {
			return fmt.Errorf("PlayerManagement - UpdatePlayer - Repository.GetPlayer updated: %w", err)
		}
		if err := u.players.CreatePlayerAudit(txCtx, AuditInput{
			Actor:       actor,
			Action:      AuditActionUpdate,
			PlayerID:    id,
			BeforeState: playerAuditState(*before, false),
			AfterState:  playerAuditState(*player, false),
			CreatedAt:   now,
		}); err != nil {
			return fmt.Errorf("PlayerManagement - UpdatePlayer - Repository.CreatePlayerAudit: %w", err)
		}
		updated = player
		return nil
	}); err != nil {
		return nil, err
	}

	u.invalidateLeaderboard()
	return updated, nil
}

func (u *ManagementUseCase) DeletePlayer(ctx context.Context, id uuid.UUID, actor Actor) error {
	if err := validateActor(actor); err != nil {
		return err
	}

	deletedAt := u.clock.Now()
	deletedUsername := "deleted_" + strings.ReplaceAll(uuid.NewString(), "-", "")

	if err := u.tx.Do(ctx, func(txCtx context.Context) error {
		player, err := u.players.GetPlayer(txCtx, id)
		if err != nil {
			return fmt.Errorf("PlayerManagement - DeletePlayer - Repository.GetPlayer: %w", err)
		}
		afterState := playerAuditState(*player, true)
		afterState.Username = deletedUsername
		if err := u.players.SoftDeletePlayer(txCtx, id, deletedUsername, deletedAt); err != nil {
			return fmt.Errorf("PlayerManagement - DeletePlayer - Repository.SoftDeletePlayer: %w", err)
		}
		if err := u.players.CreatePlayerAudit(txCtx, AuditInput{
			Actor:       actor,
			Action:      AuditActionDelete,
			PlayerID:    id,
			BeforeState: playerAuditState(*player, false),
			AfterState:  afterState,
			CreatedAt:   deletedAt,
		}); err != nil {
			return fmt.Errorf("PlayerManagement - DeletePlayer - Repository.CreatePlayerAudit: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}

	u.invalidateLeaderboard()
	return nil
}

func validateActor(actor Actor) error {
	if strings.TrimSpace(actor.Subject) == "" || strings.TrimSpace(actor.JTI) == "" {
		return domain.ErrInvalidCredentials
	}
	return nil
}

func playerAuditState(player PlayerRecord, deleted bool) AuditState {
	return AuditState{
		Username:           player.Username,
		Wins:               player.Wins,
		AverageSolveTimeMs: player.AverageSolveTimeMs,
		StatsOverridden:    player.StatsOverridden,
		Deleted:            deleted,
	}
}

func (u *ManagementUseCase) invalidateLeaderboard() {
	if u.leaderboard != nil {
		u.leaderboard.Invalidate()
	}
}

func validatePlayerInput(in PlayerInput) error {
	if !managementUsernameRE.MatchString(in.Username) {
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
