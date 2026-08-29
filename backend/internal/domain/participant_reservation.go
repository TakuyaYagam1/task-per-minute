package domain

import (
	"time"

	"github.com/google/uuid"
)

type ParticipantReservationOwner string

const (
	ParticipantReservationOwnerArena       ParticipantReservationOwner = "arena"
	ParticipantReservationOwnerCasualQueue ParticipantReservationOwner = "casual_queue"
	ParticipantReservationOwnerCasualDuel  ParticipantReservationOwner = "casual_duel"
)

func (o ParticipantReservationOwner) IsValid() bool {
	switch o {
	case ParticipantReservationOwnerArena,
		ParticipantReservationOwnerCasualQueue,
		ParticipantReservationOwnerCasualDuel:
		return true
	default:
		return false
	}
}

type ParticipantReservation struct {
	PlayerID      uuid.UUID
	ReservationID uuid.UUID
	OwnerKind     ParticipantReservationOwner
	OwnerID       uuid.UUID
	Revision      int64
	AcquiredAt    time.Time
	UpdatedAt     time.Time
}

func (r ParticipantReservation) IsValid() bool {
	return r.PlayerID != uuid.Nil && r.ReservationID != uuid.Nil && r.OwnerKind.IsValid() &&
		r.OwnerID != uuid.Nil && r.Revision >= 1 && !r.AcquiredAt.IsZero() &&
		!r.UpdatedAt.Before(r.AcquiredAt)
}
