package replay

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
)

func TestReplayStorageDocumentRoundTrip(t *testing.T) {
	t.Parallel()

	want := replayReplacementDocument{
		CommandID:             uuid.New(),
		Scope:                 replayDocumentScope{TournamentID: uuid.New(), OldWaveID: uuid.New(), SeriesID: uuid.New(), SlotID: uuid.New(), AssignmentID: uuid.New()},
		AssignmentAttemptID:   uuid.New(),
		FailedGameID:          uuid.New(),
		ClosureRevisionID:     uuid.New(),
		ReplacementGameID:     uuid.New(),
		ReplacementWaveID:     uuid.New(),
		WaveRevisionID:        uuid.New(),
		ReadyWindowID:         uuid.New(),
		ReadyWindowRevisionID: uuid.New(),
		ActorID:               uuid.New(),
		Reason:                "replace exhausted replay reserve",
		OpenedAt:              time.Date(2026, 9, 6, 10, 20, 30, 0, time.UTC),
	}

	encoded, err := encodeReplayStorageDocument(want)
	require.NoError(t, err)

	got, err := decodeReplayStorageDocument[replayReplacementDocument](encoded)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestReplayStorageDocumentRejectsUnknownVersion(t *testing.T) {
	t.Parallel()

	_, err := decodeReplayStorageDocument[replayReplacementDocument]([]byte(`{
        "schema_version": 2,
        "payload": {}
    }`))

	require.ErrorIs(t, err, errInvalidReplayStorageDocument)
}

func TestReplayStorageDocumentRejectsMalformedAndUnknownFields(t *testing.T) {
	t.Parallel()

	for name, document := range map[string][]byte{
		"missing_version": []byte(`{"payload": {}}`),
		"malformed_json":  []byte(`{"schema_version": 1,`),
		"unknown_root": []byte(`{
            "schema_version": 1,
            "payload": {},
            "unexpected": true
        }`),
		"unknown_payload": []byte(`{
            "schema_version": 1,
            "payload": {"unexpected": true}
        }`),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := decodeReplayStorageDocument[replayReplacementDocument](document)
			require.ErrorIs(t, err, errInvalidReplayStorageDocument)
		})
	}
}

func TestReplayStorageDocumentDoesNotPersistTaskSecret(t *testing.T) {
	t.Parallel()

	document := operatorReserveDocument{
		CommandID:                   uuid.New(),
		Scope:                       replayDocumentScope{TournamentID: uuid.New(), OldWaveID: uuid.New(), SeriesID: uuid.New(), SlotID: uuid.New(), AssignmentID: uuid.New()},
		ExpectedExhaustionCommandID: uuid.New(),
		AssignmentAttemptID:         uuid.New(),
		FailedGameID:                uuid.New(),
		FromSnapshotID:              uuid.New(),
		ProposedTaskID:              uuid.New(),
		ProposedVersion:             3,
		ProposedSnapshotID:          uuid.New(),
		EvidenceID:                  uuid.New(),
		ActorID:                     uuid.New(),
		Reason:                      "approve one locked reserve",
		PromotedAt:                  time.Date(2026, 9, 6, 10, 20, 30, 0, time.UTC),
	}

	encoded, err := encodeReplayStorageDocument(document)
	require.NoError(t, err)

	serialized := string(encoded)
	require.NotContains(t, serialized, "FLAG{")
	require.NotContains(t, strings.ToLower(serialized), "flag")
	require.NotContains(t, strings.ToLower(serialized), "description")
	require.NotContains(t, strings.ToLower(serialized), "hint")
}

func TestReplayReplacementDocumentBindsNormalizedIdentity(t *testing.T) {
	t.Parallel()

	row := replayReplacementDocumentRow()
	document := replayReplacementDocumentFromRow(row)

	require.NoError(t, validateReplayReplacementDocument(document, row))

	row.SnapshotID = uuid.New()
	require.ErrorIs(t, validateReplayReplacementDocument(document, row), errInvalidReplayStorageDocument)
}

