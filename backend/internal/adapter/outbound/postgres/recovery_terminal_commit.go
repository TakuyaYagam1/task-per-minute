package postgres

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery"
)

func (repository *RecoveryTerminalPostgres) commitGameTimeout(
	ctx context.Context,
	querier *sqlc.Queries,
	snapshot recoveryTerminalSnapshot,
	plan recovery.DeadlineTerminalPlan,
) error {
	if snapshot.game == nil || len(snapshot.series) != 1 || plan.GameTimeout == nil || plan.ResultEvidence == nil {
		return domain.ErrInternal
	}
	input, err := recoveryGameSettlementInput(plan, snapshot)
	if err != nil {
		return err
	}
	_, changed, err := NewResultPostgres(repository.tx).Settle(ctx, input)
	if err != nil {
		return err
	}
	if !changed {
		return domain.ErrConflict
	}
	if err := createRecoveryRoute(ctx, querier, plan.Deadline, plan.GameTimeout.WaveRoute); err != nil {
		return err
	}
	return createRecoveryReceipt(ctx, querier, plan, "game_timeout_replay", plan.GameTimeout.ExpectedAuthorityRevision)
}

func recoveryGameSettlementInput(
	plan recovery.DeadlineTerminalPlan,
	snapshot recoveryTerminalSnapshot,
) (ResultSettlementInput, error) {
	record := plan.GameTimeout
	evidence := plan.ResultEvidence
	digest, err := recoveryPayloadDigest("game_timeout", map[string]any{
		"deadline_id": plan.Deadline.ID.String(), "game_id": record.Game.ID.String(),
		"reason": string(record.Game.ResultReason), "state": string(record.Game.State),
	})
	if err != nil {
		return ResultSettlementInput{}, err
	}
	return ResultSettlementInput{
		IDs: ResultSettlementIDs{
			CommitID: evidence.CommitID, ResultEventID: evidence.ResultEventID,
			ResultEventIdempotencyKey: evidence.ResultEventIdempotencyKey,
			GameResultRevisionID:      record.AttemptGameResultRevision.ID.UUID(),
			SeriesScoreRevisionID:     record.ScoreRevision.ID.UUID(),
			AuditEventID:              record.Evidence.AuditEventID, OutboxEventID: record.Evidence.OutboxEventID,
			OutboxIdempotencyKey: evidence.OutboxIdempotencyKey,
			ProjectionEvidenceID: record.Evidence.ProjectionRevisionID,
			CommitIdempotencyKey: evidence.CommitIdempotencyKey,
		},
		Scope: ResultScope{
			TournamentID: plan.Deadline.TournamentID, RosterID: plan.Deadline.RosterID,
			SeriesID: plan.Deadline.SeriesID, AttemptID: plan.Deadline.GameID,
		},
		GameState: record.Game.State, GameReason: record.Game.ResultReason,
		GameWinnerID: record.Game.WinnerID, Score: record.ScoreRevision.ScoreAfter,
		NextSeriesState: record.Series.Series.State, ActorKind: resultActorServer,
		ProjectionArtifactKinds: []domain.ArtifactKind{domain.ArtifactKindGameResult, domain.ArtifactKindSeriesScore},
		ProjectionPayloadDigest: digest, SettledAt: record.TerminalizedAt,
		ExpectedAttemptRevision: snapshot.game.GameAttempt.Revision,
		ExpectedAttemptState:    domain.GameState(snapshot.game.GameAttempt.State),
		ExpectedSeriesRevision:  snapshot.series[0].row.Revision,
		ExpectedSeriesState:     domain.SeriesState(snapshot.series[0].row.State),
	}, nil
}

