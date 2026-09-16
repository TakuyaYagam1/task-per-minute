package participant

import (
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	staterepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/state"
)

// ParticipantStatePostgres remains a root participant facade while the
// implementation lives in the participant state capability package.
type ParticipantStatePostgres = staterepo.ParticipantStatePostgres

// ErrParticipantStateInvalid preserves the historical participant package
// error while delegating its definition to the state capability.
var ErrParticipantStateInvalid = staterepo.ErrParticipantStateInvalid

// NewParticipantStatePostgres preserves the root participant constructor.
func NewParticipantStatePostgres(tx *db.TxManager) *ParticipantStatePostgres {
	return staterepo.NewParticipantStatePostgres(tx)
}
