package leaderboard

import "github.com/google/uuid"

type WinsFilter string

const (
	WinsAll         WinsFilter = "all"
	WinsWithWins    WinsFilter = "withwins"
	WinsWithoutWins WinsFilter = "withoutwins"
	DefaultPageSize            = 100
	MaxPageSize                = 100
	MaxSearchLength            = 50
)

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
	Avatar             *AvatarMetadata
}

type AvatarMetadata struct {
	PlayerID    uuid.UUID
	Version     string
	ContentType string
}

type PageQuery struct {
	Search  string
	Wins    WinsFilter
	Page    int32
	PerPage int32
}

type PageRows struct {
	Entries []Entry
	Total   int64
}

type PageResult struct {
	Entries    []Entry
	Page       int32
	PerPage    int32
	Total      int64
	TotalPages int64
}
