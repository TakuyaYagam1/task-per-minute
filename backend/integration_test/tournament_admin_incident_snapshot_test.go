//go:build integration

package integration_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/incidentauth"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	auditrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/audit"
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

	repository := auditrepo.NewTournamentAdminAuditPostgres(postgres.NewTxManager(sharedPool))
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

func TestTournamentAdminIncidentExportSignsDurableSnapshot(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	fixture := createCorrectionRepositoryFixture(ctx, t)
	corrections := postgres.NewCorrectionPostgres(postgres.NewTxManager(sharedPool))
	input := newCorrectionInput(
		ctx, t, fixture, fixture.result, fixture.projection, 1, fixture.nextTime,
	)
	rebuilt, err := corrections.Rebuild(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, rebuilt)
	require.NotNil(t, rebuilt.Projection)
	if delay := time.Until(input.CorrectedAt); delay > 0 {
		time.Sleep(delay + time.Millisecond)
	}

	authenticator, err := incidentauth.NewHMACAuthenticator(incidentauth.HMACConfig{
		KeyID:  "incident-2026-09",
		Secret: []byte(strings.Repeat("a", 32)),
	})
	require.NoError(t, err)
	application := tournamentadmin.AdminNewUseCase(tournamentadmin.AdminDependencies{
		Incidents: auditrepo.NewTournamentAdminAuditPostgres(postgres.NewTxManager(sharedPool)),
		Signer:    authenticator,
	})

	bundle, err := application.ExportIncident(ctx, tournamentadmin.IncidentQuery{
		Operator:     tournamentadmin.OperatorIdentity{ActorID: input.OperatorID},
		TournamentID: fixture.resultFixture.draft.tournamentID,
	})
	require.NoError(t, err)
	require.Equal(t, fixture.resultFixture.draft.tournamentID, bundle.TournamentID)
	require.Equal(t, rebuilt.Projection.Revision.RevisionNumber, bundle.ProjectionRevision)
	require.Equal(t, audit.IncidentBundleAlgorithmHMACSHA256V1, bundle.Algorithm)
	require.Equal(t, "incident-2026-09", bundle.KeyID)
	require.NoError(t, audit.VerifyIncidentBundle(bundle))
	require.NoError(t, audit.VerifyIncidentBundleAuthenticityEnvelope(bundle))
	require.NoError(t, authenticator.Verify(bundle))
}
