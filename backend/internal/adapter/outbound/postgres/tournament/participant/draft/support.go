package draft

import (
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
)

func tstz(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}

func swissDraftIdentity(seriesID uuid.UUID) (playoff.FinalStageIDs, error) {
	ids, err := playoff.FinalStageIdentity(seriesID)
	if err != nil {
		return playoff.FinalStageIDs{}, err
	}
	ids.FinalSeriesID = seriesID
	ids.CategoryRevisionID = tournamentAdminExecutionID(seriesID, "swiss-category-revision")
	ids.DraftID = tournamentAdminExecutionID(seriesID, "swiss-draft")
	ids.FirstSlotID = tournamentAdminExecutionID(seriesID, "swiss-game-slot-1")
	if !ids.Valid() {
		return playoff.FinalStageIDs{}, domain.ErrValidation
	}
	return ids, nil
}

func tournamentAdminExecutionID(namespace uuid.UUID, role string) uuid.UUID {
	return uuid.NewSHA1(namespace, []byte(role))
}

// ParticipantDraftExecution keeps the validated aggregate-to-usecase mapping
// available to neighboring adapters without importing this child package's
// private helpers.
func ParticipantDraftExecution(
	aggregate *DraftAggregate,
	targetRevision int64,
) (*draftusecase.Execution, error) {
	return participantDraftExecution(aggregate, targetRevision)
}
