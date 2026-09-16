package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	terminalrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/recovery/terminal"
	resultauthority "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/authority"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	wavestartrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/execution/wavestart"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery"
)

// RecoveryTerminalPostgres keeps the root package contract stable while the
// terminal implementation lives in its recovery child package. The inner
// repository is intentionally opaque; only the narrow game snapshot bridge
// below remains for execution recovery's existing private workflow.
type RecoveryTerminalPostgres struct {
	inner *terminalrepo.RecoveryTerminalPostgres
}

type recoveryTerminalSnapshot struct {
	authority recovery.DeadlineTerminalAuthority
	game      *sqlc.LockRecoveryGameTimeoutRow
	series    []recoverySeriesSnapshot
}

type recoverySeriesSnapshot struct {
	row       sqlc.Series
	scoreHead sqlc.SeriesScoreHead
	graph     []sqlc.ListRecoverySeriesGraphRow
}

var _ recovery.DeadlineTerminalStore = (*RecoveryTerminalPostgres)(nil)

func NewRecoveryTerminalPostgres(
	tx *TxManager,
	authoritySource recovery.DeadlineAuthoritySource,
	clock recovery.Clock,
) *RecoveryTerminalPostgres {
	inner := terminalrepo.NewRecoveryTerminalPostgresWithDependencies(
		tx,
		authoritySource,
		clock,
		wavestartrepo.EnsurePreStartSwissRoundProofForCommand,
		resultauthority.FinalizeProjection,
	)
	return &RecoveryTerminalPostgres{inner: inner}
}

func (repository *RecoveryTerminalPostgres) LoadDeadlineAuthority(
	ctx context.Context,
	deadline recovery.PendingDeadline,
) (recovery.DeadlineTerminalAuthority, bool, error) {
	if repository == nil || repository.inner == nil {
		return recovery.DeadlineTerminalAuthority{}, false, domain.ErrValidation
	}
	return repository.inner.LoadDeadlineAuthority(ctx, deadline)
}

func (repository *RecoveryTerminalPostgres) CommitDeadlinePlan(
	ctx context.Context,
	plan recovery.DeadlineTerminalPlan,
) (bool, error) {
	if repository == nil || repository.inner == nil {
		return false, domain.ErrValidation
	}
	return repository.inner.CommitDeadlinePlan(ctx, plan)
}

func (repository *RecoveryTerminalPostgres) loadGameTimeout(
	ctx context.Context,
	deadline recovery.PendingDeadline,
) (recoveryTerminalSnapshot, error) {
	if repository == nil || repository.inner == nil {
		return recoveryTerminalSnapshot{}, domain.ErrValidation
	}
	snapshot, err := repository.inner.LoadGameTimeoutSnapshot(ctx, deadline)
	if err != nil {
		return recoveryTerminalSnapshot{}, err
	}
	series := make([]recoverySeriesSnapshot, len(snapshot.Series))
	for index, item := range snapshot.Series {
		series[index] = recoverySeriesSnapshot{row: item.Row, scoreHead: item.ScoreHead, graph: item.Graph}
	}
	return recoveryTerminalSnapshot{authority: snapshot.Authority, game: snapshot.Game, series: series}, nil
}

func createRecoveryRoute(
	ctx context.Context,
	querier *sqlc.Queries,
	deadline recovery.PendingDeadline,
	route gameusecase.WaveMemberRoute,
) error {
	return terminalrepo.CreateRecoveryRoute(ctx, querier, deadline, route)
}

func recoveryPayloadDigest(operation string, value any) ([32]byte, error) {
	return terminalrepo.RecoveryPayloadDigest(operation, value)
}

func recoverySeries(row sqlc.Series, graph []sqlc.ListRecoverySeriesGraphRow) (domain.Series, error) {
	return terminalrepo.RecoverySeries(row, graph)
}

func recoveryGame(row sqlc.GameAttempt) (domain.Game, error) {
	return terminalrepo.RecoveryGame(row)
}

func recoveryCurrentOrdinal(scoreHead sqlc.SeriesScoreHead) (int, error) {
	return terminalrepo.RecoveryCurrentOrdinal(scoreHead)
}

func recoveryResultRevisionIDs(rows []uuid.UUID) ([]domain.OfficialResultRevisionID, error) {
	return terminalrepo.RecoveryResultRevisionIDs(rows)
}

func recoveryPresence(rows []sqlc.PresenceState) ([]pause.PausePresence, error) {
	return terminalrepo.RecoveryPresence(rows)
}

func recoveryIntervals(rows []sqlc.ReconnectInterval) ([]pause.PauseReconnectInterval, error) {
	return terminalrepo.RecoveryIntervals(rows)
}

func recoveryCounters(rows []sqlc.ReconnectSlotCounter) ([]pause.PauseReconnectCounter, error) {
	return terminalrepo.RecoveryCounters(rows)
}

func recoveryGameClock(row sqlc.PauseClock) (pause.PauseResumeGameClock, error) {
	return terminalrepo.RecoveryGameClock(row)
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
