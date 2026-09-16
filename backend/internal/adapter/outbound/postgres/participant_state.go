package postgres

import participantstate "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant"

var ErrParticipantStateInvalid = participantstate.ErrParticipantStateInvalid

type ParticipantStatePostgres = participantstate.ParticipantStatePostgres

func NewParticipantStatePostgres(tx *TxManager) *ParticipantStatePostgres {
	return participantstate.NewParticipantStatePostgres(tx)
}
