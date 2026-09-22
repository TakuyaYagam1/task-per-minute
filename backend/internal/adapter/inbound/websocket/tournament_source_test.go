package websocket

import (
	"context"
	"encoding/json"
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
		Game: &tournamentsnapshot.ParticipantGameView{
			GameID: tournamentSourceID(12), State: "paused", Revision: 4,
			Pause: &tournamentsnapshot.ParticipantGamePauseView{
				PauseID: tournamentSourceID(13), State: "active", FrozenAt: tournamentSourceTime(), FrozenRemainingMS: 60000,
				ResumedAt:         func() *time.Time { value := tournamentSourceTime().Add(time.Second); return &value }(),
				ResumedDeadline:   func() *time.Time { value := tournamentSourceTime().Add(time.Minute); return &value }(),
				ReconnectDeadline: func() *time.Time { value := tournamentSourceTime().Add(30 * time.Second); return &value }(),
			},
		},
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
	require.NotNil(t, participant.Payload.Game)
	require.Equal(t, tournamentSourceID(12), participant.Payload.Game.GameID)
	require.Equal(t, "paused", participant.Payload.Game.State)
	require.Equal(t, int64(4), participant.Payload.Game.Revision)
	require.NotNil(t, participant.Payload.Game.Pause)
	require.Equal(t, tournamentSourceID(13), participant.Payload.Game.Pause.PauseID)
	require.Equal(t, int64(60000), participant.Payload.Game.Pause.FrozenRemainingMS)
	require.NotNil(t, participant.Payload.Game.Pause.ResumedAt)
	require.NotNil(t, participant.Payload.Game.Pause.ResumedDeadline)
	require.NotNil(t, participant.Payload.Game.Pause.ReconnectDeadline)
	require.Equal(t, "Web", participant.Payload.Assignment.Task.Title)

	public, err := source.PublicRealtimeRead(context.Background(), tournamentID)
	require.NoError(t, err)
	require.Equal(t, tournamentSourceCursor().EventSequence, public.Snapshot.LastSequence)
	require.Equal(t, tournamentSourceCursor().EventSequence, public.SnapshotMetadata.Sequence)
	require.Len(t, public.Snapshot.Scoreboard, 1)
	require.Equal(t, 1, public.Snapshot.Scoreboard[0].Wins)
	require.Equal(t, "pending", public.Snapshot.Scoreboard[0].QualificationStatus)
	require.Len(t, public.Snapshot.Bracket, 3)
	require.Equal(t, "semifinal", public.Snapshot.Bracket[0].Stage)
	require.Equal(t, "final", public.Snapshot.Bracket[2].Stage)
	require.Nil(t, public.Snapshot.Bracket[2].ScheduledAt)
	require.Len(t, public.Snapshot.LiveSeries, 1)
	require.Equal(t, "swiss", public.Snapshot.LiveSeries[0].Stage)
	require.NotNil(t, public.Snapshot.LiveSeries[0].RoundNumber)
	require.Equal(t, 1, *public.Snapshot.LiveSeries[0].RoundNumber)
	require.Nil(t, public.Snapshot.LiveSeries[0].ScheduledAt)
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
	require.NotNil(t, operator.Operator.Pause.GameID)
	require.Equal(t, tournamentSourceID(40), *operator.Operator.Pause.GameID)
	require.NotNil(t, operator.Operator.Pause.FrozenRemainingMS)
	require.Equal(t, int64(60000), *operator.Operator.Pause.FrozenRemainingMS)
	require.NotNil(t, operator.Operator.Pause.ReconnectDeadline)
}

func TestTournamentProductionSnapshotSourceRequiresReader(t *testing.T) {
	t.Parallel()

	source, err := NewTournamentProductionSnapshotSource(nil)
	require.ErrorIs(t, err, ErrTournamentSnapshotSource)
	require.Nil(t, source)
}

