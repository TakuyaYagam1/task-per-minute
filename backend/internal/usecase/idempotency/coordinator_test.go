package idempotency_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/idempotency"
	idempotencymocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/idempotency/mocks"
)

func TestCoordinatorRejectsInFlightDuplicateWithoutInvokingMutation(t *testing.T) {
	t.Parallel()

	store := idempotencymocks.NewMockStore(t)
	receipt := coordinatorReceipt(t)
	expectBegin(store, receipt, idempotency.BeginInFlight)

	called := false
	_, err := idempotency.Execute(
		t.Context(),
		idempotency.NewCoordinator(store),
		receipt,
		func(context.Context) (string, error) {
			called = true
			return "", nil
		},
	)
	var inFlight *idempotency.InFlightError
	require.ErrorAs(t, err, &inFlight)
	require.ErrorIs(t, err, domain.ErrConflict)
	require.False(t, called)
}

func TestCoordinatorReplaysCompletedReceiptThroughAuthoritativeOutcome(t *testing.T) {
	t.Parallel()

	store := idempotencymocks.NewMockStore(t)
	receipt := coordinatorReceipt(t)
	expectBegin(store, receipt, idempotency.BeginSucceeded)

	result, err := idempotency.Execute(
		t.Context(),
		idempotency.NewCoordinator(store),
		receipt,
		func(context.Context) (string, error) {
			return "durable-db-replay", nil
		},
	)
	require.NoError(t, err)
	require.Equal(t, "durable-db-replay", result)
}

func TestCoordinatorReopensFailedReceiptForTransientRetry(t *testing.T) {
	t.Parallel()

	store := idempotencymocks.NewMockStore(t)
	receipt := coordinatorReceipt(t)
	transient := errors.New("temporary database interruption")
	expectBegin(store, receipt, idempotency.BeginAcquired)
	store.EXPECT().MarkFailed(mock.Anything, receipt, mock.Anything).Return(nil).Once()
	expectBegin(store, receipt, idempotency.BeginAcquired)
	store.EXPECT().MarkSucceeded(mock.Anything, receipt, mock.Anything).Return(nil).Once()
	coordinator := idempotency.NewCoordinator(store)

	_, err := idempotency.Execute(t.Context(), coordinator, receipt, func(context.Context) (string, error) {
		return "", transient
	})
	require.ErrorIs(t, err, transient)

	result, err := idempotency.Execute(t.Context(), coordinator, receipt, func(context.Context) (string, error) {
		return "committed-after-retry", nil
	})
	require.NoError(t, err)
	require.Equal(t, "committed-after-retry", result)
}

func TestCoordinatorReturnsCommittedOutcomeWhenReceiptSuccessFinalizationFails(t *testing.T) {
	t.Parallel()

	store := idempotencymocks.NewMockStore(t)
	receipt := coordinatorReceipt(t)
	expectBegin(store, receipt, idempotency.BeginAcquired)
	store.EXPECT().MarkSucceeded(mock.Anything, receipt, mock.Anything).Return(errors.New("redis unavailable")).Once()
	store.EXPECT().MarkFailed(mock.Anything, receipt, mock.Anything).Return(nil).Once()
	expectBegin(store, receipt, idempotency.BeginAcquired)
	store.EXPECT().MarkSucceeded(mock.Anything, receipt, mock.Anything).Return(nil).Once()
	coordinator := idempotency.NewCoordinator(store)

	committed, err := idempotency.Execute(t.Context(), coordinator, receipt, func(context.Context) (string, error) {
		return "durable-commit", nil
	})
	require.NoError(t, err)
	require.Equal(t, "durable-commit", committed)

	replayed, err := idempotency.Execute(t.Context(), coordinator, receipt, func(context.Context) (string, error) {
		return "authoritative-db-replay", nil
	})
	require.NoError(t, err)
	require.Equal(t, "authoritative-db-replay", replayed)
}

