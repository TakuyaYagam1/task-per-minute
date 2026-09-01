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

// ArenaAdminService is the transport-facing application port for Arena
// tournament administration and its public projections. Each method accepts
// one complete command so HTTP handlers do not compose application workflows.
type ArenaAdminService interface {
	ListTournaments(ctx context.Context, command ArenaListTournamentsCommand) (api.ArenaOperatorTournamentList, error)
	CreateTournament(ctx context.Context, command ArenaCreateTournamentCommand) (api.ArenaTournament, error)
	ApplyTournamentAction(ctx context.Context, command ArenaTournamentActionCommand) (api.ArenaTournament, error)
	GetRoster(ctx context.Context, command ArenaGetRosterCommand) (api.ArenaRoster, error)
	ReplaceRoster(ctx context.Context, command ArenaReplaceRosterCommand) (api.ArenaRoster, error)
	RunRosterPreflight(ctx context.Context, command ArenaRosterPreflightCommand) (api.ArenaPreflightReport, error)
	LockRoster(ctx context.Context, command ArenaLockRosterCommand) (api.ArenaRoster, error)
	UnlockRoster(ctx context.Context, command ArenaUnlockRosterCommand) (api.ArenaRoster, error)
	ConfigurePairings(ctx context.Context, command ArenaConfigurePairingsCommand) (api.ArenaSwissRound, error)
	GetStandings(ctx context.Context, command ArenaTournamentReadCommand) (api.ArenaPublicScoreboardResponse, error)
	GetBracket(ctx context.Context, command ArenaTournamentReadCommand) (api.ArenaPublicBracketResponse, error)
}

type ArenaOperatorIdentity struct {
	Subject   string
	SessionID string
}

type ArenaListTournamentsCommand struct {
	Operator ArenaOperatorIdentity
	State    domain.ArenaTournamentState
	Cursor   string
	PageSize int32
}

type ArenaCreateTournamentCommand struct {
	Operator         ArenaOperatorIdentity
	CommandID        uuid.UUID
	ExpectedRevision int64
	Preset           domain.ArenaPreset
	RosterSize       int
}

type ArenaTournamentAction string

const (
	ArenaTournamentActionOpenRegistration ArenaTournamentAction = "open_registration"
	ArenaTournamentActionStartSwiss       ArenaTournamentAction = "start_swiss"
	ArenaTournamentActionStartGolden      ArenaTournamentAction = "start_golden"
	ArenaTournamentActionStartPlayoffs    ArenaTournamentAction = "start_playoffs"
	ArenaTournamentActionPause            ArenaTournamentAction = "pause"
	ArenaTournamentActionResume           ArenaTournamentAction = "resume"
	ArenaTournamentActionComplete         ArenaTournamentAction = "complete"
	ArenaTournamentActionCancel           ArenaTournamentAction = "cancel"
)

type ArenaTournamentActionCommand struct {
	Operator         ArenaOperatorIdentity
	TournamentID     uuid.UUID
	CommandID        uuid.UUID
	ExpectedRevision int64
	Action           ArenaTournamentAction
	Confirmed        bool
	Reason           string
}

type ArenaGetRosterCommand struct {
	Operator     ArenaOperatorIdentity
	TournamentID uuid.UUID
}

type ArenaAttendance = domain.ArenaAttendanceState

const (
	ArenaAttendanceInvited    = domain.ArenaAttendanceStateInvited
	ArenaAttendanceRegistered = domain.ArenaAttendanceStateRegistered
	ArenaAttendanceCheckedIn  = domain.ArenaAttendanceStateCheckedIn
	ArenaAttendanceWithdrawn  = domain.ArenaAttendanceStateWithdrawn
)

type ArenaRosterParticipantCommand struct {
	PlayerID   uuid.UUID
	Seed       int32
	Attendance ArenaAttendance
}

type ArenaReplaceRosterCommand struct {
	Operator         ArenaOperatorIdentity
	TournamentID     uuid.UUID
	CommandID        uuid.UUID
	ExpectedRevision int64
	Participants     []ArenaRosterParticipantCommand
}

type ArenaRosterPreflightCommand struct {
	Operator         ArenaOperatorIdentity
	TournamentID     uuid.UUID
	CommandID        uuid.UUID
	ExpectedRevision int64
}

type ArenaLockRosterCommand struct {
	Operator            ArenaOperatorIdentity
	TournamentID        uuid.UUID
	CommandID           uuid.UUID
	ExpectedRevision    int64
	PreflightRevisionID uuid.UUID
	CheckedInPlayerIDs  []uuid.UUID
}

type ArenaUnlockRosterCommand struct {
	Operator         ArenaOperatorIdentity
	TournamentID     uuid.UUID
	CommandID        uuid.UUID
	ExpectedRevision int64
	Confirmed        bool
	Reason           string
}

type ArenaPairingMode string

const (
	ArenaPairingModeAutomatic ArenaPairingMode = "automatic"
	ArenaPairingModeManual    ArenaPairingMode = "manual"
)

type ArenaManualPairCommand struct {
	FirstParticipantID  uuid.UUID
	SecondParticipantID uuid.UUID
}

type ArenaPairingRepeatOverrideCommand struct {
	Confirmed bool
	Reason    string
}

type ArenaConfigurePairingsCommand struct {
	Operator               ArenaOperatorIdentity
	TournamentID           uuid.UUID
	CommandID              uuid.UUID
	ExpectedRevision       int64
	RoundNumber            int32
	PairingMode            ArenaPairingMode
	CategoryMode           domain.ArenaCategoryMode
	Categories             []domain.Category
	ManualPairings         []ArenaManualPairCommand
	ManualByeParticipantID *uuid.UUID
	RepeatOverride         *ArenaPairingRepeatOverrideCommand
}

type ArenaTournamentReadCommand struct {
	TournamentID uuid.UUID
}

type ArenaRevisionConflictError struct {
	ExpectedRevision int64
	CurrentRevision  int64
	CurrentState     string
}

func (e *ArenaRevisionConflictError) Error() string { return "arena revision conflict" }

func (e *ArenaRevisionConflictError) Unwrap() error { return domain.ErrConflict }

type ArenaPairingReason string

const (
	ArenaPairingReasonMissingParticipant   ArenaPairingReason = "missing_participant"
	ArenaPairingReasonSelfPair             ArenaPairingReason = "self_pair"
	ArenaPairingReasonForeignParticipant   ArenaPairingReason = "foreign_participant"
	ArenaPairingReasonDuplicateParticipant ArenaPairingReason = "duplicate_participant"
	ArenaPairingReasonIncomplete           ArenaPairingReason = "incomplete_pairing"
	ArenaPairingReasonInvalidBye           ArenaPairingReason = "invalid_bye"
	ArenaPairingReasonRepeatNeedsOverride  ArenaPairingReason = "repeat_requires_override"
	ArenaPairingReasonOverrideMismatch     ArenaPairingReason = "override_mismatch"
)

type ArenaPairingValidationError struct {
	Reason ArenaPairingReason
}

func (e *ArenaPairingValidationError) Error() string {
	if e == nil {
		return "invalid_pairing"
	}
	return string(e.Reason)
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
