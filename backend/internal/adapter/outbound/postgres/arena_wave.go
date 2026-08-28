package postgres

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var ErrArenaWaveNotFound = errors.New("arena wave repository: wave not found")

type ArenaWavePostgres struct {
	tx *TxManager
}

type ArenaWaveCreateInput struct {
	ID             uuid.UUID
	TournamentID   uuid.UUID
	RosterID       uuid.UUID
	RevisionID     domain.ArenaWaveRevisionID
	ReplacesWaveID *uuid.UUID
	ParticipantIDs []uuid.UUID
	CreatedAt      time.Time
}

type ArenaReadyWindowInput struct {
	ID         uuid.UUID
	RevisionID domain.ArenaReadyWindowRevisionID
	OpenedAt   time.Time
	Deadline   time.Time
}

type ArenaWaveRecord struct {
	Wave           domain.ArenaWave
	RosterID       uuid.UUID
	ReplacesWaveID *uuid.UUID
	Revision       int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
	ClosedAt       *time.Time
	ReadyAt        map[uuid.UUID]time.Time
}

func NewArenaWavePostgres(tx *TxManager) *ArenaWavePostgres {
	return &ArenaWavePostgres{tx: tx}
}

func (r *ArenaWavePostgres) Create(
	ctx context.Context,
	in ArenaWaveCreateInput,
) (*ArenaWaveRecord, error) {
	if err := validateArenaWaveCreateInput(in); err != nil {
		return nil, err
	}
	participantIDs := append([]uuid.UUID(nil), in.ParticipantIDs...)
	sort.Slice(participantIDs, func(i, j int) bool {
		return participantIDs[i].String() < participantIDs[j].String()
	})
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		if _, err := querier.CreateArenaWave(txCtx, sqlc.CreateArenaWaveParams{
			ID:             in.ID,
			TournamentID:   in.TournamentID,
			RosterID:       in.RosterID,
			RevisionID:     in.RevisionID.UUID(),
			ReplacesWaveID: nullableUUID(in.ReplacesWaveID),
			CreatedAt:      tstz(in.CreatedAt),
		}); err != nil {
			return err
		}
		for _, participantID := range participantIDs {
			if err := querier.CreateArenaWaveMember(txCtx, sqlc.CreateArenaWaveMemberParams{
				WaveID:        in.ID,
				RosterID:      in.RosterID,
				ParticipantID: participantID,
				CreatedAt:     tstz(in.CreatedAt),
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, mapArenaRepositoryWriteError("ArenaWavePostgres - Create", err)
	}
	return r.Get(ctx, in.TournamentID, in.ID)
}

func (r *ArenaWavePostgres) OpenReadyWindow(
	ctx context.Context,
	tournamentID uuid.UUID,
	waveID uuid.UUID,
	expectedRevision int64,
	in ArenaReadyWindowInput,
) (*ArenaWaveRecord, bool, error) {
	if !validOpenReadyWindowCommand(tournamentID, waveID, expectedRevision, in) {
		return nil, false, domain.ErrValidation
	}
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		current, err := querier.GetArenaWave(txCtx, sqlc.GetArenaWaveParams{ID: waveID, TournamentID: tournamentID})
		if err != nil {
			return err
		}
		if current.Revision != expectedRevision || current.State != string(domain.ArenaWaveStatePlanned) {
			return errArenaWaveCAS
		}
		if _, err = querier.CreateArenaReadyWindow(txCtx, sqlc.CreateArenaReadyWindowParams{
			ID:         in.ID,
			WaveID:     waveID,
			RosterID:   current.RosterID,
			RevisionID: in.RevisionID.UUID(),
			OpenedAt:   tstz(in.OpenedAt),
			Deadline:   tstz(in.Deadline),
		}); err != nil {
			return err
		}
		if _, err = querier.OpenArenaWaveReadyWindowCAS(txCtx, sqlc.OpenArenaWaveReadyWindowCASParams{
			OpenedAt:         tstz(in.OpenedAt),
			ID:               waveID,
			ExpectedRevision: expectedRevision,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errArenaWaveCAS
			}
			return err
		}
		return nil
	})
	if err != nil {
		return r.mapWaveCAS("OpenReadyWindow", err)
	}
	record, err := r.Get(ctx, tournamentID, waveID)
	return record, true, err
}

