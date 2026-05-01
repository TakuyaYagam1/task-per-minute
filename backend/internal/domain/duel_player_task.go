package domain

import (
	"time"

	"github.com/google/uuid"
)

type DuelPlayerTask struct {
	DuelID   uuid.UUID
	PlayerID uuid.UUID
	TaskID   uuid.UUID
	Solved   bool
	SolvedAt *time.Time
}
