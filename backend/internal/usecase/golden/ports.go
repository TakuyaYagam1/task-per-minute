package golden

import (
	"context"
	"crypto/sha256"
	"errors"
	"reflect"
	"sort"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	"github.com/google/uuid"
)

type ConnectionClock interface {
	Now() time.Time
}

const (
	goldenIndividualConnectionAttempts = 3
	goldenIndividualParticipantLimit   = domain.TournamentMaxParticipants
	goldenIndividualIntervalLimit      = domain.TournamentMaxParticipants * domain.ReconnectCycleLimit
	goldenIndividualReceiptLimit       = goldenIndividualIntervalLimit * 2
)

var (
	ErrInvalidGoldenIndividualConnection           = errors.New("invalid Golden individual connection")
	ErrGoldenIndividualConnectionAuthorityConflict = errors.New("golden individual connection authority conflict")
	ErrGoldenIndividualConnectionConflict          = errors.New("golden individual connection commit conflict")
	ErrGoldenIndividualConnectionCommandReuse      = errors.New("golden individual connection command identifier was reused")
	ErrGoldenIndividualConnectionUnavailable       = errors.New("golden individual connection mutation is unavailable")
	ErrGoldenIndividualReconnectDeadline           = errors.New("golden individual reconnect deadline reached")
)

type GoldenIndividualConnectionKind string

const (
	GoldenIndividualConnectionDisconnected GoldenIndividualConnectionKind = "disconnected"
	GoldenIndividualConnectionReconnected  GoldenIndividualConnectionKind = "reconnected"
)

type GoldenIndividualReconnectState string

const (
	GoldenIndividualReconnectOpen   GoldenIndividualReconnectState = "open"
	GoldenIndividualReconnectClosed GoldenIndividualReconnectState = "reconnected"
)

type GoldenIndividualReconnectInterval struct {
	ID             uuid.UUID
	ParticipantID  uuid.UUID
	Sequence       int
	State          GoldenIndividualReconnectState
	DisconnectedAt time.Time
	Deadline       time.Time
	ReconnectedAt  *time.Time
}

type GoldenIndividualConnectionExpectation struct {
	Scope         GoldenSubmissionScope
	Execution     GoldenWaveExecutionExpectation
	Submissions   GoldenSubmissionLedgerExpectation
	StartedAt     time.Time
	Deadline      time.Time
	RevisionID    uuid.UUID
	Revision      int64
	PayloadDigest [sha256.Size]byte
}

func (e GoldenIndividualConnectionExpectation) Equal(other GoldenIndividualConnectionExpectation) bool {
	return e.Scope == other.Scope && e.Execution.Equal(other.Execution) && e.Submissions.Equal(other.Submissions) &&
		e.StartedAt.Equal(other.StartedAt) && e.Deadline.Equal(other.Deadline) &&
		e.RevisionID == other.RevisionID && e.Revision == other.Revision && e.PayloadDigest == other.PayloadDigest
}

type GoldenIndividualConnectionReceipt struct {
	CommandID           uuid.UUID
	CommandDigest       [sha256.Size]byte
	Kind                GoldenIndividualConnectionKind
	ParticipantID       uuid.UUID
	IntervalID          uuid.UUID
	Expected            GoldenIndividualConnectionExpectation
	ObservedSubmissions GoldenSubmissionLedgerExpectation
	ResultRevisionID    uuid.UUID
	ResultRevision      int64
	OccurredAt          time.Time
}

type GoldenIndividualConnectionLedger struct {
	Scope                 GoldenSubmissionScope
	Execution             GoldenWaveExecutionExpectation
	Submissions           GoldenSubmissionLedgerExpectation
	StartedAt             time.Time
	Deadline              time.Time
	RevisionID            uuid.UUID
	Revision              int64
	PreviousRevisionID    *uuid.UUID
	ParticipantIDs        []uuid.UUID
	PresentParticipantIDs []uuid.UUID
	Intervals             []GoldenIndividualReconnectInterval
	Receipts              []GoldenIndividualConnectionReceipt
	PayloadDigest         [sha256.Size]byte
}

func NewGoldenIndividualConnectionLedger(
	scope GoldenSubmissionScope,
	execution GoldenWaveExecutionExpectation,
	submissions GoldenSubmissionLedgerExpectation,
	startedAt time.Time,
	deadline time.Time,
	revisionID uuid.UUID,
	participantIDs []uuid.UUID,
) (GoldenIndividualConnectionLedger, error) {
	if len(participantIDs) < 2 || len(participantIDs) > goldenIndividualParticipantLimit ||
		!validGoldenIndividualSubmissionHead(submissions, scope) {
		return GoldenIndividualConnectionLedger{}, goldenIndividualConnectionError("invalid participant list or submission head")
	}
	participants := append([]uuid.UUID(nil), participantIDs...)
	SortIDs(participants)
	ledger := GoldenIndividualConnectionLedger{
		Scope: scope, Execution: CloneExecutionExpectation(execution), Submissions: submissions,
		StartedAt: startedAt.Round(0).UTC(), Deadline: deadline.Round(0).UTC(),
		RevisionID: revisionID, Revision: 1, ParticipantIDs: participants,
		PresentParticipantIDs: append([]uuid.UUID(nil), participants...),
	}
	if err := rebuildGoldenIndividualConnectionLedger(&ledger); err != nil {
		return GoldenIndividualConnectionLedger{}, err
	}
	return ledger.Snapshot(), nil
}

func (l GoldenIndividualConnectionLedger) Snapshot() GoldenIndividualConnectionLedger {
	clone := l
	clone.Execution = CloneExecutionExpectation(l.Execution)
	clone.PreviousRevisionID = connectionCloneUUIDPointer(l.PreviousRevisionID)
	clone.ParticipantIDs = append([]uuid.UUID(nil), l.ParticipantIDs...)
	clone.PresentParticipantIDs = append([]uuid.UUID(nil), l.PresentParticipantIDs...)
	clone.Intervals = make([]GoldenIndividualReconnectInterval, len(l.Intervals))
	for index, interval := range l.Intervals {
		clone.Intervals[index] = interval
		clone.Intervals[index].ReconnectedAt = connectionCloneTimePointer(interval.ReconnectedAt)
	}
	clone.Receipts = make([]GoldenIndividualConnectionReceipt, len(l.Receipts))
	for index, receipt := range l.Receipts {
		clone.Receipts[index] = receipt
		clone.Receipts[index].Expected.Execution = CloneExecutionExpectation(receipt.Expected.Execution)
	}
	return clone
}

func (l GoldenIndividualConnectionLedger) Expectation() GoldenIndividualConnectionExpectation {
	return GoldenIndividualConnectionExpectation{
		Scope: l.Scope, Execution: CloneExecutionExpectation(l.Execution), Submissions: l.Submissions,
		StartedAt: l.StartedAt, Deadline: l.Deadline,
		RevisionID: l.RevisionID, Revision: l.Revision, PayloadDigest: l.PayloadDigest,
	}
}

func (l GoldenIndividualConnectionLedger) IsPresent(participantID uuid.UUID) bool {
	return ContainsID(l.PresentParticipantIDs, participantID)
}

// ConnectionRepository owns the active-execution, submission-head and
// connection-ledger transaction.
type ConnectionRepository interface {
	FindGoldenIndividualConnectionCommand(ctx context.Context, tournamentID, commandID uuid.UUID) (*GoldenIndividualConnectionReplay, error)
	LoadGoldenIndividualDisconnectAuthority(ctx context.Context, scope GoldenSubmissionScope) (GoldenIndividualDisconnectAuthority, error)
	CommitGoldenIndividualConnection(ctx context.Context, commit GoldenIndividualConnectionCommit) (*GoldenIndividualConnectionLedger, bool, error)
}

type ContinuationClock interface {
	Now() time.Time
}

const goldenContinuationAttempts = 3

var (
	ErrInvalidGoldenContinuation           = errors.New("invalid Golden continuation")
	ErrGoldenContinuationAuthorityConflict = errors.New("golden continuation authority conflict")
	ErrGoldenContinuationConflict          = errors.New("golden continuation conflict")
	ErrGoldenContinuationCommandReuse      = errors.New("golden continuation command identifier was reused")
	ErrGoldenContinuationAlreadyCommitted  = errors.New("golden continuation is already committed")
	ErrGoldenContinuationFallbackRequired  = errors.New("golden continuation requires terminal fallback")
	ErrGoldenContinuationReservesExhausted = errors.New("golden continuation reserves are exhausted")
)

type GoldenContinuationCommand struct {
	Scope                    GoldenStateScope
	CommandID                uuid.UUID
	ContinuationID           uuid.UUID
	ExpectedTerminalID       uuid.UUID
	ExpectedTerminalDigest   [sha256.Size]byte
	ExpectedParentID         uuid.UUID
	ExpectedParentDigest     [sha256.Size]byte
	ExpectedState            GoldenStateExpectation
	ExpectedPlan             GoldenPlanStateBinding
	ExpectedPositions        GoldenPositionLedgerExpectation
	ExpectedSwissPoints      GoldenSwissPointLedgerSentinel
	NextAttemptID            uuid.UUID
	NextAssignmentID         uuid.UUID
	NextAssignmentRevisionID uuid.UUID
	PrivateAssignments       []GoldenPrivateAssignmentCommand
}

type GoldenContinuationAuthority struct {
	Scope       GoldenStateScope
	State       GoldenState
	Plan        ExactPlan
	Terminal    GoldenAttemptCommitRecord
	Positions   GoldenPositionLedger
	SwissPoints GoldenSwissPointLedgerSentinel
	Parent      *GoldenContinuationRecord
	Current     *GoldenContinuationRecord
}

type GoldenContinuationRecord struct {
	ID                       uuid.UUID
	CommandID                uuid.UUID
	CommandDigest            [sha256.Size]byte
	Scope                    GoldenStateScope
	SourceTerminalID         uuid.UUID
	SourceTerminalDigest     [sha256.Size]byte
	SourceParentID           uuid.UUID
	SourceParentDigest       [sha256.Size]byte
	ExpectedState            GoldenStateExpectation
	ExpectedPlan             GoldenPlanStateBinding
	ExpectedPositions        GoldenPositionLedgerExpectation
	SwissPoints              GoldenSwissPointLedgerSentinel
	NewIdentityIDs           []uuid.UUID
	ResolvedParticipantIDs   []uuid.UUID
	UnresolvedParticipantIDs []uuid.UUID
	RemainingPositions       []int
	Group                    domain.GoldenGroupState
	Attempt                  domain.GoldenAttempt
	Assignment               GoldenReserveAttemptAssignment
	CreatedAt                time.Time
	Positions                GoldenPositionLedger
	PayloadDigest            [sha256.Size]byte
}

func (r GoldenContinuationRecord) Snapshot() GoldenContinuationRecord {
	clone := r
	clone.ExpectedState = CloneExpectation(r.ExpectedState)
	clone.NewIdentityIDs = append([]uuid.UUID(nil), r.NewIdentityIDs...)
	clone.ResolvedParticipantIDs = append([]uuid.UUID(nil), r.ResolvedParticipantIDs...)
	clone.UnresolvedParticipantIDs = append([]uuid.UUID(nil), r.UnresolvedParticipantIDs...)
	clone.RemainingPositions = append([]int(nil), r.RemainingPositions...)
	clone.Group = CloneGroup(r.Group)
	clone.Attempt = CloneAttempt(r.Attempt)
	clone.Assignment = r.Assignment.Snapshot()
	clone.Positions = r.Positions.Snapshot()
	return clone
}

