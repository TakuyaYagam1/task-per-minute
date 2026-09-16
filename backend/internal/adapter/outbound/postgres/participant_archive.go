package postgres

import "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/participantarchive"

type ParticipantArchivePostgres = participantarchive.ParticipantArchivePostgres

func NewParticipantArchivePostgres(tx *TxManager) *ParticipantArchivePostgres {
	return participantarchive.NewParticipantArchivePostgres(tx)
}
