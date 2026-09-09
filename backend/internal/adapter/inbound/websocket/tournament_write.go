package websocket

import (
	"errors"
	"fmt"

	"github.com/google/uuid"

	tournamentws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/tournament"
)

var ErrTournamentWriteScope = errors.New("tournament write scope mismatch")

type tournamentWriteScope struct {
	Role          TournamentRole
	TournamentID  uuid.UUID
	ParticipantID uuid.UUID
	OperatorID    uuid.UUID
}

func newTournamentWriteScope(
	scope tournamentConnectionScope,
	principal tournamentConnectionPrincipal,
) (tournamentWriteScope, error) {
	if scope.TournamentID == uuid.Nil {
		return tournamentWriteScope{}, ErrTournamentWriteScope
	}
	switch scope.Role {
	case TournamentRoleParticipant:
		return newParticipantWriteScope(scope, principal)
	case TournamentRolePublic:
		return newPublicWriteScope(scope, principal)
	case TournamentRoleOperator:
		return newOperatorWriteScope(scope, principal)
	default:
		return tournamentWriteScope{}, ErrTournamentWriteScope
	}
}

func newParticipantWriteScope(scope tournamentConnectionScope, principal tournamentConnectionPrincipal) (tournamentWriteScope, error) {
	if principal.Player == nil || principal.Player.ID == uuid.Nil || principal.ParticipantSession == uuid.Nil ||
		principal.Player.SessionToken == nil || *principal.Player.SessionToken != principal.ParticipantSession ||
		principal.OperatorSession != nil {
		return tournamentWriteScope{}, ErrTournamentWriteScope
	}
	return tournamentWriteScope{Role: scope.Role, TournamentID: scope.TournamentID, ParticipantID: principal.Player.ID}, nil
}

func newPublicWriteScope(scope tournamentConnectionScope, principal tournamentConnectionPrincipal) (tournamentWriteScope, error) {
	if principal.Player != nil || principal.ParticipantSession != uuid.Nil || principal.OperatorSession != nil {
		return tournamentWriteScope{}, ErrTournamentWriteScope
	}
	return tournamentWriteScope{Role: scope.Role, TournamentID: scope.TournamentID}, nil
}

func newOperatorWriteScope(scope tournamentConnectionScope, principal tournamentConnectionPrincipal) (tournamentWriteScope, error) {
	if principal.Player != nil || principal.ParticipantSession != uuid.Nil || principal.OperatorSession == nil ||
		!principal.OperatorSession.Principal.Authenticated ||
		principal.OperatorSession.Principal.PrincipalID == uuid.Nil ||
		principal.OperatorSession.Principal.Role != tournamentws.OperatorRealtimeRole ||
		principal.OperatorSession.Principal.TournamentID != scope.TournamentID {
		return tournamentWriteScope{}, ErrTournamentWriteScope
	}
	return tournamentWriteScope{
		Role:         scope.Role,
		TournamentID: scope.TournamentID,
		OperatorID:   principal.OperatorSession.Principal.PrincipalID,
	}, nil
}

func (scope tournamentWriteScope) validate() error {
	if scope.TournamentID == uuid.Nil {
		return ErrTournamentWriteScope
	}
	switch scope.Role {
	case TournamentRoleParticipant:
		if scope.ParticipantID == uuid.Nil || scope.OperatorID != uuid.Nil {
			return ErrTournamentWriteScope
		}
	case TournamentRolePublic:
		if scope.ParticipantID != uuid.Nil || scope.OperatorID != uuid.Nil {
			return ErrTournamentWriteScope
		}
	case TournamentRoleOperator:
		if scope.ParticipantID != uuid.Nil || scope.OperatorID == uuid.Nil {
			return ErrTournamentWriteScope
		}
	default:
		return ErrTournamentWriteScope
	}
	return nil
}

func validateTournamentWrite(scope tournamentWriteScope, data []byte) error {
	if err := scope.validate(); err != nil {
		return err
	}
	switch scope.Role {
	case TournamentRoleParticipant:
		return validateParticipantWrite(scope, data)
	case TournamentRolePublic:
		return validatePublicWrite(scope, data)
	case TournamentRoleOperator:
		return validateOperatorWrite(scope, data)
	default:
		return ErrTournamentWriteScope
	}
}

func validateParticipantWrite(scope tournamentWriteScope, data []byte) error {
	message, err := DecodeTournamentParticipantMessage(data)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrTournamentWriteScope, err)
	}
	if message.Rejected != nil {
		return nil
	}
	if message.Terminal != nil {
		if message.Terminal.TournamentID != scope.TournamentID {
			return ErrTournamentWriteScope
		}
		return nil
	}
	if message.Participant == nil || message.Participant.Envelope.TournamentID != scope.TournamentID ||
		message.Participant.Envelope.Participant.PlayerID != scope.ParticipantID {
		return ErrTournamentWriteScope
	}
	return nil
}

func validatePublicWrite(scope tournamentWriteScope, data []byte) error {
	message, err := DecodeTournamentPublicMessage(data)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrTournamentWriteScope, err)
	}
	if message.Rejected != nil {
		return nil
	}
	if message.Terminal != nil {
		if message.Terminal.TournamentID != scope.TournamentID {
			return ErrTournamentWriteScope
		}
		return nil
	}
	if message.Public == nil || message.Public.Envelope.TournamentID != scope.TournamentID {
		return ErrTournamentWriteScope
	}
	return nil
}

func validateOperatorWrite(scope tournamentWriteScope, data []byte) error {
	message, err := DecodeTournamentOperatorMessage(data)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrTournamentWriteScope, err)
	}
	if message.Rejected != nil {
		return nil
	}
	if message.Terminal != nil {
		if message.Terminal.TournamentID != scope.TournamentID {
			return ErrTournamentWriteScope
		}
		return nil
	}
	if message.Operator == nil || message.Operator.Envelope.TournamentID != scope.TournamentID {
		return ErrTournamentWriteScope
	}
	return nil
}
