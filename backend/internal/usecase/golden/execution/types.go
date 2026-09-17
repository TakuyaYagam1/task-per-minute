package execution

import (
	"crypto/sha256"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	goldenstate "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/state"
)

var ErrInvalidGoldenWaveExecution = errors.New("invalid Golden Wave execution")

type GoldenWaveCommandKind string

const (
	GoldenWaveCommandOpened       GoldenWaveCommandKind = "opened"
	GoldenWaveCommandReady        GoldenWaveCommandKind = "ready"
	GoldenWaveCommandDisconnected GoldenWaveCommandKind = "disconnected"
	GoldenWaveCommandReconnected  GoldenWaveCommandKind = "reconnected"
	GoldenWaveCommandStarted      GoldenWaveCommandKind = "started"
)

type GoldenWaveMembershipBinding struct {
	ID             uuid.UUID
	RevisionID     uuid.UUID
	Revision       int64
	Source         goldenstate.GoldenMembershipRevision
	ParticipantIDs []uuid.UUID
	PayloadDigest  [sha256.Size]byte
}

type GoldenPrivateAssignment struct {
	ID            uuid.UUID
	ParticipantID uuid.UUID
	SnapshotID    uuid.UUID
	ContentDigest [sha256.Size]byte
}

type GoldenAttemptAssignment struct {
	ID            uuid.UUID
	RevisionID    uuid.UUID
	Revision      int64
	Scope         goldenstate.GoldenStateScope
	AttemptID     uuid.UUID
	WaveID        uuid.UUID
	MembershipID  uuid.UUID
	Plan          goldenstate.GoldenPlanStateBinding
	EdgeID        uuid.UUID
	ReservationID uuid.UUID
	Snapshot      domain.AssignmentTaskSnapshot
	ContentDigest [sha256.Size]byte
	Private       []GoldenPrivateAssignment
	PayloadDigest [sha256.Size]byte
}

type GoldenAttemptAssignmentEvidence struct {
	ID                     uuid.UUID
	RevisionID             uuid.UUID
	Revision               int64
	Scope                  goldenstate.GoldenStateScope
	AttemptID              uuid.UUID
	WaveID                 uuid.UUID
	MembershipID           uuid.UUID
	Plan                   goldenstate.GoldenPlanStateBinding
	EdgeID                 uuid.UUID
	ReservationID          uuid.UUID
	SnapshotID             uuid.UUID
	TaskID                 uuid.UUID
	ContentDigest          [sha256.Size]byte
	Private                []GoldenPrivateAssignment
	ExecutionPayloadDigest [sha256.Size]byte
	PayloadDigest          [sha256.Size]byte
}

type GoldenWaveExecutionExpectation struct {
	Scope                goldenstate.GoldenStateScope
	Source               goldenstate.GoldenStateExpectation
	RevisionID           uuid.UUID
	Revision             int64
	PayloadDigest        [sha256.Size]byte
	AttemptID            uuid.UUID
	WaveID               uuid.UUID
	WaveRevisionID       domain.WaveRevisionID
	Window               goldenstate.GoldenReadyWindowExpectation
	MembershipID         uuid.UUID
	MembershipRevisionID uuid.UUID
	MembershipRevision   int64
	MembershipDigest     [sha256.Size]byte
	AssignmentID         uuid.UUID
	AssignmentRevisionID uuid.UUID
	AssignmentRevision   int64
	AssignmentDigest     [sha256.Size]byte
	Started              bool
}

type GoldenWaveCommandReceipt struct {
	CommandID         uuid.UUID
	Scope             goldenstate.GoldenStateScope
	Kind              GoldenWaveCommandKind
	CommandDigest     [sha256.Size]byte
	Expected          *GoldenWaveExecutionExpectation
	Result            GoldenWaveExecutionExpectation
	OccurredAt        time.Time
	ParticipantID     uuid.UUID
	UnusedIdentityIDs []uuid.UUID
}

