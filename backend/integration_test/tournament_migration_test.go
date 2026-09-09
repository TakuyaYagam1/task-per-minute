//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestTournamentMigration(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

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
			err := sharedPool.QueryRow(ctx, `
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

			_, err = sharedPool.Exec(ctx, `DELETE FROM tournaments WHERE id = $1`, id)
			require.NoError(t, err)
		})
	}

	_, err := sharedPool.Exec(ctx, `
		INSERT INTO tournaments (preset)
		VALUES ('unsupported')`)
	require.Error(t, err)

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO tournaments (state, created_at, updated_at, started_at)
		VALUES ('technical_pause', $1, $1, $2)`, createdAt, startedAt)
	require.Error(t, err)

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO tournaments (state, paused_from_state)
		VALUES ('draft', 'swiss')`)
	require.Error(t, err)

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO tournaments (revision)
		VALUES (0)`)
	require.Error(t, err)

	assertSingleActiveSlot(ctx, t, createdAt, startedAt, finishedAt)
}

func assertSingleActiveSlot(
	ctx context.Context, t *testing.T,
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
			err := sharedPool.QueryRow(ctx, `
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

	var activeID uuid.UUID
	failures := 0
	for range states {
		result := <-results
		if result.err != nil {
			failures++
			continue
		}
		activeID = result.id
	}
	require.NotEqual(t, uuid.Nil, activeID)
	require.Equal(t, 1, failures)

	var activeCount int
	err := sharedPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM tournaments
		WHERE state IN ('swiss', 'golden', 'playoffs', 'technical_pause')`).Scan(&activeCount)
	require.NoError(t, err)
	require.Equal(t, 1, activeCount)

	_, err = sharedPool.Exec(ctx, `
		UPDATE tournaments
		SET state = 'completed',
			paused_from_state = NULL,
			revision = revision + 1,
			updated_at = $2,
			finished_at = $2
		WHERE id = $1`, activeID, finishedAt)
	require.NoError(t, err)

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO tournaments (
			state, paused_from_state, created_at, updated_at, started_at
		)
		VALUES ('technical_pause', 'golden', $1, $1, $2)`, createdAt, startedAt)
	require.NoError(t, err)
}

func resetMigrationTables(ctx context.Context, tb testing.TB) {
	tb.Helper()
	_, err := sharedPool.Exec(ctx, `
		TRUNCATE TABLE participant_reservations, tournaments CASCADE`)
	require.NoError(tb, err)
}

func stringPointer(value string) *string {
	return &value
}
