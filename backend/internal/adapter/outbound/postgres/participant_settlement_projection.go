package postgres

import (
	"github.com/google/uuid"

	settlementpostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/settlement"
)

// participantSettlementLedgerEntry remains a root type alias for terminal
// Swiss projection code that still lives in this package during migration.
type participantSettlementLedgerEntry = settlementpostgres.SettlementLedgerEntry

// participantSettlementStandingsDependencies keeps the root terminal
// materializer source-compatible without moving its implementation back here.
func participantSettlementStandingsDependencies(
	commandID uuid.UUID,
	ledger []participantSettlementLedgerEntry,
) ([]ProjectionDependencyInput, error) {
	return settlementpostgres.ParticipantSettlementStandingsDependencies(commandID, ledger)
}
