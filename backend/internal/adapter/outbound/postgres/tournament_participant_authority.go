package postgres

import (
	participantauthority "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/authority"
)

// TournamentParticipantPostgres is kept as a root-package facade for the
// participant command authority implementation.
type TournamentParticipantPostgres = participantauthority.TournamentParticipantPostgres

// NewTournamentParticipantPostgres constructs the participant command
// authority facade.
func NewTournamentParticipantPostgres(tx *TxManager) *TournamentParticipantPostgres {
	return participantauthority.NewTournamentParticipantPostgres(tx)
}
