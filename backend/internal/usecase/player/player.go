package player

import (
	"time"

	"github.com/google/uuid"
)

type StatsInput struct {
	Wins               int
	AverageSolveTimeMs int64
}

type PlayerInput struct {
	Username           string
	Wins               int
	AverageSolveTimeMs int64
}

type PlayerRecord struct {
	PlayerID           uuid.UUID
	Username           string
	CreatedAt          time.Time
	DeletedAt          *time.Time
	Wins               int
	AverageSolveTimeMs int64
	StatsOverridden    bool
}
