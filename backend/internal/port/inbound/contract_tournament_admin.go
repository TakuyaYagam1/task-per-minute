package usecase

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var ErrAdminRevisionConflict = errors.New("tournament projection revision conflict")

type AdminRevisionConflictError struct {
	ExpectedRevision int64
	CurrentRevision  int64
	CurrentState     domain.TournamentState
}

func (e *AdminRevisionConflictError) Error() string { return ErrAdminRevisionConflict.Error() }
func (e *AdminRevisionConflictError) Unwrap() error { return domain.ErrConflict }

// TournamentAdminUseCase is the transport-neutral operator boundary for a
// single tournament. The inbound adapter derives operator identity from the
// authenticated session and never accepts it from an external request.
type TournamentAdminUseCase interface {
	GetRoster(context.Context, AdminRosterQuery) (AdminRosterView, error)
	ReplaceRoster(context.Context, AdminReplaceRosterCommand) (AdminRosterView, error)
	RunPreflight(context.Context, AdminPreflightCommand) (AdminPreflightReport, error)
	LockRoster(context.Context, AdminLockRosterCommand) (AdminRosterView, error)
	UnlockRoster(context.Context, AdminUnlockRosterCommand) (AdminRosterView, error)
	ConfigurePairings(context.Context, AdminPairingCommand) (AdminSwissRoundView, error)
	ApplyTournamentAction(context.Context, AdminTournamentActionCommand) (TournamentView, error)
	ControlWave(context.Context, AdminWaveCommand) (AdminWaveView, error)
	ResolveNoShow(context.Context, AdminNoShowCommand) error
	AssignReserve(context.Context, AdminReserveCommand) error
	RecordForfeit(context.Context, AdminForfeitCommand) error
	ReplayGame(context.Context, AdminReplayCommand) error
	CorrectGameResult(context.Context, AdminCorrectionCommand) (AdminCorrectionEvidence, error)
	ListAudit(context.Context, AdminAuditQuery) (AdminAuditPage, error)
	ExportIncident(context.Context, AdminIncidentQuery) (AdminIncidentBundle, error)
	GetOperatorSnapshot(context.Context, AdminSnapshotQuery) (AdminOperatorSnapshotView, error)
}

type AdminOperatorIdentity struct{ ActorID uuid.UUID }

type AdminCommandScope struct {
	Operator     AdminOperatorIdentity
	TournamentID uuid.UUID
	CommandID    uuid.UUID
}

type AdminRosterQuery struct {
	Operator     AdminOperatorIdentity
	TournamentID uuid.UUID
}

type AdminRosterParticipantInput struct {
	PlayerID   uuid.UUID
	Seed       int
	Attendance domain.AttendanceState
}

type AdminReplaceRosterCommand struct {
	AdminCommandScope
	ExpectedProjectionRevision int64
	Participants               []AdminRosterParticipantInput
}

type AdminPreflightCommand struct {
	AdminCommandScope
	ExpectedProjectionRevision int64
}

type AdminLockRosterCommand struct {
	AdminCommandScope
	ExpectedProjectionRevision int64
	PreflightRevisionID        uuid.UUID
	CheckedInPlayerIDs         []uuid.UUID
}

type AdminUnlockRosterCommand struct {
	AdminCommandScope
	ExpectedProjectionRevision int64
	Confirmed                  bool
	Reason                     string
}

