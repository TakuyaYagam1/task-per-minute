package state

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
		Attendance: string(domain.AttendanceStateCheckedIn), CurrentSwissRound: 2, SwissPoints: 3,
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
		AttemptState:     string(domain.GameStateActive),
		AttemptStartedAt: participantStateTimestamp(participantStateTestTime().Add(-120 * time.Second)),
		WaveID:           participantStateTestID(15).String(), SlotID: participantStateTestID(19), GameNumber: 1,
		Stage: string(domain.TournamentStageSwiss), SwissRound: 1,
		SeriesFirstParticipantWins: 1, SeriesSecondParticipantWins: 0,
		EffectiveDeadline: participantStateTimestamp(participantStateTestTime()), ParticipantID: participantID,
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
		ObservedAt:              participantStateTimestamp(participantStateTestTime()),
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

func TestParticipantAssignmentContextUsesAuthoritativeDeadlineStates(t *testing.T) {
	t.Parallel()

	startedAt := participantStateTestTime()
	tests := []struct {
		name         string
		mutate       func(*sqlc.GetParticipantStateAssignmentRow)
		wantErr      bool
		wantStart    bool
		wantDeadline bool
	}{
		{
			name:         "active exact task deadline",
			wantStart:    true,
			wantDeadline: true,
		},
		{
			name:    "active deadline cannot drift",
			wantErr: true,
			mutate: func(row *sqlc.GetParticipantStateAssignmentRow) {
				row.EffectiveDeadline = participantStateTimestamp(startedAt.Add(40*time.Second + time.Nanosecond))
			},
		},
		{
			name: "active pause has no running deadline",
			mutate: func(row *sqlc.GetParticipantStateAssignmentRow) {
				row.AttemptState = string(domain.GameStatePaused)
				row.ObservedAt = participantStateTimestamp(startedAt.Add(20 * time.Second))
				participantHintActiveClock(row, startedAt.Add(12*time.Second), startedAt.Add(40*time.Second), 28_000)
			},
			wantStart: true,
		},
		{
			name: "resumed pause uses persisted deadline",
			mutate: func(row *sqlc.GetParticipantStateAssignmentRow) {
				row.ObservedAt = participantStateTimestamp(startedAt.Add(80 * time.Second))
				participantHintResumedClock(row, startedAt.Add(12*time.Second), startedAt.Add(40*time.Second), startedAt.Add(50*time.Second))
			},
			wantStart:    true,
			wantDeadline: true,
		},
		{
			name: "ready context is pre-start",
			mutate: func(row *sqlc.GetParticipantStateAssignmentRow) {
				row.AttemptState = string(domain.GameStateReady)
				row.AttemptStartedAt = pgtype.Timestamptz{}
				row.EffectiveDeadline = pgtype.Timestamptz{}
			},
		},
		{
			name: "semifinal evidence has no Swiss round",
			mutate: func(row *sqlc.GetParticipantStateAssignmentRow) {
				row.Stage = string(domain.TournamentStageSemifinal)
				row.SwissRound = 0
			},
			wantStart:    true,
			wantDeadline: true,
		},
		{
			name: "final evidence has no Swiss round",
			mutate: func(row *sqlc.GetParticipantStateAssignmentRow) {
				row.Stage = string(domain.TournamentStageFinal)
				row.SwissRound = 0
			},
			wantStart:    true,
			wantDeadline: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			row := participantHintAssignmentRow()
			if tt.mutate != nil {
				tt.mutate(&row)
			}
			assignment, err := participantAssignmentFromRow(row)
			if tt.wantErr {
				require.ErrorIs(t, err, ErrParticipantStateInvalid)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantStart, assignment.Context.StartedAt != nil)
			require.Equal(t, tt.wantDeadline, assignment.Context.EffectiveDeadline != nil)
			require.Equal(t, uuid.MustParse(row.WaveID), assignment.Context.WaveID)
			require.Equal(t, row.SlotID, assignment.Context.SlotID)
			require.Equal(t, int(row.GameNumber), assignment.Context.GameNumber)
			require.NotEqual(t, domain.TournamentStageGolden, assignment.Context.Stage)
			if assignment.Context.Stage == domain.TournamentStageSwiss {
				require.Equal(t, 1, *assignment.Context.SwissRound)
			} else {
				require.Nil(t, assignment.Context.SwissRound)
			}
		})
	}
}

