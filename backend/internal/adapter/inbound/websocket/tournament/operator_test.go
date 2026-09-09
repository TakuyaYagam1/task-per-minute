package tournament

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestOperatorRealtimeAdapterEnforcesAdminPrincipal(t *testing.T) {
	t.Parallel()
	tournamentID := uuid.MustParse("93000000-0000-4000-8000-000000000001")
	operatorID := uuid.MustParse("93000000-0000-4000-8000-000000000002")
	source := NewMockOperatorRealtimeReadSource(t)
	source.EXPECT().
		ReadOperatorRealtime(mock.Anything, OperatorRealtimeQuery{
			TournamentID: tournamentID,
			OperatorID:   operatorID,
		}).
		Return(operatorRealtimeTestEnvelope(t, tournamentID, operatorID), nil).
		Once()
	adapter := NewOperatorRealtimeAdapter(source)
	principal := OperatorRealtimePrincipal{
		Authenticated: true,
		PrincipalID:   operatorID,
		Role:          OperatorRealtimeRole,
		TournamentID:  tournamentID,
	}

	envelope, err := adapter.Read(context.Background(), principal, tournamentID)
	require.NoError(t, err)
	require.Equal(t, tournamentID, envelope.TournamentID)

	principal.Authenticated = false
	_, err = adapter.Read(context.Background(), principal, tournamentID)
	require.ErrorIs(t, err, ErrOperatorRealtimeAuthentication)

	principal.Authenticated = true
	principal.Role = "participant"
	_, err = adapter.Read(context.Background(), principal, tournamentID)
	require.ErrorIs(t, err, ErrOperatorRealtimeRole)
}

func operatorRealtimeTestEnvelope(t *testing.T, tournamentID, operatorID uuid.UUID) RealtimeEnvelope {
	t.Helper()
	snapshot, err := NewOperatorSnapshot(
		OperatorSnapshotAccess{Authenticated: true, TournamentID: tournamentID, OperatorID: operatorID},
		OperatorSnapshotInput{
			TournamentID: tournamentID,
			Revision:     5,
			LastSequence: 6,
			Waves:        []OperatorWaveInput{},
			Presence:     []OperatorPresenceInput{},
			Replays:      []OperatorReplayInput{},
			AuditLinks:   []OperatorAuditLinkInput{},
		},
	)
	require.NoError(t, err)
	envelope, err := NewRealtimeEnvelope(RealtimeEnvelopeMetadata{
		SchemaVersion:      TournamentRealtimeSchemaVersion,
		TournamentID:       tournamentID,
		Sequence:           6,
		EventID:            uuid.MustParse("93000000-0000-4000-8000-000000000003"),
		OccurredAt:         time.Date(2026, 9, 5, 8, 0, 0, 0, time.UTC),
		ProjectionRevision: 5,
	}, snapshot)
	require.NoError(t, err)
	return envelope
}
