package tournament

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestParticipantRealtimeViewEnforcesAuthenticatedPlayerScope(t *testing.T) {
	t.Parallel()
	tournamentID := uuid.MustParse("91000000-0000-4000-8000-000000000001")
	playerID := uuid.MustParse("91000000-0000-4000-8000-000000000002")
	otherPlayerID := uuid.MustParse("91000000-0000-4000-8000-000000000003")
	snapshot := participantRealtimeTestSnapshot(t, tournamentID, playerID)
	source := NewMockParticipantRealtimeReadSource(t)
	source.EXPECT().
		ReadParticipantRealtime(mock.Anything, ParticipantRealtimeReadQuery{
			TournamentID: tournamentID,
			PlayerID:     playerID,
		}).
		Return(snapshot, nil).
		Once()
	source.EXPECT().
		ReadParticipantRealtime(mock.Anything, ParticipantRealtimeReadQuery{
			TournamentID: tournamentID,
			PlayerID:     otherPlayerID,
		}).
		Return(snapshot, nil).
		Once()

	result, err := ParticipantRealtimeView(context.Background(), source, ParticipantRealtimeRequest{
		Principal: ParticipantRealtimePrincipal{
			Authenticated: true,
			TournamentID:  tournamentID,
			PlayerID:      playerID,
		},
		TournamentID: tournamentID,
	})
	require.NoError(t, err)
	require.Equal(t, playerID, result.Participant.PlayerID)

	_, err = ParticipantRealtimeView(context.Background(), source, ParticipantRealtimeRequest{
		Principal: ParticipantRealtimePrincipal{
			Authenticated: true,
			TournamentID:  tournamentID,
			PlayerID:      otherPlayerID,
		},
		TournamentID: tournamentID,
	})
	require.ErrorIs(t, err, ErrParticipantRealtimePlayerScope)

	_, err = ParticipantRealtimeView(context.Background(), source, ParticipantRealtimeRequest{
		Principal:    ParticipantRealtimePrincipal{TournamentID: tournamentID, PlayerID: playerID},
		TournamentID: tournamentID,
	})
	require.ErrorIs(t, err, ErrParticipantRealtimeUnauthenticated)
}

func TestParticipantRealtimeViewSanitizesSourceFailure(t *testing.T) {
	t.Parallel()
	tournamentID := uuid.MustParse("91000000-0000-4000-8000-000000000011")
	playerID := uuid.MustParse("91000000-0000-4000-8000-000000000012")
	source := NewMockParticipantRealtimeReadSource(t)
	source.EXPECT().
		ReadParticipantRealtime(mock.Anything, ParticipantRealtimeReadQuery{
			TournamentID: tournamentID,
			PlayerID:     playerID,
		}).
		Return(ParticipantRealtimeSnapshot{}, errors.New("private storage detail")).
		Once()
	_, err := ParticipantRealtimeView(context.Background(), source, ParticipantRealtimeRequest{
		Principal: ParticipantRealtimePrincipal{
			Authenticated: true,
			TournamentID:  tournamentID,
			PlayerID:      playerID,
		},
		TournamentID: tournamentID,
	})
	require.ErrorIs(t, err, ErrParticipantRealtimeUnavailable)
	require.NotContains(t, err.Error(), "private storage detail")
}

func participantRealtimeTestSnapshot(
	t *testing.T,
	tournamentID uuid.UUID,
	playerID uuid.UUID,
) ParticipantRealtimeSnapshot {
	t.Helper()
	snapshot, err := NewParticipantSnapshot(
		ParticipantSnapshotScope{TournamentID: tournamentID, PlayerID: playerID},
		ParticipantSnapshotInput{
			TournamentID: tournamentID,
			PlayerID:     playerID,
			Revision:     7,
			LastSequence: 9,
		},
	)
	require.NoError(t, err)
	return ParticipantRealtimeSnapshot{
		Metadata: RealtimeEnvelopeMetadata{
			SchemaVersion:      TournamentRealtimeSchemaVersion,
			TournamentID:       tournamentID,
			Sequence:           9,
			EventID:            uuid.MustParse("91000000-0000-4000-8000-000000000004"),
			OccurredAt:         time.Date(2026, 9, 5, 8, 0, 0, 0, time.UTC),
			ProjectionRevision: 7,
		},
		Payload: snapshot,
	}
}
