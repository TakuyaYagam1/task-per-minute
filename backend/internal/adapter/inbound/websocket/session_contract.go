package websocket

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"

	tournamentws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/tournament"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type PlayerSessionReader interface {
	GetBySessionToken(ctx context.Context, token uuid.UUID) (*domain.Player, error)
}

type TournamentOperatorSession struct {
	Principal tournamentws.OperatorRealtimePrincipal
	ExpiresAt time.Time
	Validate  func(context.Context) bool
}

type TournamentOperatorSessionResolver func(
	r *http.Request,
	tournamentID uuid.UUID,
) (TournamentOperatorSession, bool)
