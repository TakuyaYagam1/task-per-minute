package domain

import (
	"time"

	"github.com/google/uuid"
)

type ParticipantReservation struct {
	PlayerID      uuid.UUID
	ReservationID uuid.UUID
	TournamentID  uuid.UUID
	Revision      int64
	AcquiredAt    time.Time
	UpdatedAt     time.Time
}

func (r ParticipantReservation) IsValid() bool {
	return r.PlayerID != uuid.Nil && r.ReservationID != uuid.Nil &&
		r.TournamentID != uuid.Nil && r.Revision >= 1 && !r.AcquiredAt.IsZero() &&
		!r.UpdatedAt.Before(r.AcquiredAt)
}
