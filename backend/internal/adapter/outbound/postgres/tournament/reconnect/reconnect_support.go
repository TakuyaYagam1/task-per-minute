package reconnect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	return terminalrepo.RecoveryGameClock(row)
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