func (r GoldenContinuationRecord) Validate() error {
	if !validGoldenContinuationRecordIdentity(r) || !validGoldenContinuationRecordBindings(r) {
		return goldenContinuationError("invalid continuation identity or binding")
	}
	if !validGoldenContinuationSuccessor(r) {
		return goldenContinuationError("successor group does not retain the next attempt")
	}
	if !validGoldenContinuationMembership(r) {
		return goldenContinuationError("successor membership is not exact")
	}
	if err := validateGoldenContinuationPartition(r); err != nil {
		return err
	}
	payload, err := goldenContinuationPayload(r)
	if err != nil || r.PayloadDigest == [sha256.Size]byte{} || sha256.Sum256(payload) != r.PayloadDigest {
		return goldenContinuationError("continuation payload digest changed")
	}
	return nil
}

func validGoldenContinuationRecordIdentity(r GoldenContinuationRecord) bool {
	return r.ID != uuid.Nil && r.CommandID != uuid.Nil && r.ID != r.CommandID &&
		ValidStateScope(r.Scope) && r.CommandDigest != [sha256.Size]byte{} &&
		r.SourceTerminalID != uuid.Nil && r.SourceTerminalDigest != [sha256.Size]byte{} &&
		validGoldenContinuationParentReference(r.SourceParentID, r.SourceParentDigest) &&
		r.SourceTerminalID != r.SourceParentID && !ContainsID(r.NewIdentityIDs, r.SourceTerminalID) &&
		(r.SourceParentID == uuid.Nil || !ContainsID(r.NewIdentityIDs, r.SourceParentID)) &&
		domain.IsValidServerTime(r.CreatedAt) && IDsCanonical(r.NewIdentityIDs) &&
		EqualIDs(r.NewIdentityIDs, goldenContinuationRecordIdentityIDs(r))
}

func validGoldenContinuationParentReference(id uuid.UUID, digest [sha256.Size]byte) bool {
	return (id == uuid.Nil) == (digest == [sha256.Size]byte{})
}

func validGoldenContinuationRecordBindings(r GoldenContinuationRecord) bool {
	return r.ExpectedState.Scope == r.Scope && r.ExpectedPlan.GroupID == r.Scope.GroupID &&
		r.ExpectedPlan.GroupRevisionID == r.Scope.GroupRevisionID && r.ExpectedPositions.ScopeIs(r.Scope) &&
		r.SwissPoints.Validate() == nil && r.Attempt.Validate() == nil &&
		r.Attempt.State == domain.GoldenAttemptStatePlanned && r.Attempt.GroupID == r.Scope.GroupID &&
		r.Attempt.GroupRevisionID == r.Scope.GroupRevisionID && r.Assignment.Validate() == nil &&
		r.Assignment.Scope == r.Scope && r.Assignment.AttemptID == r.Attempt.ID &&
		r.Positions.Validate() == nil && r.Positions.Expectation().Equal(r.ExpectedPositions) &&
		goldenContinuationPositionsMatchGroup(r.Positions, r.Group)
}

func goldenContinuationPositionsMatchGroup(
	positions GoldenPositionLedger,
	group domain.GoldenGroupState,
) bool {
	if !PositionLedgerMatchesGroup(positions, group) {
		return false
	}
	members := make(map[uuid.UUID]struct{}, len(group.Members))
	for _, member := range group.Members {
		members[member.ParticipantID] = struct{}{}
	}
	return PositionParticipantsBelongToGroup(positions, members)
}

func validGoldenContinuationSuccessor(r GoldenContinuationRecord) bool {
	if _, err := domain.NewGoldenGroup(r.Group); err != nil || len(r.Group.Attempts) < 2 {
		return false
	}
	return reflect.DeepEqual(r.Group.Attempts[len(r.Group.Attempts)-1], r.Attempt)
}

func validGoldenContinuationMembership(r GoldenContinuationRecord) bool {
	return len(r.UnresolvedParticipantIDs) >= 2 &&
		EqualIDs(r.UnresolvedParticipantIDs, r.Attempt.ParticipantIDs) &&
		EqualIDs(r.UnresolvedParticipantIDs, PrivateAssignmentParticipantIDs(r.Assignment.Private)) &&
		IDsCanonical(r.ResolvedParticipantIDs) && IDsCanonical(r.UnresolvedParticipantIDs)
}

// ContinuationRepository atomically consumes a terminal receipt and stores its successor.
type ContinuationRepository interface {
	FindGoldenContinuation(ctx context.Context, tournamentID, commandID uuid.UUID) (*GoldenContinuationRecord, error)
	LoadGoldenContinuationAuthority(ctx context.Context, scope GoldenStateScope) (GoldenContinuationAuthority, error)
	CommitGoldenContinuation(ctx context.Context, record GoldenContinuationRecord) (*GoldenContinuationRecord, bool, error)
}
type FailureClock interface {
	Now() time.Time
}

const (
	goldenFailureCommitAttempts = 3
	goldenFailureAttemptLimit   = domain.AssignmentReserveCount + 1
	goldenFailureReceiptLimit   = domain.TournamentMaxParticipants * 2
)

var (
	ErrInvalidGoldenFailure           = errors.New("invalid Golden failure")
	ErrGoldenFailureAuthorityConflict = errors.New("golden failure authority conflict")
	ErrGoldenFailureConflict          = errors.New("golden failure commit conflict")
	ErrGoldenFailureCommandReuse      = errors.New("golden failure command identifier was reused")
	ErrGoldenFailureRouteConflict     = errors.New("golden failure route conflicts with locked reserve classification")
)

type GoldenFailureRoute string

const (
	GoldenFailureRouteReplay    GoldenFailureRoute = "replay"
	GoldenFailureRouteExhausted GoldenFailureRoute = "exhausted"
)

type GoldenFailureClassificationKind string

const (
	GoldenFailureClassificationReplay    GoldenFailureClassificationKind = "next_reserve"
	GoldenFailureClassificationExhausted GoldenFailureClassificationKind = "reserve_exhausted"
)

type GoldenFailureEdge struct {
	ID            uuid.UUID
	Position      int
	ReservationID uuid.UUID
	SnapshotID    uuid.UUID
	TaskID        uuid.UUID
	ContentDigest [sha256.Size]byte
}

type GoldenFailureClassification struct {
	Kind             GoldenFailureClassificationKind
	Plan             GoldenPlanStateBinding
	FailedEdge       GoldenFailureEdge
	NextEdge         *GoldenFailureEdge
	FailedAttemptNo  int
	ParticipantIDs   []uuid.UUID
	MembershipDigest [sha256.Size]byte
	Exhausted        bool
	PayloadDigest    [sha256.Size]byte
}

func (c GoldenFailureClassification) Snapshot() GoldenFailureClassification {
	clone := c
	clone.ParticipantIDs = append([]uuid.UUID(nil), c.ParticipantIDs...)
	if c.NextEdge != nil {
		next := *c.NextEdge
		clone.NextEdge = &next
	}
	return clone
}

func (c GoldenFailureClassification) Validate() error {
	if !validGoldenFailureClassificationRoute(c) || !validGoldenFailureClassificationParticipants(c) {
		return goldenFailureError("invalid reserve classification")
	}
	if c.NextEdge != nil && (!validGoldenFailureEdge(*c.NextEdge) || c.NextEdge.Position != c.FailedEdge.Position+1) {
		return goldenFailureError("next reserve edge is not sequential")
	}
	payload, err := goldenFailureClassificationPayload(c)
	if err != nil || c.PayloadDigest == [sha256.Size]byte{} || sha256.Sum256(payload) != c.PayloadDigest {
		return goldenFailureError("reserve classification digest changed")
	}
	return nil
}

func validGoldenFailureClassificationRoute(classification GoldenFailureClassification) bool {
	if classification.Kind != GoldenFailureClassificationReplay &&
		classification.Kind != GoldenFailureClassificationExhausted {
		return false
	}
	if classification.FailedAttemptNo < 1 || !validGoldenFailureEdge(classification.FailedEdge) {
		return false
	}
	if classification.Exhausted != (classification.NextEdge == nil) {
		return false
	}
	return classification.Exhausted == (classification.Kind == GoldenFailureClassificationExhausted)
}

func validGoldenFailureClassificationParticipants(classification GoldenFailureClassification) bool {
	return len(classification.ParticipantIDs) >= 2 &&
		len(classification.ParticipantIDs) <= domain.TournamentMaxParticipants &&
		IDsCanonical(classification.ParticipantIDs) &&
		classification.MembershipDigest == DigestIDs(classification.ParticipantIDs)
}

type GoldenFailureActiveExecution struct {
	Scope          GoldenSubmissionScope
	Expectation    GoldenWaveExecutionExpectation
	Group          domain.GoldenGroupState
	Attempt        domain.GoldenAttempt
	Wave           domain.Wave
	Assignment     GoldenAttemptAssignmentEvidence
	ParticipantIDs []uuid.UUID
	StartedAt      time.Time
	Deadline       time.Time
}

func NewGoldenFailureActiveExecution(execution GoldenWaveExecution) (GoldenFailureActiveExecution, error) {
	if execution.Validate() != nil || execution.Start == nil {
		return GoldenFailureActiveExecution{}, goldenFailureError("active execution is malformed")
	}
	assignment, err := BuildAttemptAssignmentEvidence(execution.Assignment)
	if err != nil {
		return GoldenFailureActiveExecution{}, err
	}
	active := GoldenFailureActiveExecution{
		Scope: GoldenSubmissionScope{
			State: execution.Scope, AttemptID: execution.Attempt.ID, WaveID: execution.Wave.ID,
			AssignmentID: execution.Assignment.ID, SnapshotID: execution.Assignment.Snapshot.SnapshotID,
			TaskID: execution.Assignment.Snapshot.TaskID,
		},
		Expectation: execution.Expectation(), Group: CloneGroup(execution.Group),
		Attempt: CloneAttempt(execution.Attempt), Wave: CloneExecution(execution.Wave),
		Assignment: assignment, ParticipantIDs: append([]uuid.UUID(nil), execution.Membership.ParticipantIDs...),
		StartedAt: execution.Start.StartedAt, Deadline: execution.Start.Deadline,
	}
	SortIDs(active.ParticipantIDs)
	if err := active.Validate(); err != nil {
		return GoldenFailureActiveExecution{}, err
	}
	return active.Snapshot(), nil
}

func (a GoldenFailureActiveExecution) Snapshot() GoldenFailureActiveExecution {
	clone := a
	clone.Expectation = CloneExecutionExpectation(a.Expectation)
	clone.Group = CloneGroup(a.Group)
	clone.Attempt = CloneAttempt(a.Attempt)
	clone.Wave = CloneExecution(a.Wave)
	clone.Assignment = a.Assignment.Snapshot()
	clone.ParticipantIDs = append([]uuid.UUID(nil), a.ParticipantIDs...)
	return clone
}