func (repository *RecoveryTerminalPostgres) commitReadyWindow(
	ctx context.Context,
	querier *sqlc.Queries,
	snapshot recoveryTerminalSnapshot,
	plan recovery.DeadlineTerminalPlan,
) error {
	if snapshot.ready == nil || len(snapshot.series) != len(plan.ReadyWindow) {
		return domain.ErrInternal
	}
	first := plan.ReadyWindow[0]
	if err := ensurePreStartSwissRoundProof(ctx, repository.tx, plan.Deadline.TournamentID, first.Scope.SeriesID, first.ResolvedAt,
		swissRoundProofOrigin{mode: "normal_no_show", commandID: first.CommandID}); err != nil {
		return fmt.Errorf("ensure recovery Swiss proof: %w", err)
	}
	readiness, err := querier.ClearWaveReadinessHeads(ctx, sqlc.ClearWaveReadinessHeadsParams{
		UpdatedAt: tstz(plan.ReadyWindow[0].ResolvedAt), WaveID: plan.Deadline.WaveID,
		ReadyWindowID: nullableUUIDValue(plan.Deadline.ID),
	})
	if err != nil {
		return mapRepositoryWriteError("RecoveryTerminalPostgres - clear Wave readiness", err)
	}
	expectedReady := make(map[uuid.UUID]struct{})
	for _, member := range plan.Authority.ReadyWindow[0].Wave.Members {
		if member.Ready {
			expectedReady[member.ParticipantID] = struct{}{}
		}
	}
	if len(readiness) != len(expectedReady) {
		return fmt.Errorf("recovery readiness count changed: %w", domain.ErrConflict)
	}
	for _, member := range readiness {
		if _, found := expectedReady[member.ParticipantID]; !found {
			return fmt.Errorf("recovery readiness membership changed: %w", domain.ErrConflict)
		}
		delete(expectedReady, member.ParticipantID)
	}
	if _, err = querier.ExpireRecoveryReadyWindowCAS(ctx, sqlc.ExpireRecoveryReadyWindowCASParams{
		ReadyWindowID: plan.Deadline.ID, WaveID: plan.Deadline.WaveID,
		ExpectedRevisionID: plan.Deadline.ReadyWindowRevisionID, Deadline: tstz(plan.Deadline.DueAt),
	}); err != nil {
		return resultCASWriteError("expire ready window", err)
	}
	if _, err = querier.ExpireRecoveryWaveCAS(ctx, sqlc.ExpireRecoveryWaveCASParams{
		ExpiredAt: tstz(plan.ReadyWindow[0].ResolvedAt), WaveID: plan.Deadline.WaveID,
		TournamentID: plan.Deadline.TournamentID, RosterID: plan.Deadline.RosterID,
		ExpectedRevision: plan.Deadline.ExpectedRevision,
	}); err != nil {
		return resultCASWriteError("expire Wave", err)
	}
	bySeries := make(map[uuid.UUID]recoverySeriesSnapshot, len(snapshot.series))
	for _, item := range snapshot.series {
		bySeries[item.row.ID] = item
	}
	for index := range plan.ReadyWindow {
		resolution := plan.ReadyWindow[index]
		seriesSnapshot, ok := bySeries[resolution.Scope.SeriesID]
		if !ok {
			return domain.ErrConflict
		}
		if err := commitRecoveryNoShow(ctx, repository.tx, plan, resolution, plan.ReadyWindowEvidence[index], seriesSnapshot); err != nil {
			return fmt.Errorf("commit recovery no-show Series %d: %w", index, err)
		}
	}
	return createRecoveryReceipt(ctx, querier, plan, "ready_window_no_show", plan.Deadline.ExpectedRevision)
}

func commitRecoveryNoShow(
	ctx context.Context,
	tx *TxManager,
	plan recovery.DeadlineTerminalPlan,
	resolution gameusecase.NoShowResolution,
	evidence recovery.DeadlineNoShowEvidenceIDs,
	snapshot recoverySeriesSnapshot,
) error {
	querier := tx.Querier(ctx)
	if resolution.Validate() != nil || resolution.ExpectedAuthorityRevision != snapshot.row.Revision ||
		len(resolution.GameRevisions) == 0 {
		return fmt.Errorf("recovery no-show resolution lineage: %w", domain.ErrConflict)
	}
	rawGames := recoveryRawGames(snapshot)
	if _, err := querier.LockProjectionRoster(ctx, sqlc.LockProjectionRosterParams{
		TournamentID: plan.Deadline.TournamentID,
		RosterID:     plan.Deadline.RosterID,
	}); err != nil {
		return mapRepositoryWriteError("RecoveryTerminalPostgres - lock no-show projection roster", err)
	}
	if _, err := querier.LockProjectionRevisionSet(ctx, sqlc.LockProjectionRevisionSetParams{
		TournamentID: plan.Deadline.TournamentID,
		RosterID:     plan.Deadline.RosterID,
	}); err != nil {
		return mapRepositoryWriteError("RecoveryTerminalPostgres - lock no-show projection revisions", err)
	}
	source, err := querier.LockResultSourceProjection(ctx, sqlc.LockResultSourceProjectionParams{
		TournamentID: plan.Deadline.TournamentID,
		RosterID:     plan.Deadline.RosterID,
	})
	if err != nil {
		return mapRepositoryWriteError("RecoveryTerminalPostgres - lock no-show source projection", err)
	}
	resultEvent, err := createRecoveryNoShowResultEvent(ctx, querier, plan, resolution, evidence)
	if err != nil {
		return err
	}
	if err := createRecoveryNoShowGameRevisions(ctx, querier, plan, resolution, resultEvent.ID, rawGames, source); err != nil {
		return err
	}
	if err := createRecoveryNoShowSeriesEvidence(ctx, querier, plan, resolution, evidence, snapshot, resultEvent.ID, source); err != nil {
		return fmt.Errorf("create recovery Series evidence: %w", err)
	}
	if err := settleRecoveryNoShowGames(ctx, querier, plan, resolution, evidence, rawGames); err != nil {
		return fmt.Errorf("settle recovery no-show Games: %w", err)
	}
	if err := settleRecoveryNoShowSeries(ctx, querier, plan, resolution, snapshot); err != nil {
		return fmt.Errorf("settle recovery no-show Series: %w", err)
	}
	return publishResultProjection(ctx, tx, ResultSettlementInput{
		IDs: ResultSettlementIDs{
			GameResultRevisionID: resolution.GameRevisions[0].ID.UUID(),
			ProjectionEvidenceID: evidence.ProjectionEvidenceID,
		},
		Scope: ResultScope{TournamentID: plan.Deadline.TournamentID, RosterID: plan.Deadline.RosterID,
			SeriesID: resolution.Scope.SeriesID, AttemptID: resolution.GameRevisions[0].GameID},
		SettledAt: resolution.ResolvedAt,
	}, source)
}

