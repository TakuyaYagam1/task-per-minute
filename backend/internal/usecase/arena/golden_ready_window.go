package arena

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/taskexec"
)

const (
	goldenReadyWindowDuration = 30 * time.Second
	goldenWaveCommitAttempts  = 2
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
	Snapshot      domain.ArenaTaskSnapshot
	ContentDigest [sha256.Size]byte
	Private       []GoldenPrivateAssignment
	PayloadDigest [sha256.Size]byte
}

type GoldenWaveExecutionExpectation struct {
	Scope                GoldenStateScope
	Source               GoldenStateExpectation
	RevisionID           uuid.UUID
	Revision             int64
	PayloadDigest        [sha256.Size]byte
	AttemptID            uuid.UUID
	WaveID               uuid.UUID
	WaveRevisionID       domain.ArenaWaveRevisionID
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
	// Group is opening evidence. Members, exclusions, topology and prior attempts
	// stay frozen. Only the current Attempt and ParticipationEstablished may
	// advance as derived execution evidence during the atomic start.
	Group                           domain.ArenaGoldenGroupState
	GroupBindingDigest              [sha256.Size]byte
	OpeningParticipationEstablished bool
	Attempt                         domain.ArenaGoldenAttempt
	Wave                            domain.ArenaWave
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
	Identity      ExecutionAuthorityIdentity
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

// GoldenWaveRepository owns the execution-only transaction. Commit must lock
// and compare ExpectedState plus ExpectedExecution before it stores Next and
// its command receipt. The source Golden state is read-only through this port.
// When Authority is set, the transaction must use authoritative transaction
// time to prove the exact lease identity, epoch, revision and digest are live.
// The transaction compares both expectations first, returning domain.ErrConflict
// on drift, then unique-reserves NewIdentityIDs across current and archived
// Golden executions. It must persist no part when either proof fails.
type GoldenWaveRepository interface {
	LoadGoldenState(ctx context.Context, scope GoldenStateScope) (GoldenState, error)
	FindGoldenWaveCommand(
		ctx context.Context,
		tournamentID uuid.UUID,
		commandID uuid.UUID,
	) (*GoldenWaveCommandReplay, error)
	// LoadGoldenWaveExecution returns only the current, non-archived execution.
	// A retry after Cancelled/Void may open only after the prior execution was
	// archived while its command receipts remain globally findable.
	LoadGoldenWaveExecution(ctx context.Context, scope GoldenStateScope) (*GoldenWaveExecution, error)
	CommitGoldenWaveExecution(
		ctx context.Context,
		commit GoldenWaveExecutionCommit,
	) (*GoldenWaveExecution, bool, error)
}

type OpenGoldenReadyWindowCommand struct {
	Scope         GoldenStateScope
	CommandID     uuid.UUID
	ExpectedState GoldenStateExpectation

	AttemptID            uuid.UUID
	WaveID               uuid.UUID
	WaveRevisionID       domain.ArenaWaveRevisionID
	WindowID             uuid.UUID
	WaveWindowRevisionID domain.ArenaReadyWindowRevisionID
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

type GoldenReadyWindowUseCase struct {
	repository GoldenWaveRepository
	clock      Clock
}

func NewGoldenReadyWindowUseCase(repository GoldenWaveRepository, clock Clock) *GoldenReadyWindowUseCase {
	return &GoldenReadyWindowUseCase{repository: repository, clock: clock}
}

func (u *GoldenReadyWindowUseCase) Open(
	ctx context.Context,
	command OpenGoldenReadyWindowCommand,
) (*GoldenWaveExecution, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil {
		return nil, false, domain.ErrValidation
	}
	command.PrivateAssignments = append([]GoldenPrivateAssignmentCommand(nil), command.PrivateAssignments...)
	if err := validateOpenGoldenReadyWindowCommand(command); err != nil {
		return nil, false, err
	}
	if replay, found, err := findGoldenWaveReplay(ctx, u.repository, command.Scope, command.CommandID); err != nil {
		return nil, false, err
	} else if found {
		return reconcileGoldenWaveReplay(command.Scope, command.CommandID,
			GoldenWaveCommandOpened, goldenOpenCommandDigest(command), replay)
	}
	openedAt := u.clock.Now().Round(0).UTC()
	if !validArenaServerTime(openedAt) {
		return nil, false, domain.ErrValidation
	}
	for range goldenWaveCommitAttempts {
		execution, changed, retry, err := u.openAttempt(ctx, command, openedAt)
		if retry {
			continue
		}
		return execution, changed, err
	}
	return nil, false, ErrGoldenWaveCommitConflict
}

func (u *GoldenReadyWindowUseCase) openAttempt(
	ctx context.Context,
	command OpenGoldenReadyWindowCommand,
	openedAt time.Time,
) (*GoldenWaveExecution, bool, bool, error) {
	digest := goldenOpenCommandDigest(command)
	current, err := u.repository.LoadGoldenWaveExecution(ctx, command.Scope)
	if err != nil {
		return nil, false, false, fmt.Errorf("GoldenReadyWindowUseCase - load execution: %w", err)
	}
	if replay, found, findErr := findGoldenWaveReplay(
		ctx, u.repository, command.Scope, command.CommandID,
	); findErr != nil {
		return nil, false, false, findErr
	} else if found {
		result, changed, replayErr := reconcileGoldenWaveReplay(
			command.Scope, command.CommandID, GoldenWaveCommandOpened, digest, replay,
		)
		return result, changed, false, replayErr
	}
	if current != nil {
		if current.Validate() != nil {
			return nil, false, false, domain.ErrInternal
		}
		if receipt, found := goldenExecutionReceiptByCommand(*current, command.CommandID); found {
			if receipt.Kind != GoldenWaveCommandOpened || receipt.CommandDigest != digest {
				return nil, false, false, ErrGoldenWaveCommandReuse
			}
			clone := current.Snapshot()
			return &clone, false, false, nil
		}
		return nil, false, false, ErrGoldenWaveAuthorityConflict
	}
	state, err := u.repository.LoadGoldenState(ctx, command.Scope)
	if err != nil {
		return nil, false, false, fmt.Errorf("GoldenReadyWindowUseCase - load state: %w", err)
	}
	if state.Validate() != nil {
		return nil, false, false, domain.ErrInternal
	}
	if !state.Expectation().Equal(command.ExpectedState) {
		return nil, false, false, ErrGoldenWaveAuthorityConflict
	}
	execution, err := buildGoldenWaveExecution(state, command, openedAt)
	if err != nil {
		return nil, false, false, err
	}
	identities := goldenOpenCommandIdentityIDs(command)
	canonicalGoldenIDs(identities)
	commit := GoldenWaveExecutionCommit{
		ExpectedState: command.ExpectedState, NewIdentityIDs: identities, Next: execution,
	}
	return commitGoldenWaveExecution(ctx, u.repository, commit, command.CommandID,
		GoldenWaveCommandOpened, digest)
}

func buildGoldenWaveExecution(
	state GoldenState,
	command OpenGoldenReadyWindowCommand,
	openedAt time.Time,
) (GoldenWaveExecution, error) {
	active := state.ActiveParticipantIDs()
	canonicalGoldenIDs(active)
	if len(active) < 2 || state.Allocation != nil || goldenStateHasUnresolvedAttempt(state) {
		return GoldenWaveExecution{}, ErrGoldenReadyWindowIneligible
	}
	if err := validateGoldenOpenIdentityOwnership(state, command); err != nil {
		return GoldenWaveExecution{}, err
	}
	groupPlan, edge, found := goldenAttemptPlanEdge(state, state.Scope)
	if !found {
		return GoldenWaveExecution{}, ErrGoldenReadyWindowIneligible
	}
	planned := append([]uuid.UUID(nil), groupPlan.ParticipantIDs...)
	canonicalGoldenIDs(planned)
	if !goldenIDsSubset(active, planned) {
		return GoldenWaveExecution{}, ErrGoldenReadyWindowIneligible
	}
	private, err := buildGoldenPrivateAssignments(command.PrivateAssignments, active, edge)
	if err != nil {
		return GoldenWaveExecution{}, err
	}
	deadline := openedAt.Add(goldenReadyWindowDuration)
	attempt := buildGoldenAttempt(state.Group, command.AttemptID, active)
	group := cloneGoldenStateGroup(state.Group)
	group.Attempts = append(group.Attempts, attempt)
	if _, err := domain.NewArenaGoldenGroup(group); err != nil {
		return GoldenWaveExecution{}, goldenWaveError("group attempt: %v", err)
	}
	groupBindingDigest, err := goldenExecutionGroupBindingDigest(group, group.ParticipationEstablished)
	if err != nil {
		return GoldenWaveExecution{}, goldenWaveError("encode group binding: %v", err)
	}
	wave := domain.ArenaWave{
		ID: command.WaveID, TournamentID: state.Scope.TournamentID,
		RevisionID: command.WaveRevisionID, State: domain.ArenaWaveStatePlanned,
		Members: goldenWaveMembers(active),
	}
	if err := wave.OpenReadyWindow(command.WindowID, command.WaveWindowRevisionID, openedAt, deadline); err != nil {
		return GoldenWaveExecution{}, goldenWaveError("open Wave: %v", err)
	}
	membership := GoldenWaveMembershipBinding{
		ID: command.MembershipID, RevisionID: command.MembershipRevisionID, Revision: 1,
		Source: state.Membership, ParticipantIDs: append([]uuid.UUID(nil), active...),
		PayloadDigest: goldenParticipantSetDigest(active),
	}
	assignment := GoldenAttemptAssignment{
		ID: command.AssignmentID, RevisionID: command.AssignmentRevisionID, Revision: 1,
		Scope: state.Scope, AttemptID: command.AttemptID, WaveID: command.WaveID,
		MembershipID: command.MembershipID, Plan: state.Plan, EdgeID: edge.ID,
		ReservationID: edge.ReservationID, Snapshot: cloneTaskSnapshot(edge.Snapshot),
		ContentDigest: edge.ContentDigest, Private: private,
	}
	assignment.PayloadDigest = goldenAssignmentDigest(assignment)
	window := GoldenReadyWindow{
		ID: command.WindowID, RevisionID: command.WindowRevisionID, Revision: 1,
		AttemptID: command.AttemptID, AttemptNo: attempt.AttemptNo,
		OpenedAt: openedAt, Deadline: deadline, State: GoldenReadyWindowOpen,
		ReadinessRevisionID: command.ReadinessRevisionID, ReadinessRevision: 1,
		PresenceRevisionID: command.PresenceRevisionID, PresenceRevision: 1,
		BasePresentParticipantIDs: append([]uuid.UUID(nil), active...),
		PresentParticipantIDs:     append([]uuid.UUID(nil), active...),
	}
	window.ReadinessDigest = goldenParticipantSetDigest(nil)
	window.PresenceDigest = goldenParticipantSetDigest(active)
	execution := GoldenWaveExecution{
		Scope: state.Scope, Source: state.Expectation(), RevisionID: command.ExecutionRevisionID,
		Revision: 1, Group: group, GroupBindingDigest: groupBindingDigest,
		OpeningParticipationEstablished: group.ParticipationEstablished,
		Attempt:                         attempt, Wave: wave, Membership: membership,
		Assignment: assignment, Window: window, OpenedAt: openedAt, Deadline: deadline,
	}
	receipt := GoldenWaveCommandReceipt{
		CommandID: command.CommandID, Scope: command.Scope, Kind: GoldenWaveCommandOpened,
		CommandDigest: goldenOpenCommandDigest(command), Result: execution.Expectation(), OccurredAt: openedAt,
	}
	execution.Receipts = []GoldenWaveCommandReceipt{receipt}
	if err := sealGoldenWaveExecution(&execution); err != nil {
		return GoldenWaveExecution{}, err
	}
	if err := validateGoldenExecutionSource(state, execution); err != nil {
		return GoldenWaveExecution{}, err
	}
	if err := execution.Validate(); err != nil {
		return GoldenWaveExecution{}, err
	}
	return execution.Snapshot(), nil
}

func rebuildGoldenWaveExecution(execution *GoldenWaveExecution) error {
	if execution == nil {
		return goldenWaveError("nil execution")
	}
	receiptsDigest, err := goldenWaveReceiptsDigest(execution.Receipts)
	if err != nil {
		return goldenWaveError("encode command receipts: %v", err)
	}
	execution.ReceiptsDigest = receiptsDigest
	execution.PayloadDigest = goldenWavePayloadDigest(*execution)
	return nil
}

func sealGoldenWaveExecution(execution *GoldenWaveExecution) error {
	if execution == nil || len(execution.Receipts) == 0 {
		return goldenWaveError("cannot seal execution without receipt")
	}
	last := len(execution.Receipts) - 1
	execution.Receipts[last].Result.PayloadDigest = [sha256.Size]byte{}
	if err := rebuildGoldenWaveExecution(execution); err != nil {
		return err
	}
	execution.Receipts[last].Result = execution.Expectation()
	return nil
}

func (e GoldenWaveExecution) Expectation() GoldenWaveExecutionExpectation {
	return GoldenWaveExecutionExpectation{
		Scope: e.Scope, Source: cloneGoldenStateExpectation(e.Source), RevisionID: e.RevisionID,
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
	clone.Source = cloneGoldenStateExpectation(e.Source)
	clone.PreviousRevisionID = cloneGoldenUUID(e.PreviousRevisionID)
	clone.Group = cloneGoldenStateGroup(e.Group)
	clone.Attempt = cloneGoldenAttempt(e.Attempt)
	clone.Wave = cloneArenaWaveExecution(e.Wave)
	clone.Membership = cloneGoldenMembershipBinding(e.Membership)
	clone.Assignment = cloneGoldenAttemptAssignment(e.Assignment)
	clone.Window = cloneGoldenWindow(e.Window)
	clone.Receipts = cloneGoldenWaveReceipts(e.Receipts)
	clone.Start = cloneGoldenStartRecord(e.Start)
	return clone
}

func (e GoldenWaveExecution) Validate() error {
	if err := validateGoldenWaveExecutionIdentity(e); err != nil {
		return err
	}
	if err := validateGoldenWaveExecutionBindings(e); err != nil {
		return err
	}
	if err := validateGoldenWaveReceiptChain(e); err != nil {
		return err
	}
	receiptsDigest, err := goldenWaveReceiptsDigest(e.Receipts)
	if err != nil || e.ReceiptsDigest == [sha256.Size]byte{} || e.ReceiptsDigest != receiptsDigest ||
		goldenWavePayloadDigest(e) != e.PayloadDigest {
		return goldenWaveError("execution payload digest changed")
	}
	return nil
}

func validateGoldenWaveExecutionIdentity(e GoldenWaveExecution) error {
	if !validGoldenStateScope(e.Scope) || e.Source.Scope != e.Scope || e.Source.PayloadDigest == [sha256.Size]byte{} ||
		e.RevisionID == uuid.Nil || e.Revision < 1 || !validGoldenRevisionPredecessor(
		e.RevisionID, e.Revision, e.PreviousRevisionID,
	) || !validArenaServerTime(e.OpenedAt) || !validArenaServerTime(e.Deadline) ||
		!e.Deadline.Equal(e.OpenedAt.Add(goldenReadyWindowDuration)) {
		return goldenWaveError("invalid execution identity, source or interval")
	}
	return nil
}

func validateGoldenWaveExecutionBindings(e GoldenWaveExecution) error {
	if err := validateGoldenRetainedGroup(e); err != nil {
		return err
	}
	if err := validateGoldenRetainedWave(e); err != nil {
		return err
	}
	if err := validateGoldenExecutionWindow(e); err != nil {
		return err
	}
	if err := validateGoldenMembershipBinding(e); err != nil {
		return err
	}
	if err := validateGoldenAttemptAssignment(e); err != nil {
		return err
	}
	if err := validateGoldenStartRecord(e); err != nil {
		return err
	}
	if err := validateGoldenExactExecutionCoverage(e); err != nil {
		return err
	}
	return validateGoldenExecutionIdentityRoles(e)
}

func validateGoldenRetainedGroup(e GoldenWaveExecution) error {
	if _, err := domain.NewArenaGoldenGroup(e.Group); err != nil || e.Attempt.Validate() != nil {
		return goldenWaveError("invalid retained group or attempt")
	}
	if e.Group.ID != e.Scope.GroupID || e.Group.RevisionID != e.Scope.GroupRevisionID ||
		e.Attempt.ID == uuid.Nil || len(e.Group.Attempts) == 0 ||
		!reflect.DeepEqual(e.Group.Attempts[len(e.Group.Attempts)-1], e.Attempt) {
		return goldenWaveError("group does not retain the current attempt")
	}
	return validateGoldenGroupBindingEvidence(e)
}

func validateGoldenGroupBindingEvidence(e GoldenWaveExecution) error {
	groupBindingDigest, err := goldenExecutionGroupBindingDigest(e.Group, e.OpeningParticipationEstablished)
	if err != nil || e.GroupBindingDigest == [sha256.Size]byte{} || groupBindingDigest != e.GroupBindingDigest {
		return goldenWaveError("immutable group binding changed")
	}
	if (e.Start == nil && e.Group.ParticipationEstablished != e.OpeningParticipationEstablished) ||
		(e.Start != nil && !e.Group.ParticipationEstablished) {
		return goldenWaveError("group participation evidence changed outside start")
	}
	return nil
}

func validateGoldenRetainedWave(e GoldenWaveExecution) error {
	if err := e.Wave.Validate(); err != nil || e.Wave.ID == uuid.Nil || e.Wave.TournamentID != e.Scope.TournamentID ||
		e.Wave.ReadyWindow == nil || e.Wave.ReadyWindow.ID != e.Window.ID ||
		!e.Wave.ReadyWindow.OpenedAt.Equal(e.OpenedAt) || !e.Wave.ReadyWindow.Deadline.Equal(e.Deadline) {
		return goldenWaveError("invalid retained Wave")
	}
	return nil
}

func validateGoldenExactExecutionCoverage(e GoldenWaveExecution) error {
	active := goldenExecutionActiveIDs(e.Group)
	waveMembers := goldenWaveMemberIDs(e.Wave)
	if !equalGoldenIDs(active, e.Attempt.ParticipantIDs) ||
		!equalGoldenIDs(active, e.Membership.ParticipantIDs) ||
		!equalGoldenIDs(active, waveMembers) || !equalGoldenIDs(active, goldenPrivateAssignmentIDs(e.Assignment.Private)) ||
		!equalGoldenIDs(active, e.Window.BasePresentParticipantIDs) {
		return goldenWaveError("execution does not cover the exact active membership")
	}
	return nil
}

func validateGoldenExecutionWindow(e GoldenWaveExecution) error {
	w := e.Window
	if err := validateGoldenExecutionWindowIdentity(e, w); err != nil {
		return err
	}
	if err := validateGoldenWindowParticipantSets(w); err != nil {
		return err
	}
	waveReady := goldenReadyWaveMemberIDs(e.Wave)
	if !equalGoldenIDs(waveReady, w.ReadyParticipantIDs) {
		return goldenWaveError("Wave readiness does not match Golden readiness")
	}
	return validateGoldenWindowState(e, w)
}

func validateGoldenExecutionWindowIdentity(e GoldenWaveExecution, w GoldenReadyWindow) error {
	if w.ID == uuid.Nil || w.RevisionID == uuid.Nil || w.Revision < 1 ||
		w.AttemptID != e.Attempt.ID || w.AttemptNo != e.Attempt.AttemptNo ||
		!w.OpenedAt.Equal(e.OpenedAt) || !w.Deadline.Equal(e.Deadline) ||
		w.ReadinessRevisionID == uuid.Nil || w.ReadinessRevision < 1 ||
		w.PresenceRevisionID == uuid.Nil || w.PresenceRevision < 1 {
		return goldenWaveError("invalid ready-window identity")
	}
	if !validGoldenRevisionPredecessor(w.RevisionID, w.Revision, w.PreviousRevisionID) ||
		!validGoldenRevisionPredecessor(w.ReadinessRevisionID, w.ReadinessRevision, w.ReadinessPreviousRevisionID) ||
		!validGoldenRevisionPredecessor(w.PresenceRevisionID, w.PresenceRevision, w.PresencePreviousRevisionID) {
		return goldenWaveError("invalid ready-window revision lineage")
	}
	return nil
}

func validateGoldenWindowParticipantSets(w GoldenReadyWindow) error {
	if !goldenIDsAreCanonical(w.BasePresentParticipantIDs) || !goldenIDsAreCanonical(w.ReadyParticipantIDs) ||
		!goldenIDsAreCanonical(w.PresentParticipantIDs) || !goldenIDsSubset(w.ReadyParticipantIDs, w.PresentParticipantIDs) ||
		!goldenIDsSubset(w.PresentParticipantIDs, w.BasePresentParticipantIDs) ||
		w.ReadinessDigest != goldenParticipantSetDigest(w.ReadyParticipantIDs) ||
		w.PresenceDigest != goldenParticipantSetDigest(w.PresentParticipantIDs) {
		return goldenWaveError("invalid ready-window participant binding")
	}
	return nil
}

func goldenReadyWaveMemberIDs(wave domain.ArenaWave) []uuid.UUID {
	ready := make([]uuid.UUID, 0, len(wave.Members))
	for _, member := range wave.Members {
		if member.Ready {
			ready = append(ready, member.ParticipantID)
		}
	}
	canonicalGoldenIDs(ready)
	return ready
}

func validateGoldenWindowState(e GoldenWaveExecution, w GoldenReadyWindow) error {
	if w.State != GoldenReadyWindowOpen && w.State != GoldenReadyWindowConsumed {
		return goldenWaveError("invalid ready-window state")
	}
	switch w.State {
	case GoldenReadyWindowOpen:
		return validateGoldenOpenWindowState(e, w)
	case GoldenReadyWindowConsumed:
		return validateGoldenConsumedWindowState(e)
	case GoldenReadyWindowExpired:
		return goldenWaveError("expired ready window cannot back a Wave execution")
	default:
		return goldenWaveError("invalid ready-window state")
	}
}

func validateGoldenOpenWindowState(e GoldenWaveExecution, w GoldenReadyWindow) error {
	if e.Wave.ReadyWindow.State != domain.ArenaReadyWindowStateOpen ||
		(e.Wave.State != domain.ArenaWaveStateReadyWindowOpen && e.Wave.State != domain.ArenaWaveStateReady) ||
		e.Attempt.State != domain.ArenaGoldenAttemptStateWaitingReady || e.Start != nil {
		return goldenWaveError("open Wave, window and attempt states diverged")
	}
	allReady := len(w.ReadyParticipantIDs) == len(w.BasePresentParticipantIDs)
	if (e.Wave.State == domain.ArenaWaveStateReady) != allReady {
		return goldenWaveError("Wave ready state does not match exact membership")
	}
	return nil
}

func validateGoldenConsumedWindowState(e GoldenWaveExecution) error {
	if e.Wave.ReadyWindow.State != domain.ArenaReadyWindowStateConsumed ||
		e.Wave.State != domain.ArenaWaveStateActive || e.Attempt.State != domain.ArenaGoldenAttemptStateActive ||
		e.Start == nil {
		return goldenWaveError("started Wave, window and attempt states diverged")
	}
	return nil
}

func validateGoldenMembershipBinding(e GoldenWaveExecution) error {
	m := e.Membership
	if m.ID == uuid.Nil || m.RevisionID == uuid.Nil || m.Revision != 1 ||
		!goldenMembershipRevisionsEqual(m.Source, e.Source.Membership) ||
		!goldenIDsAreCanonical(m.ParticipantIDs) || m.PayloadDigest != goldenParticipantSetDigest(m.ParticipantIDs) {
		return goldenWaveError("invalid exact membership binding")
	}
	return nil
}

func validateGoldenAttemptAssignment(e GoldenWaveExecution) error {
	a := e.Assignment
	if err := validateGoldenAssignmentIdentity(e, a); err != nil {
		return err
	}
	if err := validateGoldenAssignmentArtifact(a); err != nil {
		return err
	}
	return validateGoldenPrivateAssignments(a)
}

func validateGoldenAssignmentIdentity(e GoldenWaveExecution, a GoldenAttemptAssignment) error {
	if a.ID == uuid.Nil || a.RevisionID == uuid.Nil || a.Revision != 1 || a.Scope != e.Scope ||
		a.AttemptID != e.Attempt.ID || a.WaveID != e.Wave.ID || a.MembershipID != e.Membership.ID ||
		a.Plan != e.Source.Plan || a.EdgeID == uuid.Nil || a.ReservationID == uuid.Nil {
		return goldenWaveError("invalid immutable assignment identity")
	}
	return nil
}

func validateGoldenAssignmentArtifact(a GoldenAttemptAssignment) error {
	digest, digestErr := taskexec.SnapshotDigest(a.Snapshot)
	if a.Snapshot.Validate() != nil || digestErr != nil || digest != a.ContentDigest ||
		a.ContentDigest == [sha256.Size]byte{} ||
		a.Snapshot.SnapshotID == uuid.Nil || a.PayloadDigest != goldenAssignmentDigest(a) || len(a.Private) < 2 {
		return goldenWaveError("invalid immutable assignment artifact")
	}
	return nil
}

func validateGoldenPrivateAssignments(a GoldenAttemptAssignment) error {
	seen := make(map[uuid.UUID]struct{}, len(a.Private))
	for _, private := range a.Private {
		if private.ID == uuid.Nil || private.ParticipantID == uuid.Nil || private.SnapshotID != a.Snapshot.SnapshotID ||
			private.ContentDigest != a.ContentDigest {
			return goldenWaveError("invalid private assignment")
		}
		if _, duplicate := seen[private.ID]; duplicate {
			return goldenWaveError("private assignment identity is reused")
		}
		seen[private.ID] = struct{}{}
	}
	return nil
}

func validateGoldenWaveReceiptChain(e GoldenWaveExecution) error {
	if len(e.Receipts) == 0 || len(e.Receipts) != int(e.Revision) {
		return goldenWaveError("receipt count does not match execution revision")
	}
	seen := make(map[uuid.UUID]struct{}, len(e.Receipts))
	var prior GoldenWaveExecutionExpectation
	var previousAt time.Time
	for index, receipt := range e.Receipts {
		if err := validateGoldenReceiptStructure(e, receipt, index, previousAt); err != nil {
			return err
		}
		if _, duplicate := seen[receipt.CommandID]; duplicate {
			return goldenWaveError("duplicate retained command")
		}
		if err := validateGoldenReceiptAuthority(e, receipt, index); err != nil {
			return err
		}
		if err := validateGoldenReceiptSemantics(e, receipt, index); err != nil {
			return err
		}
		if err := validateGoldenReceiptLink(receipt, index, prior); err != nil {
			return err
		}
		prior = receipt.Result
		seen[receipt.CommandID] = struct{}{}
		previousAt = receipt.OccurredAt
	}
	if !prior.Equal(e.Expectation()) {
		return goldenWaveError("final command does not link current execution")
	}
	return nil
}

func validateGoldenReceiptStructure(
	execution GoldenWaveExecution,
	receipt GoldenWaveCommandReceipt,
	index int,
	previousAt time.Time,
) error {
	if receipt.CommandID == uuid.Nil || receipt.Scope != execution.Scope ||
		receipt.CommandDigest == [sha256.Size]byte{} || !validGoldenWaveCommandKind(receipt.Kind) ||
		!validArenaServerTime(receipt.OccurredAt) || receipt.Result.Revision != int64(index+1) {
		return goldenWaveError("invalid retained command receipt")
	}
	if !previousAt.IsZero() && receipt.OccurredAt.Before(previousAt) {
		return goldenWaveError("retained command time moved backwards")
	}
	return nil
}

func validateGoldenReceiptAuthority(
	execution GoldenWaveExecution,
	receipt GoldenWaveCommandReceipt,
	index int,
) error {
	if !goldenIDsAreCanonical(receipt.UnusedIdentityIDs) ||
		!validGoldenReceiptUnusedIdentities(receipt, execution.Window, index == len(execution.Receipts)-1) ||
		!goldenReceiptExpectationAnchored(receipt.Result, execution) {
		return goldenWaveError("invalid retained unused command identities")
	}
	if receipt.Expected != nil && !goldenReceiptExpectationAnchored(*receipt.Expected, execution) {
		return goldenWaveError("invalid retained expected command authority")
	}
	return nil
}

func validateGoldenReceiptSemantics(
	execution GoldenWaveExecution,
	receipt GoldenWaveCommandReceipt,
	index int,
) error {
	switch receipt.Kind {
	case GoldenWaveCommandOpened:
		return validateGoldenOpenedReceipt(execution, receipt, index)
	case GoldenWaveCommandStarted:
		return validateGoldenStartedReceipt(execution, receipt, index)
	case GoldenWaveCommandReady, GoldenWaveCommandDisconnected, GoldenWaveCommandReconnected:
		return validateGoldenReadinessReceipt(execution, receipt)
	default:
		return goldenWaveError("invalid retained command receipt kind")
	}
}

func validateGoldenOpenedReceipt(
	execution GoldenWaveExecution,
	receipt GoldenWaveCommandReceipt,
	index int,
) error {
	if receipt.ParticipantID != uuid.Nil || index != 0 || receipt.Result.Started ||
		!receipt.OccurredAt.Equal(execution.OpenedAt) {
		return goldenWaveError("invalid opened receipt participant")
	}
	return nil
}

func validateGoldenStartedReceipt(
	execution GoldenWaveExecution,
	receipt GoldenWaveCommandReceipt,
	index int,
) error {
	if receipt.ParticipantID != uuid.Nil || execution.Start == nil || index != len(execution.Receipts)-1 ||
		!receipt.OccurredAt.Equal(execution.Start.StartedAt) || receipt.Expected == nil ||
		receipt.Expected.Started || !receipt.Result.Started {
		return goldenWaveError("invalid started receipt")
	}
	return nil
}

func validateGoldenReadinessReceipt(
	execution GoldenWaveExecution,
	receipt GoldenWaveCommandReceipt,
) error {
	if !goldenIDsContain(execution.Membership.ParticipantIDs, receipt.ParticipantID) ||
		receipt.OccurredAt.Before(execution.OpenedAt) || receipt.OccurredAt.After(execution.Deadline) ||
		receipt.Expected == nil || receipt.Expected.Started || receipt.Result.Started {
		return goldenWaveError("invalid readiness receipt participant or time")
	}
	return nil
}

func validateGoldenReceiptLink(
	receipt GoldenWaveCommandReceipt,
	index int,
	prior GoldenWaveExecutionExpectation,
) error {
	if index == 0 {
		if receipt.Kind != GoldenWaveCommandOpened || receipt.Expected != nil {
			return goldenWaveError("first command did not open execution")
		}
		return nil
	}
	if receipt.Expected == nil || !receipt.Expected.Equal(prior) {
		return goldenWaveError("retained command chain is broken")
	}
	return nil
}

func validGoldenReceiptUnusedIdentities(
	receipt GoldenWaveCommandReceipt,
	current GoldenReadyWindow,
	final bool,
) bool {
	switch receipt.Kind {
	case GoldenWaveCommandReady:
		return validGoldenReadyUnusedIdentities(receipt, current, final)
	case GoldenWaveCommandDisconnected:
		return validGoldenDisconnectUnusedIdentities(receipt, current, final)
	case GoldenWaveCommandReconnected:
		return validGoldenReconnectUnusedIdentities(receipt, current, final)
	case GoldenWaveCommandOpened, GoldenWaveCommandStarted:
		return len(receipt.UnusedIdentityIDs) == 0
	default:
		return false
	}
}

type goldenReceiptWindowTransition struct {
	expected          GoldenReadyWindowExpectation
	windowSame        bool
	readinessSame     bool
	presenceSame      bool
	windowAdvanced    bool
	readinessAdvanced bool
	presenceAdvanced  bool
}

func goldenReceiptTransition(receipt GoldenWaveCommandReceipt) (goldenReceiptWindowTransition, bool) {
	if receipt.Expected == nil {
		return goldenReceiptWindowTransition{}, false
	}
	expected := receipt.Expected.Window
	result := receipt.Result.Window
	return goldenReceiptWindowTransition{
		expected:          expected,
		windowSame:        goldenWindowRevisionSame(expected, result),
		readinessSame:     goldenReadinessRevisionSame(expected, result),
		presenceSame:      goldenPresenceRevisionSame(expected, result),
		windowAdvanced:    goldenWindowRevisionAdvanced(expected, result),
		readinessAdvanced: goldenReadinessRevisionAdvanced(expected, result),
		presenceAdvanced:  goldenPresenceRevisionAdvanced(expected, result),
	}, true
}

func (t goldenReceiptWindowTransition) allSame() bool {
	return t.windowSame && t.readinessSame && t.presenceSame
}

func validGoldenReadyUnusedIdentities(
	receipt GoldenWaveCommandReceipt,
	current GoldenReadyWindow,
	final bool,
) bool {
	transition, ok := goldenReceiptTransition(receipt)
	if !ok {
		return false
	}
	if transition.allSame() {
		return len(receipt.UnusedIdentityIDs) == 2
	}
	return len(receipt.UnusedIdentityIDs) == 0 && transition.windowAdvanced &&
		transition.readinessAdvanced && transition.presenceSame &&
		goldenReceiptFinalPredecessors(current, transition.expected, final, true, true, false)
}

func validGoldenDisconnectUnusedIdentities(
	receipt GoldenWaveCommandReceipt,
	current GoldenReadyWindow,
	final bool,
) bool {
	transition, ok := goldenReceiptTransition(receipt)
	if !ok {
		return false
	}
	if transition.allSame() {
		return len(receipt.UnusedIdentityIDs) == 3
	}
	return len(receipt.UnusedIdentityIDs) == 0 && transition.windowAdvanced &&
		transition.readinessAdvanced && transition.presenceAdvanced &&
		goldenReceiptFinalPredecessors(current, transition.expected, final, true, true, true)
}

func validGoldenReconnectUnusedIdentities(
	receipt GoldenWaveCommandReceipt,
	current GoldenReadyWindow,
	final bool,
) bool {
	transition, ok := goldenReceiptTransition(receipt)
	if !ok {
		return false
	}
	if transition.allSame() {
		return len(receipt.UnusedIdentityIDs) == 2
	}
	return len(receipt.UnusedIdentityIDs) == 0 && transition.windowAdvanced &&
		transition.readinessSame && transition.presenceAdvanced &&
		goldenReceiptFinalPredecessors(current, transition.expected, final, true, false, true)
}

func goldenReceiptFinalPredecessors(
	current GoldenReadyWindow,
	expected GoldenReadyWindowExpectation,
	final bool,
	windowChanged bool,
	readinessChanged bool,
	presenceChanged bool,
) bool {
	return !final || goldenReceiptCurrentPredecessors(
		current, expected, windowChanged, readinessChanged, presenceChanged,
	)
}

func goldenWindowRevisionSame(expected, result GoldenReadyWindowExpectation) bool {
	return result.Revision == expected.Revision && result.RevisionID == expected.RevisionID
}

func goldenReadinessRevisionSame(expected, result GoldenReadyWindowExpectation) bool {
	return result.ReadinessRevision == expected.ReadinessRevision &&
		result.ReadinessRevisionID == expected.ReadinessRevisionID && result.ReadinessDigest == expected.ReadinessDigest
}

func goldenPresenceRevisionSame(expected, result GoldenReadyWindowExpectation) bool {
	return result.PresenceRevision == expected.PresenceRevision &&
		result.PresenceRevisionID == expected.PresenceRevisionID && result.PresenceDigest == expected.PresenceDigest
}

func goldenWindowRevisionAdvanced(expected, result GoldenReadyWindowExpectation) bool {
	return expected.Revision < math.MaxInt64 && result.Revision == expected.Revision+1 &&
		result.RevisionID != expected.RevisionID
}

func goldenReadinessRevisionAdvanced(expected, result GoldenReadyWindowExpectation) bool {
	return expected.ReadinessRevision < math.MaxInt64 &&
		result.ReadinessRevision == expected.ReadinessRevision+1 &&
		result.ReadinessRevisionID != expected.ReadinessRevisionID && result.ReadinessDigest != expected.ReadinessDigest
}

func goldenPresenceRevisionAdvanced(expected, result GoldenReadyWindowExpectation) bool {
	return expected.PresenceRevision < math.MaxInt64 && result.PresenceRevision == expected.PresenceRevision+1 &&
		result.PresenceRevisionID != expected.PresenceRevisionID && result.PresenceDigest != expected.PresenceDigest
}

func goldenReceiptCurrentPredecessors(
	current GoldenReadyWindow,
	expected GoldenReadyWindowExpectation,
	windowChanged bool,
	readinessChanged bool,
	presenceChanged bool,
) bool {
	return (!windowChanged || current.PreviousRevisionID != nil && *current.PreviousRevisionID == expected.RevisionID) &&
		(!readinessChanged || current.ReadinessPreviousRevisionID != nil &&
			*current.ReadinessPreviousRevisionID == expected.ReadinessRevisionID) &&
		(!presenceChanged || current.PresencePreviousRevisionID != nil &&
			*current.PresencePreviousRevisionID == expected.PresenceRevisionID)
}

func goldenReceiptExpectationAnchored(
	expectation GoldenWaveExecutionExpectation,
	execution GoldenWaveExecution,
) bool {
	return expectation.Scope == execution.Scope && expectation.Source.Equal(execution.Source) &&
		expectation.AttemptID == execution.Attempt.ID && expectation.WaveID == execution.Wave.ID &&
		expectation.WaveRevisionID == execution.Wave.RevisionID && expectation.Window.WindowID == execution.Window.ID &&
		expectation.MembershipID == execution.Membership.ID &&
		expectation.MembershipRevisionID == execution.Membership.RevisionID &&
		expectation.MembershipRevision == execution.Membership.Revision &&
		expectation.MembershipDigest == execution.Membership.PayloadDigest &&
		expectation.AssignmentID == execution.Assignment.ID &&
		expectation.AssignmentRevisionID == execution.Assignment.RevisionID &&
		expectation.AssignmentRevision == execution.Assignment.Revision &&
		expectation.AssignmentDigest == execution.Assignment.PayloadDigest
}

func validGoldenWaveCommandKind(kind GoldenWaveCommandKind) bool {
	switch kind {
	case GoldenWaveCommandOpened, GoldenWaveCommandReady, GoldenWaveCommandDisconnected,
		GoldenWaveCommandReconnected, GoldenWaveCommandStarted:
		return true
	}
	return false
}

func validateOpenGoldenReadyWindowCommand(command OpenGoldenReadyWindowCommand) error {
	if !validGoldenStateScope(command.Scope) || command.ExpectedState.Scope != command.Scope ||
		len(command.PrivateAssignments) == 0 || !validGoldenOpenPrimaryIDs(command) ||
		!validGoldenOpenRevisionIDs(command) {
		return goldenWaveError("invalid open command identity")
	}
	return nil
}

func validGoldenOpenPrimaryIDs(command OpenGoldenReadyWindowCommand) bool {
	return command.CommandID != uuid.Nil && command.AttemptID != uuid.Nil && command.WaveID != uuid.Nil &&
		command.WindowID != uuid.Nil && command.MembershipID != uuid.Nil && command.AssignmentID != uuid.Nil
}

func validGoldenOpenRevisionIDs(command OpenGoldenReadyWindowCommand) bool {
	return !command.WaveRevisionID.IsZero() && !command.WaveWindowRevisionID.IsZero() &&
		command.WindowRevisionID != uuid.Nil && command.ReadinessRevisionID != uuid.Nil &&
		command.PresenceRevisionID != uuid.Nil && command.MembershipRevisionID != uuid.Nil &&
		command.AssignmentRevisionID != uuid.Nil && command.ExecutionRevisionID != uuid.Nil
}

func validateGoldenOpenIdentityOwnership(state GoldenState, command OpenGoldenReadyWindowCommand) error {
	return validateGoldenFreshExecutionIDs(state, goldenOpenCommandIdentityIDs(command)...)
}

func goldenOpenCommandIdentityIDs(command OpenGoldenReadyWindowCommand) []uuid.UUID {
	identities := make([]uuid.UUID, 0, 14+len(command.PrivateAssignments))
	identities = append(identities,
		command.CommandID, command.AttemptID, command.WaveID, command.WaveRevisionID.UUID(), command.WindowID,
		command.WaveWindowRevisionID.UUID(), command.WindowRevisionID, command.ReadinessRevisionID,
		command.PresenceRevisionID, command.MembershipID, command.MembershipRevisionID,
		command.AssignmentID, command.AssignmentRevisionID, command.ExecutionRevisionID,
	)
	for _, private := range command.PrivateAssignments {
		identities = append(identities, private.AssignmentID)
	}
	return identities
}

func validateGoldenFreshExecutionIDs(state GoldenState, identities ...uuid.UUID) error {
	if err := validateGoldenFreshIDs(state, identities...); err != nil {
		return goldenWaveError("identity ownership: %v", err)
	}
	reserved := make(map[uuid.UUID]struct{})
	for _, value := range goldenAuthorityBoundIDs(state.ExactPlan.Authority) {
		if value != uuid.Nil {
			reserved[value] = struct{}{}
		}
	}
	for _, reservation := range state.ExactPlan.Authority.ExistingTaskReservations {
		reserved[reservation.PlanID] = struct{}{}
		reserved[reservation.PlanRevisionID] = struct{}{}
	}
	for _, identity := range identities {
		if _, exists := reserved[identity]; exists {
			return goldenWaveError("identity aliases exact plan authority")
		}
	}
	return nil
}

func goldenStateHasUnresolvedAttempt(state GoldenState) bool {
	if len(state.Group.Attempts) == 0 {
		return false
	}
	for _, attempt := range state.Group.Attempts {
		if attempt.State == domain.ArenaGoldenAttemptStateCompleted {
			return true
		}
	}
	switch state.Group.Attempts[len(state.Group.Attempts)-1].State {
	case domain.ArenaGoldenAttemptStateCancelled, domain.ArenaGoldenAttemptStateVoid:
		return false
	case domain.ArenaGoldenAttemptStatePlanned, domain.ArenaGoldenAttemptStateWaitingReady,
		domain.ArenaGoldenAttemptStateActive, domain.ArenaGoldenAttemptStateCompleted:
		return true
	default:
		return true
	}
}

func buildGoldenAttempt(
	group domain.ArenaGoldenGroupState,
	attemptID uuid.UUID,
	participants []uuid.UUID,
) domain.ArenaGoldenAttempt {
	attempt := domain.ArenaGoldenAttempt{
		ID: attemptID, GroupID: group.ID, GroupRevisionID: group.RevisionID,
		AttemptNo: len(group.Attempts) + 1, State: domain.ArenaGoldenAttemptStateWaitingReady,
		ParticipantIDs: append([]uuid.UUID(nil), participants...),
	}
	if len(group.Attempts) > 0 {
		attempt.PreviousAttemptID = goldenUUID(group.Attempts[len(group.Attempts)-1].ID)
	}
	return attempt
}

func goldenExactGroupPlan(
	plan GoldenExactPlan,
	scope GoldenStateScope,
) (GoldenExactGroupPlan, bool) {
	for _, group := range plan.Groups {
		if group.GroupID == scope.GroupID && group.GroupRevisionID == scope.GroupRevisionID {
			return group, true
		}
	}
	return GoldenExactGroupPlan{}, false
}

func goldenAttemptPlanEdge(
	state GoldenState,
	scope GoldenStateScope,
) (GoldenExactGroupPlan, GoldenExactPlanEdge, bool) {
	group, found := goldenExactGroupPlan(state.ExactPlan, scope)
	if !found {
		return GoldenExactGroupPlan{}, GoldenExactPlanEdge{}, false
	}
	edgeIndex := len(state.Group.Attempts)
	if edgeIndex < 0 || edgeIndex >= len(group.Edges) {
		return GoldenExactGroupPlan{}, GoldenExactPlanEdge{}, false
	}
	return group, group.Edges[edgeIndex], true
}

func validateGoldenExecutionSource(state GoldenState, execution GoldenWaveExecution) error {
	if state.Validate() != nil || execution.Validate() != nil {
		return domain.ErrInternal
	}
	if !state.Expectation().Equal(execution.Source) || state.Scope != execution.Scope {
		return ErrGoldenWaveAuthorityConflict
	}
	if !goldenSourceGroupIdentityEqual(state, execution) || !goldenSourceAttemptsEqual(state, execution) ||
		!goldenSourceActiveSetEqual(state, execution) || !goldenSourceAssignmentEqual(state, execution) {
		return ErrGoldenWaveAuthorityConflict
	}
	return nil
}

func goldenSourceGroupIdentityEqual(state GoldenState, execution GoldenWaveExecution) bool {
	if state.Group.ID != execution.Group.ID || state.Group.TournamentID != execution.Group.TournamentID ||
		state.Group.RevisionID != execution.Group.RevisionID ||
		state.Group.SourceProjectionRevisionID != execution.Group.SourceProjectionRevisionID ||
		state.Group.PositionFrom != execution.Group.PositionFrom || state.Group.PositionTo != execution.Group.PositionTo ||
		state.Group.ParticipationEstablished != execution.Group.ParticipationEstablished ||
		!reflect.DeepEqual(state.Group.Members, execution.Group.Members) ||
		len(execution.Group.Attempts) != len(state.Group.Attempts)+1 {
		return false
	}
	return true
}

func goldenSourceAttemptsEqual(state GoldenState, execution GoldenWaveExecution) bool {
	for index := range state.Group.Attempts {
		if !reflect.DeepEqual(state.Group.Attempts[index], execution.Group.Attempts[index]) {
			return false
		}
	}
	return reflect.DeepEqual(execution.Group.Attempts[len(execution.Group.Attempts)-1], execution.Attempt) &&
		execution.Attempt.AttemptNo == len(state.Group.Attempts)+1
}

func goldenSourceActiveSetEqual(state GoldenState, execution GoldenWaveExecution) bool {
	active := state.ActiveParticipantIDs()
	canonicalGoldenIDs(active)
	return len(active) >= 2 && equalGoldenIDs(active, execution.Membership.ParticipantIDs) &&
		equalGoldenIDs(active, execution.Attempt.ParticipantIDs) &&
		equalGoldenIDs(active, goldenPrivateAssignmentIDs(execution.Assignment.Private))
}

func goldenSourceAssignmentEqual(state GoldenState, execution GoldenWaveExecution) bool {
	_, edge, found := goldenAttemptPlanEdge(state, execution.Scope)
	return found && execution.Assignment.EdgeID == edge.ID &&
		execution.Assignment.ReservationID == edge.ReservationID &&
		execution.Assignment.ContentDigest == edge.ContentDigest &&
		reflect.DeepEqual(execution.Assignment.Snapshot, edge.Snapshot)
}

func buildGoldenPrivateAssignments(
	commands []GoldenPrivateAssignmentCommand,
	active []uuid.UUID,
	edge GoldenExactPlanEdge,
) ([]GoldenPrivateAssignment, error) {
	if len(commands) != len(active) {
		return nil, goldenWaveError("private assignments do not cover active membership")
	}
	byParticipant := make(map[uuid.UUID]uuid.UUID, len(commands))
	for index, command := range commands {
		if command.ParticipantID == uuid.Nil || command.AssignmentID == uuid.Nil {
			return nil, goldenWaveError("invalid private assignment command")
		}
		if command.ParticipantID != active[index] {
			return nil, goldenWaveError("private assignments are not in exact membership order")
		}
		if _, duplicate := byParticipant[command.ParticipantID]; duplicate {
			return nil, goldenWaveError("duplicate private assignment participant")
		}
		byParticipant[command.ParticipantID] = command.AssignmentID
	}
	if len(byParticipant) != len(active) {
		return nil, goldenWaveError("private assignments do not cover active membership")
	}
	result := make([]GoldenPrivateAssignment, len(active))
	for index, participantID := range active {
		assignmentID, found := byParticipant[participantID]
		if !found {
			return nil, goldenWaveError("private assignments do not cover active membership")
		}
		result[index] = GoldenPrivateAssignment{
			ID: assignmentID, ParticipantID: participantID,
			SnapshotID: edge.Snapshot.SnapshotID, ContentDigest: edge.ContentDigest,
		}
	}
	return result, nil
}

func goldenWaveMembers(participants []uuid.UUID) []domain.ArenaWaveMember {
	members := make([]domain.ArenaWaveMember, len(participants))
	for index, participantID := range participants {
		members[index] = domain.ArenaWaveMember{ParticipantID: participantID}
	}
	return members
}

func goldenExecutionActiveIDs(group domain.ArenaGoldenGroupState) []uuid.UUID {
	result := make([]uuid.UUID, 0, len(group.Members))
	for _, member := range group.Members {
		if !member.Excluded {
			result = append(result, member.ParticipantID)
		}
	}
	canonicalGoldenIDs(result)
	return result
}

func goldenWaveMemberIDs(wave domain.ArenaWave) []uuid.UUID {
	result := make([]uuid.UUID, len(wave.Members))
	for index, member := range wave.Members {
		result[index] = member.ParticipantID
	}
	canonicalGoldenIDs(result)
	return result
}

func goldenPrivateAssignmentIDs(assignments []GoldenPrivateAssignment) []uuid.UUID {
	result := make([]uuid.UUID, len(assignments))
	for index, assignment := range assignments {
		result[index] = assignment.ParticipantID
	}
	canonicalGoldenIDs(result)
	return result
}

func validateGoldenExecutionIdentityRoles(e GoldenWaveExecution) error {
	roles := []goldenIdentityRole{
		{e.Scope.TournamentID, "tournament"}, {e.Scope.GroupID, "group"},
		{e.Scope.GroupRevisionID.UUID(), "group-revision"},
		{e.Source.RevisionID, goldenExecutionSourceStateRole(e.Source.Revision)},
		{e.Source.Membership.RevisionID, goldenExecutionSourceMembershipRole(e.Source.Membership.Revision)},
		{e.Source.Plan.PlanID, "plan"}, {e.Source.Plan.RevisionID, "plan-revision"},
		{e.Source.SourceProjectionRevisionID.UUID(), "source-projection-revision"},
		{e.RevisionID, goldenExecutionRevisionRole(e.Revision)}, {e.Attempt.ID, "golden-attempt"},
		{e.Wave.ID, "golden-wave"}, {e.Wave.RevisionID.UUID(), "golden-wave-revision"},
		{e.Window.ID, "golden-window"}, {e.Wave.ReadyWindow.RevisionID.UUID(), "golden-wave-window-revision"},
		{e.Window.RevisionID, goldenExecutionWindowRevisionRole(e.Window.Revision)},
		{e.Window.ReadinessRevisionID, goldenExecutionReadinessRevisionRole(e.Window.ReadinessRevision)},
		{e.Window.PresenceRevisionID, goldenExecutionPresenceRevisionRole(e.Window.PresenceRevision)},
		{e.Membership.ID, "golden-membership-binding"}, {e.Membership.RevisionID, "golden-membership-binding-revision"},
		{e.Assignment.ID, "golden-assignment"}, {e.Assignment.RevisionID, "golden-assignment-revision"},
		{e.Assignment.EdgeID, "plan-edge"}, {e.Assignment.ReservationID, "task-reservation"},
		{e.Assignment.Snapshot.SnapshotID, "task-snapshot"}, {e.Assignment.Snapshot.TaskID, "task"},
	}
	if e.PreviousRevisionID != nil {
		roles = append(roles, goldenIdentityRole{*e.PreviousRevisionID, goldenExecutionRevisionRole(e.Revision - 1)})
	}
	for _, private := range e.Assignment.Private {
		roles = append(roles, goldenIdentityRole{private.ID, "golden-private-assignment:" + private.ParticipantID.String()})
	}
	for _, receipt := range e.Receipts {
		roles = append(roles,
			goldenIdentityRole{receipt.CommandID, "golden-command:" + receipt.CommandID.String()},
			goldenIdentityRole{receipt.Result.RevisionID, goldenExecutionRevisionRole(receipt.Result.Revision)},
			goldenIdentityRole{receipt.Result.Window.RevisionID,
				goldenExecutionWindowRevisionRole(receipt.Result.Window.Revision)},
			goldenIdentityRole{receipt.Result.Window.ReadinessRevisionID,
				goldenExecutionReadinessRevisionRole(receipt.Result.Window.ReadinessRevision)},
			goldenIdentityRole{receipt.Result.Window.PresenceRevisionID,
				goldenExecutionPresenceRevisionRole(receipt.Result.Window.PresenceRevision)},
		)
		if receipt.Expected != nil {
			roles = append(roles,
				goldenIdentityRole{receipt.Expected.RevisionID,
					goldenExecutionRevisionRole(receipt.Expected.Revision)},
				goldenIdentityRole{receipt.Expected.Window.RevisionID,
					goldenExecutionWindowRevisionRole(receipt.Expected.Window.Revision)},
				goldenIdentityRole{receipt.Expected.Window.ReadinessRevisionID,
					goldenExecutionReadinessRevisionRole(receipt.Expected.Window.ReadinessRevision)},
				goldenIdentityRole{receipt.Expected.Window.PresenceRevisionID,
					goldenExecutionPresenceRevisionRole(receipt.Expected.Window.PresenceRevision)},
			)
		}
		for index, identity := range receipt.UnusedIdentityIDs {
			roles = append(roles, goldenIdentityRole{
				identity,
				fmt.Sprintf("golden-command-unused:%s:%d", receipt.CommandID, index),
			})
		}
	}
	owners := make(map[uuid.UUID]string, len(roles))
	for _, role := range roles {
		if role.value == uuid.Nil {
			continue
		}
		if owner, exists := owners[role.value]; exists && owner != role.role {
			return goldenWaveError("identity is reused across execution roles")
		}
		owners[role.value] = role.role
	}
	return nil
}

func goldenExecutionRevisionRole(revision int64) string {
	return fmt.Sprintf("golden-execution-revision:%d", revision)
}

func goldenExecutionSourceStateRole(revision int64) string {
	return fmt.Sprintf("source-state-revision:%d", revision)
}

func goldenExecutionSourceMembershipRole(revision int64) string {
	return fmt.Sprintf("source-membership-revision:%d", revision)
}

func goldenExecutionWindowRevisionRole(revision int64) string {
	return fmt.Sprintf("golden-window-state-revision:%d", revision)
}

func goldenExecutionReadinessRevisionRole(revision int64) string {
	return fmt.Sprintf("golden-readiness-revision:%d", revision)
}

func goldenExecutionPresenceRevisionRole(revision int64) string {
	return fmt.Sprintf("golden-presence-revision:%d", revision)
}

func goldenOpenCommandDigest(command OpenGoldenReadyWindowCommand) [sha256.Size]byte {
	payload, _ := goldenEncode(command)
	return sha256.Sum256(payload)
}

func goldenAssignmentDigest(assignment GoldenAttemptAssignment) [sha256.Size]byte {
	type document struct {
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
		Snapshot      domain.ArenaTaskSnapshot
		ContentDigest [sha256.Size]byte
		Private       []GoldenPrivateAssignment
	}
	payload, _ := goldenEncode(document{
		ID: assignment.ID, RevisionID: assignment.RevisionID, Revision: assignment.Revision,
		Scope: assignment.Scope, AttemptID: assignment.AttemptID, WaveID: assignment.WaveID,
		MembershipID: assignment.MembershipID, Plan: assignment.Plan, EdgeID: assignment.EdgeID,
		ReservationID: assignment.ReservationID, Snapshot: assignment.Snapshot,
		ContentDigest: assignment.ContentDigest, Private: assignment.Private,
	})
	return sha256.Sum256(payload)
}

func goldenWavePayloadDigest(execution GoldenWaveExecution) [sha256.Size]byte {
	type document struct {
		Scope                           GoldenStateScope
		Source                          GoldenStateExpectation
		RevisionID                      uuid.UUID
		Revision                        int64
		PreviousRevisionID              *uuid.UUID
		Group                           domain.ArenaGoldenGroupState
		GroupBindingDigest              [sha256.Size]byte
		OpeningParticipationEstablished bool
		Attempt                         domain.ArenaGoldenAttempt
		Wave                            domain.ArenaWave
		Membership                      GoldenWaveMembershipBinding
		Assignment                      GoldenAttemptAssignment
		Window                          GoldenReadyWindow
		OpenedAt                        time.Time
		Deadline                        time.Time
		ReceiptsDigest                  [sha256.Size]byte
		Start                           *GoldenStartRecord
	}
	payload, _ := goldenEncode(document{
		Scope: execution.Scope, Source: execution.Source, RevisionID: execution.RevisionID,
		Revision: execution.Revision, PreviousRevisionID: execution.PreviousRevisionID,
		Group: execution.Group, GroupBindingDigest: execution.GroupBindingDigest,
		OpeningParticipationEstablished: execution.OpeningParticipationEstablished,
		Attempt:                         execution.Attempt, Wave: execution.Wave,
		Membership: execution.Membership, Assignment: execution.Assignment, Window: execution.Window,
		OpenedAt: execution.OpenedAt, Deadline: execution.Deadline,
		ReceiptsDigest: execution.ReceiptsDigest, Start: execution.Start,
	})
	return sha256.Sum256(payload)
}

func goldenExecutionGroupBindingDigest(
	group domain.ArenaGoldenGroupState,
	openingParticipationEstablished bool,
) ([sha256.Size]byte, error) {
	normalized := cloneGoldenStateGroup(group)
	normalized.ParticipationEstablished = openingParticipationEstablished
	if len(normalized.Attempts) > 0 {
		last := len(normalized.Attempts) - 1
		normalized.Attempts[last].State = domain.ArenaGoldenAttemptStateWaitingReady
		normalized.Attempts[last].StartedAt = nil
	}
	payload, err := goldenEncode(normalized)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(payload), nil
}

func goldenWaveReceiptsDigest(receipts []GoldenWaveCommandReceipt) ([sha256.Size]byte, error) {
	normalized := cloneGoldenWaveReceipts(receipts)
	if len(normalized) > 0 {
		normalized[len(normalized)-1].Result.PayloadDigest = [sha256.Size]byte{}
	}
	payload, err := goldenEncode(normalized)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(payload), nil
}

func findGoldenWaveReplay(
	ctx context.Context,
	repository GoldenWaveRepository,
	scope GoldenStateScope,
	commandID uuid.UUID,
) (GoldenWaveCommandReplay, bool, error) {
	replay, err := repository.FindGoldenWaveCommand(ctx, scope.TournamentID, commandID)
	if err != nil {
		return GoldenWaveCommandReplay{}, false, fmt.Errorf("golden Wave - find command: %w", err)
	}
	if replay == nil {
		return GoldenWaveCommandReplay{}, false, nil
	}
	return *replay, true, nil
}

func reconcileGoldenWaveReplay(
	scope GoldenStateScope,
	commandID uuid.UUID,
	kind GoldenWaveCommandKind,
	digest [sha256.Size]byte,
	replay GoldenWaveCommandReplay,
) (*GoldenWaveExecution, bool, error) {
	receipt := replay.Receipt
	if receipt.CommandID != commandID || receipt.Scope != scope || receipt.Kind != kind || receipt.CommandDigest != digest {
		return nil, false, ErrGoldenWaveCommandReuse
	}
	if replay.Execution.Scope != scope || replay.Execution.Validate() != nil ||
		!goldenExecutionHasReceipt(replay.Execution, receipt) {
		return nil, false, domain.ErrInternal
	}
	clone := replay.Execution.Snapshot()
	return &clone, false, nil
}

type goldenWaveAttemptLoad struct {
	Execution *GoldenWaveExecution
	Replay    *GoldenWaveExecution
}

func loadGoldenWaveCommandAttempt(
	ctx context.Context,
	repository GoldenWaveRepository,
	scope GoldenStateScope,
	commandID uuid.UUID,
	kind GoldenWaveCommandKind,
	digest [sha256.Size]byte,
) (goldenWaveAttemptLoad, error) {
	execution, err := repository.LoadGoldenWaveExecution(ctx, scope)
	if err != nil {
		return goldenWaveAttemptLoad{}, fmt.Errorf("load golden Wave execution: %w", err)
	}
	if replay, found, findErr := findGoldenWaveReplay(ctx, repository, scope, commandID); findErr != nil {
		return goldenWaveAttemptLoad{}, findErr
	} else if found {
		result, _, replayErr := reconcileGoldenWaveReplay(scope, commandID, kind, digest, replay)
		return goldenWaveAttemptLoad{Replay: result}, replayErr
	}
	if execution == nil {
		return goldenWaveAttemptLoad{}, nil
	}
	if execution.Validate() != nil {
		return goldenWaveAttemptLoad{}, domain.ErrInternal
	}
	if receipt, found := goldenExecutionReceiptByCommand(*execution, commandID); found {
		replay := GoldenWaveCommandReplay{Receipt: receipt, Execution: execution.Snapshot()}
		result, _, replayErr := reconcileGoldenWaveReplay(scope, commandID, kind, digest, replay)
		return goldenWaveAttemptLoad{Replay: result}, replayErr
	}
	return goldenWaveAttemptLoad{Execution: execution}, nil
}

func loadValidGoldenState(
	ctx context.Context,
	repository GoldenWaveRepository,
	scope GoldenStateScope,
) (GoldenState, error) {
	state, err := repository.LoadGoldenState(ctx, scope)
	if err != nil {
		return GoldenState{}, fmt.Errorf("load golden state: %w", err)
	}
	if state.Validate() != nil {
		return GoldenState{}, domain.ErrInternal
	}
	return state, nil
}

func commitGoldenWaveExecution(
	ctx context.Context,
	repository GoldenWaveRepository,
	commit GoldenWaveExecutionCommit,
	commandID uuid.UUID,
	kind GoldenWaveCommandKind,
	digest [sha256.Size]byte,
) (*GoldenWaveExecution, bool, bool, error) {
	proposed := commit.Next.Snapshot()
	frozen := commit
	frozen.ExpectedState = cloneGoldenStateExpectation(commit.ExpectedState)
	frozen.ExpectedExecution = cloneGoldenExecutionExpectation(commit.ExpectedExecution)
	frozen.NewIdentityIDs = append([]uuid.UUID(nil), commit.NewIdentityIDs...)
	frozen.Next = proposed.Snapshot()
	if commit.Authority != nil {
		authority := *commit.Authority
		frozen.Authority = &authority
	}
	if len(frozen.NewIdentityIDs) == 0 || !goldenIDsAreCanonical(frozen.NewIdentityIDs) {
		return nil, false, false, domain.ErrInternal
	}
	committed, changed, err := repository.CommitGoldenWaveExecution(ctx, frozen)
	if errors.Is(err, domain.ErrConflict) {
		return nil, false, true, nil
	}
	if errors.Is(err, ErrGoldenWaveAuthorityNotLive) {
		return nil, false, false, ErrGoldenWaveAuthorityNotLive
	}
	if errors.Is(err, ErrGoldenWaveIdentityConflict) {
		return nil, false, false, ErrGoldenWaveIdentityConflict
	}
	if err != nil {
		return nil, false, false, fmt.Errorf("golden Wave - commit execution: %w", err)
	}
	if committed == nil || committed.Validate() != nil {
		return nil, false, false, domain.ErrInternal
	}
	receipt, found := goldenExecutionReceiptByCommand(*committed, commandID)
	if !found || receipt.Kind != kind || receipt.CommandDigest != digest ||
		(changed && !committed.Expectation().Equal(proposed.Expectation())) {
		return nil, false, false, domain.ErrInternal
	}
	clone := committed.Snapshot()
	return &clone, changed, false, nil
}

func goldenExecutionHasReceipt(execution GoldenWaveExecution, expected GoldenWaveCommandReceipt) bool {
	receipt, found := goldenExecutionReceiptByCommand(execution, expected.CommandID)
	return found && goldenWaveReceiptsEqual(receipt, expected)
}

func goldenExecutionReceiptByCommand(
	execution GoldenWaveExecution,
	commandID uuid.UUID,
) (GoldenWaveCommandReceipt, bool) {
	for _, receipt := range execution.Receipts {
		if receipt.CommandID == commandID {
			return receipt, true
		}
	}
	return GoldenWaveCommandReceipt{}, false
}

func goldenWaveReceiptsEqual(first, second GoldenWaveCommandReceipt) bool {
	if first.CommandID != second.CommandID || first.Scope != second.Scope || first.Kind != second.Kind ||
		first.CommandDigest != second.CommandDigest || !first.Result.Equal(second.Result) ||
		!first.OccurredAt.Equal(second.OccurredAt) || first.ParticipantID != second.ParticipantID ||
		!equalGoldenIDs(first.UnusedIdentityIDs, second.UnusedIdentityIDs) {
		return false
	}
	if first.Expected == nil || second.Expected == nil {
		return first.Expected == nil && second.Expected == nil
	}
	return first.Expected.Equal(*second.Expected)
}

func cloneGoldenMembershipBinding(input GoldenWaveMembershipBinding) GoldenWaveMembershipBinding {
	clone := input
	clone.Source.PreviousRevisionID = cloneGoldenUUID(input.Source.PreviousRevisionID)
	clone.ParticipantIDs = append([]uuid.UUID(nil), input.ParticipantIDs...)
	return clone
}

func cloneGoldenAttemptAssignment(input GoldenAttemptAssignment) GoldenAttemptAssignment {
	clone := input
	clone.Snapshot = cloneTaskSnapshot(input.Snapshot)
	clone.Private = append([]GoldenPrivateAssignment(nil), input.Private...)
	return clone
}

func cloneGoldenWaveReceipts(input []GoldenWaveCommandReceipt) []GoldenWaveCommandReceipt {
	result := make([]GoldenWaveCommandReceipt, len(input))
	for index, receipt := range input {
		result[index] = receipt
		result[index].UnusedIdentityIDs = append([]uuid.UUID(nil), receipt.UnusedIdentityIDs...)
		result[index].Result.Source = cloneGoldenStateExpectation(receipt.Result.Source)
		if receipt.Expected != nil {
			expected := *receipt.Expected
			expected.Source = cloneGoldenStateExpectation(receipt.Expected.Source)
			result[index].Expected = &expected
		}
	}
	return result
}

func cloneGoldenStateExpectation(input GoldenStateExpectation) GoldenStateExpectation {
	clone := input
	clone.Membership.PreviousRevisionID = cloneGoldenUUID(input.Membership.PreviousRevisionID)
	return clone
}

func cloneGoldenExecutionExpectation(
	input *GoldenWaveExecutionExpectation,
) *GoldenWaveExecutionExpectation {
	if input == nil {
		return nil
	}
	clone := *input
	clone.Source = cloneGoldenStateExpectation(input.Source)
	return &clone
}

func cloneGoldenAttempt(input domain.ArenaGoldenAttempt) domain.ArenaGoldenAttempt {
	clone := input
	clone.PreviousAttemptID = cloneGoldenUUID(input.PreviousAttemptID)
	clone.ParticipantIDs = append([]uuid.UUID(nil), input.ParticipantIDs...)
	clone.RetainedAt = cloneGoldenTime(input.RetainedAt)
	clone.StartedAt = cloneGoldenTime(input.StartedAt)
	clone.FinishedAt = cloneGoldenTime(input.FinishedAt)
	return clone
}

func goldenWaveError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidGoldenWaveExecution, fmt.Sprintf(format, arguments...))
}
