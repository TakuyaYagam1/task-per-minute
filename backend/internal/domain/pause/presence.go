package pause

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var ErrInvalidPresence = errors.New("invalid pause presence")

type PresenceState string

const (
	PresenceStateConnected    PresenceState = "connected"
	PresenceStateDisconnected PresenceState = "disconnected"
)

type PausePresence struct {
	ID             uuid.UUID
	TournamentID   uuid.UUID
	RosterID       uuid.UUID
	SeriesID       uuid.UUID
	ParticipantID  uuid.UUID
	State          PresenceState
	PresenceEpoch  int64
	Revision       int64
	ConnectedAt    time.Time
	DisconnectedAt *time.Time
	UpdatedAt      time.Time
}

func (presence PausePresence) Validate() error {
	if !validPresenceIdentity(presence) || !validPresenceTimeline(presence) {
		return invalidPresence("invalid identity or revision")
	}
	return validatePresenceState(presence)
}

func validPresenceIdentity(presence PausePresence) bool {
	return presence.ID != uuid.Nil && presence.TournamentID != uuid.Nil &&
		presence.RosterID != uuid.Nil && presence.SeriesID != uuid.Nil &&
		presence.ParticipantID != uuid.Nil && presence.PresenceEpoch >= 1 &&
		presence.Revision >= 1
}

func validPresenceTimeline(presence PausePresence) bool {
	return domain.IsValidServerTime(presence.ConnectedAt) &&
		domain.IsValidServerTime(presence.UpdatedAt) &&
		!presence.UpdatedAt.Before(presence.ConnectedAt)
}

func validatePresenceState(presence PausePresence) error {
	switch presence.State {
	case PresenceStateConnected:
		if presence.DisconnectedAt != nil {
			return invalidPresence("connected presence has disconnect time")
		}
	case PresenceStateDisconnected:
		if presence.DisconnectedAt == nil || !domain.IsValidServerTime(*presence.DisconnectedAt) ||
			presence.DisconnectedAt.Before(presence.ConnectedAt) || presence.UpdatedAt.Before(*presence.DisconnectedAt) {
			return invalidPresence("disconnected presence lacks valid time")
		}
	default:
		return invalidPresence("unknown state %q", presence.State)
	}
	return nil
}

func TimeCoversPresenceHistory(at time.Time, presence PausePresence) bool {
	if at.Before(presence.ConnectedAt) || at.Before(presence.UpdatedAt) {
		return false
	}
	return presence.DisconnectedAt == nil || !at.Before(*presence.DisconnectedAt)
}

func invalidPresence(message string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidPresence, fmt.Sprintf(message, arguments...))
}
