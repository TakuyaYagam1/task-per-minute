package reconnect

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause"
	executionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/execution"
)

const pausedPresenceDocumentVersion = 1

type pausedPresenceDocument struct {
	Version int                            `json:"version"`
	View    json.RawMessage                `json:"view"`
	Pause   *gameusecase.NormalPauseRecord `json:"normal_pause,omitempty"`
}

func decodePausedPresenceWaveResult(action string, document []byte) ([]byte, *gameusecase.NormalPauseRecord) {
	if action != string(executionusecase.WaveActionPause) {
		return append([]byte(nil), document...), nil
	}
	var envelope pausedPresenceDocument
	if err := json.Unmarshal(document, &envelope); err != nil || envelope.Version != pausedPresenceDocumentVersion || //nolint:musttag // Versioned pause evidence has explicit JSON tags on every persisted field.
		len(envelope.View) == 0 || envelope.Pause == nil {
		return append([]byte(nil), document...), nil
	}
	pause := *envelope.Pause
	return append([]byte(nil), envelope.View...), &pause
}

//nolint:gocyclo // Matching the immutable receipt requires the full stored root identity.
func pausedPresenceNormalPauseRecordMatchesRoot(record gameusecase.NormalPauseRecord, row sqlc.LockTournamentPausedPresenceRootRow, scope pausedomain.GraphScope) bool {
	return record.Scope.TournamentID == scope.TournamentID && record.Scope.RosterID == scope.RosterID && record.Scope.WaveID == scope.WaveID && record.ScopeKind == pausedomain.ScopeWave && record.ScopeID == scope.WaveID && record.PauseID == row.PauseID && record.Reason == gameusecase.PauseReasonOperator && record.State == gameusecase.PauseStateActive && record.Revision >= 1 && record.Graph.ActivePauseID == row.PauseID && record.Graph.Scope.TournamentID == scope.TournamentID && record.Graph.Scope.RosterID == scope.RosterID && record.Graph.Scope.WaveID == scope.WaveID && record.Graph.PausedAt != nil && domain.IsValidServerTime(record.PausedAt) && domain.IsValidServerTime(record.Graph.PausedAt.UTC())
}

var errPausedPresenceRecoverySnapshot = errors.New("invalid recovery terminal snapshot")

func pausedPresenceRecoveryPresence(rows []sqlc.PresenceState) ([]pausedomain.PausePresence, error) {
	result := make([]pausedomain.PausePresence, len(rows))
	for index, row := range rows {
		connectedAt, err := pausedPresenceRequiredTime(row.ConnectedAt)
		if err != nil {
			return nil, fmt.Errorf("%w: Presence connected_at", errPausedPresenceRecoverySnapshot)
		}
		updatedAt, err := pausedPresenceRequiredTime(row.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("%w: Presence updated_at", errPausedPresenceRecoverySnapshot)
		}
		result[index] = pausedomain.PausePresence{
			ID: row.ID, TournamentID: row.TournamentID, RosterID: row.RosterID,
			SeriesID: row.SeriesID, ParticipantID: row.ParticipantID,
			State: pausedomain.PresenceState(row.State), PresenceEpoch: row.PresenceEpoch,
			Revision: row.Revision, ConnectedAt: connectedAt,
			DisconnectedAt: pausedPresenceOptionalTime(row.DisconnectedAt), UpdatedAt: updatedAt,
		}
		if err := result[index].Validate(); err != nil {
			return nil, fmt.Errorf("%w: Presence: %w", errPausedPresenceRecoverySnapshot, err)
		}
	}
	return result, nil
}

func pausedPresenceRequiredTime(value pgtype.Timestamptz) (time.Time, error) {
	if !value.Valid {
		return time.Time{}, errPausedPresenceRecoverySnapshot
	}
	result := value.Time.Round(0).UTC()
	if !domain.IsValidServerTime(result) {
		return time.Time{}, errPausedPresenceRecoverySnapshot
	}
	return result, nil
}

func pausedPresenceOptionalTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time.Round(0).UTC()
	return &result
}

func pausedPresenceTSTZ(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}

func pausedPresenceNullableTSTZ(value *time.Time) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *value, Valid: true}
}