func recoveryRawGames(snapshot recoverySeriesSnapshot) map[uuid.UUID]sqlc.GameAttempt {
	rawGames := make(map[uuid.UUID]sqlc.GameAttempt, len(snapshot.graph))
	for _, row := range snapshot.graph {
		rawGames[row.GameAttempt.ID] = row.GameAttempt
	}
	return rawGames
}

func createRecoveryNoShowResultEvent(
	ctx context.Context,
	querier *sqlc.Queries,
	plan recovery.DeadlineTerminalPlan,
	resolution gameusecase.NoShowResolution,
	evidence recovery.DeadlineNoShowEvidenceIDs,
) (sqlc.ResultEvent, error) {
	firstGame := resolution.GameRevisions[0]
	sequence, err := querier.AllocateResultEventSequence(ctx, sqlc.AllocateResultEventSequenceParams{
		AttemptID: firstGame.GameID, SeriesID: resolution.Scope.SeriesID,
		RosterID: plan.Deadline.RosterID, TournamentID: plan.Deadline.TournamentID,
	})
	if err != nil {
		return sqlc.ResultEvent{}, mapRepositoryWriteError(
			"RecoveryTerminalPostgres - allocate no-show result event sequence",
			err,
		)
	}
	resultEvent, err := querier.CreateResultEvent(ctx, sqlc.CreateResultEventParams{
		ID: evidence.ResultEventID, TournamentID: plan.Deadline.TournamentID,
		RosterID: plan.Deadline.RosterID, SeriesID: resolution.Scope.SeriesID,
		AttemptID: firstGame.GameID, ServerSequence: sequence,
		IdempotencyKey: evidence.ResultEventIdempotencyKey,
		ResultState:    string(firstGame.State), ResultReason: string(firstGame.Reason),
		OccurredAt: tstz(resolution.ResolvedAt), CreatedAt: tstz(resolution.ResolvedAt),
	})
	if err != nil {
		return sqlc.ResultEvent{}, mapRepositoryWriteError(
			"RecoveryTerminalPostgres - create no-show result event",
			err,
		)
	}
	return resultEvent, nil
}

func createRecoveryNoShowGameRevisions(
	ctx context.Context,
	querier *sqlc.Queries,
	plan recovery.DeadlineTerminalPlan,
	resolution gameusecase.NoShowResolution,
	resultEventID uuid.UUID,
	rawGames map[uuid.UUID]sqlc.GameAttempt,
	source sqlc.LockResultSourceProjectionRow,
) error {
	for _, revision := range resolution.GameRevisions {
		raw, ok := rawGames[revision.GameID]
		if !ok || raw.ResultRevisionID.Valid || revision.PreviousRevisionID != nil {
			return domain.ErrConflict
		}
		if _, err := querier.CreateOfficialResultRevision(ctx, sqlc.CreateOfficialResultRevisionParams{
			ID: revision.ID.UUID(), TournamentID: plan.Deadline.TournamentID, RosterID: plan.Deadline.RosterID,
			EntityKind: "game_attempt", EntityID: revision.GameID, SeriesID: resolution.Scope.SeriesID,
			GameAttemptID: nullableUUIDValue(revision.GameID), ResultEventID: resultEventID,
			RevisionNumber: 1, ResultState: string(revision.State), ResultReason: string(revision.Reason),
			CommandID: resolution.CommandID, ActorKind: resultActorServer,
			SourceProjectionRevisionID: source.ID, SourceProjectionRevision: source.RevisionNumber,
			CreatedAt: tstz(resolution.ResolvedAt),
		}); err != nil {
			return mapRepositoryWriteError("RecoveryTerminalPostgres - create no-show Game revision", err)
		}
	}
	return nil
}

func settleRecoveryNoShowGames(
	ctx context.Context,
	querier *sqlc.Queries,
	plan recovery.DeadlineTerminalPlan,
	resolution gameusecase.NoShowResolution,
	evidence recovery.DeadlineNoShowEvidenceIDs,
	rawGames map[uuid.UUID]sqlc.GameAttempt,
) error {
	for position, revision := range resolution.GameRevisions {
		if err := querier.CreateRecoveryNormalNoShowGame(ctx, sqlc.CreateRecoveryNormalNoShowGameParams{
			CommitID: evidence.CommitID, GameAttemptID: revision.GameID,
			GameResultRevisionID: revision.ID.UUID(), Position: int16(position + 1),
		}); err != nil {
			return mapRepositoryWriteError("RecoveryTerminalPostgres - link no-show Game", err)
		}
	}
	for _, revision := range resolution.GameRevisions {
		raw := rawGames[revision.GameID]
		if _, err := querier.CreateOfficialResultHead(ctx, sqlc.CreateOfficialResultHeadParams{
			EntityKind: "game_attempt", EntityID: revision.GameID, SeriesID: resolution.Scope.SeriesID,
			RosterID: plan.Deadline.RosterID, GameAttemptID: nullableUUIDValue(revision.GameID),
			CurrentRevisionID: revision.ID.UUID(), UpdatedAt: tstz(resolution.ResolvedAt),
		}); err != nil {
			return mapRepositoryWriteError("RecoveryTerminalPostgres - create no-show Game head", err)
		}
		if _, err := querier.SettleGameAttemptCAS(ctx, sqlc.SettleGameAttemptCASParams{
			ResultState: string(revision.State), ResultReason: optionalTrimmedString(string(revision.Reason)),
			ResultRevisionID: nullableUUIDValue(revision.ID.UUID()), SettledAt: tstz(resolution.ResolvedAt),
			AttemptID: revision.GameID, SeriesID: resolution.Scope.SeriesID, RosterID: plan.Deadline.RosterID,
			TournamentID: plan.Deadline.TournamentID, ExpectedRevision: raw.Revision, ExpectedState: raw.State,
		}); err != nil {
			return resultCASWriteError("settle no-show Game", err)
		}
	}
	return nil
}