func (a GoldenFailureActiveExecution) Validate() error {
	if !validGoldenFailureActiveExpectation(a) || !validGoldenFailureActiveTiming(a) {
		return goldenFailureError("invalid active execution header")
	}
	if !validGoldenFailureActiveAttempt(a) {
		return goldenFailureError("invalid active attempt")
	}
	if !validGoldenFailureActiveGroup(a) {
		return goldenFailureError("active group lost its attempt")
	}
	if !validGoldenFailureActiveWave(a) {
		return goldenFailureError("invalid active Wave")
	}
	if !validGoldenFailureActiveAssignment(a) {
		return goldenFailureError("invalid active assignment binding")
	}
	return nil
}

func validGoldenFailureActiveExpectation(active GoldenFailureActiveExecution) bool {
	return validGoldenFailureActiveExecutionHead(active) &&
		validGoldenFailureActiveWindowHead(active.Expectation.Window) &&
		validGoldenFailureActiveMembershipHead(active)
}

func validGoldenFailureActiveExecutionHead(active GoldenFailureActiveExecution) bool {
	expected := active.Expectation
	return active.Scope.IsValid() && expected.Scope == active.Scope.State && expected.Started &&
		expected.Source.Scope == active.Scope.State && expected.RevisionID != uuid.Nil && expected.Revision >= 1 &&
		expected.PayloadDigest != [sha256.Size]byte{} && expected.AttemptID == active.Scope.AttemptID &&
		expected.WaveID == active.Scope.WaveID && !expected.WaveRevisionID.IsZero() &&
		expected.AssignmentID == active.Scope.AssignmentID && expected.AssignmentRevisionID != uuid.Nil &&
		expected.AssignmentRevision >= 1 && expected.AssignmentDigest != [sha256.Size]byte{}
}

func validGoldenFailureActiveWindowHead(window GoldenReadyWindowExpectation) bool {
	return window.WindowID != uuid.Nil && window.RevisionID != uuid.Nil && window.Revision >= 1 &&
		window.ReadinessRevisionID != uuid.Nil && window.ReadinessRevision >= 1 &&
		window.PresenceRevisionID != uuid.Nil && window.PresenceRevision >= 1
}

func validGoldenFailureActiveMembershipHead(active GoldenFailureActiveExecution) bool {
	expected := active.Expectation
	participantDigest := DigestIDs(active.ParticipantIDs)
	return expected.MembershipID != uuid.Nil && expected.MembershipRevisionID != uuid.Nil &&
		expected.MembershipRevision >= 1 && expected.MembershipDigest != [sha256.Size]byte{} &&
		expected.MembershipDigest == participantDigest && expected.Window.ReadinessDigest == participantDigest &&
		expected.Window.PresenceDigest == participantDigest
}

func validGoldenFailureActiveTiming(active GoldenFailureActiveExecution) bool {
	return domain.IsValidServerTime(active.StartedAt) && domain.IsValidServerTime(active.Deadline) &&
		active.Deadline.After(active.StartedAt) && len(active.ParticipantIDs) >= 2 &&
		len(active.ParticipantIDs) <= domain.TournamentMaxParticipants && IDsCanonical(active.ParticipantIDs)
}

func validGoldenFailureActiveAttempt(active GoldenFailureActiveExecution) bool {
	attempt := active.Attempt
	return attempt.Validate() == nil && attempt.State == domain.GoldenAttemptStateActive &&
		attempt.ID == active.Scope.AttemptID && attempt.StartedAt != nil && attempt.StartedAt.Equal(active.StartedAt) &&
		attempt.GroupID == active.Scope.State.GroupID && attempt.GroupRevisionID == active.Scope.State.GroupRevisionID &&
		EqualIDs(attempt.ParticipantIDs, active.ParticipantIDs)
}

func validGoldenFailureActiveGroup(active GoldenFailureActiveExecution) bool {
	if _, err := domain.NewGoldenGroup(active.Group); err != nil || len(active.Group.Attempts) == 0 {
		return false
	}
	return active.Group.ID == active.Scope.State.GroupID &&
		active.Group.RevisionID == active.Scope.State.GroupRevisionID &&
		active.Group.TournamentID == active.Scope.State.TournamentID &&
		reflect.DeepEqual(active.Group.Attempts[len(active.Group.Attempts)-1], active.Attempt)
}

func validGoldenFailureActiveWave(active GoldenFailureActiveExecution) bool {
	wave := active.Wave
	if wave.Validate() != nil || wave.State != domain.WaveStateActive || wave.StartedAt == nil ||
		wave.ReadyWindow == nil || wave.ReadyWindow.ConsumedAt == nil {
		return false
	}
	return wave.ID == active.Scope.WaveID && wave.StartedAt.Equal(active.StartedAt) &&
		wave.TournamentID == active.Scope.State.TournamentID && wave.RevisionID == active.Expectation.WaveRevisionID &&
		wave.ReadyWindow.ID == active.Expectation.Window.WindowID &&
		wave.ReadyWindow.State == domain.ReadyWindowStateConsumed &&
		wave.ReadyWindow.ConsumedAt.Equal(active.StartedAt) &&
		EqualIDs(MemberIDs(wave), active.ParticipantIDs)
}

func validGoldenFailureActiveAssignment(active GoldenFailureActiveExecution) bool {
	assignment := active.Assignment
	if assignment.Validate() != nil || assignment.ID != active.Scope.AssignmentID ||
		assignment.ID != active.Expectation.AssignmentID || assignment.RevisionID != active.Expectation.AssignmentRevisionID ||
		assignment.Revision != active.Expectation.AssignmentRevision || assignment.Scope != active.Scope.State ||
		assignment.MembershipID != active.Expectation.MembershipID || assignment.Plan != active.Expectation.Source.Plan {
		return false
	}
	return validGoldenFailureActiveAssignmentPayload(active)
}

func validGoldenFailureActiveAssignmentPayload(active GoldenFailureActiveExecution) bool {
	assignment := active.Assignment
	return assignment.AttemptID == active.Scope.AttemptID && assignment.WaveID == active.Scope.WaveID &&
		assignment.SnapshotID == active.Scope.SnapshotID && assignment.TaskID == active.Scope.TaskID &&
		assignment.ExecutionPayloadDigest == active.Expectation.AssignmentDigest &&
		EqualIDs(PrivateAssignmentParticipantIDs(assignment.Private), active.ParticipantIDs)
}

func ClassifyGoldenFailure(
	plan ExactPlan,
	active GoldenFailureActiveExecution,
) (GoldenFailureClassification, error) {
	if len(plan.Groups) == 0 || len(plan.Groups) > domain.TournamentMaxParticipants || active.Validate() != nil || plan.Validate() != nil {
		return GoldenFailureClassification{}, goldenFailureError("malformed plan or active execution")
	}
	return classifyGoldenFailureValidatedPlan(plan, active)
}

func classifyGoldenFailureValidatedPlan(
	plan ExactPlan,
	active GoldenFailureActiveExecution,
) (GoldenFailureClassification, error) {
	group := goldenFailurePlanGroup(plan, active.Scope.State)
	if group == nil || len(group.Edges) != domain.AssignmentReserveCount+1 {
		return GoldenFailureClassification{}, goldenFailureError("locked group chain is missing")
	}
	failedIndex := active.Attempt.AttemptNo - 1
	if failedIndex < 0 || failedIndex >= len(group.Edges) {
		return GoldenFailureClassification{}, goldenFailureError("failed attempt is outside the locked chain")
	}
	failed := goldenFailureEdge(group.Edges[failedIndex])
	if !goldenFailureEdgeMatchesAssignment(failed, active.Assignment) {
		return GoldenFailureClassification{}, goldenFailureError("failed attempt did not use its locked edge")
	}
	classification := GoldenFailureClassification{
		Plan: active.Expectation.Source.Plan, FailedEdge: failed, FailedAttemptNo: active.Attempt.AttemptNo,
		ParticipantIDs:   append([]uuid.UUID(nil), active.ParticipantIDs...),
		MembershipDigest: DigestIDs(active.ParticipantIDs),
	}
	if failedIndex+1 < len(group.Edges) {
		next := goldenFailureEdge(group.Edges[failedIndex+1])
		classification.Kind = GoldenFailureClassificationReplay
		classification.NextEdge = &next
	} else {
		classification.Kind = GoldenFailureClassificationExhausted
		classification.Exhausted = true
	}
	payload, err := goldenFailureClassificationPayload(classification)
	if err != nil {
		return GoldenFailureClassification{}, goldenFailureError("encode reserve classification")
	}
	classification.PayloadDigest = sha256.Sum256(payload)
	if err := classification.Validate(); err != nil {
		return GoldenFailureClassification{}, err
	}
	return classification.Snapshot(), nil
}

func goldenFailurePlanGroup(plan ExactPlan, scope GoldenStateScope) *Group {
	for index := range plan.Groups {
		candidate := &plan.Groups[index]
		if candidate.GroupID == scope.GroupID && candidate.GroupRevisionID == scope.GroupRevisionID {
			return candidate
		}
	}
	return nil
}

func goldenFailureEdgeMatchesAssignment(edge GoldenFailureEdge, assignment GoldenAttemptAssignmentEvidence) bool {
	return edge.ID == assignment.EdgeID && edge.ReservationID == assignment.ReservationID &&
		edge.SnapshotID == assignment.SnapshotID && edge.TaskID == assignment.TaskID &&
		edge.ContentDigest == assignment.ContentDigest
}

func goldenFailureEdge(edge Edge) GoldenFailureEdge {
	return GoldenFailureEdge{
		ID: edge.ID, Position: edge.Position, ReservationID: edge.ReservationID,
		SnapshotID: edge.Snapshot.SnapshotID, TaskID: edge.Snapshot.TaskID, ContentDigest: edge.ContentDigest,
	}
}

func validGoldenFailureEdge(edge GoldenFailureEdge) bool {
	return edge.ID != uuid.Nil && edge.Position >= 1 && edge.ReservationID != uuid.Nil &&
		edge.SnapshotID != uuid.Nil && edge.TaskID != uuid.Nil && edge.ContentDigest != [sha256.Size]byte{}
}

// FailureRepository atomically archives the failed execution and stores one failure route.
type FailureRepository interface {
	FindGoldenFailure(ctx context.Context, tournamentID, commandID uuid.UUID) (*GoldenFailureRecord, error)
	LoadGoldenFailureAuthority(ctx context.Context, scope GoldenSubmissionScope) (GoldenFailureAuthority, error)
	CommitGoldenFailure(ctx context.Context, commit GoldenFailureCommit) (*GoldenFailureRecord, bool, error)
}
type Scope struct {
	TournamentID uuid.UUID `json:"tournament_id"`
	PlanSetID    uuid.UUID `json:"plan_set_id"`
}

type Revisions struct {
	SourceProjectionRevisionID domain.DerivedRevisionID `json:"source_projection_revision_id"`
	GroupSetRevisionID         uuid.UUID                `json:"group_set_revision_id"`
	GroupSetRevision           int64                    `json:"group_set_revision"`
	PoolRevisionID             uuid.UUID                `json:"pool_revision_id"`
	PoolRevision               int64                    `json:"pool_revision"`
	HistoryRevisionID          uuid.UUID                `json:"history_revision_id"`
	HistoryRevision            int64                    `json:"history_revision"`
	TaskHealthRevisionID       uuid.UUID                `json:"task_health_revision_id"`
	TaskHealthRevision         int64                    `json:"task_health_revision"`
	ArtifactRevisionID         uuid.UUID                `json:"artifact_revision_id"`
	ArtifactRevision           int64                    `json:"artifact_revision"`
	ReservationRevisionID      uuid.UUID                `json:"reservation_revision_id"`
	ReservationRevision        int64                    `json:"reservation_revision"`
	MembershipRevisionID       uuid.UUID                `json:"membership_revision_id"`
	MembershipRevision         int64                    `json:"membership_revision"`
}