func TestOperatorSnapshotInputMapsGoldenRuntimeProjectionFence(t *testing.T) {
	t.Parallel()

	tournamentID := tournamentSourceID(40)
	group := tournamentsnapshot.GoldenOperatorGroupView{
		GroupID:         tournamentSourceID(41),
		GroupRevisionID: tournamentSourceID(42),
		AttemptID:       tournamentSourceID(43),
		State:           "ready",
		RuntimeRevision: 1,
		ReadyWindowID:   tournamentSourceID(44),
		PositionFrom:    1,
		PositionTo:      2,
		Members: []tournamentsnapshot.GoldenMemberView{
			{ParticipantID: tournamentSourceID(45), Ready: true},
			{ParticipantID: tournamentSourceID(46)},
		},
	}
	view := usecaseOperatorSnapshotView(tournamentID)
	firstInput := operatorSnapshotInput(view, []tournamentsnapshot.GoldenOperatorGroupView{group})
	first, err := tournamentws.NewOperatorSnapshot(
		tournamentws.OperatorSnapshotAccess{Authenticated: true, TournamentID: tournamentID, OperatorID: tournamentSourceID(47)},
		firstInput,
	)
	require.NoError(t, err)
	require.Len(t, first.Golden, 1)
	require.Equal(t, int64(1), first.Golden[0].RuntimeRevision)
	require.Equal(t, tournamentSourceID(44), first.Golden[0].ReadyWindowID)

	group.RuntimeRevision = 2
	secondInput := operatorSnapshotInput(view, []tournamentsnapshot.GoldenOperatorGroupView{group})
	second, err := tournamentws.NewOperatorSnapshot(
		tournamentws.OperatorSnapshotAccess{Authenticated: true, TournamentID: tournamentID, OperatorID: tournamentSourceID(47)},
		secondInput,
	)
	require.NoError(t, err)
	require.Equal(t, first.Revision, second.Revision)
	require.Equal(t, first.Golden[0].RuntimeRevision+1, second.Golden[0].RuntimeRevision)
	require.Equal(t, first.Golden[0].ReadyWindowID, second.Golden[0].ReadyWindowID)
}

func TestParticipantGoldenInputMapsCompleteParticipantTask(t *testing.T) {
	t.Parallel()

	tournamentID := tournamentSourceID(50)
	playerID := tournamentSourceID(51)
	startedAt := tournamentSourceTime()
	deadline := startedAt.Add(180 * time.Second)
	taskURL := "https://golden.example/tasks/immutable"
	view := tournamentsnapshot.GoldenParticipantView{
		TournamentID: tournamentID, ParticipantID: playerID,
		GroupID: tournamentSourceID(52), GroupRevisionID: tournamentSourceID(53), AttemptID: tournamentSourceID(54),
		State: "active", RuntimeRevision: 2, ReadyWindowID: tournamentSourceID(55), Ready: true,
		StartedAt: &startedAt, Deadline: &deadline,
		Task: &tournamentsnapshot.GoldenTaskView{
			AssignmentID: tournamentSourceID(56), SnapshotID: tournamentSourceID(57), TaskID: tournamentSourceID(58),
			Version: 3, Title: "Immutable task", Description: "Inspect the immutable task",
			Category: "web", Difficulty: "medium", TimeLimitSeconds: 180,
			TaskURL: &taskURL, SourceFileAvailable: true,
		},
	}

	input := participantGoldenInput(view)
	require.NotNil(t, input)
	require.NotNil(t, input.Task)
	require.Equal(t, 3, input.Task.Version)
	require.Equal(t, "Inspect the immutable task", input.Task.Description)
	require.Equal(t, &taskURL, input.Task.TaskURL)
	require.True(t, input.Task.SourceFileAvailable)

	snapshot, err := tournamentws.NewParticipantSnapshot(
		tournamentws.ParticipantSnapshotScope{TournamentID: tournamentID, PlayerID: playerID},
		tournamentws.ParticipantSnapshotInput{
			TournamentID: tournamentID, PlayerID: playerID, Revision: 1, LastSequence: 1, Golden: input,
		},
	)
	require.NoError(t, err)
	require.Equal(t, 3, snapshot.Golden.Task.Version)
	require.Equal(t, "Inspect the immutable task", snapshot.Golden.Task.Description)
	require.Equal(t, &taskURL, snapshot.Golden.Task.TaskURL)
	require.True(t, snapshot.Golden.Task.SourceFileAvailable)

	encoded, err := json.Marshal(snapshot)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "flag")
	require.NotContains(t, string(encoded), "hints")
	require.NotContains(t, string(encoded), "source_file_url")
	require.NotContains(t, string(encoded), "content_digest")
}

