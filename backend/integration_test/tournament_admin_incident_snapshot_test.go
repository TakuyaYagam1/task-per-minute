//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/audit"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
)

const incidentSnapshotSeedAuditEvents = 201

func TestTournamentAdminIncidentSnapshotPinsConcurrentAuditWrites(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	fixture := createCorrectionRepositoryFixture(ctx, t)
	corrections := postgres.NewCorrectionPostgres(postgres.NewTxManager(sharedPool))
	firstInput := newCorrectionInput(
		ctx, t, fixture, fixture.result, fixture.projection, 1, fixture.nextTime,
	)
	_, err := corrections.Rebuild(ctx, firstInput)
	require.NoError(t, err)
	for range incidentSnapshotSeedAuditEvents {
		settleCursorSafeAuditRecord(
			ctx,
			t,
			fixture,
			firstInput.CorrectedAt,
			uuid.New(),
		)
	}
	if delay := time.Until(firstInput.CorrectedAt); delay > 0 {
		time.Sleep(delay + time.Millisecond)
	}

	repository := postgres.NewTournamentAdminAuditPostgres(postgres.NewTxManager(sharedPool))
	query := tournamentadmin.IncidentQuery{
		Operator:     tournamentadmin.OperatorIdentity{ActorID: firstInput.OperatorID},
		TournamentID: fixture.resultFixture.draft.tournamentID,
	}

	var first, second audit.IncidentBundleSnapshot
	err = postgres.NewTxManager(sharedPool).ReadSnapshot(ctx, func(snapshotCtx context.Context) error {
		first, err = repository.LoadIncidentSnapshot(snapshotCtx, query)
		if err != nil {
			return err
		}
		require.Greater(t, len(first.Events), 200)

		insertedAuditID := settleCursorSafeAuditRecord(
			ctx,
			t,
			fixture,
			time.Now().UTC().Add(2*time.Second).Truncate(time.Microsecond),
			uuid.New(),
		)
		second, err = repository.LoadIncidentSnapshot(snapshotCtx, query)
		if err != nil {
			return err
		}
		require.Equal(t, first, second)
		for _, event := range second.Events {
			require.NotEqual(t, insertedAuditID, event.AuditEventID)
		}
		return nil
	})
	require.NoError(t, err)

	_, err = audit.GenerateIncidentBundle(first)
	require.NoError(t, err)
	fresh, err := repository.LoadIncidentSnapshot(ctx, query)
	require.NoError(t, err)
	require.Greater(t, len(fresh.Events), len(first.Events))
}
