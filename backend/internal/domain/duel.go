package domain

import (
	"time"

	"github.com/google/uuid"
)

const (
	ErrorCodeDuelNotFound       ErrorCode = "duel.not_found"
	ErrorCodeDuelFinished       ErrorCode = "duel.finished"
	ErrorCodeDuelDeadlinePassed ErrorCode = "duel.deadline_passed"
	ErrorCodeFlagIncorrect      ErrorCode = "duel.flag_incorrect"
	ErrorCodeNotDuelParticipant ErrorCode = "duel.not_participant"
)

var (
	ErrDuelNotFound       = &Error{Code: ErrorCodeDuelNotFound, Message: "duel not found"}
	ErrDuelFinished       = &Error{Code: ErrorCodeDuelFinished, Message: "duel is already finished"}
	ErrDuelDeadlinePassed = &Error{Code: ErrorCodeDuelDeadlinePassed, Message: "duel deadline has passed"}
	ErrFlagIncorrect      = &Error{Code: ErrorCodeFlagIncorrect, Message: "flag is incorrect"}
	ErrNotDuelParticipant = &Error{Code: ErrorCodeNotDuelParticipant, Message: "player is not a participant of this duel"}
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
