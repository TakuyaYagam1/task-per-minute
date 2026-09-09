package leaderboard

import "github.com/google/uuid"

type PlayerStats struct {
	PlayerID           uuid.UUID
	Username           string
	Wins               int
	AverageSolveTimeMs int64
}

type Entry struct {
	Rank               int
	Username           string
	Wins               int
	AverageSolveTimeMs int64
}
