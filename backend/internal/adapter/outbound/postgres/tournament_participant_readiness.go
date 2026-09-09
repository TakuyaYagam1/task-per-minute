package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/readiness"
)

type ParticipantReadinessRepository struct {
	tx    *TxManager
	waves *WavePostgres
}

func NewParticipantReadinessRepository(
	tx *TxManager,
	waves *WavePostgres,
) *ParticipantReadinessRepository {
	return &ParticipantReadinessRepository{tx: tx, waves: waves}
}

func (r *ParticipantReadinessRepository) LoadReadinessAuthority(
	ctx context.Context,
	scope readiness.ReadinessScope,
) (readiness.ReadinessAuthority, error) {
	if ctx == nil || r == nil || r.tx == nil || r.waves == nil ||
		scope.WaveID == uuid.Nil || scope.WindowID == uuid.Nil {
		return readiness.ReadinessAuthority{}, domain.ErrValidation
	}

	querier := r.tx.Querier(ctx)
	window, err := querier.GetReadyWindow(ctx, scope.WaveID)
	if err != nil {
		return readiness.ReadinessAuthority{}, participantReadinessLookupError("ready window", err)
	}
	if window.ID != scope.WindowID || window.RosterID == uuid.Nil {
		return readiness.ReadinessAuthority{}, readiness.ErrReadinessAuthorityConflict
	}
	roster, err := querier.GetTournamentRoster(ctx, window.RosterID)
	if err != nil {
		return readiness.ReadinessAuthority{}, participantReadinessLookupError("roster", err)
	}
	wave, err := r.waves.Get(ctx, roster.TournamentID, scope.WaveID)
	if err != nil {
		return readiness.ReadinessAuthority{}, participantReadinessLookupError("wave", err)
	}
	eventRows, err := querier.ListParticipantReadinessEvents(
		ctx,
		sqlc.ListParticipantReadinessEventsParams{WaveID: scope.WaveID, ReadyWindowID: scope.WindowID},
	)
	if err != nil {
		return readiness.ReadinessAuthority{}, fmt.Errorf(
			"ParticipantReadinessRepository - list events: %w",
			err,
		)
	}
	events := make([]readiness.ReadinessEvent, len(eventRows))
	for index, event := range eventRows {
		events[index] = participantReadinessEvent(event)
	}
	return readiness.ReadinessAuthority{
		Scope: scope, Revision: wave.Revision, Wave: wave.Wave, Events: events,
	}, nil
}

func (r *ParticipantReadinessRepository) CommitReadiness(
	ctx context.Context,
	commit readiness.ReadinessCommit,
) (*readiness.ReadinessRecord, bool, error) {
	if ctx == nil || r == nil || r.tx == nil || r.waves == nil ||
		!validParticipantReadinessCommit(commit) {
		return nil, false, domain.ErrValidation
	}

	var result *readiness.ReadinessRecord
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
	commit readiness.ReadinessCommit,
) (*readiness.ReadinessRecord, bool, error) {
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
	record := readiness.ReadinessRecord(loaded)
	return &record, true, nil
}

func participantReadinessCommand(
	current readiness.ReadinessAuthority,
	event readiness.ReadinessEvent,
) (*readiness.ReadinessRecord, bool, error) {
	for _, stored := range current.Events {
		if stored.CommandID != event.CommandID {
			continue
		}
		if stored != event {
			return nil, true, readiness.ErrReadinessAuthorityConflict
		}
		record := readiness.ReadinessRecord(current)
		return &record, true, nil
	}
	return nil, false, nil
}

func (r *ParticipantReadinessRepository) writeParticipantReadiness(
	ctx context.Context,
	commit readiness.ReadinessCommit,
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
	wave, err := querier.SetParticipantWaveReadiness(
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
	if wave.Revision != commit.ExpectedRevision+1 {
		return domain.ErrInternal
	}
	return createParticipantReadinessEvent(ctx, querier, state.RosterID, wave.Revision, commit)
}

func setParticipantReadinessHead(
	ctx context.Context,
	querier *sqlc.Queries,
	state sqlc.LockParticipantReadinessStateRow,
	commit readiness.ReadinessCommit,
) error {
	_, err := querier.SetParticipantReadinessHead(
		ctx,
		sqlc.SetParticipantReadinessHeadParams{
			Ready:      commit.Event.Type == readiness.ReadinessEventReady,
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
	commit readiness.ReadinessCommit,
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

func validParticipantReadinessCommit(commit readiness.ReadinessCommit) bool {
	if commit.Scope.WaveID == uuid.Nil || commit.Scope.WindowID == uuid.Nil ||
		commit.ExpectedRevision < 1 || commit.ExpectedWaveRevisionID.IsZero() ||
		commit.ExpectedWindowRevisionID.IsZero() || commit.Event.CommandID == uuid.Nil ||
		commit.Event.Scope != commit.Scope || commit.Event.ParticipantID == uuid.Nil ||
		!domain.IsValidServerTime(commit.Event.OccurredAt) || commit.Wave.Validate() != nil ||
		commit.Wave.ID != commit.Scope.WaveID || commit.Wave.ReadyWindow == nil ||
		commit.Wave.ReadyWindow.ID != commit.Scope.WindowID {
		return false
	}
	return commit.Event.Type == readiness.ReadinessEventReady ||
		commit.Event.Type == readiness.ReadinessEventCleared
}

func participantReadinessAuthorityMatches(
	state sqlc.LockParticipantReadinessStateRow,
	commit readiness.ReadinessCommit,
) bool {
	return state.TournamentID == commit.Wave.TournamentID &&
		state.WaveRevisionID == commit.ExpectedWaveRevisionID.UUID() &&
		state.WaveRevision == commit.ExpectedRevision &&
		state.ReadyWindowRevisionID == commit.ExpectedWindowRevisionID.UUID() &&
		state.ReadyWindowState == string(domain.ReadyWindowStateOpen) &&
		(state.WaveState == string(domain.WaveStateReadyWindowOpen) ||
			state.WaveState == string(domain.WaveStateReady))
}

func participantReadinessEvent(row sqlc.ReadinessEvent) readiness.ReadinessEvent {
	return readiness.ReadinessEvent{
		CommandID:     row.CommandID,
		Scope:         readiness.ReadinessScope{WaveID: row.WaveID, WindowID: row.ReadyWindowID},
		ParticipantID: row.ParticipantID,
		Type:          readiness.ReadinessEventType(row.EventType),
		OccurredAt:    row.OccurredAt.Time.UTC(),
	}
}

func participantReadinessLookupError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, ErrWaveNotFound) {
		return readiness.ErrReadinessAuthorityConflict
	}
	return fmt.Errorf("ParticipantReadinessRepository - %s: %w", operation, err)
}

func participantReadinessConflict(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrConflict
	}
	return mapRepositoryWriteError("ParticipantReadinessRepository - commit", err)
}

var _ readiness.ReadinessRepository = (*ParticipantReadinessRepository)(nil)
