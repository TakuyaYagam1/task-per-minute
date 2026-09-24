package terminal

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	reconnectusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/reconnect"
)

// RestoreReconnectCounters restores carried budgets that cannot have a counter
// row until the participant opens a root in the current pause.
func RestoreReconnectCounters(
	persisted []pause.PauseReconnectCounter,
	intervals []pause.PauseReconnectInterval,
	latest *reconnectusecase.ReconnectRecord,
	pauseID uuid.UUID,
	revision int64,
) ([]pause.PauseReconnectCounter, error) {
	if !validReconnectCounterSource(latest, pauseID, revision) {
		return nil, domain.ErrConflict
	}
	counters := slices.Clone(persisted)
	seen := make(map[uuid.UUID]struct{}, 2)
	for _, saved := range latest.ReconnectAuthority.Counters {
		if saved.PauseID != pauseID || saved.Validate() != nil {
			return nil, domain.ErrConflict
		}
		if _, duplicate := seen[saved.ParticipantID]; duplicate {
			return nil, domain.ErrConflict
		}
		seen[saved.ParticipantID] = struct{}{}
		index := slices.IndexFunc(persisted, func(row pause.PauseReconnectCounter) bool {
			return row.ParticipantID == saved.ParticipantID
		})
		if index >= 0 {
			if persisted[index] != saved {
				return nil, domain.ErrConflict
			}
			continue
		}
		hasRoot := slices.ContainsFunc(intervals, func(interval pause.PauseReconnectInterval) bool {
			return interval.PauseID == pauseID && interval.ParticipantID == saved.ParticipantID && interval.ContinuationNumber == 0
		})
		if saved.Used == 0 || hasRoot {
			return nil, domain.ErrConflict
		}
		counters = append(counters, saved)
	}
	if len(counters) != 2 {
		return nil, domain.ErrConflict
	}
	return counters, nil
}

func validReconnectCounterSource(latest *reconnectusecase.ReconnectRecord, pauseID uuid.UUID, revision int64) bool {
	if latest == nil {
		return false
	}
	saved := latest.ReconnectAuthority
	return saved.PauseID == pauseID && saved.Game.State == domain.GameStatePaused && saved.Current == nil &&
		saved.Revision == revision && len(saved.Counters) == 2
}

func (repository *RecoveryTerminalPostgres) restoreReconnectCounters(ctx context.Context, current *reconnectusecase.ReconnectAuthority) error {
	if len(current.Counters) == 2 {
		return nil
	}
	rows, err := repository.tx.Querier(ctx).ListTournamentReconnectCommandReceipts(ctx, sqlc.ListTournamentReconnectCommandReceiptsParams{
		TournamentID: current.Scope.TournamentID, RosterID: current.Scope.RosterID, WaveID: current.Scope.WaveID,
	})
	if err != nil {
		return err
	}
	for _, row := range rows {
		var document struct {
			SchemaVersion int16                            `json:"schema_version"`
			Record        reconnectusecase.ReconnectRecord `json:"record"`
		}
		//nolint:musttag // The versioned receipt contains an application-owned domain record.
		if err := json.Unmarshal(row.RecordDocument, &document); err != nil || document.SchemaVersion != 1 || row.SchemaVersion != 1 {
			return domain.ErrConflict
		}
		saved := document.Record.ReconnectAuthority
		if saved.Game.ID != current.Game.ID || saved.Series.ID != current.Series.ID {
			continue
		}
		if !reconnectCounterReceiptMatches(row, document.Record, *current) {
			return domain.ErrConflict
		}
		current.Counters, err = RestoreReconnectCounters(current.Counters, current.Reconnect, &document.Record, current.PauseID, saved.Revision)
		return err
	}
	return domain.ErrConflict
}

func reconnectCounterReceiptMatches(row sqlc.ReconnectCommandReceipt, record reconnectusecase.ReconnectRecord, current reconnectusecase.ReconnectAuthority) bool {
	saved := record.ReconnectAuthority
	return reconnectCounterScopeMatches(row, saved.Scope, current.Scope) &&
		reconnectCounterAuthorityMatches(saved, current) &&
		row.ResultAuthorityRevision == saved.Revision && row.ExpectedAuthorityRevision == record.ExpectedAuthorityRevision &&
		row.MutationKind == string(record.Kind) && row.RecordedAt.Valid && row.RecordedAt.Time.Equal(record.RecordedAt) &&
		saved.GameRevision == current.GameRevision && saved.SeriesRevision == current.SeriesRevision &&
		reflect.DeepEqual(saved.Game, current.Game) && reflect.DeepEqual(saved.Series, current.Series)
}

func reconnectCounterAuthorityMatches(saved, current reconnectusecase.ReconnectAuthority) bool {
	return saved.PauseID == current.PauseID &&
		saved.Game.ID == current.Game.ID && saved.Series.ID == current.Series.ID
}

func reconnectCounterScopeMatches(row sqlc.ReconnectCommandReceipt, saved, current pause.GraphScope) bool {
	return row.TournamentID == current.TournamentID && row.RosterID == current.RosterID && row.WaveID == current.WaveID &&
		saved.TournamentID == current.TournamentID && saved.RosterID == current.RosterID && saved.WaveID == current.WaveID
}
