package terminal

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	waverepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/execution/wave"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	resultrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	attemptusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/attempt"
	noshowusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/noshow"
	reconnectusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/reconnect"

	recoveryusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery"
)

type RecoveryTerminalPostgres struct {
	tx                    *db.TxManager
	authority             recoveryusecase.DeadlineAuthoritySource
	clock                 recoveryusecase.Clock
	ensureSwissRoundProof SwissRoundProofEnsurer
	resultFinalizer       resultrepo.ProjectionFinalizer
}

// SwissRoundProofEnsurer persists the pre-start Swiss pairing proof while the
// terminal transition still owns the transaction. The root facade supplies
// the existing admin-execution proof implementation without coupling this
// child package back to the parent adapter.
type SwissRoundProofEnsurer func(
	context.Context,
	*db.TxManager,
	uuid.UUID,
	uuid.UUID,
	time.Time,
	string,
	uuid.UUID,
) error

type recoveryTerminalSnapshot struct {
	authority recoveryusecase.DeadlineTerminalAuthority
	lease     *authority.Lease
	game      *sqlc.LockRecoveryGameTimeoutRow
	ready     *sqlc.LockRecoveryReadyWindowRow
	series    []recoverySeriesSnapshot
	reconnect *sqlc.LockRecoveryReconnectTimeoutRow
}

type recoveryReconnectGraph struct {
	presence  []pause.PausePresence
	intervals []pause.PauseReconnectInterval
	counters  []pause.PauseReconnectCounter
	clock     pause.PauseResumeGameClock
}

var _ recoveryusecase.DeadlineTerminalStore = (*RecoveryTerminalPostgres)(nil)

func NewRecoveryTerminalPostgres(
	tx *db.TxManager,
	authoritySource recoveryusecase.DeadlineAuthoritySource,
	clock recoveryusecase.Clock,
) *RecoveryTerminalPostgres {
	return NewRecoveryTerminalPostgresWithDependencies(tx, authoritySource, clock, nil, nil)
}

func NewRecoveryTerminalPostgresWithDependencies(
	tx *db.TxManager,
	authoritySource recoveryusecase.DeadlineAuthoritySource,
	clock recoveryusecase.Clock,
	ensureSwissRoundProof SwissRoundProofEnsurer,
	resultFinalizer resultrepo.ProjectionFinalizer,
) *RecoveryTerminalPostgres {
	return &RecoveryTerminalPostgres{
		tx: tx, authority: authoritySource, clock: clock,
		ensureSwissRoundProof: ensureSwissRoundProof, resultFinalizer: resultFinalizer,
	}
}

func (repository *RecoveryTerminalPostgres) LoadDeadlineAuthority(
	ctx context.Context,
	deadline recoveryusecase.PendingDeadline,
) (recoveryusecase.DeadlineTerminalAuthority, bool, error) {
	if ctx == nil || repository == nil || repository.tx == nil || repository.clock == nil ||
		deadline.Validate() != nil {
		return recoveryusecase.DeadlineTerminalAuthority{}, false, domain.ErrValidation
	}
	var snapshot recoveryTerminalSnapshot
	pending := false
	err := repository.tx.Do(ctx, func(txCtx context.Context) error {
		querier := repository.tx.Querier(txCtx)
		receipt, err := recoveryDeadlineReceipt(txCtx, querier, deadline)
		if err != nil {
			return err
		}
		if receipt != nil {
			if !recoveryReceiptMatchesDeadline(*receipt, deadline) {
				return domain.ErrConflict
			}
			return nil
		}
		loaded, err := repository.loadTerminalSnapshot(txCtx, deadline)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		snapshot = loaded
		pending = true
		return nil
	})
	if err != nil {
		return recoveryusecase.DeadlineTerminalAuthority{}, false,
			fmt.Errorf("RecoveryTerminalPostgres - LoadDeadlineAuthority: %w", err)
	}
	return snapshot.authority, pending, nil
}