func (r *ArenaWavePostgres) MarkReady(
	ctx context.Context,
	tournamentID uuid.UUID,
	waveID uuid.UUID,
	windowID uuid.UUID,
	participantID uuid.UUID,
	expectedRevision int64,
	readyAt time.Time,
) (*ArenaWaveRecord, bool, error) {
	if !validMarkReadyCommand(tournamentID, waveID, windowID, participantID, expectedRevision, readyAt) {
		return nil, false, domain.ErrValidation
	}
	duplicate := false
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		current, err := querier.GetArenaWave(txCtx, sqlc.GetArenaWaveParams{ID: waveID, TournamentID: tournamentID})
		if err != nil {
			return err
		}
		if current.Revision != expectedRevision {
			return errArenaWaveCAS
		}
		_, err = querier.CreateArenaWaveReadiness(txCtx, sqlc.CreateArenaWaveReadinessParams{
			ReadyWindowID: windowID,
			WaveID:        waveID,
			RosterID:      current.RosterID,
			ParticipantID: participantID,
			ReadyAt:       tstz(readyAt),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			duplicate = true
			return nil
		}
		if err != nil {
			return err
		}
		readyCount, err := querier.CountArenaWaveReadiness(txCtx, windowID)
		if err != nil {
			return err
		}
		memberCount, err := querier.CountArenaWaveMembers(txCtx, waveID)
		if err != nil {
			return err
		}
		if memberCount < 2 || readyCount > memberCount {
			return domain.ErrValidation
		}
		_, err = querier.MarkArenaWaveReadinessCAS(txCtx, sqlc.MarkArenaWaveReadinessCASParams{
			AllReady:         readyCount == memberCount,
			UpdatedAt:        tstz(readyAt),
			ID:               waveID,
			ExpectedRevision: expectedRevision,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return errArenaWaveCAS
		}
		return err
	})
	if err != nil {
		return r.mapWaveCAS("MarkReady", err)
	}
	record, err := r.Get(ctx, tournamentID, waveID)
	return record, !duplicate, err
}

func (r *ArenaWavePostgres) Start(
	ctx context.Context,
	tournamentID uuid.UUID,
	waveID uuid.UUID,
	windowID uuid.UUID,
	expectedRevision int64,
	startedAt time.Time,
) (*ArenaWaveRecord, bool, error) {
	if tournamentID == uuid.Nil || waveID == uuid.Nil || windowID == uuid.Nil || expectedRevision < 1 ||
		!validServerTime(startedAt) {
		return nil, false, domain.ErrValidation
	}
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		if _, err := querier.ConsumeArenaReadyWindowCAS(txCtx, sqlc.ConsumeArenaReadyWindowCASParams{
			StartedAt: tstz(startedAt),
			ID:        windowID,
			WaveID:    waveID,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errArenaWaveCAS
			}
			return err
		}
		if _, err := querier.StartArenaWaveCAS(txCtx, sqlc.StartArenaWaveCASParams{
			StartedAt:        tstz(startedAt),
			ID:               waveID,
			TournamentID:     tournamentID,
			ExpectedRevision: expectedRevision,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errArenaWaveCAS
			}
			return err
		}
		return nil
	})
	if err != nil {
		return r.mapWaveCAS("Start", err)
	}
	record, err := r.Get(ctx, tournamentID, waveID)
	return record, true, err
}

