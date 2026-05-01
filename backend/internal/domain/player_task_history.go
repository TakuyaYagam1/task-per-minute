package domain

import (
	"time"

	"github.com/google/uuid"
)

type PlayerTaskHistory struct {
	PlayerID uuid.UUID
	TaskID   uuid.UUID
	SolvedAt time.Time
}