func (repository *RecoveryTerminalPostgres) CommitDeadlinePlan(
	ctx context.Context,
	plan recoveryusecase.DeadlineTerminalPlan,
) (bool, error) {
	if ctx == nil || repository == nil || repository.tx == nil || repository.clock == nil || plan.Validate() != nil {
		return false, domain.ErrValidation
	}
	changed := false
	err := repository.tx.Do(ctx, func(txCtx context.Context) error {
		querier := repository.tx.Querier(txCtx)
		receipt, err := recoveryDeadlineReceipt(txCtx, querier, plan.Deadline)
		if err != nil {
			return err
		}
		if receipt != nil {
			if !recoveryReceiptMatchesPlan(*receipt, plan) {
				return domain.ErrConflict
			}
			return nil
		}
		snapshot, err := repository.loadTerminalSnapshot(txCtx, plan.Deadline)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrConflict
		}
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(snapshot.authority, plan.Authority) {
			return fmt.Errorf("recovery terminal authority changed: %w", domain.ErrConflict)
		}
		if commitErr := repository.commitTerminalSnapshot(txCtx, querier, snapshot, plan); commitErr != nil {
			return commitErr
		}
		changed = true
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("RecoveryTerminalPostgres - CommitDeadlinePlan: %w", err)
	}
	return changed, nil
}

func (repository *RecoveryTerminalPostgres) commitTerminalSnapshot(
	ctx context.Context,
	querier *sqlc.Queries,
	snapshot recoveryTerminalSnapshot,
	plan recoveryusecase.DeadlineTerminalPlan,
) error {
	switch plan.Deadline.Kind {
	case recoveryusecase.DeadlineKindGame:
		return repository.commitGameTimeout(ctx, querier, snapshot, plan)
	case recoveryusecase.DeadlineKindReadyWindow:
		return repository.commitReadyWindow(ctx, querier, snapshot, plan)
	case recoveryusecase.DeadlineKindReconnect:
		return repository.commitReconnectTimeout(ctx, querier, snapshot, plan)
	default:
		return domain.ErrValidation
	}
}

func (repository *RecoveryTerminalPostgres) loadTerminalSnapshot(
	ctx context.Context,
	deadline recoveryusecase.PendingDeadline,
) (recoveryTerminalSnapshot, error) {
	querier := repository.tx.Querier(ctx)
	if err := lockTournamentResultScope(ctx, querier, deadline.TournamentID, deadline.RosterID); err != nil {
		return recoveryTerminalSnapshot{}, err
	}
	tournament, err := querier.GetTournament(ctx, deadline.TournamentID)
	if err != nil {
		return recoveryTerminalSnapshot{}, err
	}
	// The locked terminal parent makes queued child deadlines stale even when
	// cancellation leaves their local execution state unchanged.
	if tournament.State == string(domain.TournamentStateCancelled) || tournament.State == string(domain.TournamentStateCompleted) {
		return recoveryTerminalSnapshot{}, pgx.ErrNoRows
	}
	switch deadline.Kind {
	case recoveryusecase.DeadlineKindGame:
		return repository.loadGameTimeout(ctx, deadline)
	case recoveryusecase.DeadlineKindReadyWindow:
		return repository.loadReadyWindow(ctx, deadline)
	case recoveryusecase.DeadlineKindReconnect:
		return repository.loadReconnectTimeout(ctx, deadline)
	default:
		return recoveryTerminalSnapshot{}, domain.ErrValidation
	}
}

func (repository *RecoveryTerminalPostgres) loadGameTimeout(
	ctx context.Context,
	deadline recoveryusecase.PendingDeadline,
) (recoveryTerminalSnapshot, error) {
	querier := repository.tx.Querier(ctx)
	row, err := querier.LockRecoveryGameTimeout(ctx, sqlc.LockRecoveryGameTimeoutParams{
		WaveID: deadline.WaveID, GameAttemptID: deadline.GameID, RosterID: deadline.RosterID,
		TournamentID: deadline.TournamentID, ExpectedRevision: deadline.ExpectedRevision, DueAt: tstz(deadline.DueAt),
	})
	if err != nil {
		return recoveryTerminalSnapshot{}, err
	}
	seriesSnapshot, series, ordinal, revisionIDs, projectionRevision, err := repository.loadRecoverySeries(
		ctx,
		row.Series,
		row.SeriesScoreHead,
	)
	if err != nil {
		return recoveryTerminalSnapshot{}, err
	}
	wave, err := repository.loadRecoveryWave(ctx, deadline)
	if err != nil {
		return recoveryTerminalSnapshot{}, err
	}
	authorityValue := attemptusecase.AttemptAuthority{
		Scope: domain.FailedAttemptScope{
			TournamentID: deadline.TournamentID, WaveID: deadline.WaveID, SeriesID: deadline.SeriesID,
			SlotID: deadline.SlotID, GameID: deadline.GameID,
			AssignmentID: row.Assignment.ID, AssignmentAttemptID: row.Assignment.AttemptID,
		},
		Revision: row.GameAttempt.Revision, Wave: wave, Series: seriesdomain.Execution{Series: series},
		ActiveSnapshotID: row.Assignment.SnapshotID, CurrentOrdinal: ordinal,
		CurrentProjectionRevision: projectionRevision, CurrentGameResultRevisionIDs: revisionIDs,
	}
	if attemptusecase.ValidateAuthority(authorityValue) != nil {
		return recoveryTerminalSnapshot{}, errRecoveryTerminalSnapshot
	}
	return recoveryTerminalSnapshot{
		authority: recoveryusecase.DeadlineTerminalAuthority{Deadline: deadline, GameTimeout: &authorityValue},
		game:      &row, series: []recoverySeriesSnapshot{seriesSnapshot},
	}, nil
}

