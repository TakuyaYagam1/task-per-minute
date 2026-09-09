package tournament

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	appobservability "github.com/TakuyaYagam1/task-per-minute/internal/observability"
)

const (
	TournamentTransportConnect    = "connect"
	TournamentTransportReject     = "reject"
	TournamentTransportDelivery   = "delivery"
	TournamentTransportDisconnect = "disconnect"

	tournamentTransportEvent          = "tournament.websocket"
	tournamentTransportEntityKind     = "ws_connection"
	tournamentTransportCorrelationMax = 128
)

var ErrTournamentTransportEvent = errors.New("invalid tournament transport event")

// TournamentTransportEvent contains only safe transport metadata.
type TournamentTransportEvent struct {
	CorrelationID string
	TournamentID  uuid.UUID
	Role          string
	Action        string
	Outcome       string
	ReasonCode    string
	Duration      time.Duration
	Revision      int64
}

// ObserveTransportEvent maps transport metadata to the canonical event model.
func ObserveTransportEvent(
	ctx context.Context,
	observer appobservability.TournamentEventObserver,
	input TournamentTransportEvent,
) error {
	if input.TournamentID == uuid.Nil || !validTournamentTransportRole(input.Role) ||
		!validTournamentTransportAction(input.Action) {
		return ErrTournamentTransportEvent
	}
	correlationID := TransportCorrelationID(input.CorrelationID, input.TournamentID)
	return appobservability.EmitTournamentEvent(ctx, observer, appobservability.TournamentEventInput{
		Event:         tournamentTransportEvent,
		Outcome:       input.Outcome,
		CorrelationID: correlationID,
		TournamentID:  input.TournamentID.String(),
		EntityKind:    tournamentTransportEntityKind,
		EntityID:      correlationID,
		Stage:         input.Role,
		Transition:    input.Action,
		Duration:      input.Duration,
		ReasonCode:    input.ReasonCode,
		Revision:      input.Revision,
	})
}

// TransportCorrelationID chooses a bounded request or tournament correlation.
func TransportCorrelationID(requestID string, tournamentID uuid.UUID) string {
	if validTournamentTransportCorrelation(requestID) {
		return requestID
	}
	if tournamentID != uuid.Nil {
		return tournamentID.String()
	}
	return ""
}

func validTournamentTransportCorrelation(value string) bool {
	if value == "" || len(value) > tournamentTransportCorrelationMax {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '.' || character == '_' ||
			character == ':' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func validTournamentTransportRole(role string) bool {
	switch role {
	case "participant", "public", "operator":
		return true
	default:
		return false
	}
}

func validTournamentTransportAction(action string) bool {
	switch action {
	case TournamentTransportConnect, TournamentTransportReject,
		TournamentTransportDelivery, TournamentTransportDisconnect:
		return true
	default:
		return false
	}
}