type AdminRosterParticipantView struct {
	ID           uuid.UUID
	RosterID     uuid.UUID
	TournamentID uuid.UUID
	PlayerID     uuid.UUID
	Seed         int
	Attendance   domain.AttendanceState
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type AdminRosterView struct {
	ID                 uuid.UUID
	TournamentID       uuid.UUID
	Revision           int64
	Participants       []AdminRosterParticipantView
	Locked             bool
	ExecutionStarted   bool
	LockedAt           *time.Time
	ExecutionStartedAt *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type AdminPreflightCheck struct {
	Code        string
	Passed      bool
	Explanation string
	Evidence    []string
}

type AdminPreflightSourceRevision struct{ Source, Value string }

type AdminPreflightReport struct {
	ID               uuid.UUID
	TournamentID     uuid.UUID
	AlgorithmVersion string
	EvaluatedAt      time.Time
	NormalizedInputs []string
	Revisions        []AdminPreflightSourceRevision
	ProofHash        string
	Checks           []AdminPreflightCheck
}

type AdminPairingMode string

const (
	AdminPairingModeAutomatic AdminPairingMode = "automatic"
	AdminPairingModeManual    AdminPairingMode = "manual"
)

type AdminParticipantPair struct{ FirstParticipantID, SecondParticipantID uuid.UUID }
type AdminRepeatOverride struct {
	Confirmed bool
	Reason    string
}

type AdminPairingCommand struct {
	AdminCommandScope
	ExpectedProjectionRevision int64
	RoundNumber                int
	PairingMode                AdminPairingMode
	CategoryMode               domain.CategoryMode
	Categories                 []domain.Category
	ManualPairings             []AdminParticipantPair
	ManualPairingsProvided     bool
	ManualByeParticipantID     *uuid.UUID
	RepeatOverride             *AdminRepeatOverride
}

type AdminWaveAction string

const (
	AdminWaveActionOpenReadyWindow AdminWaveAction = "open_ready_window"
	AdminWaveActionStart           AdminWaveAction = "start"
	AdminWaveActionPause           AdminWaveAction = "pause"
	AdminWaveActionResume          AdminWaveAction = "resume"
	AdminWaveActionComplete        AdminWaveAction = "complete"
	AdminWaveActionCancel          AdminWaveAction = "cancel"
)

type AdminWaveCommand struct {
	AdminCommandScope
	WaveID                     uuid.UUID
	ExpectedProjectionRevision int64
	Action                     AdminWaveAction
	Confirmed                  bool
	Reason                     string
}

type AdminSwissPairingEvidenceView struct {
	ID               uuid.UUID
	Purpose          string
	AlgorithmVersion string
	NormalizedInputs []string
	Result           []string
	ReplayDigest     string
	OwnerID          uuid.UUID
	DecidedAt        time.Time
}

type AdminSwissPairingView struct {
	ID, RoundID, FirstParticipantID, SecondParticipantID, EvidenceID uuid.UUID
	Repeated                                                         bool
	OverrideActorID                                                  *uuid.UUID
	OverrideReason                                                   *string
}

type AdminSwissByeView struct {
	ID, RoundID, ParticipantID, RevisionID, EvidenceID uuid.UUID
	PointsAwarded                                      int
}

type AdminSwissStandingView struct {
	ParticipantID                                            uuid.UUID
	Position, Points, Buchholz, HeadToHeadPoints, StableSeed int
	PointsLabel, BuchholzStatus                              string
	HeadToHeadApplied                                        bool
	EffectiveTimeMS                                          int64
	AcceptedSolveTimeMS                                      *int64
}

type AdminSwissRoundView struct {
	ID, TournamentID                 uuid.UUID
	RoundNumber                      int
	Revision                         int64
	RosterParticipantIDs             []uuid.UUID
	PairingEvidence                  *AdminSwissPairingEvidenceView
	Pairings                         []AdminSwissPairingView
	Bye                              *AdminSwissByeView
	Standings                        []AdminSwissStandingView
	Locked                           bool
	LockedAt, StartedAt, CompletedAt *time.Time
	CreatedAt, UpdatedAt             time.Time
}

type AdminWaveView struct {
	Wave               domain.Wave
	Revision           int64
	ReadinessRevisions map[uuid.UUID]int64
	SeriesIDs          map[uuid.UUID]uuid.UUID
	ByeParticipantID   *uuid.UUID
}

type AdminTournamentAction string

const (
	AdminTournamentActionOpenRegistration AdminTournamentAction = "open_registration"
	AdminTournamentActionStartSwiss       AdminTournamentAction = "start_swiss"
	AdminTournamentActionStartGolden      AdminTournamentAction = "start_golden"
	AdminTournamentActionStartPlayoffs    AdminTournamentAction = "start_playoffs"
	AdminTournamentActionPause            AdminTournamentAction = "pause"
	AdminTournamentActionResume           AdminTournamentAction = "resume"
	AdminTournamentActionComplete         AdminTournamentAction = "complete"
	AdminTournamentActionCancel           AdminTournamentAction = "cancel"
)

type AdminTournamentActionCommand struct {
	AdminCommandScope
	ExpectedProjectionRevision int64
	Action                     AdminTournamentAction
	Confirmed                  bool
	Reason                     string
}

type AdminNoShowCommand struct {
	AdminCommandScope
	WaveID, WindowID, SeriesID                       uuid.UUID
	Confirmed                                        bool
	Reason                                           string
	ExpectedAuthorityRevision                        int64
	ExpectedWaveRevisionID, ExpectedWindowRevisionID uuid.UUID
	ExpectedSeriesState                              domain.SeriesState
	GameResultRevisionIDs                            []uuid.UUID
	ScoreRevisionID, SeriesResultRevisionID          uuid.UUID
}

type AdminReserveCommand struct {
	AdminCommandScope
	OldWaveID, SeriesID, SlotID, AssignmentID, AssignmentAttemptID uuid.UUID
	Confirmed                                                      bool
	Reason                                                         string
	ExpectedAuthorityRevision                                      int64
	ExpectedExhaustionCommandID                                    uuid.UUID
	ExpectedAssignmentRevision                                     int64
	ProposedTaskID                                                 uuid.UUID
	ProposedVersion                                                int
	ProposedSnapshotID, ExpectedSnapshotID, EvidenceID             uuid.UUID
	ExpectedPoolRevisionID                                         uuid.UUID
	ExpectedPoolRevision                                           int64
	ExpectedHistoryRevisionID                                      uuid.UUID
	ExpectedHistoryRevision                                        int64
	ExpectedArtifactRevisionID                                     uuid.UUID
	ExpectedArtifactRevision                                       int64
	ExpectedReservationRevisionID                                  uuid.UUID
	ExpectedReservationRevision                                    int64
	ExpectedCategoryRevisionID                                     uuid.UUID
	ExpectedCategoryRevision                                       int64
}

type AdminGameExpectation struct {
	SlotID, GameID uuid.UUID
	AttemptNo      int
	State          domain.GameState
}

type AdminForfeitCommand struct {
	AdminCommandScope
	SeriesID, ForfeitingParticipantID                                                          uuid.UUID
	Confirmed                                                                                  bool
	Reason                                                                                     string
	ExpectedAuthorityRevision                                                                  int64
	ExpectedGame                                                                               *AdminGameExpectation
	Basis, RuleID                                                                              string
	EvidenceIDs                                                                                []uuid.UUID
	GameResultRevisionID                                                                       *uuid.UUID
	ScoreRevisionID, SeriesResultRevisionID, AuditEventID, OutboxEventID, ProjectionRevisionID uuid.UUID
}

type AdminReplayCommand struct {
	AdminCommandScope
	OldWaveID, SeriesID, SlotID, AssignmentID, FailedGameID                                                                                               uuid.UUID
	Confirmed                                                                                                                                             bool
	Reason                                                                                                                                                string
	ExpectedAuthorityRevision                                                                                                                             int64
	ExpectedClosureRevisionID, AssignmentAttemptID, ReplacementGameID, ReplacementWaveID, ReplacementWaveRevisionID, ReadyWindowID, ReadyWindowRevisionID uuid.UUID
}

type AdminProjectionRevisionExpectation struct {
	ID, TournamentID   uuid.UUID
	ArtifactKind       string
	ArtifactID         uuid.UUID
	RevisionNo         int
	PreviousRevisionID *uuid.UUID
	PayloadDigest      [sha256.Size]byte
	CreatedAt          time.Time
}

type AdminCorrectionProjectionIntent struct {
	ExpectedRevision           AdminProjectionRevisionExpectation
	NextRevisionID, DecisionID uuid.UUID
	PayloadDigest              [sha256.Size]byte
}

type AdminCorrectionUnlockIntent struct {
	ReservationID, TournamentID, OwnerID, SourceRevisionID uuid.UUID
	ExpectedRevision                                       int64
	ExpectedUsed, ExpectedDisclosed                        bool
	EvidenceDigest, BindingDigest                          [sha256.Size]byte
}

type AdminCorrectionPatch struct {
	State          domain.GameState
	Reason         domain.GameResultReason
	WinnerID       *uuid.UUID
	SolvedAt       *time.Time
	SubmissionID   *uuid.UUID
	EvidenceDigest [sha256.Size]byte
}

type AdminCorrectionCommand struct {
	AdminCommandScope
	SeriesID, GameID           uuid.UUID
	ExpectedProjectionRevision int64
	Confirmed                  bool
	Reason, Explanation        string
	Fields                     []string
	Patch                      AdminCorrectionPatch
	ProjectionIntents          []AdminCorrectionProjectionIntent
	UnlockIntents              []AdminCorrectionUnlockIntent
}

type AdminProjectionSupersessionView struct {
	ArtifactKind                                        string
	ArtifactID, PreviousRevisionID, SuccessorRevisionID uuid.UUID
	PreviousDecisionID                                  *uuid.UUID
	ReplacementDecisionID                               uuid.UUID
}

type AdminCorrectionEvidence struct {
	CommandID, TournamentID, SeriesID, GameID, OperatorID uuid.UUID
	Reason                                                string
	Fields                                                []string
	RequestedAt                                           time.Time
	ValidationDigest                                      [sha256.Size]byte
	Supersessions                                         []AdminProjectionSupersessionView
	UnlockIntents                                         []AdminCorrectionUnlockIntent
}

type AdminAuditCursor struct {
	OccurredAt               time.Time
	AuditEventID, RevisionID uuid.UUID
	SnapshotBound            string
}

type AdminAuditFilter struct {
	TournamentID             uuid.UUID
	EntityKind               string
	EntityID                 *uuid.UUID
	EventType                string
	ActorKind                domain.ResultActorKind
	ActorID                  *uuid.UUID
	ResultReason             string
	OccurredFrom, OccurredTo *time.Time
	Cursor                   *AdminAuditCursor
	PageSize                 int
}

type AdminAuditQuery struct {
	Operator AdminOperatorIdentity
	Filter   AdminAuditFilter
}

type AdminAuditEvent struct {
	AuditEventID, TournamentID, RosterID, SeriesID, ResultEventID uuid.UUID
	ActorKind                                                     domain.ResultActorKind
	ActorID                                                       *uuid.UUID
	EventType                                                     string
	RedactedPayload                                               []byte
	OccurredAt, CreatedAt                                         time.Time
	ResultState, ResultReason                                     string
	WinnerID                                                      *uuid.UUID
	OfficialResultRevisionID                                      uuid.UUID
	EntityKind                                                    string
	EntityID                                                      uuid.UUID
	RevisionNumber                                                int64
	IsCurrent, IsSuperseded                                       bool
}

type AdminAuditPage struct {
	Events     []AdminAuditEvent
	NextCursor *AdminAuditCursor
}

type AdminIncidentQuery struct {
	Operator     AdminOperatorIdentity
	TournamentID uuid.UUID
}

type AdminIncidentBundle struct {
	TournamentID       uuid.UUID
	ProjectionRevision int64
	GeneratedAt        time.Time
	CanonicalContent   []byte
	SHA256             [sha256.Size]byte
	Algorithm, KeyID   string
	MAC                [sha256.Size]byte
}

type AdminOperatorCursor struct{ ProjectionRevision, AuthorityRevision, AuditSequence int64 }
type AdminSnapshotQuery struct {
	Operator     AdminOperatorIdentity
	TournamentID uuid.UUID
	Cursor       *AdminOperatorCursor
}

type AdminDraftActionView struct {
	Turn                     int
	ActorID                  uuid.UUID
	Action                   domain.DraftActionType
	Category                 domain.Category
	OccurredAt, TurnDeadline time.Time
}
type AdminDraftView struct {
	ID, SeriesID, FirstParticipantID, SecondParticipantID uuid.UUID
	Format                                                domain.SeriesFormat
	Pool                                                  []domain.Category
	State                                                 domain.DraftState
	Turn                                                  int
	TurnDeadline                                          *time.Time
	Actions                                               []AdminDraftActionView
	SelectedCategories                                    []domain.Category
	Revision                                              int64
}
type AdminPauseSeriesView struct {
	Series        domain.Series
	Revision      int64
	CurrentGameID *uuid.UUID
	ResumeState   *domain.SeriesState
}
type AdminPauseGameView struct {
	SeriesID    uuid.UUID
	Game        domain.Game
	Revision    int64
	Deadline    *time.Time
	ResumeState *domain.GameState
}
type AdminFrozenDeadlineView struct {
	Kind                       string
	OwnerID                    uuid.UUID
	OriginalDeadline, FrozenAt time.Time
	Remaining                  time.Duration
	ResumedAt, ResumedDeadline *time.Time
	Revision                   int64
}
type AdminPresenceView struct {
	ID, TournamentID, RosterID, SeriesID, ParticipantID uuid.UUID
	State                                               string
	PresenceEpoch, Revision                             int64
	ConnectedAt                                         time.Time
	DisconnectedAt                                      *time.Time
	UpdatedAt                                           time.Time
}
type AdminReconnectIntervalView struct {
	ID, PauseID, RosterID, SeriesID, GameID, ParticipantID uuid.UUID
	PresenceEpoch                                          int64
	Number, ContinuationNumber                             int
	ContinuedFromID, SuspendedByPauseID                    *uuid.UUID
	State                                                  string
	OpenedAt, Deadline                                     time.Time
	ClosedAt                                               *time.Time
	Revision                                               int64
	UpdatedAt                                              time.Time
}
type AdminReconnectCounterView struct {
	PauseID, RosterID, ParticipantID uuid.UUID
	Limit, Used                      int
	Revision                         int64
}
type AdminPauseGraphView struct {
	TournamentID, RosterID uuid.UUID
	Revision               int64
	Wave                   AdminWaveView
	Series                 []AdminPauseSeriesView
	Games                  []AdminPauseGameView
	Draft                  *AdminDraftView
	Presence               []AdminPresenceView
	Reconnect              []AdminReconnectIntervalView
	Counters               []AdminReconnectCounterView
	FrozenDeadlines        []AdminFrozenDeadlineView
	ActivePauseID          *uuid.UUID
	PausedAt               *time.Time
	DeadlinesSuppressed    bool
	TerminalActionRevision int64
}
type AdminOperatorSnapshotView struct {
	Tournament TournamentView
	Roster     AdminRosterView
	Waves      []AdminWaveView
	Series     []domain.Series
	PauseGraph *AdminPauseGraphView
	NextCursor AdminOperatorCursor
}
