package deadline

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
)

func lockTournamentResultScope(ctx context.Context, q *sqlc.Queries, tournamentID, rosterID uuid.UUID) error {
	_, err := q.LockTournamentResultScope(ctx, sqlc.LockTournamentResultScopeParams{
		TournamentID: tournamentID,
		RosterID:     uuid.NullUUID{UUID: rosterID, Valid: rosterID != uuid.Nil},
	})
	return err
}

func tstz(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}