func createRecoveryNoShowSeriesEvidence(
	ctx context.Context,
	querier *sqlc.Queries,
	plan recovery.DeadlineTerminalPlan,
	resolution gameusecase.NoShowResolution,
	evidence recovery.DeadlineNoShowEvidenceIDs,
	snapshot recoverySeriesSnapshot,
	resultEventID uuid.UUID,
	source sqlc.LockResultSourceProjectionRow,
) error {
	if resolution.ScoreRevision.PreviousRevisionID == nil ||
		resolution.ScoreRevision.PreviousRevisionID.UUID() != snapshot.scoreHead.CurrentRevisionID ||
		resolution.SeriesRevision.PreviousRevisionID != nil || snapshot.row.CurrentResultRevisionID.Valid {
		return domain.ErrConflict
	}
	previousAttempts, err := querier.LockSeriesScoreRevisionAttempts(ctx, sqlc.LockSeriesScoreRevisionAttemptsParams{
		ScoreRevisionID: snapshot.scoreHead.CurrentRevisionID, TournamentID: plan.Deadline.TournamentID,
		RosterID: plan.Deadline.RosterID, SeriesID: resolution.Scope.SeriesID,
	})
	if err != nil {
		return mapRepositoryWriteError("RecoveryTerminalPostgres - lock no-show score ledger", err)
	}
	if len(previousAttempts) != 0 {
		return domain.ErrConflict
	}
	if _, err := querier.CreateSeriesScoreRevision(ctx, sqlc.CreateSeriesScoreRevisionParams{
		ID: resolution.ScoreRevision.ID.UUID(), TournamentID: plan.Deadline.TournamentID,
		RosterID: plan.Deadline.RosterID, SeriesID: resolution.Scope.SeriesID,
		ResultEventID: nullableUUIDValue(resultEventID), PreviousRevisionID: nullableUUIDValue(snapshot.scoreHead.CurrentRevisionID),
		RevisionNumber:             snapshot.scoreHead.Revision + 1,
		Operation:                  "no_show",
		CommandID:                  resolution.CommandID,
		ActorKind:                  resultActorServer,
		SourceProjectionRevisionID: source.ID,
		SourceProjectionRevision:   source.RevisionNumber,
		FirstParticipantWins:       int16(resolution.ScoreRevision.Score.FirstParticipantWins),  //nolint:gosec // BO1/BO3 score is bounded by domain validation.
		SecondParticipantWins:      int16(resolution.ScoreRevision.Score.SecondParticipantWins), //nolint:gosec // BO1/BO3 score is bounded by domain validation.
		CreatedAt:                  tstz(resolution.ResolvedAt),
	}); err != nil {
		return mapRepositoryWriteError("RecoveryTerminalPostgres - create no-show score revision", err)
	}
	ledger, err := normalNoShowScoreEvidence(
		snapshot.graph,
		resolution.Scope.SeriesID,
		resolution.GameRevisions,
		resultEventID,
		tstz(resolution.ResolvedAt),
	)
	if err != nil {
		return err
	}
	for index, item := range ledger {
		if err = createScoreRevisionAttempt(
			ctx,
			querier,
			ResultScope{TournamentID: plan.Deadline.TournamentID, RosterID: plan.Deadline.RosterID,
				SeriesID: resolution.Scope.SeriesID, AttemptID: item.GameAttemptID},
			resolution.ScoreRevision.ID.UUID(),
			int16(index+1), //nolint:gosec // a BO1 or BO3 no-show has at most three slots.
			item,
		); err != nil {
			return err
		}
	}
	seriesReason := "series_cancelled"
	if resolution.Action == domain.NormalNoShowActionReopenWave {
		seriesReason = "score_complete"
	}
	if _, err := querier.CreateOfficialResultRevision(ctx, sqlc.CreateOfficialResultRevisionParams{
		ID: resolution.SeriesRevision.ID.UUID(), TournamentID: plan.Deadline.TournamentID,
		RosterID: plan.Deadline.RosterID, EntityKind: "series", EntityID: resolution.Scope.SeriesID,
		SeriesID: resolution.Scope.SeriesID, ResultEventID: resultEventID, RevisionNumber: 1,
		ResultState: string(resolution.SeriesRevision.State), ResultReason: seriesReason,
		CommandID: resolution.CommandID, ActorKind: resultActorServer,
		SourceProjectionRevisionID: source.ID, SourceProjectionRevision: source.RevisionNumber,
		WinnerID: nullableUUID(resolution.SeriesRevision.WinnerID), CreatedAt: tstz(resolution.ResolvedAt),
	}); err != nil {
		return mapRepositoryWriteError("RecoveryTerminalPostgres - create no-show Series revision", err)
	}
	if err := createRecoveryNoShowSideEvidence(ctx, querier, plan, resolution, evidence, resultEventID, source); err != nil {
		return err
	}
	if _, err := querier.CreateRecoveryNormalNoShowCommit(ctx, sqlc.CreateRecoveryNormalNoShowCommitParams{
		ID: evidence.CommitID, TournamentID: plan.Deadline.TournamentID, RosterID: plan.Deadline.RosterID,
		WaveID: plan.Deadline.WaveID, ReadyWindowID: plan.Deadline.ID,
		ReadyWindowRevisionID: plan.Deadline.ReadyWindowRevisionID, SeriesID: resolution.Scope.SeriesID,
		ResultEventID: resultEventID, SeriesScoreRevisionID: resolution.ScoreRevision.ID.UUID(),
		SeriesResultRevisionID: resolution.SeriesRevision.ID.UUID(), AuditEventID: evidence.AuditEventID,
		OutboxEventID: evidence.OutboxEventID, ProjectionEvidenceID: evidence.ProjectionEvidenceID,
		CommandID: resolution.CommandID, ExpectedAuthorityRevision: resolution.ExpectedAuthorityRevision,
		ExpectedWaveRevision: plan.Deadline.ExpectedRevision, Action: string(resolution.Action),
		ResolvedAt: tstz(resolution.ResolvedAt),
	}); err != nil {
		return mapRepositoryWriteError("RecoveryTerminalPostgres - create no-show commit", err)
	}
	return nil
}

