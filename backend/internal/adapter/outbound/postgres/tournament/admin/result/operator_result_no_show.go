package result

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
)

func (r *TournamentAdminResultPostgres) CommitOperatorNoShow(
	ctx context.Context,
	command tournamentadmin.NoShowCommand,
	requestDigest [32]byte,
	resolution gameusecase.NoShowResolution,
) (*gameusecase.NoShowResolution, bool, error) {
	if ctx == nil || !r.available() || resolution.Validate() != nil {
		return nil, false, domain.ErrValidation
	}
	var committed *gameusecase.NoShowResolution
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		var err error
		committed, err = r.commitOperatorNoShow(txCtx, command, requestDigest, resolution)
		return err
	})
	if err != nil {
		return nil, false, err
	}
	if committed == nil {
		return nil, false, domain.ErrInternal
	}
	return committed, true, nil
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func (r *TournamentAdminResultPostgres) commitOperatorNoShow(
	ctx context.Context,
	command tournamentadmin.NoShowCommand,
	requestDigest [32]byte,
	resolution gameusecase.NoShowResolution,
) (*gameusecase.NoShowResolution, error) {
	querier := r.tx.Querier(ctx)
	if len(resolution.GameRevisions) == 0 {
		return nil, domain.ErrConflict
	}
	if err := lockTournamentResultScope(ctx, querier, command.TournamentID, uuid.Nil); err != nil {
		return nil, operatorResultLookupError("lock no-show result scope", err)
	}
	if _, err := querier.LockOperatorNoShowPublication(ctx, sqlc.LockOperatorNoShowPublicationParams{
		TournamentID: command.TournamentID, WaveID: command.WaveID, ReadyWindowID: command.WindowID, SeriesID: command.SeriesID,
	}); err != nil {
		return nil, operatorResultLookupError("lock no-show publication authority", err)
	}
	row, err := querier.LockOperatorNoShowSnapshot(ctx, sqlc.LockOperatorNoShowSnapshotParams{
		SeriesID: command.SeriesID, TournamentID: command.TournamentID,
		ExpectedAuthorityRevision: command.ExpectedAuthorityRevision,
		ExpectedSeriesState:       string(command.ExpectedSeriesState),
		WaveID:                    command.WaveID, ExpectedWaveRevisionID: command.ExpectedWaveRevisionID,
		ReadyWindowID: command.WindowID, ExpectedWindowRevisionID: command.ExpectedWindowRevisionID,
	})
	if err != nil {
		return nil, operatorResultLookupError("reload no-show snapshot", err)
	}
	snapshot, err := r.loadOperatorSeriesSnapshot(ctx, row.Series, row.SeriesScoreHead, "", 0)
	if err != nil {
		return nil, err
	}
	if _, err = querier.LockProjectionRoster(ctx, sqlc.LockProjectionRosterParams{
		TournamentID: command.TournamentID,
		RosterID:     snapshot.series.RosterID,
	}); err != nil {
		return nil, operatorResultLookupError("lock no-show projection roster", err)
	}
	if _, err = querier.LockProjectionRevisionSet(ctx, sqlc.LockProjectionRevisionSetParams{
		TournamentID: command.TournamentID,
		RosterID:     snapshot.series.RosterID,
	}); err != nil {
		return nil, operatorResultLookupError("lock no-show projection revisions", err)
	}
	anchor, err := querier.LockOperatorNoShowAttempt(ctx, sqlc.LockOperatorNoShowAttemptParams{
		AttemptID: resolution.GameRevisions[0].GameID, SeriesID: command.SeriesID, TournamentID: command.TournamentID,
	})
	if err != nil {
		return nil, operatorResultLookupError("lock no-show attempt", err)
	}
	if anchor != resolution.GameRevisions[0].GameID {
		return nil, domain.ErrConflict
	}
	sourceWave, err := r.loadOperatorNoShowWave(ctx, row)
	if err != nil {
		return nil, err
	}
	if err = validateOperatorNoShowCommit(command, resolution, snapshot); err != nil {
		return nil, err
	}
	if _, err = querier.AssertOperatorProjectionRevision(ctx, sqlc.AssertOperatorProjectionRevisionParams{
		ProjectionRevisionID: snapshot.projection.ID, TournamentID: command.TournamentID,
		RosterID: snapshot.series.RosterID, ProjectionRevision: snapshot.projection.RevisionNumber,
	}); err != nil {
		return nil, operatorResultLookupError("assert no-show projection", err)
	}
	ids := operatorNoShowEvidenceIDs(command.CommandID)
	if err := r.ensurePreStartSwissRoundProof(ctx, command.TournamentID, command.SeriesID, resolution.ResolvedAt, swissRoundProofOrigin{mode: "normal_no_show", commandID: command.CommandID}); err != nil {
		return nil, err
	}
	target, err := operatorResultProjectionTarget(snapshot, ids.projectionEvidenceID)
	if err != nil {
		return nil, err
	}
	sequence, err := querier.AllocateResultEventSequence(ctx, sqlc.AllocateResultEventSequenceParams{
		AttemptID: resolution.GameRevisions[0].GameID, SeriesID: command.SeriesID,
		RosterID: snapshot.series.RosterID, TournamentID: command.TournamentID,
	})
	if err != nil {
		return nil, fmt.Errorf("TournamentAdminResultPostgres - allocate no-show event sequence: %w", err)
	}
	if _, err = querier.CreateResultEvent(ctx, sqlc.CreateResultEventParams{
		ID: ids.resultEventID, TournamentID: command.TournamentID, RosterID: snapshot.series.RosterID,
		SeriesID: command.SeriesID, AttemptID: resolution.GameRevisions[0].GameID,
		ServerSequence: sequence, IdempotencyKey: ids.resultIdempotencyKey,
		ResultState: string(domain.GameStateCancelled), ResultReason: string(domain.GameResultReasonSeriesCancelled),
		OccurredAt: tstz(resolution.ResolvedAt), CreatedAt: tstz(resolution.ResolvedAt),
	}); err != nil {
		return nil, mapRepositoryWriteError("TournamentAdminResultPostgres - create no-show result event", err)
	}
	if err = r.createOperatorNoShowRevisions(ctx, querier, command, snapshot, resolution, ids.resultEventID); err != nil {
		return nil, err
	}
	if err = createOperatorResultSideEvidence(ctx, querier, operatorResultSideEvidence{
		ids: ids,
		scope: ResultScope{TournamentID: command.TournamentID, RosterID: snapshot.series.RosterID,
			SeriesID: command.SeriesID, AttemptID: resolution.GameRevisions[0].GameID},
		target:  target,
		actorID: command.Operator.ActorID,
		auditPayload: map[string]any{
			"action": string(resolution.Action), "command_id": command.CommandID.String(),
			"reason": command.Reason, "ready_window_id": command.WindowID.String(),
			"series_id": command.SeriesID.String(),
		},
		outboxPayload: map[string]any{
			"action": "no_show", "result_event_id": ids.resultEventID.String(),
			"series_id": command.SeriesID.String(), "tournament_id": command.TournamentID.String(),
		},
		artifacts: []domain.ArtifactKind{
			domain.ArtifactKindGameResult, domain.ArtifactKindSeriesScore,
			domain.ArtifactKindSeriesResult, domain.ArtifactKindStandings, domain.ArtifactKindBracket,
		},
		digest: requestDigest, createdAt: resolution.ResolvedAt,
	}); err != nil {
		return nil, err
	}
	if _, err = querier.CreateOperatorNoShowCommit(ctx, sqlc.CreateOperatorNoShowCommitParams{
		ID: ids.commitID, TournamentID: command.TournamentID, RosterID: snapshot.series.RosterID,
		WaveID: command.WaveID, ReadyWindowID: command.WindowID,
		ReadyWindowRevisionID: command.ExpectedWindowRevisionID, SeriesID: command.SeriesID,
		ResultEventID: ids.resultEventID, SeriesScoreRevisionID: resolution.ScoreRevision.ID.UUID(),
		SeriesResultRevisionID: resolution.SeriesRevision.ID.UUID(), AuditEventID: ids.auditEventID,
		OutboxEventID: ids.outboxEventID, ProjectionEvidenceID: ids.projectionEvidenceID,
		CommandID: command.CommandID, ExpectedAuthorityRevision: command.ExpectedAuthorityRevision,
		ExpectedWaveRevision: row.Wave.Revision, Action: string(resolution.Action),
		ResolvedAt: tstz(resolution.ResolvedAt),
	}); err != nil {
		return nil, mapRepositoryWriteError("TournamentAdminResultPostgres - create no-show commit", err)
	}
	for index, revision := range resolution.GameRevisions {
		if err = querier.CreateOperatorNoShowCommitGame(ctx, sqlc.CreateOperatorNoShowCommitGameParams{
			CommitID: ids.commitID, GameAttemptID: revision.GameID,
			GameResultRevisionID: revision.ID.UUID(), Position: int16(index + 1),
		}); err != nil {
			return nil, mapRepositoryWriteError("TournamentAdminResultPostgres - link no-show Game", err)
		}
	}
	if err = r.advanceOperatorNoShowState(
		ctx,
		querier,
		command,
		resolution,
		snapshot,
		sourceWave,
		row.Wave.Revision,
	); err != nil {
		return nil, err
	}
	if err = r.publishOperatorResultProjection(ctx, r.tx,
		ResultScope{TournamentID: command.TournamentID, RosterID: snapshot.series.RosterID, SeriesID: command.SeriesID, AttemptID: resolution.GameRevisions[0].GameID},
		snapshot, target, resolution.GameRevisions[0].ID.UUID(), resolution.ResolvedAt); err != nil {
		return nil, err
	}
	if err = r.createOperatorResultCommand(
		ctx, command, command.CommandScope, command.SeriesID, snapshot.series.RosterID,
		tournamentadmin.OperatorResultActionNoShow, command.ExpectedAuthorityRevision,
		requestDigest, ids.commitID, ids.resultEventID, resolution.ResolvedAt,
	); err != nil {
		return nil, err
	}
	stored := resolution
	return &stored, nil
}

