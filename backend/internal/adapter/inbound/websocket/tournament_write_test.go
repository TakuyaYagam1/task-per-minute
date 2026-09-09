package websocket

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	tournamentws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/tournament"
)

func TestValidateTournamentWriteRejectsCrossScopeFrames(t *testing.T) {
	t.Parallel()
	tournamentID := tournamentSourceID(921)
	playerID := tournamentSourceID(922)
	participantFrame, publicFrame, operatorFrame := tournamentWriteTestFrames(t, tournamentID, playerID)

	valid := []struct {
		name  string
		scope tournamentWriteScope
		frame []byte
	}{
		{name: "participant", scope: tournamentWriteScope{Role: TournamentRoleParticipant, TournamentID: tournamentID, ParticipantID: playerID}, frame: participantFrame},
		{name: "public", scope: tournamentWriteScope{Role: TournamentRolePublic, TournamentID: tournamentID}, frame: publicFrame},
		{name: "operator", scope: tournamentWriteScope{Role: TournamentRoleOperator, TournamentID: tournamentID, OperatorID: tournamentSourceID(923)}, frame: operatorFrame},
	}
	for _, tt := range valid {
		t.Run("valid "+tt.name, func(t *testing.T) {
			require.NoError(t, validateTournamentWrite(tt.scope, tt.frame))
		})
	}

	mismatches := []struct {
		name  string
		scope tournamentWriteScope
		frame []byte
	}{
		{name: "participant role", scope: tournamentWriteScope{Role: TournamentRolePublic, TournamentID: tournamentID}, frame: participantFrame},
		{name: "participant tournament", scope: tournamentWriteScope{Role: TournamentRoleParticipant, TournamentID: tournamentSourceID(924), ParticipantID: playerID}, frame: participantFrame},
		{name: "participant identity", scope: tournamentWriteScope{Role: TournamentRoleParticipant, TournamentID: tournamentID, ParticipantID: tournamentSourceID(925)}, frame: participantFrame},
		{name: "public tournament", scope: tournamentWriteScope{Role: TournamentRolePublic, TournamentID: tournamentSourceID(926)}, frame: publicFrame},
		{name: "operator tournament", scope: tournamentWriteScope{Role: TournamentRoleOperator, TournamentID: tournamentSourceID(927), OperatorID: tournamentSourceID(923)}, frame: operatorFrame},
	}
	for _, tt := range mismatches {
		t.Run(tt.name, func(t *testing.T) {
			require.ErrorIs(t, validateTournamentWrite(tt.scope, tt.frame), ErrTournamentWriteScope)
		})
	}
}

func TestSocketWritesCheckScopeBeforeUsingConnection(t *testing.T) {
	t.Parallel()
	invalid := tournamentWriteScope{Role: TournamentRolePublic}
	require.ErrorIs(t, writeTournamentMessage(context.Background(), nil, invalid, []byte(`{}`)), ErrTournamentWriteScope)
	require.ErrorIs(t, writeTournamentPing(context.Background(), nil, invalid), ErrTournamentWriteScope)
}

func TestTournamentRejectionCanUseEveryBoundRoleScope(t *testing.T) {
	t.Parallel()
	frame, err := MarshalTournamentRejected(TournamentRejection{Code: TournamentRejectionUnauthenticated, Message: "authentication required"})
	require.NoError(t, err)
	tournamentID := tournamentSourceID(931)
	for _, scope := range []tournamentWriteScope{
		{Role: TournamentRoleParticipant, TournamentID: tournamentID, ParticipantID: tournamentSourceID(932)},
		{Role: TournamentRolePublic, TournamentID: tournamentID},
		{Role: TournamentRoleOperator, TournamentID: tournamentID, OperatorID: tournamentSourceID(933)},
	} {
		require.NoError(t, validateTournamentWrite(scope, frame))
	}
}

func tournamentWriteTestFrames(t *testing.T, tournamentID, playerID uuid.UUID) ([]byte, []byte, []byte) {
	t.Helper()
	reader := tournamentServerSnapshotReader(t, tournamentID, playerID)
	source, err := NewTournamentProductionSnapshotSource(reader)
	require.NoError(t, err)

	participantEnvelope, err := tournamentws.ParticipantRealtimeView(context.Background(), source, tournamentws.ParticipantRealtimeRequest{
		Principal:    tournamentws.ParticipantRealtimePrincipal{Authenticated: true, TournamentID: tournamentID, PlayerID: playerID},
		TournamentID: tournamentID,
	})
	require.NoError(t, err)
	participantPayload, err := NewTournamentParticipantPayload(participantEnvelope)
	require.NoError(t, err)
	participantFrame, err := MarshalTournamentParticipant(participantPayload)
	require.NoError(t, err)

	publicRead, err := source.PublicRealtimeRead(context.Background(), tournamentID)
	require.NoError(t, err)
	publicEnvelope, err := tournamentws.NewRealtimeEnvelope(publicRead.SnapshotMetadata, publicRead.Snapshot)
	require.NoError(t, err)
	publicPayload, err := NewTournamentPublicPayload(publicEnvelope)
	require.NoError(t, err)
	publicFrame, err := MarshalTournamentPublic(publicPayload)
	require.NoError(t, err)

	operatorEnvelope, err := source.ReadOperatorRealtime(context.Background(), tournamentws.OperatorRealtimeQuery{
		TournamentID: tournamentID,
		OperatorID:   tournamentSourceID(923),
	})
	require.NoError(t, err)
	operatorPayload, err := NewTournamentOperatorPayload(operatorEnvelope)
	require.NoError(t, err)
	operatorFrame, err := MarshalTournamentOperator(operatorPayload)
	require.NoError(t, err)

	return participantFrame, publicFrame, operatorFrame
}

func TestNewTournamentWriteScopeRejectsConfusedOperatorPrincipal(t *testing.T) {
	t.Parallel()
	tournamentID := tournamentSourceID(941)
	_, err := newTournamentWriteScope(
		tournamentConnectionScope{Role: TournamentRoleOperator, TournamentID: tournamentID},
		tournamentConnectionPrincipal{OperatorSession: &TournamentOperatorSession{
			Principal: tournamentws.OperatorRealtimePrincipal{
				Authenticated: true,
				PrincipalID:   tournamentSourceID(942),
				Role:          tournamentws.OperatorRealtimeRole,
				TournamentID:  tournamentSourceID(943),
			},
			ExpiresAt: time.Now().Add(time.Hour),
			Validate:  func(context.Context) bool { return true },
		}},
	)
	require.ErrorIs(t, err, ErrTournamentWriteScope)
}