func createRecoveryNoShowSideEvidence(
	ctx context.Context,
	querier *sqlc.Queries,
	plan recovery.DeadlineTerminalPlan,
	resolution gameusecase.NoShowResolution,
	evidence recovery.DeadlineNoShowEvidenceIDs,
	resultEventID uuid.UUID,
	source sqlc.LockResultSourceProjectionRow,
) error {
	target, ok := resultProjectionTarget(source, evidence.ProjectionEvidenceID)
	if !ok {
		return domain.ErrConflict
	}
	auditPayload, err := marshalJSON("RecoveryTerminalPostgres - no-show audit payload", map[string]any{
		"ready_window_id": plan.Deadline.ID.String(), "series_id": resolution.Scope.SeriesID.String(),
		"action": string(resolution.Action),
	})
	if err != nil {
		return err
	}
	outboxPayload, err := marshalJSON("RecoveryTerminalPostgres - no-show outbox payload", map[string]any{
		"result_event_id": resultEventID.String(), "series_id": resolution.Scope.SeriesID.String(),
		"tournament_id": plan.Deadline.TournamentID.String(),
	})
	if err != nil {
		return err
	}
	artifactKinds := []domain.ArtifactKind{
		domain.ArtifactKindGameResult, domain.ArtifactKindSeriesScore, domain.ArtifactKindSeriesResult,
	}
	artifactPayload := []string{string(artifactKinds[0]), string(artifactKinds[1]), string(artifactKinds[2])}
	artifactJSON, err := marshalJSON("RecoveryTerminalPostgres - no-show artifacts", artifactPayload)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(outboxPayload)
	if _, err = querier.CreateResultAuditEvent(ctx, sqlc.CreateResultAuditEventParams{
		ID: evidence.AuditEventID, TournamentID: plan.Deadline.TournamentID, RosterID: plan.Deadline.RosterID,
		SeriesID: nullableUUIDValue(resolution.Scope.SeriesID), ResultEventID: nullableUUIDValue(resultEventID),
		ActorKind: resultActorServer,
		Action:    resultOutboxTopic, Payload: auditPayload, OccurredAt: tstz(resolution.ResolvedAt),
		CreatedAt: tstz(resolution.ResolvedAt),
	}); err != nil {
		return mapRepositoryWriteError("RecoveryTerminalPostgres - create no-show audit", err)
	}
	if _, err = querier.CreateResultProjectionEvidence(ctx, sqlc.CreateResultProjectionEvidenceParams{
		ID: evidence.ProjectionEvidenceID, TournamentID: plan.Deadline.TournamentID,
		RosterID: plan.Deadline.RosterID, SeriesID: resolution.Scope.SeriesID,
		ResultEventID: resultEventID, ArtifactKinds: artifactJSON, PayloadDigest: digest[:],
		CreatedAt: tstz(resolution.ResolvedAt),
	}); err != nil {
		return mapRepositoryWriteError("RecoveryTerminalPostgres - create no-show projection evidence", err)
	}
	if _, err = querier.CreateResultOutboxEvent(ctx, sqlc.CreateResultOutboxEventParams{
		ID: evidence.OutboxEventID, TournamentID: plan.Deadline.TournamentID, RosterID: plan.Deadline.RosterID,
		ProjectionRevisionID: target.ID, ProjectionRevision: target.Revision,
		SeriesID: resolution.Scope.SeriesID, ResultEventID: resultEventID,
		ProjectionEvidenceID: evidence.ProjectionEvidenceID, IdempotencyKey: evidence.OutboxIdempotencyKey,
		Topic: resultOutboxTopic, Payload: outboxPayload, CreatedAt: tstz(resolution.ResolvedAt),
	}); err != nil {
		return mapRepositoryWriteError("RecoveryTerminalPostgres - create no-show outbox", err)
	}
	return nil
}

