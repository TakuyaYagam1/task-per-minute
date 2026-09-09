package websocket

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	tournamentws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/tournament"
	tournamentsnapshot "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	tournamentsnapshotmocks "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound/mocks"
)

func TestTournamentProductionSnapshotSourceUsesCompleteRoleReaders(t *testing.T) {
	t.Parallel()
	tournamentID := tournamentSourceID(1)
	playerID := tournamentSourceID(2)
	operatorID := tournamentSourceID(3)
	reader := tournamentsnapshotmocks.NewMockTournamentSnapshotUseCase(t)
	reader.EXPECT().ParticipantSnapshot(mock.Anything, tournamentsnapshot.ParticipantSnapshotQuery{
		TournamentID: tournamentID,
		PlayerID:     playerID,
	}).Return(tournamentsnapshot.ParticipantSnapshotView{
		TournamentID: tournamentID,
		PlayerID:     playerID,
		Cursor:       tournamentSourceCursor(),
		Assignment: &tournamentsnapshot.ParticipantAssignmentView{
			AssignmentID: tournamentSourceID(4),
			AttemptID:    tournamentSourceID(5),
			SeriesID:     tournamentSourceID(6),
			GameID:       tournamentSourceID(7),
			WaveID:       tournamentSourceID(8),
			Task: tournamentsnapshot.ParticipantTaskView{
				SnapshotID:       tournamentSourceID(9),
				TaskID:           tournamentSourceID(10),
				Title:            "Web",
				Category:         "web",
				Difficulty:       "medium",
				TimeLimitSeconds: 300,
			},
		},
		Opponent: &tournamentsnapshot.ParticipantOpponentView{
			PlayerID:    tournamentSourceID(11),
			DisplayName: "opponent",
			SeriesID:    tournamentSourceID(6),
			Ready:       true,
			SeriesState: "active",
			Score:       1,
		},
	}, nil).Once()
	reader.EXPECT().PublicSnapshot(
		mock.Anything,
		tournamentsnapshot.PublicSnapshotQuery{TournamentID: tournamentID},
	).Return(tournamentSourcePublicView(tournamentID), nil).Once()
	reader.EXPECT().OperatorSnapshot(mock.Anything, tournamentsnapshot.OperatorSnapshotQuery{
		TournamentID: tournamentID,
		OperatorID:   operatorID,
	}).Return(tournamentSourceOperatorView(tournamentID), nil).Once()

	source, err := NewTournamentProductionSnapshotSource(reader)
	require.NoError(t, err)

	participant, err := source.ReadParticipantRealtime(context.Background(), tournamentws.ParticipantRealtimeReadQuery{
		TournamentID: tournamentID,
		PlayerID:     playerID,
	})
	require.NoError(t, err)
	require.NotNil(t, participant.Payload.Assignment)
	require.NotNil(t, participant.Payload.Opponent)
	require.Equal(t, "Web", participant.Payload.Assignment.Task.Title)

	public, err := source.PublicRealtimeRead(context.Background(), tournamentID)
	require.NoError(t, err)
	require.Len(t, public.Snapshot.Scoreboard, 1)
	require.Len(t, public.Snapshot.Bracket, 1)
	require.Len(t, public.Snapshot.LiveSeries, 1)
	require.Len(t, public.Snapshot.OfficialResults, 1)
	require.NotNil(t, public.Snapshot.Draft)

	operator, err := source.ReadOperatorRealtime(context.Background(), tournamentws.OperatorRealtimeQuery{
		TournamentID: tournamentID,
		OperatorID:   operatorID,
	})
	require.NoError(t, err)
	require.NotNil(t, operator.Operator)
	require.Len(t, operator.Operator.Waves, 1)
	require.Len(t, operator.Operator.Presence, 1)
	require.Len(t, operator.Operator.Replays, 1)
	require.Len(t, operator.Operator.AuditLinks, 1)
	require.NotNil(t, operator.Operator.Pause)
}

func TestTournamentProductionSnapshotSourceRequiresReader(t *testing.T) {
	t.Parallel()

	source, err := NewTournamentProductionSnapshotSource(nil)
	require.ErrorIs(t, err, ErrTournamentSnapshotSource)
	require.Nil(t, source)
}

