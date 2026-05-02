package duel

import (
	"context"

	"github.com/google/uuid"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
)

// bumpLeaderboard runs LeaderboardBumper.IncrementWin and logs the error
// instead of swallowing it.
//
// IncrementWin lives outside the duel-finish transaction (Redis is not
// transactional with Postgres) and its failure must not roll the duel back -
// the duel is already finalized in PG and can be reconciled offline from
// duels.winner_id. This helper guarantees that any IncrementWin failure is
// at least surfaced in the structured logs so operators can react to it.
func bumpLeaderboard(
	ctx context.Context,
	board usecase.LeaderboardBumper,
	log logkit.Logger,
	duelID uuid.UUID,
	username string,
) {
	if board == nil || username == "" {
		return
	}
	err := board.IncrementWin(ctx, username)
	if err == nil || log == nil {
		return
	}
	log.Error("leaderboard increment win failed", logkit.Fields{
		"duel_id":  duelID.String(),
		"username": username,
		"error":    err.Error(),
	})
}
