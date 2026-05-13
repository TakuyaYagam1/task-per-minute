package usecase

import (
	"context"
	"io"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type TxManager interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}

type PlayerRepo interface {
	Create(ctx context.Context, username string) (*domain.Player, error)
	JoinByUsername(ctx context.Context, username string, sessionToken uuid.UUID) (*domain.Player, error)
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Player, error)
	GetByUsername(ctx context.Context, username string) (*domain.Player, error)
	GetBySessionToken(ctx context.Context, token uuid.UUID) (*domain.Player, error)
	UpdateSessionToken(ctx context.Context, id uuid.UUID, token *uuid.UUID) (*domain.Player, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status domain.PlayerStatus) (*domain.Player, error)
	UpdateStatusIfCurrent(ctx context.Context, id uuid.UUID, from, to domain.PlayerStatus) (*domain.Player, bool, error)
}

type DuelRepo interface {
	Create(ctx context.Context, player1ID, player2ID uuid.UUID, deadline time.Time) (*domain.Duel, error)
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Duel, error)
	GetActiveByPlayerID(ctx context.Context, playerID uuid.UUID) (*domain.Duel, error)
	UpdateDeadline(ctx context.Context, id uuid.UUID, deadline time.Time) (*domain.Duel, error)
	Finish(ctx context.Context, id uuid.UUID, winnerID *uuid.UUID, finishedAt time.Time, status domain.DuelStatus) (*domain.Duel, error)
	CreateDuelPlayerTask(ctx context.Context, duelID, playerID, taskID uuid.UUID) error
	GetDuelPlayerTask(ctx context.Context, duelID, playerID uuid.UUID) (*domain.DuelPlayerTask, error)
	GetPlayerTask(ctx context.Context, duelID, playerID uuid.UUID) (*domain.Task, error)
	MarkSolved(ctx context.Context, duelID, playerID uuid.UUID, solvedAt time.Time) error
}

type ActiveDuelRepo interface {
	ListActive(ctx context.Context) ([]*domain.Duel, error)
	Finish(ctx context.Context, id uuid.UUID, winnerID *uuid.UUID, finishedAt time.Time, status domain.DuelStatus) (*domain.Duel, error)
}

type PlayerStatusRepo interface {
	UpdateStatus(ctx context.Context, id uuid.UUID, status domain.PlayerStatus) (*domain.Player, error)
}

type QueuedPlayerResetter interface {
	ResetQueuedToIdle(ctx context.Context) (int64, error)
}

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

type TaskRepo interface {
	Create(ctx context.Context, in TaskInput) (*domain.Task, error)
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Task, error)
	List(ctx context.Context) ([]*domain.Task, error)
	ListByDifficulty(ctx context.Context, difficulty domain.Difficulty) ([]*domain.Task, error)
	Update(ctx context.Context, id uuid.UUID, in TaskInput) (*domain.Task, error)
	Delete(ctx context.Context, id uuid.UUID) error
	IsUsedInActiveDuel(ctx context.Context, id uuid.UUID) (bool, error)
	CountByDifficulty(ctx context.Context, difficulty domain.Difficulty) (int64, error)
	CountSolvedByDifficulty(ctx context.Context, playerID uuid.UUID, difficulty domain.Difficulty) (int64, error)
}

type HistoryRepo interface {
	AddSolved(ctx context.Context, playerID, taskID uuid.UUID, solvedAt time.Time) error
	ListSolvedTaskIDs(ctx context.Context, playerID uuid.UUID) ([]uuid.UUID, error)
	SelectUnsolvedTaskByDifficulty(ctx context.Context, playerID uuid.UUID, difficulty domain.Difficulty) (*domain.Task, error)
	SelectAnyTaskByDifficulty(ctx context.Context, difficulty domain.Difficulty) (*domain.Task, error)
}