func TestParticipantAssignmentUnlocksAtActiveBoundaries(t *testing.T) {
	t.Parallel()

	startedAt := participantStateTestTime()
	tests := []struct {
		name   string
		offset time.Duration
		want   []string
	}{
		{name: "before quarter", offset: 9 * time.Second, want: []string{}},
		{name: "at quarter", offset: 10 * time.Second, want: []string{"first"}},
		{name: "at half", offset: 20 * time.Second, want: []string{"first", "second"}},
		{name: "at three quarters", offset: 30 * time.Second, want: []string{"first", "second", "third"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			row := participantHintAssignmentRow()
			row.AttemptState = string(domain.GameStateActive)
			row.AttemptStartedAt = participantStateTimestamp(startedAt)
			row.ObservedAt = participantStateTimestamp(startedAt.Add(tt.offset))

			assignment, err := participantAssignmentFromRow(row)

			require.NoError(t, err)
			require.Equal(t, tt.want, assignment.ActiveSnapshot.Hints)
		})
	}
}

func TestParticipantAssignmentStartsWithNoHints(t *testing.T) {
	t.Parallel()

	row := participantHintAssignmentRow()
	row.AttemptState = string(domain.GameStateReady)
	row.AttemptStartedAt = pgtype.Timestamptz{}
	row.EffectiveDeadline = pgtype.Timestamptz{}

	assignment, err := participantAssignmentFromRow(row)

	require.NoError(t, err)
	require.NotNil(t, assignment.ActiveSnapshot.Hints)
	require.Empty(t, assignment.ActiveSnapshot.Hints)
}

func TestParticipantAssignmentFreezesHintsDuringPause(t *testing.T) {
	t.Parallel()

	startedAt := participantStateTestTime()
	pauseAt := startedAt.Add(12 * time.Second)
	row := participantHintAssignmentRow()
	row.AttemptState = string(domain.GameStatePaused)
	row.AttemptStartedAt = participantStateTimestamp(startedAt)
	row.ObservedAt = participantStateTimestamp(startedAt.Add(5 * time.Minute))
	participantHintActiveClock(&row, pauseAt, startedAt.Add(40*time.Second), 28_000)

	assignment, err := participantAssignmentFromRow(row)

	require.NoError(t, err)
	require.Equal(t, []string{"first"}, assignment.ActiveSnapshot.Hints)
}

func TestParticipantAssignmentAcceptsPersistedMillisecondPauseResidual(t *testing.T) {
	t.Parallel()

	startedAt := participantStateTestTime()
	frozenAt := startedAt.Add(12 * time.Second)
	originalDeadline := startedAt.Add(40*time.Second + 876*time.Microsecond)
	row := participantHintAssignmentRow()
	row.AttemptState = string(domain.GameStatePaused)
	row.AttemptStartedAt = participantStateTimestamp(startedAt)
	row.ObservedAt = participantStateTimestamp(startedAt.Add(5 * time.Minute))
	participantHintActiveClock(&row, frozenAt, originalDeadline, 28_000)

	assignment, err := participantAssignmentFromRow(row)

	require.NoError(t, err)
	require.Equal(t, []string{"first"}, assignment.ActiveSnapshot.Hints)
}

func TestParticipantAssignmentFreezesHintsWhenPostgresReturnsLocalTime(t *testing.T) {
	t.Parallel()

	location := time.FixedZone("MSK", 3*60*60)
	startedAt := participantStateTestTime()
	pauseAt := startedAt.Add(12 * time.Second)
	row := participantHintAssignmentRow()
	row.AttemptState = string(domain.GameStatePaused)
	row.AttemptStartedAt = participantStateTimestamp(startedAt.In(location))
	row.ObservedAt = participantStateTimestamp(startedAt.Add(5 * time.Minute).In(location))
	participantHintActiveClock(
		&row,
		pauseAt.In(location),
		startedAt.Add(40*time.Second).In(location),
		28_000,
	)

	assignment, err := participantAssignmentFromRow(row)

	require.NoError(t, err)
	require.Equal(t, []string{"first"}, assignment.ActiveSnapshot.Hints)
}