func (r *TournamentAdminResultPostgres) createOperatorNoShowRevisions(
	ctx context.Context,
	querier *sqlc.Queries,
	command tournamentadmin.NoShowCommand,
	snapshot operatorSeriesSnapshot,
	resolution gameusecase.NoShowResolution,
	resultEventID uuid.UUID,
) error {
	for _, revision := range resolution.GameRevisions {
		if _, err := querier.CreateOfficialResultRevision(ctx, sqlc.CreateOfficialResultRevisionParams{
			ID: revision.ID.UUID(), TournamentID: resolution.Scope.TournamentID,
			RosterID: snapshot.series.RosterID, EntityKind: "game_attempt", EntityID: revision.GameID,
			SeriesID: resolution.Scope.SeriesID, GameAttemptID: nullableUUIDValue(revision.GameID),
			ResultEventID: resultEventID, RevisionNumber: 1, ResultState: string(revision.State),
			ResultReason: string(revision.Reason), CommandID: command.CommandID,
			ActorKind: resultActorOperator, ActorID: nullableUUIDValue(command.Operator.ActorID),
			SourceProjectionRevisionID: snapshot.projection.ID, SourceProjectionRevision: snapshot.projection.RevisionNumber,
			CreatedAt: tstz(resolution.ResolvedAt),
		}); err != nil {
			return mapRepositoryWriteError("TournamentAdminResultPostgres - create no-show Game revision", err)
		}
	}
	previousAttempts, err := querier.LockSeriesScoreRevisionAttempts(ctx, sqlc.LockSeriesScoreRevisionAttemptsParams{
		ScoreRevisionID: snapshot.scoreHead.CurrentRevisionID, TournamentID: resolution.Scope.TournamentID,
		RosterID: snapshot.series.RosterID, SeriesID: resolution.Scope.SeriesID,
	})
	if err != nil {
		return mapRepositoryWriteError("TournamentAdminResultPostgres - lock no-show score ledger", err)
	}
	if len(previousAttempts) != 0 {
		return domain.ErrConflict
	}
	if _, err := querier.CreateSeriesScoreRevision(ctx, sqlc.CreateSeriesScoreRevisionParams{
		ID: resolution.ScoreRevision.ID.UUID(), TournamentID: resolution.Scope.TournamentID,
		RosterID: snapshot.series.RosterID, SeriesID: resolution.Scope.SeriesID,
		ResultEventID:              nullableUUIDValue(resultEventID),
		PreviousRevisionID:         nullableUUIDValue(snapshot.scoreHead.CurrentRevisionID),
		RevisionNumber:             snapshot.scoreHead.Revision + 1,
		Operation:                  "no_show",
		CommandID:                  command.CommandID,
		ActorKind:                  resultActorOperator,
		ActorID:                    nullableUUIDValue(command.Operator.ActorID),
		SourceProjectionRevisionID: snapshot.projection.ID,
		SourceProjectionRevision:   snapshot.projection.RevisionNumber,
		FirstParticipantWins:       int16(resolution.ScoreRevision.Score.FirstParticipantWins),  //nolint:gosec // BO1 and BO3 scores are bounded to 0..2.
		SecondParticipantWins:      int16(resolution.ScoreRevision.Score.SecondParticipantWins), //nolint:gosec // BO1 and BO3 scores are bounded to 0..2.
		CreatedAt:                  tstz(resolution.ResolvedAt),
	}); err != nil {
		return mapRepositoryWriteError("TournamentAdminResultPostgres - create no-show score revision", err)
	}
	evidence, err := normalNoShowScoreEvidence(
		snapshot.graph,
		resolution.Scope.SeriesID,
		resolution.GameRevisions,
		resultEventID,
		tstz(resolution.ResolvedAt),
	)
	if err != nil {
		return err
	}
	for index, item := range evidence {
		if err = createScoreRevisionAttempt(
			ctx,
			querier,
			ResultScope{TournamentID: resolution.Scope.TournamentID, RosterID: snapshot.series.RosterID,
				SeriesID: resolution.Scope.SeriesID, AttemptID: item.GameAttemptID},
			resolution.ScoreRevision.ID.UUID(),
			int16(index+1),
			item,
		); err != nil {
			return err
		}
	}
	seriesReason := domain.SeriesResultReasonSeriesCancelled
	if resolution.Action == domain.NormalNoShowActionReopenWave {
		seriesReason = domain.SeriesResultReasonScoreComplete
	}
	if _, err := querier.CreateOfficialResultRevision(ctx, sqlc.CreateOfficialResultRevisionParams{
		ID: resolution.SeriesRevision.ID.UUID(), TournamentID: resolution.Scope.TournamentID,
		RosterID: snapshot.series.RosterID, EntityKind: "series", EntityID: resolution.Scope.SeriesID,
		SeriesID: resolution.Scope.SeriesID, ResultEventID: resultEventID, RevisionNumber: 1,
		ResultState: string(resolution.SeriesRevision.State), ResultReason: string(seriesReason),
		CommandID: command.CommandID, ActorKind: resultActorOperator, ActorID: nullableUUIDValue(command.Operator.ActorID),
		SourceProjectionRevisionID: snapshot.projection.ID, SourceProjectionRevision: snapshot.projection.RevisionNumber,
		WinnerID: nullableUUID(resolution.SeriesRevision.WinnerID), CreatedAt: tstz(resolution.ResolvedAt),
	}); err != nil {
		return mapRepositoryWriteError("TournamentAdminResultPostgres - create no-show Series revision", err)
	}
	return nil
}

