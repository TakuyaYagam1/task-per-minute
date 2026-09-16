package settlement

import (
	"context"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/settlement"
	tournamentparticipant "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/participant"
)

// ParticipantSettlementWorkflow is the production settlement port used by the
// participant command coordinator. Its repository uses the coordinator's
// transaction context, so no settlement evidence can commit independently.
type ParticipantSettlementWorkflow struct {
	usecase *gameusecase.SettlementUseCase
}

func NewParticipantSettlementWorkflow(
	repository *ParticipantSettlementRepository,
) *ParticipantSettlementWorkflow {
	return &ParticipantSettlementWorkflow{usecase: gameusecase.SettlementNewUseCase(repository)}
}

func (w *ParticipantSettlementWorkflow) Settle(
	ctx context.Context,
	command gameusecase.SettlementCommand,
) (*gameusecase.SettlementRecord, bool, error) {
	if w == nil || w.usecase == nil {
		return nil, false, domain.ErrInternal
	}
	return w.usecase.Settle(ctx, command)
}

var _ tournamentparticipant.SettlementWorkflow = (*ParticipantSettlementWorkflow)(nil)