func settleRecoveryNoShowSeries(
	ctx context.Context,
	querier *sqlc.Queries,
	plan recovery.DeadlineTerminalPlan,
	resolution gameusecase.NoShowResolution,
	snapshot recoverySeriesSnapshot,
) error {
	if _, err := querier.CreateOfficialResultHead(ctx, sqlc.CreateOfficialResultHeadParams{
		EntityKind: "series", EntityID: resolution.Scope.SeriesID, SeriesID: resolution.Scope.SeriesID,
		RosterID: plan.Deadline.RosterID, CurrentRevisionID: resolution.SeriesRevision.ID.UUID(),
		UpdatedAt: tstz(resolution.ResolvedAt),
	}); err != nil {
		return mapRepositoryWriteError("RecoveryTerminalPostgres - create no-show Series head", err)
	}
	if _, err := querier.AdvanceSeriesScoreHeadCAS(ctx, sqlc.AdvanceSeriesScoreHeadCASParams{
		CurrentRevisionID: resolution.ScoreRevision.ID.UUID(), UpdatedAt: tstz(resolution.ResolvedAt),
		SeriesID: resolution.Scope.SeriesID, RosterID: plan.Deadline.RosterID,
		ExpectedRevisionID: snapshot.scoreHead.CurrentRevisionID, ExpectedRevision: snapshot.scoreHead.Revision,
	}); err != nil {
		return resultCASWriteError("advance no-show score head", err)
	}
	if _, err := querier.SettleSeriesCAS(ctx, sqlc.SettleSeriesCASParams{
		NextState:             string(resolution.Series.Series.State),
		FirstParticipantWins:  int16(resolution.Series.Series.Score.FirstParticipantWins),  //nolint:gosec // BO1/BO3 score is bounded by domain validation.
		SecondParticipantWins: int16(resolution.Series.Series.Score.SecondParticipantWins), //nolint:gosec // BO1/BO3 score is bounded by domain validation.
		WinnerID:              nullableUUID(resolution.Series.Series.WinnerID),
		ScoreRevisionID:       nullableUUIDValue(resolution.ScoreRevision.ID.UUID()),
		ResultRevisionID:      nullableUUIDValue(resolution.SeriesRevision.ID.UUID()), SettledAt: tstz(resolution.ResolvedAt),
		SeriesID: resolution.Scope.SeriesID, TournamentID: plan.Deadline.TournamentID,
		RosterID: plan.Deadline.RosterID, ExpectedRevision: snapshot.row.Revision,
		ExpectedState: snapshot.row.State, ExpectedScoreRevisionID: nullableUUIDValue(snapshot.scoreHead.CurrentRevisionID),
	}); err != nil {
		return resultCASWriteError("settle no-show Series", err)
	}
	return nil
}

func (repository *RecoveryTerminalPostgres) commitReconnectTimeout(
	ctx context.Context,
	querier *sqlc.Queries,
	snapshot recoveryTerminalSnapshot,
	plan recovery.DeadlineTerminalPlan,
) error {
	if snapshot.reconnect == nil || snapshot.lease == nil || plan.ReconnectTimeout == nil {
		return domain.ErrInternal
	}
	now := repository.clock.Now().Round(0).UTC()
	lease, err := repository.authority.LoadAuthority(ctx, plan.Deadline.TournamentID)
	if err != nil {
		return fmt.Errorf("reload execution authority: %w", err)
	}
	if lease == nil || !lease.Proves(snapshot.lease.Identity(), now) {
		return domain.ErrConflict
	}
	if err := expireRecoveryReconnectIntervals(ctx, querier, snapshot.authority.ReconnectTimeout.Reconnect,
		plan.ReconnectTimeout.ReconnectAuthority.Reconnect, plan.ReconnectTimeout.RecordedAt); err != nil {
		return err
	}
	transitionKind := "reconnect_interval_expired"
	if plan.ReconnectTimeout.Evidence != nil {
		transitionKind, err = repository.commitReconnectTerminalOutcome(ctx, querier, snapshot, plan)
		if err != nil {
			return err
		}
	}
	return createRecoveryReceipt(ctx, querier, plan, transitionKind, plan.ReconnectTimeout.ExpectedAuthorityRevision)
}

func (repository *RecoveryTerminalPostgres) commitReconnectTerminalOutcome(
	ctx context.Context,
	querier *sqlc.Queries,
	snapshot recoveryTerminalSnapshot,
	plan recovery.DeadlineTerminalPlan,
) (string, error) {
	if plan.ResultEvidence == nil {
		return "", domain.ErrInternal
	}
	input, err := recoveryReconnectSettlementInput(plan, snapshot)
	if err != nil {
		return "", err
	}
	_, changed, err := NewResultPostgres(repository.tx).Settle(ctx, input)
	if err != nil {
		return "", err
	}
	if !changed {
		return "", domain.ErrConflict
	}
	transitionKind := "reconnect_forfeit"
	if plan.ReconnectTimeout.ReplayRoute != nil {
		transitionKind = "reconnect_replay"
		if err := createRecoveryRoute(ctx, querier, plan.Deadline, *plan.ReconnectTimeout.ReplayRoute); err != nil {
			return "", err
		}
	}
	if err := repository.cancelRecoveryPause(ctx, querier, snapshot, plan); err != nil {
		return "", err
	}
	return transitionKind, nil
}