func TestParticipantAssignmentNestedOperatorPauseAndDuplicateResume(t *testing.T) {
	t.Parallel()

	startedAt := participantStateTestTime()
	pauseAt := startedAt.Add(12 * time.Second)
	row := participantHintAssignmentRow()
	row.AttemptState = string(domain.GameStatePaused)
	row.AttemptStartedAt = participantStateTimestamp(startedAt)
	row.ObservedAt = participantStateTimestamp(startedAt.Add(10 * time.Minute))
	participantHintActiveClock(&row, pauseAt, startedAt.Add(40*time.Second), 28_000)

	// A nested Wave/Series operator pause has no independent Game clock. The
	// direct Game pause remains the authority even when the read happens later.
	first, err := participantAssignmentFromRow(row)
	require.NoError(t, err)
	require.Equal(t, []string{"first"}, first.ActiveSnapshot.Hints)

	row.ObservedAt = participantStateTimestamp(startedAt.Add(20 * time.Minute))
	nested, err := participantAssignmentFromRow(row)
	require.NoError(t, err)
	require.Equal(t, first.ActiveSnapshot.Hints, nested.ActiveSnapshot.Hints)

	resumedAt := startedAt.Add(50 * time.Second)
	row.AttemptState = string(domain.GameStateActive)
	row.ObservedAt = participantStateTimestamp(startedAt.Add(58 * time.Second))
	participantHintResumedClock(&row, pauseAt, startedAt.Add(40*time.Second), resumedAt)
	resumed, err := participantAssignmentFromRow(row)
	require.NoError(t, err)
	require.Equal(t, []string{"first", "second"}, resumed.ActiveSnapshot.Hints)

	row.ObservedAt = participantStateTimestamp(startedAt.Add(59 * time.Second))
	repeated, err := participantAssignmentFromRow(row)
	require.NoError(t, err)
	require.Equal(t, resumed.ActiveSnapshot.Hints, repeated.ActiveSnapshot.Hints)
}

func TestParticipantAssignmentShiftsHintsOnceAfterResumeAndReconstructs(t *testing.T) {
	t.Parallel()

	startedAt := participantStateTestTime()
	pauseAt := startedAt.Add(12 * time.Second)
	resumedAt := startedAt.Add(50 * time.Second)
	row := participantHintAssignmentRow()
	row.AttemptState = string(domain.GameStateActive)
	row.AttemptStartedAt = participantStateTimestamp(startedAt)
	row.ObservedAt = participantStateTimestamp(startedAt.Add(58 * time.Second))
	participantHintResumedClock(&row, pauseAt, startedAt.Add(40*time.Second), resumedAt)

	first, err := participantAssignmentFromRow(row)
	require.NoError(t, err)
	require.Equal(t, []string{"first", "second"}, first.ActiveSnapshot.Hints)

	second, err := participantAssignmentFromRow(row)
	require.NoError(t, err)
	require.Equal(t, first.ActiveSnapshot.Hints, second.ActiveSnapshot.Hints)
}

