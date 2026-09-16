package participant

import (
	"context"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	connectionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/connection"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
)

// participantConnectionAuthorityController is deliberately narrower than the
// authority usecase. The facade keeps the historical constructor contract
// while the implementation lives in the connection capability package.
type participantConnectionAuthorityController interface {
	AuthorityFor(ctx context.Context, tournamentID uuid.UUID) (authoritydomain.Identity, error)
}

// ParticipantConnectionPostgres is the durable participant socket boundary.
// It is kept as a root alias for source compatibility during the extraction.
type ParticipantConnectionPostgres = connectionrepo.ParticipantConnectionPostgres

// NewParticipantConnectionPostgres preserves the root postgres constructor
// while delegating implementation to the participant connection capability.
func NewParticipantConnectionPostgres(
	tx *db.TxManager,
	authority participantConnectionAuthorityController,
) *ParticipantConnectionPostgres {
	return connectionrepo.NewParticipantConnectionPostgres(tx, authority)
}