func (repository *RecoveryTerminalPostgres) cancelRecoveryPause(
	ctx context.Context,
	querier *sqlc.Queries,
	snapshot recoveryTerminalSnapshot,
	plan recovery.DeadlineTerminalPlan,
) error {
	if _, err := querier.CreateRecoveryPauseRevision(ctx, sqlc.CreateRecoveryPauseRevisionParams{
		ID: plan.PauseRevisionID, PauseID: plan.Deadline.PauseID,
		PreviousRevisionID: nullableUUIDValue(snapshot.reconnect.Pause.CurrentRevisionID),
		RevisionNumber:     snapshot.reconnect.Pause.Revision + 1,
		CreatedAt:          tstz(plan.ReconnectTimeout.RecordedAt),
	}); err != nil {
		return mapRepositoryWriteError("RecoveryTerminalPostgres - create pause revision", err)
	}
	if _, err := querier.CancelRecoveryPauseCAS(ctx, sqlc.CancelRecoveryPauseCASParams{
		CurrentRevisionID: plan.PauseRevisionID, ResolvedAt: tstz(plan.ReconnectTimeout.RecordedAt),
		PauseID: plan.Deadline.PauseID, ExpectedRevisionID: snapshot.reconnect.Pause.CurrentRevisionID,
		ExpectedRevision: snapshot.reconnect.Pause.Revision,
	}); err != nil {
		return resultCASWriteError("cancel reconnect pause", err)
	}
	return nil
}

func expireRecoveryReconnectIntervals(
	ctx context.Context,
	querier *sqlc.Queries,
	before []pause.PauseReconnectInterval,
	after []pause.PauseReconnectInterval,
	expiredAt time.Time,
) error {
	if len(before) != len(after) || !domain.IsValidServerTime(expiredAt) {
		return domain.ErrConflict
	}
	afterByID := make(map[uuid.UUID]pause.PauseReconnectInterval, len(after))
	for _, interval := range after {
		if _, duplicate := afterByID[interval.ID]; duplicate {
			return domain.ErrConflict
		}
		afterByID[interval.ID] = interval
	}
	for _, current := range before {
		next, ok := afterByID[current.ID]
		if !ok {
			return domain.ErrConflict
		}
		if reflect.DeepEqual(current, next) {
			continue
		}
		if current.State != pause.ReconnectStateOpen || current.Revision == math.MaxInt64 {
			return domain.ErrConflict
		}
		expected := current
		expected.State = pause.ReconnectStateExpired
		at := expiredAt
		expected.ClosedAt = &at
		expected.Revision++
		expected.UpdatedAt = expiredAt
		if !reflect.DeepEqual(expected, next) {
			return domain.ErrConflict
		}
		if _, err := querier.ExpireRecoveryReconnectIntervalCAS(ctx, sqlc.ExpireRecoveryReconnectIntervalCASParams{
			ExpiredAt: tstz(expiredAt), ReconnectIntervalID: current.ID, PauseID: current.PauseID,
			ParticipantID: current.ParticipantID, ExpectedRevision: current.Revision,
		}); err != nil {
			return resultCASWriteError("expire reconnect interval", err)
		}
	}
	return nil
}

func recoveryReconnectSettlementInput(
	plan recovery.DeadlineTerminalPlan,
	snapshot recoveryTerminalSnapshot,
) (ResultSettlementInput, error) {
	record := plan.ReconnectTimeout
	evidence := plan.ResultEvidence
	digest, err := recoveryPayloadDigest("reconnect_timeout", map[string]any{
		"deadline_id": plan.Deadline.ID.String(), "game_id": record.ReconnectAuthority.Game.ID.String(),
		"reason": string(record.ReconnectAuthority.Game.ResultReason), "state": string(record.ReconnectAuthority.Game.State),
	})
	if err != nil {
		return ResultSettlementInput{}, err
	}
	seriesResultID := uuid.Nil
	seriesResultReason := ""
	artifactKinds := []domain.ArtifactKind{domain.ArtifactKindGameResult, domain.ArtifactKindSeriesScore}
	if record.SeriesResultRevision != nil {
		seriesResultID = record.SeriesResultRevision.ID.UUID()
		seriesResultReason = "score_complete"
		artifactKinds = append(artifactKinds, domain.ArtifactKindSeriesResult)
	}
	return ResultSettlementInput{
		IDs: ResultSettlementIDs{
			CommitID: evidence.CommitID, ResultEventID: evidence.ResultEventID,
			ResultEventIdempotencyKey: evidence.ResultEventIdempotencyKey,
			GameResultRevisionID:      record.ScoreRevision.GameResultRevisionIDs[len(record.ScoreRevision.GameResultRevisionIDs)-1].UUID(),
			SeriesScoreRevisionID:     record.ScoreRevision.ID.UUID(), SeriesResultRevisionID: seriesResultID,
			AuditEventID: record.Evidence.AuditEventID, OutboxEventID: record.Evidence.OutboxEventID,
			OutboxIdempotencyKey: evidence.OutboxIdempotencyKey,
			ProjectionEvidenceID: record.Evidence.ProjectionRevisionID,
			CommitIdempotencyKey: evidence.CommitIdempotencyKey,
		},
		Scope: ResultScope{
			TournamentID: plan.Deadline.TournamentID, RosterID: plan.Deadline.RosterID,
			SeriesID: plan.Deadline.SeriesID, AttemptID: plan.Deadline.GameID,
		},
		GameState:  record.ReconnectAuthority.Game.State,
		GameReason: record.ReconnectAuthority.Game.ResultReason, GameWinnerID: record.ReconnectAuthority.Game.WinnerID,
		Score: record.ScoreRevision.ScoreAfter, NextSeriesState: record.ReconnectAuthority.Series.State,
		SeriesResultReason: seriesResultReason, SeriesWinnerID: record.ReconnectAuthority.Series.WinnerID,
		ActorKind: resultActorServer, ProjectionArtifactKinds: artifactKinds,
		ProjectionPayloadDigest: digest, SettledAt: record.RecordedAt,
		ExpectedAttemptRevision: snapshot.reconnect.GameAttempt.Revision,
		ExpectedAttemptState:    domain.GameState(snapshot.reconnect.GameAttempt.State),
		ExpectedSeriesRevision:  snapshot.series[0].row.Revision,
		ExpectedSeriesState:     domain.SeriesState(snapshot.series[0].row.State),
	}, nil
}