func (repository *RecoveryTerminalPostgres) loadReadyWindow(
	ctx context.Context,
	deadline recoveryusecase.PendingDeadline,
) (recoveryTerminalSnapshot, error) {
	querier := repository.tx.Querier(ctx)
	row, err := querier.LockRecoveryReadyWindow(ctx, sqlc.LockRecoveryReadyWindowParams{
		ReadyWindowID: deadline.ID, ReadyWindowRevisionID: deadline.ReadyWindowRevisionID,
		DueAt: tstz(deadline.DueAt), WaveID: deadline.WaveID, TournamentID: deadline.TournamentID,
		RosterID: deadline.RosterID, ExpectedRevision: deadline.ExpectedRevision,
	})
	if err != nil {
		return recoveryTerminalSnapshot{}, err
	}
	wave, err := repository.loadRecoveryWave(ctx, deadline)
	if err != nil {
		return recoveryTerminalSnapshot{}, err
	}
	rows, err := querier.ListRecoveryReadyWindowSeries(ctx, sqlc.ListRecoveryReadyWindowSeriesParams{
		WaveID: deadline.WaveID, ReadyWindowID: nullableUUIDValue(deadline.ID),
	})
	if err != nil {
		return recoveryTerminalSnapshot{}, err
	}
	if len(rows) == 0 {
		return recoveryTerminalSnapshot{}, errRecoveryTerminalSnapshot
	}
	authorities := make([]noshowusecase.NoShowAuthority, len(rows))
	seriesSnapshots := make([]recoverySeriesSnapshot, len(rows))
	for index, seriesRow := range rows {
		seriesSnapshot, series, ordinal, _, _, loadErr := repository.loadRecoverySeries(
			ctx,
			seriesRow.Series,
			seriesRow.SeriesScoreHead,
		)
		if loadErr != nil {
			return recoveryTerminalSnapshot{}, loadErr
		}
		authorities[index] = noshowusecase.NoShowAuthority{
			Scope: domain.NormalNoShowScope{
				TournamentID: deadline.TournamentID, WaveID: deadline.WaveID,
				WindowID: deadline.ID, SeriesID: series.ID,
			},
			Revision: seriesRow.Series.Revision, Wave: wave,
			Series: seriesdomain.Execution{Series: series}, CurrentOrdinal: ordinal,
		}
		seriesSnapshots[index] = seriesSnapshot
	}
	terminalAuthority := recoveryusecase.DeadlineTerminalAuthority{Deadline: deadline, ReadyWindow: authorities}
	if terminalAuthority.Validate() != nil {
		return recoveryTerminalSnapshot{}, errRecoveryTerminalSnapshot
	}
	return recoveryTerminalSnapshot{authority: terminalAuthority, ready: &row, series: seriesSnapshots}, nil
}

