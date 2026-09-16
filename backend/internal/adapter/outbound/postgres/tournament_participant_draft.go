package postgres

import (
	"context"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	adminroster "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/roster"
	participantdraft "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/draft"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
)

type ParticipantDraftRepository = participantdraft.ParticipantDraftRepository

func NewParticipantDraftRepository(
	tx *TxManager,
	drafts *DraftPostgres,
) *ParticipantDraftRepository {
	return participantdraft.NewParticipantDraftRepositoryWithDependencies(tx, drafts, loadParticipantDraftContent)
}

func loadParticipantDraftContent(
	ctx context.Context,
	querier *sqlc.Queries,
	tournamentID uuid.UUID,
) (domain.ContentConfiguration, error) {
	content, err := adminroster.LoadPreflightContent(ctx, querier, tournamentID)
	if err != nil {
		return domain.ContentConfiguration{}, err
	}
	return content.Configuration, nil
}

var _ draftusecase.Repository = (*ParticipantDraftRepository)(nil)
