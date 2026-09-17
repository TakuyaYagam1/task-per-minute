package admin

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	correctionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/correction"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	pauseusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause"
	gamestart "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/start"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

// CorrectionWorkflowAuthority is the exact database snapshot used to plan one
// operator correction. Core contains the immutable result DAG and its current
// heads; the remaining fields bind that snapshot to the published projection.
type CorrectionWorkflowAuthority struct {
	RosterID             uuid.UUID
	ProjectionRevisionID uuid.UUID
	ProjectionRevision   int64
	Core                 correctionusecase.Authority
	Stage                correctionusecase.StageSnapshot
}

// CorrectionCommandRecord is the durable idempotency record for an already
// committed correction. Evidence is returned verbatim on an exact replay.
type CorrectionCommandRecord struct {
	CommandID                  uuid.UUID
	TournamentID               uuid.UUID
	RosterID                   uuid.UUID
	SeriesID                   uuid.UUID
	GameID                     uuid.UUID
	OperatorID                 uuid.UUID
	ExpectedProjectionRevision int64
	RequestDigest              [sha256.Size]byte
	Evidence                   CorrectionEvidence
	ExecutedAt                 time.Time
}

// CorrectionMutation carries the validated core plan and the use case
// evidence which must be persisted in the same transaction as every CAS write.
type CorrectionMutation struct {
	Command       CorrectionCommand
	Authority     CorrectionWorkflowAuthority
	RequestDigest [sha256.Size]byte
	Plan          correctionusecase.Plan
	Stage         correctionusecase.StageResult
	Evidence      CorrectionEvidence
}

type CorrectionTransactionManager interface {
	Do(ctx context.Context, fn func(context.Context) error) error
}

type CorrectionWorkflowRepository interface {
	LockCorrectionAuthority(
		ctx context.Context,
		tournamentID uuid.UUID,
		seriesID uuid.UUID,
		gameID uuid.UUID,
	) (CorrectionWorkflowAuthority, error)
	FindCorrectionCommand(
		ctx context.Context,
		commandID uuid.UUID,
	) (*CorrectionCommandRecord, error)
	ReadCorrectionTime(ctx context.Context) (time.Time, error)
	CommitCorrection(
		ctx context.Context,
		mutation CorrectionMutation,
	) (CorrectionEvidence, bool, error)
}

type CorrectionWorkflowDependencies struct {
	Transactions CorrectionTransactionManager
	Repository   CorrectionWorkflowRepository
}

var ErrManualByeMismatch = errors.New("manual Swiss bye does not match the deterministic selection")

type ExecutionTransactionManager interface {
	Do(ctx context.Context, fn func(context.Context) error) error
}

// ExecutionAuthorityProvider is a service-owned authority boundary. It is
// invoked before the workflow enters its roster transaction, so acquiring or
// renewing a lease never runs while WaveStart holds tournament graph locks.
type ExecutionAuthorityProvider interface {
	AuthorityFor(ctx context.Context, tournamentID uuid.UUID) (authoritydomain.Identity, error)
}

type PairingParticipant struct {
	ID         uuid.UUID
	StableSeed int
}

type PairingAuthority struct {
	TournamentID         uuid.UUID
	TournamentState      domain.TournamentState
	TournamentRevision   int64
	RosterID             uuid.UUID
	RosterRevision       int64
	RosterLockedAt       time.Time
	ProjectionRevisionID uuid.UUID
	ProjectionRevision   int64
	HistoryRevision      int64
	Participants         []PairingParticipant
	Standings            []SwissStandingView
	PreviousMeetings     []swissusecase.Pair
	PriorMeetingCounts   map[swissusecase.PairKey]int
	ReceivedBye          map[uuid.UUID]bool
	RoundCount           int
	CompletedRoundCount  int
}

type PairingPlan struct {
	Command                 PairingCommand
	Authority               PairingAuthority
	RoundID                 uuid.UUID
	PairingEvidenceID       uuid.UUID
	WaveID                  uuid.UUID
	WaveRevisionID          domain.WaveRevisionID
	PairingIDs              []uuid.UUID
	SeriesIDs               []uuid.UUID
	InitialScoreRevisionIDs []domain.SeriesScoreRevisionID
	Pairs                   []swissusecase.Pair
	Automatic               *swissusecase.AutomaticPairing
	Bye                     *swissusecase.ByeSelection
	DecidedAt               time.Time
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

type ManualByeMismatchError struct {
	RequestedParticipantID uuid.UUID
	SelectedParticipantID  uuid.UUID
	ExpectedRevision       int64
	CurrentRevision        int64
	CurrentState           domain.TournamentState
}

func (e *ManualByeMismatchError) Error() string {
	return ErrManualByeMismatch.Error()
}

func (e *ManualByeMismatchError) Unwrap() []error {
	return []error{
		ErrManualByeMismatch,
		&RevisionConflictError{
			ExpectedRevision: e.ExpectedRevision,
			CurrentRevision:  e.CurrentRevision,
			CurrentState:     e.CurrentState,
		},
	}
}

type OperatorReserveAuthority struct {
	TournamentState domain.TournamentState
	Replay          gameusecase.OperatorReserveAuthority
}

type ReplayReplacementAuthority struct {
	TournamentState domain.TournamentState
	Replay          gameusecase.ReplayReplacementAuthority
}

// ReplayWorkflowRepository keeps each admin command on the transaction opened
// by ReplayWorkflow. The command argument carries candidate and idempotency
// evidence that the narrower replay use cases intentionally do not own.
type ReplayWorkflowRepository interface {
	ReadReplayTime(ctx context.Context, commandID uuid.UUID) (time.Time, error)
	LoadOperatorReserveAuthority(
		ctx context.Context,
		command ReserveCommand,
	) (OperatorReserveAuthority, error)
	CommitOperatorReserve(
		ctx context.Context,
		command ReserveCommand,
		requestDigest [32]byte,
		record gameusecase.OperatorReserve,
	) (*gameusecase.OperatorReserve, bool, error)
	LoadReplayReplacementAuthority(
		ctx context.Context,
		command ReplayCommand,
	) (ReplayReplacementAuthority, error)
	CommitReplayReplacement(
		ctx context.Context,
		command ReplayCommand,
		requestDigest [32]byte,
		record gameusecase.ReplayReplacement,
	) (*gameusecase.ReplayReplacement, bool, error)
}

type ReplayWorkflowDependencies struct {
	Transactions ExecutionTransactionManager
	Repository   ReplayWorkflowRepository
}
