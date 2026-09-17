package terminal

import (
	"context"
	"crypto/sha256"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	reconnectusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/reconnect"
	recoveryusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery"
)

// SeriesSnapshot is the immutable Series evidence needed by the execution
// recovery compatibility bridge.
type SeriesSnapshot struct {
	Row       sqlc.Series
	ScoreHead sqlc.SeriesScoreHead
	Graph     []sqlc.ListRecoverySeriesGraphRow
}

// GameTimeoutSnapshot exposes only the game-timeout evidence still consumed by
// the execution recovery workflow in the parent adapter package.
type GameTimeoutSnapshot struct {
	Authority recoveryusecase.DeadlineTerminalAuthority
	Game      *sqlc.LockRecoveryGameTimeoutRow
	Series    []SeriesSnapshot
}

func (repository *RecoveryTerminalPostgres) LoadGameTimeoutSnapshot(
	ctx context.Context,
	deadline recoveryusecase.PendingDeadline,
) (GameTimeoutSnapshot, error) {
	snapshot, err := repository.loadGameTimeout(ctx, deadline)
	if err != nil {
		return GameTimeoutSnapshot{}, err
	}
	series := make([]SeriesSnapshot, len(snapshot.series))
	for index, item := range snapshot.series {
		series[index] = SeriesSnapshot{Row: item.row, ScoreHead: item.scoreHead, Graph: item.graph}
	}
	return GameTimeoutSnapshot{Authority: snapshot.authority, Game: snapshot.game, Series: series}, nil
}

func CreateRecoveryRoute(
	ctx context.Context,
	querier *sqlc.Queries,
	deadline recoveryusecase.PendingDeadline,
	route reconnectusecase.WaveMemberRoute,
) error {
	return createRecoveryRoute(ctx, querier, deadline, route)
}

func RecoveryPayloadDigest(operation string, value any) ([sha256.Size]byte, error) {
	return recoveryPayloadDigest(operation, value)
}

func RecoverySeries(row sqlc.Series, graph []sqlc.ListRecoverySeriesGraphRow) (domain.Series, error) {
	return recoverySeries(row, graph)
}

func RecoveryGame(row sqlc.GameAttempt) (domain.Game, error) {
	return recoveryGame(row)
}

func RecoveryCurrentOrdinal(scoreHead sqlc.SeriesScoreHead) (int, error) {
	return recoveryCurrentOrdinal(scoreHead)
}

func RecoveryResultRevisionIDs(rows []uuid.UUID) ([]domain.OfficialResultRevisionID, error) {
	return recoveryResultRevisionIDs(rows)
}

func RecoveryPresence(rows []sqlc.PresenceState) ([]pause.PausePresence, error) {
	return recoveryPresence(rows)
}

func RecoveryIntervals(rows []sqlc.ReconnectInterval) ([]pause.PauseReconnectInterval, error) {
	return recoveryIntervals(rows)
}

func RecoveryCounters(rows []sqlc.ReconnectSlotCounter) ([]pause.PauseReconnectCounter, error) {
	return recoveryCounters(rows)
}

func RecoveryGameClock(row sqlc.PauseClock) (pause.PauseResumeGameClock, error) {
	return recoveryGameClock(row)
}

func RequiredRecoveryTime(value pgtype.Timestamptz) (time.Time, error) {
	return requiredRecoveryTime(value)
}

func OptionalRecoveryTime(value pgtype.Timestamptz) *time.Time {
	return optionalRecoveryTime(value)
}

func OptionalRecoveryUUID(value uuid.NullUUID) *uuid.UUID {
	return optionalRecoveryUUID(value)
}
