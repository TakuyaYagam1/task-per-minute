package postgres

import participantsubmission "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/submission"

const participantIncorrectFlagReason = "incorrect_flag"

type ParticipantSubmissionRepository = participantsubmission.ParticipantSubmissionRepository

func NewParticipantSubmissionRepository(
	tx *TxManager,
	results *ResultPostgres,
) *ParticipantSubmissionRepository {
	return participantsubmission.NewParticipantSubmissionRepository(tx, results)
}
