package recovery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	authorityrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/execution/authority"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	recoveryrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/recovery"
	terminalrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/recovery/terminal"
	resultrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
	attemptusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/attempt"
	gamerecovery "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/recovery"

	recoveryusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery"
)

// ExecutionRecoveryPostgres owns the durable recovery boundary for game
// attempts. It never infers ownership from process state: every read and
// mutation is fenced by the latest PostgreSQL authority lease.
type ExecutionRecoveryPostgres struct {
	tx              *db.TxManager
	deadlines       DeadlineRearmRepository
	terminal        TerminalSnapshotRepository
	resultFinalizer resultrepo.ProjectionFinalizer
}

// DeadlineRearmRepository is the narrow deadline boundary required by the
// execution recovery workflow. The concrete scheduler-backed repository stays
// behind this consumer-owned port.
type DeadlineRearmRepository interface {
	RearmDeadline(ctx context.Context, pending recoveryusecase.PendingDeadline) (bool, error)
}

// TerminalSnapshotRepository is the narrow terminal evidence boundary needed
// for epoch replay authority and settlement. It intentionally exposes only the
// game-timeout snapshot consumed by this workflow.
type TerminalSnapshotRepository interface {
	LoadGameTimeoutSnapshot(
		ctx context.Context,
		pending recoveryusecase.PendingDeadline,
	) (terminalrepo.GameTimeoutSnapshot, error)
}

var (
	_ gamerecovery.RecoverySource           = (*ExecutionRecoveryPostgres)(nil)
	_ gamerecovery.RecoveryTournamentSource = (*ExecutionRecoveryPostgres)(nil)
	_ gamerecovery.DeadlineRearmer          = (*ExecutionRecoveryPostgres)(nil)
	_ gamerecovery.EpochReplayRepository    = (*ExecutionRecoveryPostgres)(nil)
	_ DeadlineRearmRepository               = (*recoveryrepo.RecoveryPostgres)(nil)
	_ TerminalSnapshotRepository            = (*terminalrepo.RecoveryTerminalPostgres)(nil)
)

func NewExecutionRecoveryPostgres(
	tx *db.TxManager,
	deadlines *recoveryrepo.RecoveryPostgres,
	terminal *terminalrepo.RecoveryTerminalPostgres,
) *ExecutionRecoveryPostgres {
	var deadlineRepository DeadlineRearmRepository
	if deadlines != nil {
		deadlineRepository = deadlines
	}
	var terminalRepository TerminalSnapshotRepository
	if terminal != nil {
		terminalRepository = terminal
	}
	return NewExecutionRecoveryPostgresWithRepositories(tx, deadlineRepository, terminalRepository, nil)
}

func NewExecutionRecoveryPostgresWithDependencies(
	tx *db.TxManager,
	deadlines *recoveryrepo.RecoveryPostgres,
	terminal *terminalrepo.RecoveryTerminalPostgres,
	resultFinalizer resultrepo.ProjectionFinalizer,
) *ExecutionRecoveryPostgres {
	var deadlineRepository DeadlineRearmRepository
	if deadlines != nil {
		deadlineRepository = deadlines
	}
	var terminalRepository TerminalSnapshotRepository
	if terminal != nil {
		terminalRepository = terminal
	}
	return NewExecutionRecoveryPostgresWithRepositories(
		tx, deadlineRepository, terminalRepository, resultFinalizer,
	)
}

// NewExecutionRecoveryPostgresWithRepositories builds the adapter from the
// narrow recovery ports used by its transactional workflow. Existing concrete
// constructors delegate here so bootstrap and external callers keep their API.
func NewExecutionRecoveryPostgresWithRepositories(
	tx *db.TxManager,
	deadlines DeadlineRearmRepository,
	terminal TerminalSnapshotRepository,
	resultFinalizer resultrepo.ProjectionFinalizer,
) *ExecutionRecoveryPostgres {
	return &ExecutionRecoveryPostgres{
		tx: tx, deadlines: deadlines, terminal: terminal, resultFinalizer: resultFinalizer,
	}
}

