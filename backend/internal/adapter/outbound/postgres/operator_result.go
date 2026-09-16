package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"

	resultrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/result"
)

type TournamentAdminResultPostgres = resultrepo.TournamentAdminResultPostgres

func NewTournamentAdminResultPostgres(
	tx *TxManager,
	results *ResultPostgres,
) *TournamentAdminResultPostgres {
	return resultrepo.NewTournamentAdminResultPostgresWithDependencies(
		tx,
		results,
		resultProjectionFinalizer,
		func(
			ctx context.Context,
			tx *TxManager,
			tournamentID uuid.UUID,
			seriesID uuid.UUID,
			at time.Time,
			mode string,
			commandID uuid.UUID,
		) error {
			return ensurePreStartSwissRoundProof(ctx, tx, tournamentID, seriesID, at, swissRoundProofOrigin{
				mode: mode, commandID: commandID,
			})
		},
	)
}
