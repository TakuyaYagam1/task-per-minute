package postgres

import participantstate "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant"

var ErrParticipantStateInvalid = participantstate.ErrParticipantStateInvalid

type ParticipantStatePostgres = participantstate.ParticipantStatePostgres

func NewParticipantStatePostgres(tx *TxManager) *ParticipantStatePostgres {
	return participantstate.NewParticipantStatePostgres(tx)
}

// cloneParticipantStateString remains available to the unmoved golden view.
func cloneParticipantStateString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
