package postgres

import (
	"github.com/google/uuid"

	swissrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/swiss"
)

var ErrSwissRoundNotFound = swissrepo.ErrSwissRoundNotFound

type SwissPostgres = swissrepo.SwissPostgres

type SwissRoundMeta = swissrepo.SwissRoundMeta

type AutomaticSwissRoundInput = swissrepo.AutomaticSwissRoundInput

type ManualSwissRoundInput = swissrepo.ManualSwissRoundInput

type SwissRoundRecord = swissrepo.SwissRoundRecord

type SwissPairingRecord = swissrepo.SwissPairingRecord

type SwissRoundAggregate = swissrepo.SwissRoundAggregate

func NewSwissPostgres(tx *TxManager) *SwissPostgres {
	return swissrepo.NewSwissPostgres(tx)
}

func nullableUUIDValue(value uuid.UUID) uuid.NullUUID {
	return uuid.NullUUID{UUID: value, Valid: value != uuid.Nil}
}