type Expectation struct {
	Revisions           Revisions         `json:"revisions"`
	SourcePayloadDigest [sha256.Size]byte `json:"source_payload_digest"`
	GroupDigest         [sha256.Size]byte `json:"group_digest"`
	PoolDigest          [sha256.Size]byte `json:"pool_digest"`
	HistoryDigest       [sha256.Size]byte `json:"history_digest"`
	TaskHealthDigest    [sha256.Size]byte `json:"task_health_digest"`
	ArtifactDigest      [sha256.Size]byte `json:"artifact_digest"`
	ReservationDigest   [sha256.Size]byte `json:"reservation_digest"`
	MembershipDigest    [sha256.Size]byte `json:"membership_digest"`
}

type GroupAuthority struct {
	Revision             GroupRevision
	ActiveParticipantIDs []uuid.UUID
}

type ParticipantReservation struct {
	ParticipantID uuid.UUID
	PlayerID      uuid.UUID
	Reservation   domain.ParticipantReservation
}

type TaskVersion struct {
	PoolRevisionID uuid.UUID
	Version        int
	Task           domain.Task
	Health         domain.TaskVersionHealth
	ArtifactDigest [sha256.Size]byte
}

type TaskReservation struct {
	TaskVersion    domain.TaskVersionRef
	ReservationID  uuid.UUID
	PlanID         uuid.UUID
	PlanRevisionID uuid.UUID
}

type Authority struct {
	Scope                    Scope
	Revisions                Revisions
	Source                   StandingsProjection
	Groups                   []GroupAuthority
	Pool                     domain.TaskPoolRevision
	History                  []assignmentusecase.TaskReceiptRef
	Candidates               []TaskVersion
	ParticipantReservations  []ParticipantReservation
	ExistingTaskReservations []TaskReservation
	Evidence                 Expectation
}

type GroupCommand struct {
	GroupID         uuid.UUID
	GroupRevisionID domain.DerivedRevisionID
	EdgeIDs         [domain.AssignmentReserveCount + 1]uuid.UUID
	ReservationIDs  [domain.AssignmentReserveCount + 1]uuid.UUID
	SnapshotIDs     [domain.AssignmentReserveCount + 1]uuid.UUID
}

type Command struct {
	Scope          Scope
	PlanID         uuid.UUID
	PlanRevisionID uuid.UUID
	Expected       Expectation
	GroupCommands  []GroupCommand
	CreatedAt      time.Time
}

type Edge struct {
	ID            uuid.UUID
	ReservationID uuid.UUID
	Position      int
	Snapshot      domain.AssignmentTaskSnapshot
	ContentDigest [sha256.Size]byte
}

// Group is opening evidence. Members, exclusions, topology and prior attempts
// stay frozen. Only the current Attempt and ParticipationEstablished may
// advance as derived execution evidence during the atomic start.
type Group struct {
	GroupID                    uuid.UUID
	GroupRevisionID            domain.DerivedRevisionID
	SourceProjectionRevisionID domain.DerivedRevisionID
	PositionFrom               int
	PositionTo                 int
	ParticipantIDs             []uuid.UUID
	Edges                      []Edge
}

type ExactPlan struct {
	Scope          Scope
	PlanID         uuid.UUID
	PlanRevisionID uuid.UUID
	Expected       Expectation
	Authority      Authority
	Groups         []Group
	CreatedAt      time.Time
	ProofHash      string
}

func (a Authority) Expectation() Expectation { return a.Evidence }

func (a Authority) Snapshot() Authority {
	clone := a
	clone.Source = a.Source.Snapshot()
	clone.Groups = make([]GroupAuthority, len(a.Groups))
	for index, group := range a.Groups {
		clone.Groups[index] = GroupAuthority{
			Revision:             group.Revision.Snapshot(),
			ActiveParticipantIDs: append([]uuid.UUID(nil), group.ActiveParticipantIDs...),
		}
	}
	clone.Pool = domain.CloneTaskPool(a.Pool)
	clone.History = append([]assignmentusecase.TaskReceiptRef(nil), a.History...)
	clone.Candidates = cloneGoldenCandidates(a.Candidates)
	clone.ParticipantReservations = append([]ParticipantReservation(nil), a.ParticipantReservations...)
	clone.ExistingTaskReservations = append([]TaskReservation(nil), a.ExistingTaskReservations...)
	return clone
}

func (p ExactPlan) Snapshot() ExactPlan {
	clone := p
	clone.Authority = p.Authority.Snapshot()
	clone.Groups = make([]Group, len(p.Groups))
	for groupIndex, group := range p.Groups {
		clone.Groups[groupIndex] = group
		clone.Groups[groupIndex].ParticipantIDs = append([]uuid.UUID(nil), group.ParticipantIDs...)
		clone.Groups[groupIndex].Edges = append([]Edge(nil), group.Edges...)
		for edgeIndex := range clone.Groups[groupIndex].Edges {
			clone.Groups[groupIndex].Edges[edgeIndex].Snapshot = CloneTaskSnapshot(group.Edges[edgeIndex].Snapshot)
		}
	}
	return clone
}

// PlanRepository owns one transaction. Commit must revalidate the full
// authority, compare every bound revision and digest, and atomically lock the
// plan plus every task reservation.
type PlanRepository interface {
	Get(ctx context.Context, scope Scope, planID uuid.UUID) (*ExactPlan, error)
	LoadAuthority(ctx context.Context, scope Scope) (Authority, error)
	Commit(ctx context.Context, plan ExactPlan) (*ExactPlan, bool, error)
}
type AttemptClock interface {
	Now() time.Time
}

// AttemptRepository atomically commits the terminal attempt, submissions and positions.
type AttemptRepository interface {
	FindGoldenAttemptCommit(ctx context.Context, tournamentID, commandID uuid.UUID) (*GoldenAttemptCommitRecord, error)
	LoadGoldenAttemptCommitAuthority(ctx context.Context, scope GoldenSubmissionScope) (GoldenAttemptCommitAuthority, error)
	CommitGoldenAttempt(ctx context.Context, record GoldenAttemptCommitRecord) (*GoldenAttemptCommitRecord, bool, error)
}
type PrestartClock interface {
	Now() time.Time
}

const retainedGoldenPrestartCommitAttempts = 3

var (
	ErrInvalidGoldenPrestartPause         = errors.New("invalid retained Golden pre-start pause")
	ErrGoldenPrestartAuthorityConflict    = errors.New("retained Golden pre-start authority conflict")
	ErrGoldenPrestartConflict             = errors.New("retained Golden pre-start commit conflict")
	ErrGoldenPrestartCommandReuse         = errors.New("retained Golden pre-start command identifier was reused")
	ErrGoldenPrestartAlreadyStarted       = errors.New("golden attempt already started")
	ErrGoldenPrestartSessionStateConflict = errors.New("retained Golden pre-start session state conflict")
)

type GoldenPrestartPauseReason string

const GoldenPrestartPauseOperatorManual GoldenPrestartPauseReason = "operator_manual"

type RetainedGoldenPrestartState string

const (
	RetainedGoldenPrestartPaused RetainedGoldenPrestartState = "paused"
	RetainedGoldenPrestartReady  RetainedGoldenPrestartState = "ready"
)

type GoldenPrestartOperatorAuthorizationExpectation struct {
	TournamentID  uuid.UUID
	ActorID       uuid.UUID
	RevisionID    uuid.UUID
	Revision      int64
	PayloadDigest [sha256.Size]byte
}

func (e GoldenPrestartOperatorAuthorizationExpectation) Equal(
	other GoldenPrestartOperatorAuthorizationExpectation,
) bool {
	return e == other
}

type GoldenPrestartOperatorAuthorization struct {
	TournamentID  uuid.UUID
	ActorID       uuid.UUID
	RevisionID    uuid.UUID
	Revision      int64
	PayloadDigest [sha256.Size]byte
}

func NewGoldenPrestartOperatorAuthorization(
	tournamentID uuid.UUID,
	actorID uuid.UUID,
	revisionID uuid.UUID,
	revision int64,
) (GoldenPrestartOperatorAuthorization, error) {
	authorization := GoldenPrestartOperatorAuthorization{
		TournamentID: tournamentID, ActorID: actorID, RevisionID: revisionID, Revision: revision,
	}
	payload, err := goldenPrestartAuthorizationPayload(authorization)
	if err != nil {
		return GoldenPrestartOperatorAuthorization{}, goldenPrestartError("encode operator authorization")
	}
	authorization.PayloadDigest = sha256.Sum256(payload)
	if authorization.Validate() != nil {
		return GoldenPrestartOperatorAuthorization{}, goldenPrestartError("invalid operator authorization")
	}
	return authorization, nil
}

func (a GoldenPrestartOperatorAuthorization) Expectation() GoldenPrestartOperatorAuthorizationExpectation {
	return GoldenPrestartOperatorAuthorizationExpectation(a)
}

func (a GoldenPrestartOperatorAuthorization) Validate() error {
	if a.TournamentID == uuid.Nil || a.ActorID == uuid.Nil || a.RevisionID == uuid.Nil || a.Revision < 1 ||
		!ValidIdentitySet([]uuid.UUID{a.TournamentID, a.ActorID, a.RevisionID}) {
		return goldenPrestartError("invalid operator authorization head")
	}
	payload, err := goldenPrestartAuthorizationPayload(a)
	if err != nil || a.PayloadDigest == [sha256.Size]byte{} || sha256.Sum256(payload) != a.PayloadDigest {
		return goldenPrestartError("operator authorization digest changed")
	}
	return nil
}

type RetainedGoldenPrestartExpectation struct {
	Scope         GoldenStateScope
	SessionID     uuid.UUID
	RevisionID    uuid.UUID
	Revision      int64
	State         RetainedGoldenPrestartState
	PayloadDigest [sha256.Size]byte
}

func (e RetainedGoldenPrestartExpectation) Equal(other RetainedGoldenPrestartExpectation) bool {
	return e == other
}

type RetainedGoldenPrestartRecord struct {
	SessionID                 uuid.UUID
	CommandID                 uuid.UUID
	CommandDigest             [sha256.Size]byte
	ActorID                   uuid.UUID
	Authorization             GoldenPrestartOperatorAuthorizationExpectation
	Scope                     GoldenStateScope
	RevisionID                uuid.UUID
	Revision                  int64
	PreviousRevisionID        *uuid.UUID
	State                     RetainedGoldenPrestartState
	Reason                    GoldenPrestartPauseReason
	OccurredAt                time.Time
	SourceExecution           GoldenWaveExecutionExpectation
	Group                     domain.GoldenGroupState
	Attempt                   domain.GoldenAttempt
	WaveID                    uuid.UUID
	Assignment                GoldenAttemptAssignmentEvidence
	Membership                GoldenWaveMembershipBinding
	SupersededWindow          GoldenReadyWindow
	FreshWindow               *GoldenReadyWindow
	FreshExecutionRevisionID  uuid.UUID
	FreshWaveRevisionID       domain.WaveRevisionID
	FreshWaveWindowRevisionID domain.ReadyWindowRevisionID
	FreshExecution            *GoldenWaveExecutionExpectation
	NewIdentityIDs            []uuid.UUID
	PayloadDigest             [sha256.Size]byte
}

