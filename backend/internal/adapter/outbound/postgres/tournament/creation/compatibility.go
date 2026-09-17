package creation

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// TournamentCreateInput is the narrow input required from the tournament
// aggregate writer. The parent postgres package adapts its aggregate method
// to this contract in the root facade.
type TournamentCreateInput struct {
	ID                uuid.UUID
	RosterID          uuid.UUID
	Name              string
	PublicID          string
	PlannedRosterSize int
	ContentRevision   int64
	CreatedAt         time.Time
}

type TournamentRecord struct {
	ID                uuid.UUID
	Preset            string
	Name              string
	PublicID          string
	PlannedRosterSize int
	ContentRevision   int64
	State             string
	Revision          int64
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type RosterRecord struct {
	ID           uuid.UUID
	TournamentID uuid.UUID
}

type TournamentCreator interface {
	Create(ctx context.Context, input TournamentCreateInput) (*TournamentRecord, *RosterRecord, error)
}

func tstz(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}

func validServerTime(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC
}

func tournamentV1ContentID(tournamentID, rosterID uuid.UUID, role string) uuid.UUID {
	return uuid.NewSHA1(tournamentID, []byte("tournament_v1:content:"+rosterID.String()+":"+role))
}
