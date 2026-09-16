package postgres

import (
	"context"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
)

type participantConnectionAuthorityController interface {
	AuthorityFor(ctx context.Context, tournamentID uuid.UUID) (authority.Identity, error)
}

type ParticipantConnectionPostgres = participant.ParticipantConnectionPostgres

func NewParticipantConnectionPostgres(
	tx *TxManager,
	authority participantConnectionAuthorityController,
) *ParticipantConnectionPostgres {
	return participant.NewParticipantConnectionPostgres(tx, authority)
}
