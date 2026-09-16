package snapshot

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
)

var errRecoveryTerminalSnapshot = errors.New("invalid recovery terminal snapshot")

func recoveryGame(row sqlc.GameAttempt) (domain.Game, error) {
	game := domain.Game{
		ID: row.ID, SlotID: row.SlotID, AttemptNo: int(row.AttemptNumber),
		State: domain.GameState(row.State), ResultReason: domain.GameResultReason(stringValue(row.ResultReason)),
	}
	if row.WinnerID.Valid {
		winnerID := row.WinnerID.UUID
		game.WinnerID = &winnerID
	}
	if row.ResultRevisionID.Valid {
		revisionID := domain.OfficialResultRevisionID(row.ResultRevisionID.UUID)
		game.ResultRevisionID = &revisionID
	}
	if err := game.Validate(); err != nil {
		return domain.Game{}, fmt.Errorf("%w: Game: %w", errRecoveryTerminalSnapshot, err)
	}
	return game, nil
}

func recoveryPresence(rows []sqlc.PresenceState) ([]pause.PausePresence, error) {
	result := make([]pause.PausePresence, len(rows))
	for index, row := range rows {
		connectedAt, err := requiredRecoveryTime(row.ConnectedAt)
		if err != nil {
			return nil, fmt.Errorf("%w: Presence connected_at", errRecoveryTerminalSnapshot)
		}
		updatedAt, err := requiredRecoveryTime(row.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("%w: Presence updated_at", errRecoveryTerminalSnapshot)
		}
		result[index] = pause.PausePresence{
			ID: row.ID, TournamentID: row.TournamentID, RosterID: row.RosterID,
			SeriesID: row.SeriesID, ParticipantID: row.ParticipantID,
			State: pause.PresenceState(row.State), PresenceEpoch: row.PresenceEpoch,
			Revision: row.Revision, ConnectedAt: connectedAt,
			DisconnectedAt: optionalRecoveryTime(row.DisconnectedAt), UpdatedAt: updatedAt,
		}
		if err := result[index].Validate(); err != nil {
			return nil, fmt.Errorf("%w: Presence: %w", errRecoveryTerminalSnapshot, err)
		}
	}
	return result, nil
}

func recoveryIntervals(rows []sqlc.ReconnectInterval) ([]pause.PauseReconnectInterval, error) {
	result := make([]pause.PauseReconnectInterval, len(rows))
	for index, row := range rows {
		openedAt, err := requiredRecoveryTime(row.OpenedAt)
		if err != nil {
			return nil, fmt.Errorf("%w: reconnect opened_at", errRecoveryTerminalSnapshot)
		}
		deadline, err := requiredRecoveryTime(row.DeadlineAt)
		if err != nil {
			return nil, fmt.Errorf("%w: reconnect deadline_at", errRecoveryTerminalSnapshot)
		}
		updatedAt, err := requiredRecoveryTime(row.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("%w: reconnect updated_at", errRecoveryTerminalSnapshot)
		}
		result[index] = pause.PauseReconnectInterval{
			ID: row.ID, PauseID: row.PauseID, RosterID: row.RosterID, SeriesID: row.SeriesID,
			GameID: row.GameAttemptID, ParticipantID: row.ParticipantID,
			PresenceEpoch: row.PresenceEpoch, Number: int(row.IntervalNumber),
			ContinuationNumber: int(row.ContinuationNumber), ContinuedFromID: optionalRecoveryUUID(row.ContinuedFromID),
			SuspendedByPauseID: optionalRecoveryUUID(row.SuspendedByPauseID), State: pause.ReconnectState(row.State),
			OpenedAt: openedAt, Deadline: deadline, ClosedAt: optionalRecoveryTime(row.ClosedAt),
			Revision: row.Revision, UpdatedAt: updatedAt,
		}
		if err := result[index].Validate(); err != nil {
			return nil, fmt.Errorf("%w: reconnect interval: %w", errRecoveryTerminalSnapshot, err)
		}
	}
	return result, nil
}

func recoveryCounters(rows []sqlc.ReconnectSlotCounter) ([]pause.PauseReconnectCounter, error) {
	result := make([]pause.PauseReconnectCounter, len(rows))
	for index, row := range rows {
		result[index] = pause.PauseReconnectCounter{
			PauseID: row.PauseID, RosterID: row.RosterID, ParticipantID: row.ParticipantID,
			Limit: int(row.SlotLimit), Used: int(row.SlotsUsed), Revision: row.Revision,
		}
		if err := result[index].Validate(); err != nil {
			return nil, fmt.Errorf("%w: reconnect counter: %w", errRecoveryTerminalSnapshot, err)
		}
	}
	return result, nil
}

func recoveryGameClock(row sqlc.PauseClock) (pause.PauseResumeGameClock, error) {
	if row.FrozenRemainingMs < 1 || row.FrozenRemainingMs > math.MaxInt64/int64(time.Millisecond) {
		return pause.PauseResumeGameClock{}, fmt.Errorf("%w: invalid frozen duration", errRecoveryTerminalSnapshot)
	}
	originalDeadline, err := requiredRecoveryTime(row.OriginalDeadline)
	if err != nil {
		return pause.PauseResumeGameClock{}, err
	}
	frozenAt, err := requiredRecoveryTime(row.FrozenAt)
	if err != nil {
		return pause.PauseResumeGameClock{}, err
	}
	clock := pause.PauseResumeGameClock{
		PauseID: row.PauseID, GameID: row.GameAttemptID, OriginalDeadline: originalDeadline,
		FrozenAt: frozenAt, Remaining: time.Duration(row.FrozenRemainingMs) * time.Millisecond,
		ResumedAt: optionalRecoveryTime(row.ResumedAt), ResumedDeadline: optionalRecoveryTime(row.ResumedDeadline),
		Revision: row.Revision,
	}
	if err := clock.Validate(true); err != nil {
		return pause.PauseResumeGameClock{}, fmt.Errorf("%w: Game clock: %w", errRecoveryTerminalSnapshot, err)
	}
	return clock, nil
}

func requiredRecoveryTime(value pgtype.Timestamptz) (time.Time, error) {
	if !value.Valid {
		return time.Time{}, errRecoveryTerminalSnapshot
	}
	result := value.Time.Round(0).UTC()
	if !domain.IsValidServerTime(result) {
		return time.Time{}, errRecoveryTerminalSnapshot
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

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
