package catalog_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/idempotency"
	idempotencymocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/idempotency/mocks"
	catalogusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/catalog"
	tournamentmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/catalog/mocks"
)

func TestUseCase_ListTournaments(t *testing.T) {
	t.Parallel()

	ids := tournamentmocks.NewMockIDGenerator(t)
	lister := tournamentmocks.NewMockTournamentLister(t)
	application := catalogusecase.NewUseCase(catalogusecase.Dependencies{
		IDs: ids, Lister: lister,
	})

	older := tournamentRecordFixture(
		uuid.MustParse("10000000-0000-0000-0000-000000000001"),
		time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC),
	)
	newer := tournamentRecordFixture(
		uuid.MustParse("20000000-0000-0000-0000-000000000002"),
		older.CreatedAt.Add(time.Minute),
	)
	records := []catalogusecase.CatalogTournamentRecord{older, newer}
	lister.EXPECT().ListTournaments(mock.Anything, catalogusecase.TournamentListFilter{
		States: []domain.TournamentState{domain.TournamentStateDraft},
	}).Return(records, nil).Once()

	page, err := application.ListTournaments(t.Context(), inbound.TournamentListCommand{
		Operator: inbound.OperatorIdentity{ActorID: uuid.New()},
		State:    domain.TournamentStateDraft,
		PageSize: 1,
	})

	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, newer.ID, page.Items[0].ID)
	require.NotNil(t, page.Next)
	require.Equal(t, newer.ID, page.Next.TournamentID)
	require.Equal(t, older.ID, records[0].ID)
}

func TestUseCase_CreateTournament(t *testing.T) {
	t.Parallel()

	commandID := uuid.MustParse("30000000-0000-0000-0000-000000000003")
	tournamentID := uuid.MustParse("40000000-0000-0000-0000-000000000004")
	rosterID := uuid.MustParse("50000000-0000-0000-0000-000000000005")
	createdAt := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	ids := tournamentmocks.NewMockIDGenerator(t)
	clock := tournamentmocks.NewMockClock(t)
	createStore := tournamentmocks.NewMockTournamentCreateStore(t)
	receipts := idempotencymocks.NewMockStore(t)
	application := catalogusecase.NewUseCase(catalogusecase.Dependencies{
		IDs: ids, Clock: clock, CreateStore: createStore, Receipts: receipts,
	})
	receipt := createReceiptMatcher(commandID)
	var acquiredLease idempotency.LeaseToken
	receipts.EXPECT().Begin(mock.Anything, receipt, mock.Anything).
		RunAndReturn(func(_ context.Context, _ idempotency.Command, lease idempotency.LeaseToken) (idempotency.BeginResult, error) {
			acquiredLease = lease
			return idempotency.BeginResult{Disposition: idempotency.BeginAcquired, Lease: lease}, nil
		}).Once()
	receipts.EXPECT().MarkSucceeded(mock.Anything, receipt, mock.MatchedBy(func(lease idempotency.LeaseToken) bool {
		return lease == acquiredLease && lease != (idempotency.LeaseToken{})
	})).Return(nil).Once()
	ids.EXPECT().Derive(catalogusecase.IDInput{
		Scope: catalogusecase.IDScopeTournament, IdempotencyKey: commandID,
	}).Return(tournamentID, nil).Once()
	ids.EXPECT().Derive(catalogusecase.IDInput{
		Scope: catalogusecase.IDScopeRoster, IdempotencyKey: commandID,
	}).Return(rosterID, nil).Once()
	clock.EXPECT().Now().Return(createdAt).Once()
	createStore.EXPECT().Create(mock.Anything, createReceiptCommandMatcher(
		commandID,
		tournamentID,
		rosterID,
		createdAt,
	)).Return(tournamentResultFixture(tournamentID, rosterID, createdAt), nil).Once()

	result, err := application.CreateTournament(t.Context(), tournamentCreateCommand(commandID))

	require.NoError(t, err)
	require.True(t, result.Changed)
	require.Equal(t, tournamentID, result.Tournament.ID)
	require.Equal(t, rosterID, result.Tournament.RosterID)
}