func TestParticipantAssignmentReplayAttemptDoesNotInheritHints(t *testing.T) {
	t.Parallel()

	startedAt := participantStateTestTime()
	row := participantHintAssignmentRow()
	row.AttemptState = string(domain.GameStateActive)
	row.AttemptStartedAt = participantStateTimestamp(startedAt)
	row.ObservedAt = participantStateTimestamp(startedAt.Add(30 * time.Second))
	completed, err := participantAssignmentFromRow(row)
	require.NoError(t, err)
	require.Equal(t, []string{"first", "second", "third"}, completed.ActiveSnapshot.Hints)

	row.AttemptID = participantStateTestID(120)
	row.GameID = row.AttemptID
	row.AttemptStartedAt = participantStateTimestamp(startedAt.Add(25 * time.Second))
	row.EffectiveDeadline = participantStateTimestamp(startedAt.Add(65 * time.Second))
	row.ObservedAt = participantStateTimestamp(startedAt.Add(26 * time.Second))
	row.GamePauseID = uuid.Nil
	row.GamePauseGameAttemptID = uuid.NullUUID{}
	row.GamePauseState = ""
	row.GamePauseStartedAt = pgtype.Timestamptz{}
	row.GamePauseOriginalDeadline = pgtype.Timestamptz{}
	row.GamePauseFrozenAt = pgtype.Timestamptz{}
	row.GamePauseFrozenRemainingMs = nil
	row.GamePauseResumedAt = pgtype.Timestamptz{}
	row.GamePauseResumedDeadline = pgtype.Timestamptz{}

	replay, err := participantAssignmentFromRow(row)
	require.NoError(t, err)
	require.Empty(t, replay.ActiveSnapshot.Hints)
}

func TestParticipantAssignmentCompletedAttemptExposesUnlockedHints(t *testing.T) {
	t.Parallel()

	row := participantHintAssignmentRow()
	row.AttemptState = string(domain.GameStateCompleted)
	row.AttemptStartedAt = participantStateTimestamp(participantStateTestTime())
	row.EffectiveDeadline = pgtype.Timestamptz{}
	row.ObservedAt = participantStateTimestamp(participantStateTestTime().Add(time.Minute))

	assignment, err := participantAssignmentFromRow(row)

	require.NoError(t, err)
	require.Equal(t, []string{"first", "second", "third"}, assignment.ActiveSnapshot.Hints)
}

