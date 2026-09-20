package usecase

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var ErrPublicSnapshotCursorConflict = errors.New("public tournament snapshot cursor conflict")

// TournamentSnapshotUseCase provides role-scoped tournament read models.
// Implementations assemble each response from one consistent durable snapshot.
type TournamentSnapshotUseCase interface {
	ParticipantSnapshot(ctx context.Context, query ParticipantSnapshotQuery) (ParticipantSnapshotView, error)
	PublicSnapshot(ctx context.Context, query PublicSnapshotQuery) (PublicSnapshotView, error)
	OperatorSnapshot(ctx context.Context, query OperatorSnapshotQuery) (OperatorSnapshotView, error)
}

type SnapshotCursor struct {
	ProjectionRevision int64
	EventSequence      int64
	ObservedAt         time.Time
}

// PublicSnapshotCursorConflictError reports a client watermark that is ahead
// of the durable public snapshot while retaining transport-neutral cursor
// details for an inbound adapter.
type PublicSnapshotCursorConflictError struct {
	TournamentID                uuid.UUID
	RequestedProjectionRevision int64
	RequestedEventSequence      int64
	CurrentProjectionRevision   int64
	CurrentEventSequence        int64
}

func (e *PublicSnapshotCursorConflictError) Error() string {
	return ErrPublicSnapshotCursorConflict.Error()
}

func (e *PublicSnapshotCursorConflictError) Unwrap() error {
	return domain.ErrConflict
}

func (e *PublicSnapshotCursorConflictError) Is(target error) bool {
	return target == ErrPublicSnapshotCursorConflict || target == domain.ErrConflict
}

type ParticipantSnapshotQuery struct {
	TournamentID uuid.UUID
	PlayerID     uuid.UUID
}

type PublicSnapshotQuery struct {
	TournamentID uuid.UUID
	Cursor       *SnapshotCursor
}

type OperatorSnapshotQuery struct {
	TournamentID uuid.UUID
	OperatorID   uuid.UUID
}

type ParticipantSnapshotView struct {
	TournamentID  uuid.UUID
	PlayerID      uuid.UUID
	ParticipantID uuid.UUID
	Cursor        SnapshotCursor
	Assignment    *ParticipantAssignmentView
	Opponent      *ParticipantOpponentView
	Game          *ParticipantGameView
}

type ParticipantAssignmentView struct {
	AssignmentID uuid.UUID
	AttemptID    uuid.UUID
	SeriesID     uuid.UUID
	GameID       uuid.UUID
	WaveID       uuid.UUID
	Task         ParticipantTaskView
}

type ParticipantTaskView struct {
	SnapshotID       uuid.UUID
	TaskID           uuid.UUID
	Title            string
	Category         string
	Difficulty       string
	TimeLimitSeconds int
}

type ParticipantOpponentView struct {
	PlayerID    uuid.UUID
	DisplayName string
	SeriesID    uuid.UUID
	Ready       bool
	SeriesState string
	Score       int
}

type ParticipantGameView struct {
	GameID           uuid.UUID
	State            string
	Revision         int64
	ResultReason     string
	WinnerID         *uuid.UUID
	ResultRevisionID *uuid.UUID
	Pause            *ParticipantGamePauseView
	Presence         []ParticipantPresenceView
	Reconnect        []ParticipantReconnectView
}

type ParticipantGamePauseView struct {
	PauseID           uuid.UUID
	State             string
	Reason            string
	FrozenAt          time.Time
	FrozenRemainingMS int64
	ResumedAt         *time.Time
	ResumedDeadline   *time.Time
	ReconnectDeadline *time.Time
}

type ParticipantPresenceView struct {
	ParticipantID  uuid.UUID
	State          string
	PresenceEpoch  int64
	Revision       int64
	ConnectedAt    time.Time
	DisconnectedAt *time.Time
	UpdatedAt      time.Time
}

type ParticipantReconnectView struct {
	ID                 uuid.UUID
	PauseID            uuid.UUID
	ParticipantID      uuid.UUID
	PresenceEpoch      int64
	Number             int
	ContinuationNumber int
	ContinuedFromID    *uuid.UUID
	SuspendedByPauseID *uuid.UUID
	State              string
	OpenedAt           time.Time
	Deadline           time.Time
	ClosedAt           *time.Time
	Revision           int64
	UpdatedAt          time.Time
}

type PublicSnapshotView struct {
	Cursor          SnapshotCursor
	Tournament      PublicTournamentView
	Scoreboard      []PublicScoreboardEntryView
	Bracket         []PublicBracketMatchView
	LiveSeries      []PublicSeriesView
	OfficialResults []PublicOfficialResultView
	Draft           *PublicDraftView
}

type PublicTournamentView struct {
	TournamentID uuid.UUID
	Preset       string
	State        string
	RosterSize   int
	StartedAt    *time.Time
	FinishedAt   *time.Time
}

type PublicScoreboardEntryView struct {
	Rank            int
	DisplayName     string
	Points          int
	Buchholz        int
	EffectiveTimeMS int64
}

type PublicBracketMatchView struct {
	Stage             string
	Position          int
	FirstDisplayName  string
	SecondDisplayName string
	FirstWins         int
	SecondWins        int
	State             string
}

type PublicSeriesView struct {
	SeriesID            uuid.UUID
	Format              string
	State               string
	FirstDisplayName    string
	SecondDisplayName   string
	FirstWins           int
	SecondWins          int
	CurrentGamePosition int
}

type PublicOfficialResultView struct {
	RevisionID        uuid.UUID
	SeriesID          uuid.UUID
	State             string
	WinnerDisplayName string
	FirstWins         int
	SecondWins        int
	RecordedAt        time.Time
}

type PublicDraftView struct {
	SeriesID           uuid.UUID
	Format             string
	State              string
	Pool               []string
	SelectedCategories []string
	Actions            []PublicDraftActionView
}

type PublicDraftActionView struct {
	Turn             int
	Action           string
	Category         string
	ActorDisplayName string
	OccurredAt       time.Time
}

type OperatorSnapshotView struct {
	TournamentID uuid.UUID
	Cursor       SnapshotCursor
	Waves        []OperatorWaveView
	Presence     []OperatorPresenceView
	Replays      []OperatorReplayView
	Pause        *OperatorPauseView
	AuditLinks   []OperatorAuditLinkView
}

type OperatorWaveView struct {
	WaveID         uuid.UUID
	State          string
	WindowDeadline *time.Time
	Members        []OperatorWaveMemberView
}

type OperatorWaveMemberView struct {
	ParticipantID     uuid.UUID
	SeriesID          *uuid.UUID
	Ready             bool
	ReadinessRevision int64
}

type OperatorPresenceView struct {
	ParticipantID uuid.UUID
	SeriesID      uuid.UUID
	State         string
	PresenceEpoch int64
	UpdatedAt     time.Time
}

type OperatorReplayView struct {
	SeriesID          uuid.UUID
	SlotID            uuid.UUID
	FailedGameID      uuid.UUID
	ReplacementGameID uuid.UUID
	ReplacementWaveID uuid.UUID
	State             string
	Revision          int64
}

type OperatorPauseView struct {
	PauseID           uuid.UUID
	State             string
	Reason            string
	PausedAt          time.Time
	GraphRevision     int64
	GameID            *uuid.UUID
	FrozenRemainingMS *int64
	ReconnectDeadline *time.Time
}

type OperatorAuditLinkView struct {
	AuditEventID             uuid.UUID
	EntityKind               string
	EntityID                 uuid.UUID
	OfficialResultRevisionID uuid.UUID
}
