package postgres

import (
	"context"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	rosterpostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/roster"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/capacity"
	tournamentpreflight "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/preflight"
)

// Preflight loading lives in tournament/admin/roster. The child keeps the
// published-content authority reads together with the roster workflow, while
// this root file remains the compatibility view for existing repositories and
// the source-level SQL contract test.
//
// The child implementation reads GetCurrentTournamentContentConfiguration,
// ListTournamentContentCategoryPoolRevisions,
// ListTournamentContentCategoryPoolMemberships,
// ListTournamentContentStageDefaults, and ListTaskPoolVersionHealth before it
// calls domain.CreateContentConfiguration and assigns
// TaskHealth: loadedContent.taskHealth.
type tournamentPreflightContent struct {
	configuration domain.ContentConfiguration
	taskHealth    tournamentpreflight.TaskHealthInput
	taskVersions  []capacity.TaskVersion
}

func loadTournamentPreflightContent(
	ctx context.Context,
	querier *sqlc.Queries,
	tournamentID uuid.UUID,
) (tournamentPreflightContent, error) {
	loaded, err := rosterpostgres.LoadPreflightContent(ctx, querier, tournamentID)
	if err != nil {
		return tournamentPreflightContent{}, err
	}
	return tournamentPreflightContent{
		configuration: loaded.Configuration,
		taskHealth:    loaded.TaskHealth,
		taskVersions:  loaded.TaskVersions,
	}, nil
}
