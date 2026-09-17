package creation

import (
	"context"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	catalogrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/catalog"
)

// NewProductionTournamentCreatePostgres composes the receipt workflow with
// the child tournament catalog writer used by production wiring. The raw
// constructor remains available for tests and other callers with an injected
// TournamentCreator.
func NewProductionTournamentCreatePostgres(tx *db.TxManager) *TournamentCreatePostgres {
	return NewTournamentCreatePostgres(tx, tournamentCatalogCreator{
		repository: catalogrepo.NewTournamentCatalogPostgres(tx),
	})
}

type tournamentCatalogCreator struct {
	repository *catalogrepo.TournamentCatalogPostgres
}

var _ TournamentCreator = tournamentCatalogCreator{}

func (creator tournamentCatalogCreator) Create(
	ctx context.Context,
	in TournamentCreateInput,
) (*TournamentRecord, *RosterRecord, error) {
	tournament, roster, err := creator.repository.Create(ctx, catalogrepo.TournamentCreateInput{
		ID: in.ID, RosterID: in.RosterID, Name: in.Name, PublicID: in.PublicID,
		PlannedRosterSize: in.PlannedRosterSize, ContentRevision: in.ContentRevision,
		CreatedAt: in.CreatedAt,
	})
	if err != nil {
		return nil, nil, err
	}
	return &TournamentRecord{
		ID: tournament.ID, Preset: tournament.Preset, Name: tournament.Name, PublicID: tournament.PublicID,
		PlannedRosterSize: int(tournament.PlannedRosterSize), ContentRevision: tournament.ContentRevision,
		State: tournament.State, Revision: tournament.Revision,
		CreatedAt: tournament.CreatedAt.Time, UpdatedAt: tournament.UpdatedAt.Time,
	}, &RosterRecord{
		ID: roster.ID, TournamentID: roster.TournamentID,
	}, nil
}
