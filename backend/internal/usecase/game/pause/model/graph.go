package model

import (
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
)

const (
	normalPauseGraphAttempts = 2
	maxFrozenPauseDuration   = 7 * 24 * time.Hour
)

var (
	ErrInvalidNormalPauseGraph    = errors.New("invalid normal pause graph")
	ErrNormalPauseGraphConflict   = errors.New("normal pause graph conflict")
	ErrNormalPauseCommandReuse    = errors.New("normal pause command reuse")
	ErrNormalPauseGraphIncomplete = errors.New("normal pause graph incomplete")
	ErrNormalPauseGoldenActive    = errors.New("normal pause rejects active golden activity")
	ErrNormalPauseDeadline        = errors.New("normal pause deadline reached")
	ErrNormalPauseOverflow        = errors.New("normal pause revision overflow")
)

type PauseReason string

const (
	PauseReasonOperator       PauseReason = "operator"
	PauseReasonDisconnect     PauseReason = "disconnect"
	PauseReasonPlatform       PauseReason = "platform"
	PauseReasonExecutionEpoch PauseReason = "execution_epoch"
)

type PauseState string

const (
	PauseStateActive    PauseState = "active"
	PauseStateResumed   PauseState = "resumed"
	PauseStateCancelled PauseState = "cancelled"
)

type PauseDeadlineKind string

const (
	PauseDeadlineReadyWindow PauseDeadlineKind = "ready_window"
	PauseDeadlineGame        PauseDeadlineKind = "game"
	PauseDeadlineDraft       PauseDeadlineKind = "draft"
)

type PauseChildRevision struct {
	ID       uuid.UUID
	Revision int64
}

type PausePresenceRevision struct {
	ID            uuid.UUID
	TournamentID  uuid.UUID
	RosterID      uuid.UUID
	SeriesID      uuid.UUID
	ParticipantID uuid.UUID
	PresenceEpoch int64
	Revision      int64
}

type PauseReconnectCounterRevision struct {
	PauseID       uuid.UUID
	RosterID      uuid.UUID
	ParticipantID uuid.UUID
	Revision      int64
}

type PauseFrozenDeadlineRevision struct {
	Kind     PauseDeadlineKind
	OwnerID  uuid.UUID
	Revision int64
}

type PauseGraphRevisions struct {
	GraphRevision           int64
	TournamentState         domain.TournamentState
	TournamentRevision      int64
	WaveRevision            int64
	Series                  []PauseChildRevision
	Games                   []PauseChildRevision
	SourcePauses            []PauseSourcePauseRevision
	Draft                   *draftusecase.RevisionExpectation
	DraftPreviousRevisionID uuid.UUID
	Presence                []PausePresenceRevision
	Reconnect               []PauseChildRevision
	Counters                []PauseReconnectCounterRevision
	FrozenDeadlines         []PauseFrozenDeadlineRevision
	TerminalActionRevision  int64
}

type PauseSourcePauseRevision struct {
	GameID            uuid.UUID
	PauseID           uuid.UUID
	CurrentRevisionID uuid.UUID
	Revision          int64
	DecisionNumber    int64
	ClockRevision     int64
	Remaining         time.Duration
}

type PauseWave struct {
	Wave     domain.Wave
	Revision int64
}

type PauseSeries struct {
	Execution     seriesdomain.Execution
	Revision      int64
	CurrentGameID *uuid.UUID
}

type PauseGame struct {
	SeriesID    uuid.UUID
	Game        domain.Game
	Revision    int64
	Deadline    *time.Time
	ResumeState *domain.GameState
	SourcePause *PauseGameSourcePause
}

// PauseGameSourcePause is immutable evidence for a Game pause that predates a
// normal Wave pause. Its reconnect rows and clock remain owned by PauseID.
type PauseGameSourcePause struct {
	PauseID           uuid.UUID
	ScopeKind         string
	ScopeID           uuid.UUID
	SeriesID          uuid.UUID
	GameID            uuid.UUID
	Reason            PauseReason
	ParentPauseID     *uuid.UUID
	Depth             int
	State             PauseState
	CurrentRevisionID uuid.UUID
	Revision          int64
	StartedAt         time.Time
	ResolvedAt        *time.Time
	DecisionNumber    int64
	Clock             PauseFrozenDeadline
	Presence          []PausePresenceSnapshot
}

type PausePresenceSnapshot struct {
	ParticipantID uuid.UUID
	State         pausedomain.PresenceState
	PresenceEpoch int64
	Revision      int64
	CapturedAt    time.Time
}

type PauseFrozenDeadline struct {
	Kind             PauseDeadlineKind
	OwnerID          uuid.UUID
	OriginalDeadline time.Time
	FrozenAt         time.Time
	Remaining        time.Duration
	ResumedAt        *time.Time
	ResumedDeadline  *time.Time
	Revision         int64
}

type TournamentRecord struct {
	ID              uuid.UUID
	RosterID        uuid.UUID
	Preset          domain.TournamentPreset
	State           domain.TournamentState
	PausedFromState *domain.TournamentState
	Revision        int64
	RosterSize      int
	CreatedAt       time.Time
	UpdatedAt       time.Time
	StartedAt       *time.Time
	FinishedAt      *time.Time
}

type pauseDeadlineIdentity struct {
	Kind    PauseDeadlineKind
	OwnerID uuid.UUID
}

type PauseGraph struct {
	Scope                  pausedomain.GraphScope
	Revision               int64
	Tournament             TournamentRecord
	Wave                   PauseWave
	Series                 []PauseSeries
	Games                  []PauseGame
	Draft                  *draftusecase.Execution
	Presence               []pausedomain.PausePresence
	Reconnect              []pausedomain.PauseReconnectInterval
	Counters               []pausedomain.PauseReconnectCounter
	FrozenDeadlines        []PauseFrozenDeadline
	ActivePauseID          uuid.UUID
	PausedAt               *time.Time
	DeadlinesSuppressed    bool
	TerminalActionRevision int64
}

type NormalPauseAuthority struct {
	Scope        pausedomain.GraphScope
	Revisions    PauseGraphRevisions
	Graph        PauseGraph
	ActiveGolden bool
	Complete     bool
}

type NormalPauseCommand struct {
	Scope                 pausedomain.GraphScope
	CommandID             uuid.UUID
	PauseID               uuid.UUID
	ActorID               uuid.UUID
	DraftResultRevisionID uuid.UUID
	Reason                PauseReason
	Expected              PauseGraphRevisions
}

type NormalPauseRecord struct {
	Scope                 pausedomain.GraphScope
	ScopeKind             pausedomain.ScopeKind
	ScopeID               uuid.UUID
	CommandID             uuid.UUID
	PauseID               uuid.UUID
	ActorID               uuid.UUID
	DraftResultRevisionID uuid.UUID
	Reason                PauseReason
	State                 PauseState
	Revision              int64
	Expected              PauseGraphRevisions
	Graph                 PauseGraph
	SuspendedReconnect    []PauseChildRevision
	PausedAt              time.Time
	ResolvedAt            *time.Time
}
