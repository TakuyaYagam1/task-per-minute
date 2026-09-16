package configuration

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
)

// SeriesGenesisInput contains the durable identity and provenance needed to
// create a successor Series score projection node.
type SeriesGenesisInput struct {
	WaveID                     uuid.UUID
	SeriesID                   uuid.UUID
	InitialScoreRevisionID     uuid.UUID
	TournamentID               uuid.UUID
	RosterID                   uuid.UUID
	CommandID                  uuid.UUID
	SourceProjectionRevisionID uuid.UUID
	SourceProjectionRevision   int64
	CreatedAt                  time.Time
	FirstParticipantID         uuid.UUID
	SecondParticipantID        uuid.UUID
}

// SeriesMaterializer bridges configuration edits to the execution-owned
// graph materialization boundary without coupling this package to its parent.
type SeriesMaterializer interface {
	CreateGenesis(ctx context.Context, queries *sqlc.Queries, input SeriesGenesisInput) error
	Materialize(ctx context.Context, plan tournamentadmin.PairingPlan) error
}
