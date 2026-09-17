package readiness

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/execution/wave"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	readinessusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/readiness"
)

const (
	pgUniqueViolation     = "23505"
	pgForeignKeyViolation = "23503"
)

type waveReader interface {
	Get(context.Context, uuid.UUID, uuid.UUID) (*wave.WaveRecord, error)
}

type ParticipantReadinessRepository struct {
	tx    *db.TxManager
	waves waveReader
}

func NewParticipantReadinessRepository(
	tx *db.TxManager,
	waves *wave.WavePostgres,
) *ParticipantReadinessRepository {
	if waves == nil {
		return &ParticipantReadinessRepository{tx: tx}
	}
	return newParticipantReadinessRepository(tx, waves)
}

func newParticipantReadinessRepository(tx *db.TxManager, waves waveReader) *ParticipantReadinessRepository {
	return &ParticipantReadinessRepository{tx: tx, waves: waves}
}

func (r *ParticipantReadinessRepository) LoadReadinessAuthority(
	ctx context.Context,
	scope readinessusecase.ReadinessScope,
) (readinessusecase.ReadinessAuthority, error) {
	if ctx == nil || r == nil || r.tx == nil || r.waves == nil ||
		scope.WaveID == uuid.Nil || scope.WindowID == uuid.Nil {
		return readinessusecase.ReadinessAuthority{}, domain.ErrValidation
	}

	querier := r.tx.Querier(ctx)
	window, err := querier.GetReadyWindow(ctx, scope.WaveID)
	if err != nil {
		return readinessusecase.ReadinessAuthority{}, participantReadinessLookupError("ready window", err)
	}
	if window.ID != scope.WindowID || window.RosterID == uuid.Nil {
		return readinessusecase.ReadinessAuthority{}, readinessusecase.ErrReadinessAuthorityConflict
	}
	roster, err := querier.GetTournamentRoster(ctx, window.RosterID)
	if err != nil {
		return readinessusecase.ReadinessAuthority{}, participantReadinessLookupError("roster", err)
	}
	waveRecord, err := r.waves.Get(ctx, roster.TournamentID, scope.WaveID)
	if err != nil {
		return readinessusecase.ReadinessAuthority{}, participantReadinessLookupError("wave", err)
	}
	eventRows, err := querier.ListParticipantReadinessEvents(
		ctx,
		sqlc.ListParticipantReadinessEventsParams{WaveID: scope.WaveID, ReadyWindowID: scope.WindowID},
	)
	if err != nil {
		return readinessusecase.ReadinessAuthority{}, fmt.Errorf(
			"ParticipantReadinessRepository - list events: %w",
			err,
		)
	}
	events := make([]readinessusecase.ReadinessEvent, len(eventRows))
	for index, event := range eventRows {
		events[index] = participantReadinessEvent(event)
	}
	return readinessusecase.ReadinessAuthority{
		Scope: scope, Revision: waveRecord.Revision, Wave: waveRecord.Wave, Events: events,
	}, nil
}

func (r *ParticipantReadinessRepository) CommitReadiness(
	ctx context.Context,
	commit readinessusecase.ReadinessCommit,
) (*readinessusecase.ReadinessRecord, bool, error) {
	if ctx == nil || r == nil || r.tx == nil || r.waves == nil ||
		!validParticipantReadinessCommit(commit) {
		return nil, false, domain.ErrValidation
	}

	var result *readinessusecase.ReadinessRecord
	changed := false
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		var err error
		result, changed, err = r.commitParticipantReadiness(txCtx, commit)
		return err
	})
	if err != nil {
		return nil, false, err
	}
	if result == nil {
		return nil, false, domain.ErrInternal
	}
	return result, changed, nil
}

func (r *ParticipantReadinessRepository) commitParticipantReadiness(
	ctx context.Context,
	commit readinessusecase.ReadinessCommit,
) (*readinessusecase.ReadinessRecord, bool, error) {
	current, err := r.LoadReadinessAuthority(ctx, commit.Scope)
	if err != nil {
		return nil, false, err
	}
	if existing, found, err := participantReadinessCommand(current, commit.Event); found || err != nil {
		return existing, false, err
	}
	if err := r.writeParticipantReadiness(ctx, commit); err != nil {
		return nil, false, err
	}
	loaded, err := r.LoadReadinessAuthority(ctx, commit.Scope)
	if err != nil {
		return nil, false, err
	}
	record := readinessusecase.ReadinessRecord(loaded)
	return &record, true, nil
}

func participantReadinessCommand(
	current readinessusecase.ReadinessAuthority,
	event readinessusecase.ReadinessEvent,
) (*readinessusecase.ReadinessRecord, bool, error) {
	for _, stored := range current.Events {
		if stored.CommandID != event.CommandID {
			continue
		}
		if stored != event {
			return nil, true, readinessusecase.ErrReadinessAuthorityConflict
		}
		record := readinessusecase.ReadinessRecord(current)
		return &record, true, nil
	}
	return nil, false, nil
}

