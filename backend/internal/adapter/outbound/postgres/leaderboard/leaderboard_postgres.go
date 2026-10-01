package leaderboard

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	leaderboardusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/leaderboard"
)

type LeaderboardPostgres struct {
	tx *db.TxManager
}

func NewLeaderboardPostgres(tx *db.TxManager) *LeaderboardPostgres {
	return &LeaderboardPostgres{tx: tx}
}

var _ leaderboardusecase.StatsRepository = (*LeaderboardPostgres)(nil)
var _ leaderboardusecase.PageRepository = (*LeaderboardPostgres)(nil)

// TopStats returns players with a current, accepted tournament solve result.
func (r *LeaderboardPostgres) TopStats(ctx context.Context, limit int32) ([]leaderboardusecase.PlayerStats, error) {
	rows, err := r.tx.Querier(ctx).TopLeaderboardStats(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("LeaderboardPostgres - TopStats - Querier.TopLeaderboardStats: %w", err)
	}
	out := make([]leaderboardusecase.PlayerStats, 0, len(rows))
	for _, row := range rows {
		out = append(out, leaderboardusecase.PlayerStats{
			PlayerID:           row.PlayerID,
			Username:           row.Username,
			Wins:               int(row.Wins),
			AverageSolveTimeMs: row.AverageSolveTimeMs,
		})
	}
	return out, nil
}

func (r *LeaderboardPostgres) LeaderboardPage(ctx context.Context, query leaderboardusecase.PageQuery) (leaderboardusecase.PageRows, error) {
	rows, err := r.tx.Querier(ctx).GetLeaderboardPage(ctx, sqlc.GetLeaderboardPageParams{
		Column1: escapeLeaderboardSearch(query.Search),
		Column2: string(query.Wins),
		Column3: query.PerPage,
		Column4: query.Page,
	})
	if err != nil {
		return leaderboardusecase.PageRows{}, fmt.Errorf("LeaderboardPostgres - LeaderboardPage - Querier.GetLeaderboardPage: %w", err)
	}

	page := leaderboardusecase.PageRows{Entries: make([]leaderboardusecase.Entry, 0, len(rows))}
	for _, row := range rows {
		page.Total = row.Total
		if row.GlobalRank == 0 || row.Username == nil || row.Wins == nil || row.AverageSolveTimeMs == nil {
			continue
		}
		entry := leaderboardusecase.Entry{
			Rank:               int(row.GlobalRank),
			Username:           *row.Username,
			Wins:               int(*row.Wins),
			AverageSolveTimeMs: *row.AverageSolveTimeMs,
		}
		if row.AvatarPlayerID.Valid || row.AvatarSha256 != nil || row.AvatarContentType != nil {
			if !row.AvatarPlayerID.Valid || len(row.AvatarSha256) != 32 || row.AvatarContentType == nil {
				return leaderboardusecase.PageRows{}, fmt.Errorf("LeaderboardPostgres - LeaderboardPage - invalid avatar metadata")
			}
			entry.Avatar = &leaderboardusecase.AvatarMetadata{
				PlayerID:    row.AvatarPlayerID.UUID,
				Version:     hex.EncodeToString(row.AvatarSha256),
				ContentType: *row.AvatarContentType,
			}
		}
		page.Entries = append(page.Entries, entry)
	}
	return page, nil
}

func escapeLeaderboardSearch(search string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(search)
}
