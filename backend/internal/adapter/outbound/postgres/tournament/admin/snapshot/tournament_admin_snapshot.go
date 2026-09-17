package snapshot

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	draftrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment/draft"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	rosterpostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admin/roster"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentadminexecution "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/execution"
	rostercapability "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/roster"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/snapshot"
)

type TournamentAdminSnapshotPostgres struct {
	tx     *db.TxManager
	roster RosterReader
	drafts DraftReader
}

// RosterReader is the narrow roster query boundary owned by the snapshot
// consumer. The snapshot adapter does not need roster mutation methods.
type RosterReader interface {
	GetRoster(context.Context, uuid.UUID) (rostercapability.RosterView, error)
}

// DraftReader is the narrow draft query boundary owned by the snapshot
// consumer. The snapshot adapter only loads one aggregate for pause details.
type DraftReader interface {
	Get(context.Context, uuid.UUID) (*draftrepo.DraftAggregate, error)
}

func NewTournamentAdminSnapshotPostgres(tx *db.TxManager) *TournamentAdminSnapshotPostgres {
	return NewTournamentAdminSnapshotPostgresWithDependencies(
		tx,
		rosterpostgres.NewTournamentAdminRosterPostgres(tx),
		draftrepo.NewDraftPostgres(tx),
	)
}

func NewTournamentAdminSnapshotPostgresWithDependencies(
	tx *db.TxManager,
	roster RosterReader,
	drafts DraftReader,
) *TournamentAdminSnapshotPostgres {
	return &TournamentAdminSnapshotPostgres{
		tx: tx, roster: roster, drafts: drafts,
	}
}

func (r *TournamentAdminSnapshotPostgres) GetOperatorSnapshot(
	ctx context.Context,
	query tournamentadmin.SnapshotQuery,
) (tournamentadmin.OperatorSnapshotView, error) {
	if !tournamentAdminSnapshotQueryValid(ctx, r, query) {
		return tournamentadmin.OperatorSnapshotView{}, domain.ErrValidation
	}

	var view tournamentadmin.OperatorSnapshotView
	err := r.tx.ReadSnapshot(ctx, func(txCtx context.Context) error {
		loaded, loadErr := r.loadOperatorSnapshot(txCtx, query)
		if loadErr != nil {
			return loadErr
		}
		view = loaded
		return nil
	})
	if err != nil {
		return tournamentadmin.OperatorSnapshotView{}, err
	}
	return view, nil
}

func (r *TournamentAdminSnapshotPostgres) loadOperatorSnapshot(
	ctx context.Context,
	query tournamentadmin.SnapshotQuery,
) (tournamentadmin.OperatorSnapshotView, error) {
	querier := r.tx.Querier(ctx)
	headerRow, err := querier.GetTournamentAdminSnapshotHeader(ctx, query.TournamentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return tournamentadmin.OperatorSnapshotView{}, domain.ErrTournamentNotFound
		}
		return tournamentadmin.OperatorSnapshotView{}, tournamentAdminSnapshotQueryError("header", err)
	}
	header, err := tournamentAdminSnapshotHeader(headerRow, query.TournamentID)
	if err != nil {
		return tournamentadmin.OperatorSnapshotView{}, err
	}
	if err := tournamentAdminSnapshotCursorError(query.Cursor, header.cursor, header.tournament.State); err != nil {
		return tournamentadmin.OperatorSnapshotView{}, err
	}

	roster, err := r.roster.GetRoster(ctx, query.TournamentID)
	if err != nil {
		if errors.Is(err, domain.ErrTournamentNotFound) {
			return tournamentadmin.OperatorSnapshotView{}, fmt.Errorf("operator snapshot roster disappeared: %w", domain.ErrInternal)
		}
		return tournamentadmin.OperatorSnapshotView{}, err
	}
	if roster.ID != header.tournament.RosterID || roster.TournamentID != query.TournamentID ||
		len(roster.Participants) != header.tournament.RosterSize {
		return tournamentadmin.OperatorSnapshotView{}, fmt.Errorf("operator snapshot roster mismatch: %w", domain.ErrInternal)
	}

	waves, err := tournamentAdminSnapshotLoadWaves(ctx, querier, query.TournamentID, roster)
	if err != nil {
		return tournamentadmin.OperatorSnapshotView{}, err
	}
	seriesGraph, err := tournamentAdminSnapshotLoadSeries(ctx, querier, query.TournamentID, roster)
	if err != nil {
		return tournamentadmin.OperatorSnapshotView{}, err
	}
	if !tournamentAdminSnapshotWaveSeriesMatch(waves, seriesGraph) {
		return tournamentadmin.OperatorSnapshotView{}, fmt.Errorf("operator snapshot Wave and Series mismatch: %w", domain.ErrInternal)
	}

	pauseRows, err := querier.ListTournamentAdminSnapshotPauses(
		ctx,
		sqlc.ListTournamentAdminSnapshotPausesParams{
			TournamentID: query.TournamentID,
			RosterID:     roster.ID,
		},
	)
	if err != nil {
		return tournamentadmin.OperatorSnapshotView{}, tournamentAdminSnapshotQueryError("pauses", err)
	}
	pauseGraph, err := r.loadPauseGraph(ctx, querier, header, roster, waves, seriesGraph, pauseRows)
	if err != nil {
		return tournamentadmin.OperatorSnapshotView{}, fmt.Errorf("operator snapshot pause graph: %w", err)
	}

	return tournamentadmin.OperatorSnapshotView{
		Tournament: header.tournament,
		Roster:     roster,
		Waves:      waves,
		Series:     seriesGraph.values,
		PauseGraph: pauseGraph,
		NextCursor: header.cursor,
	}, nil
}

