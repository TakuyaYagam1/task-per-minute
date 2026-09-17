package result

import (
	"context"
	"fmt"
	"math"

	"github.com/google/uuid"

	wavepostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/execution/wave"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	gameforfeit "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/forfeit"
	gamenoshow "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/noshow"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/result"
)

type operatorSeriesSnapshot struct {
	series            sqlc.Series
	scoreHead         sqlc.SeriesScoreHead
	graph             []sqlc.ListRecoverySeriesGraphRow
	execution         seriesdomain.Execution
	currentOrdinal    int
	gameRevisionIDs   []domain.OfficialResultRevisionID
	projection        sqlc.ProjectionRevision
	attempts          map[uuid.UUID]sqlc.GameAttempt
	seriesResultOrder int
}

func (r *TournamentAdminResultPostgres) LoadOperatorNoShowAuthority(
	ctx context.Context,
	command resultusecase.NoShowCommand,
) (gamenoshow.NoShowAuthority, error) {
	if ctx == nil || !r.available() {
		return gamenoshow.NoShowAuthority{}, domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	if err := lockTournamentResultScope(ctx, querier, command.TournamentID, uuid.Nil); err != nil {
		return gamenoshow.NoShowAuthority{}, operatorResultLookupError("lock no-show result scope", err)
	}
	if _, err := querier.LockOperatorNoShowPublication(ctx, sqlc.LockOperatorNoShowPublicationParams{
		TournamentID: command.TournamentID, WaveID: command.WaveID, ReadyWindowID: command.WindowID, SeriesID: command.SeriesID,
	}); err != nil {
		return gamenoshow.NoShowAuthority{}, operatorResultLookupError("lock no-show publication authority", err)
	}
	row, err := querier.LockOperatorNoShowSnapshot(ctx, sqlc.LockOperatorNoShowSnapshotParams{
		SeriesID: command.SeriesID, TournamentID: command.TournamentID,
		ExpectedAuthorityRevision: command.ExpectedAuthorityRevision,
		ExpectedSeriesState:       string(command.ExpectedSeriesState),
		WaveID:                    command.WaveID, ExpectedWaveRevisionID: command.ExpectedWaveRevisionID,
		ReadyWindowID: command.WindowID, ExpectedWindowRevisionID: command.ExpectedWindowRevisionID,
	})
	if err != nil {
		return gamenoshow.NoShowAuthority{}, operatorResultLookupError("lock no-show snapshot", err)
	}
	snapshot, err := r.loadOperatorSeriesSnapshot(ctx, row.Series, row.SeriesScoreHead, "", 0)
	if err != nil {
		return gamenoshow.NoShowAuthority{}, err
	}
	wave, err := r.loadOperatorNoShowWave(ctx, row)
	if err != nil {
		return gamenoshow.NoShowAuthority{}, err
	}
	authority := gamenoshow.NoShowAuthority{
		Scope: domain.NormalNoShowScope{
			TournamentID: command.TournamentID, WaveID: command.WaveID,
			WindowID: command.WindowID, SeriesID: command.SeriesID,
		},
		Revision: command.ExpectedAuthorityRevision, Wave: wave,
		Series: snapshot.execution, CurrentOrdinal: snapshot.currentOrdinal,
	}
	return authority, nil
}

func (r *TournamentAdminResultPostgres) LoadOperatorForfeitAuthority(
	ctx context.Context,
	command resultusecase.ForfeitCommand,
) (gameforfeit.ForfeitAuthority, error) {
	if ctx == nil || !r.available() {
		return gameforfeit.ForfeitAuthority{}, domain.ErrValidation
	}
	if err := lockTournamentResultScope(ctx, r.tx.Querier(ctx), command.TournamentID, uuid.Nil); err != nil {
		return gameforfeit.ForfeitAuthority{}, operatorResultLookupError("lock forfeit result scope", err)
	}
	row, err := r.tx.Querier(ctx).LockOperatorForfeitSnapshot(
		ctx,
		sqlc.LockOperatorForfeitSnapshotParams{
			SeriesID: command.SeriesID, TournamentID: command.TournamentID,
			ExpectedAuthorityRevision: command.ExpectedAuthorityRevision,
		},
	)
	if err != nil {
		return gameforfeit.ForfeitAuthority{}, operatorResultLookupError("lock forfeit snapshot", err)
	}
	snapshot, err := r.loadOperatorSeriesSnapshot(
		ctx,
		row.Series,
		row.SeriesScoreHead,
		row.SeriesResumeState,
		row.SeriesResultRevision,
	)
	if err != nil {
		return gameforfeit.ForfeitAuthority{}, err
	}
	return gameforfeit.ForfeitAuthority{
		Scope:    gameforfeit.Scope{TournamentID: command.TournamentID, SeriesID: command.SeriesID},
		Revision: command.ExpectedAuthorityRevision, Series: snapshot.execution,
		AuthorizedOperatorIDs:        []uuid.UUID{command.Operator.ActorID},
		CurrentOrdinal:               snapshot.currentOrdinal,
		CurrentSeriesResultOrdinal:   snapshot.seriesResultOrder,
		CurrentProjectionRevision:    snapshot.projection.RevisionNumber,
		CurrentGameResultRevisionIDs: append([]domain.OfficialResultRevisionID(nil), snapshot.gameRevisionIDs...),
	}, nil
}

func (r *TournamentAdminResultPostgres) loadOperatorSeriesSnapshot(
	ctx context.Context,
	series sqlc.Series,
	scoreHead sqlc.SeriesScoreHead,
	resumeState string,
	seriesResultOrder int64,
) (operatorSeriesSnapshot, error) {
	if !validOperatorSeriesSnapshotHeader(series, scoreHead, seriesResultOrder) {
		return operatorSeriesSnapshot{}, domain.ErrInternal
	}
	querier := r.tx.Querier(ctx)
	graph, err := querier.ListRecoverySeriesGraph(ctx, sqlc.ListRecoverySeriesGraphParams{
		SeriesID: series.ID, RosterID: series.RosterID,
	})
	if err != nil {
		return operatorSeriesSnapshot{}, fmt.Errorf("TournamentAdminResultPostgres - load Series graph: %w", err)
	}
	execution, err := operatorSeriesExecution(series, graph, resumeState)
	if err != nil {
		return operatorSeriesSnapshot{}, err
	}
	ordinal, err := recoveryCurrentOrdinal(scoreHead)
	if err != nil {
		return operatorSeriesSnapshot{}, err
	}
	revisionIDs, projection, err := r.loadOperatorResultRevisionState(ctx, series)
	if err != nil {
		return operatorSeriesSnapshot{}, err
	}
	attempts, err := operatorSeriesAttemptIndex(series, graph)
	if err != nil {
		return operatorSeriesSnapshot{}, err
	}
	return operatorSeriesSnapshot{
		series: series, scoreHead: scoreHead, graph: graph, execution: execution,
		currentOrdinal: ordinal, gameRevisionIDs: revisionIDs, projection: projection,
		attempts: attempts, seriesResultOrder: int(seriesResultOrder),
	}, nil
}

func validOperatorSeriesSnapshotHeader(
	series sqlc.Series,
	scoreHead sqlc.SeriesScoreHead,
	seriesResultOrder int64,
) bool {
	return series.ID != uuid.Nil && series.TournamentID != uuid.Nil && series.RosterID != uuid.Nil &&
		scoreHead.SeriesID == series.ID && scoreHead.RosterID == series.RosterID &&
		series.CurrentScoreRevisionID.Valid && series.CurrentScoreRevisionID.UUID == scoreHead.CurrentRevisionID &&
		seriesResultOrder >= 0 && seriesResultOrder <= math.MaxInt
}

func operatorSeriesExecution(
	series sqlc.Series,
	graph []sqlc.ListRecoverySeriesGraphRow,
	resumeState string,
) (seriesdomain.Execution, error) {
	domainSeries, err := recoverySeries(series, graph)
	if err != nil {
		return seriesdomain.Execution{}, err
	}
	execution := seriesdomain.Execution{Series: domainSeries}
	if domainSeries.State == domain.SeriesStateTechnicalPause {
		state := domain.SeriesState(resumeState)
		if !state.IsValid() {
			return seriesdomain.Execution{}, domain.ErrInternal
		}
		execution.ResumeState = &state
	}
	if err = execution.Validate(); err != nil {
		return seriesdomain.Execution{}, fmt.Errorf(
			"TournamentAdminResultPostgres - invalid Series authority: %w",
			err,
		)
	}
	return execution, nil
}

func (r *TournamentAdminResultPostgres) loadOperatorResultRevisionState(
	ctx context.Context,
	series sqlc.Series,
) ([]domain.OfficialResultRevisionID, sqlc.ProjectionRevision, error) {
	querier := r.tx.Querier(ctx)
	revisionRows, err := querier.ListRecoveryGameResultRevisionIDs(
		ctx,
		sqlc.ListRecoveryGameResultRevisionIDsParams{SeriesID: series.ID, RosterID: series.RosterID},
	)
	if err != nil {
		return nil, sqlc.ProjectionRevision{}, fmt.Errorf(
			"TournamentAdminResultPostgres - load Game revisions: %w",
			err,
		)
	}
	revisionIDs, err := recoveryResultRevisionIDs(revisionRows)
	if err != nil {
		return nil, sqlc.ProjectionRevision{}, err
	}
	projection, err := querier.GetCurrentProjectionRevision(ctx, sqlc.GetCurrentProjectionRevisionParams{
		TournamentID: series.TournamentID, RosterID: series.RosterID,
	})
	if err != nil {
		return nil, sqlc.ProjectionRevision{}, operatorResultLookupError("load projection revision", err)
	}
	if projection.ID == uuid.Nil || projection.RevisionNumber < 1 || projection.State != "published" {
		return nil, sqlc.ProjectionRevision{}, domain.ErrInternal
	}
	return revisionIDs, projection, nil
}

func operatorSeriesAttemptIndex(
	series sqlc.Series,
	graph []sqlc.ListRecoverySeriesGraphRow,
) (map[uuid.UUID]sqlc.GameAttempt, error) {
	attempts := make(map[uuid.UUID]sqlc.GameAttempt, len(graph))
	for _, item := range graph {
		attempt := item.GameAttempt
		if attempt.ID == uuid.Nil || attempt.SeriesID != series.ID || attempt.RosterID != series.RosterID {
			return nil, domain.ErrInternal
		}
		if _, duplicate := attempts[attempt.ID]; duplicate {
			return nil, domain.ErrInternal
		}
		attempts[attempt.ID] = attempt
	}
	return attempts, nil
}

func (r *TournamentAdminResultPostgres) loadOperatorNoShowWave(
	ctx context.Context,
	row sqlc.LockOperatorNoShowSnapshotRow,
) (domain.Wave, error) {
	querier := r.tx.Querier(ctx)
	members, err := querier.ListWaveMembers(ctx, row.Wave.ID)
	if err != nil {
		return domain.Wave{}, fmt.Errorf("TournamentAdminResultPostgres - load Wave members: %w", err)
	}
	readiness, err := querier.LockOperatorNoShowReadiness(ctx, sqlc.LockOperatorNoShowReadinessParams{
		WaveID: row.Wave.ID, RosterID: row.Wave.RosterID,
		ReadyWindowID: nullableUUIDValue(row.ReadyWindow.ID),
	})
	if err != nil {
		return domain.Wave{}, fmt.Errorf("TournamentAdminResultPostgres - lock readiness: %w", err)
	}
	if !operatorReadinessMatchesMembers(
		members,
		readiness,
		row.Wave.ID,
		row.Wave.RosterID,
		row.ReadyWindow.ID,
	) {
		return domain.Wave{}, domain.ErrConflict
	}
	record, err := wavepostgres.MapWaveRecord(row.Wave, members, nil, readiness, row.ReadyWindow, true)
	if err != nil {
		return domain.Wave{}, fmt.Errorf("TournamentAdminResultPostgres - map no-show Wave: %w", err)
	}
	return record.Wave, nil
}

func operatorReadinessMatchesMembers(
	members []sqlc.WaveMember,
	readiness []sqlc.WaveReadiness,
	waveID uuid.UUID,
	rosterID uuid.UUID,
	windowID uuid.UUID,
) bool {
	if waveID == uuid.Nil || rosterID == uuid.Nil || windowID == uuid.Nil ||
		len(members) == 0 || len(readiness) != len(members) {
		return false
	}
	memberIDs, ok := operatorWaveMemberIDs(members, waveID, rosterID)
	if !ok {
		return false
	}
	return operatorReadinessCoversMembers(readiness, memberIDs, waveID, rosterID, windowID)
}

func operatorWaveMemberIDs(
	members []sqlc.WaveMember,
	waveID uuid.UUID,
	rosterID uuid.UUID,
) (map[uuid.UUID]struct{}, bool) {
	memberIDs := make(map[uuid.UUID]struct{}, len(members))
	for _, member := range members {
		if member.WaveID != waveID || member.RosterID != rosterID || member.ParticipantID == uuid.Nil {
			return nil, false
		}
		if _, duplicate := memberIDs[member.ParticipantID]; duplicate {
			return nil, false
		}
		memberIDs[member.ParticipantID] = struct{}{}
	}
	return memberIDs, true
}

func operatorReadinessCoversMembers(
	readiness []sqlc.WaveReadiness,
	memberIDs map[uuid.UUID]struct{},
	waveID uuid.UUID,
	rosterID uuid.UUID,
	windowID uuid.UUID,
) bool {
	for _, item := range readiness {
		if !item.ReadyWindowID.Valid || item.ReadyWindowID.UUID != windowID ||
			item.WaveID != waveID || item.RosterID != rosterID {
			return false
		}
		if _, ok := memberIDs[item.ParticipantID]; !ok {
			return false
		}
		delete(memberIDs, item.ParticipantID)
	}
	return len(memberIDs) == 0
}
