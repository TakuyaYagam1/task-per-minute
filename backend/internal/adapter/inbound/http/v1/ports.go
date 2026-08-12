package v1

import (
	"context"
	"io"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	adminusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/admin"
	duelusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/duel"
	leaderboardusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/leaderboard"
	playerusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player"
)

type PlayerService interface {
	Join(ctx context.Context, username string) (*domain.Player, error)
	GetMe(ctx context.Context, sessionToken uuid.UUID) (*playerusecase.PlayerWithActiveDuel, error)
	Logout(ctx context.Context, sessionToken uuid.UUID) error
}

type AdminAuthService interface {
	Login(ctx context.Context, password string) (*adminusecase.TokenPair, error)
	Refresh(ctx context.Context, refreshToken string) (*adminusecase.TokenPair, error)
	Logout(ctx context.Context, refreshToken string, accessTokens ...string) error
}

type AdminTaskService interface {
	CreateTask(ctx context.Context, in adminusecase.TaskInput) (*domain.Task, error)
	GetTask(ctx context.Context, id uuid.UUID) (*domain.Task, error)
	ListTasks(ctx context.Context) ([]*domain.Task, error)
	UpdateTask(ctx context.Context, id uuid.UUID, in adminusecase.TaskInput) (*domain.Task, error)
	DeleteTask(ctx context.Context, id uuid.UUID) error
}

type AdminPlayerService interface {
	ListPlayers(ctx context.Context, includeDeleted bool) ([]adminusecase.PlayerRecord, error)
	ListPlayerAudit(ctx context.Context, id uuid.UUID, limit int32) ([]adminusecase.PlayerAuditEvent, error)
	UpdatePlayer(ctx context.Context, id uuid.UUID, in adminusecase.PlayerInput, actor adminusecase.Actor) (*adminusecase.PlayerRecord, error)
	DeletePlayer(ctx context.Context, id uuid.UUID, actor adminusecase.Actor) error
}

type AdminPlayerEventSubscriber interface {
	SubscribeAdminPlayerChanges(ctx context.Context) (<-chan struct{}, func(), error)
}

type UploadService interface {
	UploadSourceFile(ctx context.Context, taskID uuid.UUID, reader io.Reader, size int64, contentType string) (string, error)
	ClearSourceFile(ctx context.Context, taskID uuid.UUID, in adminusecase.TaskInput) (*domain.Task, error)
	PresignedSourceFileURL(ctx context.Context, taskID uuid.UUID) (string, error)
	DeleteSourceFile(ctx context.Context, taskID uuid.UUID, sourceFileURL *string) error
}

type LeaderboardService interface {
	Top50(ctx context.Context) ([]leaderboardusecase.Entry, error)
}

type DuelService interface {
	GetDuel(ctx context.Context, duelID, playerID uuid.UUID) (*duelusecase.Detail, error)
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
