package reconnect

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	resultrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	pauseusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause"
	reconnectusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/reconnect"
)

// TournamentReconnectPostgres owns the transactional reconnect persistence
// boundary. Result projection publication is supplied by the composition
// root so this package does not depend on the root postgres facade.
type TournamentReconnectPostgres struct {
	tx              *db.TxManager
	resultFinalizer resultrepo.ProjectionFinalizer
}

func NewTournamentReconnectPostgres(tx *db.TxManager) *TournamentReconnectPostgres {
	return &TournamentReconnectPostgres{tx: tx}
}

func NewTournamentReconnectPostgresWithDependencies(
	tx *db.TxManager,
	resultFinalizer resultrepo.ProjectionFinalizer,
) *TournamentReconnectPostgres {
	return &TournamentReconnectPostgres{tx: tx, resultFinalizer: resultFinalizer}
}

const (
	reconnectReceiptSchemaVersion = 1
	reconnectOutboxSchema         = "game-reconnect-changed-v1"
	reconnectOutboxTopic          = "game.reconnect.changed"
)

type reconnectOutboxPayload struct {
	Schema       string    `json:"schema"`
	GameID       uuid.UUID `json:"game_id"`
	GameRevision int64     `json:"game_revision"`
	MutationKind string    `json:"mutation_kind"`
}

// tournamentReconnectReceiptDocument is deliberately an application-owned
// envelope.  The database columns provide queryable idempotency metadata while
// the nested record retains the exact immutable domain result returned to a
// retrying caller.
type tournamentReconnectReceiptDocument struct {
	SchemaVersion             int16                            `json:"schema_version"`
	CommandID                 uuid.UUID                        `json:"command_id"`
	TournamentID              uuid.UUID                        `json:"tournament_id"`
	RosterID                  uuid.UUID                        `json:"roster_id"`
	WaveID                    uuid.UUID                        `json:"wave_id"`
	MutationKind              reconnectusecase.MutationKind    `json:"mutation_kind"`
	ParticipantID             uuid.UUID                        `json:"participant_id"`
	IntervalID                *uuid.UUID                       `json:"interval_id,omitempty"`
	ExpectedAuthorityRevision int64                            `json:"expected_authority_revision"`
	ResultAuthorityRevision   int64                            `json:"result_authority_revision"`
	RecordedAt                time.Time                        `json:"recorded_at"`
	Record                    reconnectusecase.ReconnectRecord `json:"record"`
}

type tournamentReconnectReceiptMeta struct {
	CommandID                 uuid.UUID
	TournamentID              uuid.UUID
	RosterID                  uuid.UUID
	WaveID                    uuid.UUID
	MutationKind              reconnectusecase.MutationKind
	ParticipantID             uuid.UUID
	IntervalID                *uuid.UUID
	ExpectedAuthorityRevision int64
	ResultAuthorityRevision   int64
	SchemaVersion             int16
	RecordedAt                time.Time
}

var _ reconnectusecase.ReconnectRepository = (*TournamentReconnectPostgres)(nil)

// FindCommand loads only the immutable domain receipt.  Socket identity and
// participant connection leases intentionally do not cross this boundary.
func (r *TournamentReconnectPostgres) FindCommand(
	ctx context.Context,
	tournamentID uuid.UUID,
	commandID uuid.UUID,
) (*reconnectusecase.ReconnectRecord, error) {
	if !validReconnectRepository(ctx, r) || tournamentID == uuid.Nil || commandID == uuid.Nil {
		return nil, domain.ErrValidation
	}
	row, err := r.tx.Querier(ctx).GetTournamentReconnectCommandReceipt(ctx, sqlc.GetTournamentReconnectCommandReceiptParams{
		TournamentID: tournamentID,
		CommandID:    commandID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("TournamentAdminExecutionPostgres - FindCommand: %w", err)
	}
	record, meta, err := decodeTournamentReconnectReceipt(row.RecordDocument)
	if err != nil {
		return nil, fmt.Errorf("TournamentAdminExecutionPostgres - FindCommand - decode: %w", err)
	}
	rowMeta, err := tournamentReconnectReceiptRowMeta(row)
	if err != nil || !sameTournamentReconnectReceiptMeta(meta, rowMeta) ||
		row.CommandID != commandID || row.TournamentID != tournamentID {
		return nil, domain.ErrInternal
	}
	if err := validateTournamentReconnectRecord(*record, meta); err != nil {
		return nil, err
	}
	return record, nil
}

// LoadAuthority locks the result scope and the complete current game graph in
// the same order used by ResultPostgres.  A GraphScope identifies a Wave, so
// participantID selects the one Series/game that owns this command when a Wave
// contains several executable games.
func (r *TournamentReconnectPostgres) LoadAuthority(
	ctx context.Context,
	scope pausedomain.GraphScope,
	participantID uuid.UUID,
) (reconnectusecase.ReconnectAuthority, error) {
	if !validReconnectRepository(ctx, r) || scope.Validate() != nil || participantID == uuid.Nil {
		return reconnectusecase.ReconnectAuthority{}, domain.ErrValidation
	}
	var authority reconnectusecase.ReconnectAuthority
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		loaded, err := r.loadTournamentReconnectAuthority(txCtx, scope, participantID)
		if err == nil {
			authority = loaded
		}
		return err
	})
	if err != nil {
		return reconnectusecase.ReconnectAuthority{}, fmt.Errorf("TournamentAdminExecutionPostgres - LoadAuthority: %w", err)
	}
	return authority, nil
}

// CommitMutation is the single write boundary for game reconnect mutations.
// All durable graph changes and the command receipt execute under one
// TxManager transaction.  A primary-key race on command_id is surfaced as a
// conflict so the use case can retry against the winning receipt.
//
//nolint:gocyclo // One transaction owns receipt replay, authority CAS, persistence, and immutable evidence.
func (r *TournamentReconnectPostgres) CommitMutation(
	ctx context.Context,
	expectedRevision int64,
	record reconnectusecase.ReconnectRecord,
) (*reconnectusecase.ReconnectRecord, bool, error) {
	if !validReconnectRepository(ctx, r) || expectedRevision < 1 ||
		record.ExpectedAuthorityRevision != expectedRevision {
		return nil, false, domain.ErrValidation
	}
	if err := validateTournamentReconnectRecordShape(record); err != nil {
		return nil, false, err
	}
	_, _, participantID, _ := reconnectRecordCommand(record)
	if participantID == uuid.Nil {
		return nil, false, domain.ErrValidation
	}
	var committed *reconnectusecase.ReconnectRecord
	changed := false
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		q := r.tx.Querier(txCtx)
		existing, err := r.findTournamentReconnectReceipt(txCtx, record.ReconnectAuthority.Scope.TournamentID, commandIDOfReconnectRecord(record))
		if err != nil {
			return err
		}
		if existing != nil {
			if !reconnectRecordsEqual(*existing, record) {
				return reconnectusecase.ErrCommandReuse
			}
			committed = existing
			return nil
		}

		current, err := r.loadTournamentReconnectAuthority(txCtx, record.ReconnectAuthority.Scope, participantID)
		if err != nil {
			return err
		}
		switch {
		case current.Current != nil:
			return fmt.Errorf("reconnect authority conflict: current graph is terminal: %w", domain.ErrConflict)
		case current.Revision != expectedRevision:
			return fmt.Errorf("reconnect authority conflict: revision changed from %d to %d: %w", expectedRevision, current.Revision, domain.ErrConflict)
		case current.Scope != record.ReconnectAuthority.Scope:
			return fmt.Errorf("reconnect authority conflict: scope changed: %w", domain.ErrConflict)
		case current.Game.ID != record.ReconnectAuthority.Game.ID:
			return fmt.Errorf("reconnect authority conflict: game changed from %s to %s: %w", record.ReconnectAuthority.Game.ID, current.Game.ID, domain.ErrConflict)
		case current.Series.ID != record.ReconnectAuthority.Series.ID:
			return fmt.Errorf("reconnect authority conflict: Series changed from %s to %s: %w", record.ReconnectAuthority.Series.ID, current.Series.ID, domain.ErrConflict)
		case current.PauseID != record.ReconnectAuthority.PauseID:
			return fmt.Errorf("reconnect authority conflict: pause changed from %s to %s: %w", record.ReconnectAuthority.PauseID, current.PauseID, domain.ErrConflict)
		}
		if err := validateReconnectMutationDelta(current, record); err != nil {
			return fmt.Errorf("validate reconnect mutation: %w", err)
		}
		if err := r.persistTournamentReconnectMutation(txCtx, q, current, record); err != nil {
			return err
		}
		document, meta, err := encodeTournamentReconnectReceipt(record)
		if err != nil {
			return err
		}
		created, err := q.InsertTournamentReconnectCommandReceipt(txCtx, sqlc.InsertTournamentReconnectCommandReceiptParams{
			CommandID:                 meta.CommandID,
			TournamentID:              meta.TournamentID,
			RosterID:                  meta.RosterID,
			WaveID:                    meta.WaveID,
			MutationKind:              string(meta.MutationKind),
			ParticipantID:             meta.ParticipantID,
			IntervalID:                nullableUUID(meta.IntervalID),
			ExpectedAuthorityRevision: meta.ExpectedAuthorityRevision,
			ResultAuthorityRevision:   meta.ResultAuthorityRevision,
			SchemaVersion:             meta.SchemaVersion,
			RecordDocument:            document,
			RecordedAt:                tstz(meta.RecordedAt),
			CreatedAt:                 tstz(meta.RecordedAt),
		})
		if err != nil {
			return mapRepositoryWriteError("save reconnect command receipt", err)
		}
		if created != meta.CommandID {
			return fmt.Errorf("save reconnect command receipt returned unexpected command: %w", domain.ErrInternal)
		}
		if record.ReconnectAuthority.Current == nil &&
			(record.Kind == reconnectusecase.MutationDisconnect || record.Kind == reconnectusecase.MutationReconnect) {
			if err := r.persistReconnectLiveOutbox(txCtx, q, record); err != nil {
				return err
			}
		}
		clone := record
		committed = &clone
		changed = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	if committed == nil {
		return nil, false, fmt.Errorf("commit reconnect mutation returned no record: %w", domain.ErrInternal)
	}
	return committed, changed, nil
}