func (r RetainedGoldenPrestartRecord) Snapshot() RetainedGoldenPrestartRecord {
	clone := r
	clone.PreviousRevisionID = prestartCloneUUIDPointer(r.PreviousRevisionID)
	clone.SourceExecution = CloneExecutionExpectation(r.SourceExecution)
	clone.Group = CloneGroup(r.Group)
	clone.Attempt = CloneAttempt(r.Attempt)
	clone.Assignment = r.Assignment.Snapshot()
	clone.Membership = CloneMembershipBinding(r.Membership)
	clone.SupersededWindow = CloneReadyWindow(r.SupersededWindow)
	if r.FreshWindow != nil {
		fresh := CloneReadyWindow(*r.FreshWindow)
		clone.FreshWindow = &fresh
	}
	if r.FreshExecution != nil {
		fresh := CloneExecutionExpectation(*r.FreshExecution)
		clone.FreshExecution = &fresh
	}
	clone.NewIdentityIDs = append([]uuid.UUID(nil), r.NewIdentityIDs...)
	return clone
}

func (r RetainedGoldenPrestartRecord) Expectation() RetainedGoldenPrestartExpectation {
	return RetainedGoldenPrestartExpectation{
		Scope: r.Scope, SessionID: r.SessionID, RevisionID: r.RevisionID,
		Revision: r.Revision, State: r.State, PayloadDigest: r.PayloadDigest,
	}
}

// PrestartRepository atomically archives and republishes a retained prestart execution.
type PrestartRepository interface {
	FindRetainedGoldenPrestartCommand(ctx context.Context, tournamentID, commandID uuid.UUID) (*RetainedGoldenPrestartRecord, error)
	LoadRetainedGoldenPrestartAuthority(ctx context.Context, scope GoldenStateScope) (RetainedGoldenPrestartAuthority, error)
	CommitRetainedGoldenPrestart(ctx context.Context, commit RetainedGoldenPrestartCommit) (*RetainedGoldenPrestartRecord, bool, error)
}

type StateClock interface {
	Now() time.Time
}

const goldenStateAttempts = 2

var (
	ErrInvalidGoldenState                   = errors.New("invalid Golden state")
	ErrGoldenParticipationAuthorityConflict = errors.New("golden participation authority conflict")
	ErrGoldenParticipationConflict          = errors.New("golden participation commit conflict")
	ErrGoldenCommandReuse                   = errors.New("golden command identifier was reused")
	ErrGoldenRevisionOverflow               = errors.New("golden revision overflow")
	ErrGoldenParticipantExcluded            = errors.New("golden participant is excluded")
	ErrGoldenReadyWindowClosed              = errors.New("golden ready window is closed")
)

type GoldenReadyEventType string

const (
	GoldenReadyEventAccepted      GoldenReadyEventType = "accepted"
	GoldenReadyEventDisconnected  GoldenReadyEventType = "disconnected"
	GoldenReadyEventAlreadyReady  GoldenReadyEventType = "already_ready"
	GoldenReadyEventAlreadyAbsent GoldenReadyEventType = "already_absent"
)

type GoldenReadyWindowState string

const (
	GoldenReadyWindowOpen    GoldenReadyWindowState = "open"
	GoldenReadyWindowExpired GoldenReadyWindowState = "expired"
)

type GoldenStateScope struct {
	TournamentID    uuid.UUID
	GroupID         uuid.UUID
	GroupRevisionID domain.DerivedRevisionID
}

type GoldenPlanStateBinding struct {
	PlanID                     uuid.UUID
	RevisionID                 uuid.UUID
	Expected                   Expectation
	GroupID                    uuid.UUID
	GroupRevisionID            domain.DerivedRevisionID
	SourceProjectionRevisionID domain.DerivedRevisionID
	PositionFrom               int
	PositionTo                 int
	ParticipantDigest          [sha256.Size]byte
	EdgeDigest                 [sha256.Size]byte
	ProofDigest                [sha256.Size]byte
}

type GoldenMembershipRevision struct {
	RevisionID         uuid.UUID
	Revision           int64
	PreviousRevisionID *uuid.UUID
	PayloadDigest      [sha256.Size]byte
}

type GoldenReadyWindow struct {
	ID                 uuid.UUID
	RevisionID         uuid.UUID
	Revision           int64
	PreviousRevisionID *uuid.UUID
	AttemptID          uuid.UUID
	AttemptNo          int
	OpenedAt           time.Time
	Deadline           time.Time
	State              GoldenReadyWindowState

	ReadinessRevisionID         uuid.UUID
	ReadinessRevision           int64
	ReadinessPreviousRevisionID *uuid.UUID
	ReadinessDigest             [sha256.Size]byte
	ReadyParticipantIDs         []uuid.UUID

	PresenceRevisionID         uuid.UUID
	PresenceRevision           int64
	PresencePreviousRevisionID *uuid.UUID
	PresenceDigest             [sha256.Size]byte
	BasePresentParticipantIDs  []uuid.UUID
	PresentParticipantIDs      []uuid.UUID
}

type GoldenReadyWindowExpectation struct {
	WindowID            uuid.UUID
	RevisionID          uuid.UUID
	Revision            int64
	ReadinessRevisionID uuid.UUID
	ReadinessRevision   int64
	ReadinessDigest     [sha256.Size]byte
	PresenceRevisionID  uuid.UUID
	PresenceRevision    int64
	PresenceDigest      [sha256.Size]byte
}

type GoldenStateExpectation struct {
	Scope                         GoldenStateScope
	RevisionID                    uuid.UUID
	Revision                      int64
	PayloadDigest                 [sha256.Size]byte
	Plan                          GoldenPlanStateBinding
	Membership                    GoldenMembershipRevision
	SourceProjectionRevisionID    domain.DerivedRevisionID
	SourceProjectionPayloadDigest [sha256.Size]byte
	TopologyPayloadDigest         [sha256.Size]byte
}

type GoldenReadyEvent struct {
	CommandID      uuid.UUID
	Scope          GoldenStateScope
	ParticipantID  uuid.UUID
	Type           GoldenReadyEventType
	AttemptID      uuid.UUID
	WindowID       uuid.UUID
	ExpectedState  GoldenStateExpectation
	ExpectedWindow GoldenReadyWindowExpectation
	CommandDigest  [sha256.Size]byte

	ResultStateRevisionID     uuid.UUID
	ResultWindowRevisionID    uuid.UUID
	ResultReadinessRevisionID uuid.UUID
	ResultPresenceRevisionID  uuid.UUID
	OccurredAt                time.Time
}

type GoldenState struct {
	Scope              GoldenStateScope
	Topology           GroupRevision
	ExactPlan          ExactPlan
	Group              domain.GoldenGroupState
	Plan               GoldenPlanStateBinding
	Membership         GoldenMembershipRevision
	RevisionID         uuid.UUID
	Revision           int64
	PreviousRevisionID *uuid.UUID
	Windows            []GoldenReadyWindow
	ReadyEvents        []GoldenReadyEvent
	NoShows            []GoldenNoShowResolution
	Allocation         *GoldenAllocation
	PayloadDigest      [sha256.Size]byte
}

type GoldenStateCommit struct {
	Expected GoldenStateExpectation
	Next     GoldenState
}

type GoldenReadyCommand struct {
	Scope              GoldenStateScope
	CommandID          uuid.UUID
	ActorParticipantID uuid.UUID
	ParticipantID      uuid.UUID
	AttemptID          uuid.UUID
	WindowID           uuid.UUID
	ExpectedState      GoldenStateExpectation
	ExpectedWindow     GoldenReadyWindowExpectation

	NextStateRevisionID     uuid.UUID
	NextWindowRevisionID    uuid.UUID
	NextReadinessRevisionID uuid.UUID
}

type GoldenDisconnectCommand struct {
	Scope          GoldenStateScope
	CommandID      uuid.UUID
	ParticipantID  uuid.UUID
	AttemptID      uuid.UUID
	WindowID       uuid.UUID
	ExpectedState  GoldenStateExpectation
	ExpectedWindow GoldenReadyWindowExpectation

	NextStateRevisionID     uuid.UUID
	NextWindowRevisionID    uuid.UUID
	NextReadinessRevisionID uuid.UUID
	NextPresenceRevisionID  uuid.UUID
}

func BuildGoldenState(input GoldenState) (GoldenState, error) {
	state := input.Snapshot()
	plan, err := goldenPlanBinding(state.ExactPlan, state.Scope)
	if err != nil {
		return GoldenState{}, err
	}
	if state.Plan == (GoldenPlanStateBinding{}) {
		state.Plan = plan
	} else if state.Plan != plan {
		return GoldenState{}, goldenStateError("exact plan binding changed")
	}
	for index := range state.Windows {
		window := &state.Windows[index]
		if window.BasePresentParticipantIDs == nil {
			window.BasePresentParticipantIDs = append([]uuid.UUID(nil), window.PresentParticipantIDs...)
		}
		canonicalGoldenIDs(window.BasePresentParticipantIDs)
		canonicalGoldenIDs(window.ReadyParticipantIDs)
		canonicalGoldenIDs(window.PresentParticipantIDs)
		window.ReadinessDigest = goldenParticipantSetDigest(window.ReadyParticipantIDs)
		window.PresenceDigest = goldenParticipantSetDigest(window.PresentParticipantIDs)
	}
	state.Membership.PayloadDigest = goldenMembershipDigest(state.Group.Members)
	payload, err := goldenStatePayload(state)
	if err != nil {
		return GoldenState{}, goldenStateError("encode payload: %v", err)
	}
	state.PayloadDigest = sha256.Sum256(payload)
	if err := state.Validate(); err != nil {
		return GoldenState{}, err
	}
	return state.Snapshot(), nil
}

func (s GoldenState) Validate() error {
	if err := validateGoldenStateIdentity(s); err != nil {
		return err
	}
	if err := validateGoldenIdentityRoles(s); err != nil {
		return err
	}
	if err := validateGoldenStateTopology(s); err != nil {
		return err
	}
	if err := validateGoldenStateWindows(s); err != nil {
		return err
	}
	if err := validateGoldenReadyEvents(s); err != nil {
		return err
	}
	if err := validateGoldenNoShows(s); err != nil {
		return err
	}
	if err := validateGoldenAttemptCoverage(s); err != nil {
		return err
	}
	if err := validateGoldenAllocation(s); err != nil {
		return err
	}
	if err := validateGoldenTransitionChain(s); err != nil {
		return err
	}
	payload, err := goldenStatePayload(s)
	if err != nil || s.PayloadDigest == [sha256.Size]byte{} || sha256.Sum256(payload) != s.PayloadDigest {
		return goldenStateError("payload digest changed")
	}
	return nil
}

