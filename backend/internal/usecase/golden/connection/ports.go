package golden

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenexecution "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/execution"
	goldensubmission "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/submission"
)

type GoldenSubmissionScope = goldensubmission.GoldenSubmissionScope
type GoldenSubmissionLedgerExpectation = goldensubmission.GoldenSubmissionLedgerExpectation
type GoldenSubmissionLedger = goldensubmission.GoldenSubmissionLedger
type GoldenWaveExecutionExpectation = goldenexecution.GoldenWaveExecutionExpectation
type GoldenWaveExecution = goldenexecution.GoldenWaveExecution

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
	sortConnectionIDs(participants)
	ledger := GoldenIndividualConnectionLedger{
		Scope: scope, Execution: cloneConnectionExecutionExpectation(execution), Submissions: submissions,
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
	clone.Execution = cloneConnectionExecutionExpectation(l.Execution)
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
		clone.Receipts[index].Expected.Execution = cloneConnectionExecutionExpectation(receipt.Expected.Execution)
	}
	return clone
}

func (l GoldenIndividualConnectionLedger) Expectation() GoldenIndividualConnectionExpectation {
	return GoldenIndividualConnectionExpectation{
		Scope: l.Scope, Execution: cloneConnectionExecutionExpectation(l.Execution), Submissions: l.Submissions,
		StartedAt: l.StartedAt, Deadline: l.Deadline,
		RevisionID: l.RevisionID, Revision: l.Revision, PayloadDigest: l.PayloadDigest,
	}
}

func (l GoldenIndividualConnectionLedger) IsPresent(participantID uuid.UUID) bool {
	return connectionContainsID(l.PresentParticipantIDs, participantID)
}

// ConnectionRepository owns the active-execution, submission-head and
// connection-ledger transaction.
type ConnectionRepository interface {
	FindGoldenIndividualConnectionCommand(ctx context.Context, tournamentID, commandID uuid.UUID) (*GoldenIndividualConnectionReplay, error)
	LoadGoldenIndividualDisconnectAuthority(ctx context.Context, scope GoldenSubmissionScope) (GoldenIndividualDisconnectAuthority, error)
	CommitGoldenIndividualConnection(ctx context.Context, commit GoldenIndividualConnectionCommit) (*GoldenIndividualConnectionLedger, bool, error)
}