func createRecoveryRoute(
	ctx context.Context,
	querier *sqlc.Queries,
	deadline recovery.PendingDeadline,
	route gameusecase.WaveMemberRoute,
) error {
	_, err := querier.CreateRecoveryWaveMemberRoute(ctx, sqlc.CreateRecoveryWaveMemberRouteParams{
		ID: route.ID, TournamentID: deadline.TournamentID, RosterID: deadline.RosterID,
		WaveID: route.WaveID, SeriesID: route.SeriesID, SlotID: route.SlotID,
		GameAttemptID: route.GameID, Category: string(route.Category), RoutedAt: tstz(route.RoutedAt),
	})
	if err != nil {
		return mapRepositoryWriteError("RecoveryTerminalPostgres - create Wave member route", err)
	}
	return nil
}

func createRecoveryReceipt(
	ctx context.Context,
	querier *sqlc.Queries,
	plan recovery.DeadlineTerminalPlan,
	transitionKind string,
	expectedAuthorityRevision int64,
) error {
	params := sqlc.CreateRecoveryDeadlineReceiptParams{
		ID: plan.ReceiptID, CommandID: plan.CommandID, TransitionKind: transitionKind,
		DeadlineID: plan.Deadline.ID, ExpectedDeadlineRevision: plan.Deadline.ExpectedRevision,
		ExpectedAuthorityRevision: expectedAuthorityRevision, TournamentID: plan.Deadline.TournamentID,
		RosterID: plan.Deadline.RosterID, WaveID: plan.Deadline.WaveID,
		ResolvedAt: tstz(recoveryPlanTime(plan)),
	}
	switch plan.Deadline.Kind {
	case recovery.DeadlineKindGame:
		params.SeriesID = nullableUUIDValue(plan.Deadline.SeriesID)
		params.GameAttemptID = nullableUUIDValue(plan.Deadline.GameID)
		params.ResultCommitID = nullableUUIDValue(plan.ResultEvidence.CommitID)
		params.RouteEvidenceID = nullableUUIDValue(plan.GameTimeout.WaveRoute.ID)
	case recovery.DeadlineKindReadyWindow:
		params.ReadyWindowID = nullableUUIDValue(plan.Deadline.ID)
		params.NormalNoShowCommitIds = recoveryNoShowCommitIDs(plan)
	case recovery.DeadlineKindReconnect:
		params.SeriesID = nullableUUIDValue(plan.Deadline.SeriesID)
		params.GameAttemptID = nullableUUIDValue(plan.Deadline.GameID)
		params.PauseID = nullableUUIDValue(plan.Deadline.PauseID)
		params.ParticipantID = nullableUUIDValue(plan.Deadline.ParticipantID)
		if plan.ResultEvidence != nil {
			params.ResultCommitID = nullableUUIDValue(plan.ResultEvidence.CommitID)
		}
		if plan.ReconnectTimeout.ReplayRoute != nil {
			params.RouteEvidenceID = nullableUUIDValue(plan.ReconnectTimeout.ReplayRoute.ID)
		}
	default:
		return domain.ErrValidation
	}
	if _, err := querier.CreateRecoveryDeadlineReceipt(ctx, params); err != nil {
		return mapRepositoryWriteError("RecoveryTerminalPostgres - create deadline receipt", err)
	}
	return nil
}

func recoveryPlanTime(plan recovery.DeadlineTerminalPlan) time.Time {
	switch plan.Deadline.Kind {
	case recovery.DeadlineKindGame:
		return plan.GameTimeout.TerminalizedAt
	case recovery.DeadlineKindReadyWindow:
		return plan.ReadyWindow[0].ResolvedAt
	case recovery.DeadlineKindReconnect:
		return plan.ReconnectTimeout.RecordedAt
	default:
		return time.Time{}
	}
}

func recoveryPayloadDigest(operation string, value any) ([sha256.Size]byte, error) {
	payload, err := marshalJSON("RecoveryTerminalPostgres - "+operation+" payload", value)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(payload), nil
}