func (s GoldenState) Snapshot() GoldenState {
	clone := s
	clone.Topology = s.Topology.Snapshot()
	clone.ExactPlan = s.ExactPlan.Snapshot()
	clone.Group = CloneGroup(s.Group)
	clone.Membership.PreviousRevisionID = cloneGoldenUUID(s.Membership.PreviousRevisionID)
	clone.PreviousRevisionID = cloneGoldenUUID(s.PreviousRevisionID)
	clone.Windows = make([]GoldenReadyWindow, len(s.Windows))
	for index, window := range s.Windows {
		clone.Windows[index] = CloneReadyWindow(window)
	}
	clone.ReadyEvents = make([]GoldenReadyEvent, len(s.ReadyEvents))
	for index, event := range s.ReadyEvents {
		clone.ReadyEvents[index] = event
		clone.ReadyEvents[index].ExpectedState.Membership.PreviousRevisionID = cloneGoldenUUID(
			event.ExpectedState.Membership.PreviousRevisionID,
		)
	}
	clone.NoShows = cloneGoldenNoShows(s.NoShows)
	clone.Allocation = cloneGoldenAllocation(s.Allocation)
	return clone
}

func (s GoldenState) Expectation() GoldenStateExpectation {
	return GoldenStateExpectation{
		Scope: s.Scope, RevisionID: s.RevisionID, Revision: s.Revision,
		PayloadDigest: s.PayloadDigest, Plan: s.Plan, Membership: s.Membership,
		SourceProjectionRevisionID:    s.Topology.SourceProjectionRevisionID(),
		SourceProjectionPayloadDigest: s.Topology.SourceProjectionPayloadDigest(),
		TopologyPayloadDigest:         s.Topology.PayloadDigest(),
	}
}

func (e GoldenStateExpectation) Equal(other GoldenStateExpectation) bool {
	return e.Scope == other.Scope && e.RevisionID == other.RevisionID && e.Revision == other.Revision &&
		e.PayloadDigest == other.PayloadDigest && e.Plan == other.Plan &&
		MembershipRevisionsEqual(e.Membership, other.Membership) &&
		e.SourceProjectionRevisionID == other.SourceProjectionRevisionID &&
		e.SourceProjectionPayloadDigest == other.SourceProjectionPayloadDigest &&
		e.TopologyPayloadDigest == other.TopologyPayloadDigest
}

func (w GoldenReadyWindow) Expectation() GoldenReadyWindowExpectation {
	return GoldenReadyWindowExpectation{
		WindowID: w.ID, RevisionID: w.RevisionID, Revision: w.Revision,
		ReadinessRevisionID: w.ReadinessRevisionID, ReadinessRevision: w.ReadinessRevision,
		ReadinessDigest:    w.ReadinessDigest,
		PresenceRevisionID: w.PresenceRevisionID, PresenceRevision: w.PresenceRevision,
		PresenceDigest: w.PresenceDigest,
	}
}

func (s GoldenState) ActiveParticipantIDs() []uuid.UUID {
	active := make([]uuid.UUID, 0, len(s.Group.Members))
	for _, member := range s.Group.Members {
		if !member.Excluded {
			active = append(active, member.ParticipantID)
		}
	}
	return active
}

// StateRepository owns the group-level compare-and-set.
type StateRepository interface {
	LoadGoldenState(ctx context.Context, scope GoldenStateScope) (GoldenState, error)
	CommitGoldenState(ctx context.Context, commit GoldenStateCommit) (*GoldenState, bool, error)
}

const goldenSubmissionCommitAttempts = 4

var (
	ErrInvalidGoldenSubmission           = errors.New("invalid Golden submission")
	ErrGoldenSubmissionAuthorityConflict = errors.New("golden submission authority conflict")
	ErrGoldenSubmissionConflict          = errors.New("golden submission commit conflict")
	ErrGoldenSubmissionCommandReuse      = errors.New("golden submission command identifier was reused")
	ErrGoldenSubmissionIncorrect         = errors.New("golden submission is not correct")
	ErrGoldenSubmissionClosed            = errors.New("golden submission window is closed")
	ErrGoldenSubmissionDuplicateLimit    = errors.New("golden submission duplicate limit reached")
)

type GoldenSubmissionDisposition string

const (
	GoldenSubmissionAccepted  GoldenSubmissionDisposition = "accepted"
	GoldenSubmissionDuplicate GoldenSubmissionDisposition = "duplicate_first_correct"
)

type GoldenSubmissionVerification struct {
	ID               uuid.UUID
	RevisionID       uuid.UUID
	Scope            GoldenSubmissionScope
	ParticipantID    uuid.UUID
	Correct          bool
	VerifiedAt       time.Time
	EvidenceDigest   [sha256.Size]byte
	Authority        authoritydomain.Identity
	AssignmentDigest [sha256.Size]byte
}

func (v GoldenSubmissionVerification) Validate() error {
	if v.ID == uuid.Nil || v.RevisionID == uuid.Nil || v.ID == v.RevisionID || !v.Scope.IsValid() ||
		v.ParticipantID == uuid.Nil || !domain.IsValidServerTime(v.VerifiedAt) ||
		v.EvidenceDigest == [sha256.Size]byte{} || v.AssignmentDigest == [sha256.Size]byte{} ||
		v.Authority.Validate() != nil {
		return goldenSubmissionError("invalid correctness attestation")
	}
	if !ValidIdentitySet([]uuid.UUID{
		v.ID, v.RevisionID, v.ParticipantID, v.Authority.HolderID, v.Authority.LeaseID,
	}) {
		return goldenSubmissionError("correctness attestation identity is aliased")
	}
	return nil
}

type GoldenSubmissionCommand struct {
	Scope                GoldenSubmissionScope
	CommandID            uuid.UUID
	ActorParticipantID   uuid.UUID
	ParticipantID        uuid.UUID
	VerificationID       uuid.UUID
	ExpectedExecution    GoldenWaveExecutionExpectation
	NextLedgerRevisionID uuid.UUID
}

type GoldenSubmissionRecord struct {
	ID                     uint64
	Scope                  GoldenSubmissionScope
	ParticipantID          uuid.UUID
	VerificationID         uuid.UUID
	VerificationRevisionID uuid.UUID
	EvidenceDigest         [sha256.Size]byte
	VerifiedAt             time.Time
	CommittedAt            time.Time
	Authority              authoritydomain.Identity
	AssignmentDigest       [sha256.Size]byte
}

type GoldenSubmissionReceipt struct {
	CommandID        uuid.UUID
	Scope            GoldenSubmissionScope
	ParticipantID    uuid.UUID
	VerificationID   uuid.UUID
	CommandDigest    [sha256.Size]byte
	Disposition      GoldenSubmissionDisposition
	SubmissionID     uint64
	Expected         GoldenSubmissionLedgerExpectation
	ResultRevisionID uuid.UUID
	ResultRevision   int64
	CommittedAt      time.Time
}

type GoldenSubmissionLedger struct {
	Scope              GoldenSubmissionScope
	RevisionID         uuid.UUID
	Revision           int64
	PreviousRevisionID *uuid.UUID
	NextSubmissionID   uint64
	Submissions        []GoldenSubmissionRecord
	Receipts           []GoldenSubmissionReceipt
	PayloadDigest      [sha256.Size]byte
}

func NewGoldenSubmissionLedger(
	scope GoldenSubmissionScope,
	revisionID uuid.UUID,
) (GoldenSubmissionLedger, error) {
	ledger := GoldenSubmissionLedger{
		Scope: scope, RevisionID: revisionID, Revision: 1, NextSubmissionID: 1,
	}
	return buildGoldenSubmissionLedger(ledger)
}

func (l GoldenSubmissionLedger) Snapshot() GoldenSubmissionLedger {
	clone := l
	clone.PreviousRevisionID = submissionCloneUUIDPointer(l.PreviousRevisionID)
	clone.Submissions = append([]GoldenSubmissionRecord(nil), l.Submissions...)
	clone.Receipts = append([]GoldenSubmissionReceipt(nil), l.Receipts...)
	return clone
}

func (l GoldenSubmissionLedger) Expectation() GoldenSubmissionLedgerExpectation {
	return GoldenSubmissionLedgerExpectation{
		Scope: l.Scope, RevisionID: l.RevisionID, Revision: l.Revision,
		NextSubmissionID: l.NextSubmissionID, PayloadDigest: l.PayloadDigest,
	}
}

func (l GoldenSubmissionLedger) HasParticipant(participantID uuid.UUID) bool {
	for _, submission := range l.Submissions {
		if submission.ParticipantID == participantID {
			return true
		}
	}
	return false
}

func (l GoldenSubmissionLedger) ProvisionalOrder() []GoldenSubmissionRecord {
	result := append([]GoldenSubmissionRecord(nil), l.Submissions...)
	sort.Slice(result, func(i, j int) bool {
		if result[i].CommittedAt.Equal(result[j].CommittedAt) {
			return result[i].ID < result[j].ID
		}
		return result[i].CommittedAt.Before(result[j].CommittedAt)
	})
	return result
}

func (l GoldenSubmissionLedger) Validate() error {
	if !l.Scope.IsValid() || l.RevisionID == uuid.Nil || l.Revision < 1 ||
		!submissionValidGoldenRevisionPredecessor(l.RevisionID, l.Revision, l.PreviousRevisionID) ||
		l.NextSubmissionID < 1 || len(l.Receipts) != int(l.Revision-1) {
		return goldenSubmissionError("invalid ledger identity or revision")
	}
	if err := validateGoldenSubmissionRecords(l); err != nil {
		return err
	}
	if err := validateGoldenSubmissionReceipts(l); err != nil {
		return err
	}
	payload, err := goldenSubmissionLedgerPayload(l)
	if err != nil || l.PayloadDigest == [sha256.Size]byte{} || sha256.Sum256(payload) != l.PayloadDigest {
		return goldenSubmissionError("ledger payload digest changed")
	}
	return nil
}

func validateGoldenSubmissionRecords(ledger GoldenSubmissionLedger) error {
	participants := make(map[uuid.UUID]struct{}, len(ledger.Submissions))
	verifications := make(map[uuid.UUID]struct{}, len(ledger.Submissions))
	expectedID := uint64(1)
	for _, submission := range ledger.Submissions {
		if !ValidRecord(submission, ledger.Scope, expectedID) {
			return goldenSubmissionError("invalid retained correct submission")
		}
		if _, duplicate := participants[submission.ParticipantID]; duplicate {
			return goldenSubmissionError("participant has more than one correct submission")
		}
		if _, duplicate := verifications[submission.VerificationID]; duplicate {
			return goldenSubmissionError("correctness attestation is reused")
		}
		participants[submission.ParticipantID] = struct{}{}
		verifications[submission.VerificationID] = struct{}{}
		expectedID++
	}
	if ledger.NextSubmissionID != uint64(len(ledger.Submissions))+1 {
		return goldenSubmissionError("monotonic submission sequence changed")
	}
	return nil
}

func ValidRecord(
	submission GoldenSubmissionRecord,
	scope GoldenSubmissionScope,
	expectedID uint64,
) bool {
	validIdentity := submission.ID == expectedID && submission.Scope == scope &&
		submission.ParticipantID != uuid.Nil && submission.VerificationID != uuid.Nil &&
		submission.VerificationRevisionID != uuid.Nil
	validEvidence := submission.EvidenceDigest != [sha256.Size]byte{} &&
		submission.AssignmentDigest != [sha256.Size]byte{} && submission.Authority.Validate() == nil
	validTime := domain.IsValidServerTime(submission.VerifiedAt) && domain.IsValidServerTime(submission.CommittedAt) &&
		!submission.CommittedAt.Before(submission.VerifiedAt)
	return validIdentity && validEvidence && validTime
}