type LeaderboardPlayerStats struct {
	PlayerID           uuid.UUID
	Username           string
	Wins               int
	AverageSolveTimeMs int64
}

type AdminPlayerStatsInput struct {
	Wins               int
	AverageSolveTimeMs int64
}

type AdminPlayerInput struct {
	Username           string
	Wins               int
	AverageSolveTimeMs int64
}

type AdminPlayerRecord struct {
	PlayerID           uuid.UUID
	Username           string
	Status             domain.PlayerStatus
	CreatedAt          time.Time
	DeletedAt          *time.Time
	Wins               int
	AverageSolveTimeMs int64
	StatsOverridden    bool
}

type AdminActor struct {
	Subject string
	JTI     string
}

type AdminPlayerAuditAction string

const (
	AdminPlayerAuditActionUpdate AdminPlayerAuditAction = "update"
	AdminPlayerAuditActionDelete AdminPlayerAuditAction = "delete"
)

type AdminPlayerAuditState struct {
	Username           string `json:"username"`
	Status             string `json:"status"`
	Wins               int    `json:"wins"`
	AverageSolveTimeMs int64  `json:"average_solve_time_ms"`
	StatsOverridden    bool   `json:"stats_overridden"`
	Deleted            bool   `json:"deleted"`
}

type AdminPlayerAuditInput struct {
	Actor       AdminActor
	Action      AdminPlayerAuditAction
	PlayerID    uuid.UUID
	BeforeState AdminPlayerAuditState
	AfterState  AdminPlayerAuditState
	CreatedAt   time.Time
}

type AdminPlayerAuditEvent struct {
	ID          uuid.UUID
	Actor       AdminActor
	Action      AdminPlayerAuditAction
	PlayerID    uuid.UUID
	BeforeState AdminPlayerAuditState
	AfterState  AdminPlayerAuditState
	CreatedAt   time.Time
}

type LeaderboardRepo interface {
	TopStats(ctx context.Context, limit int32) ([]LeaderboardPlayerStats, error)
}

type AdminPlayerRepo interface {
	ListAdminPlayers(ctx context.Context, includeDeleted bool) ([]AdminPlayerRecord, error)
	GetAdminPlayer(ctx context.Context, id uuid.UUID) (*AdminPlayerRecord, error)
	GetAdminPlayerIncludingDeleted(ctx context.Context, id uuid.UUID) (*AdminPlayerRecord, error)
	UpdateAdminPlayerUsername(ctx context.Context, id uuid.UUID, username string) error
	UpsertAdminPlayerStats(ctx context.Context, id uuid.UUID, in AdminPlayerStatsInput, updatedAt time.Time) error
	SoftDeleteAdminPlayer(ctx context.Context, id uuid.UUID, deletedUsername string, deletedAt time.Time) error
	CreateAdminPlayerAudit(ctx context.Context, in AdminPlayerAuditInput) error
	ListAdminPlayerAudit(ctx context.Context, playerID uuid.UUID, limit int32) ([]AdminPlayerAuditEvent, error)
}

type LeaderboardScore struct {
	Username string
	Wins     int
}

type LeaderboardBumper interface {
	IncrementWin(ctx context.Context, username string) error
}

type LeaderboardInvalidator interface {
	Invalidate()
}

type LeaderboardStore interface {
	LeaderboardBumper
	WinScores(ctx context.Context) ([]LeaderboardScore, error)
}

type MatchmakingQueue interface {
	Enqueue(ctx context.Context, playerID uuid.UUID) error
	PopPair(ctx context.Context) (uuid.UUID, uuid.UUID, bool, error)
	Remove(ctx context.Context, playerID uuid.UUID) error
}

type MatchmakingQueueCleaner interface {
	Clear(ctx context.Context) error
}

type RevocationStore interface {
	Revoke(ctx context.Context, jti string, expiresAt time.Time) error
	IsRevoked(ctx context.Context, jti string) (bool, error)
}

