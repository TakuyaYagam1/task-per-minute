package configuration

import (
	"context"

	waverepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/execution/wave"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	adminexecution "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/execution"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
)

// NewProductionTournamentConfigurationPostgres builds the configuration
// repository with the execution materializer used by the production wiring.
// The raw constructor remains available for callers that provide their own
// SeriesMaterializer.
func NewProductionTournamentConfigurationPostgres(tx *db.TxManager) *TournamentConfigurationPostgres {
	return NewTournamentConfigurationPostgresWithMaterializer(
		tx,
		productionSeriesMaterializer{execution: adminexecution.NewRepository(tx, nil)},
	)
}

type productionSeriesMaterializer struct {
	execution *adminexecution.Repository
}

var _ SeriesMaterializer = productionSeriesMaterializer{}

func (m productionSeriesMaterializer) CreateGenesis(
	ctx context.Context,
	q *sqlc.Queries,
	input SeriesGenesisInput,
) error {
	wave := waverepo.WaveCreateInput{
		ID: input.WaveID, TournamentID: input.TournamentID, RosterID: input.RosterID,
		CommandID: input.CommandID, SourceProjectionRevisionID: input.SourceProjectionRevisionID,
		SourceProjectionRevision: input.SourceProjectionRevision, CreatedAt: input.CreatedAt,
	}
	series := waverepo.WaveSeriesInput{
		ID: input.SeriesID, FirstParticipantID: input.FirstParticipantID, SecondParticipantID: input.SecondParticipantID,
		Format: domain.SeriesFormatBO1, InitialScoreRevisionID: domain.SeriesScoreRevisionID(input.InitialScoreRevisionID),
	}
	return waverepo.CreateWaveGenesisProjectionNode(ctx, q, wave, series)
}

func (m productionSeriesMaterializer) Materialize(
	ctx context.Context,
	plan tournamentadmin.PairingPlan,
) error {
	if m.execution == nil {
		return domain.ErrValidation
	}
	switch plan.Command.CategoryMode {
	case domain.CategoryModeRandom:
		return m.execution.MaterializeSwissRandomBO1(ctx, plan)
	case domain.CategoryModeAdmin:
		return m.execution.MaterializeSwissAdminBO1(ctx, plan)
	case domain.CategoryModeDraft:
		return m.execution.MaterializeSwissDraftBO1(ctx, plan)
	default:
		return domain.ErrValidation
	}
}
