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
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
)

func (r *TournamentAdminExecutionPostgres) openTournamentAdminWave(
	ctx context.Context,
	mutation tournamentadmin.WaveMutation,
) error {
	window := mutation.Next.ReadyWindow
	if window == nil {
		return domain.ErrValidation
	}
	_, changed, err := r.waves.OpenReadyWindow(
		ctx,
		mutation.Command.TournamentID,
		mutation.Command.WaveID,
		mutation.Authority.View.Revision,
		ReadyWindowInput{
			ID: window.ID, RevisionID: window.RevisionID,
			OpenedAt: window.OpenedAt, Deadline: window.Deadline,
		},
	)
	if err != nil {
		return err
	}
	if !changed {
		return domain.ErrConflict
	}
	return nil
}

func (r *TournamentAdminExecutionPostgres) startTournamentAdminWave(
	ctx context.Context,
	mutation tournamentadmin.WaveMutation,
) error {
	window := mutation.Authority.View.Wave.ReadyWindow
	if window == nil {
		return domain.ErrConflict
	}
	querier := r.tx.Querier(ctx)
	seriesIDs, err := querier.StartTournamentAdminWaveSeries(
		ctx,
		sqlc.StartTournamentAdminWaveSeriesParams{
			StartedAt: tstz(mutation.MutatedAt), WaveID: mutation.Command.WaveID,
		},
	)
	if err != nil {
		return executionWriteError("start Wave Series", err)
	}
	gameIDs, err := querier.StartTournamentAdminWaveGames(
		ctx,
		sqlc.StartTournamentAdminWaveGamesParams{
			StartedAt: tstz(mutation.MutatedAt), WaveID: mutation.Command.WaveID,
		},
	)
	if err != nil {
		return executionWriteError("start Wave Games", err)
	}
	if len(seriesIDs) != mutation.Authority.Graph.SeriesCount ||
		len(gameIDs) != mutation.Authority.Graph.CurrentGameCount {
		return domain.ErrConflict
	}
	_, changed, err := r.waves.Start(
		ctx,
		mutation.Command.TournamentID,
		mutation.Command.WaveID,
		window.ID,
		mutation.Authority.View.Revision,
		mutation.MutatedAt,
	)
	if err != nil {
		return err
	}
	if !changed {
		return domain.ErrConflict
	}
	return nil
}

func (r *TournamentAdminExecutionPostgres) pauseTournamentAdminWave(
	ctx context.Context,
	mutation tournamentadmin.WaveMutation,
) error {
	querier := r.tx.Querier(ctx)
	gameIDs, err := querier.PauseTournamentAdminWaveGames(
		ctx,
		sqlc.PauseTournamentAdminWaveGamesParams{
			PausedAt: tstz(mutation.MutatedAt), WaveID: mutation.Command.WaveID,
		},
	)
	if err != nil {
		return executionWriteError("pause Wave Games", err)
	}
	seriesIDs, err := querier.PauseTournamentAdminWaveSeries(
		ctx,
		sqlc.PauseTournamentAdminWaveSeriesParams{
			PausedAt: tstz(mutation.MutatedAt), WaveID: mutation.Command.WaveID,
		},
	)
	if err != nil {
		return executionWriteError("pause Wave Series", err)
	}
	if len(seriesIDs) != mutation.Authority.Graph.SeriesCount ||
		len(gameIDs) != mutation.Authority.Graph.CurrentGameCount {
		return domain.ErrConflict
	}
	pausedAt := mutation.MutatedAt
	return r.transitionTournamentAdminWave(
		ctx, mutation, domain.WaveStateActive, domain.WaveStatePaused, &pausedAt, nil,
	)
}

func (r *TournamentAdminExecutionPostgres) resumeTournamentAdminWave(
	ctx context.Context,
	mutation tournamentadmin.WaveMutation,
) error {
	querier := r.tx.Querier(ctx)
	reboundGames, err := querier.RebindPausedExecutionGameEpochs(
		ctx,
		sqlc.RebindPausedExecutionGameEpochsParams{
			TournamentID:      mutation.Command.TournamentID,
			WaveID:            mutation.Command.WaveID,
			CommandID:         mutation.Command.CommandID,
			AuthorityHolderID: mutation.ExecutionAuthority.HolderID,
			AuthorityLeaseID:  mutation.ExecutionAuthority.LeaseID,
			AuthorityEpoch:    mutation.ExecutionAuthority.Epoch,
			ReboundAt:         tstz(mutation.MutatedAt),
		},
	)
	if err != nil {
		return executionWriteError("rebind paused execution Game epochs", err)
	}
	if len(reboundGames) != mutation.Authority.Graph.CurrentGameCount ||
		!uniqueTournamentAdminExecutionIDs(reboundGames) {
		return domain.ErrConflict
	}
	seriesIDs, err := querier.ResumeTournamentAdminWaveSeries(
		ctx,
		sqlc.ResumeTournamentAdminWaveSeriesParams{
			ResumedAt: tstz(mutation.MutatedAt), WaveID: mutation.Command.WaveID,
		},
	)
	if err != nil {
		return executionWriteError("resume Wave Series", err)
	}
	gameIDs, err := querier.ResumeTournamentAdminWaveGames(
		ctx,
		sqlc.ResumeTournamentAdminWaveGamesParams{
			ResumedAt: tstz(mutation.MutatedAt), WaveID: mutation.Command.WaveID,
		},
	)
	if err != nil {
		return executionWriteError("resume Wave Games", err)
	}
	if len(seriesIDs) != mutation.Authority.Graph.SeriesCount ||
		len(gameIDs) != mutation.Authority.Graph.CurrentGameCount ||
		!sameTournamentAdminExecutionIDs(reboundGames, gameIDs) {
		return domain.ErrConflict
	}
	return r.transitionTournamentAdminWave(
		ctx, mutation, domain.WaveStatePaused, domain.WaveStateActive, nil, nil,
	)
}

