package result

import (
	"context"
	"crypto/sha256"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func createResultEvidence(
	ctx context.Context,
	querier *sqlc.Queries,
	in ResultSettlementInput,
	attempt sqlc.LockResultAttemptRow,
	series sqlc.LockResultSeriesRow,
	source sqlc.LockResultSourceProjectionRow,
	resultSequence int64,
) error {
	resultEvent, err := querier.CreateResultEvent(ctx, sqlc.CreateResultEventParams{
		ID: in.IDs.ResultEventID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		SeriesID: in.Scope.SeriesID, AttemptID: in.Scope.AttemptID,
		SubmissionEventID: nullableUUIDValue(in.SubmissionEventID), ServerSequence: resultSequence,
		IdempotencyKey: in.IDs.ResultEventIdempotencyKey, ResultState: string(in.GameState),
		ResultReason: string(in.GameReason), WinnerID: nullableUUID(in.GameWinnerID),
		OccurredAt: tstz(in.SettledAt), CreatedAt: tstz(in.SettledAt),
	})
	if err != nil {
		return mapRepositoryWriteError("ResultPostgres - Settle - create result event", err)
	}

	_, err = querier.CreateOfficialResultRevision(ctx, sqlc.CreateOfficialResultRevisionParams{
		ID: in.IDs.GameResultRevisionID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		EntityKind: "game_attempt", EntityID: in.Scope.AttemptID, SeriesID: in.Scope.SeriesID,
		GameAttemptID: nullableUUIDValue(in.Scope.AttemptID), ResultEventID: resultEvent.ID,
		RevisionNumber: 1, ResultState: string(in.GameState), ResultReason: string(in.GameReason),
		CommandID: in.IDs.CommitIdempotencyKey, ActorKind: in.ActorKind, ActorID: nullableUUID(in.ActorID),
		SourceProjectionRevisionID: source.ID, SourceProjectionRevision: source.RevisionNumber,
		WinnerID: nullableUUID(in.GameWinnerID), CreatedAt: tstz(in.SettledAt),
	})
	if err != nil {
		return mapRepositoryWriteError("ResultPostgres - Settle - create Game revision", err)
	}

	previousAttempts, err := querier.LockSeriesScoreRevisionAttempts(ctx, sqlc.LockSeriesScoreRevisionAttemptsParams{
		ScoreRevisionID: series.ScoreHeadRevisionID, TournamentID: in.Scope.TournamentID,
		RosterID: in.Scope.RosterID, SeriesID: in.Scope.SeriesID,
	})
	if err != nil {
		return mapRepositoryWriteError("ResultPostgres - Settle - lock score attempt ledger", err)
	}
	position, err := nextScoreRevisionAttemptPosition(previousAttempts)
	if err != nil {
		return err
	}
	slotPosition, err := scoreSlotPosition(int32(attempt.SlotPosition))
	if err != nil {
		return err
	}
	_, err = querier.CreateSeriesScoreRevision(ctx, sqlc.CreateSeriesScoreRevisionParams{
		ID: in.IDs.SeriesScoreRevisionID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		SeriesID: in.Scope.SeriesID, ResultEventID: nullableUUIDValue(resultEvent.ID),
		PreviousRevisionID:         nullableUUIDValue(series.ScoreHeadRevisionID),
		RevisionNumber:             series.ScoreHeadRevision + 1,
		Operation:                  "append_attempt",
		CommandID:                  in.IDs.CommitIdempotencyKey,
		ActorKind:                  in.ActorKind,
		ActorID:                    nullableUUID(in.ActorID),
		CommandAttemptID:           nullableUUIDValue(in.Scope.AttemptID),
		SourceProjectionRevisionID: source.ID,
		SourceProjectionRevision:   source.RevisionNumber,
		FirstParticipantWins:       int16(in.Score.FirstParticipantWins),  //nolint:gosec // Game score validation bounds BO1 and BO3 scores to 0..2.
		SecondParticipantWins:      int16(in.Score.SecondParticipantWins), //nolint:gosec // Game score validation bounds BO1 and BO3 scores to 0..2.
		CreatedAt:                  tstz(in.SettledAt),
	})
	if err != nil {
		return mapRepositoryWriteError("ResultPostgres - Settle - create score revision", err)
	}
	if err = copyScoreRevisionAttempts(
		ctx,
		querier,
		in.Scope,
		in.IDs.SeriesScoreRevisionID,
		previousAttempts,
		nil,
		tstz(in.SettledAt),
	); err != nil {
		return err
	}
	if err = createScoreRevisionAttempt(ctx, querier, in.Scope, in.IDs.SeriesScoreRevisionID, position,
		scoreRevisionAttemptEvidence{
			SlotID:               attempt.SlotID,
			SlotPosition:         slotPosition,
			GameAttemptID:        in.Scope.AttemptID,
			AttemptNumber:        attempt.AttemptNumber,
			GameResultRevisionID: in.IDs.GameResultRevisionID,
			ResultEventID:        resultEvent.ID,
			ResultState:          string(in.GameState),
			ResultReason:         string(in.GameReason),
			WinnerID:             nullableUUID(in.GameWinnerID),
			OccurredAt:           tstz(in.SettledAt),
			CreatedAt:            tstz(in.SettledAt),
		},
	); err != nil {
		return err
	}

	seriesResultID := uuid.NullUUID{}
	if in.IDs.SeriesResultRevisionID != uuid.Nil {
		seriesResultID = nullableUUIDValue(in.IDs.SeriesResultRevisionID)
		_, err = querier.CreateOfficialResultRevision(ctx, sqlc.CreateOfficialResultRevisionParams{
			ID: in.IDs.SeriesResultRevisionID, TournamentID: in.Scope.TournamentID,
			RosterID: in.Scope.RosterID, EntityKind: "series", EntityID: in.Scope.SeriesID,
			SeriesID: in.Scope.SeriesID, ResultEventID: resultEvent.ID, RevisionNumber: 1,
			ResultState: string(in.NextSeriesState), ResultReason: in.SeriesResultReason,
			CommandID: in.IDs.CommitIdempotencyKey, ActorKind: in.ActorKind, ActorID: nullableUUID(in.ActorID),
			SourceProjectionRevisionID: source.ID, SourceProjectionRevision: source.RevisionNumber,
			WinnerID: nullableUUID(in.SeriesWinnerID), CreatedAt: tstz(in.SettledAt),
		})
		if err != nil {
			return mapRepositoryWriteError("ResultPostgres - Settle - create Series revision", err)
		}
	}

	target, ok := resultProjectionTarget(source, in.IDs.ProjectionEvidenceID)
	if !ok {
		return domain.ErrConflict
	}
	if err := createResultSideEvidence(ctx, querier, in, target, resultEvent.ID, seriesResultID); err != nil {
		return err
	}
	if err := createResultProjectionNodes(ctx, querier, in, series, resultEvent.ID, seriesResultID); err != nil {
		return err
	}
	return advanceResultHeads(ctx, querier, in, series, seriesResultID)
}

// createResultProjectionNodes records the immutable logical result evidence
// before mutable heads advance. The row payloads contain only the accepted
// server-owned result fields and every non-genesis score node points at the
// exact score head locked for this settlement.
func createResultProjectionNodes(
	ctx context.Context,
	querier *sqlc.Queries,
	in ResultSettlementInput,
	series sqlc.LockResultSeriesRow,
	resultEventID uuid.UUID,
	seriesResultID uuid.NullUUID,
) error {
	authorityID := resultProjectionAuthorityID(in.IDs.CommitID)
	if err := querier.CreateResultProjectionNodeAuthority(ctx, sqlc.CreateResultProjectionNodeAuthorityParams{
		ID: authorityID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		SourceKind: "result_commit", ResultCommitID: nullableUUIDValue(in.IDs.CommitID),
		CreatedAt: tstz(in.SettledAt),
	}); err != nil {
		return mapRepositoryWriteError("ResultPostgres - Settle - create projection authority", err)
	}

	gamePayload, err := marshalJSON("ResultPostgres - Settle - game projection node", map[string]any{
		"schema": "result-projection-game-result-node-v1", "result_event_id": resultEventID.String(),
		"revision_id": in.IDs.GameResultRevisionID.String(), "state": string(in.GameState),
		"reason": string(in.GameReason), "attempt_id": in.Scope.AttemptID.String(),
	})
	if err != nil {
		return err
	}
	if err = createResultProjectionNode(ctx, querier, sqlc.CreateResultProjectionNodeParams{
		ID: in.IDs.GameResultRevisionID, AuthorityID: authorityID, TournamentID: in.Scope.TournamentID,
		RosterID: in.Scope.RosterID, ArtifactKind: string(domain.ArtifactKindGameResult), EntityID: in.Scope.AttemptID,
		RevisionNumber: 1, Payload: gamePayload, PayloadDigest: digestBytes(gamePayload), CreatedAt: tstz(in.SettledAt),
	}); err != nil {
		return err
	}

	scorePayload, err := marshalJSON("ResultPostgres - Settle - score projection node", map[string]any{
		"schema": "result-projection-series-score-node-v1", "result_event_id": resultEventID.String(),
		"revision_id": in.IDs.SeriesScoreRevisionID.String(), "previous_revision_id": series.ScoreHeadRevisionID.String(),
		"series_id": in.Scope.SeriesID.String(), "first_wins": in.Score.FirstParticipantWins,
		"second_wins": in.Score.SecondParticipantWins,
	})
	if err != nil {
		return err
	}
	previousNodeID := series.ScoreHeadRevisionID
	if series.ScoreHeadRevision == 1 {
		stageNodes, err := querier.LockStageScoreGenesisNodes(ctx, sqlc.LockStageScoreGenesisNodesParams{
			ScoreRevisionID: series.ScoreHeadRevisionID, TournamentID: in.Scope.TournamentID,
			RosterID: in.Scope.RosterID, SeriesID: in.Scope.SeriesID,
		})
		if err != nil {
			return resultLookupError("Settle - lock stage score genesis", err)
		}
		if len(stageNodes) > 0 {
			previousNodeID, err = stageScoreGenesisNode(in.Scope, series.ScoreHeadRevisionID, stageNodes)
			if err != nil {
				return err
			}
		}
	}
	if err = createResultProjectionNode(ctx, querier, sqlc.CreateResultProjectionNodeParams{
		ID: in.IDs.SeriesScoreRevisionID, AuthorityID: authorityID, TournamentID: in.Scope.TournamentID,
		RosterID: in.Scope.RosterID, ArtifactKind: string(domain.ArtifactKindSeriesScore), EntityID: in.Scope.SeriesID,
		RevisionNumber: series.ScoreHeadRevision + 1, PreviousNodeID: nullableUUIDValue(previousNodeID),
		Payload: scorePayload, PayloadDigest: digestBytes(scorePayload), CreatedAt: tstz(in.SettledAt),
	}); err != nil {
		return err
	}
	if err = createResultProjectionDependency(ctx, querier, authorityID, in.IDs.GameResultRevisionID, in.IDs.SeriesScoreRevisionID, in.SettledAt); err != nil {
		return err
	}

	if !seriesResultID.Valid {
		return nil
	}
	seriesPayload, err := marshalJSON("ResultPostgres - Settle - series projection node", map[string]any{
		"schema": "result-projection-series-result-node-v1", "result_event_id": resultEventID.String(),
		"revision_id": seriesResultID.UUID.String(), "series_id": in.Scope.SeriesID.String(),
		"state": string(in.NextSeriesState), "reason": in.SeriesResultReason,
	})
	if err != nil {
		return err
	}
	if err = createResultProjectionNode(ctx, querier, sqlc.CreateResultProjectionNodeParams{
		ID: seriesResultID.UUID, AuthorityID: authorityID, TournamentID: in.Scope.TournamentID,
		RosterID: in.Scope.RosterID, ArtifactKind: string(domain.ArtifactKindSeriesResult), EntityID: in.Scope.SeriesID,
		RevisionNumber: 1, Payload: seriesPayload, PayloadDigest: digestBytes(seriesPayload), CreatedAt: tstz(in.SettledAt),
	}); err != nil {
		return err
	}
	return createResultProjectionDependency(ctx, querier, authorityID, in.IDs.SeriesScoreRevisionID, seriesResultID.UUID, in.SettledAt)
}

func stageScoreGenesisNode(scope ResultScope, revisionID uuid.UUID, nodes []sqlc.LockStageScoreGenesisNodesRow) (uuid.UUID, error) {
	if len(nodes) != 1 {
		return uuid.Nil, domain.ErrConflict
	}
	node := nodes[0]
	if node.ID == uuid.Nil || node.TournamentID != scope.TournamentID || node.RosterID != scope.RosterID ||
		node.EntityID != scope.SeriesID || node.ScoreRevisionID != revisionID || node.RevisionNumber != 1 {
		return uuid.Nil, domain.ErrConflict
	}
	return node.ID, nil
}

func createResultProjectionNode(
	ctx context.Context,
	querier *sqlc.Queries,
	input sqlc.CreateResultProjectionNodeParams,
) error {
	if err := querier.CreateResultProjectionNode(ctx, input); err != nil {
		return mapRepositoryWriteError("ResultPostgres - Settle - create projection node", err)
	}
	return nil
}

func createResultProjectionDependency(
	ctx context.Context,
	querier *sqlc.Queries,
	authorityID, sourceID, derivedID uuid.UUID,
	createdAt time.Time,
) error {
	if err := querier.CreateResultProjectionDependency(ctx, sqlc.CreateResultProjectionDependencyParams{
		AuthorityID: authorityID, SourceNodeID: sourceID, DerivedNodeID: derivedID, CreatedAt: tstz(createdAt),
	}); err != nil {
		return mapRepositoryWriteError("ResultPostgres - Settle - create projection dependency", err)
	}
	return nil
}

func resultProjectionAuthorityID(commitID uuid.UUID) uuid.UUID {
	return uuid.NewSHA1(commitID, []byte("result-projection-authority"))
}

func digestBytes(payload []byte) []byte {
	digest := sha256.Sum256(payload)
	return append([]byte(nil), digest[:]...)
}

func createResultSideEvidence(
	ctx context.Context,
	querier *sqlc.Queries,
	in ResultSettlementInput,
	target resultProjectionTargetBinding,
	resultEventID uuid.UUID,
	seriesResultID uuid.NullUUID,
) error {
	auditPayload, err := marshalJSON("ResultPostgres - Settle - audit payload", map[string]any{
		"attempt_id": in.Scope.AttemptID.String(),
		"reason":     string(in.GameReason),
		"state":      string(in.GameState),
	})
	if err != nil {
		return err
	}
	outboxPayload, err := marshalJSON("ResultPostgres - Settle - outbox payload", map[string]any{
		"result_event_id": resultEventID.String(),
		"series_id":       in.Scope.SeriesID.String(),
		"tournament_id":   in.Scope.TournamentID.String(),
	})
	if err != nil {
		return err
	}
	artifactKinds := make([]string, len(in.ProjectionArtifactKinds))
	for i, kind := range in.ProjectionArtifactKinds {
		artifactKinds[i] = string(kind)
	}

	artifactKindsJSON, err := marshalJSON("ResultPostgres - Settle - projection artifact kinds", artifactKinds)
	if err != nil {
		return err
	}

	_, err = querier.CreateResultAuditEvent(ctx, sqlc.CreateResultAuditEventParams{
		ID: in.IDs.AuditEventID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		SeriesID: nullableUUIDValue(in.Scope.SeriesID), ResultEventID: nullableUUIDValue(resultEventID),
		ActorKind: in.ActorKind,
		ActorID:   nullableUUID(in.ActorID), Action: resultOutboxTopic,
		Payload: auditPayload, OccurredAt: tstz(in.SettledAt), CreatedAt: tstz(in.SettledAt),
	})
	if err != nil {
		return mapRepositoryWriteError("ResultPostgres - Settle - create audit", err)
	}
	_, err = querier.CreateResultProjectionEvidence(ctx, sqlc.CreateResultProjectionEvidenceParams{
		ID: in.IDs.ProjectionEvidenceID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		SeriesID: in.Scope.SeriesID, ResultEventID: resultEventID,
		ArtifactKinds: artifactKindsJSON, PayloadDigest: append([]byte(nil), in.ProjectionPayloadDigest[:]...),
		CreatedAt: tstz(in.SettledAt),
	})
	if err != nil {
		return mapRepositoryWriteError("ResultPostgres - Settle - create projection evidence", err)
	}
	_, err = querier.CreateResultOutboxEvent(ctx, sqlc.CreateResultOutboxEventParams{
		ID: in.IDs.OutboxEventID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		ProjectionRevisionID: target.ID, ProjectionRevision: target.Revision,
		SeriesID: in.Scope.SeriesID, ResultEventID: resultEventID,
		ProjectionEvidenceID: in.IDs.ProjectionEvidenceID,
		IdempotencyKey:       in.IDs.OutboxIdempotencyKey, Topic: resultOutboxTopic,
		Payload: outboxPayload, CreatedAt: tstz(in.SettledAt),
	})
	if err != nil {
		return mapRepositoryWriteError("ResultPostgres - Settle - create outbox", err)
	}
	_, err = querier.CreateResultCommit(ctx, sqlc.CreateResultCommitParams{
		ID: in.IDs.CommitID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		SeriesID: in.Scope.SeriesID, AttemptID: in.Scope.AttemptID, ResultEventID: resultEventID,
		GameResultRevisionID:  in.IDs.GameResultRevisionID,
		SeriesScoreRevisionID: in.IDs.SeriesScoreRevisionID, SeriesResultRevisionID: seriesResultID,
		AuditEventID: in.IDs.AuditEventID, OutboxEventID: in.IDs.OutboxEventID,
		ProjectionEvidenceID: in.IDs.ProjectionEvidenceID,
		IdempotencyKey:       in.IDs.CommitIdempotencyKey, CreatedAt: tstz(in.SettledAt),
	})
	if err != nil {
		return mapRepositoryWriteError("ResultPostgres - Settle - create commit", err)
	}
	return nil
}

func advanceResultHeads(
	ctx context.Context,
	querier *sqlc.Queries,
	in ResultSettlementInput,
	series sqlc.LockResultSeriesRow,
	seriesResultID uuid.NullUUID,
) error {
	_, err := querier.CreateOfficialResultHead(ctx, sqlc.CreateOfficialResultHeadParams{
		EntityKind: "game_attempt", EntityID: in.Scope.AttemptID, SeriesID: in.Scope.SeriesID,
		RosterID: in.Scope.RosterID, GameAttemptID: nullableUUIDValue(in.Scope.AttemptID),
		CurrentRevisionID: in.IDs.GameResultRevisionID, UpdatedAt: tstz(in.SettledAt),
	})
	if err != nil {
		return mapRepositoryWriteError("ResultPostgres - Settle - create Game head", err)
	}
	if seriesResultID.Valid {
		_, err = querier.CreateOfficialResultHead(ctx, sqlc.CreateOfficialResultHeadParams{
			EntityKind: "series", EntityID: in.Scope.SeriesID, SeriesID: in.Scope.SeriesID,
			RosterID: in.Scope.RosterID, CurrentRevisionID: seriesResultID.UUID,
			UpdatedAt: tstz(in.SettledAt),
		})
		if err != nil {
			return mapRepositoryWriteError("ResultPostgres - Settle - create Series head", err)
		}
	}
	_, err = querier.AdvanceSeriesScoreHeadCAS(ctx, sqlc.AdvanceSeriesScoreHeadCASParams{
		CurrentRevisionID: in.IDs.SeriesScoreRevisionID, UpdatedAt: tstz(in.SettledAt),
		SeriesID: in.Scope.SeriesID, RosterID: in.Scope.RosterID,
		ExpectedRevisionID: series.ScoreHeadRevisionID, ExpectedRevision: series.ScoreHeadRevision,
	})
	if err != nil {
		return resultCASWriteError("advance score head", err)
	}
	_, err = querier.SettleGameAttemptCAS(ctx, sqlc.SettleGameAttemptCASParams{
		ResultState: string(in.GameState), ResultReason: optionalTrimmedString(string(in.GameReason)),
		WinnerID: nullableUUID(in.GameWinnerID), ResultRevisionID: nullableUUIDValue(in.IDs.GameResultRevisionID),
		SettledAt: tstz(in.SettledAt), AttemptID: in.Scope.AttemptID, SeriesID: in.Scope.SeriesID,
		RosterID: in.Scope.RosterID, TournamentID: in.Scope.TournamentID,
		ExpectedRevision: in.ExpectedAttemptRevision, ExpectedState: string(in.ExpectedAttemptState),
	})
	if err != nil {
		return resultCASWriteError("settle Game", err)
	}
	_, err = querier.SettleSeriesCAS(ctx, sqlc.SettleSeriesCASParams{
		NextState:             string(in.NextSeriesState),
		FirstParticipantWins:  int16(in.Score.FirstParticipantWins),  //nolint:gosec // Game score validation bounds BO1 and BO3 scores to 0..2.
		SecondParticipantWins: int16(in.Score.SecondParticipantWins), //nolint:gosec // Game score validation bounds BO1 and BO3 scores to 0..2.
		WinnerID:              nullableUUID(in.SeriesWinnerID),
		ScoreRevisionID:       nullableUUIDValue(in.IDs.SeriesScoreRevisionID), ResultRevisionID: seriesResultID,
		SettledAt: tstz(in.SettledAt), SeriesID: in.Scope.SeriesID,
		TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		ExpectedRevision: in.ExpectedSeriesRevision, ExpectedState: string(in.ExpectedSeriesState),
		ExpectedScoreRevisionID: nullableUUIDValue(series.ScoreHeadRevisionID),
	})
	if err != nil {
		return resultCASWriteError("settle Series", err)
	}
	return nil
}
