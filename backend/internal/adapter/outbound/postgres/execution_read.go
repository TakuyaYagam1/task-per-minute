package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func (r *WavePostgres) Get(
	ctx context.Context,
	tournamentID uuid.UUID,
	waveID uuid.UUID,
) (*WaveRecord, error) {
	if tournamentID == uuid.Nil || waveID == uuid.Nil {
		return nil, domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	row, err := querier.GetWave(ctx, sqlc.GetWaveParams{ID: waveID, TournamentID: tournamentID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrWaveNotFound
		}
		return nil, fmt.Errorf("WavePostgres - Get: %w", err)
	}
	members, err := querier.ListWaveMembers(ctx, waveID)
	if err != nil {
		return nil, fmt.Errorf("WavePostgres - Get - members: %w", err)
	}
	seriesRows, err := querier.ListWaveSeries(ctx, waveID)
	if err != nil {
		return nil, fmt.Errorf("WavePostgres - Get - Series: %w", err)
	}
	readiness, err := querier.ListWaveReadinessHeads(ctx, waveID)
	if err != nil {
		return nil, fmt.Errorf("WavePostgres - Get - readiness: %w", err)
	}
	window, err := querier.GetReadyWindow(ctx, waveID)
	hasWindow := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("WavePostgres - Get - ready window: %w", err)
	}
	record, err := waveRecord(row, members, seriesRows, readiness, window, hasWindow)
	if err != nil {
		return nil, fmt.Errorf("WavePostgres - Get - invalid snapshot: %w", err)
	}
	return record, nil
}

func (r *WavePostgres) mapWaveCAS(
	operation string,
	err error,
) (*WaveRecord, bool, error) {
	switch {
	case errors.Is(err, errWaveCAS):
		return nil, false, nil
	case errors.Is(err, pgx.ErrNoRows):
		return nil, false, ErrWaveNotFound
	default:
		return nil, false, mapRepositoryWriteError("WavePostgres - "+operation, err)
	}
}

func waveRecord(
	row sqlc.Wave,
	members []sqlc.WaveMember,
	seriesRows []sqlc.Series,
	readiness []sqlc.WaveReadiness,
	window sqlc.ReadyWindow,
	hasWindow bool,
) (*WaveRecord, error) {
	readyAt := make(map[uuid.UUID]time.Time)
	readinessRevisions := make(map[uuid.UUID]int64, len(readiness))
	for _, item := range readiness {
		readinessRevisions[item.ParticipantID] = item.Revision
		if item.Ready && item.ReadyAt.Valid {
			readyAt[item.ParticipantID] = item.ReadyAt.Time
		}
	}
	domainMembers := make([]domain.WaveMember, len(members))
	for index, member := range members {
		_, ready := readyAt[member.ParticipantID]
		domainMembers[index] = domain.WaveMember{ParticipantID: member.ParticipantID, Ready: ready}
	}
	wave := domain.Wave{
		ID:           row.ID,
		TournamentID: row.TournamentID,
		RevisionID:   domain.WaveRevisionID(row.RevisionID),
		State:        domain.WaveState(row.State),
		Members:      domainMembers,
		StartedAt:    nullableTime(row.StartedAt),
		PausedAt:     nullableTime(row.PausedAt),
	}
	if hasWindow {
		wave.ReadyWindow = &domain.ReadyWindow{
			ID:         window.ID,
			WaveID:     window.WaveID,
			RevisionID: domain.ReadyWindowRevisionID(window.RevisionID),
			State:      domain.ReadyWindowState(window.State),
			OpenedAt:   window.OpenedAt.Time,
			Deadline:   window.Deadline.Time,
			ConsumedAt: nullableTime(window.ConsumedAt),
		}
	}
	if err := wave.Validate(); err != nil {
		return nil, err
	}
	series := make([]domain.Series, len(seriesRows))
	for index, item := range seriesRows {
		series[index] = domain.Series{
			ID: item.ID, TournamentID: item.TournamentID,
			FirstParticipantID:  item.FirstParticipantID,
			SecondParticipantID: item.SecondParticipantID,
			Format:              domain.SeriesFormat(item.Format), State: domain.SeriesState(item.State),
			Score: domain.SeriesScore{
				FirstParticipantWins:  int(item.FirstParticipantWins),
				SecondParticipantWins: int(item.SecondParticipantWins),
			},
			Slots: []domain.GameSlot{},
		}
		if item.WinnerID.Valid {
			winnerID := item.WinnerID.UUID
			series[index].WinnerID = &winnerID
		}
		if item.CurrentScoreRevisionID.Valid {
			revisionID := domain.SeriesScoreRevisionID(item.CurrentScoreRevisionID.UUID)
			series[index].CurrentScoreRevisionID = &revisionID
		}
		if item.CurrentResultRevisionID.Valid {
			revisionID := domain.OfficialResultRevisionID(item.CurrentResultRevisionID.UUID)
			series[index].CurrentResultRevisionID = &revisionID
		}
		if err := series[index].Validate(); err != nil {
			return nil, err
		}
	}
	record := &WaveRecord{
		Wave: wave, RosterID: row.RosterID, Revision: row.Revision,
		CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
		ClosedAt: nullableTime(row.ClosedAt), ReadyAt: readyAt,
		ReadinessRevisions: readinessRevisions, Series: series,
	}
	if row.ReplacesWaveID.Valid {
		replacesWaveID := row.ReplacesWaveID.UUID
		record.ReplacesWaveID = &replacesWaveID
	}
	return record, nil
}