func validateGoldenSubmissionReceipts(ledger GoldenSubmissionLedger) error {
	state := goldenSubmissionReceiptValidationState{
		commands:   make(map[uuid.UUID]struct{}, len(ledger.Receipts)),
		revisions:  make(map[uuid.UUID]struct{}, len(ledger.Receipts)+1),
		duplicates: make(map[uuid.UUID]struct{}),
		nextID:     1,
	}
	for index, receipt := range ledger.Receipts {
		if err := state.consume(ledger, receipt, index); err != nil {
			return err
		}
	}
	if len(ledger.Receipts) > 0 && (ledger.PreviousRevisionID == nil ||
		*ledger.PreviousRevisionID != ledger.Receipts[len(ledger.Receipts)-1].Expected.RevisionID) {
		return goldenSubmissionError("ledger predecessor changed")
	}
	if state.nextID != ledger.NextSubmissionID ||
		(len(ledger.Receipts) > 0 && state.previousResult != ledger.RevisionID) {
		return goldenSubmissionError("receipt chain does not reach the ledger head")
	}
	return nil
}

type goldenSubmissionReceiptValidationState struct {
	commands       map[uuid.UUID]struct{}
	revisions      map[uuid.UUID]struct{}
	duplicates     map[uuid.UUID]struct{}
	previousResult uuid.UUID
	nextID         uint64
}

func (s *goldenSubmissionReceiptValidationState) consume(
	ledger GoldenSubmissionLedger,
	receipt GoldenSubmissionReceipt,
	index int,
) error {
	if !validGoldenSubmissionReceiptHeader(receipt, ledger.Scope, index, s.previousResult) {
		return goldenSubmissionError("invalid retained command receipt")
	}
	if !validGoldenSubmissionReceiptExpected(receipt.Expected, s.nextID) {
		return goldenSubmissionError("command receipt expected head changed")
	}
	if _, duplicate := s.commands[receipt.CommandID]; duplicate {
		return goldenSubmissionError("duplicate retained command")
	}
	if index == 0 {
		s.revisions[receipt.Expected.RevisionID] = struct{}{}
	}
	if _, reused := s.revisions[receipt.ResultRevisionID]; reused {
		return goldenSubmissionError("submission ledger revision identity is reused")
	}
	submission, found := goldenSubmissionByID(ledger.Submissions, receipt.SubmissionID)
	if !found || submission.ParticipantID != receipt.ParticipantID {
		return goldenSubmissionError("command receipt references another participant submission")
	}
	if receipt.Disposition == GoldenSubmissionDuplicate {
		if _, duplicate := s.duplicates[receipt.ParticipantID]; duplicate {
			return goldenSubmissionError("participant duplicate receipt limit exceeded")
		}
		s.duplicates[receipt.ParticipantID] = struct{}{}
	}
	nextID, err := goldenSubmissionReceiptNextID(receipt, submission, s.nextID)
	if err != nil {
		return err
	}
	s.commands[receipt.CommandID] = struct{}{}
	s.revisions[receipt.ResultRevisionID] = struct{}{}
	s.previousResult = receipt.ResultRevisionID
	s.nextID = nextID
	return nil
}

func validGoldenSubmissionReceiptExpected(expected GoldenSubmissionLedgerExpectation, nextID uint64) bool {
	return expected.RevisionID != uuid.Nil && expected.Revision >= 1 &&
		expected.NextSubmissionID == nextID && expected.PayloadDigest != [sha256.Size]byte{}
}

func goldenSubmissionReceiptNextID(
	receipt GoldenSubmissionReceipt,
	submission GoldenSubmissionRecord,
	nextID uint64,
) (uint64, error) {
	switch receipt.Disposition {
	case GoldenSubmissionAccepted:
		if receipt.SubmissionID != nextID || receipt.VerificationID != submission.VerificationID ||
			!receipt.CommittedAt.Equal(submission.CommittedAt) {
			return 0, goldenSubmissionError("accepted receipt changed its retained submission")
		}
		return nextID + 1, nil
	case GoldenSubmissionDuplicate:
		if receipt.SubmissionID >= nextID {
			return 0, goldenSubmissionError("duplicate receipt references a future submission")
		}
		return nextID, nil
	default:
		return 0, goldenSubmissionError("invalid submission disposition")
	}
}

func goldenSubmissionByID(
	submissions []GoldenSubmissionRecord,
	submissionID uint64,
) (GoldenSubmissionRecord, bool) {
	if submissionID < 1 || submissionID > uint64(len(submissions)) {
		return GoldenSubmissionRecord{}, false
	}
	submission := submissions[submissionID-1]
	return submission, submission.ID == submissionID
}

func validGoldenSubmissionReceiptHeader(
	receipt GoldenSubmissionReceipt,
	scope GoldenSubmissionScope,
	index int,
	previousResult uuid.UUID,
) bool {
	validIdentity := receipt.CommandID != uuid.Nil && receipt.Scope == scope &&
		receipt.ParticipantID != uuid.Nil && receipt.VerificationID != uuid.Nil &&
		receipt.CommandDigest != [sha256.Size]byte{}
	validRevision := receipt.Expected.Scope == scope && receipt.ResultRevisionID != uuid.Nil &&
		receipt.ResultRevision == int64(index+2) && receipt.Expected.Revision == int64(index+1)
	validPrevious := index == 0 || receipt.Expected.RevisionID == previousResult
	return validIdentity && validRevision && validPrevious && domain.IsValidServerTime(receipt.CommittedAt)
}

// SubmissionRepository owns the durable submission-ledger transaction.
type SubmissionRepository interface {
	LoadGoldenSubmissionAuthority(
		ctx context.Context,
		scope GoldenSubmissionScope,
		commandID uuid.UUID,
		verificationID uuid.UUID,
	) (GoldenSubmissionAuthority, error)
	CommitGoldenSubmission(ctx context.Context, commit GoldenSubmissionCommit) (*GoldenSubmissionLedger, bool, error)
}
type WaveClock interface {
	Now() time.Time
}

// WaveRepository owns the execution compare-and-set. The source Golden state
// is read-only through this port.
type WaveRepository interface {
	LoadGoldenState(ctx context.Context, scope GoldenStateScope) (GoldenState, error)
	FindGoldenWaveCommand(ctx context.Context, tournamentID, commandID uuid.UUID) (*GoldenWaveCommandReplay, error)
	LoadGoldenWaveExecution(ctx context.Context, scope GoldenStateScope) (*GoldenWaveExecution, error)
	CommitGoldenWaveExecution(ctx context.Context, commit GoldenWaveExecutionCommit) (*GoldenWaveExecution, bool, error)
}

const (
	goldenWaveCommitAttempts = 2
)

var (
	ErrInvalidGoldenWaveExecution  = errors.New("invalid Golden Wave execution")
	ErrGoldenReadyWindowIneligible = errors.New("golden ready window is ineligible")
	ErrGoldenWaveAuthorityConflict = errors.New("golden Wave authority conflict")
	ErrGoldenWaveCommitConflict    = errors.New("golden Wave commit conflict")
	ErrGoldenWaveCommandReuse      = errors.New("golden Wave command identifier was reused")
	ErrGoldenWaveIdentityConflict  = errors.New("golden Wave identity is already reserved")
	ErrGoldenWaveRevisionOverflow  = errors.New("golden Wave revision overflow")
	ErrGoldenWaveExecutionNotFound = errors.New("golden Wave execution was not found")
	ErrGoldenWaveAuthorityNotLive  = errors.New("golden Wave execution authority is not live")
)

const GoldenReadyWindowConsumed GoldenReadyWindowState = "consumed"

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
	Source         GoldenMembershipRevision
	ParticipantIDs []uuid.UUID
	PayloadDigest  [sha256.Size]byte
}

type GoldenPrivateAssignmentCommand struct {
	ParticipantID uuid.UUID
	AssignmentID  uuid.UUID
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
	Scope         GoldenStateScope
	AttemptID     uuid.UUID
	WaveID        uuid.UUID
	MembershipID  uuid.UUID
	Plan          GoldenPlanStateBinding
	EdgeID        uuid.UUID
	ReservationID uuid.UUID
	Snapshot      domain.AssignmentTaskSnapshot
	ContentDigest [sha256.Size]byte
	Private       []GoldenPrivateAssignment
	PayloadDigest [sha256.Size]byte
}

// GoldenAttemptAssignmentEvidence is the immutable, sanitized assignment
// authority retained by a terminal receipt. It deliberately carries only task
// snapshot identities and digests, never the snapshot's secret material.
type GoldenAttemptAssignmentEvidence struct {
	ID                     uuid.UUID
	RevisionID             uuid.UUID
	Revision               int64
	Scope                  GoldenStateScope
	AttemptID              uuid.UUID
	WaveID                 uuid.UUID
	MembershipID           uuid.UUID
	Plan                   GoldenPlanStateBinding
	EdgeID                 uuid.UUID
	ReservationID          uuid.UUID
	SnapshotID             uuid.UUID
	TaskID                 uuid.UUID
	ContentDigest          [sha256.Size]byte
	Private                []GoldenPrivateAssignment
	ExecutionPayloadDigest [sha256.Size]byte
	PayloadDigest          [sha256.Size]byte
}

func (e GoldenAttemptAssignmentEvidence) Snapshot() GoldenAttemptAssignmentEvidence {
	clone := e
	clone.Private = append([]GoldenPrivateAssignment(nil), e.Private...)
	return clone
}

func (e GoldenAttemptAssignmentEvidence) Validate() error {
	ids := make([]uuid.UUID, 0, 11+len(e.Private))
	ids = append(ids,
		e.ID, e.RevisionID, e.AttemptID, e.WaveID, e.MembershipID, e.Plan.PlanID,
		e.Plan.RevisionID, e.EdgeID, e.ReservationID, e.SnapshotID, e.TaskID,
	)
	if !validGoldenAttemptAssignmentEvidenceHeader(e) {
		return goldenWaveError("invalid sanitized assignment evidence")
	}
	participants, privateIDs, validPrivate := goldenAttemptAssignmentPrivateEvidence(e)
	if !validPrivate {
		return goldenWaveError("invalid private assignment evidence")
	}
	ids = append(ids, privateIDs...)
	SortIDs(participants)
	SortIDs(privateIDs)
	if !ValidIdentitySet(ids) || !IDsCanonical(participants) || !IDsCanonical(privateIDs) {
		return goldenWaveError("assignment evidence repeats a participant or identity")
	}
	payload, err := goldenAttemptAssignmentEvidencePayload(e)
	if err != nil || e.PayloadDigest == [sha256.Size]byte{} || sha256.Sum256(payload) != e.PayloadDigest {
		return goldenWaveError("assignment evidence digest changed")
	}
	return nil
}

func validGoldenAttemptAssignmentEvidenceHeader(e GoldenAttemptAssignmentEvidence) bool {
	return ValidStateScope(e.Scope) && e.Revision == 1 && e.Plan.GroupID == e.Scope.GroupID &&
		e.Plan.GroupRevisionID == e.Scope.GroupRevisionID && e.ContentDigest != [sha256.Size]byte{} &&
		e.ExecutionPayloadDigest != [sha256.Size]byte{} && len(e.Private) >= 2
}

