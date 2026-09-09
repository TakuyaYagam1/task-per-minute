package websocket

import (
	"context"

	"github.com/google/uuid"

	tournamentws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/tournament"
)

type TournamentParticipantConnectionRequest struct {
	Principal    tournamentws.ParticipantRealtimePrincipal
	TournamentID uuid.UUID
}

type TournamentPublicConnectionRequest struct {
	TournamentID uuid.UUID
}

type TournamentOperatorConnectionRequest struct {
	Principal    tournamentws.OperatorRealtimePrincipal
	TournamentID uuid.UUID
}

type TournamentParticipantConnectionFlow interface {
	OpenTournamentParticipant(
		ctx context.Context,
		request TournamentParticipantConnectionRequest,
	) (TournamentParticipantPayload, error)
}

type TournamentPublicConnectionFlow interface {
	OpenTournamentPublic(
		ctx context.Context,
		request TournamentPublicConnectionRequest,
	) (TournamentPublicPayload, error)
}

type TournamentOperatorConnectionFlow interface {
	OpenTournamentOperator(
		ctx context.Context,
		request TournamentOperatorConnectionRequest,
	) (TournamentOperatorPayload, error)
}