func (repository *ExecutionRecoveryPostgres) ListRecoveryTournaments(
	ctx context.Context,
) ([]uuid.UUID, error) {
	if !validExecutionRecoveryRepository(ctx, repository) {
		return nil, domain.ErrValidation
	}
	ids, err := repository.tx.Querier(ctx).ListExecutionRecoveryTournaments(ctx)
	if err != nil {
		return nil, fmt.Errorf("ExecutionRecoveryPostgres - list recovery tournaments: %w", err)
	}
	result := make([]uuid.UUID, len(ids))
	seen := make(map[uuid.UUID]struct{}, len(ids))
	for index, id := range ids {
		if id == uuid.Nil {
			return nil, domain.ErrInternal
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, domain.ErrInternal
		}
		seen[id] = struct{}{}
		result[index] = id
	}
	return result, nil
}

func (repository *ExecutionRecoveryPostgres) ListActiveGames(
	ctx context.Context,
	tournamentID uuid.UUID,
	current authoritydomain.Identity,
) ([]gamerecovery.RecoveryCandidate, error) {
	if !validExecutionRecoveryRepository(ctx, repository) || tournamentID == uuid.Nil ||
		current.Validate() != nil || current.TournamentID != tournamentID {
		return nil, domain.ErrValidation
	}
	rows, err := repository.tx.Querier(ctx).ListExecutionRecoveryGames(
		ctx,
		sqlc.ListExecutionRecoveryGamesParams{
			TournamentID:      tournamentID,
			AuthorityHolderID: current.HolderID,
			AuthorityLeaseID:  current.LeaseID,
			AuthorityEpoch:    current.Epoch,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("ExecutionRecoveryPostgres - list active Games: %w", err)
	}
	candidates := make([]gamerecovery.RecoveryCandidate, len(rows))
	seen := make(map[uuid.UUID]struct{}, len(rows))
	for index, row := range rows {
		candidate, mapErr := executionRecoveryCandidateFromRow(row, current)
		if mapErr != nil {
			return nil, fmt.Errorf("ExecutionRecoveryPostgres - map active Game %d: %w", index, mapErr)
		}
		if _, duplicate := seen[candidate.Scope.GameID]; duplicate {
			return nil, domain.ErrInternal
		}
		seen[candidate.Scope.GameID] = struct{}{}
		candidates[index] = candidate
	}
	return candidates, nil
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func (repository *ExecutionRecoveryPostgres) RearmDeadline(
	ctx context.Context,
	arm gamerecovery.DeadlineArm,
) error {
	if !validExecutionRecoveryRepository(ctx, repository) || repository.deadlines == nil ||
		!arm.Scope.IsValid() || arm.AttemptNo < 1 || arm.Authority.Validate() != nil ||
		arm.RosterID == uuid.Nil || !domain.IsValidServerTime(arm.Deadline) {
		return domain.ErrValidation
	}
	return repository.tx.Do(ctx, func(txCtx context.Context) error {
		fence, err := repository.lockEpochReplayFence(txCtx, arm.Scope, arm.RosterID)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrConflict
		}
		if err != nil {
			return err
		}
		if fence.rosterID != arm.RosterID || fence.attemptNo != arm.AttemptNo ||
			fence.current.Stamp() == nil || *fence.current.Stamp() != arm.Authority ||
			fence.bound != arm.Authority ||
			!fence.dueAt.Equal(arm.Deadline) {
			return domain.ErrConflict
		}
		_, err = repository.deadlines.RearmDeadline(txCtx, recoveryusecase.PendingDeadline{
			Kind:             recoveryusecase.DeadlineKindGame,
			ID:               arm.Scope.GameID,
			TournamentID:     arm.Scope.TournamentID,
			RosterID:         fence.rosterID,
			WaveID:           arm.Scope.WaveID,
			SeriesID:         arm.Scope.SeriesID,
			SlotID:           arm.Scope.SlotID,
			GameID:           arm.Scope.GameID,
			ExpectedRevision: fence.attemptRevision,
			DueAt:            arm.Deadline,
		})
		if err != nil {
			return fmt.Errorf("rearm fenced deadline: %w", err)
		}
		return nil
	})
}

func (repository *ExecutionRecoveryPostgres) FindEpochReplay(
	ctx context.Context,
	scope domain.FailedAttemptScope,
) (*gamerecovery.EpochReplayRecord, error) {
	if !validExecutionRecoveryRepository(ctx, repository) || !scope.IsValid() {
		return nil, domain.ErrValidation
	}
	record, err := repository.findEpochReplayByGame(ctx, scope.GameID)
	if err != nil || record == nil {
		return record, err
	}
	if record.Attempt.Scope != scope {
		return nil, domain.ErrInternal
	}
	return record, nil
}

func (repository *ExecutionRecoveryPostgres) LoadEpochReplayAuthority(
	ctx context.Context,
	scope domain.FailedAttemptScope,
	rosterID uuid.UUID,
) (gamerecovery.EpochReplayAuthority, error) {
	if !validExecutionRecoveryRepository(ctx, repository) || !scope.IsValid() || rosterID == uuid.Nil {
		return gamerecovery.EpochReplayAuthority{}, domain.ErrValidation
	}
	var result gamerecovery.EpochReplayAuthority
	err := repository.tx.Do(ctx, func(txCtx context.Context) error {
		fence, err := repository.lockEpochReplayFence(txCtx, scope, rosterID)
		if err != nil {
			return err
		}
		deadline := recoveryusecase.PendingDeadline{
			Kind:             recoveryusecase.DeadlineKindGame,
			ID:               scope.GameID,
			TournamentID:     scope.TournamentID,
			RosterID:         fence.rosterID,
			WaveID:           scope.WaveID,
			SeriesID:         scope.SeriesID,
			SlotID:           scope.SlotID,
			GameID:           scope.GameID,
			ExpectedRevision: fence.attemptRevision,
			DueAt:            fence.dueAt,
		}
		snapshot, snapshotErr := repository.terminal.LoadGameTimeoutSnapshot(txCtx, deadline)
		if snapshotErr != nil {
			return snapshotErr
		}
		if snapshot.Authority.GameTimeout == nil {
			return domain.ErrInternal
		}
		result = gamerecovery.EpochReplayAuthority{
			Lease:          fence.current,
			BoundAuthority: fence.bound,
			RosterID:       fence.rosterID,
			Attempt:        *snapshot.Authority.GameTimeout,
		}
		return nil
	})
	if err != nil {
		return gamerecovery.EpochReplayAuthority{}, fmt.Errorf("ExecutionRecoveryPostgres - load replay authority: %w", err)
	}
	return result, nil
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func (repository *ExecutionRecoveryPostgres) CommitEpochReplay(
	ctx context.Context,
	condition gamerecovery.EpochReplayCommitCondition,
	record gamerecovery.EpochReplayRecord,
) (*gamerecovery.EpochReplayRecord, bool, error) {
	if !validExecutionRecoveryRepository(ctx, repository) || condition.Validate() != nil ||
		record.Validate() != nil {
		return nil, false, domain.ErrValidation
	}
	var (
		committed *gamerecovery.EpochReplayRecord
		changed   bool
	)
	err := repository.tx.Do(ctx, func(txCtx context.Context) error {
		stored, findErr := repository.findEpochReplayByCommand(txCtx, record.Attempt.CommandID)
		if findErr != nil {
			return findErr
		}
		if stored != nil {
			committed = stored
			return nil
		}
		stored, findErr = repository.findEpochReplayByGame(txCtx, record.Attempt.Scope.GameID)
		if findErr != nil {
			return findErr
		}
		if stored != nil {
			committed = stored
			return nil
		}

		fence, fenceErr := repository.lockEpochReplayFence(txCtx, record.Attempt.Scope, record.RosterID)
		if fenceErr != nil {
			if errors.Is(fenceErr, pgx.ErrNoRows) {
				return domain.ErrConflict
			}
			return fenceErr
		}
		if !fence.matches(condition, record) {
			return domain.ErrConflict
		}
		snapshot, snapshotErr := repository.terminal.LoadGameTimeoutSnapshot(txCtx, recoveryusecase.PendingDeadline{
			Kind:             recoveryusecase.DeadlineKindGame,
			ID:               record.Attempt.Scope.GameID,
			TournamentID:     record.Attempt.Scope.TournamentID,
			RosterID:         fence.rosterID,
			WaveID:           record.Attempt.Scope.WaveID,
			SeriesID:         record.Attempt.Scope.SeriesID,
			SlotID:           record.Attempt.Scope.SlotID,
			GameID:           record.Attempt.Scope.GameID,
			ExpectedRevision: condition.ExpectedAttemptRevision,
			DueAt:            fence.dueAt,
		})
		if snapshotErr != nil {
			return snapshotErr
		}
		if snapshot.Authority.GameTimeout == nil ||
			snapshot.Authority.GameTimeout.Scope != record.Attempt.Scope ||
			snapshot.Game == nil || len(snapshot.Series) != 1 ||
			snapshot.Game.GameAttempt.Revision != condition.ExpectedAttemptRevision ||
			domain.GameState(snapshot.Game.GameAttempt.State) != domain.GameStateActive ||
			snapshot.Authority.GameTimeout.Current != nil {
			return domain.ErrConflict
		}
		input, inputErr := executionEpochReplaySettlementInput(record, snapshot)
		if inputErr != nil {
			return inputErr
		}
		_, settled, settleErr := resultrepo.NewResultPostgresWithFinalizer(repository.tx, repository.resultFinalizer).Settle(txCtx, input)
		if settleErr != nil {
			return settleErr
		}
		if !settled {
			return domain.ErrConflict
		}
		if routeErr := terminalrepo.CreateRecoveryRoute(txCtx, repository.tx.Querier(txCtx), recoveryusecase.PendingDeadline{
			Kind:         recoveryusecase.DeadlineKindGame,
			TournamentID: record.Attempt.Scope.TournamentID,
			RosterID:     fence.rosterID,
			WaveID:       record.Attempt.Scope.WaveID,
			SeriesID:     record.Attempt.Scope.SeriesID,
			SlotID:       record.Attempt.Scope.SlotID,
			GameID:       record.Attempt.Scope.GameID,
		}, record.Attempt.WaveRoute); routeErr != nil {
			return routeErr
		}
		if createErr := repository.createEpochReplay(txCtx, condition, record); createErr != nil {
			return createErr
		}
		clone := record
		committed = &clone
		changed = true
		return nil
	})
	if err != nil {
		return nil, false, fmt.Errorf("ExecutionRecoveryPostgres - commit replay: %w", err)
	}
	if committed == nil || committed.Validate() != nil {
		return nil, false, domain.ErrInternal
	}
	return committed, changed, nil
}

type executionRecoveryFence struct {
	current         authoritydomain.Lease
	bound           authoritydomain.Stamp
	boundRevision   int64
	attemptRevision int64
	attemptNo       int
	rosterID        uuid.UUID
	dueAt           time.Time
}

func (fence executionRecoveryFence) matches(
	condition gamerecovery.EpochReplayCommitCondition,
	record gamerecovery.EpochReplayRecord,
) bool {
	return condition.Validate() == nil && record.Validate() == nil &&
		fence.current.Identity() == condition.CurrentAuthority &&
		fence.rosterID == condition.RosterID && fence.rosterID == record.RosterID &&
		fence.current.Revision == condition.ExpectedLeaseRevision &&
		fence.bound == condition.BrokenAuthority &&
		fence.attemptRevision == condition.ExpectedAttemptRevision
}

func validExecutionRecoveryRepository(
	ctx context.Context,
	repository *ExecutionRecoveryPostgres,
) bool {
	return ctx != nil && repository != nil && repository.tx != nil &&
		repository.deadlines != nil && repository.terminal != nil
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func executionRecoveryCandidateFromRow(
	row sqlc.ListExecutionRecoveryGamesRow,
	current authoritydomain.Identity,
) (gamerecovery.RecoveryCandidate, error) {
	if row.CurrentAuthorityRevision < 1 || !row.DueAt.Valid || row.GameAttemptID == uuid.Nil ||
		row.TournamentID != current.TournamentID || row.RosterID == uuid.Nil || row.WaveID == uuid.Nil ||
		row.SeriesID == uuid.Nil || row.SlotID == uuid.Nil || row.AssignmentID == uuid.Nil ||
		row.AssignmentAttemptID == uuid.Nil || row.SnapshotID == uuid.Nil || row.AttemptNumber < 1 ||
		row.AttemptRevision < 1 {
		return gamerecovery.RecoveryCandidate{}, domain.ErrInternal
	}
	dueAt := row.DueAt.Time.Round(0).UTC()
	if !domain.IsValidServerTime(dueAt) {
		return gamerecovery.RecoveryCandidate{}, domain.ErrInternal
	}
	bound := authoritydomain.Stamp{LeaseID: row.BoundLeaseID, Epoch: row.BoundEpoch}
	if bound.Validate() != nil {
		return gamerecovery.RecoveryCandidate{}, domain.ErrInternal
	}
	candidate := gamerecovery.RecoveryCandidate{
		Scope: domain.FailedAttemptScope{
			TournamentID:        current.TournamentID,
			WaveID:              row.WaveID,
			SeriesID:            row.SeriesID,
			SlotID:              row.SlotID,
			GameID:              row.GameAttemptID,
			AssignmentID:        row.AssignmentID,
			AssignmentAttemptID: row.AssignmentAttemptID,
		},
		RosterID:       row.RosterID,
		AttemptNo:      int(row.AttemptNumber),
		State:          domain.GameState(row.AttemptState),
		SnapshotID:     row.SnapshotID,
		Category:       domain.Category(row.Category),
		BoundAuthority: bound,
		Deadline:       dueAt,
	}
	if candidate.State != domain.GameStateActive && candidate.State != domain.GameStatePaused {
		return gamerecovery.RecoveryCandidate{}, domain.ErrInternal
	}
	if candidate.BoundAuthority != current.Stamp() && candidate.State == domain.GameStateActive {
		candidate.EpochReplay = executionEpochReplayCommand(candidate, current)
	}
	return candidate, nil
}

func executionEpochReplayCommand(
	candidate gamerecovery.RecoveryCandidate,
	current authoritydomain.Identity,
) *gamerecovery.EpochReplayCommand {
	commandID := executionRecoveryID(candidate.Scope.GameID, candidate.BoundAuthority, "command")
	return &gamerecovery.EpochReplayCommand{
		CurrentAuthority: current,
		BrokenAuthority:  candidate.BoundAuthority,
		RosterID:         candidate.RosterID,
		Attempt: attemptusecase.AttemptCommand{
			Scope:        candidate.Scope,
			CommandID:    commandID,
			FailureClass: gamedomain.FailureExecutionEpochBreak,
			Expected: attemptusecase.Expectation{
				AttemptNo:  candidate.AttemptNo,
				State:      domain.GameStateActive,
				SnapshotID: candidate.SnapshotID,
				Category:   candidate.Category,
			},
			Revisions: attemptusecase.AttemptRevisionSet{
				GameResultRevisionID: domain.OfficialResultRevisionID(executionRecoveryID(candidate.Scope.GameID, candidate.BoundAuthority, "game-revision")),
				ScoreRevisionID:      domain.SeriesScoreRevisionID(executionRecoveryID(candidate.Scope.GameID, candidate.BoundAuthority, "score-revision")),
				RouteEvidenceID:      executionRecoveryID(candidate.Scope.GameID, candidate.BoundAuthority, "route"),
				AuditEventID:         executionRecoveryID(candidate.Scope.GameID, candidate.BoundAuthority, "audit"),
				OutboxEventID:        executionRecoveryID(candidate.Scope.GameID, candidate.BoundAuthority, "outbox"),
				ProjectionRevisionID: executionRecoveryID(candidate.Scope.GameID, candidate.BoundAuthority, "projection"),
			},
		},
	}
}

func executionRecoveryID(
	gameID uuid.UUID,
	stamp authoritydomain.Stamp,
	role string,
) uuid.UUID {
	name := "execution-recovery/" + gameID.String() + "/" + stamp.LeaseID.String() + "/" +
		strconv.FormatInt(stamp.Epoch, 10) + "/" + role
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(name))
}

func executionEpochReplaySettlementInput(
	record gamerecovery.EpochReplayRecord,
	snapshot terminalrepo.GameTimeoutSnapshot,
) (resultrepo.ResultSettlementInput, error) {
	if record.Validate() != nil || snapshot.Game == nil || len(snapshot.Series) != 1 {
		return resultrepo.ResultSettlementInput{}, domain.ErrValidation
	}
	digest, err := terminalrepo.RecoveryPayloadDigest("execution_epoch_replay", struct {
		CommandID uuid.UUID `json:"command_id"`
		GameID    uuid.UUID `json:"game_id"`
		Reason    string    `json:"reason"`
	}{
		CommandID: record.Attempt.CommandID,
		GameID:    record.Attempt.Scope.GameID,
		Reason:    string(record.Attempt.Game.ResultReason),
	})
	if err != nil {
		return resultrepo.ResultSettlementInput{}, err
	}
	return resultrepo.ResultSettlementInput{
		IDs: resultrepo.ResultSettlementIDs{
			CommitID:                  executionRecoveryID(record.Attempt.Scope.GameID, record.BrokenAuthority, "result-commit"),
			ResultEventID:             executionRecoveryID(record.Attempt.Scope.GameID, record.BrokenAuthority, "result-event"),
			ResultEventIdempotencyKey: executionRecoveryID(record.Attempt.Scope.GameID, record.BrokenAuthority, "result-event-key"),
			GameResultRevisionID:      record.Attempt.AttemptGameResultRevision.ID.UUID(),
			SeriesScoreRevisionID:     record.Attempt.ScoreRevision.ID.UUID(),
			AuditEventID:              record.Attempt.Evidence.AuditEventID,
			OutboxEventID:             record.Attempt.Evidence.OutboxEventID,
			OutboxIdempotencyKey:      executionRecoveryID(record.Attempt.Scope.GameID, record.BrokenAuthority, "outbox-key"),
			ProjectionEvidenceID:      record.Attempt.Evidence.ProjectionRevisionID,
			CommitIdempotencyKey:      record.Attempt.CommandID,
		},
		Scope: resultrepo.ResultScope{
			TournamentID: record.Attempt.Scope.TournamentID,
			RosterID:     snapshot.Game.GameAttempt.RosterID,
			SeriesID:     record.Attempt.Scope.SeriesID,
			AttemptID:    record.Attempt.Scope.GameID,
		},
		GameState:               record.Attempt.Game.State,
		GameReason:              record.Attempt.Game.ResultReason,
		GameWinnerID:            record.Attempt.Game.WinnerID,
		Score:                   record.Attempt.ScoreRevision.ScoreAfter,
		NextSeriesState:         record.Attempt.Series.Series.State,
		ActorKind:               resultActorServer,
		ProjectionArtifactKinds: []domain.ArtifactKind{domain.ArtifactKindGameResult, domain.ArtifactKindSeriesScore},
		ProjectionPayloadDigest: digest,
		SettledAt:               record.Attempt.TerminalizedAt,
		ExpectedAttemptRevision: snapshot.Game.GameAttempt.Revision,
		ExpectedAttemptState:    domain.GameState(snapshot.Game.GameAttempt.State),
		ExpectedSeriesRevision:  snapshot.Series[0].Row.Revision,
		ExpectedSeriesState:     domain.SeriesState(snapshot.Series[0].Row.State),
	}, nil
}

func (repository *ExecutionRecoveryPostgres) findEpochReplayByGame(
	ctx context.Context,
	gameID uuid.UUID,
) (*gamerecovery.EpochReplayRecord, error) {
	row, err := repository.tx.Querier(ctx).FindExecutionEpochReplayByGame(ctx, gameID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find replay by Game: %w", err)
	}
	return executionEpochReplayRecordFromRow(row)
}

func (repository *ExecutionRecoveryPostgres) findEpochReplayByCommand(
	ctx context.Context,
	commandID uuid.UUID,
) (*gamerecovery.EpochReplayRecord, error) {
	row, err := repository.tx.Querier(ctx).FindExecutionEpochReplayByCommand(ctx, commandID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find replay by command: %w", err)
	}
	return executionEpochReplayRecordFromRow(row)
}

func (repository *ExecutionRecoveryPostgres) lockEpochReplayFence(
	ctx context.Context,
	scope domain.FailedAttemptScope,
	rosterID uuid.UUID,
) (executionRecoveryFence, error) {
	// Retain the result-writer scope prefix before the lease and Game fence.
	// Load, commit and deadline rearm all call this inside their existing tx.
	if err := lockTournamentResultScope(ctx, repository.tx.Querier(ctx), scope.TournamentID, rosterID); err != nil {
		return executionRecoveryFence{}, err
	}
	row, err := repository.tx.Querier(ctx).LockExecutionEpochReplayFence(
		ctx,
		sqlc.LockExecutionEpochReplayFenceParams{
			GameAttemptID:       scope.GameID,
			TournamentID:        scope.TournamentID,
			RosterID:            rosterID,
			WaveID:              scope.WaveID,
			SeriesID:            scope.SeriesID,
			SlotID:              scope.SlotID,
			AssignmentID:        scope.AssignmentID,
			AssignmentAttemptID: scope.AssignmentAttemptID,
		},
	)
	if err != nil {
		return executionRecoveryFence{}, err
	}
	return executionRecoveryFenceFromRow(row, scope, rosterID)
}

func (repository *ExecutionRecoveryPostgres) createEpochReplay(
	ctx context.Context,
	condition gamerecovery.EpochReplayCommitCondition,
	record gamerecovery.EpochReplayRecord,
) error {
	//nolint:musttag // This versioned application-owned document is validated on both encode and decode.
	document, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal immutable replay record: %w", err)
	}
	if len(document) == 0 {
		return domain.ErrInternal
	}
	_, err = repository.tx.Querier(ctx).CreateExecutionEpochReplay(ctx, sqlc.CreateExecutionEpochReplayParams{
		CommandID:               record.Attempt.CommandID,
		GameAttemptID:           record.Attempt.Scope.GameID,
		TournamentID:            record.Attempt.Scope.TournamentID,
		RosterID:                condition.RosterID,
		WaveID:                  record.Attempt.Scope.WaveID,
		SeriesID:                record.Attempt.Scope.SeriesID,
		SlotID:                  record.Attempt.Scope.SlotID,
		AssignmentID:            record.Attempt.Scope.AssignmentID,
		AssignmentAttemptID:     record.Attempt.Scope.AssignmentAttemptID,
		CurrentHolderID:         condition.CurrentAuthority.HolderID,
		CurrentLeaseID:          condition.CurrentAuthority.LeaseID,
		CurrentEpoch:            condition.CurrentAuthority.Epoch,
		ExpectedLeaseRevision:   condition.ExpectedLeaseRevision,
		BrokenLeaseID:           condition.BrokenAuthority.LeaseID,
		BrokenEpoch:             condition.BrokenAuthority.Epoch,
		ExpectedAttemptRevision: condition.ExpectedAttemptRevision,
		CommandDigest:           append([]byte(nil), record.CommandDigest[:]...),
		RecordDocument:          document,
		ReplayedAt:              resultrepo.TSTZ(record.Attempt.TerminalizedAt),
	})
	if err != nil {
		return resultrepo.MapRepositoryWriteError("ExecutionRecoveryPostgres - create immutable replay", err)
	}
	return nil
}

type executionEpochReplayStoredRow struct {
	commandID               uuid.UUID
	gameAttemptID           uuid.UUID
	tournamentID            uuid.UUID
	rosterID                uuid.UUID
	waveID                  uuid.UUID
	seriesID                uuid.UUID
	slotID                  uuid.UUID
	assignmentID            uuid.UUID
	assignmentAttemptID     uuid.UUID
	currentHolderID         uuid.UUID
	currentLeaseID          uuid.UUID
	currentEpoch            int64
	expectedLeaseRevision   int64
	brokenLeaseID           uuid.UUID
	brokenEpoch             int64
	expectedAttemptRevision int64
	commandDigest           []byte
	recordDocument          []byte
	replayedAt              pgtype.Timestamptz
	createdAt               pgtype.Timestamptz
}

func executionEpochReplayRecordFromRow(
	row sqlc.ExecutionEpochReplay,
) (*gamerecovery.EpochReplayRecord, error) {
	return mapExecutionEpochReplayRecord(executionEpochReplayStoredRow{
		commandID: row.CommandID, gameAttemptID: row.GameAttemptID,
		tournamentID: row.TournamentID, rosterID: row.RosterID, waveID: row.WaveID,
		seriesID: row.SeriesID, slotID: row.SlotID, assignmentID: row.AssignmentID,
		assignmentAttemptID: row.AssignmentAttemptID, currentHolderID: row.CurrentHolderID,
		currentLeaseID: row.CurrentLeaseID, currentEpoch: row.CurrentEpoch,
		expectedLeaseRevision: row.ExpectedLeaseRevision, brokenLeaseID: row.BrokenLeaseID,
		brokenEpoch: row.BrokenEpoch, expectedAttemptRevision: row.ExpectedAttemptRevision,
		commandDigest: row.CommandDigest, recordDocument: row.RecordDocument,
		replayedAt: row.ReplayedAt, createdAt: row.CreatedAt,
	})
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func mapExecutionEpochReplayRecord(
	row executionEpochReplayStoredRow,
) (*gamerecovery.EpochReplayRecord, error) {
	if row.commandID == uuid.Nil || row.gameAttemptID == uuid.Nil || row.tournamentID == uuid.Nil ||
		row.rosterID == uuid.Nil || row.waveID == uuid.Nil || row.seriesID == uuid.Nil ||
		row.slotID == uuid.Nil || row.assignmentID == uuid.Nil || row.assignmentAttemptID == uuid.Nil ||
		row.currentHolderID == uuid.Nil || row.currentLeaseID == uuid.Nil || row.currentEpoch < 1 ||
		row.expectedLeaseRevision < 1 || row.brokenLeaseID == uuid.Nil || row.brokenEpoch < 1 ||
		row.expectedAttemptRevision < 1 || len(row.commandDigest) != sha256.Size ||
		!row.replayedAt.Valid || !row.createdAt.Valid || row.createdAt.Time.Before(row.replayedAt.Time) ||
		len(row.recordDocument) == 0 {
		return nil, domain.ErrInternal
	}
	var record gamerecovery.EpochReplayRecord
	//nolint:musttag // This versioned application-owned document is validated on both encode and decode.
	if err := json.Unmarshal(row.recordDocument, &record); err != nil {
		return nil, fmt.Errorf("decode immutable replay record: %w", err)
	}
	if record.Validate() != nil || record.Attempt.CommandID != row.commandID ||
		record.Attempt.Scope.TournamentID != row.tournamentID || record.RosterID != row.rosterID ||
		record.Attempt.Scope.WaveID != row.waveID || record.Attempt.Scope.SeriesID != row.seriesID ||
		record.Attempt.Scope.SlotID != row.slotID || record.Attempt.Scope.GameID != row.gameAttemptID ||
		record.Attempt.Scope.AssignmentID != row.assignmentID ||
		record.Attempt.Scope.AssignmentAttemptID != row.assignmentAttemptID ||
		record.CurrentAuthority.HolderID != row.currentHolderID ||
		record.CurrentAuthority.LeaseID != row.currentLeaseID ||
		record.CurrentAuthority.Epoch != row.currentEpoch ||
		record.ExpectedLeaseRevision != row.expectedLeaseRevision ||
		record.BrokenAuthority.LeaseID != row.brokenLeaseID ||
		record.BrokenAuthority.Epoch != row.brokenEpoch ||
		record.Attempt.ExpectedAuthorityRevision != row.expectedAttemptRevision ||
		!bytes.Equal(record.CommandDigest[:], row.commandDigest) ||
		!record.Attempt.TerminalizedAt.Equal(row.replayedAt.Time.UTC()) {
		return nil, domain.ErrInternal
	}
	return &record, nil
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func executionRecoveryFenceFromRow(
	row sqlc.LockExecutionEpochReplayFenceRow,
	scope domain.FailedAttemptScope,
	rosterID uuid.UUID,
) (executionRecoveryFence, error) {
	if row.GameAttemptID != scope.GameID || row.TournamentID != scope.TournamentID ||
		row.RosterID != rosterID || row.WaveID != scope.WaveID || row.SeriesID != scope.SeriesID ||
		row.SlotID != scope.SlotID || row.AssignmentID != scope.AssignmentID ||
		row.AssignmentAttemptID != scope.AssignmentAttemptID || row.AttemptRevision < 1 ||
		row.AttemptNumber < 1 || row.AuthorityRevision < 1 || !row.DueAt.Valid {
		return executionRecoveryFence{}, domain.ErrInternal
	}
	current, err := authorityrepo.MapExecutionAuthorityLease(sqlc.ExecutionAuthorityLease{
		TournamentID:     scope.TournamentID,
		CommandID:        row.CurrentCommandID,
		HolderID:         row.CurrentHolderID,
		LeaseID:          row.CurrentLeaseID,
		Epoch:            row.CurrentEpoch,
		ProcessKind:      row.CurrentProcessKind,
		Revision:         row.CurrentRevision,
		PreviousRevision: row.CurrentPreviousRevision,
		PreviousLeaseID:  row.CurrentPreviousLeaseID,
		PreviousEpoch:    row.CurrentPreviousEpoch,
		AcquiredAt:       row.CurrentAcquiredAt,
		RenewedAt:        row.CurrentRenewedAt,
		ExpiresAt:        row.CurrentExpiresAt,
		CreatedAt:        row.CurrentCreatedAt,
	})
	if err != nil {
		return executionRecoveryFence{}, err
	}
	bound := authoritydomain.Stamp{LeaseID: row.AuthorityLeaseID, Epoch: row.AuthorityEpoch}
	dueAt := row.DueAt.Time.Round(0).UTC()
	if bound.Validate() != nil || !domain.IsValidServerTime(dueAt) ||
		current.Revision < row.AuthorityRevision {
		return executionRecoveryFence{}, domain.ErrInternal
	}
	return executionRecoveryFence{
		current:         *current,
		bound:           bound,
		boundRevision:   row.AuthorityRevision,
		attemptRevision: row.AttemptRevision,
		attemptNo:       int(row.AttemptNumber),
		rosterID:        row.RosterID,
		dueAt:           dueAt,
	}, nil
}
