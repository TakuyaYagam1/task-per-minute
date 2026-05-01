package domain

import (
	"time"

	"github.com/google/uuid"
)

type DuelStatus string

const (
	DuelStatusActive   DuelStatus = "active"
	DuelStatusFinished DuelStatus = "finished"
)

func (s DuelStatus) IsValid() bool {
	switch s {
	case DuelStatusActive, DuelStatusFinished:
		return true
	}
	return false
}

func (s DuelStatus) String() string {
	return string(s)
}

type Duel struct {
	ID         uuid.UUID
	Player1ID  uuid.UUID
	Player2ID  uuid.UUID
	Status     DuelStatus
	WinnerID   *uuid.UUID
	Deadline   time.Time
	StartedAt  time.Time
	FinishedAt *time.Time
}
