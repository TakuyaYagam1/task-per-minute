package player

import (
	"time"

	"github.com/google/uuid"
)

type Actor struct {
	Subject string
	JTI     string
}

type AuditAction string

const (
	AuditActionUpdate AuditAction = "update"
	AuditActionDelete AuditAction = "delete"
)

type AuditState struct {
	Username           string `json:"username"`
	Wins               int    `json:"wins"`
	AverageSolveTimeMs int64  `json:"average_solve_time_ms"`
	StatsOverridden    bool   `json:"stats_overridden"`
	Deleted            bool   `json:"deleted"`
}

type AuditInput struct {
	Actor       Actor
	Action      AuditAction
	PlayerID    uuid.UUID
	BeforeState AuditState
	AfterState  AuditState
	CreatedAt   time.Time
}

type AuditEvent struct {
	ID          uuid.UUID
	Actor       Actor
	Action      AuditAction
	PlayerID    uuid.UUID
	BeforeState AuditState
	AfterState  AuditState
	CreatedAt   time.Time
}
