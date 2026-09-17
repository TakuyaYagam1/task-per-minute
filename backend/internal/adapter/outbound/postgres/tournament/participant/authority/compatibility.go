package authority

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
)

// ParticipantSeriesAuthority is the series authority metadata exposed to the
// root facade for the remaining participant command implementations.
type ParticipantSeriesAuthority = participantSeriesAuthority

// ParticipantSeriesExecution parses the locked participant series rows for
// callers that still use the root postgres facade.
func ParticipantSeriesExecution(
	rows []sqlc.GetParticipantSeriesExecutionRow,
) (seriesdomain.Execution, ParticipantSeriesAuthority, error) {
	return participantSeriesExecution(rows)
}

// ParticipantCommandID derives the stable identifier for a participant
// command side effect.
func ParticipantCommandID(commandID uuid.UUID, role string) uuid.UUID {
	return participantCommandID(commandID, role)
}
