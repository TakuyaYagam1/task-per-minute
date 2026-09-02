package arena

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	appobservability "github.com/TakuyaYagam1/task-per-minute/internal/observability"
)

const (
	ArenaTransportConnect    = "connect"
	ArenaTransportResume     = "resume"
	ArenaTransportReject     = "reject"
	ArenaTransportDelivery   = "delivery"
	ArenaTransportDisconnect = "disconnect"

	arenaTransportEvent          = "arena.websocket"
	arenaTransportEntityKind     = "ws_connection"
	arenaTransportCorrelationMax = 128
)

var ErrArenaTransportEvent = errors.New("invalid Arena transport event")

// ArenaTransportEvent contains only safe transport metadata.
type ArenaTransportEvent struct {
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
	observer appobservability.ArenaEventObserver,
	input ArenaTransportEvent,
) error {
	if input.TournamentID == uuid.Nil || !validArenaTransportRole(input.Role) ||
		!validArenaTransportAction(input.Action) {
		return ErrArenaTransportEvent
	}
	correlationID := TransportCorrelationID(input.CorrelationID, input.TournamentID)
	return appobservability.EmitArenaEvent(ctx, observer, appobservability.ArenaEventInput{
		Event:         arenaTransportEvent,
		Outcome:       input.Outcome,
		CorrelationID: correlationID,
		TournamentID:  input.TournamentID.String(),
		EntityKind:    arenaTransportEntityKind,
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
	if validArenaTransportCorrelation(requestID) {
		return requestID
	}
	if tournamentID != uuid.Nil {
		return tournamentID.String()
	}
	return ""
}

func validArenaTransportCorrelation(value string) bool {
	if value == "" || len(value) > arenaTransportCorrelationMax {
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

func validArenaTransportRole(role string) bool {
	switch role {
	case "participant", "public", "operator":
		return true
	default:
		return false
	}
}

func validArenaTransportAction(action string) bool {
	switch action {
	case ArenaTransportConnect, ArenaTransportResume, ArenaTransportReject,
		ArenaTransportDelivery, ArenaTransportDisconnect:
		return true
	default:
		return false
	}
}