func TestUseCase_CreateTournamentPanicBeforeDurableResultMarksReceiptFailed(t *testing.T) {
	t.Parallel()

	commandID := uuid.MustParse("51000000-0000-0000-0000-000000000001")
	tournamentID := uuid.MustParse("51000000-0000-0000-0000-000000000002")
	rosterID := uuid.MustParse("51000000-0000-0000-0000-000000000003")
	createdAt := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	panicValue := errors.New("durable store interrupted after commit")
	ids := tournamentmocks.NewMockIDGenerator(t)
	clock := tournamentmocks.NewMockClock(t)
	createStore := tournamentmocks.NewMockTournamentCreateStore(t)
	receipts := idempotencymocks.NewMockStore(t)
	application := catalogusecase.NewUseCase(catalogusecase.Dependencies{
		IDs: ids, Clock: clock, CreateStore: createStore, Receipts: receipts,
	})
	receipt := createReceiptMatcher(commandID)
	receipts.EXPECT().Begin(mock.Anything, receipt, mock.Anything).
		RunAndReturn(createBeginResult(idempotency.BeginAcquired)).Once()
	receipts.EXPECT().MarkFailed(mock.Anything, receipt, mock.Anything).Return(nil).Once()
	ids.EXPECT().Derive(catalogusecase.IDInput{
		Scope: catalogusecase.IDScopeTournament, IdempotencyKey: commandID,
	}).Return(tournamentID, nil).Once()
	ids.EXPECT().Derive(catalogusecase.IDInput{
		Scope: catalogusecase.IDScopeRoster, IdempotencyKey: commandID,
	}).Return(rosterID, nil).Once()
	clock.EXPECT().Now().Return(createdAt).Once()
	createStore.EXPECT().Create(mock.Anything, createReceiptCommandMatcher(
		commandID,
		tournamentID,
		rosterID,
		createdAt,
	)).Run(func(context.Context, catalogusecase.CreateReceiptCommand) {
		panic(panicValue)
	}).Return(inbound.TournamentResult{}, nil).Once()

	require.PanicsWithValue(t, panicValue, func() {
		_, _ = application.CreateTournament(t.Context(), tournamentCreateCommand(commandID))
	})
}

func TestUseCase_CreateTournamentReadsDurableReceiptAfterCacheSuccess(t *testing.T) {
	t.Parallel()

	commandID := uuid.MustParse("60000000-0000-0000-0000-000000000006")
	tournamentID := uuid.MustParse("70000000-0000-0000-0000-000000000007")
	rosterID := uuid.MustParse("80000000-0000-0000-0000-000000000008")
	createdAt := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	ids := tournamentmocks.NewMockIDGenerator(t)
	clock := tournamentmocks.NewMockClock(t)
	createStore := tournamentmocks.NewMockTournamentCreateStore(t)
	receipts := idempotencymocks.NewMockStore(t)
	application := catalogusecase.NewUseCase(catalogusecase.Dependencies{
		IDs: ids, Clock: clock, CreateStore: createStore, Receipts: receipts,
	})
	receipt := createReceiptMatcher(commandID)
	receipts.EXPECT().Begin(mock.Anything, receipt, mock.Anything).
		RunAndReturn(createBeginResult(idempotency.BeginSucceeded)).Once()
	ids.EXPECT().Derive(catalogusecase.IDInput{
		Scope: catalogusecase.IDScopeTournament, IdempotencyKey: commandID,
	}).Return(tournamentID, nil).Once()
	ids.EXPECT().Derive(catalogusecase.IDInput{
		Scope: catalogusecase.IDScopeRoster, IdempotencyKey: commandID,
	}).Return(rosterID, nil).Once()
	clock.EXPECT().Now().Return(createdAt).Once()
	durable := tournamentResultFixture(tournamentID, rosterID, createdAt)
	createStore.EXPECT().Create(mock.Anything, createReceiptCommandMatcher(
		commandID,
		tournamentID,
		rosterID,
		createdAt,
	)).Return(durable, nil).Once()

	result, err := application.CreateTournament(t.Context(), tournamentCreateCommand(commandID))

	require.NoError(t, err)
	require.Equal(t, durable, result)
}

func TestUseCase_CreateTournamentRejectsInFlightCommand(t *testing.T) {
	t.Parallel()

	commandID := uuid.MustParse("90000000-0000-0000-0000-000000000009")
	ids := tournamentmocks.NewMockIDGenerator(t)
	clock := tournamentmocks.NewMockClock(t)
	createStore := tournamentmocks.NewMockTournamentCreateStore(t)
	receipts := idempotencymocks.NewMockStore(t)
	application := catalogusecase.NewUseCase(catalogusecase.Dependencies{
		IDs: ids, Clock: clock, CreateStore: createStore, Receipts: receipts,
	})
	receipts.EXPECT().Begin(mock.Anything, createReceiptMatcher(commandID), mock.Anything).
		RunAndReturn(createBeginResult(idempotency.BeginInFlight)).Once()

	_, err := application.CreateTournament(t.Context(), tournamentCreateCommand(commandID))

	require.ErrorIs(t, err, domain.ErrConflict)
}

