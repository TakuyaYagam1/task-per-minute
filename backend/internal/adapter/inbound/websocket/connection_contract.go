package websocket

import (
	"context"

	"github.com/google/uuid"

	tournamentws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/tournament"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
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

// TournamentParticipantLifecycleFlow aliases the transport-neutral lifecycle
// contract. It is intentionally separate from the read-only participant flow.
type TournamentParticipantLifecycleFlow = usecase.TournamentParticipantConnectionUseCase

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
