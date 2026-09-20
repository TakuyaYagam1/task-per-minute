package usecase

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
)

// TournamentParticipantUseCase is the transport-neutral participant boundary.
// The inbound adapter derives participant authority from the authenticated
// session and never accepts a participant ID from an external request body.
type TournamentParticipantUseCase interface {
	GetLobby(ctx context.Context, query LobbyQuery) (LobbyView, error)
	GetAssignment(ctx context.Context, query AssignmentQuery) (AssignmentResult, error)
	SetReady(ctx context.Context, command ReadyCommand) (ReadinessEvent, error)
	SubmitDraftAction(ctx context.Context, command DraftActionCommand) (DraftExecutionView, error)
	SubmitFlag(ctx context.Context, command SubmissionCommand) (SubmissionResult, error)
	Surrender(ctx context.Context, command SurrenderCommand) (OfficialResultView, error)
	ApplyPostSeriesAction(ctx context.Context, command PostSeriesCommand) (PostSeriesResult, error)
	GetSnapshot(ctx context.Context, query SnapshotQuery) (RecoveryView, error)
}

type Identity struct {
	PlayerID uuid.UUID
}

type LobbyQuery struct {
	Actor        Identity
	TournamentID uuid.UUID
}

type AssignmentQuery struct {
	Actor        Identity
	TournamentID uuid.UUID
	AssignmentID uuid.UUID
}

type SnapshotQuery struct {
	Actor        Identity
	TournamentID uuid.UUID
	Cursor       *RecoveryCursor
}

type ReadyCommand struct {
	Actor                      Identity
	TournamentID               uuid.UUID
	WaveID                     uuid.UUID
	CommandID                  uuid.UUID
	ExpectedProjectionRevision int64
	Ready                      bool
}

type DraftActionCommand struct {
	Actor                      Identity
	TournamentID               uuid.UUID
	SeriesID                   uuid.UUID
	CommandID                  uuid.UUID
	ExpectedProjectionRevision int64
	ExpectedDraftRevision      int64
	ExpectedTurn               int
	Action                     domain.DraftActionType
	Category                   domain.Category
}

type SubmissionCommand struct {
	Actor                      Identity
	TournamentID               uuid.UUID
	SeriesID                   uuid.UUID
	GameID                     uuid.UUID
	CommandID                  uuid.UUID
	ExpectedProjectionRevision int64
	SubmittedFlag              string
}

type SurrenderCommand struct {
	Actor                      Identity
	TournamentID               uuid.UUID
	SeriesID                   uuid.UUID
	CommandID                  uuid.UUID
	ExpectedProjectionRevision int64
	Confirmed                  bool
	Reason                     string
}

type PostSeriesAction string

const (
	PostSeriesActionAcknowledgeResult     PostSeriesAction = "acknowledge_result"
	PostSeriesActionRequestNextAssignment PostSeriesAction = "request_next_assignment"
	PostSeriesActionLeaveLobby            PostSeriesAction = "leave_lobby"
)

func (a PostSeriesAction) IsValid() bool {
	switch a {
	case PostSeriesActionAcknowledgeResult,
		PostSeriesActionRequestNextAssignment,
		PostSeriesActionLeaveLobby:
		return true
	default:
		return false
	}
}

type PostSeriesCommand struct {
	Actor                      Identity
	TournamentID               uuid.UUID
	SeriesID                   uuid.UUID
	CommandID                  uuid.UUID
	ExpectedProjectionRevision int64
	Action                     PostSeriesAction
}

type PostSeriesResult struct {
	TournamentID       uuid.UUID
	ParticipantID      uuid.UUID
	SeriesID           uuid.UUID
	ProjectionRevision int64
	AcceptedAction     PostSeriesAction
}

type SubmissionResult struct {
	ProjectionRevision int64
	Submission         gamedomain.Submission
}

// OfficialResultView is the immutable participant-safe projection returned by
// a surrender. It keeps the HTTP contract independent from the result planner's
// private aggregate representation.
type OfficialResultView struct {
	ID                         domain.OfficialResultRevisionID
	PreviousRevisionID         *domain.OfficialResultRevisionID
	Ordinal                    int
	CommandID                  uuid.UUID
	TournamentID               uuid.UUID
	SeriesID                   uuid.UUID
	Actor                      domain.ResultActor
	WinnerID                   *uuid.UUID
	ScoreRevisionID            domain.SeriesScoreRevisionID
	SourceProjectionRevisionID uuid.UUID
	SeriesState                domain.SeriesState
	SeriesReason               domain.SeriesResultReason
	RecordedAt                 time.Time
}

