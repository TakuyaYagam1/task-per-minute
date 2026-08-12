package admin

import (
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type TaskInput struct {
	Title         string
	Description   string
	Category      domain.Category
	Difficulty    domain.Difficulty
	TimeLimit     int
	Flag          string
	Hints         []string
	TaskURL       *string
	SourceFileURL *string
}

type PlayerStatsInput struct {
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
	Status             domain.PlayerStatus
	CreatedAt          time.Time
	DeletedAt          *time.Time
	Wins               int
	AverageSolveTimeMs int64
	StatsOverridden    bool
}

type Actor struct {
	Subject string
	JTI     string
}

type PlayerAuditAction string

const (
	PlayerAuditActionUpdate PlayerAuditAction = "update"
	PlayerAuditActionDelete PlayerAuditAction = "delete"
)

type PlayerAuditState struct {
	Username           string `json:"username"`
	Status             string `json:"status"`
	Wins               int    `json:"wins"`
	AverageSolveTimeMs int64  `json:"average_solve_time_ms"`
	StatsOverridden    bool   `json:"stats_overridden"`
	Deleted            bool   `json:"deleted"`
}

type PlayerAuditInput struct {
	Actor       Actor
	Action      PlayerAuditAction
	PlayerID    uuid.UUID
	BeforeState PlayerAuditState
	AfterState  PlayerAuditState
	CreatedAt   time.Time
}

type PlayerAuditEvent struct {
	ID          uuid.UUID
	Actor       Actor
	Action      PlayerAuditAction
	PlayerID    uuid.UUID
	BeforeState PlayerAuditState
	AfterState  PlayerAuditState
	CreatedAt   time.Time
}

type TokenKind string

const (
	TokenKindAccess  TokenKind = "access"
	TokenKindRefresh TokenKind = "refresh"
)

type Claims struct {
	JTI       string
	Subject   string
	Kind      TokenKind
	IssuedAt  time.Time
	ExpiresAt time.Time
}

type TokenPair struct {
	AccessToken      string
	RefreshToken     string
	AccessExpiresAt  time.Time
	RefreshExpiresAt time.Time
}
