package postgres

import (
	"context"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
)

// lockTournamentResultScope is the common transaction prefix for intersecting
// execution/result writers: Tournament -> Roster -> current Projection, before
// any Wave, Series or attempt lock. A zero roster selects the tournament's
// unique roster; callers with an exact roster also pin that identity.
func lockTournamentResultScope(ctx context.Context, q *sqlc.Queries, tournamentID, rosterID uuid.UUID) error {
	_, err := q.LockTournamentResultScope(ctx, sqlc.LockTournamentResultScopeParams{
		TournamentID: tournamentID, RosterID: uuid.NullUUID{UUID: rosterID, Valid: rosterID != uuid.Nil},
	})
	return err
}
