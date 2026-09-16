package postgres

import (
	"context"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
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
	content, err := loadTournamentPreflightContent(ctx, querier, tournamentID)
	if err != nil {
		return domain.ContentConfiguration{}, err
	}
	return content.configuration, nil
}

// participantDraftExecution keeps the root package bridge used by neighboring
// tournament adapters while the mapping lives in the participant child.
func participantDraftExecution(
	aggregate *DraftAggregate,
	targetRevision int64,
) (*draftusecase.Execution, error) {
	return participantdraft.ParticipantDraftExecution(aggregate, targetRevision)
}

var _ draftusecase.Repository = (*ParticipantDraftRepository)(nil)