func (r *ParticipantReadinessRepository) writeParticipantReadiness(
	ctx context.Context,
	commit readinessusecase.ReadinessCommit,
) error {
	querier := r.tx.Querier(ctx)
	state, err := querier.LockParticipantReadinessState(
		ctx,
		sqlc.LockParticipantReadinessStateParams{
			ReadyWindowID: commit.Scope.WindowID,
			ParticipantID: commit.Event.ParticipantID,
			WaveID:        commit.Scope.WaveID,
		},
	)
	if err != nil {
		return participantReadinessConflict(err)
	}
	if !participantReadinessAuthorityMatches(state, commit) {
		return domain.ErrConflict
	}
	if err := setParticipantReadinessHead(ctx, querier, state, commit); err != nil {
		return err
	}
	waveRow, err := querier.SetParticipantWaveReadiness(
		ctx,
		sqlc.SetParticipantWaveReadinessParams{
			NextState: string(commit.Wave.State), OccurredAt: tstz(commit.Event.OccurredAt),
			WaveID:                 commit.Scope.WaveID,
			ExpectedWaveRevisionID: commit.ExpectedWaveRevisionID.UUID(),
			ExpectedRevision:       commit.ExpectedRevision,
		},
	)
	if err != nil {
		return participantReadinessConflict(err)
	}
	if waveRow.Revision != commit.ExpectedRevision+1 {
		return domain.ErrInternal
	}
	return createParticipantReadinessEvent(ctx, querier, state.RosterID, waveRow.Revision, commit)
}

func setParticipantReadinessHead(
	ctx context.Context,
	querier *sqlc.Queries,
	state sqlc.LockParticipantReadinessStateRow,
	commit readinessusecase.ReadinessCommit,
) error {
	_, err := querier.SetParticipantReadinessHead(
		ctx,
		sqlc.SetParticipantReadinessHeadParams{
			Ready:      commit.Event.Type == readinessusecase.ReadinessEventReady,
			OccurredAt: tstz(commit.Event.OccurredAt),
			WaveID:     commit.Scope.WaveID, ReadyWindowID: nullableUUIDValue(commit.Scope.WindowID),
			ParticipantID:    commit.Event.ParticipantID,
			ExpectedRevision: state.ParticipantReadinessRevision,
		},
	)
	if err != nil {
		return participantReadinessConflict(err)
	}
	return nil
}

func createParticipantReadinessEvent(
	ctx context.Context,
	querier *sqlc.Queries,
	rosterID uuid.UUID,
	revision int64,
	commit readinessusecase.ReadinessCommit,
) error {
	_, err := querier.CreateParticipantReadinessEvent(
		ctx,
		sqlc.CreateParticipantReadinessEventParams{
			CommandID: commit.Event.CommandID, WaveID: commit.Scope.WaveID,
			ReadyWindowID: commit.Scope.WindowID, RosterID: rosterID,
			ParticipantID: commit.Event.ParticipantID, ReadinessRevision: revision,
			EventType: string(commit.Event.Type), OccurredAt: tstz(commit.Event.OccurredAt),
		},
	)
	if err != nil {
		return participantReadinessConflict(err)
	}
	return nil
}

func validParticipantReadinessCommit(commit readinessusecase.ReadinessCommit) bool {
	if commit.Scope.WaveID == uuid.Nil || commit.Scope.WindowID == uuid.Nil ||
		commit.ExpectedRevision < 1 || commit.ExpectedWaveRevisionID.IsZero() ||
		commit.ExpectedWindowRevisionID.IsZero() || commit.Event.CommandID == uuid.Nil ||
		commit.Event.Scope != commit.Scope || commit.Event.ParticipantID == uuid.Nil ||
		!domain.IsValidServerTime(commit.Event.OccurredAt) || commit.Wave.Validate() != nil ||
		commit.Wave.ID != commit.Scope.WaveID || commit.Wave.ReadyWindow == nil ||
		commit.Wave.ReadyWindow.ID != commit.Scope.WindowID {
		return false
	}
	return commit.Event.Type == readinessusecase.ReadinessEventReady ||
		commit.Event.Type == readinessusecase.ReadinessEventCleared
}

func participantReadinessAuthorityMatches(
	state sqlc.LockParticipantReadinessStateRow,
	commit readinessusecase.ReadinessCommit,
) bool {
	return state.TournamentID == commit.Wave.TournamentID &&
		state.WaveRevisionID == commit.ExpectedWaveRevisionID.UUID() &&
		state.WaveRevision == commit.ExpectedRevision &&
		state.ReadyWindowRevisionID == commit.ExpectedWindowRevisionID.UUID() &&
		state.ReadyWindowState == string(domain.ReadyWindowStateOpen) &&
		(state.WaveState == string(domain.WaveStateReadyWindowOpen) ||
			state.WaveState == string(domain.WaveStateReady))
}

func participantReadinessEvent(row sqlc.ReadinessEvent) readinessusecase.ReadinessEvent {
	return readinessusecase.ReadinessEvent{
		CommandID:     row.CommandID,
		Scope:         readinessusecase.ReadinessScope{WaveID: row.WaveID, WindowID: row.ReadyWindowID},
		ParticipantID: row.ParticipantID,
		Type:          readinessusecase.ReadinessEventType(row.EventType),
		OccurredAt:    row.OccurredAt.Time.UTC(),
	}
}

func participantReadinessLookupError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, wave.ErrWaveNotFound) {
		return readinessusecase.ErrReadinessAuthorityConflict
	}
	return fmt.Errorf("ParticipantReadinessRepository - %s: %w", operation, err)
}

func participantReadinessConflict(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrConflict
	}
	return mapRepositoryWriteError("ParticipantReadinessRepository - commit", err)
}

func nullableUUIDValue(value uuid.UUID) uuid.NullUUID {
	return uuid.NullUUID{UUID: value, Valid: value != uuid.Nil}
}

func tstz(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}

func mapRepositoryWriteError(operation string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case pgUniqueViolation, pgForeignKeyViolation, "23514", "40001":
			return domain.WrapError(err, domain.ErrConflict)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}

var _ waveReader = (*wave.WavePostgres)(nil)
var _ readinessusecase.ReadinessRepository = (*ParticipantReadinessRepository)(nil)