func tournamentSourcePublicView(tournamentID uuid.UUID) tournamentsnapshot.PublicSnapshotView {
	seriesID := tournamentSourceID(20)
	return tournamentsnapshot.PublicSnapshotView{
		Cursor: tournamentSourceCursor(),
		Tournament: tournamentsnapshot.PublicTournamentView{
			TournamentID: tournamentID,
			Preset:       "tournament_v1",
			State:        "swiss",
			RosterSize:   2,
		},
		Scoreboard: []tournamentsnapshot.PublicScoreboardEntryView{{
			Rank:        1,
			DisplayName: "player",
			Points:      3,
			Buchholz:    1,
		}},
		Bracket: []tournamentsnapshot.PublicBracketMatchView{{
			Stage:             "final",
			Position:          1,
			FirstDisplayName:  "player",
			SecondDisplayName: "opponent",
			FirstWins:         1,
			SecondWins:        0,
			State:             "active",
		}},
		LiveSeries: []tournamentsnapshot.PublicSeriesView{{
			SeriesID:            seriesID,
			Format:              "bo3",
			State:               "active",
			FirstDisplayName:    "player",
			SecondDisplayName:   "opponent",
			FirstWins:           1,
			SecondWins:          0,
			CurrentGamePosition: 2,
		}},
		OfficialResults: []tournamentsnapshot.PublicOfficialResultView{{
			RevisionID:        tournamentSourceID(21),
			SeriesID:          seriesID,
			State:             "official",
			WinnerDisplayName: "player",
			FirstWins:         2,
			SecondWins:        0,
			RecordedAt:        tournamentSourceTime(),
		}},
		Draft: &tournamentsnapshot.PublicDraftView{
			SeriesID:           seriesID,
			Format:             "ban-pick",
			State:              "active",
			Pool:               []string{"web", "pwn"},
			SelectedCategories: []string{"web"},
			Actions: []tournamentsnapshot.PublicDraftActionView{{
				Turn:             1,
				Action:           "pick",
				Category:         "web",
				ActorDisplayName: "player",
				OccurredAt:       tournamentSourceTime(),
			}},
		},
	}
}

func tournamentSourceOperatorView(tournamentID uuid.UUID) tournamentsnapshot.OperatorSnapshotView {
	seriesID := tournamentSourceID(30)
	return tournamentsnapshot.OperatorSnapshotView{
		TournamentID: tournamentID,
		Cursor:       tournamentSourceCursor(),
		Waves: []tournamentsnapshot.OperatorWaveView{{
			WaveID: tournamentSourceID(31),
			State:  "active",
			Members: []tournamentsnapshot.OperatorWaveMemberView{{
				ParticipantID:     tournamentSourceID(32),
				SeriesID:          &seriesID,
				Ready:             true,
				ReadinessRevision: 2,
			}},
		}},
		Presence: []tournamentsnapshot.OperatorPresenceView{{
			ParticipantID: tournamentSourceID(32),
			SeriesID:      seriesID,
			State:         "connected",
			PresenceEpoch: 2,
			UpdatedAt:     tournamentSourceTime(),
		}},
		Replays: []tournamentsnapshot.OperatorReplayView{{
			SeriesID:          seriesID,
			SlotID:            tournamentSourceID(33),
			FailedGameID:      tournamentSourceID(34),
			ReplacementGameID: tournamentSourceID(35),
			ReplacementWaveID: tournamentSourceID(36),
			State:             "planned",
			Revision:          1,
		}},
		Pause: &tournamentsnapshot.OperatorPauseView{
			PauseID:       tournamentSourceID(37),
			State:         "paused",
			Reason:        "operator pause",
			PausedAt:      tournamentSourceTime(),
			GraphRevision: 3,
		},
		AuditLinks: []tournamentsnapshot.OperatorAuditLinkView{{
			AuditEventID:             tournamentSourceID(38),
			EntityKind:               "series",
			EntityID:                 seriesID,
			OfficialResultRevisionID: tournamentSourceID(39),
		}},
	}
}

func tournamentSourceCursor() tournamentsnapshot.SnapshotCursor {
	return tournamentsnapshot.SnapshotCursor{
		ProjectionRevision: 7,
		ObservedAt:         tournamentSourceTime(),
	}
}

func tournamentSourceID(value int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("94000000-0000-4000-8000-%012d", value))
}

func tournamentSourceTime() time.Time {
	return time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
}
