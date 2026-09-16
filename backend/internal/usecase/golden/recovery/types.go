package recovery

import (
	"errors"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/google/uuid"
)

var (
	ErrInvalidGoldenRecovery   = errors.New("invalid Golden recovery")
	ErrAmbiguousGoldenRecovery = errors.New("ambiguous live Golden attempts")
)

type GoldenRecoverySelection string

const (
	GoldenRecoverySelectionDirect   GoldenRecoverySelection = "direct"
	GoldenRecoverySelectionReserve  GoldenRecoverySelection = "reserve"
	GoldenRecoverySelectionExcluded GoldenRecoverySelection = "excluded"
)

type GoldenRecoveryGroup struct {
	ID                         uuid.UUID
	TournamentID               uuid.UUID
	RevisionID                 domain.DerivedRevisionID
	SourceProjectionRevisionID domain.DerivedRevisionID
	PositionFrom               int
	PositionTo                 int
	ParticipantIDs             []uuid.UUID
}

type GoldenRecoveryMembership struct {
	ID                         uuid.UUID
	ParticipantID              uuid.UUID
	Selection                  GoldenRecoverySelection
	ReservePosition            int
	SelectedAt                 time.Time
	ReadyAt                    *time.Time
	ParticipationEstablishedAt *time.Time
	PromotedAt                 *time.Time
	ExcludedAt                 *time.Time
}

type GoldenRecoverySubmission struct {
	ID             uuid.UUID
	MembershipID   uuid.UUID
	ParticipantID  uuid.UUID
	ServerSequence int64
	Position       int
	AcceptedAt     time.Time
}

type GoldenRecoveryPositionCommit struct {
	ID            uuid.UUID
	AttemptID     uuid.UUID
	SubmissionID  uuid.UUID
	ParticipantID uuid.UUID
	Position      int
	CommittedAt   time.Time
}

type GoldenRecoveryAttempt struct {
	Attempt           domain.GoldenAttempt
	Memberships       []GoldenRecoveryMembership
	Submissions       []GoldenRecoverySubmission
	PositionCommits   []GoldenRecoveryPositionCommit
	ReadyDeadline     *time.Time
	ExecutionDeadline *time.Time
}

type GoldenRecoveryInput struct {
	Group    GoldenRecoveryGroup
	Attempts []GoldenRecoveryAttempt
}

type GoldenRecoveryCommittedAttempt struct {
	AttemptID uuid.UUID
	AttemptNo int
	Positions []GoldenRecoveryPositionCommit
}

type GoldenRecoveryReserve struct {
	MembershipID  uuid.UUID
	ParticipantID uuid.UUID
	Position      int
	PromotedAt    *time.Time
}

type GoldenRecoveryProvisional struct {
	SubmissionID   uuid.UUID
	ParticipantID  uuid.UUID
	ServerSequence int64
	Position       int
}

type GoldenRecoveryDeadlines struct {
	Ready     time.Time
	Execution *time.Time
}

type GoldenRecoveryLiveAttempt struct {
	AttemptID           uuid.UUID
	Reserves            []GoldenRecoveryReserve
	ReadyParticipantIDs []uuid.UUID
	ProvisionalOrder    []GoldenRecoveryProvisional
	Deadlines           GoldenRecoveryDeadlines
}

type GoldenRecoveryResult struct {
	Group                      domain.GoldenGroup
	RetainedPreStartAttemptIDs []uuid.UUID
	CommittedPriorAttempts     []GoldenRecoveryCommittedAttempt
	PermanentExclusions        []uuid.UUID
	LiveAttempt                *GoldenRecoveryLiveAttempt
}
