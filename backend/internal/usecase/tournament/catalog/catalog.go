package catalog

import (
	"bytes"
	"context"
	"errors"
	"sort"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/idempotency"
)

func (a *UseCase) ListTournaments(ctx context.Context, command usecase.TournamentListCommand) (usecase.TournamentPage, error) {
	if !a.isAvailable() {
		return usecase.TournamentPage{}, domain.ErrInternal
	}
	if !validOperatorIdentity(command.Operator) || !validListCommand(command) {
		return usecase.TournamentPage{}, domain.ErrValidation
	}
	filter := TournamentListFilter{}
	if command.State != "" {
		filter.States = []domain.TournamentState{command.State}
	}
	records, err := a.lister.ListTournaments(ctx, filter)
	if err != nil {
		return usecase.TournamentPage{}, err
	}
	records = append([]CatalogTournamentRecord(nil), records...)
	sort.Slice(records, func(i, j int) bool {
		if !records[i].CreatedAt.Equal(records[j].CreatedAt) {
			return records[i].CreatedAt.After(records[j].CreatedAt)
		}
		return bytes.Compare(records[i].ID[:], records[j].ID[:]) < 0
	})

	pageSize := command.PageSize
	if pageSize == 0 {
		pageSize = usecase.DefaultTournamentPageSize
	}
	start := tournamentPageStart(records, command.After)
	end := min(start+pageSize, len(records))
	items := make([]usecase.TournamentView, 0, end-start)
	for i := start; i < end; i++ {
		view, mapErr := tournamentView(records[i])
		if mapErr != nil {
			return usecase.TournamentPage{}, mapErr
		}
		items = append(items, view)
	}
	page := usecase.TournamentPage{Items: items}
	if end < len(records) && len(items) > 0 {
		last := items[len(items)-1]
		page.Next = &usecase.TournamentCursor{CreatedAt: last.CreatedAt, TournamentID: last.ID}
	}
	return page, nil
}

func (a *UseCase) CreateTournament(ctx context.Context, command usecase.TournamentCreateCommand) (usecase.TournamentResult, error) {
	if !a.isCreateAvailable() {
		return usecase.TournamentResult{}, domain.ErrInternal
	}
	if !validOperatorIdentity(command.Operator) || !validCreateCommand(command) {
		return usecase.TournamentResult{}, domain.ErrValidation
	}
	receipt, lease, acquired, err := a.beginCreateReceipt(ctx, command)
	if err != nil {
		return usecase.TournamentResult{}, err
	}
	durableSuccess := false
	if acquired {
		defer func() {
			a.finalizeCreateReceipt(ctx, receipt, lease, durableSuccess)
		}()
	}
	tournamentID, rosterID, err := a.createAggregateIDs(command.IdempotencyKey)
	if err != nil {
		return usecase.TournamentResult{}, err
	}
	createdAt := a.clock.Now()
	if !validServerTime(createdAt) {
		return usecase.TournamentResult{}, domain.ErrInternal
	}
	result, err := a.createStore.Create(ctx, CreateReceiptCommand{
		ActorID: command.Operator.ActorID, IdempotencyKey: receipt.ID, PayloadDigest: receipt.PayloadDigest,
		TournamentID: tournamentID, RosterID: rosterID, CreatedAt: createdAt,
	})
	if err != nil {
		if errors.Is(err, idempotency.ErrPayloadConflict) {
			return usecase.TournamentResult{}, domain.ErrConflict
		}
		return usecase.TournamentResult{}, err
	}
	if err := validateCreateResult(result, tournamentID, rosterID, command.Preset); err != nil {
		return usecase.TournamentResult{}, domain.ErrInternal
	}
	durableSuccess = true
	return cloneTournamentResult(result), nil
}

func (a *UseCase) createAggregateIDs(commandID uuid.UUID) (uuid.UUID, uuid.UUID, error) {
	tournamentID, err := a.ids.Derive(IDInput{
		Scope: IDScopeTournament, IdempotencyKey: commandID,
	})
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	rosterID, err := a.ids.Derive(IDInput{
		Scope: IDScopeRoster, IdempotencyKey: commandID,
	})
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	if tournamentID == uuid.Nil || rosterID == uuid.Nil || tournamentID == rosterID {
		return uuid.Nil, uuid.Nil, domain.ErrInternal
	}
	return tournamentID, rosterID, nil
}

func tournamentPageStart(records []CatalogTournamentRecord, after *usecase.TournamentCursor) int {
	if after == nil {
		return 0
	}
	return sort.Search(len(records), func(index int) bool {
		record := records[index]
		if !record.CreatedAt.Equal(after.CreatedAt) {
			return record.CreatedAt.Before(after.CreatedAt)
		}
		return bytes.Compare(record.ID[:], after.TournamentID[:]) > 0
	})
}
