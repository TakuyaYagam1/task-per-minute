package authority

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	resultrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result"
	progressionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/progression"
)

// NewResultPostgres composes result persistence with the final Swiss receipt
// boundary required by production settlement flows.
func NewResultPostgres(tx *db.TxManager) *resultrepo.ResultPostgres {
	return resultrepo.NewResultPostgresWithFinalizer(tx, FinalizeProjection)
}

// FinalizeProjection persists the final Swiss receipt in the same transaction
// as the authoritative result projection.
func FinalizeProjection(
	ctx context.Context,
	tx *db.TxManager,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	revisionID uuid.UUID,
	at time.Time,
) error {
	err := progressionrepo.NewTournamentProgressionPostgres(tx).PersistFinalSwissReceipt(
		ctx,
		progressionrepo.ProjectionScope{TournamentID: tournamentID, RosterID: rosterID},
		revisionID,
		at,
	)
	if err != nil {
		return fmt.Errorf("result publication - final Swiss receipt: %w", err)
	}
	return nil
}
