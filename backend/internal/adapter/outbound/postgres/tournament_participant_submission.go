package postgres

import (
	resultpostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result"
	participantsubmission "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/submission"
)

type ParticipantSubmissionRepository = participantsubmission.ParticipantSubmissionRepository

func NewParticipantSubmissionRepository(
	tx *TxManager,
	results *resultpostgres.ResultPostgres,
) *ParticipantSubmissionRepository {
	return participantsubmission.NewParticipantSubmissionRepository(tx, results)
}
