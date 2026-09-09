package tournament

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestPublicRealtimeAdapterBoundsConnectionLifetime(t *testing.T) {
	t.Parallel()
	tournamentID := uuid.MustParse("92000000-0000-4000-8000-000000000001")
	source := NewMockPublicRealtimeReadSource(t)
	source.EXPECT().
		PublicRealtimeRead(mock.Anything, tournamentID).
		Return(publicRealtimeTestRead(t, tournamentID), nil).
		Twice()
	adapter, err := NewPublicRealtimeAdapter(source, PublicRealtimeConfig{MaxConnections: 1})
	require.NoError(t, err)

	first, err := adapter.PublicRealtimeOpen(context.Background(), PublicRealtimeOpenRequest{TournamentID: tournamentID})
	require.NoError(t, err)
	require.Equal(t, tournamentID, first.PublicRealtimeEnvelope().TournamentID)

	second, err := adapter.PublicRealtimeOpen(context.Background(), PublicRealtimeOpenRequest{TournamentID: tournamentID})
	require.ErrorIs(t, err, ErrPublicRealtimeConnectionLimit)
	require.Nil(t, second)

	first.PublicRealtimeClose()
	first.PublicRealtimeClose()
	second, err = adapter.PublicRealtimeOpen(context.Background(), PublicRealtimeOpenRequest{TournamentID: tournamentID})
	require.NoError(t, err)
	second.PublicRealtimeClose()
}

func publicRealtimeTestRead(t *testing.T, tournamentID uuid.UUID) PublicRealtimeReadResult {
	t.Helper()
	snapshot, err := NewPublicSnapshot(tournamentID, PublicSnapshotInput{
		Revision:     3,
		LastSequence: 4,
		Tournament: PublicTournamentInput{
			TournamentID: tournamentID,
			Preset:       "tournament_v1",
			State:        "swiss",
			RosterSize:   0,
		},
		Scoreboard:      []PublicScoreboardEntryInput{},
		Bracket:         []PublicBracketMatchInput{},
		LiveSeries:      []PublicSeriesInput{},
		OfficialResults: []PublicOfficialResultInput{},
	})
	require.NoError(t, err)
	return PublicRealtimeReadResult{
		Snapshot: snapshot,
		SnapshotMetadata: RealtimeEnvelopeMetadata{
			SchemaVersion:      TournamentRealtimeSchemaVersion,
			TournamentID:       tournamentID,
			Sequence:           4,
			EventID:            uuid.MustParse("92000000-0000-4000-8000-000000000002"),
			OccurredAt:         time.Date(2026, 9, 5, 8, 0, 0, 0, time.UTC),
			ProjectionRevision: 3,
		},
	}
}
