package postgres

import (
	"context"

	creation "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/creation"
	catalogusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/catalog"
)

// TournamentCreatePostgres keeps the historical root-package API while the
// durable receipt workflow lives in the tournament creation capability.
type TournamentCreatePostgres = creation.TournamentCreatePostgres

type tournamentCreateReceiptAdapter struct {
	tournaments *TournamentPostgres
}

func (a tournamentCreateReceiptAdapter) Create(
	ctx context.Context,
	in creation.TournamentCreateInput,
) (*creation.TournamentRecord, *creation.RosterRecord, error) {
	if a.tournaments == nil {
		return nil, nil, nil
	}
	created, roster, err := a.tournaments.Create(ctx, TournamentCreateInput{
		ID: in.ID, RosterID: in.RosterID, Name: in.Name, PublicID: in.PublicID,
		PlannedRosterSize: in.PlannedRosterSize, ContentRevision: in.ContentRevision,
		CreatedAt: in.CreatedAt,
	})
	if err != nil || created == nil || roster == nil {
		return nil, nil, err
	}
	return &creation.TournamentRecord{
		ID: created.ID, Preset: created.Preset, Name: created.Name, PublicID: created.PublicID,
		PlannedRosterSize: created.PlannedRosterSize, ContentRevision: created.ContentRevision,
		State: string(created.State), Revision: created.Revision,
		CreatedAt: created.CreatedAt, UpdatedAt: created.UpdatedAt,
	}, &creation.RosterRecord{ID: roster.ID, TournamentID: roster.TournamentID}, nil
}

func NewTournamentCreatePostgres(tournaments *TournamentPostgres) *TournamentCreatePostgres {
	var tx *TxManager
	if tournaments != nil {
		tx = tournaments.tx
	}
	return creation.NewTournamentCreatePostgres(tx, tournamentCreateReceiptAdapter{tournaments: tournaments})
}

var _ catalogusecase.TournamentCreateStore = (*TournamentCreatePostgres)(nil)
