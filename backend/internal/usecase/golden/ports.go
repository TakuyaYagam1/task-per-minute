package golden

import (
	"context"
	"crypto/sha256"
	"errors"
	"reflect"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
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
	ErrGoldenParticipationAuthorityConflict = errors.New("golden participation authority conflict")
	ErrGoldenParticipationConflict          = errors.New("golden participation commit conflict")
	ErrGoldenCommandReuse                   = errors.New("golden command identifier was reused")
	ErrGoldenRevisionOverflow               = errors.New("golden revision overflow")
	ErrGoldenParticipantExcluded            = errors.New("golden participant is excluded")
	ErrGoldenReadyWindowClosed              = errors.New("golden ready window is closed")
)

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

// StateRepository owns the group-level compare-and-set.
type StateRepository interface {
	LoadGoldenState(ctx context.Context, scope GoldenStateScope) (GoldenState, error)
	CommitGoldenState(ctx context.Context, commit GoldenStateCommit) (*GoldenState, bool, error)
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
	ErrGoldenReadyWindowIneligible = errors.New("golden ready window is ineligible")
	ErrGoldenWaveAuthorityConflict = errors.New("golden Wave authority conflict")
	ErrGoldenWaveCommitConflict    = errors.New("golden Wave commit conflict")
	ErrGoldenWaveCommandReuse      = errors.New("golden Wave command identifier was reused")
	ErrGoldenWaveIdentityConflict  = errors.New("golden Wave identity is already reserved")
	ErrGoldenWaveRevisionOverflow  = errors.New("golden Wave revision overflow")
	ErrGoldenWaveExecutionNotFound = errors.New("golden Wave execution was not found")
	ErrGoldenWaveAuthorityNotLive  = errors.New("golden Wave execution authority is not live")
)

type GoldenPrivateAssignmentCommand struct {
	ParticipantID uuid.UUID
	AssignmentID  uuid.UUID
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
