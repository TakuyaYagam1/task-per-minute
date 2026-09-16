package postgres

import (
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/golden/store"
)

var (
	ErrGoldenRepositoryInvalid      = store.ErrGoldenRepositoryInvalid
	ErrGoldenRepositoryConflict     = store.ErrGoldenRepositoryConflict
	ErrGoldenRepositoryCommandReuse = store.ErrGoldenRepositoryCommandReuse
)

type GoldenRepositoryStateCommandKind = store.GoldenRepositoryStateCommandKind

const (
	GoldenRepositoryStateReady      = store.GoldenRepositoryStateReady
	GoldenRepositoryStateNoShow     = store.GoldenRepositoryStateNoShow
	GoldenRepositoryStateAllocation = store.GoldenRepositoryStateAllocation
)

type GoldenRepositoryScope = store.GoldenRepositoryScope
type GoldenRepositoryRevision = store.GoldenRepositoryRevision
type GoldenRepositoryCommand = store.GoldenRepositoryCommand
type GoldenRepositoryCommit = store.GoldenRepositoryCommit
type GoldenRepositoryStore = store.GoldenRepositoryStore
type GoldenRepositoryStorePostgres = store.GoldenRepositoryStorePostgres

// NewGoldenRepositoryStorePostgres constructs the root-package facade for the
// Golden repository store.
func NewGoldenRepositoryStorePostgres(tx *TxManager) *GoldenRepositoryStorePostgres {
	return store.NewGoldenRepositoryStorePostgres(tx)
}
