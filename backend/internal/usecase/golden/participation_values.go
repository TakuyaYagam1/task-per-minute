package golden

import (
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenstate "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/state"
	"github.com/google/uuid"
)

// FindMember preserves the root Golden package membership lookup API.
func FindMember(members []domain.GoldenMember, participantID uuid.UUID) (domain.GoldenMember, bool) {
	return goldenstate.FindMember(members, participantID)
}
