package pause

import (
	"github.com/google/uuid"

	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	enterusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause/enter"
)

type NormalPauseGraphUseCase = enterusecase.NormalPauseGraphUseCase

func NewNormalPauseGraphUseCase(transactions TransactionManager, repository NormalPauseRepository, clock PauseClock) *NormalPauseGraphUseCase {
	return enterusecase.NewNormalPauseGraphUseCase(transactions, repository, clock)
}

func validDraftResultRevisionIdentity(
	expected *draftusecase.RevisionExpectation,
	expectedPreviousRevisionID uuid.UUID,
	resultID uuid.UUID,
	commandID uuid.UUID,
	pauseID uuid.UUID,
	actorID uuid.UUID,
) bool {
	if expected == nil {
		return resultID == uuid.Nil && expectedPreviousRevisionID == uuid.Nil
	}
	return resultID != uuid.Nil && resultID != commandID && resultID != pauseID && resultID != actorID &&
		resultID != expected.RevisionID && resultID != expectedPreviousRevisionID && resultID != expected.ServiceEpoch &&
		validDraftPreviousRevision(*expected, expectedPreviousRevisionID)
}