// RevisionConflictError carries current projection evidence without tying the
// inbound boundary to a protocol-specific 409 response.
type RevisionConflictError struct {
	TournamentID     uuid.UUID
	ExpectedRevision int64
	CurrentRevision  int64
	CurrentState     domain.TournamentState
}

func (e *RevisionConflictError) Error() string {
	return "participant projection revision conflict"
}

func (e *RevisionConflictError) Unwrap() error {
	return domain.ErrConflict
}

type RecoveryCursor struct {
	ProjectionRevision      int64
	ParticipantViewRevision int64
	EventSequence           int64
}

type LobbyView struct {
	TournamentID       uuid.UUID
	ParticipantID      uuid.UUID
	Attendance         domain.AttendanceState
	CurrentSwissRound  *int
	SwissPoints        int
	Status             LobbyStatus
	RequiredAction     LobbyRequiredAction
	ProjectionRevision int64
	State              domain.TournamentState
	RosterLocked       bool
	Series             []LobbySeriesView
}

// LobbyStatus is the server-derived participant lifecycle state. It is
// intentionally smaller than the underlying tournament, Wave, and Series
// state machines so the browser cannot infer authority from timing details.
type LobbyStatus string

const (
	LobbyStatusWaiting    LobbyStatus = "waiting"
	LobbyStatusAssigned   LobbyStatus = "assigned"
	LobbyStatusBye        LobbyStatus = "bye"
	LobbyStatusEliminated LobbyStatus = "eliminated"
	LobbyStatusCompleted  LobbyStatus = "completed"
)

func (s LobbyStatus) IsValid() bool {
	switch s {
	case LobbyStatusWaiting,
		LobbyStatusAssigned,
		LobbyStatusBye,
		LobbyStatusEliminated,
		LobbyStatusCompleted:
		return true
	default:
		return false
	}
}

// LobbyRequiredAction is the single participant-safe next action exposed by
// the lobby. The server derives it from durable assignment, draft, Wave, and
// result state; clients must not recreate that matrix locally.
type LobbyRequiredAction string

const (
	LobbyRequiredActionWait         LobbyRequiredAction = "wait"
	LobbyRequiredActionCheckIn      LobbyRequiredAction = "check_in"
	LobbyRequiredActionReady        LobbyRequiredAction = "ready"
	LobbyRequiredActionDraft        LobbyRequiredAction = "draft"
	LobbyRequiredActionPlay         LobbyRequiredAction = "play"
	LobbyRequiredActionReviewResult LobbyRequiredAction = "review_result"
	LobbyRequiredActionNone         LobbyRequiredAction = "none"
)

func (a LobbyRequiredAction) IsValid() bool {
	switch a {
	case LobbyRequiredActionWait,
		LobbyRequiredActionCheckIn,
		LobbyRequiredActionReady,
		LobbyRequiredActionDraft,
		LobbyRequiredActionPlay,
		LobbyRequiredActionReviewResult,
		LobbyRequiredActionNone:
		return true
	default:
		return false
	}
}

type LobbySeriesView struct {
	SeriesID            uuid.UUID
	WaveID              uuid.UUID
	ParticipantID       uuid.UUID
	OpponentID          uuid.UUID
	OpponentDisplayName string
	Format              domain.SeriesFormat
	State               domain.SeriesState
}

type AssignmentResult struct {
	TournamentID       uuid.UUID
	ParticipantID      uuid.UUID
	ProjectionRevision int64
	Assignment         TournamentParticipantAssignmentView
}

// TournamentParticipantAssignmentView is prefixed to avoid colliding with
// ParticipantAssignmentView from the tournament snapshot boundary.
type TournamentParticipantAssignmentView struct {
	AssignmentID            uuid.UUID
	AttemptID               uuid.UUID
	ParticipantID           uuid.UUID
	SeriesID                uuid.UUID
	GameID                  uuid.UUID
	WaveID                  uuid.UUID
	Context                 ParticipantAssignmentContextView
	ActiveSnapshot          TaskSnapshotView
	Receipt                 domain.TaskDeliveryReceipt
	UndisclosedReserveCount int
}

