package usecase

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

var ErrInvalidTournamentParticipantConnectionCommand = errors.New("invalid tournament participant connection command")

// TournamentParticipantConnectionUseCase is the narrow lifecycle boundary
// for an authenticated participant realtime connection. Implementations own
// idempotency, stale-generation fencing, and multi-tab presence semantics.
type TournamentParticipantConnectionUseCase interface {
	Connect(ctx context.Context, command TournamentParticipantConnectionCommand) error
	Disconnect(ctx context.Context, command TournamentParticipantConnectionCommand) error
}

// TournamentParticipantConnectionCommand contains only identity resolved by
// the server and the durable delivery fence for the socket being changed.
// Participant authority must be resolved by the concrete usecase.
type TournamentParticipantConnectionCommand struct {
	TournamentID         uuid.UUID
	PlayerID             uuid.UUID
	ConnectionID         uuid.UUID
	ConnectionGeneration int64
}

func (command TournamentParticipantConnectionCommand) Validate() error {
	if command.TournamentID == uuid.Nil || command.PlayerID == uuid.Nil ||
		command.ConnectionID == uuid.Nil || command.ConnectionGeneration < 1 {
		return ErrInvalidTournamentParticipantConnectionCommand
	}
	return nil
}
