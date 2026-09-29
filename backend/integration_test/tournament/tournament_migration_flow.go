//go:build integration

package tournament

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func RunTournamentMigration(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	require.NotNil(t, pool)

	ctx := context.Background()
	resetMigrationTables(ctx, pool, t)
	t.Cleanup(func() { resetMigrationTables(ctx, pool, t) })

	createdAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	startedAt := createdAt.Add(10 * time.Minute)
	finishedAt := startedAt.Add(45 * time.Minute)

	tests := []struct {
		state      string
		pausedFrom *string
		startedAt  *time.Time
		finishedAt *time.Time
	}{
		{state: "draft"},
		{state: "registration"},
		{state: "roster_locked"},
		{state: "swiss", startedAt: &startedAt},
		{state: "golden", startedAt: &startedAt},
		{state: "playoffs", startedAt: &startedAt},
		{state: "technical_pause", pausedFrom: stringPointer("swiss"), startedAt: &startedAt},
		{state: "completed", startedAt: &startedAt, finishedAt: &finishedAt},
		{state: "cancelled", finishedAt: &finishedAt},
	}
	for _, tt := range tests {
		t.Run("stores_"+tt.state, func(t *testing.T) {
			var (
				id       uuid.UUID
				preset   string
				revision int64
				created  time.Time
				updated  time.Time
			)
			err := pool.QueryRow(ctx, `
				INSERT INTO tournaments (
					state, paused_from_state, created_at, updated_at, started_at, finished_at
				)
				VALUES ($1, $2, $3, $3, $4, $5)
				RETURNING id, preset, revision, created_at, updated_at`,
				tt.state, tt.pausedFrom, createdAt, tt.startedAt, tt.finishedAt,
			).Scan(&id, &preset, &revision, &created, &updated)
			require.NoError(t, err)
			require.NotEqual(t, uuid.Nil, id)
			require.Equal(t, "tournament_v1", preset)
			require.EqualValues(t, 1, revision)
			require.True(t, createdAt.Equal(created))
			require.True(t, createdAt.Equal(updated))

			_, err = pool.Exec(ctx, `DELETE FROM tournaments WHERE id = $1`, id)
			require.NoError(t, err)
		})
	}

	_, err := pool.Exec(ctx, `
		INSERT INTO tournaments (preset)
		VALUES ('unsupported')`)
	require.Error(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO tournaments (state, created_at, updated_at, started_at)
		VALUES ('technical_pause', $1, $1, $2)`, createdAt, startedAt)
	require.Error(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO tournaments (state, paused_from_state)
		VALUES ('draft', 'swiss')`)
	require.Error(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO tournaments (revision)
		VALUES (0)`)
	require.Error(t, err)

	assertConcurrentActiveTournaments(ctx, t, pool, createdAt, startedAt, finishedAt)
}

func assertConcurrentActiveTournaments(
	ctx context.Context, t *testing.T, pool *pgxpool.Pool,
	createdAt time.Time,
	startedAt time.Time,
	finishedAt time.Time,
) {
	t.Helper()

	type insertResult struct {
		id  uuid.UUID
		err error
	}
	start := make(chan struct{})
	results := make(chan insertResult, 2)
	states := []struct {
		state      string
		pausedFrom *string
	}{
		{state: "swiss"},
		{state: "technical_pause", pausedFrom: stringPointer("playoffs")},
	}

	for _, state := range states {
		go func() {
			<-start
			var id uuid.UUID
			err := pool.QueryRow(ctx, `
				INSERT INTO tournaments (
					state, paused_from_state, created_at, updated_at, started_at
				)
				VALUES ($1, $2, $3, $3, $4)
				RETURNING id`,
				state.state, state.pausedFrom, createdAt, startedAt,
			).Scan(&id)
			results <- insertResult{id: id, err: err}
		}()
	}
	close(start)

	activeIDs := make([]uuid.UUID, 0, len(states))
	for range states {
		result := <-results
		require.NoError(t, result.err)
		activeIDs = append(activeIDs, result.id)
	}
	require.Len(t, activeIDs, len(states))
	require.NotEqual(t, uuid.Nil, activeIDs[0])
	require.NotEqual(t, uuid.Nil, activeIDs[1])
	require.NotEqual(t, activeIDs[0], activeIDs[1])

	var activeCount int
	err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM tournaments
		WHERE state IN ('swiss', 'golden', 'playoffs', 'technical_pause')`).Scan(&activeCount)
	require.NoError(t, err)
	require.Equal(t, 2, activeCount)

	_, err = pool.Exec(ctx, `
		UPDATE tournaments
		SET state = 'completed',
			paused_from_state = NULL,
			revision = revision + 1,
			updated_at = $2,
			finished_at = $2
		WHERE id = $1`, activeIDs[0], finishedAt)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO tournaments (
			state, paused_from_state, created_at, updated_at, started_at
		)
		VALUES ('technical_pause', 'golden', $1, $1, $2)`, createdAt, startedAt)
	require.NoError(t, err)
}

func resetMigrationTables(ctx context.Context, pool *pgxpool.Pool, tb testing.TB) {
	tb.Helper()
	err := ResetMigrationTables(ctx, pool)
	require.NoError(tb, err)
}

// ResetMigrationTables clears the tournament migration graph for callers that
// still compose these fixtures from the root integration package.
func ResetMigrationTables(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return fmt.Errorf("tournament migration: nil pool")
	}
	_, err := pool.Exec(ctx, `
		TRUNCATE TABLE participant_reservations, tournaments CASCADE`)
	return err
}

func stringPointer(value string) *string {
	return &value
}