// ParticipantAssignmentContextView is the persisted, participant-safe game
// context that accompanies an immutable task snapshot. It deliberately keeps
// stage and deadline authority on the server so clients cannot infer them from
// tournament state or local clocks.
type ParticipantAssignmentContextView struct {
	WaveID            uuid.UUID
	SeriesID          uuid.UUID
	SlotID            uuid.UUID
	GameID            uuid.UUID
	Stage             domain.TournamentStage
	SwissRound        *int
	GameNumber        int
	SeriesScore       domain.SeriesScore
	GameState         domain.GameState
	StartedAt         *time.Time
	EffectiveDeadline *time.Time
}

// TaskSnapshotView is the participant-safe task shape. It deliberately has no
// flag field and is built by an allowlist in the outbound read adapter.
type TaskSnapshotView struct {
	SnapshotID    uuid.UUID
	TaskID        uuid.UUID
	Version       int
	Kind          domain.AssignmentTaskKind
	Title         string
	Description   string
	Category      domain.Category
	Difficulty    domain.Difficulty
	TimeLimit     int
	Hints         []string
	TaskURL       *string
	SourceFileURL *string
}

type ReadinessEventType string

const (
	ReadinessEventReady   ReadinessEventType = "ready"
	ReadinessEventCleared ReadinessEventType = "cleared"
)

type ReadinessEvent struct {
	CommandID     uuid.UUID
	WaveID        uuid.UUID
	WindowID      uuid.UUID
	ParticipantID uuid.UUID
	Type          ReadinessEventType
	OccurredAt    time.Time
}

type DraftExecutionState string
type DraftRecoveryPolicy string
type DraftRecoveryReason string
type DraftTransitionOperation string

type DraftRecoveryEvidenceView struct {
	Policy               DraftRecoveryPolicy
	Reason               DraftRecoveryReason
	PreviousState        DraftExecutionState
	PreviousServiceEpoch uuid.UUID
	CurrentServiceEpoch  uuid.UUID
	PreviousDeadline     time.Time
	RecordedAt           time.Time
	ActorID              uuid.UUID
	Note                 string
}

type DraftTransitionEvidenceView struct {
	Operation  DraftTransitionOperation
	ActorID    uuid.UUID
	Reason     string
	OccurredAt time.Time
}

type DraftActionRecordView struct {
	ID                uuid.UUID
	ResultRevisionID  uuid.UUID
	CommandID         uuid.UUID
	Turn              int
	ActorID           uuid.UUID
	Action            domain.DraftActionType
	Category          domain.Category
	ScheduledDeadline time.Time
	OccurredAt        time.Time
	Automatic         bool
	DecisionEvidence  *domain.DecisionEvidence
}

type DraftExecutionView struct {
	ID                  uuid.UUID
	SeriesID            uuid.UUID
	Format              domain.SeriesFormat
	FirstParticipantID  uuid.UUID
	SecondParticipantID uuid.UUID
	Pool                []domain.Category
	State               DraftExecutionState
	RevisionID          uuid.UUID
	PreviousRevisionID  uuid.UUID
	Revision            int64
	CommandID           uuid.UUID
	ServiceEpoch        uuid.UUID
	Turn                int
	CurrentActorID      *uuid.UUID
	CurrentAction       *domain.DraftActionType
	TurnDeadline        time.Time
	AbsoluteDeadline    *time.Time
	PausedRemaining     time.Duration
	Recovery            *DraftRecoveryEvidenceView
	Transition          *DraftTransitionEvidenceView
	LegalCategories     []domain.Category
	Actions             []DraftActionRecordView
	SelectedCategories  []domain.Category
	FirstActorDecision  domain.DecisionEvidence
}

type DraftView struct {
	Execution DraftExecutionView
}

type WaveView struct {
	Wave               domain.Wave
	Revision           int64
	ReadinessRevisions map[uuid.UUID]int64
	SeriesIDs          map[uuid.UUID]uuid.UUID
	ByeParticipantID   *uuid.UUID
}

type RecoveryView struct {
	TournamentID            uuid.UUID
	ParticipantID           uuid.UUID
	ProjectionRevision      int64
	ParticipantViewRevision int64
	EventSequence           int64
	Lobby                   LobbyView
	Assignment              *TournamentParticipantAssignmentView
	Draft                   *DraftView
	Series                  *domain.Series
	Wave                    *WaveView
	ObservedAt              time.Time
}
