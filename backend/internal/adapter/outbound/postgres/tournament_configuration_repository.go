package postgres

import (
	"context"
	"encoding/json"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	configurationpostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/configuration"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
)

// TournamentConfigurationPostgres preserves the root adapter contract while
// keeping configuration persistence in its dedicated package.
type TournamentConfigurationPostgres struct {
	delegate *configurationpostgres.TournamentConfigurationPostgres
}

func NewTournamentConfigurationPostgres(tx *TxManager) *TournamentConfigurationPostgres {
	return &TournamentConfigurationPostgres{
		delegate: configurationpostgres.NewTournamentConfigurationPostgresWithMaterializer(
			tx,
			tournamentConfigurationMaterializer{tx: tx},
		),
	}
}

var _ tournamentadmin.TournamentConfigurationRepository = (*TournamentConfigurationPostgres)(nil)

func (r *TournamentConfigurationPostgres) LoadConfiguration(
	ctx context.Context,
	query tournamentadmin.ConfigurationLoadQuery,
) (tournamentadmin.ConfigurationAuthority, error) {
	if r == nil || r.delegate == nil {
		return tournamentadmin.ConfigurationAuthority{}, domain.ErrValidation
	}
	return r.delegate.LoadConfiguration(ctx, query)
}

func (r *TournamentConfigurationPostgres) ExecuteMutation(
	ctx context.Context,
	mutation tournamentadmin.ConfigurationMutation,
) (tournamentadmin.ConfigurationMutationResult, error) {
	if r == nil || r.delegate == nil {
		return tournamentadmin.ConfigurationMutationResult{}, domain.ErrValidation
	}
	return r.delegate.ExecuteMutation(ctx, mutation)
}

type tournamentConfigurationMaterializer struct {
	tx *TxManager
}

func (m tournamentConfigurationMaterializer) CreateGenesis(
	ctx context.Context,
	q *sqlc.Queries,
	input configurationpostgres.SeriesGenesisInput,
) error {
	wave := WaveCreateInput{
		ID: input.WaveID, TournamentID: input.TournamentID, RosterID: input.RosterID,
		CommandID: input.CommandID, SourceProjectionRevisionID: input.SourceProjectionRevisionID,
		SourceProjectionRevision: input.SourceProjectionRevision, CreatedAt: input.CreatedAt,
	}
	series := WaveSeriesInput{
		ID: input.SeriesID, FirstParticipantID: input.FirstParticipantID, SecondParticipantID: input.SecondParticipantID,
		Format: domain.SeriesFormatBO1, InitialScoreRevisionID: domain.SeriesScoreRevisionID(input.InitialScoreRevisionID),
	}
	return createWaveGenesisProjectionNode(ctx, q, wave, series)
}

func (m tournamentConfigurationMaterializer) Materialize(
	ctx context.Context,
	plan tournamentadmin.PairingPlan,
) error {
	execution := NewTournamentAdminExecutionPostgres(m.tx)
	switch plan.Command.CategoryMode {
	case domain.CategoryModeRandom:
		return execution.MaterializeSwissRandomBO1(ctx, plan)
	case domain.CategoryModeAdmin:
		return execution.MaterializeSwissAdminBO1(ctx, plan)
	case domain.CategoryModeDraft:
		return execution.MaterializeSwissDraftBO1(ctx, plan)
	default:
		return domain.ErrValidation
	}
}

// categoriesJSON remains available to the root tournament creation adapter.
func categoriesJSON(values []domain.Category) []byte {
	data, _ := json.Marshal(values)
	return data
}
