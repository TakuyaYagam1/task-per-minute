package websocket

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	tournamentws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/tournament"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestTournamentParticipantEventRoundTripIsRoleExclusive(t *testing.T) {
	t.Parallel()
	tournamentID := tournamentSourceID(301)
	playerID := tournamentSourceID(302)
	reader := tournamentServerSnapshotReader(t, tournamentID, playerID)
	source, err := NewTournamentProductionSnapshotSource(reader)
	require.NoError(t, err)
	envelope, err := tournamentws.ParticipantRealtimeView(context.Background(), source, tournamentws.ParticipantRealtimeRequest{
		Principal: tournamentws.ParticipantRealtimePrincipal{
			Authenticated: true,
			TournamentID:  tournamentID,
			PlayerID:      playerID,
		},
		TournamentID: tournamentID,
	})
	require.NoError(t, err)
	payload, err := NewTournamentParticipantPayload(envelope)
	require.NoError(t, err)
	encoded, err := MarshalTournamentParticipant(payload)
	require.NoError(t, err)

	participant, err := DecodeTournamentParticipantMessage(encoded)
	require.NoError(t, err)
	require.NotNil(t, participant.Participant)
	require.Equal(t, playerID, participant.Participant.Envelope.Participant.PlayerID)

	_, err = DecodeTournamentPublicMessage(encoded)
	require.ErrorIs(t, err, ErrTournamentRoleMismatch)
	_, err = DecodeTournamentOperatorMessage(encoded)
	require.ErrorIs(t, err, ErrTournamentRoleMismatch)
}

func TestTournamentTerminalEventIsAcceptedByEveryRole(t *testing.T) {
	t.Parallel()

	payload, err := NewTournamentTerminalPayload(
		tournamentSourceID(311),
		17,
		tournamentSourceID(312),
		time.Date(2026, 9, 7, 8, 0, 0, 0, time.UTC),
		domain.TournamentStateCancelled,
	)
	require.NoError(t, err)
	encoded, err := MarshalTournamentTerminal(payload)
	require.NoError(t, err)

	participant, err := DecodeTournamentParticipantMessage(encoded)
	require.NoError(t, err)
	require.Equal(t, payload, *participant.Terminal)
	public, err := DecodeTournamentPublicMessage(encoded)
	require.NoError(t, err)
	require.Equal(t, payload, *public.Terminal)
	operator, err := DecodeTournamentOperatorMessage(encoded)
	require.NoError(t, err)
	require.Equal(t, payload, *operator.Terminal)

	payload.State = domain.TournamentStateSwiss
	_, err = MarshalTournamentTerminal(payload)
	require.ErrorIs(t, err, ErrTournamentInvalidPayload)
}