func TestUseCase_CreateTournamentMarksCacheFailedOnDurablePayloadConflict(t *testing.T) {
	t.Parallel()

	commandID := uuid.MustParse("a0000000-0000-0000-0000-000000000001")
	tournamentID := uuid.MustParse("a0000000-0000-0000-0000-000000000002")
	rosterID := uuid.MustParse("a0000000-0000-0000-0000-000000000003")
	createdAt := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	ids := tournamentmocks.NewMockIDGenerator(t)
	clock := tournamentmocks.NewMockClock(t)
	createStore := tournamentmocks.NewMockTournamentCreateStore(t)
	receipts := idempotencymocks.NewMockStore(t)
	application := catalogusecase.NewUseCase(catalogusecase.Dependencies{
		IDs: ids, Clock: clock, CreateStore: createStore, Receipts: receipts,
	})
	receipt := createReceiptMatcher(commandID)
	receipts.EXPECT().Begin(mock.Anything, receipt, mock.Anything).
		RunAndReturn(createBeginResult(idempotency.BeginAcquired)).Once()
	receipts.EXPECT().MarkFailed(mock.Anything, receipt, mock.Anything).Return(nil).Once()
	ids.EXPECT().Derive(catalogusecase.IDInput{
		Scope: catalogusecase.IDScopeTournament, IdempotencyKey: commandID,
	}).Return(tournamentID, nil).Once()
	ids.EXPECT().Derive(catalogusecase.IDInput{
		Scope: catalogusecase.IDScopeRoster, IdempotencyKey: commandID,
	}).Return(rosterID, nil).Once()
	clock.EXPECT().Now().Return(createdAt).Once()
	createStore.EXPECT().Create(mock.Anything, createReceiptCommandMatcher(
		commandID,
		tournamentID,
		rosterID,
		createdAt,
	)).Return(inbound.TournamentResult{}, idempotency.ErrPayloadConflict).Once()

	_, err := application.CreateTournament(t.Context(), tournamentCreateCommand(commandID))

	require.ErrorIs(t, err, domain.ErrConflict)
}

func TestUseCase_CreateTournamentMarksCacheFailedOnTransientDurableFailure(t *testing.T) {
	t.Parallel()

	commandID := uuid.MustParse("a1000000-0000-0000-0000-000000000001")
	tournamentID := uuid.MustParse("a1000000-0000-0000-0000-000000000002")
	rosterID := uuid.MustParse("a1000000-0000-0000-0000-000000000003")
	createdAt := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	transient := errors.New("postgres temporarily unavailable")
	ids := tournamentmocks.NewMockIDGenerator(t)
	clock := tournamentmocks.NewMockClock(t)
	createStore := tournamentmocks.NewMockTournamentCreateStore(t)
	receipts := idempotencymocks.NewMockStore(t)
	application := catalogusecase.NewUseCase(catalogusecase.Dependencies{
		IDs: ids, Clock: clock, CreateStore: createStore, Receipts: receipts,
	})
	receipt := createReceiptMatcher(commandID)
	receipts.EXPECT().Begin(mock.Anything, receipt, mock.Anything).
		RunAndReturn(createBeginResult(idempotency.BeginAcquired)).Once()
	receipts.EXPECT().MarkFailed(mock.Anything, receipt, mock.Anything).Return(nil).Once()
	ids.EXPECT().Derive(catalogusecase.IDInput{
		Scope: catalogusecase.IDScopeTournament, IdempotencyKey: commandID,
	}).Return(tournamentID, nil).Once()
	ids.EXPECT().Derive(catalogusecase.IDInput{
		Scope: catalogusecase.IDScopeRoster, IdempotencyKey: commandID,
	}).Return(rosterID, nil).Once()
	clock.EXPECT().Now().Return(createdAt).Once()
	createStore.EXPECT().Create(mock.Anything, createReceiptCommandMatcher(
		commandID,
		tournamentID,
		rosterID,
		createdAt,
	)).Return(inbound.TournamentResult{}, transient).Once()

	_, err := application.CreateTournament(t.Context(), tournamentCreateCommand(commandID))

	require.ErrorIs(t, err, transient)
}

