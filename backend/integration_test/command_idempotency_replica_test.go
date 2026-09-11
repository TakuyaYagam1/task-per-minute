//go:build integration

package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	redisadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/redis"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/idempotency"
)

func TestCommandReceiptCoordinatorsReplayDurableOutcomesAcrossReplicas(t *testing.T) {
	ctx := context.Background()
	TruncateTables(t, sharedPool)
	t.Cleanup(func() { TruncateTables(t, sharedPool) })
	contentRevision := prepareTournamentCreateReceiptContent(ctx, t)

	redis := sharedRedis(t)
	firstStore := redisadapter.NewCommandReceiptStore(redis.client, time.Second, time.Minute, time.Second)
	secondStore := redisadapter.NewCommandReceiptStore(redis.client, time.Second, time.Minute, time.Second)
	first := idempotency.NewCoordinator(firstStore)
	second := idempotency.NewCoordinator(secondStore)
	durableCommand := tournamentCreateReceiptCommand(time.Now().UTC().Truncate(time.Microsecond), contentRevision)
	command, err := idempotency.NewCommand(
		"tournament-create",
		durableCommand.IdempotencyKey,
		durableCommand.PayloadDigest,
	)
	require.NoError(t, err)

	committed, err := idempotency.Execute(ctx, first, command, func(ctx context.Context) (usecase.TournamentResult, error) {
		return newTournamentCreateReceiptStore().Create(ctx, durableCommand)
	})
	require.NoError(t, err)

	replayed, err := idempotency.Execute(ctx, second, command, func(ctx context.Context) (usecase.TournamentResult, error) {
		return newTournamentCreateReceiptStore().Create(ctx, durableCommand)
	})
	require.NoError(t, err)
	require.Equal(t, committed, replayed)
	assertTournamentCreateReceiptCount(ctx, t, durableCommand.IdempotencyKey, 1)
}

func TestCommandReceiptCoordinatorRetriesAfterDurableRollback(t *testing.T) {
	ctx := context.Background()
	TruncateTables(t, sharedPool)
	t.Cleanup(func() { TruncateTables(t, sharedPool) })
	contentRevision := prepareTournamentCreateReceiptContent(ctx, t)

	redis := sharedRedis(t)
	first := idempotency.NewCoordinator(redisadapter.NewCommandReceiptStore(
		redis.client, time.Second, time.Minute, time.Second,
	))
	second := idempotency.NewCoordinator(redisadapter.NewCommandReceiptStore(
		redis.client, time.Second, time.Minute, time.Second,
	))
	durableCommand := tournamentCreateReceiptCommand(time.Now().UTC().Truncate(time.Microsecond), contentRevision)
	command, err := idempotency.NewCommand(
		"tournament-create",
		durableCommand.IdempotencyKey,
		durableCommand.PayloadDigest,
	)
	require.NoError(t, err)
	rollback := errors.New("force durable transaction rollback")

	_, err = idempotency.Execute(ctx, first, command, func(ctx context.Context) (usecase.TournamentResult, error) {
		var result usecase.TournamentResult
		tx := postgres.NewTxManager(sharedPool)
		err := tx.Do(ctx, func(txCtx context.Context) error {
			var createErr error
			result, createErr = newTournamentCreateReceiptStore().Create(txCtx, durableCommand)
			if createErr != nil {
				return createErr
			}
			return rollback
		})
		return result, err
	})
	require.ErrorIs(t, err, rollback)
	assertTournamentCreateReceiptCount(ctx, t, durableCommand.IdempotencyKey, 0)

	committed, err := idempotency.Execute(ctx, second, command, func(ctx context.Context) (usecase.TournamentResult, error) {
		return newTournamentCreateReceiptStore().Create(ctx, durableCommand)
	})
	require.NoError(t, err)
	require.Equal(t, durableCommand.TournamentID, committed.Tournament.ID)
	assertTournamentCreateReceiptCount(ctx, t, durableCommand.IdempotencyKey, 1)
}

func TestCommandReceiptCoordinatorReopensExpiredInFlightReceiptAfterRestart(t *testing.T) {
	redis := sharedRedis(t)
	firstStore := redisadapter.NewCommandReceiptStore(redis.client, 25*time.Millisecond, time.Minute, time.Second)
	secondStore := redisadapter.NewCommandReceiptStore(redis.client, 25*time.Millisecond, time.Minute, time.Second)
	second := idempotency.NewCoordinator(secondStore)
	command := integrationCommandReceipt(t, "participant-draft-action")

	lease := integrationLease(t)
	begin, err := firstStore.Begin(context.Background(), command, lease)
	require.NoError(t, err)
	require.Equal(t, idempotency.BeginAcquired, begin.Disposition)
	require.Equal(t, lease, begin.Lease)

	_, err = idempotency.Execute(context.Background(), second, command, func(context.Context) (string, error) {
		t.Fatal("in-flight receipt must not invoke the durable mutation")
		return "", nil
	})
	require.ErrorIs(t, err, domain.ErrConflict)

	require.Eventually(t, func() bool {
		result, executeErr := idempotency.Execute(context.Background(), second, command, func(context.Context) (string, error) {
			return "durable-retry-after-restart", nil
		})
		return executeErr == nil && result == "durable-retry-after-restart"
	}, time.Second, 10*time.Millisecond)
}

func TestCommandReceiptLeaseFencesStaleReplicaFinalization(t *testing.T) {
	redis := sharedRedis(t)
	first := redisadapter.NewCommandReceiptStore(redis.client, 25*time.Millisecond, time.Minute, time.Second)
	second := redisadapter.NewCommandReceiptStore(redis.client, time.Second, time.Minute, time.Second)
	command := integrationCommandReceipt(t, "admin-result-correct")
	firstLease := integrationLease(t)

	firstBegin, err := first.Begin(context.Background(), command, firstLease)
	require.NoError(t, err)
	require.Equal(t, idempotency.BeginAcquired, firstBegin.Disposition)
	require.Equal(t, firstLease, firstBegin.Lease)

	secondLease := integrationLease(t)
	var secondBegin idempotency.BeginResult
	require.Eventually(t, func() bool {
		begin, beginErr := second.Begin(context.Background(), command, secondLease)
		if beginErr != nil {
			return false
		}
		secondBegin = begin
		return begin.Disposition == idempotency.BeginAcquired
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, secondLease, secondBegin.Lease)

	require.ErrorIs(t, first.MarkSucceeded(context.Background(), command, firstLease), idempotency.ErrLeaseLost)
	require.ErrorIs(t, first.MarkFailed(context.Background(), command, firstLease), idempotency.ErrLeaseLost)

	stillInFlight, err := first.Begin(context.Background(), command, integrationLease(t))
	require.NoError(t, err)
	require.Equal(t, idempotency.BeginInFlight, stillInFlight.Disposition)

	require.NoError(t, second.MarkSucceeded(context.Background(), command, secondLease))
	completed, err := first.Begin(context.Background(), command, integrationLease(t))
	require.NoError(t, err)
	require.Equal(t, idempotency.BeginSucceeded, completed.Disposition)
}

func integrationCommandReceipt(t *testing.T, namespace string) idempotency.Command {
	t.Helper()
	command, err := idempotency.NewCommand(namespace, uuid.New(), [32]byte{1})
	require.NoError(t, err)
	return command
}

func integrationLease(t *testing.T) idempotency.LeaseToken {
	t.Helper()
	lease, err := idempotency.NewLeaseToken()
	require.NoError(t, err)
	return lease
}
