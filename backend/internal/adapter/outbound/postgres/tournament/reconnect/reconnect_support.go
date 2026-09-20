package reconnect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	terminalrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/recovery/terminal"
	resultrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
)

const resultActorServer = "server"

type ResultScope = resultrepo.ResultScope
type ResultSettlementIDs = resultrepo.ResultSettlementIDs
type ResultSettlementInput = resultrepo.ResultSettlementInput

func validReconnectRepository(ctx context.Context, repository *TournamentReconnectPostgres) bool {
	return ctx != nil && repository != nil && repository.tx != nil && repository.tx.HasPool()
}

func nullableUUID(value *uuid.UUID) uuid.NullUUID {
	if value == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *value, Valid: true}
}

func nullableUUIDValue(value uuid.UUID) uuid.NullUUID {
	return uuid.NullUUID{UUID: value, Valid: value != uuid.Nil}
}

func tstz(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}

func requiredRecoveryTime(value pgtype.Timestamptz) (time.Time, error) {
	return terminalrepo.RequiredRecoveryTime(value)
}

func optionalRecoveryTime(value pgtype.Timestamptz) *time.Time {
	return terminalrepo.OptionalRecoveryTime(value)
}

func optionalRecoveryUUID(value uuid.NullUUID) *uuid.UUID {
	return terminalrepo.OptionalRecoveryUUID(value)
}

func recoveryPayloadDigest(operation string, value any) ([32]byte, error) {
	return terminalrepo.RecoveryPayloadDigest(operation, value)
}

func recoverySeries(row sqlc.Series, graph []sqlc.ListRecoverySeriesGraphRow) (domain.Series, error) {
	return terminalrepo.RecoverySeries(row, graph)
}

func recoveryResultRevisionIDs(rows []uuid.UUID) ([]domain.OfficialResultRevisionID, error) {
	return terminalrepo.RecoveryResultRevisionIDs(rows)
}

func recoveryCurrentOrdinal(scoreHead sqlc.SeriesScoreHead) (int, error) {
	return terminalrepo.RecoveryCurrentOrdinal(scoreHead)
}

func recoveryGameClock(row sqlc.PauseClock) (pausedomain.PauseResumeGameClock, error) {
	if row.FrozenRemainingMs < 1 || row.FrozenRemainingMs > math.MaxInt64/int64(time.Millisecond) {
		return pausedomain.PauseResumeGameClock{}, fmt.Errorf("invalid recovery terminal snapshot: invalid frozen duration")
	}
	originalDeadline, err := requiredRecoveryTime(row.OriginalDeadline)
	if err != nil {
		return pausedomain.PauseResumeGameClock{}, err
	}
	frozenAt, err := requiredRecoveryTime(row.FrozenAt)
	if err != nil {
		return pausedomain.PauseResumeGameClock{}, err
	}
	remaining, err := reconnectFrozenDuration(originalDeadline, frozenAt, row.FrozenRemainingMs)
	if err != nil {
		return pausedomain.PauseResumeGameClock{}, fmt.Errorf("invalid recovery terminal snapshot: Game clock: %w", err)
	}
	clock := pausedomain.PauseResumeGameClock{
		PauseID: row.PauseID, GameID: row.GameAttemptID, OriginalDeadline: originalDeadline,
		FrozenAt: frozenAt, Remaining: remaining,
		ResumedAt: optionalRecoveryTime(row.ResumedAt), ResumedDeadline: optionalRecoveryTime(row.ResumedDeadline),
		Revision: row.Revision,
	}
	if err := clock.Validate(true); err != nil {
		return pausedomain.PauseResumeGameClock{}, fmt.Errorf("invalid recovery terminal snapshot: Game clock: %w", err)
	}
	return clock, nil
}

// reconnectFrozenDuration restores the duration that was authoritative before
// persistence quantized it to milliseconds. The durable endpoints retain the
// exact PostgreSQL timestamp value; the stored millisecond value is accepted
// only when it is the truncation of that duration within one millisecond.
func reconnectFrozenDuration(originalDeadline, frozenAt time.Time, frozenRemainingMs int64) (time.Duration, error) {
	persisted, err := reconnectPersistedDuration(frozenRemainingMs)
	if err != nil {
		return 0, err
	}
	exact := originalDeadline.Sub(frozenAt)
	if exact <= 0 || exact < persisted || exact-persisted >= time.Millisecond {
		return 0, fmt.Errorf("%w: frozen duration differs from persisted milliseconds", pausedomain.ErrInvalidGameClock)
	}
	return exact, nil
}

func reconnectPersistedDuration(frozenRemainingMs int64) (time.Duration, error) {
	if frozenRemainingMs < 1 || frozenRemainingMs > math.MaxInt64/int64(time.Millisecond) {
		return 0, pausedomain.ErrInvalidGameClock
	}
	return time.Duration(frozenRemainingMs) * time.Millisecond, nil
}

// reconnectCanonicalResumedDeadline returns the timestamp accepted by the
// pause_clocks resume check. The domain record may retain the exact duration,
// but the durable resumed endpoint must use the persisted millisecond value.
func reconnectCanonicalResumedDeadline(resumedAt, exactDeadline time.Time, frozenRemainingMs int64) (time.Time, error) {
	persisted, err := reconnectPersistedDuration(frozenRemainingMs)
	if err != nil {
		return time.Time{}, err
	}
	exact := exactDeadline.Sub(resumedAt)
	if exact <= 0 || exact < persisted || exact-persisted >= time.Millisecond {
		return time.Time{}, fmt.Errorf("%w: resumed duration differs from persisted milliseconds", pausedomain.ErrInvalidGameClock)
	}
	canonical, ok := pausedomain.AddTime(resumedAt, persisted)
	if !ok {
		return time.Time{}, pausedomain.ErrInvalidGameClock
	}
	return canonical, nil
}

func recoveryPresence(rows []sqlc.PresenceState) ([]pausedomain.PausePresence, error) {
	return terminalrepo.RecoveryPresence(rows)
}

func recoveryIntervals(rows []sqlc.ReconnectInterval) ([]pausedomain.PauseReconnectInterval, error) {
	return terminalrepo.RecoveryIntervals(rows)
}

func recoveryCounters(rows []sqlc.ReconnectSlotCounter) ([]pausedomain.PauseReconnectCounter, error) {
	return terminalrepo.RecoveryCounters(rows)
}

func lockTournamentResultScope(ctx context.Context, q *sqlc.Queries, tournamentID, rosterID uuid.UUID) error {
	_, err := q.LockTournamentResultScope(ctx, sqlc.LockTournamentResultScopeParams{
		TournamentID: tournamentID,
		RosterID:     uuid.NullUUID{UUID: rosterID, Valid: rosterID != uuid.Nil},
	})
	return err
}

func resultCASWriteError(operation string, err error) error {
	return resultrepo.ResultCASWriteError(operation, err)
}

func mapRepositoryWriteError(operation string, err error) error {
	return resultrepo.MapRepositoryWriteError(operation, err)
}

func marshalJSON(operation string, value any) ([]byte, error) {
	reflected := reflect.ValueOf(value)
	if reflected.IsValid() && reflected.Kind() == reflect.Slice && reflected.IsNil() {
		return []byte("[]"), nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%s - marshal JSON: %w", operation, err)
	}
	return data, nil
}

func normalPauseCAS(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) || err == nil {
		return fmt.Errorf("%s: %w", operation, domain.ErrConflict)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func tournamentAdminNormalPauseID(namespace uuid.UUID, kind string, id uuid.UUID) uuid.UUID {
	return uuid.NewSHA1(namespace, []byte(kind+":"+id.String()))
}