// persistReconnectLiveOutbox appends the generic notification only after the
// receipt exists.  It deliberately runs in CommitMutation's transaction: a
// projection race, source mismatch, or deferred source-guard failure rolls
// back the graph mutation and receipt together with the event.
//
//nolint:gocyclo // One transactional boundary validates and binds the full normalized outbox source.
func (r *TournamentReconnectPostgres) persistReconnectLiveOutbox(
	ctx context.Context,
	q *sqlc.Queries,
	record reconnectusecase.ReconnectRecord,
) error {
	if record.ReconnectAuthority.Current != nil ||
		(record.Kind != reconnectusecase.MutationDisconnect && record.Kind != reconnectusecase.MutationReconnect) {
		return domain.ErrValidation
	}
	commandID := commandIDOfReconnectRecord(record)
	scope := record.ReconnectAuthority.Scope
	if commandID == uuid.Nil || scope.Validate() != nil || record.ReconnectAuthority.Game.ID == uuid.Nil ||
		record.ReconnectAuthority.Series.ID == uuid.Nil || record.ReconnectAuthority.GameRevision < 1 ||
		record.ExpectedAuthorityRevision < 1 || record.ReconnectAuthority.Revision < 1 || record.RecordedAt.IsZero() {
		return domain.ErrValidation
	}
	projection, err := q.GetCurrentProjectionRevision(ctx, sqlc.GetCurrentProjectionRevisionParams{
		TournamentID: scope.TournamentID,
		RosterID:     scope.RosterID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("load reconnect outbox projection: %w", domain.ErrConflict)
	}
	if err != nil {
		return fmt.Errorf("load reconnect outbox projection: %w", err)
	}
	if projection.ID == uuid.Nil || projection.State != "published" || projection.RevisionNumber < 1 ||
		projection.RevisionNumber != record.ReconnectAuthority.CurrentProjectionRevision {
		return fmt.Errorf("reconnect outbox projection changed: %w", domain.ErrConflict)
	}
	payload, err := marshalJSON("TournamentAdminExecutionPostgres - CommitMutation - reconnect outbox payload", reconnectOutboxPayload{
		Schema:       reconnectOutboxSchema,
		GameID:       record.ReconnectAuthority.Game.ID,
		GameRevision: record.ReconnectAuthority.GameRevision,
		MutationKind: string(record.Kind),
	})
	if err != nil {
		return err
	}
	eventID := derivedReconnectOutboxID(commandID, "event")
	idempotencyKey := derivedReconnectOutboxID(commandID, "idempotency")
	row, err := q.CreateTournamentReconnectOutboxEvent(ctx, sqlc.CreateTournamentReconnectOutboxEventParams{
		ID:                        eventID,
		TournamentID:              scope.TournamentID,
		RosterID:                  scope.RosterID,
		WaveID:                    scope.WaveID,
		SeriesID:                  record.ReconnectAuthority.Series.ID,
		GameAttemptID:             record.ReconnectAuthority.Game.ID,
		GameRevision:              record.ReconnectAuthority.GameRevision,
		CommandID:                 commandID,
		MutationKind:              string(record.Kind),
		ExpectedAuthorityRevision: record.ExpectedAuthorityRevision,
		ResultAuthorityRevision:   record.ReconnectAuthority.Revision,
		ProjectionRevisionID:      projection.ID,
		ProjectionRevision:        projection.RevisionNumber,
		IdempotencyKey:            idempotencyKey,
		Topic:                     reconnectOutboxTopic,
		Payload:                   payload,
		CreatedAt:                 tstz(record.RecordedAt),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		if contextErr := ctx.Err(); contextErr != nil {
			return fmt.Errorf("save reconnect outbox event: %w", contextErr)
		}
		return fmt.Errorf("save reconnect outbox event: %w", domain.ErrInternal)
	}
	if err != nil {
		return mapRepositoryWriteError("save reconnect outbox event", err)
	}
	if row.ID != eventID || row.TournamentID != scope.TournamentID || row.RosterID != scope.RosterID ||
		row.ProjectionRevisionID != projection.ID || row.ProjectionRevision != projection.RevisionNumber ||
		row.IdempotencyKey != idempotencyKey || row.Terminal || row.Audience != "all" || row.PrincipalID.Valid ||
		row.Topic != reconnectOutboxTopic || !row.CreatedAt.Valid ||
		!row.CreatedAt.Time.UTC().Equal(record.RecordedAt.UTC().Truncate(time.Microsecond)) ||
		len(row.Payload) == 0 {
		return fmt.Errorf("saved reconnect outbox event differs from mutation: %w", domain.ErrInternal)
	}
	return nil
}

func (r *TournamentReconnectPostgres) findTournamentReconnectReceipt(
	ctx context.Context,
	tournamentID uuid.UUID,
	commandID uuid.UUID,
) (*reconnectusecase.ReconnectRecord, error) {
	row, err := r.tx.Querier(ctx).GetTournamentReconnectCommandReceipt(ctx, sqlc.GetTournamentReconnectCommandReceiptParams{
		TournamentID: tournamentID,
		CommandID:    commandID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load reconnect command receipt: %w", err)
	}
	record, meta, err := decodeTournamentReconnectReceipt(row.RecordDocument)
	if err != nil {
		return nil, err
	}
	rowMeta, err := tournamentReconnectReceiptRowMeta(row)
	if err != nil || !sameTournamentReconnectReceiptMeta(meta, rowMeta) || row.CommandID != commandID {
		return nil, domain.ErrInternal
	}
	if err := validateTournamentReconnectRecord(*record, meta); err != nil {
		return nil, err
	}
	return record, nil
}

//nolint:gocyclo // One locked aggregate prevents mixed-series and stale-attempt authority from escaping.
func (r *TournamentReconnectPostgres) loadTournamentReconnectAuthority(
	ctx context.Context,
	scope pausedomain.GraphScope,
	participantID uuid.UUID,
) (reconnectusecase.ReconnectAuthority, error) {
	if participantID == uuid.Nil {
		return reconnectusecase.ReconnectAuthority{}, domain.ErrValidation
	}
	q := r.tx.Querier(ctx)
	if err := lockTournamentResultScope(ctx, q, scope.TournamentID, scope.RosterID); err != nil {
		return reconnectusecase.ReconnectAuthority{}, fmt.Errorf("lock reconnect result scope: %w", err)
	}
	header, err := q.LockTournamentAdminWaveAuthority(ctx, sqlc.LockTournamentAdminWaveAuthorityParams{
		TournamentID: scope.TournamentID,
		WaveID:       scope.WaveID,
	})
	if err != nil {
		return reconnectusecase.ReconnectAuthority{}, fmt.Errorf("lock reconnect Wave: %w", err)
	}
	if header.RosterID != scope.RosterID || header.TournamentID != scope.TournamentID || header.ID != scope.WaveID ||
		header.Revision < 1 || header.ProjectionRevision < 1 {
		return reconnectusecase.ReconnectAuthority{}, domain.ErrConflict
	}
	receipts, err := r.latestTournamentReconnectReceipts(ctx, scope)
	if err != nil {
		return reconnectusecase.ReconnectAuthority{}, err
	}
	latestForParticipant := latestTournamentReconnectReceiptForParticipant(receipts, participantID)
	games, err := q.LockWaveStartGames(ctx, scope.WaveID)
	if err != nil {
		return reconnectusecase.ReconnectAuthority{}, fmt.Errorf("lock reconnect games: %w", err)
	}
	candidate, err := selectTournamentReconnectGame(games, latestForParticipant, participantID)
	if err != nil {
		return reconnectusecase.ReconnectAuthority{}, err
	}
	latest := latestTournamentReconnectReceiptForGame(receipts, candidate.SeriesID, candidate.GameID)
	seriesRow, err := q.LockResultSeries(ctx, sqlc.LockResultSeriesParams{
		SeriesID: candidate.SeriesID, TournamentID: scope.TournamentID, RosterID: scope.RosterID,
	})
	if err != nil {
		return reconnectusecase.ReconnectAuthority{}, fmt.Errorf("lock reconnect Series: %w", err)
	}
	if seriesRow.ID != candidate.SeriesID || seriesRow.TournamentID != scope.TournamentID ||
		seriesRow.RosterID != scope.RosterID || seriesRow.Revision != candidate.SeriesRevision ||
		seriesRow.ScoreHeadRevision < 1 || !seriesRow.CurrentScoreRevisionID.Valid ||
		seriesRow.CurrentScoreRevisionID.UUID != seriesRow.ScoreHeadRevisionID {
		return reconnectusecase.ReconnectAuthority{}, domain.ErrConflict
	}
	graphRows, err := q.ListRecoverySeriesGraph(ctx, sqlc.ListRecoverySeriesGraphParams{
		SeriesID: candidate.SeriesID, RosterID: scope.RosterID,
	})
	if err != nil {
		return reconnectusecase.ReconnectAuthority{}, fmt.Errorf("load reconnect Series graph: %w", err)
	}
	series, err := recoverySeries(reconnectSeriesModel(seriesRow), graphRows)
	if err != nil {
		return reconnectusecase.ReconnectAuthority{}, fmt.Errorf("map reconnect Series: %w", err)
	}
	game, found := reconnectGameInSeries(series, candidate.GameID)
	gameRevision, gameRevisionFound := reconnectGraphGameRevision(graphRows, candidate.GameID)
	if !found || !gameRevisionFound || game.ID != candidate.GameID || gameRevision != candidate.GameRevision {
		return reconnectusecase.ReconnectAuthority{}, domain.ErrConflict
	}
	resultIDs, err := q.ListRecoveryGameResultRevisionIDs(ctx, sqlc.ListRecoveryGameResultRevisionIDsParams{
		SeriesID: candidate.SeriesID, RosterID: scope.RosterID,
	})
	if err != nil {
		return reconnectusecase.ReconnectAuthority{}, fmt.Errorf("load reconnect result revisions: %w", err)
	}
	currentGameResultIDs, err := recoveryResultRevisionIDs(resultIDs)
	if err != nil {
		return reconnectusecase.ReconnectAuthority{}, err
	}
	projection, err := q.GetCurrentProjectionRevision(ctx, sqlc.GetCurrentProjectionRevisionParams{
		TournamentID: scope.TournamentID, RosterID: scope.RosterID,
	})
	if err != nil {
		return reconnectusecase.ReconnectAuthority{}, fmt.Errorf("load reconnect projection: %w", err)
	}
	if projection.RevisionNumber < 1 || projection.ID == uuid.Nil {
		return reconnectusecase.ReconnectAuthority{}, domain.ErrConflict
	}
	projectionRevision := projection.RevisionNumber
	if latest != nil && latest.ReconnectAuthority.Game.ID == game.ID && latest.ReconnectAuthority.Series.ID == series.ID {
		if latest.ReconnectAuthority.GameRevision != gameRevision ||
			latest.ReconnectAuthority.SeriesRevision != seriesRow.Revision ||
			latest.ReconnectAuthority.CurrentProjectionRevision != projectionRevision {
			return reconnectusecase.ReconnectAuthority{}, domain.ErrConflict
		}
		if latest.ReconnectAuthority.Current != nil {
			if latest.ReconnectAuthority.Scope != scope || !game.State.IsTerminal() ||
				!latest.ReconnectAuthority.Game.State.IsTerminal() {
				return reconnectusecase.ReconnectAuthority{}, domain.ErrConflict
			}
			return latest.ReconnectAuthority, nil
		}
	}
	currentRevision := int64(1)
	if latest != nil && latest.ReconnectAuthority.Game.ID == game.ID && latest.ReconnectAuthority.Series.ID == series.ID {
		currentRevision = latest.ReconnectAuthority.Revision
		if currentRevision < 1 {
			return reconnectusecase.ReconnectAuthority{}, domain.ErrInternal
		}
	}
	if currentRevision < 1 {
		currentRevision = 1
	}
	currentOrdinal, err := reconnectCurrentOrdinal(seriesRow)
	if err != nil {
		return reconnectusecase.ReconnectAuthority{}, err
	}
	activePause, pauseErr := q.LockTournamentReconnectGamePause(ctx, sqlc.LockTournamentReconnectGamePauseParams{
		TournamentID: scope.TournamentID, RosterID: scope.RosterID, WaveID: scope.WaveID, GameAttemptID: nullableUUIDValue(game.ID),
	})
	hasActivePause := pauseErr == nil
	if pauseErr != nil && !errors.Is(pauseErr, pgx.ErrNoRows) {
		return reconnectusecase.ReconnectAuthority{}, fmt.Errorf("load active reconnect pause: %w", pauseErr)
	}
	if game.State == domain.GameStatePaused && !hasActivePause {
		return reconnectusecase.ReconnectAuthority{}, domain.ErrConflict
	}
	if game.State != domain.GameStatePaused && hasActivePause {
		return reconnectusecase.ReconnectAuthority{}, domain.ErrConflict
	}
	var pauseID uuid.UUID
	var clock pausedomain.PauseResumeGameClock
	var presence []pausedomain.PausePresence
	var intervals []pausedomain.PauseReconnectInterval
	var counters []pausedomain.PauseReconnectCounter
	if hasActivePause {
		pauseID = activePause.Pause.ID
		clockRow, err := q.LockTournamentReconnectPauseClock(ctx, sqlc.LockTournamentReconnectPauseClockParams{
			TournamentID: scope.TournamentID, RosterID: scope.RosterID, WaveID: scope.WaveID,
			GameAttemptID: nullableUUIDValue(game.ID), PauseID: pauseID,
		})
		if err != nil {
			return reconnectusecase.ReconnectAuthority{}, fmt.Errorf("load reconnect clock: %w", err)
		}
		clock, err = recoveryGameClock(clockRow.PauseClock)
		if err != nil {
			return reconnectusecase.ReconnectAuthority{}, err
		}
		presenceRows, err := q.ListRecoveryPresence(ctx, sqlc.ListRecoveryPresenceParams{SeriesID: series.ID, RosterID: scope.RosterID})
		if err != nil {
			return reconnectusecase.ReconnectAuthority{}, fmt.Errorf("load reconnect Presence: %w", err)
		}
		presence, err = recoveryPresence(presenceRows)
		if err != nil {
			return reconnectusecase.ReconnectAuthority{}, err
		}
		intervalRows, err := q.ListRecoveryReconnectIntervals(ctx, sqlc.ListRecoveryReconnectIntervalsParams{PauseID: pauseID, RosterID: scope.RosterID})
		if err != nil {
			return reconnectusecase.ReconnectAuthority{}, fmt.Errorf("load reconnect intervals: %w", err)
		}
		intervals, err = recoveryIntervals(intervalRows)
		if err != nil {
			return reconnectusecase.ReconnectAuthority{}, err
		}
		counterRows, err := q.ListRecoveryReconnectCounters(ctx, sqlc.ListRecoveryReconnectCountersParams{PauseID: pauseID, RosterID: scope.RosterID})
		if err != nil {
			return reconnectusecase.ReconnectAuthority{}, fmt.Errorf("load reconnect counters: %w", err)
		}
		counters, err = recoveryCounters(counterRows)
		if err != nil {
			return reconnectusecase.ReconnectAuthority{}, err
		}
	} else {
		pauseID = reconnectSyntheticPauseID(game.ID, currentRevision)
		presenceRows, err := q.ListRecoveryPresence(ctx, sqlc.ListRecoveryPresenceParams{SeriesID: series.ID, RosterID: scope.RosterID})
		if err != nil {
			return reconnectusecase.ReconnectAuthority{}, fmt.Errorf("load reconnect Presence: %w", err)
		}
		presence, err = recoveryPresence(presenceRows)
		if err != nil {
			return reconnectusecase.ReconnectAuthority{}, err
		}
		if latest != nil && latest.Kind == reconnectusecase.MutationReconnect && latest.ReconnectAuthority.Game.ID == game.ID &&
			latest.ReconnectAuthority.Series.ID == series.ID &&
			latest.ReconnectAuthority.Game.State == domain.GameStateActive {
			// The game graph no longer points at a pause after resume. Rebuild
			// the active timing and reconnect budget from the validated receipt,
			// then verify its clock against the durable resumed pause clock.
			resumedRow, resumedErr := q.GetLatestTournamentReconnectResumedClock(ctx, sqlc.GetLatestTournamentReconnectResumedClockParams{
				TournamentID: scope.TournamentID, RosterID: scope.RosterID, WaveID: scope.WaveID,
				GameAttemptID: nullableUUIDValue(game.ID),
			})
			if resumedErr != nil {
				if errors.Is(resumedErr, pgx.ErrNoRows) {
					return reconnectusecase.ReconnectAuthority{}, fmt.Errorf("reconnect resumed clock is missing: %w", domain.ErrConflict)
				}
				return reconnectusecase.ReconnectAuthority{}, fmt.Errorf("load reconnect resumed clock: %w", resumedErr)
			}
			clock, counters, err = rehydrateTournamentReconnectActiveState(
				latest.ReconnectAuthority, resumedRow.PauseClock, pauseID,
			)
			if err != nil {
				return reconnectusecase.ReconnectAuthority{}, err
			}
			// Closed intervals remain owned by the resumed pause. The next
			// disconnect starts a new append-only pause lifecycle, so it must not
			// replay historical interval rows under the synthetic identity.
			intervals = []pausedomain.PauseReconnectInterval{}
		} else {
			deadline, deadlineErr := r.reconnectCurrentDeadline(ctx, q, scope, candidate, game)
			if deadlineErr != nil {
				return reconnectusecase.ReconnectAuthority{}, deadlineErr
			}
			clock = pausedomain.PauseResumeGameClock{PauseID: pauseID, GameID: game.ID, OriginalDeadline: deadline, Revision: 1}
			counters = []pausedomain.PauseReconnectCounter{
				{PauseID: pauseID, RosterID: scope.RosterID, ParticipantID: series.FirstParticipantID, Limit: domain.ReconnectCycleLimit, Revision: 1},
				{PauseID: pauseID, RosterID: scope.RosterID, ParticipantID: series.SecondParticipantID, Limit: domain.ReconnectCycleLimit, Revision: 1},
			}
			intervals = []pausedomain.PauseReconnectInterval{}
		}
	}
	authority := reconnectusecase.ReconnectAuthority{
		Scope: scope, Revision: currentRevision, PauseID: pauseID,
		GameRevision: gameRevision, SeriesRevision: seriesRow.Revision,
		CurrentOrdinal: currentOrdinal, CurrentProjectionRevision: projectionRevision,
		CurrentGameResultRevisionIDs: currentGameResultIDs, Game: game, Series: series,
		GameClock: clock, Presence: presence, Reconnect: intervals, Counters: counters,
	}
	if err := validateTournamentReconnectAuthorityShape(authority); err != nil {
		return reconnectusecase.ReconnectAuthority{}, err
	}
	return authority, nil
}

func (r *TournamentReconnectPostgres) latestTournamentReconnectReceipts(
	ctx context.Context,
	scope pausedomain.GraphScope,
) ([]*reconnectusecase.ReconnectRecord, error) {
	rows, err := r.tx.Querier(ctx).ListTournamentReconnectCommandReceipts(ctx, sqlc.ListTournamentReconnectCommandReceiptsParams{
		TournamentID: scope.TournamentID, RosterID: scope.RosterID, WaveID: scope.WaveID,
	})
	if err != nil {
		return nil, fmt.Errorf("load reconnect receipts: %w", err)
	}
	result := make([]*reconnectusecase.ReconnectRecord, 0, len(rows))
	for _, row := range rows {
		record, meta, decodeErr := decodeTournamentReconnectReceipt(row.RecordDocument)
		if decodeErr != nil {
			return nil, decodeErr
		}
		rowMeta, metaErr := tournamentReconnectLatestReceiptRowMeta(row)
		if metaErr != nil || !sameTournamentReconnectReceiptMeta(meta, rowMeta) ||
			meta.TournamentID != scope.TournamentID || meta.RosterID != scope.RosterID || meta.WaveID != scope.WaveID {
			return nil, domain.ErrInternal
		}
		if err := validateTournamentReconnectRecord(*record, meta); err != nil {
			return nil, err
		}
		clone := *record
		result = append(result, &clone)
	}
	return result, nil
}

func latestTournamentReconnectReceiptForParticipant(
	receipts []*reconnectusecase.ReconnectRecord,
	participantID uuid.UUID,
) *reconnectusecase.ReconnectRecord {
	for _, record := range receipts {
		if record == nil {
			continue
		}
		series := record.ReconnectAuthority.Series
		if series.FirstParticipantID == participantID || series.SecondParticipantID == participantID {
			return record
		}
	}
	return nil
}

func latestTournamentReconnectReceiptForGame(
	receipts []*reconnectusecase.ReconnectRecord,
	seriesID, gameID uuid.UUID,
) *reconnectusecase.ReconnectRecord {
	for _, record := range receipts {
		if record != nil && record.ReconnectAuthority.Series.ID == seriesID && record.ReconnectAuthority.Game.ID == gameID {
			return record
		}
	}
	return nil
}

func (r *TournamentReconnectPostgres) reconnectCurrentDeadline(
	ctx context.Context,
	q *sqlc.Queries,
	scope pausedomain.GraphScope,
	row sqlc.LockWaveStartGamesRow,
	game domain.Game,
) (time.Time, error) {
	resumed, err := q.GetLatestTournamentReconnectResumedClock(ctx, sqlc.GetLatestTournamentReconnectResumedClockParams{
		TournamentID: scope.TournamentID, RosterID: scope.RosterID, WaveID: scope.WaveID, GameAttemptID: nullableUUIDValue(game.ID),
	})
	if err == nil {
		if !resumed.PauseClock.ResumedDeadline.Valid {
			return time.Time{}, domain.ErrInternal
		}
		deadline, timeErr := requiredRecoveryTime(resumed.PauseClock.ResumedDeadline)
		if timeErr != nil {
			return time.Time{}, timeErr
		}
		return deadline, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, fmt.Errorf("load current reconnect deadline: %w", err)
	}
	attempt, err := q.LockResultAttempt(ctx, sqlc.LockResultAttemptParams{
		AttemptID: game.ID, SeriesID: row.SeriesID, RosterID: scope.RosterID, TournamentID: scope.TournamentID,
	})
	if err != nil {
		return time.Time{}, fmt.Errorf("load current reconnect attempt: %w", err)
	}
	startedAt, err := requiredRecoveryTime(attempt.StartedAt)
	if err != nil {
		return time.Time{}, err
	}
	if row.TimeLimit < 1 {
		return time.Time{}, domain.ErrInternal
	}
	deadline, ok := pausedomain.AddTime(startedAt, domain.TournamentTaskDuration)
	if !ok {
		return time.Time{}, domain.ErrInternal
	}
	return deadline, nil
}

// rehydrateTournamentReconnectActiveState restores the state that is no
// longer represented by the active game row after a reconnect pause resumes.
// Receipts retain the complete reconnect history, while the pause clock keeps
// the authoritative frozen and resumed timing. A new synthetic pause identity
// keeps the next disconnect append-only and prevents it from mutating the old
// resumed pause or its counters.
func rehydrateTournamentReconnectActiveState(
	receipt reconnectusecase.ReconnectAuthority,
	row sqlc.PauseClock,
	nextPauseID uuid.UUID,
) (pausedomain.PauseResumeGameClock, []pausedomain.PauseReconnectCounter, error) {
	if nextPauseID == uuid.Nil || receipt.Scope.Validate() != nil || receipt.PauseID == uuid.Nil ||
		receipt.Game.ID == uuid.Nil || receipt.Series.ID == uuid.Nil || receipt.Game.State != domain.GameStateActive ||
		receipt.Current != nil || row.PauseID == uuid.Nil || row.GameAttemptID != receipt.Game.ID {
		return pausedomain.PauseResumeGameClock{}, nil, domain.ErrConflict
	}
	clock, err := reconnectResumedGameClock(receipt.GameClock, row)
	if err != nil {
		return pausedomain.PauseResumeGameClock{}, nil, err
	}
	clock.PauseID = nextPauseID
	counters, err := remapTournamentReconnectCounters(receipt, nextPauseID)
	if err != nil {
		return pausedomain.PauseResumeGameClock{}, nil, err
	}
	return clock, counters, nil
}

//nolint:gocyclo // Durable and domain clock precision must be compared as one correlated authority record.
func reconnectResumedGameClock(
	receiptClock pausedomain.PauseResumeGameClock,
	row sqlc.PauseClock,
) (pausedomain.PauseResumeGameClock, error) {
	if row.PauseID == uuid.Nil || row.GameAttemptID == uuid.Nil || row.FrozenRemainingMs < 1 ||
		row.FrozenRemainingMs > math.MaxInt64/int64(time.Millisecond) {
		return pausedomain.PauseResumeGameClock{}, fmt.Errorf("invalid resumed reconnect clock: %w", domain.ErrConflict)
	}
	originalDeadline, err := requiredRecoveryTime(row.OriginalDeadline)
	if err != nil {
		return pausedomain.PauseResumeGameClock{}, fmt.Errorf("invalid resumed reconnect deadline: %w", domain.ErrConflict)
	}
	frozenAt, err := requiredRecoveryTime(row.FrozenAt)
	if err != nil {
		return pausedomain.PauseResumeGameClock{}, fmt.Errorf("invalid resumed reconnect freeze time: %w", domain.ErrConflict)
	}
	resumedAt, err := requiredRecoveryTime(row.ResumedAt)
	if err != nil {
		return pausedomain.PauseResumeGameClock{}, fmt.Errorf("invalid resumed reconnect time: %w", domain.ErrConflict)
	}
	resumedDeadline, err := requiredRecoveryTime(row.ResumedDeadline)
	if err != nil {
		return pausedomain.PauseResumeGameClock{}, fmt.Errorf("invalid resumed reconnect deadline: %w", domain.ErrConflict)
	}
	remaining, err := reconnectFrozenDuration(originalDeadline, frozenAt, row.FrozenRemainingMs)
	if err != nil {
		return pausedomain.PauseResumeGameClock{}, fmt.Errorf("invalid resumed reconnect clock: %w", domain.ErrConflict)
	}
	if receiptClock.PauseID != row.PauseID || receiptClock.GameID != row.GameAttemptID ||
		receiptClock.Revision != row.Revision || !receiptClock.OriginalDeadline.Equal(originalDeadline) ||
		!receiptClock.FrozenAt.Equal(frozenAt) || receiptClock.Remaining != remaining ||
		receiptClock.ResumedAt == nil || !receiptClock.ResumedAt.Equal(resumedAt) ||
		receiptClock.ResumedDeadline == nil || receiptClock.Validate(false) != nil || !resumedAt.After(frozenAt) {
		return pausedomain.PauseResumeGameClock{}, fmt.Errorf("invalid resumed reconnect clock: %w", domain.ErrConflict)
	}
	canonicalDeadline, err := reconnectCanonicalResumedDeadline(
		resumedAt, *receiptClock.ResumedDeadline, row.FrozenRemainingMs,
	)
	if err != nil || !resumedDeadline.Equal(canonicalDeadline) {
		return pausedomain.PauseResumeGameClock{}, fmt.Errorf("invalid resumed reconnect clock: %w", domain.ErrConflict)
	}
	return receiptClock, nil
}

//nolint:gocyclo // Both participant counters must be validated and remapped as one restart invariant.
func remapTournamentReconnectCounters(
	receipt reconnectusecase.ReconnectAuthority,
	nextPauseID uuid.UUID,
) ([]pausedomain.PauseReconnectCounter, error) {
	if nextPauseID == uuid.Nil || receipt.PauseID == uuid.Nil || receipt.Scope.Validate() != nil ||
		receipt.Series.ID == uuid.Nil || receipt.Game.ID == uuid.Nil || len(receipt.Counters) != 2 {
		return nil, domain.ErrConflict
	}
	counters := make([]pausedomain.PauseReconnectCounter, len(receipt.Counters))
	seenParticipants := make(map[uuid.UUID]struct{}, len(receipt.Counters))
	for index, counter := range receipt.Counters {
		if counter.PauseID != receipt.PauseID || counter.RosterID != receipt.Scope.RosterID ||
			counter.Validate() != nil ||
			(counter.ParticipantID != receipt.Series.FirstParticipantID && counter.ParticipantID != receipt.Series.SecondParticipantID) {
			return nil, domain.ErrConflict
		}
		if _, exists := seenParticipants[counter.ParticipantID]; exists {
			return nil, domain.ErrConflict
		}
		seenParticipants[counter.ParticipantID] = struct{}{}
		counter.PauseID = nextPauseID
		counters[index] = counter
	}
	if _, ok := seenParticipants[receipt.Series.FirstParticipantID]; !ok {
		return nil, domain.ErrConflict
	}
	if _, ok := seenParticipants[receipt.Series.SecondParticipantID]; !ok {
		return nil, domain.ErrConflict
	}
	return counters, nil
}

//nolint:gocyclo // Game selection rejects ambiguous multi-series authority in one closed decision tree.
func selectTournamentReconnectGame(
	rows []sqlc.LockWaveStartGamesRow,
	latest *reconnectusecase.ReconnectRecord,
	participantID uuid.UUID,
) (sqlc.LockWaveStartGamesRow, error) {
	if participantID == uuid.Nil {
		return sqlc.LockWaveStartGamesRow{}, domain.ErrValidation
	}
	active := make([]sqlc.LockWaveStartGamesRow, 0, len(rows))
	for _, row := range rows {
		if row.FirstParticipantID != participantID && row.SecondParticipantID != participantID {
			continue
		}
		if row.SeriesState == string(domain.SeriesStateActive) &&
			(row.GameState == string(domain.GameStateActive) || row.GameState == string(domain.GameStatePaused)) {
			active = append(active, row)
		}
	}
	if len(active) == 1 {
		return active[0], nil
	}
	if len(active) > 1 {
		return sqlc.LockWaveStartGamesRow{}, fmt.Errorf("reconnect Wave has multiple executable games: %w", domain.ErrConflict)
	}
	if latest == nil || latest.ReconnectAuthority.Current == nil {
		return sqlc.LockWaveStartGamesRow{}, fmt.Errorf("reconnect executable game is missing: %w", domain.ErrConflict)
	}
	gameID := latest.ReconnectAuthority.Game.ID
	seriesID := latest.ReconnectAuthority.Series.ID
	var selected sqlc.LockWaveStartGamesRow
	found := false
	for _, row := range rows {
		if row.FirstParticipantID != participantID && row.SecondParticipantID != participantID {
			continue
		}
		if row.GameID != gameID || row.SeriesID != seriesID {
			continue
		}
		if found {
			return sqlc.LockWaveStartGamesRow{}, fmt.Errorf("reconnect terminal game is ambiguous: %w", domain.ErrConflict)
		}
		selected, found = row, true
	}
	if !found {
		return sqlc.LockWaveStartGamesRow{}, fmt.Errorf("reconnect terminal game is missing: %w", domain.ErrConflict)
	}
	return selected, nil
}

func reconnectSeriesModel(row sqlc.LockResultSeriesRow) sqlc.Series {
	return sqlc.Series{
		ID: row.ID, TournamentID: row.TournamentID, RosterID: row.RosterID,
		FirstParticipantID: row.FirstParticipantID, SecondParticipantID: row.SecondParticipantID,
		Format: row.Format, State: row.State,
		FirstParticipantWins: row.FirstParticipantWins, SecondParticipantWins: row.SecondParticipantWins,
		WinnerID: row.WinnerID, CurrentScoreRevisionID: row.CurrentScoreRevisionID,
		CurrentResultRevisionID: row.CurrentResultRevisionID, Revision: row.Revision,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, StartedAt: row.StartedAt, FinishedAt: row.FinishedAt,
	}
}

func reconnectGameInSeries(series domain.Series, gameID uuid.UUID) (domain.Game, bool) {
	for _, slot := range series.Slots {
		for _, game := range slot.Attempts {
			if game.ID == gameID {
				return game, true
			}
		}
	}
	return domain.Game{}, false
}

func reconnectGraphGameRevision(rows []sqlc.ListRecoverySeriesGraphRow, gameID uuid.UUID) (int64, bool) {
	var revision int64
	found := false
	for _, row := range rows {
		if row.GameAttempt.ID != gameID {
			continue
		}
		if found && revision != row.GameAttempt.Revision {
			return 0, false
		}
		revision = row.GameAttempt.Revision
		found = true
	}
	return revision, found && revision >= 1
}

func reconnectCurrentOrdinal(row sqlc.LockResultSeriesRow) (int, error) {
	return recoveryCurrentOrdinal(sqlc.SeriesScoreHead{
		SeriesID: row.ID, RosterID: row.RosterID,
		CurrentRevisionID: row.ScoreHeadRevisionID, Revision: row.ScoreHeadRevision,
	})
}

func reconnectSyntheticPauseID(gameID uuid.UUID, revision int64) uuid.UUID {
	return uuid.NewSHA1(gameID, []byte(fmt.Sprintf("reconnect-pause:%d", revision)))
}

func validateTournamentReconnectAuthorityShape(authority reconnectusecase.ReconnectAuthority) error {
	if authority.Scope.Validate() != nil || authority.Revision < 1 || authority.PauseID == uuid.Nil ||
		authority.GameRevision < 1 || authority.SeriesRevision < 1 || authority.Game.ID == uuid.Nil ||
		authority.Series.ID == uuid.Nil || authority.GameClock.PauseID != authority.PauseID ||
		authority.GameClock.GameID != authority.Game.ID || authority.GameClock.OriginalDeadline.IsZero() ||
		authority.CurrentProjectionRevision < 1 || len(authority.Presence) != 2 || len(authority.Counters) != 2 {
		return domain.ErrInternal
	}
	return nil
}

//nolint:gocyclo // Receipt shape validation covers the complete mutation union and authority proof.
func validateTournamentReconnectRecordShape(record reconnectusecase.ReconnectRecord) error {
	if record.ExpectedAuthorityRevision < 1 || record.ReconnectAuthority.Revision != record.ExpectedAuthorityRevision+1 ||
		record.ReconnectAuthority.Scope.Validate() != nil || record.RecordedAt.IsZero() {
		return domain.ErrValidation
	}
	if commandIDOfReconnectRecord(record) == uuid.Nil || record.ReconnectAuthority.Scope != reconnectRecordCommandScope(record) {
		return domain.ErrValidation
	}
	if record.ReconnectAuthority.Current == nil && (record.GameResultRevision != nil || record.VoidGameResultRevision != nil ||
		record.ScoreRevision != nil || record.SeriesResultRevision != nil || record.ReplayRoute != nil || record.Evidence != nil) {
		return domain.ErrValidation
	}
	if record.ReconnectAuthority.Current != nil && (record.ScoreRevision == nil || record.Evidence == nil) {
		return domain.ErrValidation
	}
	return nil
}

func validateTournamentReconnectRecord(record reconnectusecase.ReconnectRecord, meta tournamentReconnectReceiptMeta) error {
	if err := validateTournamentReconnectRecordShape(record); err != nil {
		return err
	}
	commandID, scope, participantID, _ := reconnectRecordCommand(record)
	intervalID := reconnectRecordIntervalID(record)
	if commandID != meta.CommandID || scope.TournamentID != meta.TournamentID || scope.RosterID != meta.RosterID ||
		scope.WaveID != meta.WaveID || participantID != meta.ParticipantID || record.Kind != meta.MutationKind ||
		record.ExpectedAuthorityRevision != meta.ExpectedAuthorityRevision ||
		record.ReconnectAuthority.Revision != meta.ResultAuthorityRevision || meta.SchemaVersion != reconnectReceiptSchemaVersion ||
		!record.RecordedAt.Equal(meta.RecordedAt) {
		return domain.ErrInternal
	}
	if !sameReconnectOptionalUUID(intervalID, meta.IntervalID) {
		return domain.ErrInternal
	}
	return nil
}

func reconnectRecordCommand(record reconnectusecase.ReconnectRecord) (uuid.UUID, pausedomain.GraphScope, uuid.UUID, *uuid.UUID) {
	switch record.Kind {
	case reconnectusecase.MutationReconnect:
		if record.ReconnectCommand == nil {
			return uuid.Nil, pausedomain.GraphScope{}, uuid.Nil, nil
		}
		id := record.ReconnectCommand.IntervalID
		return record.ReconnectCommand.CommandID, record.ReconnectCommand.Scope, record.ReconnectCommand.ParticipantID, &id
	case reconnectusecase.MutationTimeout:
		if record.TimeoutCommand == nil {
			return uuid.Nil, pausedomain.GraphScope{}, uuid.Nil, nil
		}
		id := record.TimeoutCommand.IntervalID
		return record.TimeoutCommand.CommandID, record.TimeoutCommand.Scope, record.TimeoutCommand.ParticipantID, &id
	case reconnectusecase.MutationDisconnect:
		if record.DisconnectCommand == nil {
			return uuid.Nil, pausedomain.GraphScope{}, uuid.Nil, nil
		}
		id := record.DisconnectCommand.IntervalID
		return record.DisconnectCommand.CommandID, record.DisconnectCommand.Scope, record.DisconnectCommand.ParticipantID, &id
	default:
		return uuid.Nil, pausedomain.GraphScope{}, uuid.Nil, nil
	}
}

func reconnectRecordCommandScope(record reconnectusecase.ReconnectRecord) pausedomain.GraphScope {
	_, scope, _, _ := reconnectRecordCommand(record)
	return scope
}

func commandIDOfReconnectRecord(record reconnectusecase.ReconnectRecord) uuid.UUID {
	id, _, _, _ := reconnectRecordCommand(record)
	return id
}

func sameReconnectOptionalUUID(first, second *uuid.UUID) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}

func reconnectRecordIntervalID(record reconnectusecase.ReconnectRecord) *uuid.UUID {
	_, _, _, commandInterval := reconnectRecordCommand(record)
	if commandInterval == nil || *commandInterval == uuid.Nil {
		return nil
	}
	for _, interval := range record.ReconnectAuthority.Reconnect {
		if interval.ID == *commandInterval {
			id := interval.ID
			return &id
		}
	}
	return nil
}

func encodeTournamentReconnectReceipt(record reconnectusecase.ReconnectRecord) ([]byte, tournamentReconnectReceiptMeta, error) {
	if err := validateTournamentReconnectRecordShape(record); err != nil {
		return nil, tournamentReconnectReceiptMeta{}, err
	}
	commandID, scope, participantID, _ := reconnectRecordCommand(record)
	intervalID := reconnectRecordIntervalID(record)
	meta := tournamentReconnectReceiptMeta{
		CommandID: commandID, TournamentID: scope.TournamentID, RosterID: scope.RosterID, WaveID: scope.WaveID,
		MutationKind: record.Kind, ParticipantID: participantID, IntervalID: intervalID,
		ExpectedAuthorityRevision: record.ExpectedAuthorityRevision, ResultAuthorityRevision: record.ReconnectAuthority.Revision,
		SchemaVersion: reconnectReceiptSchemaVersion, RecordedAt: record.RecordedAt,
	}
	//nolint:musttag // This versioned application-owned document is tagged and validated on encode and decode.
	document, err := json.Marshal(tournamentReconnectReceiptDocument{
		SchemaVersion: meta.SchemaVersion, CommandID: meta.CommandID, TournamentID: meta.TournamentID,
		RosterID: meta.RosterID, WaveID: meta.WaveID, MutationKind: meta.MutationKind,
		ParticipantID: meta.ParticipantID, IntervalID: meta.IntervalID,
		ExpectedAuthorityRevision: meta.ExpectedAuthorityRevision, ResultAuthorityRevision: meta.ResultAuthorityRevision,
		RecordedAt: meta.RecordedAt, Record: record,
	})
	if err != nil {
		return nil, tournamentReconnectReceiptMeta{}, fmt.Errorf("encode reconnect receipt: %w", err)
	}
	return document, meta, nil
}

func decodeTournamentReconnectReceipt(document []byte) (*reconnectusecase.ReconnectRecord, tournamentReconnectReceiptMeta, error) {
	if len(document) == 0 || !json.Valid(document) {
		return nil, tournamentReconnectReceiptMeta{}, domain.ErrInternal
	}
	var envelope tournamentReconnectReceiptDocument
	//nolint:musttag // The concrete versioned envelope owns explicit JSON tags.
	if err := json.Unmarshal(document, &envelope); err != nil {
		return nil, tournamentReconnectReceiptMeta{}, fmt.Errorf("decode reconnect receipt: %w", err)
	}
	if envelope.SchemaVersion != reconnectReceiptSchemaVersion {
		return nil, tournamentReconnectReceiptMeta{}, domain.ErrInternal
	}
	meta := tournamentReconnectReceiptMeta{
		CommandID: envelope.CommandID, TournamentID: envelope.TournamentID, RosterID: envelope.RosterID,
		WaveID: envelope.WaveID, MutationKind: envelope.MutationKind, ParticipantID: envelope.ParticipantID,
		IntervalID: envelope.IntervalID, ExpectedAuthorityRevision: envelope.ExpectedAuthorityRevision,
		ResultAuthorityRevision: envelope.ResultAuthorityRevision, SchemaVersion: envelope.SchemaVersion,
		RecordedAt: envelope.RecordedAt,
	}
	return &envelope.Record, meta, nil
}

func tournamentReconnectReceiptRowMeta(row sqlc.ReconnectCommandReceipt) (tournamentReconnectReceiptMeta, error) {
	recordedAt, err := requiredRecoveryTime(row.RecordedAt)
	if err != nil {
		return tournamentReconnectReceiptMeta{}, err
	}
	return tournamentReconnectReceiptMeta{
		CommandID: row.CommandID, TournamentID: row.TournamentID, RosterID: row.RosterID, WaveID: row.WaveID,
		MutationKind: reconnectusecase.MutationKind(row.MutationKind), ParticipantID: row.ParticipantID,
		IntervalID: optionalRecoveryUUID(row.IntervalID), ExpectedAuthorityRevision: row.ExpectedAuthorityRevision,
		ResultAuthorityRevision: row.ResultAuthorityRevision, SchemaVersion: row.SchemaVersion, RecordedAt: recordedAt,
	}, nil
}

func tournamentReconnectLatestReceiptRowMeta(row sqlc.ReconnectCommandReceipt) (tournamentReconnectReceiptMeta, error) {
	recordedAt, err := requiredRecoveryTime(row.RecordedAt)
	if err != nil {
		return tournamentReconnectReceiptMeta{}, err
	}
	return tournamentReconnectReceiptMeta{
		CommandID: row.CommandID, TournamentID: row.TournamentID, RosterID: row.RosterID, WaveID: row.WaveID,
		MutationKind: reconnectusecase.MutationKind(row.MutationKind), ParticipantID: row.ParticipantID,
		IntervalID: optionalRecoveryUUID(row.IntervalID), ExpectedAuthorityRevision: row.ExpectedAuthorityRevision,
		ResultAuthorityRevision: row.ResultAuthorityRevision, SchemaVersion: row.SchemaVersion, RecordedAt: recordedAt,
	}, nil
}

func sameTournamentReconnectReceiptMeta(first, second tournamentReconnectReceiptMeta) bool {
	return first.CommandID == second.CommandID && first.TournamentID == second.TournamentID &&
		first.RosterID == second.RosterID && first.WaveID == second.WaveID && first.MutationKind == second.MutationKind &&
		first.ParticipantID == second.ParticipantID && sameReconnectOptionalUUID(first.IntervalID, second.IntervalID) &&
		first.ExpectedAuthorityRevision == second.ExpectedAuthorityRevision && first.ResultAuthorityRevision == second.ResultAuthorityRevision &&
		first.SchemaVersion == second.SchemaVersion && first.RecordedAt.Equal(second.RecordedAt)
}

func reconnectRecordsEqual(first, second reconnectusecase.ReconnectRecord) bool {
	//nolint:musttag // Domain records are embedded in the tagged, versioned receipt document.
	firstDocument, firstErr := json.Marshal(first)
	//nolint:musttag // Domain records are embedded in the tagged, versioned receipt document.
	secondDocument, secondErr := json.Marshal(second)
	return firstErr == nil && secondErr == nil && bytes.Equal(firstDocument, secondDocument)
}

// validateReconnectMutationDelta is intentionally narrower than the game
// use-case validator. The use case owns the complete domain proof; this
// adapter owns the database CAS proof and therefore rejects any shape that
// could cause a write outside that proof.
//
//nolint:gocyclo // The adapter CAS proof validates every allowed mutation delta in one boundary.
func validateReconnectMutationDelta(
	current reconnectusecase.ReconnectAuthority,
	record reconnectusecase.ReconnectRecord,
) error {
	next := record.ReconnectAuthority
	if current.Current != nil || record.ExpectedAuthorityRevision != current.Revision ||
		next.Revision != current.Revision+1 || next.Scope != current.Scope ||
		next.Game.ID != current.Game.ID || next.Series.ID != current.Series.ID ||
		next.PauseID != current.PauseID || next.SeriesRevision < current.SeriesRevision ||
		next.GameRevision < current.GameRevision || next.CurrentProjectionRevision < current.CurrentProjectionRevision {
		return domain.ErrConflict
	}
	if next.GameClock.GameID != current.Game.ID || next.GameClock.PauseID != current.PauseID ||
		next.Series.TournamentID != current.Scope.TournamentID {
		return domain.ErrValidation
	}
	stateTransitionAllowed :=
		(current.Game.State == domain.GameStateActive && next.Game.State == domain.GameStatePaused) ||
			(current.Game.State == domain.GameStatePaused && next.Game.State == domain.GameStateActive) ||
			next.Game.State.IsTerminal()
	if next.Game.State != current.Game.State && !stateTransitionAllowed {
		return domain.ErrValidation
	}
	if next.Game.State.IsTerminal() {
		if next.Current == nil || record.ScoreRevision == nil || record.Evidence == nil ||
			next.GameRevision != current.GameRevision+1 || next.SeriesRevision != current.SeriesRevision+1 ||
			next.CurrentProjectionRevision != current.CurrentProjectionRevision+1 {
			return domain.ErrValidation
		}
	} else {
		if next.Current != nil || next.CurrentProjectionRevision != current.CurrentProjectionRevision {
			return domain.ErrValidation
		}
		if next.Game.State != current.Game.State {
			if next.GameRevision != current.GameRevision+1 || next.SeriesRevision != current.SeriesRevision+1 {
				return domain.ErrValidation
			}
		} else if next.GameRevision != current.GameRevision || next.SeriesRevision != current.SeriesRevision {
			return domain.ErrValidation
		}
	}
	if err := validateReconnectPresenceDelta(current.Presence, next.Presence); err != nil {
		return err
	}
	if err := validateReconnectCounterDelta(current.Counters, next.Counters); err != nil {
		return err
	}
	return validateReconnectIntervalDelta(current.Reconnect, next.Reconnect)
}

//nolint:gocyclo // Presence transition validation is a closed cross-field state machine.
func validateReconnectPresenceDelta(before, after []pausedomain.PausePresence) error {
	if len(before) != 2 || len(after) != len(before) {
		return domain.ErrValidation
	}
	seen := make(map[uuid.UUID]struct{}, len(before))
	for _, current := range before {
		next, ok := reconnectPresenceByID(after, current.ParticipantID)
		if !ok || current.ID == uuid.Nil || next.ID != current.ID ||
			next.TournamentID != current.TournamentID || next.RosterID != current.RosterID ||
			next.SeriesID != current.SeriesID {
			return domain.ErrConflict
		}
		if _, duplicate := seen[next.ParticipantID]; duplicate {
			return domain.ErrValidation
		}
		seen[next.ParticipantID] = struct{}{}
		if reflect.DeepEqual(current, next) {
			continue
		}
		if current.PresenceEpoch == math.MaxInt64 || current.Revision == math.MaxInt64 ||
			next.PresenceEpoch != current.PresenceEpoch+1 || next.Revision != current.Revision+1 ||
			next.UpdatedAt.IsZero() {
			return domain.ErrValidation
		}
		switch {
		case current.State == pausedomain.PresenceStateConnected && next.State == pausedomain.PresenceStateDisconnected:
			if next.ConnectedAt != current.ConnectedAt || next.DisconnectedAt == nil ||
				!next.UpdatedAt.Equal(*next.DisconnectedAt) {
				return domain.ErrValidation
			}
		case current.State == pausedomain.PresenceStateDisconnected && next.State == pausedomain.PresenceStateConnected:
			if next.DisconnectedAt != nil || !next.ConnectedAt.Equal(next.UpdatedAt) {
				return domain.ErrValidation
			}
		default:
			return domain.ErrValidation
		}
	}
	return nil
}

//nolint:gocyclo // Both bounded participant counters are validated as one aggregate delta.
func validateReconnectCounterDelta(before, after []pausedomain.PauseReconnectCounter) error {
	if len(before) != 2 || len(after) != len(before) {
		return domain.ErrValidation
	}
	for _, current := range before {
		next, ok := reconnectCounterByParticipant(after, current.ParticipantID)
		if !ok || next.PauseID != current.PauseID || next.RosterID != current.RosterID ||
			next.ParticipantID != current.ParticipantID {
			return domain.ErrConflict
		}
		if reflect.DeepEqual(current, next) {
			continue
		}
		if current.Revision == math.MaxInt64 || current.Used == math.MaxInt ||
			next.Revision != current.Revision+1 || next.Used != current.Used+1 ||
			next.Limit != current.Limit || next.Used > next.Limit || next.Used > math.MaxInt16 {
			return domain.ErrValidation
		}
	}
	return nil
}

//nolint:gocyclo // Append-only interval transitions are validated in one fail-closed state machine.
func validateReconnectIntervalDelta(before, after []pausedomain.PauseReconnectInterval) error {
	beforeByID := make(map[uuid.UUID]pausedomain.PauseReconnectInterval, len(before))
	for _, current := range before {
		if current.ID == uuid.Nil {
			return domain.ErrValidation
		}
		if _, duplicate := beforeByID[current.ID]; duplicate {
			return domain.ErrValidation
		}
		beforeByID[current.ID] = current
	}
	afterByID := make(map[uuid.UUID]pausedomain.PauseReconnectInterval, len(after))
	for _, next := range after {
		if next.ID == uuid.Nil {
			return domain.ErrValidation
		}
		if _, duplicate := afterByID[next.ID]; duplicate {
			return domain.ErrValidation
		}
		afterByID[next.ID] = next
	}
	for id, current := range beforeByID {
		next, ok := afterByID[id]
		if !ok {
			return domain.ErrConflict
		}
		if reflect.DeepEqual(current, next) {
			continue
		}
		if current.State != pausedomain.ReconnectStateOpen || current.Revision == math.MaxInt64 ||
			next.PauseID != current.PauseID || next.RosterID != current.RosterID ||
			next.SeriesID != current.SeriesID || next.GameID != current.GameID ||
			next.ParticipantID != current.ParticipantID || next.PresenceEpoch != current.PresenceEpoch ||
			next.Number != current.Number || next.ContinuationNumber != current.ContinuationNumber ||
			!sameReconnectOptionalUUID(next.ContinuedFromID, current.ContinuedFromID) ||
			!sameReconnectOptionalUUID(next.SuspendedByPauseID, current.SuspendedByPauseID) ||
			next.Revision != current.Revision+1 || next.ClosedAt == nil ||
			!next.UpdatedAt.Equal(*next.ClosedAt) {
			return domain.ErrValidation
		}
		if next.State != pausedomain.ReconnectStateReconnected && next.State != pausedomain.ReconnectStateExpired &&
			next.State != pausedomain.ReconnectStateCancelled {
			return domain.ErrValidation
		}
	}
	for id, next := range afterByID {
		if _, exists := beforeByID[id]; exists {
			continue
		}
		if next.State != pausedomain.ReconnectStateOpen || next.Revision != 1 || next.ClosedAt != nil ||
			next.SuspendedByPauseID != nil || next.PauseID == uuid.Nil || next.RosterID == uuid.Nil ||
			next.SeriesID == uuid.Nil || next.GameID == uuid.Nil || next.ParticipantID == uuid.Nil ||
			next.Number < 1 || next.ContinuationNumber < 0 || next.OpenedAt.IsZero() || next.Deadline.IsZero() ||
			!next.Deadline.After(next.OpenedAt) || !next.UpdatedAt.Equal(next.OpenedAt) {
			return domain.ErrValidation
		}
	}
	return nil
}

func reconnectPresenceByID(values []pausedomain.PausePresence, participantID uuid.UUID) (pausedomain.PausePresence, bool) {
	for _, value := range values {
		if value.ParticipantID == participantID {
			return value, true
		}
	}
	return pausedomain.PausePresence{}, false
}

//nolint:gocyclo // Snapshot derivation validates both participants and pre-pause evidence together.
func reconnectPausePresenceSnapshotStates(
	rows []sqlc.PausePresenceSnapshot,
	pauseID, rosterID, seriesID uuid.UUID,
	first, second pausedomain.PausePresence,
) (string, string, error) {
	if pauseID == uuid.Nil || rosterID == uuid.Nil || seriesID == uuid.Nil ||
		first.ParticipantID == uuid.Nil || second.ParticipantID == uuid.Nil || first.ParticipantID == second.ParticipantID || len(rows) != 2 {
		return "", "", domain.ErrConflict
	}
	expected := map[uuid.UUID]pausedomain.PausePresence{
		first.ParticipantID:  first,
		second.ParticipantID: second,
	}
	states := make(map[uuid.UUID]string, len(rows))
	for _, row := range rows {
		presence, ok := expected[row.ParticipantID]
		if !ok || row.PauseID != pauseID || row.RosterID != rosterID || row.SeriesID != seriesID ||
			row.PresenceEpoch < 1 || row.PresenceRevision < 1 ||
			!reconnectPausePresenceSnapshotMatchesCurrent(row, presence) {
			return "", "", domain.ErrConflict
		}
		if _, duplicate := states[row.ParticipantID]; duplicate {
			return "", "", domain.ErrConflict
		}
		capturedAt, capturedErr := requiredRecoveryTime(row.CapturedAt)
		createdAt, createdErr := requiredRecoveryTime(row.CreatedAt)
		if capturedErr != nil || createdErr != nil || createdAt.Before(capturedAt) {
			return "", "", domain.ErrConflict
		}
		// An unchanged participant may have last updated presence before this
		// pause was captured. Only the participant whose connected snapshot is
		// now disconnected must carry a post-capture live CAS timestamp.
		if row.PresenceState == string(pausedomain.PresenceStateConnected) &&
			presence.State == pausedomain.PresenceStateDisconnected && presence.UpdatedAt.Before(capturedAt) {
			return "", "", domain.ErrConflict
		}
		states[row.ParticipantID] = row.PresenceState
	}
	firstState, firstOK := states[first.ParticipantID]
	secondState, secondOK := states[second.ParticipantID]
	if !firstOK || !secondOK {
		return "", "", domain.ErrConflict
	}
	return firstState, secondState, nil
}

func reconnectPausePresenceSnapshotMatchesCurrent(
	row sqlc.PausePresenceSnapshot,
	current pausedomain.PausePresence,
) bool {
	switch {
	case row.PresenceState == string(current.State):
		return row.PresenceEpoch == current.PresenceEpoch && row.PresenceRevision == current.Revision
	case row.PresenceState == string(pausedomain.PresenceStateConnected) && current.State == pausedomain.PresenceStateDisconnected:
		return row.PresenceEpoch != math.MaxInt64 && row.PresenceRevision != math.MaxInt64 &&
			current.PresenceEpoch == row.PresenceEpoch+1 && current.Revision == row.PresenceRevision+1
	default:
		return false
	}
}

func reconnectCounterByParticipant(values []pausedomain.PauseReconnectCounter, participantID uuid.UUID) (pausedomain.PauseReconnectCounter, bool) {
	for _, value := range values {
		if value.ParticipantID == participantID {
			return value, true
		}
	}
	return pausedomain.PauseReconnectCounter{}, false
}

func reconnectCounterPredecessorTime(at time.Time) (time.Time, bool) {
	if !domain.IsValidServerTime(at) {
		return time.Time{}, false
	}
	predecessor := at.Add(-time.Microsecond)
	if !domain.IsValidServerTime(predecessor) || !predecessor.Before(at) {
		return time.Time{}, false
	}
	return predecessor, true
}

func reconnectResumeRequiresIntervalFirst(
	current, next reconnectusecase.ReconnectAuthority,
) bool {
	return current.Game.State == domain.GameStatePaused && next.Game.State == domain.GameStateActive
}

//nolint:gocyclo // Persistence order is mutation-specific and must remain atomic with its receipt.
func (r *TournamentReconnectPostgres) persistTournamentReconnectMutation(
	ctx context.Context,
	q *sqlc.Queries,
	current reconnectusecase.ReconnectAuthority,
	record reconnectusecase.ReconnectRecord,
) error {
	next := record.ReconnectAuthority
	activePause, pauseErr := q.LockTournamentReconnectGamePause(ctx, sqlc.LockTournamentReconnectGamePauseParams{
		TournamentID: current.Scope.TournamentID, RosterID: current.Scope.RosterID,
		WaveID: current.Scope.WaveID, GameAttemptID: nullableUUIDValue(current.Game.ID),
	})
	hasPause := pauseErr == nil
	if pauseErr != nil && !errors.Is(pauseErr, pgx.ErrNoRows) {
		return fmt.Errorf("load reconnect pause for commit: %w", pauseErr)
	}
	if !hasPause && current.Game.State == domain.GameStateActive && next.Game.State == domain.GameStatePaused && next.Current == nil {
		created, err := createTournamentReconnectPause(ctx, q, current, next, record.RecordedAt)
		if err != nil {
			return err
		}
		activePause = sqlc.LockTournamentReconnectGamePauseRow{Pause: created}
		hasPause = true
	}
	if current.Game.State == domain.GameStatePaused && !hasPause {
		return domain.ErrConflict
	}
	// PostgreSQL's reconnect interval guard validates the old disconnected
	// presence during a reconnected interval CAS, while the deferred terminal
	// guard validates the final connected presence at commit. Keep this order
	// specific to the resume transition. Fresh disconnects must update live
	// presence before inserting an interval so the interval INSERT can consume
	// the new presence epoch; timeout expiry has no live presence transition.
	resuming := reconnectResumeRequiresIntervalFirst(current, next)
	if resuming {
		if err := persistReconnectIntervalsCAS(ctx, q, current, next, hasPause, next.PauseID); err != nil {
			return err
		}
		if err := persistReconnectPresenceCAS(ctx, q, current.Presence, next.Presence); err != nil {
			return err
		}
	} else {
		if err := persistReconnectPresenceCAS(ctx, q, current.Presence, next.Presence); err != nil {
			return err
		}
		if err := persistReconnectIntervalsCAS(ctx, q, current, next, hasPause, next.PauseID); err != nil {
			return err
		}
	}
	if err := persistReconnectCounterCAS(ctx, q, current.Counters, next.Counters, record.RecordedAt, next.PauseID); err != nil {
		return err
	}
	if next.Current == nil {
		if err := persistReconnectLiveState(ctx, q, current, next, activePause, hasPause, record); err != nil {
			return err
		}
		return nil
	}
	if hasPause {
		if err := cancelTournamentReconnectPause(ctx, q, activePause.Pause, commandIDOfReconnectRecord(record), record.RecordedAt); err != nil {
			return err
		}
	}
	if err := r.persistReconnectTerminalSettlement(ctx, q, current, record); err != nil {
		return err
	}
	return nil
}

//nolint:gocyclo // Initial pause creation validates and persists the complete frozen aggregate.
func createTournamentReconnectPause(
	ctx context.Context,
	q *sqlc.Queries,
	current reconnectusecase.ReconnectAuthority,
	next reconnectusecase.ReconnectAuthority,
	startedAt time.Time,
) (sqlc.Pause, error) {
	if current.Game.State != domain.GameStateActive || next.Game.State != domain.GameStatePaused ||
		next.PauseID == uuid.Nil || next.GameClock.FrozenAt.IsZero() || next.GameClock.Remaining <= 0 ||
		next.GameClock.Remaining > time.Duration(math.MaxInt64)*time.Nanosecond {
		return sqlc.Pause{}, domain.ErrValidation
	}
	revisionID := tournamentAdminNormalPauseID(next.PauseID, "revision", next.PauseID)
	created, err := q.CreateTournamentAdminNormalPause(ctx, sqlc.CreateTournamentAdminNormalPauseParams{
		ID: next.PauseID, TournamentID: current.Scope.TournamentID, RosterID: current.Scope.RosterID,
		ScopeKind: "game_attempt", ScopeID: current.Game.ID, WaveID: uuid.NullUUID{},
		SeriesID: nullableUUIDValue(current.Series.ID), GameAttemptID: nullableUUIDValue(current.Game.ID),
		ParentPauseID: uuid.NullUUID{}, Depth: 0, Reason: "disconnect",
		PausedFromState: string(current.Game.State), CurrentRevisionID: revisionID, StartedAt: tstz(startedAt),
	})
	if err != nil || created != next.PauseID {
		return sqlc.Pause{}, normalPauseCAS("create reconnect pause", err)
	}
	if createdRevision, revisionErr := q.CreateTournamentAdminNormalPauseRevision(ctx, sqlc.CreateTournamentAdminNormalPauseRevisionParams{
		ID: revisionID, PauseID: next.PauseID, PreviousRevisionID: uuid.NullUUID{}, RevisionNumber: 1,
		State: string(pauseusecase.PauseStateActive), CreatedAt: tstz(startedAt),
	}); revisionErr != nil || createdRevision != revisionID {
		return sqlc.Pause{}, normalPauseCAS("create reconnect pause revision", revisionErr)
	}
	remainingMs := next.GameClock.Remaining.Milliseconds()
	if remainingMs < 1 {
		return sqlc.Pause{}, domain.ErrValidation
	}
	if createdClock, clockErr := q.CreateTournamentReconnectPauseClock(ctx, sqlc.CreateTournamentReconnectPauseClockParams{
		PauseID: next.PauseID, GameAttemptID: current.Game.ID,
		OriginalDeadline: tstz(next.GameClock.OriginalDeadline), FrozenAt: tstz(next.GameClock.FrozenAt),
		FrozenRemainingMs: remainingMs, Revision: next.GameClock.Revision,
	}); clockErr != nil || createdClock != next.PauseID {
		return sqlc.Pause{}, normalPauseCAS("create reconnect pause clock", clockErr)
	}
	// Presence snapshots are immutable pre-mutation evidence.  The live rows
	// still contain current's CAS values here; persistReconnectPresenceCAS runs
	// after this function and applies next.Presence in the same transaction.
	for _, presence := range current.Presence {
		createdParticipant, snapshotErr := q.CreateTournamentAdminNormalPausePresenceSnapshot(ctx, sqlc.CreateTournamentAdminNormalPausePresenceSnapshotParams{
			PauseID: next.PauseID, RosterID: presence.RosterID, SeriesID: presence.SeriesID,
			ParticipantID: presence.ParticipantID, PresenceState: string(presence.State),
			PresenceEpoch: presence.PresenceEpoch, PresenceRevision: presence.Revision, CapturedAt: tstz(startedAt),
		})
		if snapshotErr != nil || createdParticipant != presence.ParticipantID {
			return sqlc.Pause{}, normalPauseCAS("snapshot reconnect Presence", snapshotErr)
		}
	}
	counterCreatedAt := startedAt
	if len(current.Counters) > 0 {
		var ok bool
		counterCreatedAt, ok = reconnectCounterPredecessorTime(startedAt)
		if !ok {
			return sqlc.Pause{}, domain.ErrValidation
		}
	}
	for _, counter := range current.Counters {
		if counter.Validate() != nil || counter.Limit > math.MaxInt16 || counter.Used > math.MaxInt16 {
			return sqlc.Pause{}, domain.ErrValidation
		}
		createdParticipant, counterErr := q.CreateTournamentAdminNormalPauseCounter(ctx, sqlc.CreateTournamentAdminNormalPauseCounterParams{
			PauseID: counter.PauseID, RosterID: counter.RosterID, ParticipantID: counter.ParticipantID,
			SlotLimit: int16(counter.Limit), SlotsUsed: int16(counter.Used), Revision: counter.Revision, CreatedAt: tstz(counterCreatedAt), //nolint:gosec // Domain validation bounds reconnect slots to the fixed tournament limit.
		})
		if counterErr != nil || createdParticipant != counter.ParticipantID {
			return sqlc.Pause{}, normalPauseCAS("create reconnect counter", counterErr)
		}
	}
	return sqlc.Pause{
		ID: next.PauseID, TournamentID: current.Scope.TournamentID, RosterID: current.Scope.RosterID,
		ScopeKind: "game_attempt", ScopeID: current.Game.ID, SeriesID: nullableUUIDValue(current.Series.ID),
		GameAttemptID: nullableUUIDValue(current.Game.ID), CurrentRevisionID: revisionID, Revision: 1,
		Reason: "disconnect", PausedFromState: string(current.Game.State), State: string(pauseusecase.PauseStateActive),
		StartedAt: tstz(startedAt), CreatedAt: tstz(startedAt), UpdatedAt: tstz(startedAt),
	}, nil
}

func persistReconnectPresenceCAS(
	ctx context.Context,
	q *sqlc.Queries,
	before, after []pausedomain.PausePresence,
) error {
	for _, current := range before {
		next, ok := reconnectPresenceByID(after, current.ParticipantID)
		if !ok || reflect.DeepEqual(current, next) {
			continue
		}
		row, err := q.UpdateTournamentReconnectPresenceCAS(ctx, sqlc.UpdateTournamentReconnectPresenceCASParams{
			NextState: string(next.State), UpdatedAt: tstz(next.UpdatedAt), ID: current.ID,
			TournamentID: current.TournamentID, RosterID: current.RosterID, SeriesID: current.SeriesID,
			ParticipantID: current.ParticipantID, ExpectedState: string(current.State),
			ExpectedEpoch: current.PresenceEpoch, ExpectedRevision: current.Revision,
		})
		if err != nil {
			return resultCASWriteError("update reconnect Presence", err)
		}
		mapped, mapErr := recoveryPresence([]sqlc.PresenceState{row})
		if mapErr != nil || len(mapped) != 1 || !reflect.DeepEqual(mapped[0], next) {
			return domain.ErrInternal
		}
	}
	return nil
}

func persistReconnectCounterCAS(
	ctx context.Context,
	q *sqlc.Queries,
	before, after []pausedomain.PauseReconnectCounter,
	updatedAt time.Time,
	pauseID uuid.UUID,
) error {
	for _, current := range before {
		next, ok := reconnectCounterByParticipant(after, current.ParticipantID)
		if !ok || reflect.DeepEqual(current, next) {
			continue
		}
		if current.Used > math.MaxInt16 || current.Revision == math.MaxInt64 || next.Used > math.MaxInt16 {
			return domain.ErrValidation
		}
		participantID, err := q.AdvanceTournamentAdminNormalPauseCounterCAS(ctx, sqlc.AdvanceTournamentAdminNormalPauseCounterCASParams{
			UpdatedAt: tstz(updatedAt), PauseID: pauseID, ParticipantID: current.ParticipantID,
			ExpectedRevision: current.Revision, ExpectedSlotsUsed: int16(current.Used), //nolint:gosec // Bounds checked above.
		})
		if err != nil || participantID != current.ParticipantID {
			return normalPauseCAS("advance reconnect counter", err)
		}
	}
	return nil
}

//nolint:gocyclo // Append, close, and continuation CAS paths share one interval ownership boundary.
func persistReconnectIntervalsCAS(
	ctx context.Context,
	q *sqlc.Queries,
	current, next reconnectusecase.ReconnectAuthority,
	hasPause bool,
	pauseID uuid.UUID,
) error {
	beforeByID := make(map[uuid.UUID]pausedomain.PauseReconnectInterval, len(current.Reconnect))
	for _, value := range current.Reconnect {
		beforeByID[value.ID] = value
	}
	for _, value := range next.Reconnect {
		before, existed := beforeByID[value.ID]
		if !existed {
			if !hasPause || value.PauseID != pauseID {
				return domain.ErrConflict
			}
			if value.State != pausedomain.ReconnectStateOpen || value.Revision != 1 ||
				value.ClosedAt != nil || value.SuspendedByPauseID != nil {
				return domain.ErrValidation
			}
			if err := value.Validate(); err != nil {
				return domain.ErrValidation
			}
			if value.Number > math.MaxInt16 || value.ContinuationNumber > math.MaxInt32 {
				return domain.ErrValidation
			}
			created, err := q.CreateTournamentAdminNormalPauseReconnectInterval(ctx, sqlc.CreateTournamentAdminNormalPauseReconnectIntervalParams{
				ID: value.ID, PauseID: value.PauseID, RosterID: value.RosterID, SeriesID: value.SeriesID,
				GameAttemptID: value.GameID, ParticipantID: value.ParticipantID, PresenceEpoch: value.PresenceEpoch,
				IntervalNumber: int16(value.Number), OpenedAt: tstz(value.OpenedAt), DeadlineAt: tstz(value.Deadline), //nolint:gosec // Domain validation bounds interval numbers to the reconnect slot limit.
				ContinuationNumber: int32(value.ContinuationNumber), ContinuedFromID: nullableUUID(value.ContinuedFromID), //nolint:gosec // Domain validation bounds continuation numbers before persistence.
			})
			if err != nil || created != value.ID {
				return normalPauseCAS("create reconnect interval", err)
			}
			continue
		}
		if reflect.DeepEqual(before, value) {
			continue
		}
		if before.State != pausedomain.ReconnectStateOpen || before.Revision == math.MaxInt64 ||
			value.Revision != before.Revision+1 || value.ClosedAt == nil || !value.UpdatedAt.Equal(*value.ClosedAt) {
			return domain.ErrValidation
		}
		closed := *value.ClosedAt
		if value.State == pausedomain.ReconnectStateReconnected {
			row, err := q.CloseTournamentReconnectIntervalCAS(ctx, sqlc.CloseTournamentReconnectIntervalCASParams{
				NextState: string(value.State), ClosedAt: tstz(closed), ID: before.ID, PauseID: before.PauseID,
				RosterID: before.RosterID, SeriesID: before.SeriesID, GameAttemptID: before.GameID,
				ParticipantID: before.ParticipantID, PresenceEpoch: before.PresenceEpoch, ExpectedRevision: before.Revision,
			})
			if err != nil {
				return resultCASWriteError("close reconnect interval", err)
			}
			mapped, mapErr := recoveryIntervals([]sqlc.ReconnectInterval{row})
			if mapErr != nil || len(mapped) != 1 || !reflect.DeepEqual(mapped[0], value) {
				return domain.ErrInternal
			}
			continue
		}
		if value.State == pausedomain.ReconnectStateExpired {
			row, err := q.CloseTournamentReconnectIntervalCAS(ctx, sqlc.CloseTournamentReconnectIntervalCASParams{
				NextState: string(value.State), ClosedAt: tstz(closed), ID: before.ID, PauseID: before.PauseID,
				RosterID: before.RosterID, SeriesID: before.SeriesID, GameAttemptID: before.GameID,
				ParticipantID: before.ParticipantID, PresenceEpoch: before.PresenceEpoch, ExpectedRevision: before.Revision,
			})
			if err != nil {
				return resultCASWriteError("expire reconnect interval", err)
			}
			mapped, mapErr := recoveryIntervals([]sqlc.ReconnectInterval{row})
			if mapErr != nil || len(mapped) != 1 || !reflect.DeepEqual(mapped[0], value) {
				return domain.ErrInternal
			}
			continue
		}
		if value.State != pausedomain.ReconnectStateCancelled {
			return domain.ErrValidation
		}
		row, err := q.CancelTournamentReconnectIntervalCAS(ctx, sqlc.CancelTournamentReconnectIntervalCASParams{
			ClosedAt: tstz(closed), ID: before.ID, PauseID: before.PauseID, RosterID: before.RosterID,
			SeriesID: before.SeriesID, GameAttemptID: before.GameID, ParticipantID: before.ParticipantID,
			PresenceEpoch: before.PresenceEpoch, ExpectedRevision: before.Revision,
		})
		if err != nil {
			return resultCASWriteError("cancel reconnect interval", err)
		}
		mapped, mapErr := recoveryIntervals([]sqlc.ReconnectInterval{row})
		if mapErr != nil || len(mapped) != 1 || !reflect.DeepEqual(mapped[0], value) {
			return domain.ErrInternal
		}
	}
	return nil
}

func persistReconnectLiveState(
	ctx context.Context,
	q *sqlc.Queries,
	current, next reconnectusecase.ReconnectAuthority,
	activePause sqlc.LockTournamentReconnectGamePauseRow,
	hasPause bool,
	record reconnectusecase.ReconnectRecord,
) error {
	if current.Game.State == next.Game.State {
		return nil
	}
	if current.Game.State == domain.GameStateActive && next.Game.State == domain.GameStatePaused {
		if !hasPause || activePause.Pause.ID != next.PauseID || next.GameClock.FrozenAt.IsZero() {
			return domain.ErrConflict
		}
		updated, err := q.PauseTournamentAdminNormalGameCAS(ctx, sqlc.PauseTournamentAdminNormalGameCASParams{
			PausedAt: tstz(record.RecordedAt), ID: current.Game.ID, ExpectedRevision: current.GameRevision,
		})
		if err != nil || updated != current.Game.ID {
			return normalPauseCAS("pause reconnect game", err)
		}
		return advanceReconnectSeriesCAS(ctx, q, current, record.RecordedAt)
	}
	if current.Game.State == domain.GameStatePaused && next.Game.State == domain.GameStateActive {
		if !hasPause || activePause.Pause.ID != next.PauseID {
			return fmt.Errorf("resume reconnect pause authority mismatch: %w", domain.ErrConflict)
		}
		if err := resumeTournamentReconnectGame(ctx, q, current, next, activePause.Pause, record); err != nil {
			return err
		}
		return nil
	}
	return domain.ErrValidation
}

//nolint:gocyclo // Resume commits clock, presence, game, and pause cancellation as one CAS transition.
func resumeTournamentReconnectGame(
	ctx context.Context,
	q *sqlc.Queries,
	current, next reconnectusecase.ReconnectAuthority,
	pauseRow sqlc.Pause,
	record reconnectusecase.ReconnectRecord,
) error {
	clockRow, err := q.LockTournamentReconnectPauseClock(ctx, sqlc.LockTournamentReconnectPauseClockParams{
		TournamentID: current.Scope.TournamentID, RosterID: current.Scope.RosterID,
		WaveID: current.Scope.WaveID, GameAttemptID: nullableUUIDValue(current.Game.ID), PauseID: pauseRow.ID,
	})
	if err != nil {
		return resultCASWriteError("load reconnect pause clock", err)
	}
	clock, err := recoveryGameClock(clockRow.PauseClock)
	if err != nil || clock.Revision != current.GameClock.Revision ||
		next.GameClock.ResumedAt == nil || next.GameClock.ResumedDeadline == nil {
		return fmt.Errorf("resume reconnect clock authority mismatch: durable revision=%d current revision=%d: %w", clock.Revision, current.GameClock.Revision, domain.ErrConflict)
	}
	first, firstOK := reconnectPresenceByID(current.Presence, current.Series.FirstParticipantID)
	second, secondOK := reconnectPresenceByID(current.Presence, current.Series.SecondParticipantID)
	firstLive, firstLiveOK := reconnectPresenceByID(next.Presence, current.Series.FirstParticipantID)
	secondLive, secondLiveOK := reconnectPresenceByID(next.Presence, current.Series.SecondParticipantID)
	if !firstOK || !secondOK || !firstLiveOK || !secondLiveOK ||
		firstLive.State != pausedomain.PresenceStateConnected || secondLive.State != pausedomain.PresenceStateConnected {
		return fmt.Errorf("resume reconnect presence authority mismatch: %w", domain.ErrConflict)
	}
	snapshotRows, err := q.LockTournamentReconnectPausePresenceSnapshots(ctx, sqlc.LockTournamentReconnectPausePresenceSnapshotsParams{
		PauseID: pauseRow.ID, RosterID: current.Scope.RosterID, SeriesID: current.Series.ID,
	})
	if err != nil {
		return resultCASWriteError("load reconnect pause presence snapshots", err)
	}
	firstPrePauseState, secondPrePauseState, snapshotErr := reconnectPausePresenceSnapshotStates(
		snapshotRows, pauseRow.ID, current.Scope.RosterID, current.Series.ID, first, second,
	)
	if snapshotErr != nil {
		return snapshotErr
	}
	decisionNumber, err := q.GetTournamentAdminNormalPauseDecisionNumber(ctx, pauseRow.ID)
	if err != nil {
		return fmt.Errorf("load reconnect resume decision number: %w", err)
	}
	if decisionNumber == math.MaxInt64 {
		return domain.ErrValidation
	}
	decisionID := tournamentAdminNormalPauseID(commandIDOfReconnectRecord(record), "resume-decision", pauseRow.ID)
	created, err := q.CreateTournamentAdminNormalResumeDecision(ctx, sqlc.CreateTournamentAdminNormalResumeDecisionParams{
		ID: decisionID, PauseID: pauseRow.ID, DecisionNumber: decisionNumber + 1,
		FirstParticipantID: firstLive.ParticipantID, SecondParticipantID: secondLive.ParticipantID,
		FirstPrePauseState: firstPrePauseState, SecondPrePauseState: secondPrePauseState,
		FirstLiveState: string(firstLive.State), SecondLiveState: string(secondLive.State),
		FirstPresenceEpoch: firstLive.PresenceEpoch, SecondPresenceEpoch: secondLive.PresenceEpoch,
		FirstPresenceRevision: firstLive.Revision, SecondPresenceRevision: secondLive.Revision,
		FirstReconnectIntervalID: uuid.NullUUID{}, SecondReconnectIntervalID: uuid.NullUUID{},
		Action: string(pauseusecase.PauseResumeActionResume), DecidedAt: tstz(record.RecordedAt),
	})
	if err != nil || created != decisionID {
		return normalPauseCAS("create reconnect resume decision", err)
	}
	resumedAt := *next.GameClock.ResumedAt
	resumedDeadline, deadlineErr := reconnectCanonicalResumedDeadline(
		resumedAt, *next.GameClock.ResumedDeadline, clockRow.PauseClock.FrozenRemainingMs,
	)
	if deadlineErr != nil {
		return fmt.Errorf("resume reconnect clock authority mismatch: %w", domain.ErrConflict)
	}
	clockID, err := q.ResumeTournamentAdminNormalPauseClockCAS(ctx, sqlc.ResumeTournamentAdminNormalPauseClockCASParams{
		ResumedAt: tstz(resumedAt), ResumedDeadline: tstz(resumedDeadline), PauseID: pauseRow.ID,
		ExpectedRevision: clock.Revision,
	})
	if err != nil || clockID != pauseRow.ID {
		return normalPauseCAS("resume reconnect pause clock", err)
	}
	gameID, err := q.ResumeTournamentAdminNormalGameCAS(ctx, sqlc.ResumeTournamentAdminNormalGameCASParams{
		ResumedAt: tstz(record.RecordedAt), ID: current.Game.ID, ExpectedRevision: current.GameRevision,
	})
	if err != nil || gameID != current.Game.ID {
		return normalPauseCAS("resume reconnect game", err)
	}
	if err := advanceReconnectSeriesCAS(ctx, q, current, record.RecordedAt); err != nil {
		return err
	}
	newRevisionID := tournamentAdminNormalPauseID(commandIDOfReconnectRecord(record), "resume-revision", pauseRow.ID)
	reason := "reconnect_resume"
	createdRevision, err := q.CreateTournamentAdminNormalPauseRevision(ctx, sqlc.CreateTournamentAdminNormalPauseRevisionParams{
		ID: newRevisionID, PauseID: pauseRow.ID, PreviousRevisionID: nullableUUIDValue(pauseRow.CurrentRevisionID),
		RevisionNumber: pauseRow.Revision + 1, State: string(pauseusecase.PauseStateResumed),
		TransitionReason: &reason, CreatedAt: tstz(record.RecordedAt),
	})
	if err != nil || createdRevision != newRevisionID {
		return normalPauseCAS("create reconnect resume revision", err)
	}
	updatedPause, err := q.ResumeTournamentAdminNormalPauseCAS(ctx, sqlc.ResumeTournamentAdminNormalPauseCASParams{
		CurrentRevisionID: newRevisionID, ResumedAt: tstz(record.RecordedAt), ID: pauseRow.ID,
		ExpectedRevision: pauseRow.Revision, ExpectedRevisionID: pauseRow.CurrentRevisionID,
	})
	if err != nil || updatedPause != pauseRow.ID {
		return normalPauseCAS("resume reconnect pause", err)
	}
	return nil
}

func advanceReconnectSeriesCAS(ctx context.Context, q *sqlc.Queries, current reconnectusecase.ReconnectAuthority, updatedAt time.Time) error {
	if current.SeriesRevision == math.MaxInt64 {
		return domain.ErrValidation
	}
	updated, err := q.AdvanceTournamentReconnectSeriesCAS(ctx, sqlc.AdvanceTournamentReconnectSeriesCASParams{
		UpdatedAt: tstz(updatedAt), SeriesID: current.Series.ID, TournamentID: current.Scope.TournamentID,
		RosterID: current.Scope.RosterID, ExpectedRevision: current.SeriesRevision,
		ExpectedState: string(current.Series.State),
	})
	if err != nil || updated != current.Series.ID {
		return resultCASWriteError("advance reconnect Series", err)
	}
	return nil
}

func cancelTournamentReconnectPause(
	ctx context.Context,
	q *sqlc.Queries,
	pauseRow sqlc.Pause,
	commandID uuid.UUID,
	resolvedAt time.Time,
) error {
	if pauseRow.ID == uuid.Nil || pauseRow.CurrentRevisionID == uuid.Nil || pauseRow.Revision < 1 {
		return domain.ErrConflict
	}
	revisionID := tournamentAdminNormalPauseID(commandID, "cancel-revision", pauseRow.ID)
	if created, err := q.CreateRecoveryPauseRevision(ctx, sqlc.CreateRecoveryPauseRevisionParams{
		ID: revisionID, PauseID: pauseRow.ID, PreviousRevisionID: nullableUUIDValue(pauseRow.CurrentRevisionID),
		RevisionNumber: pauseRow.Revision + 1, CreatedAt: tstz(resolvedAt),
	}); err != nil || created.ID != revisionID {
		return resultCASWriteError("create reconnect cancellation revision", err)
	}
	updated, err := q.CancelRecoveryPauseCAS(ctx, sqlc.CancelRecoveryPauseCASParams{
		CurrentRevisionID: revisionID, ResolvedAt: tstz(resolvedAt), PauseID: pauseRow.ID,
		ExpectedRevisionID: pauseRow.CurrentRevisionID, ExpectedRevision: pauseRow.Revision,
	})
	if err != nil || updated.ID != pauseRow.ID {
		return resultCASWriteError("cancel reconnect pause", err)
	}
	return nil
}

func (r *TournamentReconnectPostgres) persistReconnectTerminalSettlement(
	ctx context.Context,
	q *sqlc.Queries,
	current reconnectusecase.ReconnectAuthority,
	record reconnectusecase.ReconnectRecord,
) error {
	input, err := reconnectSettlementInput(current, record)
	if err != nil {
		return err
	}
	_, changed, err := resultrepo.NewResultPostgresWithFinalizer(r.tx, r.resultFinalizer).Settle(ctx, input)
	if err != nil {
		return err
	}
	if !changed {
		return domain.ErrConflict
	}
	if record.ReplayRoute != nil {
		route := record.ReplayRoute
		if _, err := q.CreateRecoveryWaveMemberRoute(ctx, sqlc.CreateRecoveryWaveMemberRouteParams{
			ID: route.ID, TournamentID: current.Scope.TournamentID, RosterID: current.Scope.RosterID,
			WaveID: route.WaveID, SeriesID: route.SeriesID, SlotID: route.SlotID,
			GameAttemptID: route.GameID, Category: string(route.Category), RoutedAt: tstz(route.RoutedAt),
		}); err != nil {
			return mapRepositoryWriteError("create reconnect replay route", err)
		}
	}
	return nil
}

func reconnectSettlementInput(
	current reconnectusecase.ReconnectAuthority,
	record reconnectusecase.ReconnectRecord,
) (ResultSettlementInput, error) {
	if record.ReconnectAuthority.Current == nil || record.ScoreRevision == nil || record.Evidence == nil ||
		len(record.ScoreRevision.GameResultRevisionIDs) == 0 {
		return ResultSettlementInput{}, domain.ErrValidation
	}
	commandID := commandIDOfReconnectRecord(record)
	if commandID == uuid.Nil {
		return ResultSettlementInput{}, domain.ErrValidation
	}
	gameState := record.ReconnectAuthority.Game.State
	gameReason := record.ReconnectAuthority.Game.ResultReason
	gameWinner := record.ReconnectAuthority.Game.WinnerID
	var gameResultID uuid.UUID
	switch {
	case record.GameResultRevision != nil:
		gameResultID = record.GameResultRevision.ID.UUID()
	case record.VoidGameResultRevision != nil:
		gameResultID = record.VoidGameResultRevision.ID.UUID()
	default:
		return ResultSettlementInput{}, domain.ErrValidation
	}
	seriesResultID := uuid.Nil
	seriesResultReason := ""
	artifactKinds := reconnectSettlementArtifactKinds(record)
	if record.SeriesResultRevision != nil {
		seriesResultID = record.SeriesResultRevision.ID.UUID()
		seriesResultReason = "score_complete"
	}
	digest, err := recoveryPayloadDigest("reconnect", map[string]any{
		"command_id": commandID.String(), "game_id": current.Game.ID.String(),
		"state": string(gameState), "reason": string(gameReason),
	})
	if err != nil {
		return ResultSettlementInput{}, err
	}
	return ResultSettlementInput{
		IDs: ResultSettlementIDs{
			CommitID:                  derivedReconnectResultID(commandID, "commit"),
			ResultEventID:             derivedReconnectResultID(commandID, "result-event"),
			ResultEventIdempotencyKey: derivedReconnectResultID(commandID, "result-event-idempotency"),
			GameResultRevisionID:      gameResultID, SeriesScoreRevisionID: record.ScoreRevision.ID.UUID(),
			SeriesResultRevisionID: seriesResultID, AuditEventID: record.Evidence.AuditEventID,
			OutboxEventID: record.Evidence.OutboxEventID, OutboxIdempotencyKey: derivedReconnectResultID(commandID, "outbox-idempotency"),
			ProjectionEvidenceID: record.Evidence.ProjectionRevisionID,
			CommitIdempotencyKey: derivedReconnectResultID(commandID, "commit-idempotency"),
		},
		Scope: ResultScope{TournamentID: current.Scope.TournamentID, RosterID: current.Scope.RosterID,
			SeriesID: current.Series.ID, AttemptID: current.Game.ID},
		GameState: gameState, GameReason: gameReason, GameWinnerID: gameWinner,
		Score: record.ScoreRevision.ScoreAfter, NextSeriesState: record.ReconnectAuthority.Series.State,
		SeriesResultReason: seriesResultReason, SeriesWinnerID: record.ReconnectAuthority.Series.WinnerID,
		ActorKind: resultActorServer, ProjectionArtifactKinds: artifactKinds,
		ProjectionPayloadDigest: digest, SettledAt: record.RecordedAt,
		ExpectedAttemptRevision: current.GameRevision, ExpectedAttemptState: current.Game.State,
		ExpectedSeriesRevision: current.SeriesRevision, ExpectedSeriesState: current.Series.State,
	}, nil
}

func reconnectSettlementArtifactKinds(record reconnectusecase.ReconnectRecord) []domain.ArtifactKind {
	kinds := []domain.ArtifactKind{domain.ArtifactKindGameResult, domain.ArtifactKindSeriesScore}
	if record.SeriesResultRevision != nil {
		kinds = append(kinds, domain.ArtifactKindStandings, domain.ArtifactKindSeriesResult)
	}
	return kinds
}

func derivedReconnectResultID(commandID uuid.UUID, label string) uuid.UUID {
	return uuid.NewSHA1(commandID, []byte("reconnect-result:"+label))
}

func derivedReconnectOutboxID(commandID uuid.UUID, label string) uuid.UUID {
	return uuid.NewSHA1(commandID, []byte("reconnect-outbox:"+label))
}