func (r *ArenaWavePostgres) ExpireReadyWindow(
	ctx context.Context,
	tournamentID uuid.UUID,
	waveID uuid.UUID,
	windowID uuid.UUID,
	expectedRevision int64,
	expiredAt time.Time,
) (*ArenaWaveRecord, bool, error) {
	if tournamentID == uuid.Nil || waveID == uuid.Nil || windowID == uuid.Nil || expectedRevision < 1 ||
		!validServerTime(expiredAt) {
		return nil, false, domain.ErrValidation
	}
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		window, err := querier.GetArenaReadyWindow(txCtx, waveID)
		if err != nil {
			return err
		}
		if window.ID != windowID || !expiredAt.After(window.Deadline.Time) {
			return domain.ErrValidation
		}
		if _, err = querier.CloseArenaReadyWindowCAS(txCtx, sqlc.CloseArenaReadyWindowCASParams{
			NextState:     string(domain.ArenaReadyWindowStateExpired),
			ID:            windowID,
			WaveID:        waveID,
			ExpectedState: string(domain.ArenaReadyWindowStateOpen),
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errArenaWaveCAS
			}
			return err
		}
		if _, err = querier.TransitionArenaWaveCAS(txCtx, sqlc.TransitionArenaWaveCASParams{
			NextState:        string(domain.ArenaWaveStateReadyWindowExpired),
			UpdatedAt:        tstz(expiredAt),
			ID:               waveID,
			TournamentID:     tournamentID,
			ExpectedRevision: expectedRevision,
			ExpectedState:    string(domain.ArenaWaveStateReadyWindowOpen),
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errArenaWaveCAS
			}
			return err
		}
		return nil
	})
	if err != nil {
		return r.mapWaveCAS("ExpireReadyWindow", err)
	}
	record, err := r.Get(ctx, tournamentID, waveID)
	return record, true, err
}

