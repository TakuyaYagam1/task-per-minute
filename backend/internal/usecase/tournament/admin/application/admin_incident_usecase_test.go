package application_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/audit"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/application"
	incidentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/incident"
	incidentmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/incident/mocks"
	operationusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/operation"
)

func TestUseCaseExportIncidentGeneratesSignsAndVerifiesBundle(t *testing.T) {
	t.Parallel()

	query := incidentusecase.IncidentQuery{
		Operator: operationusecase.OperatorIdentity{ActorID: uuid.New()}, TournamentID: uuid.New(),
	}
	generatedAt := time.Date(2026, time.September, 7, 15, 0, 0, 0, time.UTC)
	snapshot := audit.IncidentBundleSnapshot{
		TournamentID: query.TournamentID, ProjectionRevision: 7, GeneratedAt: generatedAt,
		Events: []audit.AuditEvent{{
			AuditEventID: uuid.New(), TournamentID: query.TournamentID,
			RosterID: uuid.New(), SeriesID: uuid.New(), ResultEventID: uuid.New(),
			ActorKind: domain.ResultActorServer, EventType: "tournament.result.settled",
			RedactedPayload: []byte(`{"state":"completed"}`),
			OccurredAt:      generatedAt.Add(-time.Second), CreatedAt: generatedAt.Add(-time.Second),
			ResultState: "completed", ResultReason: "winner", OfficialResultRevisionID: uuid.New(),
			EntityKind: audit.AuditEntityGameAttempt, EntityID: uuid.New(), RevisionNumber: 1, IsCurrent: true,
		}},
	}
	incidents := incidentmocks.NewMockIncidentSnapshotPort(t)
	signer := incidentmocks.NewMockIncidentAuthenticator(t)
	incidents.EXPECT().LoadIncidentSnapshot(mock.Anything, query).Return(snapshot, nil).Once()
	signer.EXPECT().Sign(mock.Anything).RunAndReturn(func(bundle audit.IncidentBundle) (audit.IncidentBundle, error) {
		require.Equal(t, snapshot.TournamentID, bundle.TournamentID)
		require.Equal(t, snapshot.ProjectionRevision, bundle.ProjectionRevision)
		require.NoError(t, audit.VerifyIncidentBundle(bundle))
		signed := bundle
		signed.Algorithm = audit.IncidentBundleAlgorithmHMACSHA256V1
		signed.KeyID = "incident-2026-09"
		signed.MAC = [32]byte{1}
		return signed, nil
	}).Once()
	signer.EXPECT().Verify(mock.MatchedBy(func(bundle audit.IncidentBundle) bool {
		return bundle.Algorithm == audit.IncidentBundleAlgorithmHMACSHA256V1 &&
			bundle.KeyID == "incident-2026-09" && bundle.MAC == [32]byte{1}
	})).Return(nil).Once()

	application := tournamentadmin.AdminNewUseCase(tournamentadmin.AdminDependencies{Incidents: incidents, Signer: signer})
	bundle, err := application.ExportIncident(context.Background(), query)
	require.NoError(t, err)
	require.Equal(t, snapshot.TournamentID, bundle.TournamentID)
	require.Equal(t, snapshot.ProjectionRevision, bundle.ProjectionRevision)
	require.NoError(t, audit.VerifyIncidentBundle(bundle))
	require.NoError(t, audit.VerifyIncidentBundleAuthenticityEnvelope(bundle))
}
