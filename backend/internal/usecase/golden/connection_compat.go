package golden

import (
	"time"

	"github.com/google/uuid"

	goldenconnection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/connection"
)

type ConnectionClock = goldenconnection.ConnectionClock
type GoldenIndividualConnectionKind = goldenconnection.GoldenIndividualConnectionKind
type GoldenIndividualReconnectState = goldenconnection.GoldenIndividualReconnectState
type GoldenIndividualReconnectInterval = goldenconnection.GoldenIndividualReconnectInterval
type GoldenIndividualConnectionExpectation = goldenconnection.GoldenIndividualConnectionExpectation
type GoldenIndividualConnectionReceipt = goldenconnection.GoldenIndividualConnectionReceipt
type GoldenIndividualConnectionLedger = goldenconnection.GoldenIndividualConnectionLedger
type GoldenIndividualConnectionReplay = goldenconnection.GoldenIndividualConnectionReplay
type GoldenIndividualDisconnectAuthority = goldenconnection.GoldenIndividualDisconnectAuthority
type GoldenIndividualConnectionCommit = goldenconnection.GoldenIndividualConnectionCommit
type GoldenIndividualDisconnectCommand = goldenconnection.GoldenIndividualDisconnectCommand
type GoldenIndividualReconnectCommand = goldenconnection.GoldenIndividualReconnectCommand
type GoldenIndividualDisconnectUseCase = goldenconnection.GoldenIndividualDisconnectUseCase
type ConnectionRepository = goldenconnection.ConnectionRepository

var (
	ErrInvalidGoldenIndividualConnection           = goldenconnection.ErrInvalidGoldenIndividualConnection
	ErrGoldenIndividualConnectionAuthorityConflict = goldenconnection.ErrGoldenIndividualConnectionAuthorityConflict
	ErrGoldenIndividualConnectionConflict          = goldenconnection.ErrGoldenIndividualConnectionConflict
	ErrGoldenIndividualConnectionCommandReuse      = goldenconnection.ErrGoldenIndividualConnectionCommandReuse
	ErrGoldenIndividualConnectionUnavailable       = goldenconnection.ErrGoldenIndividualConnectionUnavailable
	ErrGoldenIndividualReconnectDeadline           = goldenconnection.ErrGoldenIndividualReconnectDeadline
)

const (
	GoldenIndividualConnectionDisconnected = goldenconnection.GoldenIndividualConnectionDisconnected
	GoldenIndividualConnectionReconnected  = goldenconnection.GoldenIndividualConnectionReconnected
	GoldenIndividualReconnectOpen          = goldenconnection.GoldenIndividualReconnectOpen
	GoldenIndividualReconnectClosed        = goldenconnection.GoldenIndividualReconnectClosed
)

func NewGoldenIndividualConnectionLedger(
	scope GoldenSubmissionScope,
	execution GoldenWaveExecutionExpectation,
	submissions GoldenSubmissionLedgerExpectation,
	startedAt time.Time,
	deadline time.Time,
	revisionID uuid.UUID,
	participantIDs []uuid.UUID,
) (GoldenIndividualConnectionLedger, error) {
	return goldenconnection.NewGoldenIndividualConnectionLedger(
		scope, execution, submissions, startedAt, deadline, revisionID, participantIDs,
	)
}

func NewGoldenIndividualDisconnectUseCase(
	repository ConnectionRepository,
	clock ConnectionClock,
) *GoldenIndividualDisconnectUseCase {
	return goldenconnection.NewGoldenIndividualDisconnectUseCase(repository, clock)
}
