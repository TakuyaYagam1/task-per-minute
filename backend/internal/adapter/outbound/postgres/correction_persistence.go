package postgres

import (
	"context"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func createCorrectionResultEvidence(
	ctx context.Context,
	querier *sqlc.Queries,
	in CorrectionInput,
	source sqlc.GetCorrectionSourceRow,
	attempt sqlc.LockResultAttemptRow,
	series sqlc.LockResultSeriesRow,
	sourceProjection sqlc.ProjectionRevision,
) (correctionResultEvidence, error) {
	resultSequence, err := querier.AllocateResultEventSequence(ctx, sqlc.AllocateResultEventSequenceParams{
		AttemptID: in.Scope.AttemptID, SeriesID: in.Scope.SeriesID,
		RosterID: in.Scope.RosterID, TournamentID: in.Scope.TournamentID,
	})
	if err != nil {
		return correctionResultEvidence{}, mapRepositoryWriteError("CorrectionPostgres - allocate result event sequence", err)
	}
	event, err := querier.CreateResultEvent(ctx, sqlc.CreateResultEventParams{
		ID: in.IDs.ResultEventID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		SeriesID: in.Scope.SeriesID, AttemptID: in.Scope.AttemptID,
		SubmissionEventID: nullableUUIDValue(in.SubmissionEventID), ServerSequence: resultSequence,
		IdempotencyKey: in.IDs.ResultEventIdempotencyKey, ResultState: string(in.GameState),
		ResultReason: string(in.GameReason), WinnerID: nullableUUID(in.GameWinnerID),
		OccurredAt: tstz(in.CorrectedAt), CreatedAt: tstz(in.CorrectedAt),
	})
	if err != nil {
		return correctionResultEvidence{}, mapRepositoryWriteError("CorrectionPostgres - create result event", err)
	}
	if _, err = querier.CreateOfficialResultRevision(ctx, sqlc.CreateOfficialResultRevisionParams{
		ID: in.IDs.GameResultRevisionID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		EntityKind: "game_attempt", EntityID: in.Scope.AttemptID, SeriesID: in.Scope.SeriesID,
		GameAttemptID: nullableUUIDValue(in.Scope.AttemptID), ResultEventID: event.ID,
		PreviousRevisionID: nullableUUIDValue(in.SourceRevisionID), RevisionNumber: source.RevisionNumber + 1,
		ResultState: string(in.GameState), ResultReason: string(in.GameReason),
		CommandID: correctionGameCommandID(in), ActorKind: resultActorOperator, ActorID: nullableUUIDValue(in.OperatorID),
		SourceProjectionRevisionID: sourceProjection.ID, SourceProjectionRevision: sourceProjection.RevisionNumber,
		WinnerID: nullableUUID(in.GameWinnerID), CreatedAt: tstz(in.CorrectedAt),
	}); err != nil {
		return correctionResultEvidence{}, mapRepositoryWriteError("CorrectionPostgres - create Game revision", err)
	}
	previousAttempts, err := querier.LockSeriesScoreRevisionAttempts(ctx, sqlc.LockSeriesScoreRevisionAttemptsParams{
		ScoreRevisionID: in.ExpectedScoreRevisionID, TournamentID: in.Scope.TournamentID,
		RosterID: in.Scope.RosterID, SeriesID: in.Scope.SeriesID,
	})
	if err != nil {
		return correctionResultEvidence{}, correctionLookupError("lock score attempt ledger", err)
	}
	position, found := scoreRevisionAttemptPosition(previousAttempts, in.Scope.AttemptID)
	if !found {
		return correctionResultEvidence{}, domain.ErrConflict
	}
	slotPosition, err := scoreSlotPosition(int32(attempt.SlotPosition))
	if err != nil {
		return correctionResultEvidence{}, err
	}
	if _, err = querier.CreateSeriesScoreRevision(ctx, sqlc.CreateSeriesScoreRevisionParams{
		ID: in.IDs.SeriesScoreRevisionID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		SeriesID: in.Scope.SeriesID, ResultEventID: nullableUUIDValue(event.ID),
		PreviousRevisionID:         nullableUUIDValue(in.ExpectedScoreRevisionID),
		RevisionNumber:             series.ScoreHeadRevision + 1,
		Operation:                  "replace_result",
		CommandID:                  correctionScoreCommandID(in),
		ActorKind:                  resultActorOperator,
		ActorID:                    nullableUUIDValue(in.OperatorID),
		CommandAttemptID:           nullableUUIDValue(in.Scope.AttemptID),
		SourceProjectionRevisionID: sourceProjection.ID,
		SourceProjectionRevision:   sourceProjection.RevisionNumber,
		FirstParticipantWins:       int16(in.Score.FirstParticipantWins),  //nolint:gosec // validated against BO1 or BO3 bounds.
		SecondParticipantWins:      int16(in.Score.SecondParticipantWins), //nolint:gosec // validated against BO1 or BO3 bounds.
		CreatedAt:                  tstz(in.CorrectedAt),
	}); err != nil {
		return correctionResultEvidence{}, mapRepositoryWriteError("CorrectionPostgres - create score revision", err)
	}
	if err = copyScoreRevisionAttempts(
		ctx,
		querier,
		in.Scope,
		in.IDs.SeriesScoreRevisionID,
		previousAttempts,
		[]uuid.UUID{in.Scope.AttemptID},
		tstz(in.CorrectedAt),
	); err != nil {
		return correctionResultEvidence{}, err
	}
	if err = createScoreRevisionAttempt(ctx, querier, in.Scope, in.IDs.SeriesScoreRevisionID, position,
		scoreRevisionAttemptEvidence{
			SlotID:               attempt.SlotID,
			SlotPosition:         slotPosition,
			GameAttemptID:        in.Scope.AttemptID,
			AttemptNumber:        attempt.AttemptNumber,
			GameResultRevisionID: in.IDs.GameResultRevisionID,
			ResultEventID:        event.ID,
			ResultState:          string(in.GameState),
			ResultReason:         string(in.GameReason),
			WinnerID:             nullableUUID(in.GameWinnerID),
			OccurredAt:           tstz(in.CorrectedAt),
			CreatedAt:            tstz(in.CorrectedAt),
		},
	); err != nil {
		return correctionResultEvidence{}, err
	}
	seriesResultID := uuid.NullUUID{}
	if in.IDs.SeriesResultRevisionID != uuid.Nil {
		seriesResultID = nullableUUIDValue(in.IDs.SeriesResultRevisionID)
		previousID := nullableUUID(in.ExpectedSeriesResultRevisionID)
		revisionNumber := int64(1)
		if in.ExpectedSeriesResultRevisionID != nil {
			previous, loadErr := querier.GetOfficialResultRevisionByID(ctx, *in.ExpectedSeriesResultRevisionID)
			if loadErr != nil {
				return correctionResultEvidence{}, correctionLookupError("Series revision", loadErr)
			}
			revisionNumber = previous.RevisionNumber + 1
		}
		if _, err = querier.CreateOfficialResultRevision(ctx, sqlc.CreateOfficialResultRevisionParams{
			ID: in.IDs.SeriesResultRevisionID, TournamentID: in.Scope.TournamentID,
			RosterID: in.Scope.RosterID, EntityKind: "series", EntityID: in.Scope.SeriesID,
			SeriesID: in.Scope.SeriesID, ResultEventID: event.ID, PreviousRevisionID: previousID,
			RevisionNumber: revisionNumber, ResultState: string(in.NextSeriesState),
			ResultReason: in.SeriesResultReason, WinnerID: nullableUUID(in.SeriesWinnerID),
			CommandID: correctionSeriesCommandID(in), ActorKind: resultActorOperator, ActorID: nullableUUIDValue(in.OperatorID),
			SourceProjectionRevisionID: sourceProjection.ID, SourceProjectionRevision: sourceProjection.RevisionNumber,
			CreatedAt: tstz(in.CorrectedAt),
		}); err != nil {
			return correctionResultEvidence{}, mapRepositoryWriteError("CorrectionPostgres - create Series revision", err)
		}
	}
	return correctionResultEvidence{resultEventID: event.ID, seriesResultID: seriesResultID}, nil
}

func correctionGameCommandID(in CorrectionInput) uuid.UUID {
	if in.GameResultCommandID != uuid.Nil {
		return in.GameResultCommandID
	}
	return in.IDs.CommitIdempotencyKey
}

func correctionScoreCommandID(in CorrectionInput) uuid.UUID {
	if in.ScoreCommandID != uuid.Nil {
		return in.ScoreCommandID
	}
	return in.IDs.CommitIdempotencyKey
}

func correctionSeriesCommandID(in CorrectionInput) uuid.UUID {
	if in.SeriesResultCommandID != uuid.Nil {
		return in.SeriesResultCommandID
	}
	return in.IDs.CommitIdempotencyKey
}

func createCorrectionSideEvidence(
	ctx context.Context,
	querier *sqlc.Queries,
	in CorrectionInput,
	resultEventID uuid.UUID,
	seriesResultID uuid.NullUUID,
	artifactKinds []domain.ArtifactKind,
	producedProjectionID uuid.UUID,
	producedProjectionRevision int64,
) error {
	kinds := make([]string, len(artifactKinds))
	for i, kind := range artifactKinds {
		kinds[i] = string(kind)
	}
	auditPayload, err := marshalJSON("CorrectionPostgres - audit payload", map[string]any{
		"attempt_id": in.Scope.AttemptID.String(), "entity_id": in.Scope.AttemptID.String(),
		"entity_kind": "game_attempt", "previous_revision_id": in.SourceRevisionID.String(),
		"projection_revision_id": in.ProjectionIDs.RevisionID.String(), "reason": in.Reason,
		"result_reason": string(in.GameReason), "state": string(in.GameState),
	})
	if err != nil {
		return err
	}
	outboxPayload, err := marshalJSON("CorrectionPostgres - outbox payload", map[string]any{
		"result_event_id": resultEventID.String(), "series_id": in.Scope.SeriesID.String(),
		"tournament_id": in.Scope.TournamentID.String(),
	})
	if err != nil {
		return err
	}
	artifactKindsJSON, err := marshalJSON("CorrectionPostgres - projection artifact kinds", kinds)
	if err != nil {
		return err
	}
	if _, err := querier.CreateResultAuditEvent(ctx, sqlc.CreateResultAuditEventParams{
		ID: in.IDs.AuditEventID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		SeriesID: nullableUUIDValue(in.Scope.SeriesID), ResultEventID: nullableUUIDValue(resultEventID),
		ActorKind: resultActorOperator, ActorID: nullableUUIDValue(in.OperatorID),
		Action: correctionOutboxTopic, Payload: auditPayload,
		OccurredAt: tstz(in.CorrectedAt), CreatedAt: tstz(in.CorrectedAt),
	}); err != nil {
		return mapRepositoryWriteError("CorrectionPostgres - create audit", err)
	}
	if _, err := querier.CreateResultProjectionEvidence(ctx, sqlc.CreateResultProjectionEvidenceParams{
		ID: in.IDs.ProjectionEvidenceID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		SeriesID: in.Scope.SeriesID, ResultEventID: resultEventID, ArtifactKinds: artifactKindsJSON,
		PayloadDigest: append([]byte(nil), in.ProjectionPayloadDigest[:]...), CreatedAt: tstz(in.CorrectedAt),
	}); err != nil {
		return mapRepositoryWriteError("CorrectionPostgres - create projection evidence", err)
	}
	if _, err := querier.CreateResultOutboxEvent(ctx, sqlc.CreateResultOutboxEventParams{
		ID: in.IDs.OutboxEventID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		ProjectionRevisionID: producedProjectionID, ProjectionRevision: producedProjectionRevision,
		SeriesID: in.Scope.SeriesID, ResultEventID: resultEventID,
		ProjectionEvidenceID: in.IDs.ProjectionEvidenceID,
		IdempotencyKey:       in.IDs.OutboxIdempotencyKey, Topic: correctionOutboxTopic,
		Payload: outboxPayload, CreatedAt: tstz(in.CorrectedAt),
	}); err != nil {
		return mapRepositoryWriteError("CorrectionPostgres - create outbox", err)
	}
	if _, err := querier.CreateResultCommit(ctx, sqlc.CreateResultCommitParams{
		ID: in.IDs.CommitID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		SeriesID: in.Scope.SeriesID, AttemptID: in.Scope.AttemptID, ResultEventID: resultEventID,
		GameResultRevisionID:  in.IDs.GameResultRevisionID,
		SeriesScoreRevisionID: in.IDs.SeriesScoreRevisionID, SeriesResultRevisionID: seriesResultID,
		AuditEventID: in.IDs.AuditEventID, OutboxEventID: in.IDs.OutboxEventID,
		ProjectionEvidenceID: in.IDs.ProjectionEvidenceID,
		IdempotencyKey:       in.IDs.CommitIdempotencyKey, CreatedAt: tstz(in.CorrectedAt),
	}); err != nil {
		return mapRepositoryWriteError("CorrectionPostgres - create commit", err)
	}
	return nil
}

func prepareCorrectionProjection(
	ctx context.Context,
	querier *sqlc.Queries,
	in CorrectionInput,
	current sqlc.ProjectionRevision,
	plan correctionProjectionPlan,
) error {
	projectionInput := ProjectionPublishInput{
		IDs:       in.ProjectionIDs,
		Scope:     ProjectionScope{TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID},
		Source:    ProjectionSource{Kind: projectionSourceOperatorRebuild, Reason: in.Reason},
		Artifacts: plan.newArtifacts, SupersessionReason: in.Reason,
		CutoffAt: in.CorrectedAt, CreatedAt: in.CorrectedAt, PublishedAt: in.CorrectedAt,
	}
	if _, err := querier.CreateProjectionCutoff(ctx, sqlc.CreateProjectionCutoffParams{
		ID: in.ProjectionIDs.CutoffID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		SequenceNumber: current.RevisionNumber + 1, PreviousCutoffID: nullableUUIDValue(current.CutoffID),
		SourceKind: projectionSourceOperatorRebuild, Reason: in.Reason,
		CutoffAt: tstz(in.CorrectedAt), CreatedAt: tstz(in.CorrectedAt),
	}); err != nil {
		return mapRepositoryWriteError("CorrectionPostgres - create cutoff", err)
	}
	if _, err := querier.CreateProjectionRevision(ctx, sqlc.CreateProjectionRevisionParams{
		ID: in.ProjectionIDs.RevisionID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		RevisionNumber: current.RevisionNumber + 1, PreviousRevisionID: nullableUUIDValue(current.ID),
		CutoffID: in.ProjectionIDs.CutoffID, CreatedAt: tstz(in.CorrectedAt),
	}); err != nil {
		return mapRepositoryWriteError("CorrectionPostgres - create projection revision", err)
	}
	for _, artifact := range plan.newArtifacts {
		if err := createProjectionArtifact(ctx, querier, projectionInput, artifact); err != nil {
			return err
		}
	}
	for _, artifact := range plan.reused {
		if _, err := querier.LinkProjectionArtifact(ctx, sqlc.LinkProjectionArtifactParams{
			RevisionID: in.ProjectionIDs.RevisionID, TournamentID: in.Scope.TournamentID,
			RosterID: in.Scope.RosterID, ArtifactKind: artifact.ArtifactKind,
			ArtifactID: artifact.ID, ChangeKind: "reused", CreatedAt: tstz(in.CorrectedAt),
		}); err != nil {
			return mapRepositoryWriteError("CorrectionPostgres - reuse artifact", err)
		}
	}
	return nil
}

func publishCorrectionProjection(
	ctx context.Context,
	querier *sqlc.Queries,
	in CorrectionInput,
	current sqlc.ProjectionRevision,
) error {
	if _, err := querier.SupersedeProjectionRevisionCAS(
		ctx,
		sqlc.SupersedeProjectionRevisionCASParams{
			SupersededByRevisionID: nullableUUIDValue(in.ProjectionIDs.RevisionID),
			SupersededAt:           tstz(in.CorrectedAt), SupersessionReason: &in.Reason,
			ID: current.ID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		},
	); err != nil {
		return projectionCASWriteError("correction supersession", err)
	}
	if _, err := querier.PublishProjectionRevisionCAS(
		ctx,
		sqlc.PublishProjectionRevisionCASParams{
			PublishedAt: tstz(in.CorrectedAt), ID: in.ProjectionIDs.RevisionID,
			TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		},
	); err != nil {
		return projectionCASWriteError("correction publish", err)
	}
	return nil
}

type correctionResultEvidence struct {
	resultEventID  uuid.UUID
	seriesResultID uuid.NullUUID
}

func closeCorrectionReadiness(
	ctx context.Context,
	querier *sqlc.Queries,
	in CorrectionInput,
	windows []sqlc.LockCorrectionOpenReadyWindowsRow,
) ([]uuid.UUID, error) {
	closed := make([]uuid.UUID, 0, len(windows))
	for _, window := range windows {
		if _, err := querier.CloseReadyWindowCAS(ctx, sqlc.CloseReadyWindowCASParams{
			NextState: "superseded", ID: window.ReadyWindowID,
			WaveID: window.WaveID, ExpectedState: window.ReadyWindowState,
		}); err != nil {
			return nil, resultCASWriteError("close correction readiness", err)
		}
		if _, err := querier.TransitionWaveCAS(ctx, sqlc.TransitionWaveCASParams{
			NextState: "superseded", UpdatedAt: tstz(in.CorrectedAt), ClosedAt: tstz(in.CorrectedAt),
			ID: window.WaveID, TournamentID: in.Scope.TournamentID,
			ExpectedRevision: window.WaveRevision, ExpectedState: window.WaveState,
		}); err != nil {
			return nil, resultCASWriteError("supersede correction Wave", err)
		}
		closed = append(closed, window.ReadyWindowID)
	}
	return closed, nil
}

func advanceCorrectionHeads(
	ctx context.Context,
	querier *sqlc.Queries,
	in CorrectionInput,
	source sqlc.GetCorrectionSourceRow,
	heads []sqlc.OfficialResultHead,
) error {
	if _, err := querier.AdvanceCorrectionOfficialHeadCAS(
		ctx,
		sqlc.AdvanceCorrectionOfficialHeadCASParams{
			CurrentRevisionID: in.IDs.GameResultRevisionID, UpdatedAt: tstz(in.CorrectedAt),
			EntityKind: "game_attempt", EntityID: in.Scope.AttemptID, RosterID: in.Scope.RosterID,
			ExpectedRevisionID: in.SourceRevisionID, ExpectedRevision: source.HeadRevision,
		},
	); err != nil {
		return resultCASWriteError("advance corrected Game head", err)
	}
	_, seriesHead := correctionHeads(heads, in.Scope)
	seriesResultID := uuid.NullUUID{}
	if in.IDs.SeriesResultRevisionID != uuid.Nil {
		seriesResultID = nullableUUIDValue(in.IDs.SeriesResultRevisionID)
		if seriesHead == nil {
			return domain.ErrConflict
		}
		if _, err := querier.AdvanceCorrectionOfficialHeadCAS(
			ctx,
			sqlc.AdvanceCorrectionOfficialHeadCASParams{
				CurrentRevisionID: in.IDs.SeriesResultRevisionID, UpdatedAt: tstz(in.CorrectedAt),
				EntityKind: "series", EntityID: in.Scope.SeriesID, RosterID: in.Scope.RosterID,
				ExpectedRevisionID: seriesHead.CurrentRevisionID, ExpectedRevision: seriesHead.Revision,
			},
		); err != nil {
			return resultCASWriteError("advance corrected Series head", err)
		}
	}
	if _, err := querier.AdvanceSeriesScoreHeadCAS(ctx, sqlc.AdvanceSeriesScoreHeadCASParams{
		CurrentRevisionID: in.IDs.SeriesScoreRevisionID, UpdatedAt: tstz(in.CorrectedAt),
		SeriesID: in.Scope.SeriesID, RosterID: in.Scope.RosterID,
		ExpectedRevisionID: in.ExpectedScoreRevisionID, ExpectedRevision: in.ExpectedScoreRevision,
	}); err != nil {
		return resultCASWriteError("advance corrected score head", err)
	}
	if _, err := querier.CorrectGameAttemptCAS(ctx, sqlc.CorrectGameAttemptCASParams{
		ResultState: string(in.GameState), ResultReason: optionalTrimmedString(string(in.GameReason)),
		WinnerID: nullableUUID(in.GameWinnerID), ResultRevisionID: nullableUUIDValue(in.IDs.GameResultRevisionID),
		CorrectedAt: tstz(in.CorrectedAt), AttemptID: in.Scope.AttemptID,
		SeriesID: in.Scope.SeriesID, RosterID: in.Scope.RosterID, TournamentID: in.Scope.TournamentID,
		ExpectedRevision: in.ExpectedAttemptRevision, ExpectedState: string(in.ExpectedAttemptState),
		ExpectedResultRevisionID: nullableUUIDValue(in.SourceRevisionID),
	}); err != nil {
		return resultCASWriteError("correct Game", err)
	}
	if _, err := querier.CorrectSeriesCAS(ctx, sqlc.CorrectSeriesCASParams{
		NextState:             string(in.NextSeriesState),
		FirstParticipantWins:  int16(in.Score.FirstParticipantWins),  //nolint:gosec // validated against BO1 or BO3 bounds.
		SecondParticipantWins: int16(in.Score.SecondParticipantWins), //nolint:gosec // validated against BO1 or BO3 bounds.
		WinnerID:              nullableUUID(in.SeriesWinnerID), ScoreRevisionID: nullableUUIDValue(in.IDs.SeriesScoreRevisionID),
		ResultRevisionID: seriesResultID, CorrectedAt: tstz(in.CorrectedAt),
		SeriesID: in.Scope.SeriesID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		ExpectedRevision: in.ExpectedSeriesRevision, ExpectedState: string(in.ExpectedSeriesState),
		ExpectedScoreRevisionID:  nullableUUIDValue(in.ExpectedScoreRevisionID),
		ExpectedResultRevisionID: nullableUUID(in.ExpectedSeriesResultRevisionID),
	}); err != nil {
		return resultCASWriteError("correct Series", err)
	}
	return nil
}
