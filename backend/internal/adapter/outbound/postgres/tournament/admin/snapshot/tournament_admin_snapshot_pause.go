package snapshot

import (
	"context"
	"errors"

	"github.com/google/uuid"

	draftpostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment/draft"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause"
	tournamentadminexecution "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/execution"
	rostercapability "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/roster"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/snapshot"
)

func (r *TournamentAdminSnapshotPostgres) loadPauseGraph(
	ctx context.Context,
	querier *sqlc.Queries,
	header tournamentAdminSnapshotHeaderState,
	roster rostercapability.RosterView,
	waves []tournamentadminexecution.WaveView,
	seriesGraph tournamentAdminSnapshotSeriesGraph,
	pauseRows []sqlc.Pause,
) (*tournamentadmin.PauseGraphView, error) {
	pauseIndex, err := tournamentAdminSnapshotPauses(pauseRows, header, roster, waves, seriesGraph)
	if err != nil {
		return nil, err
	}
	root, err := pauseIndex.normalRoot(header.tournament.State)
	if err != nil || root == nil {
		return nil, err
	}
	if header.authority == nil {
		return nil, domain.ErrInternal
	}

	wave, exists := tournamentAdminSnapshotWaveByID(waves, root.WaveID.UUID)
	if !exists || !tournamentAdminSnapshotRootMatchesWave(*root, wave) {
		return nil, domain.ErrInternal
	}
	seriesValues, games, seriesIDs, err := tournamentAdminSnapshotPauseExecutions(
		*root,
		wave,
		seriesGraph,
		pauseIndex,
	)
	if err != nil {
		return nil, err
	}

	draft, err := r.loadSnapshotPauseDraft(ctx, querier, wave, roster, seriesValues)
	if err != nil {
		return nil, err
	}
	presence, reconnect, counters, err := tournamentAdminSnapshotPauseConnectivity(
		ctx,
		querier,
		header,
		roster,
		seriesIDs,
		seriesGraph,
		pauseIndex,
		*root,
	)
	if err != nil {
		return nil, err
	}
	clocks, err := querier.ListTournamentAdminSnapshotPauseClocks(ctx, sqlc.ListTournamentAdminSnapshotPauseClocksParams{
		TournamentID: header.tournament.ID,
		RosterID:     roster.ID,
	})
	if err != nil {
		return nil, tournamentAdminSnapshotQueryError("pause clocks", err)
	}
	frozen, err := tournamentAdminSnapshotFrozenDeadlines(*root, wave, games, draft, clocks, pauseIndex)
	if err != nil {
		return nil, err
	}

	pausedAt, valid := tournamentAdminSnapshotRequiredTime(root.StartedAt)
	if !valid {
		return nil, domain.ErrInternal
	}
	graph := gameusecase.PauseGraph{
		Scope: pausedomain.GraphScope{
			TournamentID: header.tournament.ID,
			RosterID:     roster.ID,
			WaveID:       wave.Wave.ID,
			Authority:    *header.authority,
		},
		Revision:               root.Revision,
		Tournament:             tournamentAdminSnapshotPauseTournament(header),
		Wave:                   gameusecase.PauseWave{Wave: wave.Wave, Revision: wave.Revision},
		Series:                 seriesValues,
		Games:                  games,
		Draft:                  draft,
		Presence:               presence,
		Reconnect:              reconnect,
		Counters:               counters,
		FrozenDeadlines:        frozen,
		ActivePauseID:          root.ID,
		PausedAt:               &pausedAt,
		DeadlinesSuppressed:    true,
		TerminalActionRevision: 0,
	}
	view := &tournamentadmin.PauseGraphView{Graph: graph, Wave: wave}
	if !tournamentAdminSnapshotPauseGraphValid(*view, *root, seriesIDs, pauseIndex) {
		return nil, domain.ErrInternal
	}
	return view, nil
}

