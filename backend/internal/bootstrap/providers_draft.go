package bootstrap

import (
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
)

func provideSwissDraftDeadlineWorker(
	repository *postgres.SwissDraftDeadlinePostgres,
	clock clockFunc,
) (*draftusecase.DeadlineWorker, error) {
	return draftusecase.NewDeadlineWorker(repository, clock, draftusecase.DeadlineWorkerConfig{})
}