// GoldenWaveCommandReplay is the durable replay envelope for one attempt. Its
// Execution is the latest live snapshot or the final archived snapshot of the
// attempt that accepted Receipt; it is independent from the current pointer.
type GoldenWaveCommandReplay struct {
	Receipt   GoldenWaveCommandReceipt
	Execution GoldenWaveExecution
}

type GoldenWaveExecution struct {
	Scope              goldenstate.GoldenStateScope
	Source             goldenstate.GoldenStateExpectation
	RevisionID         uuid.UUID
	Revision           int64
	PreviousRevisionID *uuid.UUID

	Group                           domain.GoldenGroupState
	GroupBindingDigest              [sha256.Size]byte
	OpeningParticipationEstablished bool
	Attempt                         domain.GoldenAttempt
	Wave                            domain.Wave
	Membership                      GoldenWaveMembershipBinding
	Assignment                      GoldenAttemptAssignment
	Window                          goldenstate.GoldenReadyWindow
	OpenedAt                        time.Time
	Deadline                        time.Time
	Receipts                        []GoldenWaveCommandReceipt
	ReceiptsDigest                  [sha256.Size]byte
	Start                           *GoldenStartRecord
	PayloadDigest                   [sha256.Size]byte
}

type GoldenStartAuthority struct {
	Identity      authoritydomain.Identity
	LeaseRevision int64
	LeaseDigest   [sha256.Size]byte
}

type GoldenStartedAssignment struct {
	AssignmentID    uuid.UUID
	ParticipantID   uuid.UUID
	SnapshotID      uuid.UUID
	ContentDigest   [sha256.Size]byte
	StartedAt       time.Time
	Deadline        time.Time
	Authority       authoritydomain.Identity
	AuthorityDigest [sha256.Size]byte
	DeliveryEnabled bool
}

type GoldenStartRecord struct {
	CommandID                 uuid.UUID
	Scope                     goldenstate.GoldenStateScope
	AttemptID                 uuid.UUID
	WaveID                    uuid.UUID
	WindowID                  uuid.UUID
	ExpectedState             goldenstate.GoldenStateExpectation
	ExpectedExecution         GoldenWaveExecutionExpectation
	ResultExecutionRevisionID uuid.UUID
	ResultWindowRevisionID    uuid.UUID
	StartedAt                 time.Time
	Deadline                  time.Time
	Authority                 GoldenStartAuthority
	Assignments               []GoldenStartedAssignment
	PayloadDigest             [sha256.Size]byte
}

func (e GoldenWaveExecutionExpectation) Equal(other GoldenWaveExecutionExpectation) bool {
	return e.equalAuthority(other) && e.equalMembership(other) && e.equalAssignment(other)
}

func (e GoldenWaveExecutionExpectation) equalAuthority(other GoldenWaveExecutionExpectation) bool {
	return e.Scope == other.Scope && e.Source.Equal(other.Source) && e.RevisionID == other.RevisionID &&
		e.Revision == other.Revision && e.PayloadDigest == other.PayloadDigest &&
		e.AttemptID == other.AttemptID && e.WaveID == other.WaveID &&
		e.WaveRevisionID == other.WaveRevisionID && e.Window == other.Window && e.Started == other.Started
}

func (e GoldenWaveExecutionExpectation) equalMembership(other GoldenWaveExecutionExpectation) bool {
	return e.MembershipID == other.MembershipID && e.MembershipRevisionID == other.MembershipRevisionID &&
		e.MembershipRevision == other.MembershipRevision && e.MembershipDigest == other.MembershipDigest
}

func (e GoldenWaveExecutionExpectation) equalAssignment(other GoldenWaveExecutionExpectation) bool {
	return e.AssignmentID == other.AssignmentID && e.AssignmentRevisionID == other.AssignmentRevisionID &&
		e.AssignmentRevision == other.AssignmentRevision && e.AssignmentDigest == other.AssignmentDigest
}

func (r GoldenWaveCommandReceipt) ExpectedValue() GoldenWaveExecutionExpectation {
	if r.Expected == nil {
		return GoldenWaveExecutionExpectation{}
	}
	return *r.Expected
}