func (r *TournamentAdminSnapshotPostgres) loadSnapshotPauseDraft(
	ctx context.Context,
	querier *sqlc.Queries,
	wave tournamentadminexecution.WaveView,
	roster rostercapability.RosterView,
	seriesValues []gameusecase.PauseSeries,
) (*draftusecase.Execution, error) {
	draftIDs, err := querier.ListTournamentAdminSnapshotActiveDraftIDs(ctx, wave.Wave.ID)
	if err != nil {
		return nil, tournamentAdminSnapshotQueryError("active drafts", err)
	}
	if len(draftIDs) > 1 || len(draftIDs) == 1 && draftIDs[0] == uuid.Nil {
		return nil, domain.ErrInternal
	}
	if len(draftIDs) == 0 {
		if tournamentAdminSnapshotSeriesNeedsDraft(seriesValues) {
			return nil, domain.ErrInternal
		}
		return nil, nil
	}
	aggregate, err := r.drafts.Get(ctx, draftIDs[0])
	if err != nil {
		if errors.Is(err, draftpostgres.ErrDraftNotFound) {
			return nil, domain.ErrInternal
		}
		return nil, err
	}
	if aggregate == nil || aggregate.Draft.RosterID != roster.ID || len(aggregate.Revisions) == 0 {
		return nil, domain.ErrInternal
	}
	execution, err := participantDraftExecution(aggregate, aggregate.Revisions[len(aggregate.Revisions)-1].Revision)
	if err != nil || execution == nil || !tournamentAdminSnapshotDraftMatchesSeries(*execution, seriesValues) {
		return nil, domain.ErrInternal
	}
	return execution, nil
}

func tournamentAdminSnapshotPauseConnectivity(
	ctx context.Context,
	querier *sqlc.Queries,
	header tournamentAdminSnapshotHeaderState,
	roster rostercapability.RosterView,
	seriesIDs map[uuid.UUID]struct{},
	seriesGraph tournamentAdminSnapshotSeriesGraph,
	pauseIndex tournamentAdminSnapshotPauseIndex,
	root sqlc.Pause,
) ([]pausedomain.PausePresence, []pausedomain.PauseReconnectInterval, []pausedomain.PauseReconnectCounter, error) {
	params := sqlc.ListTournamentAdminSnapshotPresenceParams{
		TournamentID: header.tournament.ID,
		RosterID:     roster.ID,
		PauseID:      root.ID,
	}
	presenceRows, err := querier.ListTournamentAdminSnapshotPresence(ctx, params)
	if err != nil {
		return nil, nil, nil, tournamentAdminSnapshotQueryError("presence", err)
	}
	intervalRows, err := querier.ListTournamentAdminSnapshotReconnectIntervals(
		ctx,
		sqlc.ListTournamentAdminSnapshotReconnectIntervalsParams{
			TournamentID: header.tournament.ID,
			RosterID:     roster.ID,
		},
	)
	if err != nil {
		return nil, nil, nil, tournamentAdminSnapshotQueryError("reconnect intervals", err)
	}
	counterRows, err := querier.ListTournamentAdminSnapshotReconnectCounters(
		ctx,
		sqlc.ListTournamentAdminSnapshotReconnectCountersParams{
			TournamentID: header.tournament.ID,
			RosterID:     roster.ID,
		},
	)
	if err != nil {
		return nil, nil, nil, tournamentAdminSnapshotQueryError("reconnect counters", err)
	}

	presenceRows = tournamentAdminSnapshotPresenceRows(presenceRows, seriesIDs)
	intervalRows = tournamentAdminSnapshotReconnectRows(intervalRows, seriesIDs)
	counterRows, ok := tournamentAdminSnapshotCounterRows(counterRows, seriesIDs, pauseIndex)
	if !ok {
		return nil, nil, nil, domain.ErrInternal
	}
	presence, err := recoveryPresence(presenceRows)
	if err != nil {
		return nil, nil, nil, domain.ErrInternal
	}
	reconnect, err := recoveryIntervals(intervalRows)
	if err != nil {
		return nil, nil, nil, domain.ErrInternal
	}
	counters, err := recoveryCounters(counterRows)
	if err != nil {
		return nil, nil, nil, domain.ErrInternal
	}
	if !tournamentAdminSnapshotConnectivityValid(
		header.tournament.ID,
		roster.ID,
		seriesIDs,
		seriesGraph,
		pauseIndex,
		root,
		presence,
		reconnect,
		counters,
	) {
		return nil, nil, nil, domain.ErrInternal
	}
	return presence, reconnect, counters, nil
}
