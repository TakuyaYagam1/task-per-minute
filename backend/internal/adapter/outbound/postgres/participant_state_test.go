package postgres

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentparticipant "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/participant"
)

func TestParticipantStateRootPreservesDurableCursors(t *testing.T) {
	t.Parallel()

	tournamentID := participantStateTestID(1)
	playerID := participantStateTestID(2)
	observedAt := participantStateTestTime()
	row := sqlc.GetParticipantStateRootRow{
		TournamentID: tournamentID, TournamentState: string(domain.TournamentStateSwiss),
		RosterID: participantStateTestID(3), RosterLocked: true,
		ParticipantID: participantStateTestID(4), PlayerID: playerID,
		ProjectionRevisionID: participantStateTestID(5), ProjectionRevision: 7,
		ParticipantViewRevision: 31, EventSequence: 12,
		ObservedAt: participantStateTimestamp(observedAt),
	}

	root, err := participantStateRootFromRow(row, tournamentparticipant.StateQuery{
		TournamentID: tournamentID, PlayerID: playerID, ExpectedProjectionRevision: 7,
	})
	require.NoError(t, err)
	require.Equal(t, int64(7), root.projectionRevision)
	require.Equal(t, int64(31), root.participantViewRevision)
	require.Equal(t, int64(12), root.eventSequence)
	require.Equal(t, observedAt, root.observedAt)
}

func TestParticipantAssignmentMapsOnlyDisclosedSnapshot(t *testing.T) {
	t.Parallel()

	assignmentID := participantStateTestID(10)
	participantID := participantStateTestID(11)
	row := sqlc.GetParticipantStateAssignmentRow{
		AssignmentID: assignmentID, AttemptID: participantStateTestID(12),
		SeriesID: participantStateTestID(13), GameID: participantStateTestID(14),
		WaveID: participantStateTestID(15).String(), ParticipantID: participantID,
		SnapshotID: participantStateTestID(16), TaskID: participantStateTestID(17),
		TaskVersion: 3, Kind: string(domain.AssignmentTaskKindNormal),
		Title: "web task", Description: "solve the disclosed service",
		Category: string(domain.CategoryWeb), Difficulty: string(domain.DifficultyMedium),
		TimeLimit: 120, Hints: []byte(`["first","second"]`),
		TaskUrl:                 participantStateString("challenge.internal:8080"),
		ReceiptID:               participantStateTestID(18),
		InstanceID:              domain.ParticipantTaskInstanceID(assignmentID, participantID),
		DeliveredAt:             participantStateTimestamp(participantStateTestTime()),
		UndisclosedReserveCount: 2,
	}

	assignment, err := participantAssignmentFromRow(row)
	require.NoError(t, err)
	require.Equal(t, row.SnapshotID, assignment.ActiveSnapshot.SnapshotID)
	require.Equal(t, []string{"first", "second"}, assignment.ActiveSnapshot.Hints)
	require.Equal(t, 2, assignment.UndisclosedReserveCount)
	require.Equal(t, row.InstanceID, assignment.Receipt.InstanceID)

	row.Hints = []byte(`["one","two","three","private reserve"]`)
	_, err = participantAssignmentFromRow(row)
	require.ErrorIs(t, err, ErrParticipantStateInvalid)
}

func TestParticipantSeriesMapsOrderedGameGraph(t *testing.T) {
	t.Parallel()

	root := participantStateRoot{
		tournamentID: participantStateTestID(20), rosterID: participantStateTestID(21),
		participantID: participantStateTestID(22),
	}
	secondParticipantID := participantStateTestID(23)
	seriesRow := sqlc.Series{
		ID: participantStateTestID(24), TournamentID: root.tournamentID, RosterID: root.rosterID,
		FirstParticipantID: root.participantID, SecondParticipantID: secondParticipantID,
		Format: string(domain.SeriesFormatBO1), State: string(domain.SeriesStateActive), Revision: 2,
	}
	slotID := participantStateTestID(25)
	graph := []sqlc.ListParticipantStateSeriesGraphRow{{
		GameSlot: sqlc.GameSlot{
			ID: slotID, SeriesID: seriesRow.ID, RosterID: root.rosterID, SlotNumber: 1,
			Category: string(domain.CategoryWeb), Revision: 1,
		},
		GameAttempt: sqlc.GameAttempt{
			ID: participantStateTestID(26), SlotID: slotID, SeriesID: seriesRow.ID,
			RosterID: root.rosterID, AttemptNumber: 1,
			State: string(domain.GameStateActive), Revision: 1,
		},
	}}

	series, err := participantSeriesFromRows(seriesRow, graph, root)
	require.NoError(t, err)
	require.Len(t, series.Slots, 1)
	require.Len(t, series.Slots[0].Attempts, 1)
	require.Equal(t, graph[0].GameAttempt.ID, series.Slots[0].Attempts[0].ID)
}

func TestParticipantWaveRequiresOneSeriesPerMember(t *testing.T) {
	t.Parallel()

	root := participantStateRoot{
		tournamentID: participantStateTestID(30), participantID: participantStateTestID(31),
	}
	waveID := participantStateTestID(32)
	openedAt := participantStateTestTime()
	startedAt := openedAt.Add(time.Second)
	consumedAt := startedAt
	state := string(domain.ReadyWindowStateConsumed)
	row := sqlc.GetParticipantStateWaveRow{
		WaveID: waveID, TournamentID: root.tournamentID,
		WaveRevisionID: participantStateTestID(33), WaveRevision: 4,
		WaveState: string(domain.WaveStateActive), StartedAt: participantStateTimestamp(startedAt),
		ReadyWindowID:         uuid.NullUUID{UUID: participantStateTestID(34), Valid: true},
		ReadyWindowRevisionID: uuid.NullUUID{UUID: participantStateTestID(35), Valid: true},
		ReadyWindowState:      &state, OpenedAt: participantStateTimestamp(openedAt),
		Deadline:   participantStateTimestamp(openedAt.Add(15 * time.Second)),
		ConsumedAt: participantStateTimestamp(consumedAt),
	}
	secondParticipantID := participantStateTestID(36)
	seriesID := participantStateTestID(37)
	members := []sqlc.ListParticipantStateWaveMembersRow{
		{ParticipantID: root.participantID, Ready: true, ReadinessRevision: 2, SeriesID: seriesID.String(), SeriesCount: 1},
		{ParticipantID: secondParticipantID, Ready: true, ReadinessRevision: 3, SeriesID: seriesID.String(), SeriesCount: 1},
	}

	wave, err := participantWaveFromRows(row, members, root)
	require.NoError(t, err)
	require.Equal(t, int64(4), wave.Revision)
	require.Equal(t, int64(2), wave.ReadinessRevisions[root.participantID])
	require.Equal(t, seriesID, wave.SeriesIDs[secondParticipantID])

	members[1].SeriesCount = 2
	_, err = participantWaveFromRows(row, members, root)
	require.ErrorIs(t, err, ErrParticipantStateInvalid)
}

func participantStateTestID(value byte) uuid.UUID {
	var id uuid.UUID
	id[6] = 0x40
	id[8] = 0x80
	id[15] = value
	return id
}

func participantStateTestTime() time.Time {
	return time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
}

func participantStateTimestamp(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}

func participantStateString(value string) *string {
	return &value
}