func usecaseOperatorSnapshotView(tournamentID uuid.UUID) tournamentsnapshot.OperatorSnapshotView {
	return tournamentsnapshot.OperatorSnapshotView{TournamentID: tournamentID, Cursor: tournamentSourceCursor()}
}

func tournamentSourcePublicView(tournamentID uuid.UUID) tournamentsnapshot.PublicSnapshotView {
	seriesID := tournamentSourceID(20)
	return tournamentsnapshot.PublicSnapshotView{
		Cursor: tournamentSourceCursor(),
		Tournament: tournamentsnapshot.PublicTournamentView{
			TournamentID: tournamentID,
			Preset:       "tournament_v1",
			State:        "swiss",
			RosterSize:   4,
		},
		Scoreboard: []tournamentsnapshot.PublicScoreboardEntryView{{
			Rank: 1, DisplayName: "player", Points: 3, Wins: 1, ByeCount: 1,
			Buchholz: 1, QualificationStatus: "pending",
		}},
		Bracket: []tournamentsnapshot.PublicBracketMatchView{
			{
				Stage:             "semifinal",
				Position:          1,
				Format:            "bo1",
				FirstDisplayName:  func() *string { value := "player"; return &value }(),
				SecondDisplayName: func() *string { value := "opponent"; return &value }(),
				FirstWins:         1,
				SecondWins:        0,
				State:             "completed",
				WinnerDisplayName: func() *string { value := "player"; return &value }(),
			},
			{
				Stage:             "semifinal",
				Position:          2,
				Format:            "bo1",
				FirstDisplayName:  func() *string { value := "alpha"; return &value }(),
				SecondDisplayName: func() *string { value := "beta"; return &value }(),
				FirstWins:         1,
				SecondWins:        0,
				State:             "completed",
				WinnerDisplayName: func() *string { value := "alpha"; return &value }(),
			},
			{
				Stage:             "final",
				Position:          1,
				Format:            "bo3",
				FirstDisplayName:  func() *string { value := "player"; return &value }(),
				SecondDisplayName: func() *string { value := "alpha"; return &value }(),
				FirstWins:         1,
				SecondWins:        0,
				State:             "active",
			},
		},
		LiveSeries: []tournamentsnapshot.PublicSeriesView{{
			SeriesID:            seriesID,
			Stage:               "swiss",
			RoundNumber:         func() *int { value := 1; return &value }(),
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
			PauseID:           tournamentSourceID(37),
			State:             "paused",
			Reason:            "operator pause",
			PausedAt:          tournamentSourceTime(),
			GraphRevision:     3,
			GameID:            func() *uuid.UUID { value := tournamentSourceID(40); return &value }(),
			FrozenRemainingMS: func() *int64 { value := int64(60000); return &value }(),
			ReconnectDeadline: func() *time.Time { value := tournamentSourceTime().Add(time.Minute); return &value }(),
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
		EventSequence:      12,
		ObservedAt:         tournamentSourceTime(),
	}
}

func tournamentSourceID(value int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("94000000-0000-4000-8000-%012d", value))
}

func tournamentSourceTime() time.Time {
	return time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
}