func (r *TournamentAdminResultPostgres) advanceOperatorNoShowState(
	ctx context.Context,
	querier *sqlc.Queries,
	command tournamentadmin.NoShowCommand,
	resolution gameusecase.NoShowResolution,
	snapshot operatorSeriesSnapshot,
	sourceWave domain.Wave,
	waveRevision int64,
) error {
	for _, revision := range resolution.GameRevisions {
		raw := snapshot.attempts[revision.GameID]
		if _, err := querier.CreateOfficialResultHead(ctx, sqlc.CreateOfficialResultHeadParams{
			EntityKind: "game_attempt", EntityID: revision.GameID, SeriesID: command.SeriesID,
			RosterID: snapshot.series.RosterID, GameAttemptID: nullableUUIDValue(revision.GameID),
			CurrentRevisionID: revision.ID.UUID(), UpdatedAt: tstz(resolution.ResolvedAt),
		}); err != nil {
			return mapRepositoryWriteError("TournamentAdminResultPostgres - create no-show Game head", err)
		}
		if _, err := querier.SettleGameAttemptCAS(ctx, sqlc.SettleGameAttemptCASParams{
			ResultState: string(revision.State), ResultReason: optionalTrimmedString(string(revision.Reason)),
			ResultRevisionID: nullableUUIDValue(revision.ID.UUID()), SettledAt: tstz(resolution.ResolvedAt),
			AttemptID: revision.GameID, SeriesID: command.SeriesID, RosterID: snapshot.series.RosterID,
			TournamentID: command.TournamentID, ExpectedRevision: raw.Revision, ExpectedState: raw.State,
		}); err != nil {
			return resultCASWriteError("settle operator no-show Game", err)
		}
	}
	if _, err := querier.CreateOfficialResultHead(ctx, sqlc.CreateOfficialResultHeadParams{
		EntityKind: "series", EntityID: command.SeriesID, SeriesID: command.SeriesID,
		RosterID: snapshot.series.RosterID, CurrentRevisionID: resolution.SeriesRevision.ID.UUID(),
		UpdatedAt: tstz(resolution.ResolvedAt),
	}); err != nil {
		return mapRepositoryWriteError("TournamentAdminResultPostgres - create no-show Series head", err)
	}
	if _, err := querier.AdvanceSeriesScoreHeadCAS(ctx, sqlc.AdvanceSeriesScoreHeadCASParams{
		CurrentRevisionID: resolution.ScoreRevision.ID.UUID(), UpdatedAt: tstz(resolution.ResolvedAt),
		SeriesID: command.SeriesID, RosterID: snapshot.series.RosterID,
		ExpectedRevisionID: snapshot.scoreHead.CurrentRevisionID, ExpectedRevision: snapshot.scoreHead.Revision,
	}); err != nil {
		return resultCASWriteError("advance operator no-show score head", err)
	}
	if _, err := querier.SettleSeriesCAS(ctx, sqlc.SettleSeriesCASParams{
		NextState:             string(resolution.Series.Series.State),
		FirstParticipantWins:  int16(resolution.Series.Series.Score.FirstParticipantWins),  //nolint:gosec // BO1 and BO3 scores are bounded to 0..2.
		SecondParticipantWins: int16(resolution.Series.Series.Score.SecondParticipantWins), //nolint:gosec // BO1 and BO3 scores are bounded to 0..2.
		WinnerID:              nullableUUID(resolution.Series.Series.WinnerID),
		ScoreRevisionID:       nullableUUIDValue(resolution.ScoreRevision.ID.UUID()),
		ResultRevisionID:      nullableUUIDValue(resolution.SeriesRevision.ID.UUID()),
		SettledAt:             tstz(resolution.ResolvedAt), SeriesID: command.SeriesID,
		TournamentID: command.TournamentID, RosterID: snapshot.series.RosterID,
		ExpectedRevision: snapshot.series.Revision, ExpectedState: snapshot.series.State,
		ExpectedScoreRevisionID: nullableUUIDValue(snapshot.scoreHead.CurrentRevisionID),
	}); err != nil {
		return resultCASWriteError("settle operator no-show Series", err)
	}
	readiness, err := querier.ClearWaveReadinessHeads(ctx, sqlc.ClearWaveReadinessHeadsParams{
		UpdatedAt: tstz(resolution.ResolvedAt), WaveID: command.WaveID,
		ReadyWindowID: nullableUUIDValue(command.WindowID),
	})
	if err != nil {
		return mapRepositoryWriteError("TournamentAdminResultPostgres - clear no-show readiness", err)
	}
	expectedReady := 0
	for _, member := range sourceWave.Members {
		if member.Ready {
			expectedReady++
		}
	}
	if len(readiness) != expectedReady {
		return domain.ErrConflict
	}
	if _, err = querier.ExpireRecoveryReadyWindowCAS(ctx, sqlc.ExpireRecoveryReadyWindowCASParams{
		ReadyWindowID: command.WindowID, WaveID: command.WaveID,
		ExpectedRevisionID: command.ExpectedWindowRevisionID,
		Deadline:           tstz(resolution.Wave.ReadyWindow.Deadline),
	}); err != nil {
		return resultCASWriteError("expire operator ready window", err)
	}
	if _, err = querier.ExpireRecoveryWaveCAS(ctx, sqlc.ExpireRecoveryWaveCASParams{
		ExpiredAt: tstz(resolution.ResolvedAt), WaveID: command.WaveID,
		TournamentID: command.TournamentID, RosterID: snapshot.series.RosterID,
		ExpectedRevision: waveRevision,
	}); err != nil {
		return resultCASWriteError("expire operator Wave", err)
	}
	return nil
}