func TestCoordinatorKeepsCommittedOutcomeWhenSuccessReopenAlsoFails(t *testing.T) {
	t.Parallel()

	store := idempotencymocks.NewMockStore(t)
	receipt := coordinatorReceipt(t)
	expectBegin(store, receipt, idempotency.BeginAcquired)
	store.EXPECT().MarkSucceeded(mock.Anything, receipt, mock.Anything).Return(errors.New("redis unavailable")).Once()
	store.EXPECT().MarkFailed(mock.Anything, receipt, mock.Anything).Return(errors.New("redis still unavailable")).Once()
	expectBegin(store, receipt, idempotency.BeginInFlight)
	coordinator := idempotency.NewCoordinator(store)

	committed, err := idempotency.Execute(t.Context(), coordinator, receipt, func(context.Context) (string, error) {
		return "durable-commit", nil
	})
	require.NoError(t, err)
	require.Equal(t, "durable-commit", committed)

	_, err = idempotency.Execute(t.Context(), coordinator, receipt, func(context.Context) (string, error) {
		t.Fatal("in-flight receipt must not invoke a second mutation")
		return "", nil
	})
	require.ErrorIs(t, err, domain.ErrConflict)
}

func TestNewCommandRequiresNamespaceIdentityAndDigest(t *testing.T) {
	t.Parallel()

	commandID := uuid.MustParse("60000000-0000-4000-8000-000000000001")
	first, err := idempotency.NewCommand("participant-draft-action", commandID, [32]byte{1})
	require.NoError(t, err)
	second, err := idempotency.NewCommand("participant-draft-action", commandID, [32]byte{1})
	require.NoError(t, err)
	require.Equal(t, first, second)

	_, err = idempotency.NewCommand("participant-draft-action", commandID, [32]byte{})
	require.ErrorIs(t, err, idempotency.ErrInvalidCommand)
	_, err = idempotency.NewCommand("bad:namespace", commandID, [32]byte{1})
	require.ErrorIs(t, err, idempotency.ErrInvalidCommand)
}

func TestCoordinatorRejectsAcquisitionReturnedForAnotherLease(t *testing.T) {
	t.Parallel()

	store := idempotencymocks.NewMockStore(t)
	receipt := coordinatorReceipt(t)
	store.EXPECT().Begin(mock.Anything, receipt, mock.Anything).
		Return(idempotency.BeginResult{
			Disposition: idempotency.BeginAcquired,
			Lease:       idempotency.LeaseToken{1},
		}, nil).
		Once()

	_, err := idempotency.Execute(t.Context(), idempotency.NewCoordinator(store), receipt, func(context.Context) (string, error) {
		t.Fatal("a mismatched lease must not invoke a mutation")
		return "", nil
	})
	require.ErrorIs(t, err, domain.ErrInternal)
}

func TestCoordinatorFinalizesOnlyItsAcquiredLease(t *testing.T) {
	t.Parallel()

	store := idempotencymocks.NewMockStore(t)
	receipt := coordinatorReceipt(t)
	var acquiredLease idempotency.LeaseToken
	store.EXPECT().Begin(mock.Anything, receipt, mock.Anything).
		RunAndReturn(func(_ context.Context, _ idempotency.Command, lease idempotency.LeaseToken) (idempotency.BeginResult, error) {
			acquiredLease = lease
			return idempotency.BeginResult{Disposition: idempotency.BeginAcquired, Lease: lease}, nil
		}).Once()
	store.EXPECT().MarkSucceeded(mock.Anything, receipt, mock.MatchedBy(func(lease idempotency.LeaseToken) bool {
		return lease == acquiredLease && lease != (idempotency.LeaseToken{})
	})).Return(nil).Once()

	result, err := idempotency.Execute(t.Context(), idempotency.NewCoordinator(store), receipt, func(context.Context) (string, error) {
		return "durable-commit", nil
	})
	require.NoError(t, err)
	require.Equal(t, "durable-commit", result)
}

func coordinatorReceipt(t *testing.T) idempotency.Command {
	t.Helper()
	receipt, err := idempotency.NewCommand(
		"admin-roster-replace",
		uuid.MustParse("50000000-0000-4000-8000-000000000001"),
		[32]byte{1},
	)
	require.NoError(t, err)
	return receipt
}

func expectBegin(
	store *idempotencymocks.MockStore,
	receipt idempotency.Command,
	disposition idempotency.BeginDisposition,
) {
	store.EXPECT().Begin(mock.Anything, receipt, mock.Anything).
		RunAndReturn(func(_ context.Context, _ idempotency.Command, lease idempotency.LeaseToken) (idempotency.BeginResult, error) {
			result := idempotency.BeginResult{Disposition: disposition}
			if disposition == idempotency.BeginAcquired {
				result.Lease = lease
			}
			return result, nil
		}).Once()
}
