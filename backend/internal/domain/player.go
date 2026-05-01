package domain

import (
	"time"

	"github.com/google/uuid"
)

type PlayerStatus string

const (
	PlayerStatusIdle   PlayerStatus = "idle"
	PlayerStatusQueued PlayerStatus = "queued"
	PlayerStatusInDuel PlayerStatus = "in_duel"
)

func (s PlayerStatus) IsValid() bool {
	switch s {
	case PlayerStatusIdle, PlayerStatusQueued, PlayerStatusInDuel:
		return true
	}
	return false
}

func (s PlayerStatus) String() string {
	return string(s)
}

type Player struct {
	ID           uuid.UUID
	Username     string
	SessionToken *uuid.UUID
	Status       PlayerStatus
	CreatedAt    time.Time
}
