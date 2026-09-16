//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	projectionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/projection"
	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
)

func TestTournamentProjectionHealthUsesDurableDraftLagAcrossRestart(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	first := projectionrepo.NewProjectionHealthPostgres(postgres.NewTxManager(sharedPool))
	empty, err := first.ProjectionHealth(ctx)
	require.NoError(t, err)
	require.Zero(t, empty.PendingCount)
	require.Nil(t, empty.OldestPendingAt)
	require.NoError(t, empty.Validate())

	createdAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	insertProjectionHealthDraft(ctx, t, createdAt)

	// A newly constructed source observes durable state rather than process-local
	// delivery or session state.
	restarted := projectionrepo.NewProjectionHealthPostgres(postgres.NewTxManager(sharedPool))
	lag, err := restarted.ProjectionHealth(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, lag.PendingCount)
	require.NotNil(t, lag.OldestPendingAt)
	require.Equal(t, createdAt, lag.OldestPendingAt.UTC())
	require.False(t, lag.ObservedAt.Before(createdAt))
	require.NoError(t, lag.Validate())
}

func TestTournamentProjectionHealthRejectsFutureDurableDraft(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	insertProjectionHealthDraft(ctx, t, time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond))
	_, err := projectionrepo.NewProjectionHealthPostgres(postgres.NewTxManager(sharedPool)).ProjectionHealth(ctx)
	require.ErrorIs(t, err, observability.ErrInvalidProjectionHealth)
}

func insertProjectionHealthDraft(ctx context.Context, tb testing.TB, createdAt time.Time) {
	tb.Helper()

	tournamentID := createMigrationTournament(ctx, tb)
	rosterID := createMigrationRoster(ctx, tb, tournamentID)
	cutoffID := uuid.New()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO projection_cutoffs (
			id, tournament_id, roster_id, sequence_number, source_kind, reason, cutoff_at, created_at
		)
		VALUES ($1, $2, $3, 1, 'initial', 'projection health', $4, $4)`,
		cutoffID, tournamentID, rosterID, createdAt,
	)
	require.NoError(tb, err)
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO projection_revisions (
			id, tournament_id, roster_id, revision_number, cutoff_id, created_at
		)
		VALUES ($1, $2, $3, 1, $4, $5)`,
		uuid.New(), tournamentID, rosterID, cutoffID, createdAt,
	)
	require.NoError(tb, err)
}