func uniqueTournamentAdminExecutionIDs(ids []uuid.UUID) bool {
	seen := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		if id == uuid.Nil {
			return false
		}
		if _, duplicate := seen[id]; duplicate {
			return false
		}
		seen[id] = struct{}{}
	}
	return true
}

func sameTournamentAdminExecutionIDs(first, second []uuid.UUID) bool {
	if len(first) != len(second) || !uniqueTournamentAdminExecutionIDs(first) ||
		!uniqueTournamentAdminExecutionIDs(second) {
		return false
	}
	seen := make(map[uuid.UUID]struct{}, len(first))
	for _, id := range first {
		seen[id] = struct{}{}
	}
	for _, id := range second {
		if _, ok := seen[id]; !ok {
			return false
		}
	}
	return true
}

func (r *TournamentAdminExecutionPostgres) completeTournamentAdminWave(
	ctx context.Context,
	mutation tournamentadmin.WaveMutation,
) error {
	row, err := r.tx.Querier(ctx).CompleteTournamentAdminWaveCAS(
		ctx,
		sqlc.CompleteTournamentAdminWaveCASParams{
			RevisionID:         mutation.Next.RevisionID.UUID(),
			UpdatedAt:          tstz(mutation.MutatedAt),
			ClosedAt:           tstz(mutation.MutatedAt),
			ID:                 mutation.Command.WaveID,
			TournamentID:       mutation.Command.TournamentID,
			ExpectedRevisionID: mutation.Authority.View.Wave.RevisionID.UUID(),
			ExpectedRevision:   mutation.Authority.View.Revision,
		},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrConflict
	}
	if err != nil {
		return executionWriteError("complete Wave", err)
	}
	if row.ID != mutation.Command.WaveID || row.RevisionID != mutation.Next.RevisionID.UUID() ||
		row.Revision != mutation.Authority.View.Revision+1 || row.State != string(domain.WaveStateCompleted) {
		return fmt.Errorf("TournamentAdminExecutionPostgres - complete Wave: %w", domain.ErrInternal)
	}
	return nil
}

func (r *TournamentAdminExecutionPostgres) cancelTournamentAdminWave(
	ctx context.Context,
	mutation tournamentadmin.WaveMutation,
) error {
	wave := mutation.Authority.View.Wave
	if wave.ReadyWindow == nil || wave.ReadyWindow.State != domain.ReadyWindowStateOpen {
		return domain.ErrConflict
	}
	readyCount := 0
	for _, member := range wave.Members {
		if member.Ready {
			readyCount++
		}
	}
	querier := r.tx.Querier(ctx)
	cleared, err := querier.ClearWaveReadinessHeads(ctx, sqlc.ClearWaveReadinessHeadsParams{
		UpdatedAt: tstz(mutation.MutatedAt), WaveID: wave.ID,
		ReadyWindowID: uuid.NullUUID{UUID: wave.ReadyWindow.ID, Valid: true},
	})
	if err != nil {
		return executionWriteError("cancel Wave readiness", err)
	}
	if len(cleared) != readyCount {
		return domain.ErrConflict
	}
	if _, err = querier.CloseReadyWindowCAS(ctx, sqlc.CloseReadyWindowCASParams{
		NextState: string(domain.ReadyWindowStateSuperseded), ID: wave.ReadyWindow.ID,
		WaveID: wave.ID, ExpectedState: string(domain.ReadyWindowStateOpen),
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrConflict
		}
		return executionWriteError("cancel ready window", err)
	}
	closedAt := mutation.MutatedAt
	return r.transitionTournamentAdminWave(
		ctx, mutation, wave.State, domain.WaveStateSuperseded, nil, &closedAt,
	)
}

func (r *TournamentAdminExecutionPostgres) transitionTournamentAdminWave(
	ctx context.Context,
	mutation tournamentadmin.WaveMutation,
	expected domain.WaveState,
	next domain.WaveState,
	pausedAt *time.Time,
	closedAt *time.Time,
) error {
	row, err := r.tx.Querier(ctx).TransitionWaveCAS(ctx, sqlc.TransitionWaveCASParams{
		NextState: string(next), UpdatedAt: tstz(mutation.MutatedAt),
		PausedAt: nullableTSTZ(pausedAt), ClosedAt: nullableTSTZ(closedAt),
		ID: mutation.Command.WaveID, TournamentID: mutation.Command.TournamentID,
		ExpectedRevision: mutation.Authority.View.Revision, ExpectedState: string(expected),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrConflict
	}
	if err != nil {
		return executionWriteError("transition Wave", err)
	}
	if row.ID != mutation.Command.WaveID || row.Revision != mutation.Authority.View.Revision+1 ||
		row.State != string(next) {
		return fmt.Errorf("TournamentAdminExecutionPostgres - transition Wave: %w", domain.ErrInternal)
	}
	return nil
}