func validateOperatorNoShowCommit(
	command tournamentadmin.NoShowCommand,
	resolution gameusecase.NoShowResolution,
	snapshot operatorSeriesSnapshot,
) error {
	if !operatorNoShowIdentityMatches(command, resolution) ||
		!operatorNoShowRevisionSetMatches(command, resolution, snapshot) ||
		!operatorNoShowGameRevisionsMatch(command, resolution, snapshot) ||
		!operatorNoShowHeadsMatch(resolution, snapshot) {
		return domain.ErrConflict
	}
	return nil
}

func operatorNoShowIdentityMatches(
	command tournamentadmin.NoShowCommand,
	resolution gameusecase.NoShowResolution,
) bool {
	return resolution.Scope == (domain.NormalNoShowScope{
		TournamentID: command.TournamentID, WaveID: command.WaveID,
		WindowID: command.WindowID, SeriesID: command.SeriesID,
	}) && resolution.CommandID == command.CommandID &&
		resolution.ExpectedAuthorityRevision == command.ExpectedAuthorityRevision
}

func operatorNoShowRevisionSetMatches(
	command tournamentadmin.NoShowCommand,
	resolution gameusecase.NoShowResolution,
	snapshot operatorSeriesSnapshot,
) bool {
	return snapshot.series.Revision == command.ExpectedAuthorityRevision && len(resolution.GameRevisions) > 0 &&
		resolution.ScoreRevision.ID.UUID() == command.ScoreRevisionID &&
		resolution.SeriesRevision.ID.UUID() == command.SeriesResultRevisionID
}

func operatorNoShowGameRevisionsMatch(
	command tournamentadmin.NoShowCommand,
	resolution gameusecase.NoShowResolution,
	snapshot operatorSeriesSnapshot,
) bool {
	if len(resolution.GameRevisions) != len(command.GameResultRevisionIDs) {
		return false
	}
	for index, revision := range resolution.GameRevisions {
		if revision.ID.UUID() != command.GameResultRevisionIDs[index] {
			return false
		}
		raw, ok := snapshot.attempts[revision.GameID]
		if !ok || raw.ResultRevisionID.Valid ||
			(domain.GameState(raw.State) != domain.GameStatePlanned && domain.GameState(raw.State) != domain.GameStateReady) {
			return false
		}
	}
	return true
}

func operatorNoShowHeadsMatch(resolution gameusecase.NoShowResolution, snapshot operatorSeriesSnapshot) bool {
	return resolution.ScoreRevision.PreviousRevisionID != nil &&
		resolution.ScoreRevision.PreviousRevisionID.UUID() == snapshot.scoreHead.CurrentRevisionID &&
		resolution.SeriesRevision.PreviousRevisionID == nil && !snapshot.series.CurrentResultRevisionID.Valid
}
