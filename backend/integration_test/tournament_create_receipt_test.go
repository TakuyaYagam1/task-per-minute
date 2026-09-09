//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/idempotency"
	catalogusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/catalog"
)

func TestTournamentCreateReceiptPersistsAcrossReplicaRestart(t *testing.T) {
	ctx := context.Background()
	TruncateTables(t, sharedPool)
	t.Cleanup(func() { TruncateTables(t, sharedPool) })
	prepareTournamentCreateReceiptContent(ctx, t)

	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	command := tournamentCreateReceiptCommand(createdAt)
	initial := createTournamentReceiptConcurrently(ctx, t, command)
	assertTournamentCreateReceiptCount(ctx, t, command.IdempotencyKey, 1)

	// A fresh adapter instance has no process-local admission state. This models
	// a process restart after the PostgreSQL transaction committed before Redis
	// could be updated.
	restarted, err := newTournamentCreateReceiptStore().Create(ctx, command)
	require.NoError(t, err)
	require.Equal(t, initial, restarted)

	mutated, changed, err := postgres.NewTournamentPostgres(postgres.NewTxManager(sharedPool)).Transition(
		ctx,
		postgres.TournamentTransitionInput{
			ID:               command.TournamentID,
			ExpectedRevision: 1,
			ExpectedState:    domain.TournamentStateDraft,
			NextState:        domain.TournamentStateRegistration,
			UpdatedAt:        createdAt.Add(time.Minute),
		},
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, domain.TournamentStateRegistration, mutated.State)

	replayed, err := newTournamentCreateReceiptStore().Create(ctx, command)
	require.NoError(t, err)
	require.Equal(t, initial, replayed)
	require.Equal(t, domain.TournamentStateDraft, replayed.Tournament.State)
	require.EqualValues(t, 1, replayed.Tournament.Revision)

	mismatch := command
	mismatch.ActorID = uuid.New()
	mismatch.PayloadDigest = sha256.Sum256([]byte("mismatched-create-command"))
	_, err = newTournamentCreateReceiptStore().Create(ctx, mismatch)
	require.ErrorIs(t, err, idempotency.ErrPayloadConflict)

	assertTournamentCreateReceiptRollback(ctx, t, createdAt.Add(2*time.Minute))
}

func newTournamentCreateReceiptStore() *postgres.TournamentCreatePostgres {
	tx := postgres.NewTxManager(sharedPool)
	return postgres.NewTournamentCreatePostgres(postgres.NewTournamentPostgres(tx))
}

func tournamentCreateReceiptCommand(createdAt time.Time) catalogusecase.CreateReceiptCommand {
	return catalogusecase.CreateReceiptCommand{
		ActorID:        uuid.New(),
		IdempotencyKey: uuid.New(),
		PayloadDigest:  sha256.Sum256([]byte(uuid.NewString())),
		TournamentID:   uuid.New(),
		RosterID:       uuid.New(),
		CreatedAt:      createdAt,
	}
}

func createTournamentReceiptConcurrently(
	ctx context.Context,
	t *testing.T,
	command catalogusecase.CreateReceiptCommand,
) usecase.TournamentResult {
	t.Helper()

	type createResult struct {
		result usecase.TournamentResult
		err    error
	}
	const replicas = 8
	start := make(chan struct{})
	results := make(chan createResult, replicas)
	var group sync.WaitGroup
	for range replicas {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			result, err := newTournamentCreateReceiptStore().Create(ctx, command)
			results <- createResult{result: result, err: err}
		}()
	}
	close(start)
	group.Wait()
	close(results)

	var first usecase.TournamentResult
	for result := range results {
		require.NoError(t, result.err)
		if first.Tournament.ID == uuid.Nil {
			first = result.result
			continue
		}
		require.Equal(t, first, result.result)
	}
	require.Equal(t, command.TournamentID, first.Tournament.ID)
	require.Equal(t, command.RosterID, first.Tournament.RosterID)
	require.True(t, first.Changed)
	return first
}

func assertTournamentCreateReceiptCount(
	ctx context.Context,
	t *testing.T,
	commandID uuid.UUID,
	want int,
) {
	t.Helper()

	var got int
	err := sharedPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM tournament_create_command_receipts
		WHERE command_id = $1`, commandID).Scan(&got)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func assertTournamentCreateReceiptRollback(ctx context.Context, t *testing.T, createdAt time.Time) {
	t.Helper()

	tx := postgres.NewTxManager(sharedPool)
	store := postgres.NewTournamentCreatePostgres(postgres.NewTournamentPostgres(tx))
	command := tournamentCreateReceiptCommand(createdAt)
	rollback := errors.New("force outer transaction rollback")
	err := tx.Do(ctx, func(txCtx context.Context) error {
		_, createErr := store.Create(txCtx, command)
		if createErr != nil {
			return createErr
		}
		return rollback
	})
	require.ErrorIs(t, err, rollback)
	assertTournamentCreateReceiptCount(ctx, t, command.IdempotencyKey, 0)

	var tournamentCount, rosterCount, configurationCount int
	err = sharedPool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM tournaments WHERE id = $1),
			(SELECT COUNT(*) FROM rosters WHERE id = $2),
			(SELECT COUNT(*) FROM tournament_content_configurations WHERE tournament_id = $1)`,
		command.TournamentID,
		command.RosterID,
	).Scan(&tournamentCount, &rosterCount, &configurationCount)
	require.NoError(t, err)
	require.Equal(t, 0, tournamentCount)
	require.Equal(t, 0, rosterCount)
	require.Equal(t, 0, configurationCount)
}

func prepareTournamentCreateReceiptContent(ctx context.Context, t *testing.T) {
	t.Helper()

	for _, kind := range []string{"normal", "golden"} {
		_, err := sharedPool.Exec(ctx, `
			INSERT INTO tasks (title, description, category, difficulty, time_limit, flag, kind)
			VALUES ($1, 'tournament create receipt fixture', 'web', 'easy', 60, $2, $3)`,
			"tournament_create_receipt_"+kind+"_"+uuid.NewString()[:8],
			"receipt-fixture-"+kind,
			kind,
		)
		require.NoError(t, err)
	}
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO task_version_health_attestations (task_id, task_version, revision, healthy, source)
		SELECT task_id, version, 1, true, 'content_validation'
		FROM task_versions`)
	require.NoError(t, err)
	_, err = sharedPool.Exec(ctx, `SELECT publish_task_pool_heads()`)
	require.NoError(t, err)
}
