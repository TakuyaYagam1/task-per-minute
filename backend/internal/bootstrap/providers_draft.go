package bootstrap

import (
	"context"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	draftrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment/draft"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	rosterrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/roster"
	participantdraftrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/draft"
	deadlinerepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/swiss/deadline"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
)

func provideParticipantDraftRepository(
	tx *postgres.TxManager,
	drafts *draftrepo.DraftPostgres,
) *participantdraftrepo.ParticipantDraftRepository {
	return participantdraftrepo.NewParticipantDraftRepositoryWithDependencies(
		tx,
		drafts,
		func(ctx context.Context, querier *sqlc.Queries, tournamentID uuid.UUID) (domain.ContentConfiguration, error) {
			loaded, err := rosterrepo.LoadPreflightContent(ctx, querier, tournamentID)
			if err != nil {
				return domain.ContentConfiguration{}, err
			}
			return loaded.Configuration, nil
		},
	)
}

func provideSwissDraftDeadlineWorker(
	repository *deadlinerepo.SwissDraftDeadlinePostgres,
	clock clockFunc,
) (*draftusecase.DeadlineWorker, error) {
	return draftusecase.NewDeadlineWorker(repository, clock, draftusecase.DeadlineWorkerConfig{})
}
