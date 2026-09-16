package postgres

import "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment/draft"

type DraftPersistenceState = draft.DraftPersistenceState

const (
	DraftPersistenceStateActive           = draft.DraftPersistenceStateActive
	DraftPersistenceStatePaused           = draft.DraftPersistenceStatePaused
	DraftPersistenceStateRecoveryRequired = draft.DraftPersistenceStateRecoveryRequired
	DraftPersistenceStateCompleted        = draft.DraftPersistenceStateCompleted
	DraftPersistenceStateSuperseded       = draft.DraftPersistenceStateSuperseded
)

var ErrDraftNotFound = draft.ErrDraftNotFound

type DraftPostgres = draft.DraftPostgres

type DraftCreateInput = draft.DraftCreateInput

type DraftRevisionExpectation = draft.DraftRevisionExpectation

type DraftActionInput = draft.DraftActionInput

type DraftRevisionInput = draft.DraftRevisionInput

type DraftIdentityRecord = draft.DraftIdentityRecord

type DraftRevisionRecord = draft.DraftRevisionRecord

type DraftActionRecord = draft.DraftActionRecord

type DraftAggregate = draft.DraftAggregate

func NewDraftPostgres(tx *TxManager) *DraftPostgres {
	return draft.NewDraftPostgres(tx)
}