func goldenAttemptAssignmentPrivateEvidence(
	e GoldenAttemptAssignmentEvidence,
) ([]uuid.UUID, []uuid.UUID, bool) {
	participants := make([]uuid.UUID, len(e.Private))
	privateIDs := make([]uuid.UUID, len(e.Private))
	for index, private := range e.Private {
		if private.ID == uuid.Nil || private.ParticipantID == uuid.Nil || private.SnapshotID != e.SnapshotID ||
			private.ContentDigest != e.ContentDigest {
			return nil, nil, false
		}
		participants[index] = private.ParticipantID
		privateIDs[index] = private.ID
	}
	return participants, privateIDs, true
}

func BuildAttemptAssignmentEvidence(
	assignment GoldenAttemptAssignment,
) (GoldenAttemptAssignmentEvidence, error) {
	evidence := GoldenAttemptAssignmentEvidence{
		ID: assignment.ID, RevisionID: assignment.RevisionID, Revision: assignment.Revision,
		Scope: assignment.Scope, AttemptID: assignment.AttemptID, WaveID: assignment.WaveID,
		MembershipID: assignment.MembershipID, Plan: assignment.Plan, EdgeID: assignment.EdgeID,
		ReservationID: assignment.ReservationID, SnapshotID: assignment.Snapshot.SnapshotID,
		TaskID: assignment.Snapshot.TaskID, ContentDigest: assignment.ContentDigest,
		Private:                append([]GoldenPrivateAssignment(nil), assignment.Private...),
		ExecutionPayloadDigest: assignment.PayloadDigest,
	}
	payload, err := goldenAttemptAssignmentEvidencePayload(evidence)
	if err != nil {
		return GoldenAttemptAssignmentEvidence{}, goldenWaveError("encode assignment evidence")
	}
	evidence.PayloadDigest = sha256.Sum256(payload)
	if err := evidence.Validate(); err != nil {
		return GoldenAttemptAssignmentEvidence{}, err
	}
	return evidence.Snapshot(), nil
}

func goldenAttemptAssignmentEvidencePayload(evidence GoldenAttemptAssignmentEvidence) ([]byte, error) {
	return Encode(struct {
		ID                     uuid.UUID
		RevisionID             uuid.UUID
		Revision               int64
		Scope                  GoldenStateScope
		AttemptID              uuid.UUID
		WaveID                 uuid.UUID
		MembershipID           uuid.UUID
		Plan                   GoldenPlanStateBinding
		EdgeID                 uuid.UUID
		ReservationID          uuid.UUID
		SnapshotID             uuid.UUID
		TaskID                 uuid.UUID
		ContentDigest          [sha256.Size]byte
		Private                []GoldenPrivateAssignment
		ExecutionPayloadDigest [sha256.Size]byte
	}{
		ID: evidence.ID, RevisionID: evidence.RevisionID, Revision: evidence.Revision,
		Scope: evidence.Scope, AttemptID: evidence.AttemptID, WaveID: evidence.WaveID,
		MembershipID: evidence.MembershipID, Plan: evidence.Plan, EdgeID: evidence.EdgeID,
		ReservationID: evidence.ReservationID, SnapshotID: evidence.SnapshotID,
		TaskID: evidence.TaskID, ContentDigest: evidence.ContentDigest, Private: evidence.Private,
		ExecutionPayloadDigest: evidence.ExecutionPayloadDigest,
	})
}

type GoldenWaveExecutionExpectation struct {
	Scope                GoldenStateScope
	Source               GoldenStateExpectation
	RevisionID           uuid.UUID
	Revision             int64
	PayloadDigest        [sha256.Size]byte
	AttemptID            uuid.UUID
	WaveID               uuid.UUID
	WaveRevisionID       domain.WaveRevisionID
	Window               GoldenReadyWindowExpectation
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

func IdentitySetFromExpectation(expectation GoldenWaveExecutionExpectation) map[uuid.UUID]struct{} {
	values := []uuid.UUID{
		expectation.RevisionID, expectation.AttemptID, expectation.WaveID, expectation.WaveRevisionID.UUID(),
		expectation.Window.WindowID, expectation.Window.RevisionID, expectation.Window.ReadinessRevisionID,
		expectation.Window.PresenceRevisionID, expectation.MembershipID, expectation.MembershipRevisionID,
		expectation.AssignmentID, expectation.AssignmentRevisionID,
	}
	result := make(map[uuid.UUID]struct{}, len(values))
	for _, value := range values {
		if value != uuid.Nil {
			result[value] = struct{}{}
		}
	}
	return result
}

type GoldenWaveCommandReceipt struct {
	CommandID         uuid.UUID
	Scope             GoldenStateScope
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
	Scope              GoldenStateScope
	Source             GoldenStateExpectation
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
	Window                          GoldenReadyWindow
	OpenedAt                        time.Time
	Deadline                        time.Time
	Receipts                        []GoldenWaveCommandReceipt
	ReceiptsDigest                  [sha256.Size]byte
	Start                           *GoldenStartRecord
	PayloadDigest                   [sha256.Size]byte
}

type GoldenWaveAuthorityCondition struct {
	Identity      authoritydomain.Identity
	LeaseRevision int64
	LeaseDigest   [sha256.Size]byte
}

func (c GoldenWaveAuthorityCondition) Validate() error {
	if c.Identity.Validate() != nil || c.LeaseRevision < 1 || c.LeaseDigest == [sha256.Size]byte{} {
		return goldenWaveError("invalid execution authority condition")
	}
	return nil
}

type GoldenWaveExecutionCommit struct {
	ExpectedState     GoldenStateExpectation
	ExpectedExecution *GoldenWaveExecutionExpectation
	Authority         *GoldenWaveAuthorityCondition
	NewIdentityIDs    []uuid.UUID
	Next              GoldenWaveExecution
}

type OpenGoldenReadyWindowCommand struct {
	Scope         GoldenStateScope
	CommandID     uuid.UUID
	ExpectedState GoldenStateExpectation

	AttemptID            uuid.UUID
	WaveID               uuid.UUID
	WaveRevisionID       domain.WaveRevisionID
	WindowID             uuid.UUID
	WaveWindowRevisionID domain.ReadyWindowRevisionID
	WindowRevisionID     uuid.UUID
	ReadinessRevisionID  uuid.UUID
	PresenceRevisionID   uuid.UUID
	MembershipID         uuid.UUID
	MembershipRevisionID uuid.UUID
	AssignmentID         uuid.UUID
	AssignmentRevisionID uuid.UUID
	ExecutionRevisionID  uuid.UUID
	PrivateAssignments   []GoldenPrivateAssignmentCommand
}

func (e GoldenWaveExecution) Expectation() GoldenWaveExecutionExpectation {
	return GoldenWaveExecutionExpectation{
		Scope: e.Scope, Source: CloneExpectation(e.Source), RevisionID: e.RevisionID,
		Revision: e.Revision, PayloadDigest: e.PayloadDigest, AttemptID: e.Attempt.ID,
		WaveID: e.Wave.ID, WaveRevisionID: e.Wave.RevisionID, Window: e.Window.Expectation(),
		MembershipID: e.Membership.ID, MembershipRevisionID: e.Membership.RevisionID,
		MembershipRevision: e.Membership.Revision, MembershipDigest: e.Membership.PayloadDigest,
		AssignmentID: e.Assignment.ID, AssignmentRevisionID: e.Assignment.RevisionID,
		AssignmentRevision: e.Assignment.Revision, AssignmentDigest: e.Assignment.PayloadDigest,
		Started: e.Start != nil,
	}
}

func (e GoldenWaveExecution) Snapshot() GoldenWaveExecution {
	clone := e
	clone.Source = CloneExpectation(e.Source)
	clone.PreviousRevisionID = waveCloneUUIDPointer(e.PreviousRevisionID)
	clone.Group = CloneGroup(e.Group)
	clone.Attempt = cloneGoldenAttempt(e.Attempt)
	clone.Wave = CloneExecution(e.Wave)
	clone.Membership = CloneMembershipBinding(e.Membership)
	clone.Assignment = CloneAttemptAssignment(e.Assignment)
	clone.Window = CloneReadyWindow(e.Window)
	clone.Receipts = cloneGoldenWaveReceipts(e.Receipts)
	clone.Start = cloneGoldenStartRecord(e.Start)
	return clone
}

func RetainedIdentitySet(execution GoldenWaveExecution) map[uuid.UUID]struct{} {
	values := []uuid.UUID{
		execution.Scope.TournamentID, execution.Scope.GroupID, execution.Scope.GroupRevisionID.UUID(),
		execution.Source.RevisionID, execution.Source.Membership.RevisionID,
		execution.Source.Plan.PlanID, execution.Source.Plan.RevisionID,
		execution.Source.SourceProjectionRevisionID.UUID(),
		execution.RevisionID, execution.Attempt.ID, execution.Wave.ID, execution.Wave.RevisionID.UUID(),
		execution.Window.ID, execution.Wave.ReadyWindow.RevisionID.UUID(), execution.Window.RevisionID,
		execution.Window.ReadinessRevisionID, execution.Window.PresenceRevisionID,
		execution.Membership.ID, execution.Membership.RevisionID,
		execution.Assignment.ID, execution.Assignment.RevisionID, execution.Assignment.EdgeID,
		execution.Assignment.ReservationID, execution.Assignment.Snapshot.SnapshotID,
		execution.Assignment.Snapshot.TaskID,
	}
	if execution.PreviousRevisionID != nil {
		values = append(values, *execution.PreviousRevisionID)
	}
	if execution.Source.Membership.PreviousRevisionID != nil {
		values = append(values, *execution.Source.Membership.PreviousRevisionID)
	}
	for _, member := range execution.Group.Members {
		values = append(values, member.ParticipantID)
	}
	for _, attempt := range execution.Group.Attempts {
		values = append(values, attempt.ID)
		values = append(values, attempt.ParticipantIDs...)
	}
	for _, private := range execution.Assignment.Private {
		values = append(values, private.ID)
	}
	for _, receipt := range execution.Receipts {
		values = append(values, receipt.CommandID, receipt.Result.RevisionID,
			receipt.Result.Window.RevisionID, receipt.Result.Window.ReadinessRevisionID,
			receipt.Result.Window.PresenceRevisionID)
		values = append(values, receipt.UnusedIdentityIDs...)
		if receipt.Expected != nil {
			values = append(values, receipt.Expected.RevisionID,
				receipt.Expected.Window.RevisionID, receipt.Expected.Window.ReadinessRevisionID,
				receipt.Expected.Window.PresenceRevisionID)
		}
	}
	if execution.Start != nil {
		values = append(values,
			execution.Start.CommandID, execution.Start.ResultExecutionRevisionID,
			execution.Start.ResultWindowRevisionID, execution.Start.Authority.Identity.HolderID,
			execution.Start.Authority.Identity.LeaseID,
		)
		for _, assignment := range execution.Start.Assignments {
			values = append(values, assignment.AssignmentID, assignment.ParticipantID, assignment.SnapshotID)
		}
	}
	result := make(map[uuid.UUID]struct{}, len(values))
	for _, value := range values {
		if value != uuid.Nil {
			result[value] = struct{}{}
		}
	}
	return result
}