type SourceFileStorage interface {
	Upload(ctx context.Context, key string, r io.Reader, size int64) (string, error)
	PresignedGetURL(ctx context.Context, key string, ttl time.Duration) (string, error)
	Delete(ctx context.Context, key string) error
}

type DuelBroadcaster interface {
	BroadcastOpponentDisconnected(ctx context.Context, duelID, playerID uuid.UUID, reconnectDeadline time.Time)
	BroadcastDuelExpired(ctx context.Context, duelID uuid.UUID)
	BroadcastDuelFinished(ctx context.Context, duel *domain.Duel)
}

type HealthChecker interface {
	Check(ctx context.Context) error
}

type HealthCheckerFunc func(ctx context.Context) error

func (f HealthCheckerFunc) Check(ctx context.Context) error { return f(ctx) }

type SchemaVersionReader interface {
	SchemaVersion(ctx context.Context) (int64, error)
}

type SchemaVersionReaderFunc func(ctx context.Context) (int64, error)

func (f SchemaVersionReaderFunc) SchemaVersion(ctx context.Context) (int64, error) {
	return f(ctx)
}

type PlayerWithActiveDuel struct {
	Player     *domain.Player
	ActiveDuel *domain.Duel
}

type Player interface {
	Join(ctx context.Context, username string) (*domain.Player, error)
	GetMe(ctx context.Context, sessionToken uuid.UUID) (*PlayerWithActiveDuel, error)
	Logout(ctx context.Context, sessionToken uuid.UUID) error
}

type MatchResult struct {
	Duel        *domain.Duel
	Player1Task *domain.Task
	Player2Task *domain.Task
}

type FlagSubmitResult struct {
	Correct         bool
	AlreadyFinished bool
	FinishedDuel    *domain.Duel
	Winner          *domain.Player
}

type DuelDetail struct {
	Duel        *domain.Duel
	PlayerTasks []*domain.DuelPlayerTask
}

type Duel interface {
	GetDuel(ctx context.Context, duelID, playerID uuid.UUID) (*DuelDetail, error)
}

type AdminTask interface {
	CreateTask(ctx context.Context, in TaskInput) (*domain.Task, error)
	GetTask(ctx context.Context, id uuid.UUID) (*domain.Task, error)
	ListTasks(ctx context.Context) ([]*domain.Task, error)
	UpdateTask(ctx context.Context, id uuid.UUID, in TaskInput) (*domain.Task, error)
	DeleteTask(ctx context.Context, id uuid.UUID) error
}

type AdminPlayer interface {
	ListPlayers(ctx context.Context, includeDeleted bool) ([]AdminPlayerRecord, error)
	ListPlayerAudit(ctx context.Context, id uuid.UUID, limit int32) ([]AdminPlayerAuditEvent, error)
	UpdatePlayer(ctx context.Context, id uuid.UUID, in AdminPlayerInput, actor AdminActor) (*AdminPlayerRecord, error)
	DeletePlayer(ctx context.Context, id uuid.UUID, actor AdminActor) error
}

type Upload interface {
	UploadSourceFile(ctx context.Context, taskID uuid.UUID, reader io.Reader, size int64, contentType string) (string, error)
	ClearSourceFile(ctx context.Context, taskID uuid.UUID, in TaskInput) (*domain.Task, error)
	DeleteSourceFile(ctx context.Context, taskID uuid.UUID, sourceFileURL *string) error
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

type AdminAuth interface {
	Login(ctx context.Context, password string) (*TokenPair, error)
	Refresh(ctx context.Context, refreshToken string) (*TokenPair, error)
	Logout(ctx context.Context, refreshToken string, accessTokens ...string) error
}

type LeaderboardEntry struct {
	Rank               int
	Username           string
	Wins               int
	AverageSolveTimeMs int64
}

type Leaderboard interface {
	Top50(ctx context.Context) ([]LeaderboardEntry, error)
}
