package golden

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenattempt "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/attempt"
)

type AttemptClock = goldenattempt.AttemptClock
type AttemptRepository = goldenattempt.AttemptRepository

type GoldenSwissPointLedgerSentinel = goldenattempt.GoldenSwissPointLedgerSentinel
type GoldenPositionOrderEntry = goldenattempt.GoldenPositionOrderEntry
type GoldenAttemptOrderingEvidence = goldenattempt.GoldenAttemptOrderingEvidence
type GoldenCommittedPosition = goldenattempt.GoldenCommittedPosition
type GoldenPositionLedgerExpectation = goldenattempt.GoldenPositionLedgerExpectation
type GoldenPositionLedger = goldenattempt.GoldenPositionLedger
type GoldenReserveAttemptAssignment = goldenattempt.GoldenReserveAttemptAssignment
type GoldenAttemptTerminalReason = goldenattempt.GoldenAttemptTerminalReason
type GoldenAttemptCommitCommand = goldenattempt.GoldenAttemptCommitCommand
type GoldenAttemptCommitAuthority = goldenattempt.GoldenAttemptCommitAuthority
type GoldenAttemptCommitUseCase = goldenattempt.GoldenAttemptCommitUseCase
type GoldenAttemptCommitRecord = goldenattempt.GoldenAttemptCommitRecord

var (
	ErrInvalidGoldenAttemptCommit           = goldenattempt.ErrInvalidGoldenAttemptCommit
	ErrGoldenAttemptCommitAuthorityConflict = goldenattempt.ErrGoldenAttemptCommitAuthorityConflict
	ErrGoldenAttemptCommitConflict          = goldenattempt.ErrGoldenAttemptCommitConflict
	ErrGoldenAttemptCommitCommandReuse      = goldenattempt.ErrGoldenAttemptCommitCommandReuse
	ErrGoldenAttemptNotTerminal             = goldenattempt.ErrGoldenAttemptNotTerminal
	ErrInvalidGoldenReserveAssignment       = goldenattempt.ErrInvalidGoldenReserveAssignment
)

const (
	GoldenAttemptTerminalAllSolved = goldenattempt.GoldenAttemptTerminalAllSolved
	GoldenAttemptTerminalDeadline  = goldenattempt.GoldenAttemptTerminalDeadline
)

func NewGoldenPositionLedger(
	scope GoldenStateScope,
	positionFrom, positionTo int,
	revisionID uuid.UUID,
) (GoldenPositionLedger, error) {
	return goldenattempt.NewGoldenPositionLedger(scope, positionFrom, positionTo, revisionID)
}

func NewGoldenAttemptCommitUseCase(
	repository AttemptRepository,
	clock AttemptClock,
) *GoldenAttemptCommitUseCase {
	return goldenattempt.NewGoldenAttemptCommitUseCase(repository, clock)
}

func ReservePositionLedgerIdentities(
	reserved map[uuid.UUID]struct{},
	ledger GoldenPositionLedger,
) {
	goldenattempt.ReservePositionLedgerIdentities(reserved, ledger)
}

func PositionLedgerMatchesGroup(
	ledger GoldenPositionLedger,
	group domain.GoldenGroupState,
) bool {
	return goldenattempt.PositionLedgerMatchesGroup(ledger, group)
}

func PositionParticipantsBelongToGroup(
	ledger GoldenPositionLedger,
	members map[uuid.UUID]struct{},
) bool {
	return goldenattempt.PositionParticipantsBelongToGroup(ledger, members)
}