func TestOperatorReserveDocumentBindsNormalizedIdentity(t *testing.T) {
	t.Parallel()

	row := operatorReserveDocumentRow()
	document := operatorReserveDocumentFromRow(row)

	require.NoError(t, validateOperatorReserveDocument(document, row))

	row.ProposedSnapshotID = uuid.New()
	require.ErrorIs(t, validateOperatorReserveDocument(document, row), errInvalidReplayStorageDocument)
}

func TestReplayReserveExhaustionDocumentBindsNormalizedIdentity(t *testing.T) {
	t.Parallel()

	row := replayReserveExhaustionDocumentRow()
	document := replayReserveExhaustionDocument{
		ExpectedAuthorityRevision: 7,
		CommandID:                 row.CommandID,
		Scope: replayDocumentScope{
			TournamentID: row.TournamentID,
			OldWaveID:    row.OldWaveID,
			SeriesID:     row.SeriesID,
			SlotID:       row.SlotID,
			AssignmentID: row.AssignmentID,
		},
		FailedAttemptCommandID:         uuid.New(),
		FailedAttemptAuthorityRevision: 7,
		FailureClass:                   "no_solve",
		GameResultOrdinal:              1,
		ScoreOrdinal:                   1,
		RouteID:                        uuid.New(),
		AuditEventID:                   uuid.New(),
		OutboxEventID:                  uuid.New(),
		ProjectionRevisionID:           uuid.New(),
		SourceProjectionRevision:       1,
		ClosureCommandID:               uuid.New(),
		ClosureAuthorityRevision:       7,
		PreviousClosureRevisionID:      uuid.New(),
		AssignmentAttemptID:            row.AssignmentAttemptID,
		FailedGameID:                   row.FailedGameID,
		ClosureRevisionID:              row.ClosureRevisionID,
		ActiveSnapshotID:               row.ActiveSnapshotID,
		ReservePosition:                int(row.ReservePosition),
		Category:                       row.Category,
		SourceSeriesRevision:           row.SourceSeriesRevision,
		ResultingSeriesRevision:        row.ResultingSeriesRevision,
		PausedAt:                       row.PausedAt.Time,
	}

	require.NoError(t, validateReplayReserveExhaustionDocument(document, row))

	row.AssignmentAttemptID = uuid.New()
	require.ErrorIs(t, validateReplayReserveExhaustionDocument(document, row), errInvalidReplayStorageDocument)
}

func TestReplayReserveExhaustionDocumentRejectsMissingTerminalEvidence(t *testing.T) {
	t.Parallel()

	row := replayReserveExhaustionDocumentRow()
	document := replayReserveExhaustionDocument{
		ExpectedAuthorityRevision:      7,
		CommandID:                      row.CommandID,
		Scope:                          replayDocumentScope{TournamentID: row.TournamentID, OldWaveID: row.OldWaveID, SeriesID: row.SeriesID, SlotID: row.SlotID, AssignmentID: row.AssignmentID},
		FailedAttemptAuthorityRevision: 7,
		FailureClass:                   "no_solve",
		GameResultOrdinal:              1,
		ScoreOrdinal:                   1,
		RouteID:                        uuid.New(),
		AuditEventID:                   uuid.New(),
		OutboxEventID:                  uuid.New(),
		ProjectionRevisionID:           uuid.New(),
		SourceProjectionRevision:       1,
		ClosureCommandID:               uuid.New(),
		ClosureAuthorityRevision:       7,
		PreviousClosureRevisionID:      uuid.New(),
		AssignmentAttemptID:            row.AssignmentAttemptID,
		FailedGameID:                   row.FailedGameID,
		ClosureRevisionID:              row.ClosureRevisionID,
		ActiveSnapshotID:               row.ActiveSnapshotID,
		ReservePosition:                int(row.ReservePosition),
		Category:                       row.Category,
		SourceSeriesRevision:           row.SourceSeriesRevision,
		ResultingSeriesRevision:        row.ResultingSeriesRevision,
		PausedAt:                       row.PausedAt.Time,
	}

	require.ErrorIs(t, validateReplayReserveExhaustionDocument(document, row), errInvalidReplayStorageDocument)
}

