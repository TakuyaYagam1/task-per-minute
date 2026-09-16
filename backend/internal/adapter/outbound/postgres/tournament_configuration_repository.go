package postgres

import (
	"encoding/json"

	configurationpostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/configuration"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

// TournamentConfigurationPostgres preserves the root adapter contract while
// keeping configuration persistence in its dedicated package.
type TournamentConfigurationPostgres = configurationpostgres.TournamentConfigurationPostgres

func NewTournamentConfigurationPostgres(tx *TxManager) *TournamentConfigurationPostgres {
	return configurationpostgres.NewProductionTournamentConfigurationPostgres(tx)
}

// categoriesJSON remains available to the root tournament creation adapter.
func categoriesJSON(values []domain.Category) []byte {
	data, _ := json.Marshal(values)
	return data
}
