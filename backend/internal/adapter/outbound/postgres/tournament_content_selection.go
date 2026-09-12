package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	catalogusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/catalog"
)

// TournamentContentPostgres selects the latest immutable task-pool publication
// for tournament creation discovery. The publication and its health evidence
// are read in one repeatable-read snapshot and task bodies are never loaded.
type TournamentContentPostgres struct {
	tx *TxManager
}

func NewTournamentContentPostgres(tx *TxManager) *TournamentContentPostgres {
	return &TournamentContentPostgres{tx: tx}
}

var _ catalogusecase.ContentReader = (*TournamentContentPostgres)(nil)

func (r *TournamentContentPostgres) GetTournamentContent(
	ctx context.Context,
) (catalogusecase.TournamentContentRecord, error) {
	if ctx == nil || r == nil || r.tx == nil {
		return catalogusecase.TournamentContentRecord{}, domain.ErrValidation
	}

	var content catalogusecase.TournamentContentRecord
	err := r.tx.ReadSnapshot(ctx, func(snapshotCtx context.Context) error {
		querier := r.tx.Querier(snapshotCtx)
		rows, err := querier.GetLatestTaskPoolPublication(snapshotCtx)
		if err != nil {
			return fmt.Errorf("TournamentContentPostgres - select latest publication: %w", err)
		}

		pools := make([]tournamentV1ContentPublicationPool, len(rows))
		for index, row := range rows {
			pools[index] = tournamentV1ContentPublicationPool{
				publicationID:       row.PublicationID,
				publicationRevision: row.PublicationRevision,
				publishedAt:         row.PublishedAt.Time,
				publishedAtValid:    row.PublishedAt.Valid,
				poolRevisionID:      row.PoolRevisionID,
				kind:                row.Kind,
				poolRevision:        row.PoolRevision,
			}
		}
		binding, err := tournamentV1ContentBindingFromPools(uuid.Nil, uuid.Nil, pools)
		if err != nil {
			return err
		}
		if err := revalidateTournamentV1ContentPools(snapshotCtx, querier, binding); err != nil {
			return fmt.Errorf("TournamentContentPostgres - revalidate task pools: %w", err)
		}

		publishedAt := pools[0].publishedAt.Round(0).UTC()
		if !domain.IsValidServerTime(publishedAt) {
			return fmt.Errorf(
				"TournamentContentPostgres - invalid publication time: %w",
				domain.ErrInvalidContentConfiguration,
			)
		}
		content = catalogusecase.TournamentContentRecord{
			ContentRevision:      pools[0].publicationRevision,
			PublicationID:        binding.publicationID,
			PublishedAt:          publishedAt,
			NormalPoolRevisionID: binding.normalPoolRevisionID,
			GoldenPoolRevisionID: binding.goldenPoolRevisionID,
		}
		return nil
	})
	if err != nil {
		return catalogusecase.TournamentContentRecord{}, fmt.Errorf(
			"TournamentContentPostgres - GetTournamentContent: %w", err,
		)
	}
	return content, nil
}