func tournamentAdminSnapshotLoadWaves(
	ctx context.Context,
	querier *sqlc.Queries,
	tournamentID uuid.UUID,
	roster rostercapability.RosterView,
) ([]tournamentadminexecution.WaveView, error) {
	params := sqlc.ListTournamentAdminSnapshotWavesParams{TournamentID: tournamentID, RosterID: roster.ID}
	waveRows, err := querier.ListTournamentAdminSnapshotWaves(ctx, params)
	if err != nil {
		return nil, tournamentAdminSnapshotQueryError("waves", err)
	}
	memberRows, err := querier.ListTournamentAdminSnapshotWaveMembers(
		ctx,
		sqlc.ListTournamentAdminSnapshotWaveMembersParams(params),
	)
	if err != nil {
		return nil, tournamentAdminSnapshotQueryError("wave members", err)
	}
	return tournamentAdminSnapshotWaves(waveRows, memberRows, roster)
}

func tournamentAdminSnapshotLoadSeries(
	ctx context.Context,
	querier *sqlc.Queries,
	tournamentID uuid.UUID,
	roster rostercapability.RosterView,
) (tournamentAdminSnapshotSeriesGraph, error) {
	params := sqlc.ListTournamentAdminSnapshotSeriesParams{TournamentID: tournamentID, RosterID: roster.ID}
	seriesRows, err := querier.ListTournamentAdminSnapshotSeries(ctx, params)
	if err != nil {
		return tournamentAdminSnapshotSeriesGraph{}, tournamentAdminSnapshotQueryError("series", err)
	}
	slotRows, err := querier.ListTournamentAdminSnapshotGameSlots(
		ctx,
		sqlc.ListTournamentAdminSnapshotGameSlotsParams(params),
	)
	if err != nil {
		return tournamentAdminSnapshotSeriesGraph{}, tournamentAdminSnapshotQueryError("game slots", err)
	}
	attemptRows, err := querier.ListTournamentAdminSnapshotGameAttempts(
		ctx,
		sqlc.ListTournamentAdminSnapshotGameAttemptsParams(params),
	)
	if err != nil {
		return tournamentAdminSnapshotSeriesGraph{}, tournamentAdminSnapshotQueryError("game attempts", err)
	}
	return tournamentAdminSnapshotSeries(seriesRows, slotRows, tournamentAdminSnapshotGameAttempts(attemptRows), roster)
}

func tournamentAdminSnapshotGameAttempts(
	rows []sqlc.ListTournamentAdminSnapshotGameAttemptsRow,
) []sqlc.GameAttempt {
	result := make([]sqlc.GameAttempt, len(rows))
	for index, row := range rows {
		result[index] = sqlc.GameAttempt{
			ID:               row.ID,
			SlotID:           row.SlotID,
			SeriesID:         row.SeriesID,
			RosterID:         row.RosterID,
			AttemptNumber:    row.AttemptNumber,
			State:            row.State,
			ResultReason:     row.ResultReason,
			WinnerID:         row.WinnerID,
			ResultRevisionID: row.ResultRevisionID,
			Revision:         row.Revision,
			CreatedAt:        row.CreatedAt,
			UpdatedAt:        row.UpdatedAt,
			StartedAt:        row.StartedAt,
			FinishedAt:       row.FinishedAt,
		}
	}
	return result
}

func tournamentAdminSnapshotQueryValid(
	ctx context.Context,
	repository *TournamentAdminSnapshotPostgres,
	query tournamentadmin.SnapshotQuery,
) bool {
	if ctx == nil || repository == nil || repository.tx == nil || !repository.tx.HasPool() ||
		repository.roster == nil || repository.drafts == nil || query.Operator.ActorID == uuid.Nil ||
		query.TournamentID == uuid.Nil {
		return false
	}
	return query.Cursor == nil || query.Cursor.ProjectionRevision >= 1 &&
		query.Cursor.AuthorityRevision >= 1 && query.Cursor.AuditSequence >= 0
}

func tournamentAdminSnapshotQueryError(operation string, err error) error {
	return fmt.Errorf("TournamentAdminSnapshotPostgres - %s: %w", operation, err)
}

var _ tournamentadmin.SnapshotPort = (*TournamentAdminSnapshotPostgres)(nil)
var _ RosterReader = (*rosterpostgres.TournamentAdminRosterPostgres)(nil)
var _ DraftReader = (*draftrepo.DraftPostgres)(nil)