func (r *ArenaWavePostgres) Close(
	ctx context.Context,
	tournamentID uuid.UUID,
	waveID uuid.UUID,
	expectedRevision int64,
	closedAt time.Time,
) (*ArenaWaveRecord, bool, error) {
	if tournamentID == uuid.Nil || waveID == uuid.Nil || expectedRevision < 1 || !validServerTime(closedAt) {
		return nil, false, domain.ErrValidation
	}
	_, err := r.tx.Querier(ctx).TransitionArenaWaveCAS(ctx, sqlc.TransitionArenaWaveCASParams{
		NextState:        string(domain.ArenaWaveStateCompleted),
		UpdatedAt:        tstz(closedAt),
		ClosedAt:         tstz(closedAt),
		ID:               waveID,
		TournamentID:     tournamentID,
		ExpectedRevision: expectedRevision,
		ExpectedState:    string(domain.ArenaWaveStateActive),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, mapArenaRepositoryWriteError("ArenaWavePostgres - Close", err)
	}
	record, err := r.Get(ctx, tournamentID, waveID)
	return record, true, err
}

func (r *ArenaWavePostgres) Get(
	ctx context.Context,
	tournamentID uuid.UUID,
	waveID uuid.UUID,
) (*ArenaWaveRecord, error) {
	if tournamentID == uuid.Nil || waveID == uuid.Nil {
		return nil, domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	row, err := querier.GetArenaWave(ctx, sqlc.GetArenaWaveParams{ID: waveID, TournamentID: tournamentID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrArenaWaveNotFound
		}
		return nil, fmt.Errorf("ArenaWavePostgres - Get: %w", err)
	}
	members, err := querier.ListArenaWaveMembers(ctx, waveID)
	if err != nil {
		return nil, fmt.Errorf("ArenaWavePostgres - Get - members: %w", err)
	}
	window, err := querier.GetArenaReadyWindow(ctx, waveID)
	hasWindow := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("ArenaWavePostgres - Get - ready window: %w", err)
	}
	readyAt := make(map[uuid.UUID]time.Time)
	if hasWindow {
		readiness, listErr := querier.ListArenaWaveReadiness(ctx, window.ID)
		if listErr != nil {
			return nil, fmt.Errorf("ArenaWavePostgres - Get - readiness: %w", listErr)
		}
		for _, item := range readiness {
			readyAt[item.ParticipantID] = item.ReadyAt.Time
		}
	}
	record, err := arenaWaveRecord(row, members, window, hasWindow, readyAt)
	if err != nil {
		return nil, fmt.Errorf("ArenaWavePostgres - Get - invalid snapshot: %w", err)
	}
	return record, nil
}

func (r *ArenaWavePostgres) mapWaveCAS(
	operation string,
	err error,
) (*ArenaWaveRecord, bool, error) {
	switch {
	case errors.Is(err, errArenaWaveCAS):
		return nil, false, nil
	case errors.Is(err, pgx.ErrNoRows):
		return nil, false, ErrArenaWaveNotFound
	default:
		return nil, false, mapArenaRepositoryWriteError("ArenaWavePostgres - "+operation, err)
	}
}

func validateArenaWaveCreateInput(in ArenaWaveCreateInput) error {
	if in.ID == uuid.Nil || in.TournamentID == uuid.Nil || in.RosterID == uuid.Nil || in.RevisionID.IsZero() ||
		!validServerTime(in.CreatedAt) || len(in.ParticipantIDs) < 2 {
		return domain.ErrValidation
	}
	seen := make(map[uuid.UUID]struct{}, len(in.ParticipantIDs))
	for _, participantID := range in.ParticipantIDs {
		if participantID == uuid.Nil {
			return domain.ErrValidation
		}
		if _, duplicate := seen[participantID]; duplicate {
			return domain.ErrValidation
		}
		seen[participantID] = struct{}{}
	}
	if in.ReplacesWaveID != nil && (*in.ReplacesWaveID == uuid.Nil || *in.ReplacesWaveID == in.ID) {
		return domain.ErrValidation
	}
	return nil
}

func arenaWaveRecord(
	row sqlc.ArenaWafe,
	members []sqlc.ArenaWaveMember,
	window sqlc.ArenaReadyWindow,
	hasWindow bool,
	readyAt map[uuid.UUID]time.Time,
) (*ArenaWaveRecord, error) {
	domainMembers := make([]domain.ArenaWaveMember, len(members))
	for index, member := range members {
		_, ready := readyAt[member.ParticipantID]
		domainMembers[index] = domain.ArenaWaveMember{ParticipantID: member.ParticipantID, Ready: ready}
	}
	wave := domain.ArenaWave{
		ID:           row.ID,
		TournamentID: row.TournamentID,
		RevisionID:   domain.ArenaWaveRevisionID(row.RevisionID),
		State:        domain.ArenaWaveState(row.State),
		Members:      domainMembers,
		StartedAt:    nullableTime(row.StartedAt),
		PausedAt:     nullableTime(row.PausedAt),
	}
	if hasWindow {
		wave.ReadyWindow = &domain.ArenaReadyWindow{
			ID:         window.ID,
			WaveID:     window.WaveID,
			RevisionID: domain.ArenaReadyWindowRevisionID(window.RevisionID),
			State:      domain.ArenaReadyWindowState(window.State),
			OpenedAt:   window.OpenedAt.Time,
			Deadline:   window.Deadline.Time,
			ConsumedAt: nullableTime(window.ConsumedAt),
		}
	}
	if err := wave.Validate(); err != nil {
		return nil, err
	}
	record := &ArenaWaveRecord{
		Wave:      wave,
		RosterID:  row.RosterID,
		Revision:  row.Revision,
		CreatedAt: row.CreatedAt.Time,
		UpdatedAt: row.UpdatedAt.Time,
		ClosedAt:  nullableTime(row.ClosedAt),
		ReadyAt:   readyAt,
	}
	if row.ReplacesWaveID.Valid {
		replacesWaveID := row.ReplacesWaveID.UUID
		record.ReplacesWaveID = &replacesWaveID
	}
	return record, nil
}

var errArenaWaveCAS = errors.New("arena wave compare-and-set failed")

func validOpenReadyWindowCommand(
	tournamentID uuid.UUID,
	waveID uuid.UUID,
	expectedRevision int64,
	in ArenaReadyWindowInput,
) bool {
	return tournamentID != uuid.Nil && waveID != uuid.Nil && expectedRevision >= 1 && in.ID != uuid.Nil &&
		!in.RevisionID.IsZero() && validServerTime(in.OpenedAt) && validServerTime(in.Deadline) &&
		in.Deadline.After(in.OpenedAt)
}

func validMarkReadyCommand(
	tournamentID uuid.UUID,
	waveID uuid.UUID,
	windowID uuid.UUID,
	participantID uuid.UUID,
	expectedRevision int64,
	readyAt time.Time,
) bool {
	return tournamentID != uuid.Nil && waveID != uuid.Nil && windowID != uuid.Nil &&
		participantID != uuid.Nil && expectedRevision >= 1 && validServerTime(readyAt)
}
