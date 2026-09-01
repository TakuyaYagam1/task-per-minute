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
)

const (
	goldenIndividualConnectionAttempts = 3
	goldenIndividualParticipantLimit   = domain.ArenaMaxParticipants
	goldenIndividualIntervalLimit      = domain.ArenaMaxParticipants * ReconnectCycleLimit
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
	canonicalGoldenIDs(participants)
	ledger := GoldenIndividualConnectionLedger{
		Scope: scope, Execution: cloneGoldenExecutionExpectationValue(execution), Submissions: submissions,
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
	clone.Execution = cloneGoldenExecutionExpectationValue(l.Execution)
	clone.PreviousRevisionID = cloneGoldenUUID(l.PreviousRevisionID)
	clone.ParticipantIDs = append([]uuid.UUID(nil), l.ParticipantIDs...)
	clone.PresentParticipantIDs = append([]uuid.UUID(nil), l.PresentParticipantIDs...)
	clone.Intervals = make([]GoldenIndividualReconnectInterval, len(l.Intervals))
	for index, interval := range l.Intervals {
		clone.Intervals[index] = interval
		clone.Intervals[index].ReconnectedAt = cloneGoldenTime(interval.ReconnectedAt)
	}
	clone.Receipts = make([]GoldenIndividualConnectionReceipt, len(l.Receipts))
	for index, receipt := range l.Receipts {
		clone.Receipts[index] = receipt
		clone.Receipts[index].Expected.Execution = cloneGoldenExecutionExpectationValue(receipt.Expected.Execution)
	}
	return clone
}

func (l GoldenIndividualConnectionLedger) Expectation() GoldenIndividualConnectionExpectation {
	return GoldenIndividualConnectionExpectation{
		Scope: l.Scope, Execution: cloneGoldenExecutionExpectationValue(l.Execution), Submissions: l.Submissions,
		StartedAt: l.StartedAt, Deadline: l.Deadline,
		RevisionID: l.RevisionID, Revision: l.Revision, PayloadDigest: l.PayloadDigest,
	}
}

func (l GoldenIndividualConnectionLedger) IsPresent(participantID uuid.UUID) bool {
	return goldenIDsContain(l.PresentParticipantIDs, participantID)
}

func (l GoldenIndividualConnectionLedger) Validate() error {
	if !validGoldenIndividualLedgerAuthority(l) || !validGoldenIndividualLedgerRevision(l) ||
		!validGoldenIndividualLedgerCollections(l) {
		return goldenIndividualConnectionError("invalid ledger identity or participant set")
	}
	if err := validateGoldenIndividualIntervals(l); err != nil {
		return err
	}
	if err := validateGoldenIndividualReceipts(l); err != nil {
		return err
	}
	payload, err := goldenIndividualConnectionPayload(l)
	if err != nil || l.PayloadDigest == [sha256.Size]byte{} || sha256.Sum256(payload) != l.PayloadDigest {
		return goldenIndividualConnectionError("ledger digest changed")
	}
	return nil
}

func validGoldenIndividualLedgerAuthority(l GoldenIndividualConnectionLedger) bool {
	return l.Scope.IsValid() && l.Execution.Scope == l.Scope.State && l.Submissions.Scope == l.Scope &&
		validGoldenIndividualSubmissionHead(l.Submissions, l.Scope) && l.Execution.Started &&
		validArenaServerTime(l.StartedAt) && validArenaServerTime(l.Deadline) && l.StartedAt.Before(l.Deadline)
}

func validGoldenIndividualLedgerRevision(l GoldenIndividualConnectionLedger) bool {
	return l.RevisionID != uuid.Nil && l.Revision >= 1 &&
		validGoldenRevisionPredecessor(l.RevisionID, l.Revision, l.PreviousRevisionID) &&
		len(l.Receipts) == int(l.Revision-1)
}

func validGoldenIndividualLedgerCollections(l GoldenIndividualConnectionLedger) bool {
	return len(l.ParticipantIDs) >= 2 && len(l.ParticipantIDs) <= goldenIndividualParticipantLimit &&
		len(l.Intervals) <= goldenIndividualIntervalLimit && len(l.Receipts) <= goldenIndividualReceiptLimit &&
		goldenIDsAreCanonical(l.ParticipantIDs) && goldenIDsAreCanonical(l.PresentParticipantIDs) &&
		goldenIDsSubset(l.PresentParticipantIDs, l.ParticipantIDs)
}

func validateGoldenIndividualIntervals(l GoldenIndividualConnectionLedger) error {
	open := make(map[uuid.UUID]struct{})
	sequences := make(map[uuid.UUID]int)
	ids := make(map[uuid.UUID]struct{}, len(l.Intervals))
	for _, interval := range l.Intervals {
		if !validGoldenIndividualIntervalHeader(l, interval, sequences[interval.ParticipantID]+1) {
			return goldenIndividualConnectionError("invalid reconnect interval")
		}
		if _, duplicate := ids[interval.ID]; duplicate {
			return goldenIndividualConnectionError("reconnect interval identity is reused")
		}
		ids[interval.ID] = struct{}{}
		sequences[interval.ParticipantID] = interval.Sequence
		if err := validateGoldenIndividualIntervalState(l, interval, open); err != nil {
			return err
		}
	}
	return nil
}

func validGoldenIndividualIntervalHeader(
	ledger GoldenIndividualConnectionLedger,
	interval GoldenIndividualReconnectInterval,
	expectedSequence int,
) bool {
	return interval.ID != uuid.Nil && goldenIDsContain(ledger.ParticipantIDs, interval.ParticipantID) &&
		interval.Sequence == expectedSequence && interval.Sequence <= ReconnectCycleLimit &&
		validArenaServerTime(interval.DisconnectedAt) && validArenaServerTime(interval.Deadline) &&
		interval.DisconnectedAt.Before(interval.Deadline) && interval.Deadline.Equal(ledger.Deadline) &&
		!interval.DisconnectedAt.Before(ledger.StartedAt)
}

func validateGoldenIndividualIntervalState(
	ledger GoldenIndividualConnectionLedger,
	interval GoldenIndividualReconnectInterval,
	open map[uuid.UUID]struct{},
) error {
	switch interval.State {
	case GoldenIndividualReconnectOpen:
		if interval.ReconnectedAt != nil || goldenIDsContain(ledger.PresentParticipantIDs, interval.ParticipantID) {
			return goldenIndividualConnectionError("open interval retained present participant")
		}
		if _, duplicate := open[interval.ParticipantID]; duplicate {
			return goldenIndividualConnectionError("participant has two open intervals")
		}
		open[interval.ParticipantID] = struct{}{}
		return nil
	case GoldenIndividualReconnectClosed:
		if interval.ReconnectedAt == nil || !validArenaServerTime(*interval.ReconnectedAt) ||
			interval.ReconnectedAt.Before(interval.DisconnectedAt) || !interval.ReconnectedAt.Before(interval.Deadline) {
			return goldenIndividualConnectionError("invalid reconnect closure")
		}
		return nil
	default:
		return goldenIndividualConnectionError("unknown reconnect interval state")
	}
}

func validateGoldenIndividualReceipts(l GoldenIndividualConnectionLedger) error {
	if len(l.Receipts) == 0 {
		return validateInitialGoldenIndividualConnectionLedger(l)
	}
	derivation, err := newGoldenIndividualReceiptDerivation(l)
	if err != nil {
		return err
	}
	for index, receipt := range l.Receipts {
		if err := derivation.apply(l, index, receipt); err != nil {
			return err
		}
	}
	if !reflect.DeepEqual(derivation.cursor.Snapshot(), l.Snapshot()) {
		return goldenIndividualConnectionError("connection receipts do not derive the ledger head")
	}
	return nil
}

type goldenIndividualReceiptDerivation struct {
	cursor                GoldenIndividualConnectionLedger
	seen                  map[uuid.UUID]struct{}
	openByParticipant     map[uuid.UUID]int
	intervalByID          map[uuid.UUID]int
	sequenceByParticipant map[uuid.UUID]int
}

func validateInitialGoldenIndividualConnectionLedger(l GoldenIndividualConnectionLedger) error {
	if l.Revision != 1 || l.PreviousRevisionID != nil || len(l.Intervals) != 0 ||
		!equalGoldenIDs(l.PresentParticipantIDs, l.ParticipantIDs) {
		return goldenIndividualConnectionError("initial connection ledger has derived state")
	}
	return nil
}

func newGoldenIndividualReceiptDerivation(
	ledger GoldenIndividualConnectionLedger,
) (goldenIndividualReceiptDerivation, error) {
	first := ledger.Receipts[0]
	if !validGoldenIndividualSubmissionHead(first.Expected.Submissions, ledger.Scope) {
		return goldenIndividualReceiptDerivation{}, goldenIndividualConnectionError(
			"connection receipt initial submission head is malformed",
		)
	}
	cursor := GoldenIndividualConnectionLedger{
		Scope: ledger.Scope, Execution: cloneGoldenExecutionExpectationValue(ledger.Execution),
		Submissions: first.Expected.Submissions, StartedAt: ledger.StartedAt, Deadline: ledger.Deadline,
		RevisionID: first.Expected.RevisionID, Revision: 1,
		ParticipantIDs:        append([]uuid.UUID(nil), ledger.ParticipantIDs...),
		PresentParticipantIDs: append([]uuid.UUID(nil), ledger.ParticipantIDs...),
		PayloadDigest:         first.Expected.PayloadDigest,
	}
	initialPayload, err := goldenIndividualConnectionPayload(cursor)
	if err != nil || sha256.Sum256(initialPayload) != cursor.PayloadDigest ||
		!cursor.Expectation().Equal(first.Expected) {
		return goldenIndividualReceiptDerivation{}, goldenIndividualConnectionError("connection receipt initial head changed")
	}
	return goldenIndividualReceiptDerivation{
		cursor: cursor, seen: make(map[uuid.UUID]struct{}, len(ledger.Receipts)),
		openByParticipant:     make(map[uuid.UUID]int, len(ledger.ParticipantIDs)),
		intervalByID:          make(map[uuid.UUID]int, len(ledger.Intervals)),
		sequenceByParticipant: make(map[uuid.UUID]int, len(ledger.ParticipantIDs)),
	}, nil
}

func (d *goldenIndividualReceiptDerivation) apply(
	ledger GoldenIndividualConnectionLedger,
	index int,
	receipt GoldenIndividualConnectionReceipt,
) error {
	if !validGoldenIndividualReceiptIdentity(ledger, index, receipt) ||
		!validGoldenIndividualReceiptBinding(ledger, d.cursor, receipt) {
		return goldenIndividualConnectionError("invalid retained connection receipt")
	}
	if err := validateGoldenIndividualSubmissionAdvance(d.cursor.Submissions, receipt.ObservedSubmissions); err != nil {
		return err
	}
	if err := d.retainReceiptHistory(ledger, index, receipt); err != nil {
		return err
	}
	d.advanceHead(receipt)
	if err := d.applyPresence(receipt); err != nil {
		return err
	}
	return d.retainDerivedReceipt(receipt)
}

func validGoldenIndividualReceiptIdentity(
	ledger GoldenIndividualConnectionLedger,
	index int,
	receipt GoldenIndividualConnectionReceipt,
) bool {
	validKind := receipt.Kind == GoldenIndividualConnectionDisconnected ||
		receipt.Kind == GoldenIndividualConnectionReconnected
	return receipt.CommandID != uuid.Nil && receipt.CommandDigest != [sha256.Size]byte{} && validKind &&
		goldenIDsContain(ledger.ParticipantIDs, receipt.ParticipantID) && receipt.IntervalID != uuid.Nil &&
		validArenaServerTime(receipt.OccurredAt) && receipt.ResultRevision == int64(index+2) &&
		receipt.ResultRevisionID != uuid.Nil
}

func validGoldenIndividualReceiptBinding(
	ledger GoldenIndividualConnectionLedger,
	cursor GoldenIndividualConnectionLedger,
	receipt GoldenIndividualConnectionReceipt,
) bool {
	return receipt.Expected.Scope == ledger.Scope && receipt.Expected.Execution.Equal(ledger.Execution) &&
		receipt.Expected.StartedAt.Equal(ledger.StartedAt) && receipt.Expected.Deadline.Equal(ledger.Deadline) &&
		receipt.Expected.Revision == cursor.Revision &&
		validGoldenIndividualSubmissionHead(receipt.ObservedSubmissions, ledger.Scope) &&
		receipt.ObservedSubmissions.Revision >= cursor.Submissions.Revision &&
		!receipt.OccurredAt.Before(ledger.StartedAt) && receipt.OccurredAt.Before(ledger.Deadline) &&
		cursor.Expectation().Equal(receipt.Expected)
}

func validateGoldenIndividualSubmissionAdvance(
	current GoldenSubmissionLedgerExpectation,
	observed GoldenSubmissionLedgerExpectation,
) error {
	if observed.Revision == current.Revision && !observed.Equal(current) {
		return goldenIndividualConnectionError("connection receipt changed a submission head")
	}
	if observed.Revision > current.Revision &&
		(observed.RevisionID == current.RevisionID || observed.NextSubmissionID < current.NextSubmissionID) {
		return goldenIndividualConnectionError("connection receipt regressed an advanced submission head")
	}
	return nil
}

func (d *goldenIndividualReceiptDerivation) retainReceiptHistory(
	ledger GoldenIndividualConnectionLedger,
	index int,
	receipt GoldenIndividualConnectionReceipt,
) error {
	if index > 0 && receipt.OccurredAt.Before(ledger.Receipts[index-1].OccurredAt) {
		return goldenIndividualConnectionError("connection receipt time regressed")
	}
	if _, duplicate := d.seen[receipt.CommandID]; duplicate {
		return goldenIndividualConnectionError("connection command is duplicated")
	}
	d.seen[receipt.CommandID] = struct{}{}
	return nil
}

func (d *goldenIndividualReceiptDerivation) advanceHead(receipt GoldenIndividualConnectionReceipt) {
	d.cursor.PreviousRevisionID = goldenUUID(d.cursor.RevisionID)
	d.cursor.RevisionID = receipt.ResultRevisionID
	d.cursor.Revision = receipt.ResultRevision
	d.cursor.Submissions = receipt.ObservedSubmissions
}

func (d *goldenIndividualReceiptDerivation) applyPresence(receipt GoldenIndividualConnectionReceipt) error {
	switch receipt.Kind {
	case GoldenIndividualConnectionDisconnected:
		return d.applyDisconnect(receipt)
	case GoldenIndividualConnectionReconnected:
		return d.applyReconnect(receipt)
	default:
		return goldenIndividualConnectionError("invalid retained connection receipt")
	}
}

func (d *goldenIndividualReceiptDerivation) applyDisconnect(receipt GoldenIndividualConnectionReceipt) error {
	if !d.cursor.IsPresent(receipt.ParticipantID) || d.openByParticipant[receipt.ParticipantID] != 0 ||
		d.sequenceByParticipant[receipt.ParticipantID] >= ReconnectCycleLimit {
		return goldenIndividualConnectionError("disconnect receipt cannot derive presence")
	}
	d.cursor.PresentParticipantIDs = removeGoldenID(d.cursor.PresentParticipantIDs, receipt.ParticipantID)
	d.sequenceByParticipant[receipt.ParticipantID]++
	d.cursor.Intervals = append(d.cursor.Intervals, GoldenIndividualReconnectInterval{
		ID: receipt.IntervalID, ParticipantID: receipt.ParticipantID,
		Sequence: d.sequenceByParticipant[receipt.ParticipantID],
		State:    GoldenIndividualReconnectOpen, DisconnectedAt: receipt.OccurredAt, Deadline: d.cursor.Deadline,
	})
	intervalIndex := len(d.cursor.Intervals) - 1
	d.intervalByID[receipt.IntervalID] = intervalIndex + 1
	d.openByParticipant[receipt.ParticipantID] = intervalIndex + 1
	return nil
}

func (d *goldenIndividualReceiptDerivation) applyReconnect(receipt GoldenIndividualConnectionReceipt) error {
	intervalIndex := d.intervalByID[receipt.IntervalID] - 1
	openIndex := d.openByParticipant[receipt.ParticipantID] - 1
	if intervalIndex < 0 || openIndex != intervalIndex || intervalIndex >= len(d.cursor.Intervals) ||
		d.cursor.Intervals[intervalIndex].ParticipantID != receipt.ParticipantID ||
		d.cursor.Intervals[intervalIndex].State != GoldenIndividualReconnectOpen || d.cursor.IsPresent(receipt.ParticipantID) ||
		receipt.OccurredAt.Before(d.cursor.Intervals[intervalIndex].DisconnectedAt) {
		return goldenIndividualConnectionError("reconnect receipt cannot derive presence")
	}
	d.cursor.Intervals[intervalIndex].State = GoldenIndividualReconnectClosed
	d.cursor.Intervals[intervalIndex].ReconnectedAt = cloneGoldenTime(&receipt.OccurredAt)
	delete(d.openByParticipant, receipt.ParticipantID)
	d.cursor.PresentParticipantIDs = append(d.cursor.PresentParticipantIDs, receipt.ParticipantID)
	canonicalGoldenIDs(d.cursor.PresentParticipantIDs)
	return nil
}

func (d *goldenIndividualReceiptDerivation) retainDerivedReceipt(
	receipt GoldenIndividualConnectionReceipt,
) error {
	d.cursor.Receipts = append(d.cursor.Receipts, receipt)
	payload, err := goldenIndividualConnectionPayload(d.cursor)
	if err != nil {
		return goldenIndividualConnectionError("encode derived connection head")
	}
	d.cursor.PayloadDigest = sha256.Sum256(payload)
	return nil
}

func validGoldenIndividualSubmissionHead(
	head GoldenSubmissionLedgerExpectation,
	scope GoldenSubmissionScope,
) bool {
	return head.Scope == scope && head.RevisionID != uuid.Nil && head.Revision >= 1 &&
		head.NextSubmissionID >= 1 && head.PayloadDigest != [sha256.Size]byte{}
}

type GoldenIndividualConnectionReplay struct {
	Receipt     GoldenIndividualConnectionReceipt
	Connections GoldenIndividualConnectionLedger
}

func (r GoldenIndividualConnectionReplay) Snapshot() GoldenIndividualConnectionReplay {
	clone := r
	clone.Receipt.Expected.Execution = cloneGoldenExecutionExpectationValue(r.Receipt.Expected.Execution)
	clone.Connections = r.Connections.Snapshot()
	return clone
}

type GoldenIndividualDisconnectAuthority struct {
	Execution   GoldenWaveExecution
	Submissions GoldenSubmissionLedger
	Connections GoldenIndividualConnectionLedger
}

type GoldenIndividualConnectionCommit struct {
	ExpectedExecution   GoldenWaveExecutionExpectation
	ExpectedSubmissions GoldenSubmissionLedgerExpectation
	ExpectedConnections GoldenIndividualConnectionExpectation
	NewIdentityIDs      []uuid.UUID
	Next                GoldenIndividualConnectionLedger
}

// GoldenIndividualDisconnectRepository owns one CAS over the active execution,
// current submission head and independent connection ledger. The transaction
// globally reserves NewIdentityIDs across current and archived Golden records.
type GoldenIndividualDisconnectRepository interface {
	FindGoldenIndividualConnectionCommand(ctx context.Context, tournamentID, commandID uuid.UUID) (*GoldenIndividualConnectionReplay, error)
	LoadGoldenIndividualDisconnectAuthority(ctx context.Context, scope GoldenSubmissionScope) (GoldenIndividualDisconnectAuthority, error)
	CommitGoldenIndividualConnection(ctx context.Context, commit GoldenIndividualConnectionCommit) (*GoldenIndividualConnectionLedger, bool, error)
}

type GoldenIndividualDisconnectCommand struct {
	Scope                    GoldenSubmissionScope
	CommandID                uuid.UUID
	ParticipantID            uuid.UUID
	IntervalID               uuid.UUID
	ExpectedExecution        GoldenWaveExecutionExpectation
	ExpectedSubmissions      GoldenSubmissionLedgerExpectation
	ExpectedConnections      GoldenIndividualConnectionExpectation
	NextConnectionRevisionID uuid.UUID
}

type GoldenIndividualReconnectCommand struct {
	Scope                    GoldenSubmissionScope
	CommandID                uuid.UUID
	ActorParticipantID       uuid.UUID
	ParticipantID            uuid.UUID
	IntervalID               uuid.UUID
	ExpectedExecution        GoldenWaveExecutionExpectation
	ExpectedSubmissions      GoldenSubmissionLedgerExpectation
	ExpectedConnections      GoldenIndividualConnectionExpectation
	NextConnectionRevisionID uuid.UUID
}

type GoldenIndividualDisconnectUseCase struct {
	repository GoldenIndividualDisconnectRepository
	clock      Clock
}

func NewGoldenIndividualDisconnectUseCase(
	repository GoldenIndividualDisconnectRepository,
	clock Clock,
) *GoldenIndividualDisconnectUseCase {
	return &GoldenIndividualDisconnectUseCase{repository: repository, clock: clock}
}

func (u *GoldenIndividualDisconnectUseCase) Disconnect(
	ctx context.Context,
	command GoldenIndividualDisconnectCommand,
) (*GoldenIndividualConnectionLedger, bool, error) {
	operation := goldenIndividualConnectionOperation{
		scope: command.Scope, commandID: command.CommandID, participantID: command.ParticipantID,
		intervalID: command.IntervalID, expectedExecution: cloneGoldenExecutionExpectationValue(command.ExpectedExecution),
		expectedSubmissions: command.ExpectedSubmissions, expectedConnections: command.ExpectedConnections,
		nextRevisionID: command.NextConnectionRevisionID, kind: GoldenIndividualConnectionDisconnected,
	}
	return u.apply(ctx, operation)
}

func (u *GoldenIndividualDisconnectUseCase) Reconnect(
	ctx context.Context,
	command GoldenIndividualReconnectCommand,
) (*GoldenIndividualConnectionLedger, bool, error) {
	if command.ActorParticipantID == uuid.Nil || command.ActorParticipantID != command.ParticipantID {
		return nil, false, domain.ErrArenaAssignmentParticipant
	}
	operation := goldenIndividualConnectionOperation{
		scope: command.Scope, commandID: command.CommandID, participantID: command.ParticipantID,
		intervalID: command.IntervalID, expectedExecution: cloneGoldenExecutionExpectationValue(command.ExpectedExecution),
		expectedSubmissions: command.ExpectedSubmissions, expectedConnections: command.ExpectedConnections,
		nextRevisionID: command.NextConnectionRevisionID, kind: GoldenIndividualConnectionReconnected,
	}
	return u.apply(ctx, operation)
}

type goldenIndividualConnectionOperation struct {
	scope               GoldenSubmissionScope
	commandID           uuid.UUID
	participantID       uuid.UUID
	intervalID          uuid.UUID
	expectedExecution   GoldenWaveExecutionExpectation
	expectedSubmissions GoldenSubmissionLedgerExpectation
	expectedConnections GoldenIndividualConnectionExpectation
	nextRevisionID      uuid.UUID
	kind                GoldenIndividualConnectionKind
}

func (u *GoldenIndividualDisconnectUseCase) apply(
	ctx context.Context,
	operation goldenIndividualConnectionOperation,
) (*GoldenIndividualConnectionLedger, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil || !validGoldenIndividualOperation(operation) {
		return nil, false, domain.ErrValidation
	}
	digest := goldenIndividualOperationDigest(operation)
	if replay, err := u.repository.FindGoldenIndividualConnectionCommand(ctx, operation.scope.State.TournamentID, operation.commandID); err != nil {
		return nil, false, fmt.Errorf("GoldenIndividualDisconnectUseCase - find replay: %w", err)
	} else if replay != nil {
		return reconcileGoldenIndividualReplay(*replay, operation, digest)
	}
	now := u.clock.Now().Round(0).UTC()
	if !validArenaServerTime(now) {
		return nil, false, domain.ErrValidation
	}
	for range goldenIndividualConnectionAttempts {
		ledger, changed, retry, err := u.applyAttempt(ctx, operation, digest, now)
		if retry {
			replay, replayErr := u.repository.FindGoldenIndividualConnectionCommand(
				ctx, operation.scope.State.TournamentID, operation.commandID,
			)
			if replayErr != nil {
				return nil, false, fmt.Errorf("GoldenIndividualDisconnectUseCase - find retry replay: %w", replayErr)
			}
			if replay != nil {
				return reconcileGoldenIndividualReplay(*replay, operation, digest)
			}
			continue
		}
		return ledger, changed, err
	}
	return nil, false, ErrGoldenIndividualConnectionConflict
}

func (u *GoldenIndividualDisconnectUseCase) applyAttempt(
	ctx context.Context,
	operation goldenIndividualConnectionOperation,
	digest [sha256.Size]byte,
	now time.Time,
) (*GoldenIndividualConnectionLedger, bool, bool, error) {
	if replay, err := u.repository.FindGoldenIndividualConnectionCommand(
		ctx, operation.scope.State.TournamentID, operation.commandID,
	); err != nil {
		return nil, false, false, fmt.Errorf("GoldenIndividualDisconnectUseCase - find attempt replay: %w", err)
	} else if replay != nil {
		result, changed, replayErr := reconcileGoldenIndividualReplay(*replay, operation, digest)
		return result, changed, false, replayErr
	}
	authority, err := u.repository.LoadGoldenIndividualDisconnectAuthority(ctx, operation.scope)
	if err != nil {
		return nil, false, false, fmt.Errorf("GoldenIndividualDisconnectUseCase - load authority: %w", err)
	}
	if err := validateGoldenIndividualMutationAuthority(authority, operation, now); err != nil {
		return nil, false, false, err
	}
	next, ids, err := buildGoldenIndividualConnectionSuccessor(authority, operation, digest, now)
	if err != nil {
		return nil, false, false, err
	}
	commit := GoldenIndividualConnectionCommit{
		ExpectedExecution: operation.expectedExecution, ExpectedSubmissions: operation.expectedSubmissions,
		ExpectedConnections: operation.expectedConnections, NewIdentityIDs: ids, Next: next,
	}
	committed, changed, err := u.repository.CommitGoldenIndividualConnection(ctx, commit)
	if errors.Is(err, domain.ErrConflict) {
		return nil, false, true, nil
	}
	if err != nil {
		return nil, false, false, fmt.Errorf("GoldenIndividualDisconnectUseCase - commit connection: %w", err)
	}
	if err := validateGoldenIndividualCommittedMutation(committed, next, operation, digest, changed); err != nil {
		return nil, false, false, err
	}
	clone := committed.Snapshot()
	return &clone, changed, false, nil
}

func validateGoldenIndividualMutationAuthority(
	authority GoldenIndividualDisconnectAuthority,
	operation goldenIndividualConnectionOperation,
	now time.Time,
) error {
	if authority.Execution.Validate() != nil || authority.Submissions.Validate() != nil ||
		authority.Connections.Validate() != nil {
		return domain.ErrInternal
	}
	if !goldenIndividualAuthorityMatches(authority, operation) {
		return ErrGoldenIndividualConnectionAuthorityConflict
	}
	start := authority.Execution.Start
	if now.Before(start.StartedAt) || now.Before(authority.Connections.StartedAt) {
		return goldenIndividualConnectionError("connection time precedes start")
	}
	if !now.Before(start.Deadline) {
		return ErrGoldenIndividualReconnectDeadline
	}
	if len(authority.Connections.Receipts) > 0 &&
		now.Before(authority.Connections.Receipts[len(authority.Connections.Receipts)-1].OccurredAt) {
		return goldenIndividualConnectionError("connection time regressed")
	}
	return nil
}

func validateGoldenIndividualCommittedMutation(
	committed *GoldenIndividualConnectionLedger,
	next GoldenIndividualConnectionLedger,
	operation goldenIndividualConnectionOperation,
	digest [sha256.Size]byte,
	changed bool,
) error {
	if committed == nil || committed.Validate() != nil {
		return domain.ErrInternal
	}
	receipt, found := goldenIndividualReceiptByCommand(committed.Receipts, operation.commandID)
	if !found || !validGoldenIndividualCommittedLedger(committed, operation) ||
		!validGoldenIndividualCommittedReceipt(receipt, committed, operation, digest) {
		return domain.ErrInternal
	}
	if changed && !committed.Expectation().Equal(next.Expectation()) {
		return domain.ErrInternal
	}
	return nil
}

func validGoldenIndividualCommittedLedger(
	committed *GoldenIndividualConnectionLedger,
	operation goldenIndividualConnectionOperation,
) bool {
	return committed.Scope == operation.scope && committed.Execution.Equal(operation.expectedExecution) &&
		committed.Submissions.Equal(operation.expectedSubmissions) &&
		committed.RevisionID == operation.nextRevisionID &&
		committed.Revision == operation.expectedConnections.Revision+1
}

func validGoldenIndividualCommittedReceipt(
	receipt GoldenIndividualConnectionReceipt,
	committed *GoldenIndividualConnectionLedger,
	operation goldenIndividualConnectionOperation,
	digest [sha256.Size]byte,
) bool {
	return receipt.CommandDigest == digest && receipt.Kind == operation.kind &&
		receipt.ParticipantID == operation.participantID && receipt.IntervalID == operation.intervalID &&
		receipt.Expected.Equal(operation.expectedConnections) &&
		receipt.Expected.Execution.Equal(operation.expectedExecution) &&
		receipt.ObservedSubmissions.Equal(operation.expectedSubmissions) &&
		receipt.ResultRevisionID == operation.nextRevisionID && receipt.ResultRevision == committed.Revision
}

func goldenIndividualAuthorityMatches(
	authority GoldenIndividualDisconnectAuthority,
	operation goldenIndividualConnectionOperation,
) bool {
	execution := authority.Execution
	return execution.Start != nil && execution.Attempt.State == domain.ArenaGoldenAttemptStateActive &&
		execution.Wave.State == domain.ArenaWaveStateActive && execution.Expectation().Equal(operation.expectedExecution) &&
		authority.Submissions.Expectation().Equal(operation.expectedSubmissions) &&
		authority.Connections.Expectation().Equal(operation.expectedConnections) &&
		authority.Connections.Execution.Equal(operation.expectedExecution) &&
		authority.Connections.StartedAt.Equal(execution.Start.StartedAt) &&
		authority.Connections.Deadline.Equal(execution.Start.Deadline) &&
		authority.Connections.Scope == operation.scope && goldenIDsContain(execution.Membership.ParticipantIDs, operation.participantID) &&
		equalGoldenIDs(authority.Connections.ParticipantIDs, execution.Membership.ParticipantIDs)
}

func buildGoldenIndividualConnectionSuccessor(
	authority GoldenIndividualDisconnectAuthority,
	operation goldenIndividualConnectionOperation,
	digest [sha256.Size]byte,
	now time.Time,
) (GoldenIndividualConnectionLedger, []uuid.UUID, error) {
	current := authority.Connections
	if current.Revision == math.MaxInt64 {
		return GoldenIndividualConnectionLedger{}, nil, goldenIndividualConnectionError("ledger revision overflow")
	}
	ids, err := goldenIndividualSuccessorIdentityIDs(authority, operation)
	if err != nil {
		return GoldenIndividualConnectionLedger{}, nil, err
	}
	next := current.Snapshot()
	expected := current.Expectation()
	next.Submissions = authority.Submissions.Expectation()
	next.PreviousRevisionID = goldenUUID(next.RevisionID)
	next.RevisionID = operation.nextRevisionID
	next.Revision++
	if len(next.Receipts) >= goldenIndividualReceiptLimit {
		return GoldenIndividualConnectionLedger{}, nil, ErrGoldenIndividualConnectionUnavailable
	}
	if err := applyGoldenIndividualSuccessorTransition(&next, authority, operation, now); err != nil {
		return GoldenIndividualConnectionLedger{}, nil, err
	}
	next.Receipts = append(next.Receipts, GoldenIndividualConnectionReceipt{
		CommandID: operation.commandID, CommandDigest: digest, Kind: operation.kind,
		ParticipantID: operation.participantID, IntervalID: operation.intervalID,
		Expected: expected, ObservedSubmissions: authority.Submissions.Expectation(), ResultRevisionID: operation.nextRevisionID,
		ResultRevision: next.Revision, OccurredAt: now,
	})
	if err := rebuildGoldenIndividualConnectionLedger(&next); err != nil {
		return GoldenIndividualConnectionLedger{}, nil, err
	}
	return next.Snapshot(), ids, nil
}

func goldenIndividualSuccessorIdentityIDs(
	authority GoldenIndividualDisconnectAuthority,
	operation goldenIndividualConnectionOperation,
) ([]uuid.UUID, error) {
	ids := []uuid.UUID{operation.commandID, operation.nextRevisionID}
	if operation.kind == GoldenIndividualConnectionDisconnected {
		ids = append(ids, operation.intervalID)
	}
	canonicalGoldenIDs(ids)
	if !uniqueNonZeroUUIDs(ids) || goldenIndividualIdentityUsed(authority, ids) {
		return nil, goldenIndividualConnectionError("connection identity is reused")
	}
	return ids, nil
}

func applyGoldenIndividualSuccessorTransition(
	next *GoldenIndividualConnectionLedger,
	authority GoldenIndividualDisconnectAuthority,
	operation goldenIndividualConnectionOperation,
	now time.Time,
) error {
	switch operation.kind {
	case GoldenIndividualConnectionDisconnected:
		return applyGoldenIndividualDisconnectSuccessor(next, authority, operation, now)
	case GoldenIndividualConnectionReconnected:
		return applyGoldenIndividualReconnectSuccessor(next, operation, now)
	default:
		return domain.ErrValidation
	}
}

func applyGoldenIndividualDisconnectSuccessor(
	next *GoldenIndividualConnectionLedger,
	authority GoldenIndividualDisconnectAuthority,
	operation goldenIndividualConnectionOperation,
	now time.Time,
) error {
	if !next.IsPresent(operation.participantID) || goldenIndividualOpenInterval(next.Intervals, operation.participantID) != nil ||
		len(next.Intervals) >= goldenIndividualIntervalLimit ||
		goldenIndividualIntervalCount(next.Intervals, operation.participantID) >= ReconnectCycleLimit {
		return ErrGoldenIndividualConnectionUnavailable
	}
	next.PresentParticipantIDs = removeGoldenID(next.PresentParticipantIDs, operation.participantID)
	next.Intervals = append(next.Intervals, GoldenIndividualReconnectInterval{
		ID: operation.intervalID, ParticipantID: operation.participantID,
		Sequence: goldenIndividualIntervalCount(next.Intervals, operation.participantID) + 1,
		State:    GoldenIndividualReconnectOpen, DisconnectedAt: now, Deadline: authority.Execution.Start.Deadline,
	})
	return nil
}

func applyGoldenIndividualReconnectSuccessor(
	next *GoldenIndividualConnectionLedger,
	operation goldenIndividualConnectionOperation,
	now time.Time,
) error {
	interval := goldenIndividualIntervalByID(next.Intervals, operation.intervalID)
	if interval == nil || interval.ParticipantID != operation.participantID ||
		interval.State != GoldenIndividualReconnectOpen || next.IsPresent(operation.participantID) {
		return ErrGoldenIndividualConnectionUnavailable
	}
	interval.State = GoldenIndividualReconnectClosed
	interval.ReconnectedAt = cloneGoldenTime(&now)
	next.PresentParticipantIDs = append(next.PresentParticipantIDs, operation.participantID)
	canonicalGoldenIDs(next.PresentParticipantIDs)
	return nil
}

func validGoldenIndividualOperation(operation goldenIndividualConnectionOperation) bool {
	return operation.scope.IsValid() && operation.commandID != uuid.Nil && operation.participantID != uuid.Nil &&
		operation.intervalID != uuid.Nil && operation.nextRevisionID != uuid.Nil &&
		operation.expectedExecution.Scope == operation.scope.State && operation.expectedSubmissions.Scope == operation.scope &&
		operation.expectedConnections.Scope == operation.scope &&
		(operation.kind == GoldenIndividualConnectionDisconnected || operation.kind == GoldenIndividualConnectionReconnected)
}

func goldenIndividualIdentityUsed(authority GoldenIndividualDisconnectAuthority, ids []uuid.UUID) bool {
	reserved := goldenSubmissionRetainedIdentitySet(authority.Execution)
	reserved[authority.Submissions.RevisionID] = struct{}{}
	if authority.Submissions.PreviousRevisionID != nil {
		reserved[*authority.Submissions.PreviousRevisionID] = struct{}{}
	}
	for _, submission := range authority.Submissions.Submissions {
		reserved[submission.VerificationID] = struct{}{}
		reserved[submission.VerificationRevisionID] = struct{}{}
	}
	for _, receipt := range authority.Submissions.Receipts {
		reserved[receipt.CommandID] = struct{}{}
		reserved[receipt.ResultRevisionID] = struct{}{}
	}
	reserved[authority.Connections.RevisionID] = struct{}{}
	for _, interval := range authority.Connections.Intervals {
		reserved[interval.ID] = struct{}{}
	}
	for _, receipt := range authority.Connections.Receipts {
		reserved[receipt.CommandID] = struct{}{}
		reserved[receipt.ResultRevisionID] = struct{}{}
	}
	for _, id := range ids {
		if _, found := reserved[id]; found {
			return true
		}
	}
	return false
}

func goldenIndividualOpenInterval(
	intervals []GoldenIndividualReconnectInterval,
	participantID uuid.UUID,
) *GoldenIndividualReconnectInterval {
	for index := range intervals {
		if intervals[index].ParticipantID == participantID && intervals[index].State == GoldenIndividualReconnectOpen {
			return &intervals[index]
		}
	}
	return nil
}

func goldenIndividualIntervalByID(
	intervals []GoldenIndividualReconnectInterval,
	intervalID uuid.UUID,
) *GoldenIndividualReconnectInterval {
	for index := range intervals {
		if intervals[index].ID == intervalID {
			return &intervals[index]
		}
	}
	return nil
}

func goldenIndividualIntervalCount(intervals []GoldenIndividualReconnectInterval, participantID uuid.UUID) int {
	count := 0
	for _, interval := range intervals {
		if interval.ParticipantID == participantID {
			count++
		}
	}
	return count
}

func goldenIndividualReceiptByCommand(
	receipts []GoldenIndividualConnectionReceipt,
	commandID uuid.UUID,
) (GoldenIndividualConnectionReceipt, bool) {
	for _, receipt := range receipts {
		if receipt.CommandID == commandID {
			return receipt, true
		}
	}
	return GoldenIndividualConnectionReceipt{}, false
}

func reconcileGoldenIndividualReplay(
	replay GoldenIndividualConnectionReplay,
	operation goldenIndividualConnectionOperation,
	digest [sha256.Size]byte,
) (*GoldenIndividualConnectionLedger, bool, error) {
	if replay.Connections.Validate() != nil {
		return nil, false, domain.ErrInternal
	}
	if replay.Receipt.CommandID != operation.commandID {
		return nil, false, domain.ErrInternal
	}
	if replay.Receipt.CommandDigest != digest || replay.Receipt.Kind != operation.kind ||
		replay.Receipt.ParticipantID != operation.participantID || replay.Receipt.IntervalID != operation.intervalID {
		return nil, false, ErrGoldenIndividualConnectionCommandReuse
	}
	stored, found := goldenIndividualReceiptByCommand(replay.Connections.Receipts, operation.commandID)
	if !found || !reflect.DeepEqual(stored, replay.Receipt) {
		return nil, false, domain.ErrInternal
	}
	clone := replay.Connections.Snapshot()
	return &clone, false, nil
}

func rebuildGoldenIndividualConnectionLedger(ledger *GoldenIndividualConnectionLedger) error {
	if ledger == nil {
		return goldenIndividualConnectionError("nil connection ledger")
	}
	payload, err := goldenIndividualConnectionPayload(*ledger)
	if err != nil {
		return goldenIndividualConnectionError("encode connection ledger")
	}
	ledger.PayloadDigest = sha256.Sum256(payload)
	return ledger.Validate()
}

func goldenIndividualConnectionPayload(ledger GoldenIndividualConnectionLedger) ([]byte, error) {
	clone := ledger.Snapshot()
	clone.PayloadDigest = [sha256.Size]byte{}
	return goldenEncode(clone)
}

func goldenIndividualOperationDigest(operation goldenIndividualConnectionOperation) [sha256.Size]byte {
	document := struct {
		Scope               GoldenSubmissionScope
		CommandID           uuid.UUID
		ParticipantID       uuid.UUID
		IntervalID          uuid.UUID
		ExpectedExecution   GoldenWaveExecutionExpectation
		ExpectedSubmissions GoldenSubmissionLedgerExpectation
		ExpectedConnections GoldenIndividualConnectionExpectation
		NextRevisionID      uuid.UUID
		Kind                GoldenIndividualConnectionKind
	}{
		Scope: operation.scope, CommandID: operation.commandID, ParticipantID: operation.participantID,
		IntervalID: operation.intervalID, ExpectedExecution: operation.expectedExecution,
		ExpectedSubmissions: operation.expectedSubmissions, ExpectedConnections: operation.expectedConnections,
		NextRevisionID: operation.nextRevisionID, Kind: operation.kind,
	}
	payload, _ := goldenEncode(document)
	return sha256.Sum256(payload)
}

func goldenIndividualConnectionError(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidGoldenIndividualConnection, message)
}
