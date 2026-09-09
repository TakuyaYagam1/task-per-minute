package postgres

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
)

const replayStorageSchemaVersion = 1

var errInvalidReplayStorageDocument = errors.New("invalid replay storage document")

type replayStorageDocument[T any] struct {
	SchemaVersion int `json:"schema_version"`
	Payload       T   `json:"payload"`
}

// replayDocumentScope contains stable normalized identities only. Snapshot
// content remains in task_snapshots and is rehydrated by the repository.
type replayDocumentScope struct {
	TournamentID uuid.UUID `json:"tournament_id"`
	OldWaveID    uuid.UUID `json:"old_wave_id"`
	SeriesID     uuid.UUID `json:"series_id"`
	SlotID       uuid.UUID `json:"slot_id"`
	AssignmentID uuid.UUID `json:"assignment_id"`
}

// replayReserveExhaustionDocument is the retained, non-secret link between a
// failed attempt and the operator reserve path. The repository reloads the
// actual game, series, wave, route, and result rows under lock before it
// presents this evidence to the application layer.
type replayReserveExhaustionDocument struct {
	CommandID                      uuid.UUID           `json:"command_id"`
	Scope                          replayDocumentScope `json:"scope"`
	ExpectedAuthorityRevision      int64               `json:"expected_authority_revision"`
	FailedAttemptCommandID         uuid.UUID           `json:"failed_attempt_command_id"`
	FailedAttemptAuthorityRevision int64               `json:"failed_attempt_authority_revision"`
	FailureClass                   string              `json:"failure_class"`
	GameResultOrdinal              int                 `json:"game_result_ordinal"`
	ScoreOrdinal                   int                 `json:"score_ordinal"`
	RouteID                        uuid.UUID           `json:"route_id"`
	AuditEventID                   uuid.UUID           `json:"audit_event_id"`
	OutboxEventID                  uuid.UUID           `json:"outbox_event_id"`
	ProjectionRevisionID           uuid.UUID           `json:"projection_revision_id"`
	SourceProjectionRevision       int64               `json:"source_projection_revision"`
	ClosureCommandID               uuid.UUID           `json:"closure_command_id"`
	ClosureAuthorityRevision       int64               `json:"closure_authority_revision"`
	PreviousClosureRevisionID      uuid.UUID           `json:"previous_closure_revision_id"`
	AssignmentAttemptID            uuid.UUID           `json:"assignment_attempt_id"`
	FailedGameID                   uuid.UUID           `json:"failed_game_id"`
	ClosureRevisionID              uuid.UUID           `json:"closure_revision_id"`
	ActiveSnapshotID               uuid.UUID           `json:"active_snapshot_id"`
	ReservePosition                int                 `json:"reserve_position"`
	Category                       string              `json:"category"`
	SourceSeriesRevision           int64               `json:"source_series_revision"`
	ResultingSeriesRevision        int64               `json:"resulting_series_revision"`
	PausedAt                       time.Time           `json:"paused_at"`
}

// operatorReserveDocument retains command-only operator evidence. In
// particular, it intentionally omits task text, hints, flags, and delivery
// history, all of which must come from normalized rows under the same lock.
type operatorReserveDocument struct {
	CommandID                   uuid.UUID           `json:"command_id"`
	Scope                       replayDocumentScope `json:"scope"`
	ExpectedExhaustionCommandID uuid.UUID           `json:"expected_exhaustion_command_id"`
	ExpectedAuthorityRevision   int64               `json:"expected_authority_revision"`
	AssignmentAttemptID         uuid.UUID           `json:"assignment_attempt_id"`
	FailedGameID                uuid.UUID           `json:"failed_game_id"`
	ClosureRevisionID           uuid.UUID           `json:"closure_revision_id"`
	FromSnapshotID              uuid.UUID           `json:"from_snapshot_id"`
	ProposedTaskID              uuid.UUID           `json:"proposed_task_id"`
	ProposedVersion             int                 `json:"proposed_version"`
	ProposedSnapshotID          uuid.UUID           `json:"proposed_snapshot_id"`
	EvidenceID                  uuid.UUID           `json:"evidence_id"`
	ActorID                     uuid.UUID           `json:"actor_id"`
	Reason                      string              `json:"reason"`
	PromotedAt                  time.Time           `json:"promoted_at"`
}

