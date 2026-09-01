package v1

import (
	"context"
	"io"
	"net/http"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
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

type ArenaOperatorController interface {
	ListArenaOperatorAudit(w http.ResponseWriter, r *http.Request, params api.ListArenaOperatorAuditParams)
	ListArenaOperatorTournaments(w http.ResponseWriter, r *http.Request, params api.ListArenaOperatorTournamentsParams)
	CreateArenaTournament(w http.ResponseWriter, r *http.Request, params api.CreateArenaTournamentParams)
	ApplyArenaTournamentAction(w http.ResponseWriter, r *http.Request, tournamentID api.ArenaTournamentId, params api.ApplyArenaTournamentActionParams)
	ExportArenaOperatorIncident(w http.ResponseWriter, r *http.Request, tournamentID api.ArenaTournamentId)
	ConfigureArenaTournamentPairings(w http.ResponseWriter, r *http.Request, tournamentID api.ArenaTournamentId, params api.ConfigureArenaTournamentPairingsParams)
	GetArenaOperatorRoster(w http.ResponseWriter, r *http.Request, tournamentID api.ArenaTournamentId)
	ReplaceArenaTournamentRoster(w http.ResponseWriter, r *http.Request, tournamentID api.ArenaTournamentId, params api.ReplaceArenaTournamentRosterParams)
	LockArenaTournamentRoster(w http.ResponseWriter, r *http.Request, tournamentID api.ArenaTournamentId, params api.LockArenaTournamentRosterParams)
	RunArenaRosterPreflight(w http.ResponseWriter, r *http.Request, tournamentID api.ArenaTournamentId, params api.RunArenaRosterPreflightParams)
	UnlockArenaTournamentRoster(w http.ResponseWriter, r *http.Request, tournamentID api.ArenaTournamentId, params api.UnlockArenaTournamentRosterParams)
	CorrectArenaGameResult(w http.ResponseWriter, r *http.Request, tournamentID api.ArenaTournamentId, seriesID api.ArenaSeriesId, gameID api.ArenaGameId, params api.CorrectArenaGameResultParams)
	GetArenaOperatorSnapshot(w http.ResponseWriter, r *http.Request, tournamentID api.ArenaTournamentId, params api.GetArenaOperatorSnapshotParams)
	ControlArenaTournamentWave(w http.ResponseWriter, r *http.Request, tournamentID api.ArenaTournamentId, waveID api.ArenaWaveId, params api.ControlArenaTournamentWaveParams)
}

type ArenaParticipantController interface {
	GetArenaParticipantAssignment(w http.ResponseWriter, r *http.Request, tournamentID api.ArenaTournamentId, assignmentID api.ArenaAssignmentId)
	GetArenaParticipantLobby(w http.ResponseWriter, r *http.Request, tournamentID api.ArenaTournamentId)
	SubmitArenaParticipantDraftAction(w http.ResponseWriter, r *http.Request, tournamentID api.ArenaTournamentId, seriesID api.ArenaSeriesId, params api.SubmitArenaParticipantDraftActionParams)
	SubmitArenaParticipantFlag(w http.ResponseWriter, r *http.Request, tournamentID api.ArenaTournamentId, seriesID api.ArenaSeriesId, gameID api.ArenaGameId, params api.SubmitArenaParticipantFlagParams)
	ApplyArenaParticipantPostSeriesAction(w http.ResponseWriter, r *http.Request, tournamentID api.ArenaTournamentId, seriesID api.ArenaSeriesId, params api.ApplyArenaParticipantPostSeriesActionParams)
	SurrenderArenaParticipantSeries(w http.ResponseWriter, r *http.Request, tournamentID api.ArenaTournamentId, seriesID api.ArenaSeriesId, params api.SurrenderArenaParticipantSeriesParams)
	GetArenaParticipantSnapshot(w http.ResponseWriter, r *http.Request, tournamentID api.ArenaTournamentId, params api.GetArenaParticipantSnapshotParams)
	SetArenaParticipantReady(w http.ResponseWriter, r *http.Request, tournamentID api.ArenaTournamentId, waveID api.ArenaWaveId, params api.SetArenaParticipantReadyParams)
}

type ArenaPublicController interface {
	GetArenaPublicTournament(w http.ResponseWriter, r *http.Request, tournamentID api.ArenaTournamentId)
	GetArenaPublicBracket(w http.ResponseWriter, r *http.Request, tournamentID api.ArenaTournamentId)
	GetArenaPublicLiveDraft(w http.ResponseWriter, r *http.Request, tournamentID api.ArenaTournamentId)
	GetArenaPublicScoreboard(w http.ResponseWriter, r *http.Request, tournamentID api.ArenaTournamentId)
	GetArenaPublicSnapshot(w http.ResponseWriter, r *http.Request, tournamentID api.ArenaTournamentId, params api.GetArenaPublicSnapshotParams)
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
