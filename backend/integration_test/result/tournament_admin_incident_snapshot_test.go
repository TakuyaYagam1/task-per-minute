//go:build integration

package result

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
	correctionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/correction"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/audit"
	admin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/application"
	incidentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/incident"
	operation "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/operation"
)

const incidentSnapshotSeedAuditEvents = 201

func TestTournamentAdminIncidentSnapshotPinsConcurrentAuditWrites(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, resetResultTables(ctx, sharedPool))
	t.Cleanup(func() { require.NoError(t, resetResultTables(ctx, sharedPool)) })

	fixture, err := createCorrectionFixture(ctx, sharedPool)
	require.NoError(t, err)
	corrections := correctionrepo.NewCorrectionPostgres(postgres.NewTxManager(sharedPool))
	firstInput := newCorrectionInput(
		ctx, t, sharedPool, fixture, fixture.correction.Result, fixture.correction.Projection, 1, fixture.correction.NextTime,
	)
	_, err = corrections.Rebuild(ctx, firstInput)
	require.NoError(t, err)
	for range incidentSnapshotSeedAuditEvents {
		SettleCursorSafeAuditRecord(t, sharedPool, CursorAuditSeedInput{
			TournamentID: fixture.draft.TournamentID,
			RosterID:     fixture.draft.RosterID,
			ParticipantIDs: [2]uuid.UUID{
				fixture.draft.ParticipantIDs[0], fixture.draft.ParticipantIDs[1],
			},
			SettledAt:    firstInput.CorrectedAt,
			AuditEventID: uuid.New(),
		})
	}
	if delay := time.Until(firstInput.CorrectedAt); delay > 0 {
		time.Sleep(delay + time.Millisecond)
	}

	repository := auditrepo.NewTournamentAdminAuditPostgres(postgres.NewTxManager(sharedPool))
	query := incidentusecase.IncidentQuery{
		Operator:     operation.OperatorIdentity{ActorID: firstInput.OperatorID},
		TournamentID: fixture.draft.TournamentID,
	}

	var first, second audit.IncidentBundleSnapshot
	err = postgres.NewTxManager(sharedPool).ReadSnapshot(ctx, func(snapshotCtx context.Context) error {
		first, err = repository.LoadIncidentSnapshot(snapshotCtx, query)
		if err != nil {
			return err
		}
		require.Greater(t, len(first.Events), 200)

		insertedAuditID := SettleCursorSafeAuditRecord(t, sharedPool, CursorAuditSeedInput{
			TournamentID: fixture.draft.TournamentID,
			RosterID:     fixture.draft.RosterID,
			ParticipantIDs: [2]uuid.UUID{
				fixture.draft.ParticipantIDs[0], fixture.draft.ParticipantIDs[1],
			},
			SettledAt:    time.Now().UTC().Add(2 * time.Second).Truncate(time.Microsecond),
			AuditEventID: uuid.New(),
		})
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
	require.NoError(t, resetResultTables(ctx, sharedPool))
	t.Cleanup(func() { require.NoError(t, resetResultTables(ctx, sharedPool)) })

	fixture, err := createCorrectionFixture(ctx, sharedPool)
	require.NoError(t, err)
	corrections := correctionrepo.NewCorrectionPostgres(postgres.NewTxManager(sharedPool))
	input := newCorrectionInput(
		ctx, t, sharedPool, fixture, fixture.correction.Result, fixture.correction.Projection, 1, fixture.correction.NextTime,
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
	application := admin.AdminNewUseCase(admin.AdminDependencies{
		Incidents: auditrepo.NewTournamentAdminAuditPostgres(postgres.NewTxManager(sharedPool)),
		Signer:    authenticator,
	})

	bundle, err := application.ExportIncident(ctx, incidentusecase.IncidentQuery{
		Operator:     operation.OperatorIdentity{ActorID: input.OperatorID},
		TournamentID: fixture.draft.TournamentID,
	})
	require.NoError(t, err)
	require.Equal(t, fixture.draft.TournamentID, bundle.TournamentID)
	require.Equal(t, rebuilt.Projection.Revision.RevisionNumber, bundle.ProjectionRevision)
	require.Equal(t, audit.IncidentBundleAlgorithmHMACSHA256V1, bundle.Algorithm)
	require.Equal(t, "incident-2026-09", bundle.KeyID)
	require.NoError(t, audit.VerifyIncidentBundle(bundle))
	require.NoError(t, audit.VerifyIncidentBundleAuthenticityEnvelope(bundle))
	require.NoError(t, authenticator.Verify(bundle))
}
