package replay

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestReplaySourceReconstructsExhaustedTechnicalPause(t *testing.T) {
	t.Parallel()

	source, exhaustion := replayTechnicalPauseSourceFixture(t)

	require.NoError(t, source.validateTechnicalPauseReplaySource(exhaustion))
	record, err := source.replayExhaustion(exhaustion)
	require.NoError(t, err)
	require.Equal(t, domain.SeriesStateReplayRequired, record.PreviousSeries.Series.State)
	require.Equal(t, domain.SeriesStateTechnicalPause, record.Series.Series.State)
	require.NotNil(t, record.Series.ResumeState)
	require.Equal(t, domain.SeriesStateReplayRequired, *record.Series.ResumeState)
	require.NoError(t, record.Validate())
}

func TestReplaySourceRejectsInvalidTechnicalPauseEvidence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*replaySource, *sqlc.ReplayReserveExhaustion)
	}{
		{
			name: "missing exhaustion row",
			mutate: func(_ *replaySource, row *sqlc.ReplayReserveExhaustion) {
				*row = sqlc.ReplayReserveExhaustion{}
			},
		},
		{
			name: "series revision does not match exhaustion",
			mutate: func(_ *replaySource, row *sqlc.ReplayReserveExhaustion) {
				row.ResultingSeriesRevision++
			},
		},
		{
			name: "exhaustion belongs to another roster",
			mutate: func(_ *replaySource, row *sqlc.ReplayReserveExhaustion) {
				row.RosterID = uuid.New()
			},
		},
		{
			name: "record document is malformed",
			mutate: func(_ *replaySource, row *sqlc.ReplayReserveExhaustion) {
				row.RecordDocument = []byte(`{"schema_version":1,"payload":{}}`)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			source, exhaustion := replayTechnicalPauseSourceFixture(t)
			test.mutate(&source, &exhaustion)

			require.ErrorIs(t, source.validateTechnicalPauseReplaySource(exhaustion), errReplayWorkflowAuthority)
		})
	}
}

