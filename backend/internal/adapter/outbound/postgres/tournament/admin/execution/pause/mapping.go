package pause

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	adminexecution "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/execution"
)

func pauseWaveByID(waves []adminexecution.WaveView, id uuid.UUID) (adminexecution.WaveView, bool) {
	for _, wave := range waves {
		if wave.Wave.ID == id {
			return wave, true
		}
	}
	return adminexecution.WaveView{}, false
}

func pauseCurrentGame(series domain.Series) (*domain.Game, bool) {
	for slotIndex := len(series.Slots) - 1; slotIndex >= 0; slotIndex-- {
		slot := series.Slots[slotIndex]
		if len(slot.Attempts) == 0 {
			continue
		}
		game := slot.Attempts[len(slot.Attempts)-1]
		return &game, true
	}
	return nil, false
}

var errNormalPauseSnapshot = errors.New("invalid recovery terminal snapshot")

func recoveryPresence(rows []sqlc.PresenceState) ([]pausedomain.PausePresence, error) {
	result := make([]pausedomain.PausePresence, len(rows))
	for index, row := range rows {
		connectedAt, err := requiredRecoveryTime(row.ConnectedAt)
		if err != nil {
			return nil, fmt.Errorf("%w: Presence connected_at", errNormalPauseSnapshot)
		}
		updatedAt, err := requiredRecoveryTime(row.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("%w: Presence updated_at", errNormalPauseSnapshot)
		}
		result[index] = pausedomain.PausePresence{
			ID: row.ID, TournamentID: row.TournamentID, RosterID: row.RosterID,
			SeriesID: row.SeriesID, ParticipantID: row.ParticipantID,
			State: pausedomain.PresenceState(row.State), PresenceEpoch: row.PresenceEpoch,
			Revision: row.Revision, ConnectedAt: connectedAt,
			DisconnectedAt: optionalRecoveryTime(row.DisconnectedAt), UpdatedAt: updatedAt,
		}
		if err := result[index].Validate(); err != nil {
			return nil, fmt.Errorf("%w: Presence: %w", errNormalPauseSnapshot, err)
		}
	}
	return result, nil
}

func recoveryIntervals(rows []sqlc.ReconnectInterval) ([]pausedomain.PauseReconnectInterval, error) {
	result := make([]pausedomain.PauseReconnectInterval, len(rows))
	for index, row := range rows {
		openedAt, err := requiredRecoveryTime(row.OpenedAt)
		if err != nil {
			return nil, fmt.Errorf("%w: reconnect opened_at", errNormalPauseSnapshot)
		}
		deadline, err := requiredRecoveryTime(row.DeadlineAt)
		if err != nil {
			return nil, fmt.Errorf("%w: reconnect deadline_at", errNormalPauseSnapshot)
		}
		updatedAt, err := requiredRecoveryTime(row.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("%w: reconnect updated_at", errNormalPauseSnapshot)
		}
		result[index] = pausedomain.PauseReconnectInterval{
			ID: row.ID, PauseID: row.PauseID, RosterID: row.RosterID, SeriesID: row.SeriesID,
			GameID: row.GameAttemptID, ParticipantID: row.ParticipantID,
			PresenceEpoch: row.PresenceEpoch, Number: int(row.IntervalNumber),
			ContinuationNumber: int(row.ContinuationNumber), ContinuedFromID: optionalRecoveryUUID(row.ContinuedFromID),
			SuspendedByPauseID: optionalRecoveryUUID(row.SuspendedByPauseID), State: pausedomain.ReconnectState(row.State),
			OpenedAt: openedAt, Deadline: deadline, ClosedAt: optionalRecoveryTime(row.ClosedAt),
			Revision: row.Revision, UpdatedAt: updatedAt,
		}
		if err := result[index].Validate(); err != nil {
			return nil, fmt.Errorf("%w: reconnect interval: %w", errNormalPauseSnapshot, err)
		}
	}
	return result, nil
}

func recoveryCounters(rows []sqlc.ReconnectSlotCounter) ([]pausedomain.PauseReconnectCounter, error) {
	result := make([]pausedomain.PauseReconnectCounter, len(rows))
	for index, row := range rows {
		result[index] = pausedomain.PauseReconnectCounter{
			PauseID: row.PauseID, RosterID: row.RosterID, ParticipantID: row.ParticipantID,
			Limit: int(row.SlotLimit), Used: int(row.SlotsUsed), Revision: row.Revision,
		}
		if err := result[index].Validate(); err != nil {
			return nil, fmt.Errorf("%w: reconnect counter: %w", errNormalPauseSnapshot, err)
		}
	}
	return result, nil
}

func recoveryGameClock(row sqlc.PauseClock) (pausedomain.PauseResumeGameClock, error) {
	if row.FrozenRemainingMs < 1 || row.FrozenRemainingMs > math.MaxInt64/int64(time.Millisecond) {
		return pausedomain.PauseResumeGameClock{}, fmt.Errorf("%w: invalid frozen duration", errNormalPauseSnapshot)
	}
	originalDeadline, err := requiredRecoveryTime(row.OriginalDeadline)
	if err != nil {
		return pausedomain.PauseResumeGameClock{}, err
	}
	frozenAt, err := requiredRecoveryTime(row.FrozenAt)
	if err != nil {
		return pausedomain.PauseResumeGameClock{}, err
	}
	exactRemaining, err := recoveredFrozenDuration(originalDeadline, frozenAt, row.FrozenRemainingMs)
	if err != nil {
		return pausedomain.PauseResumeGameClock{}, err
	}
	clock := pausedomain.PauseResumeGameClock{
		PauseID: row.PauseID, GameID: row.GameAttemptID, OriginalDeadline: originalDeadline,
		FrozenAt: frozenAt, Remaining: exactRemaining,
		ResumedAt: optionalRecoveryTime(row.ResumedAt), ResumedDeadline: optionalRecoveryTime(row.ResumedDeadline),
		Revision: row.Revision,
	}
	if err := clock.Validate(true); err != nil {
		return pausedomain.PauseResumeGameClock{}, fmt.Errorf("%w: Game clock: %w", errNormalPauseSnapshot, err)
	}
	return clock, nil
}

func recoveredFrozenDuration(originalDeadline, frozenAt time.Time, frozenRemainingMs int64) (time.Duration, error) {
	if frozenRemainingMs < 1 || frozenRemainingMs > math.MaxInt64/int64(time.Millisecond) {
		return 0, fmt.Errorf("%w: invalid frozen duration", errNormalPauseSnapshot)
	}
	persistedRemaining := time.Duration(frozenRemainingMs) * time.Millisecond
	exactRemaining := originalDeadline.Sub(frozenAt)
	if exactRemaining <= 0 || exactRemaining < persistedRemaining || exactRemaining-persistedRemaining >= time.Millisecond {
		return 0, fmt.Errorf("%w: frozen duration differs from persisted milliseconds", errNormalPauseSnapshot)
	}
	return exactRemaining, nil
}

func requiredRecoveryTime(value pgtype.Timestamptz) (time.Time, error) {
	if !value.Valid {
		return time.Time{}, errNormalPauseSnapshot
	}
	result := value.Time.Round(0).UTC()
	if !domain.IsValidServerTime(result) {
		return time.Time{}, errNormalPauseSnapshot
	}
	return result, nil
}

func optionalRecoveryTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time.Round(0).UTC()
	return &result
}

func optionalRecoveryUUID(value uuid.NullUUID) *uuid.UUID {
	if !value.Valid {
		return nil
	}
	result := value.UUID
	return &result
}
