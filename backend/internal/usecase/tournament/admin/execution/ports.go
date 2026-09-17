package execution

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	pauseusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause"
	gamestart "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/start"
)

type ExecutionTransactionManager interface {
	Do(ctx context.Context, fn func(context.Context) error) error
}

// ExecutionAuthorityProvider is a service-owned authority boundary. It is
// invoked before the workflow enters its roster transaction, so acquiring or
// renewing a lease never runs while WaveStart holds tournament graph locks.
type ExecutionAuthorityProvider interface {
	AuthorityFor(ctx context.Context, tournamentID uuid.UUID) (authoritydomain.Identity, error)
}

type PairingCommandRecord struct {
	CommandScope

	RosterID                   uuid.UUID
	RoundNumber                int
	Mode                       PairingMode
	CategoryMode               domain.CategoryMode
	Categories                 []domain.Category
	SourceProjectionRevisionID uuid.UUID
	SourceProjectionRevision   int64
	SourceTournamentRevision   int64
	SourceRosterRevision       int64
	SourceHistoryRevision      int64
	RequestDigest              [32]byte
	ResultDocument             json.RawMessage
	ExecutedAt                 time.Time
}

type WaveGraph struct {
	PendingDrafts         bool
	SeriesCount           int
	PlayableMemberCount   int
	CurrentGameCount      int
	ReadySeriesCount      int
	ActiveSeriesCount     int
	ContinuingSeriesCount int
	PausedSeriesCount     int
	TerminalSeriesCount   int
	ReadyGameCount        int
	ActiveGameCount       int
	PausedGameCount       int
	TerminalGameCount     int
	AssignmentCount       int
	DeliveryMemberCount   int
}

type WaveAuthority struct {
	TournamentState      domain.TournamentState
	TournamentRevision   int64
	RosterID             uuid.UUID
	RosterRevision       int64
	ProjectionRevisionID uuid.UUID
	ProjectionRevision   int64
	SourceRevisions      domain.ReadyWindowSourceRevisions
	View                 WaveView
	Graph                WaveGraph
}

type WaveMutation struct {
	Command            WaveCommand
	Authority          WaveAuthority
	ExecutionAuthority authoritydomain.Identity
	Next               domain.Wave
	MutatedAt          time.Time
}

type WaveCommandRecord struct {
	CommandScope

	RosterID                   uuid.UUID
	WaveID                     uuid.UUID
	Action                     WaveAction
	SourceProjectionRevisionID uuid.UUID
	SourceProjectionRevision   int64
	SourceTournamentRevision   int64
	SourceRosterRevision       int64
	SourceWaveRevision         int64
	ResultingWaveRevision      int64
	SourceRevisions            domain.ReadyWindowSourceRevisions
	SourceGraph                WaveGraph
	RequestDigest              [32]byte
	Reason                     string
	ResultDocument             json.RawMessage
	NormalPause                *pauseusecase.NormalPauseRecord
	ExecutedAt                 time.Time
}

// NormalPauseExecutionRepository is the transaction-participating durable
// boundary used by the operator Wave pause and resume actions. The game
// policies own graph planning; this boundary only adds active-root discovery
// and execution-epoch rebinding required by the admin workflow.
type NormalPauseExecutionRepository interface {
	pauseusecase.NormalPauseRepository
	pauseusecase.PauseResumeRepository
	pauseusecase.PauseResumePresenceRepository
	ActiveNormalPauseID(ctx context.Context, scope pausedomain.GraphScope) (uuid.UUID, error)
}

type ExecutionWorkflowRepository interface {
	LockPairingAuthority(ctx context.Context, tournamentID uuid.UUID) (PairingAuthority, error)
	FindPairingCommand(ctx context.Context, tournamentID, commandID uuid.UUID) (*PairingCommandRecord, error)
	ReadExecutionTime(ctx context.Context) (time.Time, error)
	CommitPairing(ctx context.Context, plan PairingPlan) (SwissRoundView, error)
	SavePairingCommand(ctx context.Context, record PairingCommandRecord) error

	LockWaveAuthority(ctx context.Context, tournamentID, waveID uuid.UUID) (WaveAuthority, error)
	FindWaveCommand(ctx context.Context, tournamentID, commandID uuid.UUID) (*WaveCommandRecord, error)
	CommitWave(ctx context.Context, mutation WaveMutation) (WaveView, error)
	SaveWaveCommand(ctx context.Context, record WaveCommandRecord) error
}

type ExecutionWorkflowDependencies struct {
	Transactions ExecutionTransactionManager
	Repository   ExecutionWorkflowRepository
	NormalPause  NormalPauseExecutionRepository
	WaveStart    *gamestart.StartUseCase
	Authority    ExecutionAuthorityProvider
}
