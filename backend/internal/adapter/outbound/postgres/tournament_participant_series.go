package postgres

import (
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	participantauthority "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/authority"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
)

type participantSeriesAuthority = participantauthority.ParticipantSeriesAuthority

func participantSeriesExecution(
	rows []sqlc.GetParticipantSeriesExecutionRow,
) (seriesdomain.Execution, participantSeriesAuthority, error) {
	return participantauthority.ParticipantSeriesExecution(rows)
}