func replayReplacementDocumentRow() sqlc.ReplayReplacement {
	openedAt := time.Date(2026, 9, 6, 10, 20, 30, 0, time.UTC)
	return sqlc.ReplayReplacement{
		CommandID:                      uuid.New(),
		TournamentID:                   uuid.New(),
		RosterID:                       uuid.New(),
		OldWaveID:                      uuid.New(),
		SeriesID:                       uuid.New(),
		SlotID:                         uuid.New(),
		AssignmentID:                   uuid.New(),
		AssignmentAttemptID:            uuid.New(),
		FailedGameID:                   uuid.New(),
		ClosureRevisionID:              uuid.New(),
		FromSnapshotID:                 uuid.New(),
		ReplacementAssignmentAttemptID: uuid.New(),
		ReplacementGameID:              uuid.New(),
		ReplacementWaveID:              uuid.New(),
		ReplacementWaveRevisionID:      uuid.New(),
		ReadyWindowID:                  uuid.New(),
		ReadyWindowRevisionID:          uuid.New(),
		SnapshotID:                     uuid.New(),
		ReservePosition:                4,
		SourceSeriesRevision:           7,
		ResultingSeriesRevision:        8,
		ActorID:                        uuid.New(),
		Reason:                         "replace exhausted replay reserve",
		OpenedAt:                       pgtype.Timestamptz{Time: openedAt, Valid: true},
	}
}

func replayReserveExhaustionDocumentRow() sqlc.ReplayReserveExhaustion {
	pausedAt := time.Date(2026, 9, 6, 10, 20, 30, 0, time.UTC)
	return sqlc.ReplayReserveExhaustion{
		CommandID:               uuid.New(),
		TournamentID:            uuid.New(),
		RosterID:                uuid.New(),
		OldWaveID:               uuid.New(),
		SeriesID:                uuid.New(),
		SlotID:                  uuid.New(),
		AssignmentID:            uuid.New(),
		AssignmentAttemptID:     uuid.New(),
		FailedGameID:            uuid.New(),
		ClosureRevisionID:       uuid.New(),
		ActiveSnapshotID:        uuid.New(),
		FromSnapshotID:          uuid.New(),
		ReservePosition:         3,
		Category:                "web",
		SourceSeriesRevision:    7,
		ResultingSeriesRevision: 8,
		PausedAt:                pgtype.Timestamptz{Time: pausedAt, Valid: true},
	}
}

func operatorReserveDocumentRow() sqlc.OperatorReplayReserve {
	promotedAt := time.Date(2026, 9, 6, 10, 20, 30, 0, time.UTC)
	return sqlc.OperatorReplayReserve{
		CommandID:               uuid.New(),
		ExhaustionCommandID:     uuid.New(),
		TournamentID:            uuid.New(),
		OldWaveID:               uuid.New(),
		SeriesID:                uuid.New(),
		SlotID:                  uuid.New(),
		AssignmentID:            uuid.New(),
		AssignmentAttemptID:     uuid.New(),
		FailedGameID:            uuid.New(),
		ClosureRevisionID:       uuid.New(),
		FromSnapshotID:          uuid.New(),
		ActorID:                 uuid.New(),
		Reason:                  "approve one locked reserve",
		ProposedTaskID:          uuid.New(),
		ProposedVersion:         3,
		ProposedSnapshotID:      uuid.New(),
		EvidenceID:              uuid.New(),
		SourceSeriesRevision:    7,
		ResultingSeriesRevision: 8,
		PromotedAt:              pgtype.Timestamptz{Time: promotedAt, Valid: true},
	}
}