func replayTechnicalPauseSourceFixture(t *testing.T) (replaySource, sqlc.ReplayReserveExhaustion) {
	t.Helper()

	tournamentID := uuid.New()
	rosterID := uuid.New()
	seriesID := uuid.New()
	oldWaveID := uuid.New()
	slotID := uuid.New()
	gameID := uuid.New()
	assignmentID := uuid.New()
	snapshotID := uuid.New()
	firstParticipantID := uuid.New()
	secondParticipantID := uuid.New()
	seriesScoreRevisionID := uuid.New()
	gameResultRevisionID := uuid.New()
	closureRevisionID := uuid.New()
	previousClosureRevisionID := uuid.New()
	readyWindowID := uuid.New()
	readyWindowRevisionID := uuid.New()
	routeID := uuid.New()
	exhaustionID := uuid.New()

	startedAt := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	failedAt := startedAt.Add(2 * time.Minute)
	closedAt := failedAt.Add(time.Minute)
	readyWindow := sqlTimestamp(startedAt)
	deadline := sqlTimestamp(startedAt.Add(10 * time.Minute))
	consumedAt := sqlTimestamp(startedAt.Add(time.Minute))
	closedTimestamp := sqlTimestamp(closedAt)
	resultReason := "no_solve"

	source := replaySource{
		row: sqlc.LockReplayWorkflowSourceRow{
			Tournament: sqlc.Tournament{ID: tournamentID, State: "swiss"},
			Roster:     sqlc.Roster{ID: rosterID, TournamentID: tournamentID},
			Wave: sqlc.Wave{
				ID: oldWaveID, TournamentID: tournamentID, RosterID: rosterID,
				RevisionID: closureRevisionID, Revision: 2, State: "completed",
				StartedAt: sqlTimestamp(startedAt), ClosedAt: closedTimestamp,
			},
			Series: sqlc.Series{
				ID: seriesID, TournamentID: tournamentID, RosterID: rosterID,
				FirstParticipantID: firstParticipantID, SecondParticipantID: secondParticipantID,
				Format: "bo1", State: "technical_pause", Revision: 8,
				CurrentScoreRevisionID: uuid.NullUUID{UUID: seriesScoreRevisionID, Valid: true},
			},
			GameSlot: sqlc.GameSlot{
				ID: slotID, SeriesID: seriesID, RosterID: rosterID, SlotNumber: 1,
				Category: "web",
			},
			GameAttempt: sqlc.GameAttempt{
				ID: gameID, SlotID: slotID, SeriesID: seriesID, RosterID: rosterID,
				AttemptNumber: 1, State: "void", ResultReason: &resultReason,
				ResultRevisionID: uuid.NullUUID{UUID: gameResultRevisionID, Valid: true},
			},
			Assignment: sqlc.Assignment{
				ID: assignmentID, AttemptID: gameID, SeriesID: seriesID, RosterID: rosterID,
				SnapshotID: snapshotID,
			},
			TaskSnapshot: sqlc.TaskSnapshot{ID: snapshotID},
		},
		graph: []sqlc.LockReplayWorkflowSeriesGraphRow{{
			GameSlot: sqlc.GameSlot{
				ID: slotID, SeriesID: seriesID, RosterID: rosterID, SlotNumber: 1,
				Category: "web",
			},
			GameAttempt: sqlc.GameAttempt{
				ID: gameID, SlotID: slotID, SeriesID: seriesID, RosterID: rosterID,
				AttemptNumber: 1, State: "void", ResultReason: &resultReason,
				ResultRevisionID: uuid.NullUUID{UUID: gameResultRevisionID, Valid: true},
			},
		}},
		gameHeads: []sqlc.LockReplayWorkflowGameResultHeadsRow{{
			GameAttemptID: gameID, ResultRevisionID: gameResultRevisionID,
			ResultHeadRevision: 1, RevisionNumber: 1, ResultState: "void",
			ResultReason: resultReason, CreatedAt: closedTimestamp, OccurredAt: closedTimestamp,
		}},
		scoreHead: sqlc.LockReplayWorkflowScoreHeadRow{
			CurrentRevisionID: seriesScoreRevisionID, HeadRevision: 1,
			ScoreRevisionID: seriesScoreRevisionID, RevisionNumber: 2,
			ScoreRecordedAt: closedTimestamp,
		},
		waveRows: []sqlc.LockReplayWorkflowOldWaveExecutionRow{
			{
				ReadyWindow: sqlc.ReadyWindow{
					ID: readyWindowID, WaveID: oldWaveID, RosterID: rosterID,
					RevisionID: readyWindowRevisionID, State: "consumed",
					OpenedAt: readyWindow, Deadline: deadline, ConsumedAt: consumedAt,
				},
				WaveMember: sqlc.WaveMember{WaveID: oldWaveID, RosterID: rosterID, ParticipantID: firstParticipantID},
				WaveReadiness: sqlc.WaveReadiness{
					ReadyWindowID: uuid.NullUUID{UUID: readyWindowID, Valid: true}, WaveID: oldWaveID,
					RosterID: rosterID, ParticipantID: firstParticipantID, Ready: true,
				},
			},
			{
				ReadyWindow: sqlc.ReadyWindow{
					ID: readyWindowID, WaveID: oldWaveID, RosterID: rosterID,
					RevisionID: readyWindowRevisionID, State: "consumed",
					OpenedAt: readyWindow, Deadline: deadline, ConsumedAt: consumedAt,
				},
				WaveMember: sqlc.WaveMember{WaveID: oldWaveID, RosterID: rosterID, ParticipantID: secondParticipantID},
				WaveReadiness: sqlc.WaveReadiness{
					ReadyWindowID: uuid.NullUUID{UUID: readyWindowID, Valid: true}, WaveID: oldWaveID,
					RosterID: rosterID, ParticipantID: secondParticipantID, Ready: true,
				},
			},
		},
		routes: []sqlc.WaveMemberRoute{{
			ID: routeID, TournamentID: tournamentID, RosterID: rosterID,
			WaveID: oldWaveID, SeriesID: seriesID, SlotID: slotID,
			GameAttemptID: gameID, Category: "web", RoutedAt: closedTimestamp,
		}},
	}

	row := sqlc.ReplayReserveExhaustion{
		CommandID: exhaustionID, TournamentID: tournamentID, RosterID: rosterID,
		OldWaveID: oldWaveID, SeriesID: seriesID, SlotID: slotID, AssignmentID: assignmentID,
		AssignmentAttemptID: gameID, FailedGameID: gameID, ClosureRevisionID: closureRevisionID,
		ActiveSnapshotID: snapshotID, FromSnapshotID: snapshotID,
		ReservePosition: domain.AssignmentReserveCount + 1, Category: "web",
		SourceSeriesRevision: 7, ResultingSeriesRevision: 8, PausedAt: closedTimestamp,
	}
	document := replayReserveExhaustionDocument{
		CommandID: exhaustionID,
		Scope: replayDocumentScope{
			TournamentID: tournamentID, OldWaveID: oldWaveID, SeriesID: seriesID,
			SlotID: slotID, AssignmentID: assignmentID,
		},
		ExpectedAuthorityRevision: 7, FailedAttemptCommandID: uuid.New(),
		FailedAttemptAuthorityRevision: 7, FailureClass: "no_solve",
		GameResultOrdinal: 1, ScoreOrdinal: 2, RouteID: routeID,
		AuditEventID: uuid.New(), OutboxEventID: uuid.New(), ProjectionRevisionID: uuid.New(),
		SourceProjectionRevision: 1, ClosureCommandID: uuid.New(), ClosureAuthorityRevision: 7,
		PreviousClosureRevisionID: previousClosureRevisionID, AssignmentAttemptID: gameID,
		FailedGameID: gameID, ClosureRevisionID: closureRevisionID, ActiveSnapshotID: snapshotID,
		ReservePosition: domain.AssignmentReserveCount + 1, Category: "web",
		SourceSeriesRevision: 7, ResultingSeriesRevision: 8, PausedAt: closedAt,
	}
	encoded, err := encodeReplayStorageDocument(document)
	require.NoError(t, err)
	row.AuthorityDocument = encoded
	row.RecordDocument = encoded
	return source, row
}

func sqlTimestamp(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}