// replayReplacementDocument retains only command and execution identifiers.
// The repository rehydrates the snapshot, game, Series, and Wave from their
// normalized rows before passing a record to the application layer.
type replayReplacementDocument struct {
	CommandID                 uuid.UUID           `json:"command_id"`
	Scope                     replayDocumentScope `json:"scope"`
	ExpectedAuthorityRevision int64               `json:"expected_authority_revision"`
	AssignmentAttemptID       uuid.UUID           `json:"assignment_attempt_id"`
	FailedGameID              uuid.UUID           `json:"failed_game_id"`
	ClosureRevisionID         uuid.UUID           `json:"closure_revision_id"`
	FromSnapshotID            uuid.UUID           `json:"from_snapshot_id"`
	ReservePosition           int                 `json:"reserve_position"`
	SnapshotID                uuid.UUID           `json:"snapshot_id"`
	ReplacementGameID         uuid.UUID           `json:"replacement_game_id"`
	ReplacementWaveID         uuid.UUID           `json:"replacement_wave_id"`
	WaveRevisionID            uuid.UUID           `json:"wave_revision_id"`
	ReadyWindowID             uuid.UUID           `json:"ready_window_id"`
	ReadyWindowRevisionID     uuid.UUID           `json:"ready_window_revision_id"`
	ActorID                   uuid.UUID           `json:"actor_id"`
	Reason                    string              `json:"reason"`
	OpenedAt                  time.Time           `json:"opened_at"`
}

func encodeReplayStorageDocument[T any](payload T) ([]byte, error) {
	encoded, err := json.Marshal(replayStorageDocument[T]{
		SchemaVersion: replayStorageSchemaVersion,
		Payload:       payload,
	})
	if err != nil {
		return nil, fmt.Errorf("encode replay storage document: %w", err)
	}
	return encoded, nil
}