func TestUseCase_CreateTournamentKeepsDurableSuccessWhenCacheFinalizationFails(t *testing.T) {
	t.Parallel()

	commandID := uuid.MustParse("b0000000-0000-0000-0000-000000000001")
	tournamentID := uuid.MustParse("b0000000-0000-0000-0000-000000000002")
	rosterID := uuid.MustParse("b0000000-0000-0000-0000-000000000003")
	createdAt := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	ids := tournamentmocks.NewMockIDGenerator(t)
	clock := tournamentmocks.NewMockClock(t)
	createStore := tournamentmocks.NewMockTournamentCreateStore(t)
	receipts := idempotencymocks.NewMockStore(t)
	application := catalogusecase.NewUseCase(catalogusecase.Dependencies{
		IDs: ids, Clock: clock, CreateStore: createStore, Receipts: receipts,
	})
	receipt := createReceiptMatcher(commandID)
	receipts.EXPECT().Begin(mock.Anything, receipt, mock.Anything).
		RunAndReturn(createBeginResult(idempotency.BeginAcquired)).Once()
	receipts.EXPECT().MarkSucceeded(mock.Anything, receipt, mock.Anything).Return(errors.New("cache unavailable")).Once()
	receipts.EXPECT().MarkFailed(mock.Anything, receipt, mock.Anything).Return(nil).Once()
	ids.EXPECT().Derive(catalogusecase.IDInput{
		Scope: catalogusecase.IDScopeTournament, IdempotencyKey: commandID,
	}).Return(tournamentID, nil).Once()
	ids.EXPECT().Derive(catalogusecase.IDInput{
		Scope: catalogusecase.IDScopeRoster, IdempotencyKey: commandID,
	}).Return(rosterID, nil).Once()
	clock.EXPECT().Now().Return(createdAt).Once()
	durable := tournamentResultFixture(tournamentID, rosterID, createdAt)
	createStore.EXPECT().Create(mock.Anything, createReceiptCommandMatcher(
		commandID,
		tournamentID,
		rosterID,
		createdAt,
	)).Return(durable, nil).Once()

	result, err := application.CreateTournament(t.Context(), tournamentCreateCommand(commandID))

	require.NoError(t, err)
	require.Equal(t, durable, result)
}

func createReceiptMatcher(commandID uuid.UUID) interface{} {
	return mock.MatchedBy(func(command idempotency.Command) bool {
		return command.Namespace == "tournament-create" && command.ID == commandID &&
			command.PayloadDigest != [32]byte{}
	})
}

func tournamentCreateCommand(commandID uuid.UUID) inbound.TournamentCreateCommand {
	return inbound.TournamentCreateCommand{
		Operator: inbound.OperatorIdentity{ActorID: uuid.New()}, IdempotencyKey: commandID,
		Preset: domain.TournamentPresetV1, Name: "September Invitational", PublicID: "september-invitational",
		PlannedRosterSize: 8, ContentRevision: 1,
	}
}

func createBeginResult(
	disposition idempotency.BeginDisposition,
) func(context.Context, idempotency.Command, idempotency.LeaseToken) (idempotency.BeginResult, error) {
	return func(_ context.Context, _ idempotency.Command, lease idempotency.LeaseToken) (idempotency.BeginResult, error) {
		result := idempotency.BeginResult{Disposition: disposition}
		if disposition == idempotency.BeginAcquired {
			result.Lease = lease
		}
		return result, nil
	}
}

func createReceiptCommandMatcher(
	commandID uuid.UUID,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	createdAt time.Time,
) interface{} {
	return mock.MatchedBy(func(command catalogusecase.CreateReceiptCommand) bool {
		return command.IdempotencyKey == commandID && command.TournamentID == tournamentID &&
			command.RosterID == rosterID && command.ActorID != uuid.Nil &&
			command.Name == "September Invitational" && command.PublicID == "september-invitational" &&
			command.PlannedRosterSize == 8 && command.ContentRevision == 1 &&
			command.PayloadDigest != [32]byte{} && command.CreatedAt.Equal(createdAt)
	})
}

func tournamentResultFixture(
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	createdAt time.Time,
) inbound.TournamentResult {
	return inbound.TournamentResult{
		Tournament: inbound.TournamentView{
			ID: tournamentID, RosterID: rosterID, Preset: domain.TournamentPresetV1,
			Name: "September Invitational", PublicID: "september-invitational",
			PlannedRosterSize: 8, ContentRevision: 1,
			State: domain.TournamentStateDraft, Revision: 1, CreatedAt: createdAt, UpdatedAt: createdAt,
		},
		Changed: true,
	}
}

func tournamentRecordFixture(id uuid.UUID, createdAt time.Time) catalogusecase.CatalogTournamentRecord {
	return catalogusecase.CatalogTournamentRecord{
		ID: id, RosterID: uuid.New(), Preset: domain.TournamentPresetV1,
		Name: "September Invitational", PublicID: "september-invitational",
		PlannedRosterSize: 8, ContentRevision: 1,
		State: domain.TournamentStateDraft, Revision: 1,
		CreatedAt: createdAt, UpdatedAt: createdAt,
	}
}
