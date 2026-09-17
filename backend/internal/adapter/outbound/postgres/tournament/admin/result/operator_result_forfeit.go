package result

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/result"
)

func (r *TournamentAdminResultPostgres) CommitOperatorForfeit(
	ctx context.Context,
	command resultusecase.ForfeitCommand,
	requestDigest [32]byte,
	resolution gameusecase.ForfeitResolution,
) (*gameusecase.ForfeitResolution, bool, error) {
	if ctx == nil || !r.available() || resolution.Validate() != nil {
		return nil, false, domain.ErrValidation
	}
	var committed *gameusecase.ForfeitResolution
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		var err error
		committed, err = r.commitOperatorForfeit(txCtx, command, requestDigest, resolution)
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

func (r *TournamentAdminResultPostgres) commitOperatorForfeit(
	ctx context.Context,
	command resultusecase.ForfeitCommand,
	requestDigest [32]byte,
	resolution gameusecase.ForfeitResolution,
) (*gameusecase.ForfeitResolution, error) {
	querier := r.tx.Querier(ctx)
	if err := lockTournamentResultScope(ctx, querier, command.TournamentID, uuid.Nil); err != nil {
		return nil, operatorResultLookupError("lock forfeit result scope", err)
	}
	expectedGameID := uuid.NullUUID{}
	if resolution.Game != nil {
		expectedGameID = nullableUUIDValue(resolution.Game.ID)
	} else if command.ExpectedGame != nil {
		expectedGameID = nullableUUIDValue(command.ExpectedGame.GameID)
	}
	lockedAttemptID, err := querier.LockOperatorForfeitAttempt(ctx, sqlc.LockOperatorForfeitAttemptParams{
		TournamentID:   command.TournamentID,
		SeriesID:       command.SeriesID,
		ExpectedGameID: expectedGameID,
	})
	if err != nil {
		return nil, operatorResultLookupError("lock forfeit attempt", err)
	}
	if lockedAttemptID == uuid.Nil || (resolution.Game != nil && lockedAttemptID != resolution.Game.ID) {
		return nil, domain.ErrConflict
	}
	row, snapshot, err := r.reloadOperatorForfeitSnapshot(ctx, command)
	if err != nil {
		return nil, err
	}
	if err = validateOperatorForfeitCommit(command, resolution, snapshot); err != nil {
		return nil, err
	}
	if _, err = querier.AssertOperatorProjectionRevision(ctx, sqlc.AssertOperatorProjectionRevisionParams{
		ProjectionRevisionID: snapshot.projection.ID, TournamentID: command.TournamentID,
		RosterID: snapshot.series.RosterID, ProjectionRevision: snapshot.projection.RevisionNumber,
	}); err != nil {
		return nil, operatorResultLookupError("assert forfeit projection", err)
	}
	ids := operatorForfeitEvidenceIDs(command)
	if resolution.Game != nil {
		return r.commitLiveOperatorForfeit(ctx, command, requestDigest, resolution, snapshot, ids)
	}
	return r.commitPreStartOperatorForfeit(
		ctx,
		command,
		requestDigest,
		resolution,
		row,
		snapshot,
		ids,
		lockedAttemptID,
	)
}

func (r *TournamentAdminResultPostgres) reloadOperatorForfeitSnapshot(
	ctx context.Context,
	command resultusecase.ForfeitCommand,
) (sqlc.LockOperatorForfeitSnapshotRow, operatorSeriesSnapshot, error) {
	row, err := r.tx.Querier(ctx).LockOperatorForfeitSnapshot(
		ctx,
		sqlc.LockOperatorForfeitSnapshotParams{
			SeriesID: command.SeriesID, TournamentID: command.TournamentID,
			ExpectedAuthorityRevision: command.ExpectedAuthorityRevision,
		},
	)
	if err != nil {
		return sqlc.LockOperatorForfeitSnapshotRow{}, operatorSeriesSnapshot{},
			operatorResultLookupError("reload forfeit snapshot", err)
	}
	snapshot, err := r.loadOperatorSeriesSnapshot(
		ctx,
		row.Series,
		row.SeriesScoreHead,
		row.SeriesResumeState,
		row.SeriesResultRevision,
	)
	if err != nil {
		return sqlc.LockOperatorForfeitSnapshotRow{}, operatorSeriesSnapshot{}, err
	}
	return row, snapshot, nil
}

func (r *TournamentAdminResultPostgres) commitLiveOperatorForfeit(
	ctx context.Context,
	command resultusecase.ForfeitCommand,
	requestDigest [32]byte,
	resolution gameusecase.ForfeitResolution,
	snapshot operatorSeriesSnapshot,
	ids operatorResultEvidenceIDs,
) (*gameusecase.ForfeitResolution, error) {
	if resolution.Game == nil || resolution.GameRevision == nil || resolution.ExpectedGame == nil {
		return nil, domain.ErrConflict
	}
	raw, ok := snapshot.attempts[resolution.Game.ID]
	if !ok {
		return nil, domain.ErrConflict
	}
	actorID := resolution.ActorID
	winnerID := resolution.GameRevision.WinnerID
	record, changed, err := r.results.Settle(ctx, ResultSettlementInput{
		IDs: ResultSettlementIDs{
			CommitID: ids.commitID, ResultEventID: ids.resultEventID,
			ResultEventIdempotencyKey: ids.resultIdempotencyKey,
			GameResultRevisionID:      resolution.GameRevision.ID.UUID(),
			SeriesScoreRevisionID:     resolution.ScoreRevision.ID.UUID(),
			SeriesResultRevisionID:    resolution.SeriesRevision.ID.UUID(),
			AuditEventID:              ids.auditEventID, OutboxEventID: ids.outboxEventID,
			OutboxIdempotencyKey: ids.outboxIdempotencyKey,
			ProjectionEvidenceID: ids.projectionEvidenceID, CommitIdempotencyKey: command.CommandID,
		},
		Scope: ResultScope{
			TournamentID: command.TournamentID, RosterID: snapshot.series.RosterID,
			SeriesID: command.SeriesID, AttemptID: resolution.Game.ID,
		},
		GameState:  resolution.Game.State,
		GameReason: resolution.Game.ResultReason, GameWinnerID: &winnerID,
		Score: resolution.ScoreRevision.ScoreAfter, NextSeriesState: resolution.Series.Series.State,
		SeriesResultReason: string(domain.SeriesResultReasonScoreComplete),
		SeriesWinnerID:     resolution.Series.Series.WinnerID, ActorKind: resultActorOperator, ActorID: &actorID,
		ProjectionArtifactKinds: []domain.ArtifactKind{
			domain.ArtifactKindGameResult, domain.ArtifactKindSeriesScore,
			domain.ArtifactKindSeriesResult, domain.ArtifactKindStandings, domain.ArtifactKindBracket,
		},
		ProjectionPayloadDigest: requestDigest, SettledAt: resolution.ResolvedAt,
		ExpectedAttemptRevision: raw.Revision, ExpectedAttemptState: domain.GameState(raw.State),
		ExpectedSeriesRevision: snapshot.series.Revision,
		ExpectedSeriesState:    domain.SeriesState(snapshot.series.State),
	})
	if err != nil {
		return nil, err
	}
	if !changed || !operatorForfeitResultRecordMatches(record, resolution, ids) {
		return nil, domain.ErrConflict
	}
	if err = r.createOperatorResultCommand(
		ctx, command, command.CommandScope, command.SeriesID, snapshot.series.RosterID,
		resultusecase.OperatorResultActionForfeit, command.ExpectedAuthorityRevision,
		requestDigest, ids.commitID, ids.resultEventID, resolution.ResolvedAt,
	); err != nil {
		return nil, err
	}
	stored := resolution
	stored.ResolvedAt = record.Event.OccurredAt.Time.Round(0).UTC()
	stored.GameRevision.RecordedAt = stored.ResolvedAt
	stored.ScoreRevision.RecordedAt = stored.ResolvedAt
	stored.SeriesRevision.RecordedAt = stored.ResolvedAt
	stored.Evidence.RecordedAt = stored.ResolvedAt
	if stored.Validate() != nil {
		return nil, domain.ErrInternal
	}
	return &stored, nil
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func (r *TournamentAdminResultPostgres) commitPreStartOperatorForfeit(
	ctx context.Context,
	command resultusecase.ForfeitCommand,
	requestDigest [32]byte,
	resolution gameusecase.ForfeitResolution,
	row sqlc.LockOperatorForfeitSnapshotRow,
	snapshot operatorSeriesSnapshot,
	ids operatorResultEvidenceIDs,
	lockedAttemptID uuid.UUID,
) (*gameusecase.ForfeitResolution, error) {
	if err := r.ensurePreStartSwissRoundProof(ctx, command.TournamentID, command.SeriesID, resolution.ResolvedAt, swissRoundProofOrigin{mode: "pre_start_forfeit", commandID: command.CommandID}); err != nil {
		return nil, err
	}
	anchor, err := operatorForfeitAnchor(command, snapshot)
	if err != nil {
		return nil, err
	}
	if anchor.ID != lockedAttemptID {
		return nil, domain.ErrConflict
	}
	querier := r.tx.Querier(ctx)
	if _, err = querier.LockProjectionRoster(ctx, sqlc.LockProjectionRosterParams{
		TournamentID: command.TournamentID,
		RosterID:     snapshot.series.RosterID,
	}); err != nil {
		return nil, operatorResultLookupError("lock pre-start forfeit projection roster", err)
	}
	if _, err = querier.LockProjectionRevisionSet(ctx, sqlc.LockProjectionRevisionSetParams{
		TournamentID: command.TournamentID,
		RosterID:     snapshot.series.RosterID,
	}); err != nil {
		return nil, operatorResultLookupError("lock pre-start forfeit projection revisions", err)
	}
	target, err := operatorResultProjectionTarget(snapshot, ids.projectionEvidenceID)
	if err != nil {
		return nil, err
	}
	sequence, err := querier.AllocateResultEventSequence(ctx, sqlc.AllocateResultEventSequenceParams{
		AttemptID: anchor.ID, SeriesID: command.SeriesID, RosterID: snapshot.series.RosterID,
		TournamentID: command.TournamentID,
	})
	if err != nil {
		return nil, fmt.Errorf("TournamentAdminResultPostgres - allocate pre-start event sequence: %w", err)
	}
	if _, err = querier.CreateResultEvent(ctx, sqlc.CreateResultEventParams{
		ID: ids.resultEventID, TournamentID: command.TournamentID, RosterID: snapshot.series.RosterID,
		SeriesID: command.SeriesID, AttemptID: anchor.ID, ServerSequence: sequence,
		IdempotencyKey: ids.resultIdempotencyKey, ResultState: string(domain.GameStateCompleted),
		ResultReason: string(domain.GameResultReasonOperatorForfeit),
		WinnerID:     nullableUUID(resolution.Series.Series.WinnerID),
		OccurredAt:   tstz(resolution.ResolvedAt), CreatedAt: tstz(resolution.ResolvedAt),
	}); err != nil {
		return nil, mapRepositoryWriteError("TournamentAdminResultPostgres - create pre-start result event", err)
	}
	if _, err = querier.CreateSeriesScoreRevision(ctx, sqlc.CreateSeriesScoreRevisionParams{
		ID: resolution.ScoreRevision.ID.UUID(), TournamentID: command.TournamentID,
		RosterID: snapshot.series.RosterID, SeriesID: command.SeriesID,
		ResultEventID:              nullableUUIDValue(ids.resultEventID),
		PreviousRevisionID:         nullableUUIDValue(snapshot.scoreHead.CurrentRevisionID),
		RevisionNumber:             snapshot.scoreHead.Revision + 1,
		Operation:                  "pre_start_forfeit",
		CommandID:                  command.CommandID,
		ActorKind:                  resultActorOperator,
		ActorID:                    nullableUUIDValue(command.Operator.ActorID),
		SourceProjectionRevisionID: snapshot.projection.ID,
		SourceProjectionRevision:   snapshot.projection.RevisionNumber,
		FirstParticipantWins:       int16(resolution.ScoreRevision.ScoreAfter.FirstParticipantWins),  //nolint:gosec // BO1 and BO3 scores are bounded to 0..2.
		SecondParticipantWins:      int16(resolution.ScoreRevision.ScoreAfter.SecondParticipantWins), //nolint:gosec // BO1 and BO3 scores are bounded to 0..2.
		CreatedAt:                  tstz(resolution.ResolvedAt),
	}); err != nil {
		return nil, mapRepositoryWriteError("TournamentAdminResultPostgres - create pre-start score revision", err)
	}
	if _, err = querier.CreateOfficialResultRevision(ctx, sqlc.CreateOfficialResultRevisionParams{
		ID: resolution.SeriesRevision.ID.UUID(), TournamentID: command.TournamentID,
		RosterID: snapshot.series.RosterID, EntityKind: "series", EntityID: command.SeriesID,
		SeriesID: command.SeriesID, ResultEventID: ids.resultEventID, RevisionNumber: 1,
		ResultState:  string(domain.SeriesStateCompleted),
		ResultReason: string(domain.SeriesResultReasonScoreComplete),
		CommandID:    command.CommandID, ActorKind: resultActorOperator, ActorID: nullableUUIDValue(command.Operator.ActorID),
		SourceProjectionRevisionID: snapshot.projection.ID, SourceProjectionRevision: snapshot.projection.RevisionNumber,
		WinnerID: nullableUUID(resolution.Series.Series.WinnerID), CreatedAt: tstz(resolution.ResolvedAt),
	}); err != nil {
		return nil, mapRepositoryWriteError("TournamentAdminResultPostgres - create pre-start Series revision", err)
	}
	if err = createOperatorResultSideEvidence(ctx, querier, operatorResultSideEvidence{
		ids: ids,
		scope: ResultScope{TournamentID: command.TournamentID, RosterID: snapshot.series.RosterID,
			SeriesID: command.SeriesID, AttemptID: anchor.ID},
		target:  target,
		actorID: command.Operator.ActorID,
		auditPayload: map[string]any{
			"action": "forfeit", "basis": command.Basis, "command_id": command.CommandID.String(),
			"evidence_ids": command.EvidenceIDs, "forfeiting_participant": command.ForfeitingParticipantID.String(),
			"reason": command.Reason, "rule_id": command.RuleID, "series_id": command.SeriesID.String(),
		},
		outboxPayload: map[string]any{
			"action": "forfeit", "result_event_id": ids.resultEventID.String(),
			"series_id": command.SeriesID.String(), "tournament_id": command.TournamentID.String(),
		},
		artifacts: []domain.ArtifactKind{
			domain.ArtifactKindSeriesScore, domain.ArtifactKindSeriesResult,
			domain.ArtifactKindStandings, domain.ArtifactKindBracket,
		},
		digest: requestDigest, createdAt: resolution.ResolvedAt,
	}); err != nil {
		return nil, err
	}
	if _, err = querier.CreateOperatorForfeitCommit(ctx, sqlc.CreateOperatorForfeitCommitParams{
		ID: ids.commitID, TournamentID: command.TournamentID, RosterID: snapshot.series.RosterID,
		SeriesID: command.SeriesID, AnchorAttemptID: anchor.ID, ResultEventID: ids.resultEventID,
		SeriesScoreRevisionID:  resolution.ScoreRevision.ID.UUID(),
		SeriesResultRevisionID: resolution.SeriesRevision.ID.UUID(),
		AuditEventID:           ids.auditEventID, OutboxEventID: ids.outboxEventID,
		ProjectionEvidenceID: ids.projectionEvidenceID, CommandID: command.CommandID,
		ActorID: command.Operator.ActorID, ForfeitingParticipantID: command.ForfeitingParticipantID,
		ExpectedAuthorityRevision:  command.ExpectedAuthorityRevision,
		SourceProjectionRevisionID: snapshot.projection.ID,
		SourceProjectionRevision:   snapshot.projection.RevisionNumber,
		RuleID:                     command.RuleID, Reason: command.Reason,
		EvidenceIds: append([]uuid.UUID(nil), command.EvidenceIDs...), ResolvedAt: tstz(resolution.ResolvedAt),
	}); err != nil {
		return nil, mapRepositoryWriteError("TournamentAdminResultPostgres - create pre-start forfeit commit", err)
	}
	if resolution.Series.Series.WinnerID == nil || *resolution.Series.Series.WinnerID == uuid.Nil {
		return nil, domain.ErrConflict
	}
	if err = querier.CreateSeriesScoreRevisionAdjudication(ctx, sqlc.CreateSeriesScoreRevisionAdjudicationParams{
		ScoreRevisionID: resolution.ScoreRevision.ID.UUID(), TournamentID: command.TournamentID,
		RosterID: snapshot.series.RosterID, SeriesID: command.SeriesID,
		OperatorForfeitCommitID: ids.commitID, CommandID: command.CommandID,
		ActorID: command.Operator.ActorID, AnchorAttemptID: anchor.ID,
		ForfeitingParticipantID:    command.ForfeitingParticipantID,
		WinnerID:                   *resolution.Series.Series.WinnerID,
		SourceProjectionRevisionID: snapshot.projection.ID,
		SourceProjectionRevision:   snapshot.projection.RevisionNumber,
		CreatedAt:                  tstz(resolution.ResolvedAt),
	}); err != nil {
		return nil, mapRepositoryWriteError("TournamentAdminResultPostgres - create pre-start forfeit adjudication", err)
	}
	if err = advancePreStartOperatorForfeit(ctx, querier, resolution, row.Series, row.SeriesScoreHead); err != nil {
		return nil, err
	}
	if err = r.publishOperatorResultProjection(ctx, r.tx,
		ResultScope{TournamentID: command.TournamentID, RosterID: snapshot.series.RosterID, SeriesID: command.SeriesID, AttemptID: anchor.ID},
		snapshot, target, resolution.SeriesRevision.ID.UUID(), resolution.ResolvedAt); err != nil {
		return nil, err
	}
	if err = r.createOperatorResultCommand(
		ctx, command, command.CommandScope, command.SeriesID, snapshot.series.RosterID,
		resultusecase.OperatorResultActionForfeit, command.ExpectedAuthorityRevision,
		requestDigest, ids.commitID, ids.resultEventID, resolution.ResolvedAt,
	); err != nil {
		return nil, err
	}
	stored := resolution
	return &stored, nil
}

func advancePreStartOperatorForfeit(
	ctx context.Context,
	querier *sqlc.Queries,
	resolution gameusecase.ForfeitResolution,
	series sqlc.Series,
	scoreHead sqlc.SeriesScoreHead,
) error {
	if _, err := querier.CreateOfficialResultHead(ctx, sqlc.CreateOfficialResultHeadParams{
		EntityKind: "series", EntityID: series.ID, SeriesID: series.ID, RosterID: series.RosterID,
		CurrentRevisionID: resolution.SeriesRevision.ID.UUID(), UpdatedAt: tstz(resolution.ResolvedAt),
	}); err != nil {
		return mapRepositoryWriteError("TournamentAdminResultPostgres - create pre-start Series head", err)
	}
	if _, err := querier.AdvanceSeriesScoreHeadCAS(ctx, sqlc.AdvanceSeriesScoreHeadCASParams{
		CurrentRevisionID: resolution.ScoreRevision.ID.UUID(), UpdatedAt: tstz(resolution.ResolvedAt),
		SeriesID: series.ID, RosterID: series.RosterID,
		ExpectedRevisionID: scoreHead.CurrentRevisionID, ExpectedRevision: scoreHead.Revision,
	}); err != nil {
		return resultCASWriteError("advance pre-start forfeit score head", err)
	}
	if _, err := querier.SettleSeriesCAS(ctx, sqlc.SettleSeriesCASParams{
		NextState:             string(resolution.Series.Series.State),
		FirstParticipantWins:  int16(resolution.Series.Series.Score.FirstParticipantWins),  //nolint:gosec // BO1 and BO3 scores are bounded to 0..2.
		SecondParticipantWins: int16(resolution.Series.Series.Score.SecondParticipantWins), //nolint:gosec // BO1 and BO3 scores are bounded to 0..2.
		WinnerID:              nullableUUID(resolution.Series.Series.WinnerID),
		ScoreRevisionID:       nullableUUIDValue(resolution.ScoreRevision.ID.UUID()),
		ResultRevisionID:      nullableUUIDValue(resolution.SeriesRevision.ID.UUID()),
		SettledAt:             tstz(resolution.ResolvedAt), SeriesID: series.ID,
		TournamentID: series.TournamentID, RosterID: series.RosterID,
		ExpectedRevision: series.Revision, ExpectedState: series.State,
		ExpectedScoreRevisionID: nullableUUIDValue(scoreHead.CurrentRevisionID),
	}); err != nil {
		return resultCASWriteError("settle pre-start operator forfeit Series", err)
	}
	return nil
}

func operatorForfeitAnchor(
	command resultusecase.ForfeitCommand,
	snapshot operatorSeriesSnapshot,
) (sqlc.GameAttempt, error) {
	var anchor sqlc.GameAttempt
	switch {
	case command.ExpectedGame != nil:
		var ok bool
		anchor, ok = snapshot.attempts[command.ExpectedGame.GameID]
		if !ok || anchor.SlotID != command.ExpectedGame.SlotID ||
			int(anchor.AttemptNumber) != command.ExpectedGame.AttemptNo || anchor.State != string(command.ExpectedGame.State) {
			return sqlc.GameAttempt{}, domain.ErrConflict
		}
	case len(snapshot.graph) > 0:
		anchor = snapshot.graph[len(snapshot.graph)-1].GameAttempt
	default:
		return sqlc.GameAttempt{}, domain.ErrConflict
	}
	if anchor.SeriesID != snapshot.series.ID || anchor.RosterID != snapshot.series.RosterID ||
		domain.GameState(anchor.State) != domain.GameStatePlanned &&
			domain.GameState(anchor.State) != domain.GameStateReady || anchor.ResultRevisionID.Valid {
		return sqlc.GameAttempt{}, domain.ErrConflict
	}
	return anchor, nil
}

func validateOperatorForfeitCommit(
	command resultusecase.ForfeitCommand,
	resolution gameusecase.ForfeitResolution,
	snapshot operatorSeriesSnapshot,
) error {
	if !operatorForfeitIdentityMatches(command, resolution) ||
		!operatorForfeitRevisionSetMatches(command, resolution, snapshot) ||
		!operatorForfeitProjectionEvidenceMatches(command, resolution, snapshot) ||
		!operatorForfeitGameResolutionMatches(command, resolution) {
		return domain.ErrConflict
	}
	return nil
}

func operatorForfeitIdentityMatches(
	command resultusecase.ForfeitCommand,
	resolution gameusecase.ForfeitResolution,
) bool {
	return resolution.Source == gameusecase.SourceOperator && resolution.CommandID == command.CommandID &&
		resolution.Scope == (gameusecase.Scope{TournamentID: command.TournamentID, SeriesID: command.SeriesID}) &&
		resolution.ActorID == command.Operator.ActorID &&
		resolution.ForfeitingParticipantID == command.ForfeitingParticipantID &&
		resolution.ExpectedAuthorityRevision == command.ExpectedAuthorityRevision
}

func operatorForfeitRevisionSetMatches(
	command resultusecase.ForfeitCommand,
	resolution gameusecase.ForfeitResolution,
	snapshot operatorSeriesSnapshot,
) bool {
	return snapshot.series.Revision == command.ExpectedAuthorityRevision &&
		resolution.ScoreRevision.ID.UUID() == command.ScoreRevisionID &&
		resolution.SeriesRevision.ID.UUID() == command.SeriesResultRevisionID &&
		resolution.ScoreRevision.PreviousRevisionID != nil &&
		resolution.ScoreRevision.PreviousRevisionID.UUID() == snapshot.scoreHead.CurrentRevisionID &&
		resolution.SeriesRevision.PreviousRevisionID == nil && !snapshot.series.CurrentResultRevisionID.Valid
}

func operatorForfeitProjectionEvidenceMatches(
	command resultusecase.ForfeitCommand,
	resolution gameusecase.ForfeitResolution,
	snapshot operatorSeriesSnapshot,
) bool {
	return resolution.Evidence.SourceProjectionRevision == snapshot.projection.RevisionNumber &&
		resolution.Evidence.ProjectionRevision == snapshot.projection.RevisionNumber+1 &&
		resolution.Evidence.AuditEventID == command.AuditEventID &&
		resolution.Evidence.OutboxEventID == command.OutboxEventID &&
		resolution.Evidence.ProjectionRevisionID == command.ProjectionRevisionID
}

func operatorForfeitGameResolutionMatches(
	command resultusecase.ForfeitCommand,
	resolution gameusecase.ForfeitResolution,
) bool {
	if resolution.Game == nil {
		return resolution.GameRevision == nil && command.GameResultRevisionID == nil
	}
	return resolution.GameRevision != nil && command.GameResultRevisionID != nil &&
		resolution.GameRevision.ID.UUID() == *command.GameResultRevisionID &&
		resolution.Game.ID == resolution.GameRevision.GameID
}

func operatorForfeitResultRecordMatches(
	record *ResultCommitRecord,
	resolution gameusecase.ForfeitResolution,
	ids operatorResultEvidenceIDs,
) bool {
	return record != nil && record.SeriesRevision != nil && record.Commit.ID == ids.commitID &&
		record.Event.ID == ids.resultEventID && record.Event.ResultReason == string(gameusecase.SourceOperator.ResultReason()) &&
		record.GameRevision.ID == resolution.GameRevision.ID.UUID() &&
		record.ScoreRevision.ID == resolution.ScoreRevision.ID.UUID() &&
		record.SeriesRevision.ID == resolution.SeriesRevision.ID.UUID() &&
		record.Audit.ID == ids.auditEventID && record.Audit.ActorKind == resultActorOperator &&
		record.Audit.ActorID.Valid && record.Audit.ActorID.UUID == resolution.ActorID &&
		record.Outbox.ID == ids.outboxEventID && record.ProjectionEvidence.ID == ids.projectionEvidenceID
}
