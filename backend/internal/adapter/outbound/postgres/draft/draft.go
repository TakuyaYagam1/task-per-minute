package draft

import (
	draftrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment/draft"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
)

type DraftPersistenceState = draftrepo.DraftPersistenceState

const (
	DraftPersistenceStateActive           = draftrepo.DraftPersistenceStateActive
	DraftPersistenceStatePaused           = draftrepo.DraftPersistenceStatePaused
	DraftPersistenceStateRecoveryRequired = draftrepo.DraftPersistenceStateRecoveryRequired
	DraftPersistenceStateCompleted        = draftrepo.DraftPersistenceStateCompleted
	DraftPersistenceStateSuperseded       = draftrepo.DraftPersistenceStateSuperseded
)

var ErrDraftNotFound = draftrepo.ErrDraftNotFound

type DraftPostgres = draftrepo.DraftPostgres
type DraftCreateInput = draftrepo.DraftCreateInput
type DraftRevisionExpectation = draftrepo.DraftRevisionExpectation
type DraftActionInput = draftrepo.DraftActionInput
type DraftRevisionInput = draftrepo.DraftRevisionInput
type DraftIdentityRecord = draftrepo.DraftIdentityRecord
type DraftRevisionRecord = draftrepo.DraftRevisionRecord
type DraftActionRecord = draftrepo.DraftActionRecord
type DraftAggregate = draftrepo.DraftAggregate

func NewDraftPostgres(tx *db.TxManager) *DraftPostgres {
	return draftrepo.NewDraftPostgres(tx)
}