func decodeReplayStorageDocument[T any](encoded []byte) (T, error) {
	var empty T
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()

	var document replayStorageDocument[T]
	if err := decoder.Decode(&document); err != nil {
		return empty, fmt.Errorf("%w: decode: %w", errInvalidReplayStorageDocument, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return empty, fmt.Errorf("%w: trailing data", errInvalidReplayStorageDocument)
	}
	if document.SchemaVersion != replayStorageSchemaVersion {
		return empty, fmt.Errorf("%w: schema version %d", errInvalidReplayStorageDocument, document.SchemaVersion)
	}
	return document.Payload, nil
}

func replayReplacementDocumentFromRow(row sqlc.ReplayReplacement) replayReplacementDocument {
	return replayReplacementDocument{
		CommandID: row.CommandID,
		Scope: replayDocumentScope{
			TournamentID: row.TournamentID,
			OldWaveID:    row.OldWaveID,
			SeriesID:     row.SeriesID,
			SlotID:       row.SlotID,
			AssignmentID: row.AssignmentID,
		},
		ExpectedAuthorityRevision: row.SourceSeriesRevision,
		AssignmentAttemptID:       row.AssignmentAttemptID,
		FailedGameID:              row.FailedGameID,
		ClosureRevisionID:         row.ClosureRevisionID,
		FromSnapshotID:            row.FromSnapshotID,
		ReservePosition:           int(row.ReservePosition),
		SnapshotID:                row.SnapshotID,
		ReplacementGameID:         row.ReplacementGameID,
		ReplacementWaveID:         row.ReplacementWaveID,
		WaveRevisionID:            row.ReplacementWaveRevisionID,
		ReadyWindowID:             row.ReadyWindowID,
		ReadyWindowRevisionID:     row.ReadyWindowRevisionID,
		ActorID:                   row.ActorID,
		Reason:                    row.Reason,
		OpenedAt:                  row.OpenedAt.Time.Round(0).UTC(),
	}
}

func validateReplayReplacementDocument(
	document replayReplacementDocument,
	row sqlc.ReplayReplacement,
) error {
	if !row.OpenedAt.Valid || !document.OpenedAt.Equal(row.OpenedAt.Time.Round(0).UTC()) ||
		document.CommandID != row.CommandID ||
		document.Scope != (replayDocumentScope{
			TournamentID: row.TournamentID,
			OldWaveID:    row.OldWaveID,
			SeriesID:     row.SeriesID,
			SlotID:       row.SlotID,
			AssignmentID: row.AssignmentID,
		}) ||
		document.ExpectedAuthorityRevision != row.SourceSeriesRevision ||
		document.AssignmentAttemptID != row.AssignmentAttemptID ||
		document.FailedGameID != row.FailedGameID ||
		document.ClosureRevisionID != row.ClosureRevisionID ||
		document.FromSnapshotID != row.FromSnapshotID ||
		document.ReservePosition != int(row.ReservePosition) ||
		document.SnapshotID != row.SnapshotID ||
		document.ReplacementGameID != row.ReplacementGameID ||
		document.ReplacementWaveID != row.ReplacementWaveID ||
		document.WaveRevisionID != row.ReplacementWaveRevisionID ||
		document.ReadyWindowID != row.ReadyWindowID ||
		document.ReadyWindowRevisionID != row.ReadyWindowRevisionID ||
		document.ActorID != row.ActorID || document.Reason != row.Reason {
		return fmt.Errorf("%w: replay replacement identity", errInvalidReplayStorageDocument)
	}
	return nil
}

func operatorReserveDocumentFromRow(row sqlc.OperatorReplayReserve) operatorReserveDocument {
	return operatorReserveDocument{
		CommandID:                   row.CommandID,
		Scope:                       replayDocumentScope{TournamentID: row.TournamentID, OldWaveID: row.OldWaveID, SeriesID: row.SeriesID, SlotID: row.SlotID, AssignmentID: row.AssignmentID},
		ExpectedExhaustionCommandID: row.ExhaustionCommandID,
		ExpectedAuthorityRevision:   row.SourceSeriesRevision,
		AssignmentAttemptID:         row.AssignmentAttemptID,
		FailedGameID:                row.FailedGameID,
		ClosureRevisionID:           row.ClosureRevisionID,
		FromSnapshotID:              row.FromSnapshotID,
		ProposedTaskID:              row.ProposedTaskID,
		ProposedVersion:             int(row.ProposedVersion),
		ProposedSnapshotID:          row.ProposedSnapshotID,
		EvidenceID:                  row.EvidenceID,
		ActorID:                     row.ActorID,
		Reason:                      row.Reason,
		PromotedAt:                  row.PromotedAt.Time.Round(0).UTC(),
	}
}

func validateOperatorReserveDocument(
	document operatorReserveDocument,
	row sqlc.OperatorReplayReserve,
) error {
	if !row.PromotedAt.Valid || !document.PromotedAt.Equal(row.PromotedAt.Time.Round(0).UTC()) ||
		document.CommandID != row.CommandID ||
		document.Scope != (replayDocumentScope{
			TournamentID: row.TournamentID,
			OldWaveID:    row.OldWaveID,
			SeriesID:     row.SeriesID,
			SlotID:       row.SlotID,
			AssignmentID: row.AssignmentID,
		}) ||
		document.ExpectedExhaustionCommandID != row.ExhaustionCommandID ||
		document.ExpectedAuthorityRevision != row.SourceSeriesRevision ||
		document.AssignmentAttemptID != row.AssignmentAttemptID ||
		document.FailedGameID != row.FailedGameID ||
		document.ClosureRevisionID != row.ClosureRevisionID ||
		document.FromSnapshotID != row.FromSnapshotID ||
		document.ProposedTaskID != row.ProposedTaskID ||
		document.ProposedVersion != int(row.ProposedVersion) ||
		document.ProposedSnapshotID != row.ProposedSnapshotID ||
		document.EvidenceID != row.EvidenceID || document.ActorID != row.ActorID ||
		document.Reason != row.Reason {
		return fmt.Errorf("%w: operator reserve identity", errInvalidReplayStorageDocument)
	}
	return nil
}

func validateReplayReserveExhaustionDocument(
	document replayReserveExhaustionDocument,
	row sqlc.ReplayReserveExhaustion,
) error {
	if !row.PausedAt.Valid || !document.PausedAt.Equal(row.PausedAt.Time.Round(0).UTC()) ||
		document.CommandID != row.CommandID ||
		document.Scope != (replayDocumentScope{
			TournamentID: row.TournamentID,
			OldWaveID:    row.OldWaveID,
			SeriesID:     row.SeriesID,
			SlotID:       row.SlotID,
			AssignmentID: row.AssignmentID,
		}) ||
		document.AssignmentAttemptID != row.AssignmentAttemptID ||
		document.FailedGameID != row.FailedGameID ||
		document.ClosureRevisionID != row.ClosureRevisionID ||
		document.ActiveSnapshotID != row.ActiveSnapshotID ||
		document.ReservePosition != int(row.ReservePosition) ||
		document.Category != row.Category ||
		document.SourceSeriesRevision != row.SourceSeriesRevision ||
		document.ResultingSeriesRevision != row.ResultingSeriesRevision {
		return fmt.Errorf("%w: replay reserve exhaustion identity", errInvalidReplayStorageDocument)
	}
	if document.ExpectedAuthorityRevision != row.SourceSeriesRevision ||
		document.FailedAttemptCommandID == uuid.Nil ||
		document.FailedAttemptAuthorityRevision < 1 ||
		document.GameResultOrdinal < 1 || document.ScoreOrdinal < 1 ||
		document.RouteID == uuid.Nil || document.AuditEventID == uuid.Nil ||
		document.OutboxEventID == uuid.Nil || document.ProjectionRevisionID == uuid.Nil ||
		document.SourceProjectionRevision < 1 || document.ClosureCommandID == uuid.Nil ||
		document.ClosureAuthorityRevision < 1 ||
		document.PreviousClosureRevisionID == uuid.Nil ||
		document.PreviousClosureRevisionID == document.ClosureRevisionID {
		return fmt.Errorf("%w: replay reserve exhaustion evidence", errInvalidReplayStorageDocument)
	}
	if _, err := gamedomain.ClassifyFailure(
		gamedomain.FailureClass(document.FailureClass),
		domain.Category(document.Category),
	); err != nil {
		return fmt.Errorf("%w: replay reserve exhaustion failure class", errInvalidReplayStorageDocument)
	}
	return nil
}
