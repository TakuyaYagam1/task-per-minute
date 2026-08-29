//go:build integration

package integration_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestParticipantReservationRepository(t *testing.T) {
	pool, _ := SetupTestDB(t)
	tx := postgres.NewTxManager(pool)
	players := postgres.NewPlayerPostgres(tx)
	duels := postgres.NewDuelPostgres(tx)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)

	alice, err := players.Create(ctx, uniq("reservation_alice"))
	require.NoError(t, err)
	bob, err := players.Create(ctx, uniq("reservation_bob"))
	require.NoError(t, err)

	aliceQueue, changed, err := players.AcquireParticipantReservation(
		ctx,
		alice.ID,
		domain.ParticipantReservationOwnerCasualQueue,
		alice.ID,
		now,
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.True(t, aliceQueue.IsValid())

	retried, changed, err := players.AcquireParticipantReservation(
		ctx,
		alice.ID,
		domain.ParticipantReservationOwnerCasualQueue,
		alice.ID,
		now.Add(time.Second),
	)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, aliceQueue.ReservationID, retried.ReservationID)
	require.Equal(t, aliceQueue.Revision, retried.Revision)

	_, changed, err = players.AcquireParticipantReservation(
		ctx,
		alice.ID,
		domain.ParticipantReservationOwnerCasualQueue,
		uuid.New(),
		now,
	)
	require.ErrorIs(t, err, domain.ErrPlayerReserved)
	require.False(t, changed)

	bobQueue := acquireQueueReservation(t, ctx, players, bob.ID, now)
	duel, err := duels.Create(ctx, alice.ID, bob.ID, now.Add(time.Minute))
	require.NoError(t, err)
	aliceDuel := promoteDuelReservation(t, ctx, players, aliceQueue, duel.ID, now.Add(time.Second))
	bobDuel := promoteDuelReservation(t, ctx, players, bobQueue, duel.ID, now.Add(time.Second))

	retried, changed, err = players.PromoteParticipantReservation(
		ctx,
		*aliceQueue,
		domain.ParticipantReservationOwnerCasualDuel,
		duel.ID,
		now.Add(2*time.Second),
	)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, aliceDuel.ReservationID, retried.ReservationID)

	_, err = players.ReleaseParticipantReservation(ctx, *aliceQueue)
	require.ErrorIs(t, err, domain.ErrPlayerReserved)

	_, err = duels.Finish(ctx, duel.ID, nil, now.Add(3*time.Second), domain.DuelStatusFinished)
	require.NoError(t, err)
	assertReservationMissing(t, ctx, players, alice.ID)
	assertReservationMissing(t, ctx, players, bob.ID)

	changed, err = players.ReleaseParticipantReservation(ctx, *aliceDuel)
	require.NoError(t, err)
	require.False(t, changed)
	changed, err = players.ReleaseParticipantReservation(ctx, *bobDuel)
	require.NoError(t, err)
	require.False(t, changed)

	assertConcurrentReservationWinner(t, ctx, players, now.Add(4*time.Second))
	assertArenaReservationBlocksPlayerEntry(t, ctx, pool, players, now.Add(5*time.Second))
}

func acquireQueueReservation(
	t testing.TB,
	ctx context.Context,
	players *postgres.PlayerPostgres,
	playerID uuid.UUID,
	at time.Time,
) *domain.ParticipantReservation {
	t.Helper()
	record, changed, err := players.AcquireParticipantReservation(
		ctx,
		playerID,
		domain.ParticipantReservationOwnerCasualQueue,
		playerID,
		at,
	)
	require.NoError(t, err)
	require.True(t, changed)
	return record
}

func promoteDuelReservation(
	t testing.TB,
	ctx context.Context,
	players *postgres.PlayerPostgres,
	queueReservation *domain.ParticipantReservation,
	duelID uuid.UUID,
	at time.Time,
) *domain.ParticipantReservation {
	t.Helper()
	record, changed, err := players.PromoteParticipantReservation(
		ctx,
		*queueReservation,
		domain.ParticipantReservationOwnerCasualDuel,
		duelID,
		at,
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, queueReservation.Revision+1, record.Revision)
	return record
}

func assertConcurrentReservationWinner(
	t *testing.T,
	ctx context.Context,
	players *postgres.PlayerPostgres,
	at time.Time,
) {
	t.Helper()
	player, err := players.Create(ctx, uniq("reservation_race"))
	require.NoError(t, err)
	owners := []uuid.UUID{uuid.New(), uuid.New()}
	type result struct {
		record  *domain.ParticipantReservation
		changed bool
		err     error
	}
	results := make([]result, len(owners))
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(len(owners))
	for index, ownerID := range owners {
		go func(index int, ownerID uuid.UUID) {
			defer wg.Done()
			<-start
			results[index].record, results[index].changed, results[index].err =
				players.AcquireParticipantReservation(
					ctx,
					player.ID,
					domain.ParticipantReservationOwnerCasualQueue,
					ownerID,
					at,
				)
		}(index, ownerID)
	}
	close(start)
	wg.Wait()

	winners := 0
	var winner *domain.ParticipantReservation
	for _, result := range results {
		if result.err == nil && result.changed {
			winners++
			winner = result.record
			continue
		}
		require.ErrorIs(t, result.err, domain.ErrPlayerReserved)
	}
	require.Equal(t, 1, winners)
	require.NotNil(t, winner)

	changed, err := players.ReleaseParticipantReservation(ctx, *winner)
	require.NoError(t, err)
	require.True(t, changed)
}

func assertArenaReservationBlocksPlayerEntry(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	players *postgres.PlayerPostgres,
	at time.Time,
) {
	t.Helper()
	player, err := players.Create(ctx, uniq("arena_reserved"))
	require.NoError(t, err)

	tournamentID := uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO arena_tournaments (id) VALUES ($1)`, tournamentID)
	require.NoError(t, err)
	arenaReservation, changed, err := players.AcquireParticipantReservation(
		ctx,
		player.ID,
		domain.ParticipantReservationOwnerArena,
		tournamentID,
		at,
	)
	require.NoError(t, err)
	require.True(t, changed)

	_, changed, err = players.AcquireParticipantReservation(
		ctx,
		player.ID,
		domain.ParticipantReservationOwnerCasualQueue,
		player.ID,
		at,
	)
	require.ErrorIs(t, err, domain.ErrPlayerReserved)
	require.False(t, changed)

	_, err = players.JoinByUsername(ctx, player.Username, uuid.New(), at.Add(time.Hour))
	require.ErrorIs(t, err, domain.ErrPlayerReserved)

	changed, err = players.ReleaseParticipantReservation(ctx, *arenaReservation)
	require.NoError(t, err)
	require.True(t, changed)
}

func assertReservationMissing(
	t testing.TB,
	ctx context.Context,
	players *postgres.PlayerPostgres,
	playerID uuid.UUID,
) {
	t.Helper()
	record, err := players.GetParticipantReservation(ctx, playerID)
	require.NoError(t, err)
	require.Nil(t, record)
}