func (repository *RecoveryTerminalPostgres) loadReconnectTimeout(
	ctx context.Context,
	deadline recoveryusecase.PendingDeadline,
) (recoveryTerminalSnapshot, error) {
	if repository.authority == nil {
		return recoveryTerminalSnapshot{}, domain.ErrValidation
	}
	querier := repository.tx.Querier(ctx)
	row, err := querier.LockRecoveryReconnectTimeout(ctx, sqlc.LockRecoveryReconnectTimeoutParams{
		WaveID: deadline.WaveID, ReconnectIntervalID: deadline.ID, ParticipantID: deadline.ParticipantID,
		ExpectedRevision: deadline.ExpectedRevision, DueAt: tstz(deadline.DueAt), PauseID: deadline.PauseID,
		TournamentID: deadline.TournamentID, RosterID: deadline.RosterID,
	})
	if err != nil {
		return recoveryTerminalSnapshot{}, err
	}
	// Only a still-current reconnect candidate requires live execution authority.
	// A settled interval must stay a no-op after its former lease expires.
	now := repository.clock.Now().Round(0).UTC()
	lease, err := repository.authority.LoadAuthority(ctx, deadline.TournamentID)
	if err != nil {
		return recoveryTerminalSnapshot{}, fmt.Errorf("load execution authority: %w", err)
	}
	if lease == nil || !lease.Proves(lease.Identity(), now) {
		return recoveryTerminalSnapshot{}, domain.ErrConflict
	}
	seriesSnapshot, series, ordinal, revisionIDs, projectionRevision, err := repository.loadRecoverySeries(
		ctx,
		row.Series,
		row.SeriesScoreHead,
	)
	if err != nil {
		return recoveryTerminalSnapshot{}, err
	}
	game, err := recoveryGame(row.GameAttempt)
	if err != nil {
		return recoveryTerminalSnapshot{}, err
	}
	graph, err := repository.loadRecoveryReconnectGraph(ctx, deadline, row.PauseClock)
	if err != nil {
		return recoveryTerminalSnapshot{}, err
	}
	authorityValue := reconnectusecase.ReconnectAuthority{
		Scope: pause.GraphScope{
			TournamentID: deadline.TournamentID, RosterID: deadline.RosterID,
			WaveID: deadline.WaveID, Authority: lease.Identity(),
		},
		Revision: row.Pause.Revision, PauseID: row.Pause.ID,
		GameRevision: row.GameAttempt.Revision, SeriesRevision: row.Series.Revision,
		CurrentOrdinal: ordinal, CurrentProjectionRevision: projectionRevision,
		CurrentGameResultRevisionIDs: revisionIDs, Game: game, Series: series,
		GameClock: graph.clock, Presence: graph.presence, Reconnect: graph.intervals, Counters: graph.counters,
	}
	if err := repository.restoreReconnectCounters(ctx, &authorityValue); err != nil {
		return recoveryTerminalSnapshot{}, err
	}
	terminalAuthority := recoveryusecase.DeadlineTerminalAuthority{Deadline: deadline, ReconnectTimeout: &authorityValue}
	if terminalAuthority.Validate() != nil {
		return recoveryTerminalSnapshot{}, errRecoveryTerminalSnapshot
	}
	leaseSnapshot := lease.Clone()
	return recoveryTerminalSnapshot{
		authority: terminalAuthority, lease: &leaseSnapshot, reconnect: &row,
		series: []recoverySeriesSnapshot{seriesSnapshot},
	}, nil
}

func (repository *RecoveryTerminalPostgres) loadRecoveryReconnectGraph(
	ctx context.Context,
	deadline recoveryusecase.PendingDeadline,
	clockRow sqlc.PauseClock,
) (recoveryReconnectGraph, error) {
	querier := repository.tx.Querier(ctx)
	presenceRows, err := querier.ListRecoveryPresence(ctx, sqlc.ListRecoveryPresenceParams{
		SeriesID: deadline.SeriesID, RosterID: deadline.RosterID,
	})
	if err != nil {
		return recoveryReconnectGraph{}, err
	}
	presenceValues, err := recoveryPresence(presenceRows)
	if err != nil {
		return recoveryReconnectGraph{}, err
	}
	intervalRows, err := querier.ListRecoveryReconnectIntervals(ctx, sqlc.ListRecoveryReconnectIntervalsParams{
		PauseID: deadline.PauseID, RosterID: deadline.RosterID,
	})
	if err != nil {
		return recoveryReconnectGraph{}, err
	}
	intervals, err := recoveryIntervals(intervalRows)
	if err != nil {
		return recoveryReconnectGraph{}, err
	}
	counterRows, err := querier.ListRecoveryReconnectCounters(ctx, sqlc.ListRecoveryReconnectCountersParams{
		PauseID: deadline.PauseID, RosterID: deadline.RosterID,
	})
	if err != nil {
		return recoveryReconnectGraph{}, err
	}
	counters, err := recoveryCounters(counterRows)
	if err != nil {
		return recoveryReconnectGraph{}, err
	}
	clock, err := recoveryGameClock(clockRow)
	if err != nil {
		return recoveryReconnectGraph{}, err
	}
	return recoveryReconnectGraph{
		presence: presenceValues, intervals: intervals, counters: counters, clock: clock,
	}, nil
}