func TestParticipantAssignmentRejectsMalformedHintClock(t *testing.T) {
	t.Parallel()

	startedAt := participantStateTestTime()
	valid := participantHintAssignmentRow()
	valid.AttemptState = string(domain.GameStateActive)
	valid.AttemptStartedAt = participantStateTimestamp(startedAt)
	valid.ObservedAt = participantStateTimestamp(startedAt.Add(30 * time.Second))

	tests := []struct {
		name   string
		mutate func(*sqlc.GetParticipantStateAssignmentRow)
	}{
		{name: "unknown attempt state", mutate: func(row *sqlc.GetParticipantStateAssignmentRow) {
			row.AttemptState = "broken"
		}},
		{name: "missing active start", mutate: func(row *sqlc.GetParticipantStateAssignmentRow) {
			row.AttemptStartedAt = pgtype.Timestamptz{}
		}},
		{name: "paused without clock", mutate: func(row *sqlc.GetParticipantStateAssignmentRow) {
			row.AttemptState = string(domain.GameStatePaused)
		}},
		{name: "clock belongs to another attempt", mutate: func(row *sqlc.GetParticipantStateAssignmentRow) {
			row.AttemptState = string(domain.GameStatePaused)
			participantHintActiveClock(row, startedAt.Add(12*time.Second), startedAt.Add(40*time.Second), 28_000)
			row.GamePauseGameAttemptID = uuid.NullUUID{UUID: participantStateTestID(113), Valid: true}
		}},
		{name: "clock duration mismatch", mutate: func(row *sqlc.GetParticipantStateAssignmentRow) {
			row.AttemptState = string(domain.GameStatePaused)
			participantHintActiveClock(row, startedAt.Add(12*time.Second), startedAt.Add(40*time.Second), 27_000)
		}},
		{name: "active clock with resumed evidence", mutate: func(row *sqlc.GetParticipantStateAssignmentRow) {
			row.AttemptState = string(domain.GameStatePaused)
			participantHintResumedClock(row, startedAt.Add(12*time.Second), startedAt.Add(40*time.Second), startedAt.Add(50*time.Second))
			row.GamePauseState = "active"
		}},
		{name: "resumed clock with duplicate shift", mutate: func(row *sqlc.GetParticipantStateAssignmentRow) {
			row.AttemptState = string(domain.GameStateActive)
			participantHintResumedClock(row, startedAt.Add(12*time.Second), startedAt.Add(40*time.Second), startedAt.Add(50*time.Second))
			row.GamePauseResumedDeadline = participantStateTimestamp(startedAt.Add(79 * time.Second))
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			row := valid
			tt.mutate(&row)

			_, err := participantAssignmentFromRow(row)

			require.ErrorIs(t, err, ErrParticipantStateInvalid)
		})
	}
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

func TestParticipantSeriesPreservesCompletedBO1Result(t *testing.T) {
	t.Parallel()

	root := participantStateRoot{
		tournamentID: participantStateTestID(27), rosterID: participantStateTestID(28),
		participantID: participantStateTestID(29),
	}
	secondParticipantID := participantStateTestID(30)
	winnerID := secondParticipantID
	scoreRevisionID := domain.SeriesScoreRevisionID(participantStateTestID(31))
	resultRevisionID := domain.OfficialResultRevisionID(participantStateTestID(32))
	seriesRow := sqlc.Series{
		ID: root.participantID, TournamentID: root.tournamentID, RosterID: root.rosterID,
		FirstParticipantID: root.participantID, SecondParticipantID: secondParticipantID,
		Format: string(domain.SeriesFormatBO1), State: string(domain.SeriesStateCompleted),
		FirstParticipantWins: 0, SecondParticipantWins: 1, WinnerID: uuid.NullUUID{UUID: winnerID, Valid: true},
		CurrentScoreRevisionID:  uuid.NullUUID{UUID: uuid.UUID(scoreRevisionID), Valid: true},
		CurrentResultRevisionID: uuid.NullUUID{UUID: uuid.UUID(resultRevisionID), Valid: true}, Revision: 2,
	}
	slotID := participantStateTestID(33)
	graph := []sqlc.ListParticipantStateSeriesGraphRow{{
		GameSlot: sqlc.GameSlot{
			ID: slotID, SeriesID: seriesRow.ID, RosterID: root.rosterID, SlotNumber: 1,
			Category: string(domain.CategoryWeb), Revision: 1,
		},
		GameAttempt: sqlc.GameAttempt{
			ID: participantStateTestID(34), SlotID: slotID, SeriesID: seriesRow.ID,
			RosterID: root.rosterID, AttemptNumber: 1, State: string(domain.GameStateCompleted),
			ResultReason:     participantStateString(string(domain.GameResultReasonSolved)),
			WinnerID:         uuid.NullUUID{UUID: winnerID, Valid: true},
			ResultRevisionID: uuid.NullUUID{UUID: uuid.UUID(resultRevisionID), Valid: true}, Revision: 1,
		},
	}}

	series, err := participantSeriesFromRows(seriesRow, graph, root)

	require.NoError(t, err)
	require.Equal(t, domain.SeriesStateCompleted, series.State)
	require.Equal(t, domain.SeriesScore{SecondParticipantWins: 1}, series.Score)
	require.Equal(t, winnerID, *series.WinnerID)
	require.Equal(t, scoreRevisionID, *series.CurrentScoreRevisionID)
	require.Equal(t, resultRevisionID, *series.CurrentResultRevisionID)
	require.Len(t, series.Slots, 1)
	require.Equal(t, domain.GameStateCompleted, series.Slots[0].Attempts[0].State)
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

func TestParticipantWaveAcceptsOnlyAuthoritativeByeMemberWithoutSeries(t *testing.T) {
	t.Parallel()

	root := participantStateRoot{
		tournamentID: participantStateTestID(40), participantID: participantStateTestID(41),
	}
	row := sqlc.GetParticipantStateWaveRow{
		WaveID: participantStateTestID(42), TournamentID: root.tournamentID,
		WaveRevisionID: participantStateTestID(43), WaveRevision: 2,
		WaveState:        string(domain.WaveStatePlanned),
		ByeParticipantID: uuid.NullUUID{UUID: root.participantID, Valid: true},
	}
	pairedParticipantID := participantStateTestID(44)
	seriesID := participantStateTestID(45)
	members := []sqlc.ListParticipantStateWaveMembersRow{
		{ParticipantID: root.participantID, ReadinessRevision: 1, SeriesID: "", SeriesCount: 0},
		{ParticipantID: pairedParticipantID, ReadinessRevision: 1, SeriesID: seriesID.String(), SeriesCount: 1},
	}

	view, err := participantWaveFromRows(row, members, root)

	require.NoError(t, err)
	require.Equal(t, root.participantID, *view.ByeParticipantID)
	_, paired := view.SeriesIDs[root.participantID]
	require.False(t, paired)
	require.Equal(t, seriesID, view.SeriesIDs[pairedParticipantID])

	members[0].SeriesID = ""
	row.ByeParticipantID = uuid.NullUUID{}
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

func participantHintAssignmentRow() sqlc.GetParticipantStateAssignmentRow {
	assignmentID := participantStateTestID(110)
	participantID := participantStateTestID(111)
	return sqlc.GetParticipantStateAssignmentRow{
		AssignmentID: assignmentID, AttemptID: participantStateTestID(112),
		SeriesID: participantStateTestID(113), GameID: participantStateTestID(114),
		AttemptState:     string(domain.GameStateActive),
		AttemptStartedAt: participantStateTimestamp(participantStateTestTime()),
		WaveID:           participantStateTestID(115).String(), SlotID: participantStateTestID(119), GameNumber: 1,
		Stage: string(domain.TournamentStageSwiss), SwissRound: 1,
		SeriesFirstParticipantWins: 1, SeriesSecondParticipantWins: 0,
		EffectiveDeadline: participantStateTimestamp(participantStateTestTime().Add(40 * time.Second)), ParticipantID: participantID,
		SnapshotID: participantStateTestID(116), TaskID: participantStateTestID(117),
		TaskVersion: 3, Kind: string(domain.AssignmentTaskKindNormal),
		Title: "web task", Description: "solve the disclosed service",
		Category: string(domain.CategoryWeb), Difficulty: string(domain.DifficultyMedium),
		TimeLimit: 40, Hints: []byte(`[
    "first",
    "second",
    "third"
]`),
		TaskUrl:                 participantStateString("challenge.internal:8080"),
		ReceiptID:               participantStateTestID(118),
		InstanceID:              domain.ParticipantTaskInstanceID(assignmentID, participantID),
		DeliveredAt:             participantStateTimestamp(participantStateTestTime()),
		UndisclosedReserveCount: 2,
		ObservedAt:              participantStateTimestamp(participantStateTestTime()),
	}
}

func participantHintActiveClock(
	row *sqlc.GetParticipantStateAssignmentRow,
	frozenAt, originalDeadline time.Time,
	remainingMS int64,
) {
	row.EffectiveDeadline = pgtype.Timestamptz{}
	row.GamePauseID = participantStateTestID(119)
	row.GamePauseGameAttemptID = uuid.NullUUID{UUID: row.AttemptID, Valid: true}
	row.GamePauseState = "active"
	row.GamePauseStartedAt = participantStateTimestamp(frozenAt)
	row.GamePauseOriginalDeadline = participantStateTimestamp(originalDeadline)
	row.GamePauseFrozenAt = participantStateTimestamp(frozenAt)
	row.GamePauseFrozenRemainingMs = &remainingMS
}

func participantHintResumedClock(
	row *sqlc.GetParticipantStateAssignmentRow,
	frozenAt, originalDeadline time.Time,
	resumedAt time.Time,
) {
	const remainingMS int64 = 28_000
	participantHintActiveClock(row, frozenAt, originalDeadline, remainingMS)
	row.GamePauseState = "resumed"
	row.GamePauseResumedAt = participantStateTimestamp(resumedAt)
	row.GamePauseResumedDeadline = participantStateTimestamp(resumedAt.Add(time.Duration(remainingMS) * time.Millisecond))
	row.EffectiveDeadline = row.GamePauseResumedDeadline
}
