package catalog

import (
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/idempotency"
)

type Dependencies struct {
	IDs           IDGenerator
	Clock         Clock
	Lister        TournamentLister
	CreateStore   TournamentCreateStore
	Receipts      idempotency.Store
	ContentReader ContentReader
}

type UseCase struct {
	ids           IDGenerator
	clock         Clock
	lister        TournamentLister
	createStore   TournamentCreateStore
	receipts      idempotency.Store
	contentReader ContentReader
}

func NewUseCase(deps Dependencies) *UseCase {
	return &UseCase{
		ids:           deps.IDs,
		clock:         deps.Clock,
		lister:        deps.Lister,
		createStore:   deps.CreateStore,
		receipts:      deps.Receipts,
		contentReader: deps.ContentReader,
	}
}

func (a *UseCase) isAvailable() bool {
	return a != nil && a.lister != nil
}

func (a *UseCase) isCreateAvailable() bool {
	return a != nil && a.ids != nil && a.clock != nil && a.createStore != nil && a.receipts != nil
}

func (a *UseCase) isContentAvailable() bool {
	return a != nil && a.contentReader != nil
}

var _ usecase.TournamentUseCase = (*UseCase)(nil)