func (repository *RecoveryTerminalPostgres) loadRecoverySeries(
	ctx context.Context,
	row sqlc.Series,
	scoreHead sqlc.SeriesScoreHead,
) (recoverySeriesSnapshot, domain.Series, int, []domain.OfficialResultRevisionID, int64, error) {
	querier := repository.tx.Querier(ctx)
	if scoreHead.SeriesID != row.ID || scoreHead.RosterID != row.RosterID ||
		!row.CurrentScoreRevisionID.Valid || row.CurrentScoreRevisionID.UUID != scoreHead.CurrentRevisionID {
		return recoverySeriesSnapshot{}, domain.Series{}, 0, nil, 0, errRecoveryTerminalSnapshot
	}
	graph, err := querier.ListRecoverySeriesGraph(ctx, sqlc.ListRecoverySeriesGraphParams{
		SeriesID: row.ID, RosterID: row.RosterID,
	})
	if err != nil {
		return recoverySeriesSnapshot{}, domain.Series{}, 0, nil, 0, err
	}
	series, err := recoverySeries(row, graph)
	if err != nil {
		return recoverySeriesSnapshot{}, domain.Series{}, 0, nil, 0, err
	}
	ordinal, err := recoveryCurrentOrdinal(scoreHead)
	if err != nil {
		return recoverySeriesSnapshot{}, domain.Series{}, 0, nil, 0, err
	}
	resultRows, err := querier.ListRecoveryGameResultRevisionIDs(ctx, sqlc.ListRecoveryGameResultRevisionIDsParams{
		SeriesID: row.ID, RosterID: row.RosterID,
	})
	if err != nil {
		return recoverySeriesSnapshot{}, domain.Series{}, 0, nil, 0, err
	}
	resultIDs, err := recoveryResultRevisionIDs(resultRows)
	if err != nil {
		return recoverySeriesSnapshot{}, domain.Series{}, 0, nil, 0, err
	}
	projection, err := querier.GetCurrentProjectionRevision(ctx, sqlc.GetCurrentProjectionRevisionParams{
		TournamentID: row.TournamentID,
		RosterID:     row.RosterID,
	})
	if err != nil || projection.RevisionNumber < 1 {
		if err == nil {
			err = errRecoveryTerminalSnapshot
		}
		return recoverySeriesSnapshot{}, domain.Series{}, 0, nil, 0, err
	}
	snapshot := recoverySeriesSnapshot{row: row, scoreHead: scoreHead, graph: graph}
	return snapshot, series, ordinal, resultIDs, projection.RevisionNumber, nil
}

func (repository *RecoveryTerminalPostgres) loadRecoveryWave(
	ctx context.Context,
	deadline recoveryusecase.PendingDeadline,
) (domain.Wave, error) {
	record, err := waverepo.NewWavePostgres(repository.tx).Get(ctx, deadline.TournamentID, deadline.WaveID)
	if err != nil {
		return domain.Wave{}, err
	}
	if record.RosterID != deadline.RosterID ||
		(deadline.Kind == recoveryusecase.DeadlineKindReadyWindow && record.Revision != deadline.ExpectedRevision) {
		return domain.Wave{}, errRecoveryTerminalSnapshot
	}
	return record.Wave, nil
}

func recoveryDeadlineReceipt(
	ctx context.Context,
	querier *sqlc.Queries,
	deadline recoveryusecase.PendingDeadline,
) (*sqlc.DeadlineTransitionReceipt, error) {
	receipt, err := querier.GetRecoveryDeadlineReceipt(ctx, sqlc.GetRecoveryDeadlineReceiptParams{
		DeadlineID: deadline.ID, ExpectedDeadlineRevision: deadline.ExpectedRevision,
		DeadlineKind: string(deadline.Kind),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &receipt, nil
}

func recoveryReceiptMatchesDeadline(receipt sqlc.DeadlineTransitionReceipt, deadline recoveryusecase.PendingDeadline) bool {
	if receipt.DeadlineID != deadline.ID || receipt.ExpectedDeadlineRevision != deadline.ExpectedRevision ||
		receipt.TournamentID != deadline.TournamentID || receipt.RosterID != deadline.RosterID ||
		receipt.WaveID != deadline.WaveID || !receipt.ResolvedAt.Valid {
		return false
	}
	switch deadline.Kind {
	case recoveryusecase.DeadlineKindGame:
		return recoveryGameReceiptMatches(receipt, deadline)
	case recoveryusecase.DeadlineKindReadyWindow:
		return recoveryReadyWindowReceiptMatches(receipt, deadline)
	case recoveryusecase.DeadlineKindReconnect:
		return recoveryReconnectReceiptMatches(receipt, deadline)
	default:
		return false
	}
}

func recoveryGameReceiptMatches(receipt sqlc.DeadlineTransitionReceipt, deadline recoveryusecase.PendingDeadline) bool {
	return receipt.TransitionKind == "game_timeout_replay" && receipt.SeriesID.Valid &&
		receipt.SeriesID.UUID == deadline.SeriesID && receipt.GameAttemptID.Valid &&
		receipt.GameAttemptID.UUID == deadline.GameID && !receipt.PauseID.Valid &&
		!receipt.ParticipantID.Valid && !receipt.ReadyWindowID.Valid
}

func recoveryReadyWindowReceiptMatches(
	receipt sqlc.DeadlineTransitionReceipt,
	deadline recoveryusecase.PendingDeadline,
) bool {
	return receipt.TransitionKind == "ready_window_no_show" && receipt.ReadyWindowID.Valid &&
		receipt.ReadyWindowID.UUID == deadline.ID && !receipt.SeriesID.Valid && !receipt.GameAttemptID.Valid &&
		!receipt.PauseID.Valid && !receipt.ParticipantID.Valid
}

func recoveryReconnectReceiptMatches(
	receipt sqlc.DeadlineTransitionReceipt,
	deadline recoveryusecase.PendingDeadline,
) bool {
	validKind := receipt.TransitionKind == "reconnect_interval_expired" ||
		receipt.TransitionKind == "reconnect_forfeit" || receipt.TransitionKind == "reconnect_replay"
	return validKind && receipt.SeriesID.Valid && receipt.SeriesID.UUID == deadline.SeriesID &&
		receipt.GameAttemptID.Valid && receipt.GameAttemptID.UUID == deadline.GameID && receipt.PauseID.Valid &&
		receipt.PauseID.UUID == deadline.PauseID && receipt.ParticipantID.Valid &&
		receipt.ParticipantID.UUID == deadline.ParticipantID && !receipt.ReadyWindowID.Valid
}

func recoveryReceiptMatchesPlan(receipt sqlc.DeadlineTransitionReceipt, plan recoveryusecase.DeadlineTerminalPlan) bool {
	if !recoveryReceiptMatchesDeadline(receipt, plan.Deadline) || receipt.ID != plan.ReceiptID ||
		receipt.CommandID != plan.CommandID {
		return false
	}
	switch plan.Deadline.Kind {
	case recoveryusecase.DeadlineKindGame:
		return plan.ResultEvidence != nil && receipt.ResultCommitID.Valid &&
			receipt.ResultCommitID.UUID == plan.ResultEvidence.CommitID && receipt.RouteEvidenceID.Valid &&
			receipt.RouteEvidenceID.UUID == plan.GameTimeout.WaveRoute.ID
	case recoveryusecase.DeadlineKindReadyWindow:
		return reflect.DeepEqual(receipt.NormalNoShowCommitIds, recoveryNoShowCommitIDs(plan))
	case recoveryusecase.DeadlineKindReconnect:
		if plan.ReconnectTimeout.Evidence == nil {
			return receipt.TransitionKind == "reconnect_interval_expired" && !receipt.ResultCommitID.Valid
		}
		return plan.ResultEvidence != nil && receipt.ResultCommitID.Valid &&
			receipt.ResultCommitID.UUID == plan.ResultEvidence.CommitID
	default:
		return false
	}
}

func recoveryNoShowCommitIDs(plan recoveryusecase.DeadlineTerminalPlan) []uuid.UUID {
	result := make([]uuid.UUID, len(plan.ReadyWindowEvidence))
	for index := range plan.ReadyWindowEvidence {
		result[index] = plan.ReadyWindowEvidence[index].CommitID
	}
	return result
}
