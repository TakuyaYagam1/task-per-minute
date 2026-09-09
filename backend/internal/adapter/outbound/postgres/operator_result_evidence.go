package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
)

type operatorResultEvidenceIDs struct {
	commitID             uuid.UUID
	resultEventID        uuid.UUID
	resultIdempotencyKey uuid.UUID
	auditEventID         uuid.UUID
	outboxEventID        uuid.UUID
	outboxIdempotencyKey uuid.UUID
	projectionEvidenceID uuid.UUID
}

type operatorResultSideEvidence struct {
	ids           operatorResultEvidenceIDs
	scope         ResultScope
	target        resultProjectionTargetBinding
	actorID       uuid.UUID
	auditPayload  map[string]any
	outboxPayload map[string]any
	artifacts     []domain.ArtifactKind
	digest        [sha256.Size]byte
	createdAt     time.Time
}

func operatorNoShowEvidenceIDs(commandID uuid.UUID) operatorResultEvidenceIDs {
	return operatorResultEvidenceIDs{
		commitID:             operatorResultID(commandID, "no-show-commit"),
		resultEventID:        operatorResultID(commandID, "no-show-result-event"),
		resultIdempotencyKey: operatorResultID(commandID, "no-show-result-idempotency"),
		auditEventID:         operatorResultID(commandID, "no-show-audit"),
		outboxEventID:        operatorResultID(commandID, "no-show-outbox"),
		outboxIdempotencyKey: operatorResultID(commandID, "no-show-outbox-idempotency"),
		projectionEvidenceID: operatorResultID(commandID, "no-show-projection-evidence"),
	}
}

func operatorForfeitEvidenceIDs(command tournamentadmin.ForfeitCommand) operatorResultEvidenceIDs {
	return operatorResultEvidenceIDs{
		commitID:             operatorResultID(command.CommandID, "forfeit-commit"),
		resultEventID:        operatorResultID(command.CommandID, "forfeit-result-event"),
		resultIdempotencyKey: operatorResultID(command.CommandID, "forfeit-result-idempotency"),
		auditEventID:         command.AuditEventID,
		outboxEventID:        command.OutboxEventID,
		outboxIdempotencyKey: operatorResultID(command.CommandID, "forfeit-outbox-idempotency"),
		projectionEvidenceID: command.ProjectionRevisionID,
	}
}

func createOperatorResultSideEvidence(
	ctx context.Context,
	querier *sqlc.Queries,
	in operatorResultSideEvidence,
) error {
	if in.target.ID == uuid.Nil || in.target.Revision < 1 {
		return domain.ErrValidation
	}
	auditPayload, err := marshalJSON("operator result audit payload", in.auditPayload)
	if err != nil {
		return err
	}
	outboxPayload, err := marshalJSON("operator result outbox payload", in.outboxPayload)
	if err != nil {
		return err
	}
	artifactNames := make([]string, len(in.artifacts))
	for index, artifact := range in.artifacts {
		if !artifact.IsValid() {
			return domain.ErrValidation
		}
		artifactNames[index] = string(artifact)
	}
	artifactJSON, err := marshalJSON("operator result artifacts", artifactNames)
	if err != nil {
		return err
	}
	if _, err = querier.CreateResultAuditEvent(ctx, sqlc.CreateResultAuditEventParams{
		ID: in.ids.auditEventID, TournamentID: in.scope.TournamentID, RosterID: in.scope.RosterID,
		SeriesID: nullableUUIDValue(in.scope.SeriesID), ResultEventID: nullableUUIDValue(in.ids.resultEventID),
		ActorKind: resultActorOperator, ActorID: nullableUUIDValue(in.actorID), Action: resultOutboxTopic,
		Payload: auditPayload, OccurredAt: tstz(in.createdAt), CreatedAt: tstz(in.createdAt),
	}); err != nil {
		return mapRepositoryWriteError("TournamentAdminResultPostgres - create audit", err)
	}
	if _, err = querier.CreateResultProjectionEvidence(ctx, sqlc.CreateResultProjectionEvidenceParams{
		ID: in.ids.projectionEvidenceID, TournamentID: in.scope.TournamentID, RosterID: in.scope.RosterID,
		SeriesID: in.scope.SeriesID, ResultEventID: in.ids.resultEventID,
		ArtifactKinds: artifactJSON, PayloadDigest: append([]byte(nil), in.digest[:]...),
		CreatedAt: tstz(in.createdAt),
	}); err != nil {
		return mapRepositoryWriteError("TournamentAdminResultPostgres - create projection evidence", err)
	}
	if _, err = querier.CreateResultOutboxEvent(ctx, sqlc.CreateResultOutboxEventParams{
		ID: in.ids.outboxEventID, TournamentID: in.scope.TournamentID, RosterID: in.scope.RosterID,
		ProjectionRevisionID: in.target.ID, ProjectionRevision: in.target.Revision,
		SeriesID: in.scope.SeriesID, ResultEventID: in.ids.resultEventID,
		ProjectionEvidenceID: in.ids.projectionEvidenceID,
		IdempotencyKey:       in.ids.outboxIdempotencyKey, Topic: resultOutboxTopic,
		Payload: outboxPayload, CreatedAt: tstz(in.createdAt),
	}); err != nil {
		return mapRepositoryWriteError("TournamentAdminResultPostgres - create outbox", err)
	}
	return nil
}

func operatorResultProjectionTarget(
	source operatorSeriesSnapshot,
	targetID uuid.UUID,
) (resultProjectionTargetBinding, error) {
	target, ok := resultProjectionTarget(sqlc.LockResultSourceProjectionRow{
		ID: source.projection.ID, RevisionNumber: source.projection.RevisionNumber,
	}, targetID)
	if !ok {
		return resultProjectionTargetBinding{}, domain.ErrConflict
	}
	return target, nil
}

func publishOperatorResultProjection(
	ctx context.Context,
	tx *TxManager,
	scope ResultScope,
	source operatorSeriesSnapshot,
	target resultProjectionTargetBinding,
	officialResultRevisionID uuid.UUID,
	createdAt time.Time,
) error {
	if target.ID == uuid.Nil || officialResultRevisionID == uuid.Nil {
		return domain.ErrValidation
	}
	return publishResultProjection(ctx, tx, ResultSettlementInput{
		IDs: ResultSettlementIDs{
			GameResultRevisionID: officialResultRevisionID,
			ProjectionEvidenceID: target.ID,
		},
		Scope: scope, SettledAt: createdAt,
	}, sqlc.LockResultSourceProjectionRow{
		ID: source.projection.ID, RevisionNumber: source.projection.RevisionNumber,
	})
}

func (r *TournamentAdminResultPostgres) createOperatorResultCommand(
	ctx context.Context,
	command any,
	scope tournamentadmin.CommandScope,
	seriesID uuid.UUID,
	rosterID uuid.UUID,
	action tournamentadmin.OperatorResultAction,
	expectedAuthorityRevision int64,
	digest [sha256.Size]byte,
	commitID uuid.UUID,
	resultEventID uuid.UUID,
	executedAt time.Time,
) error {
	document, err := operatorResultRequestDocument(action, command, digest)
	if err != nil {
		return err
	}
	_, err = r.tx.Querier(ctx).CreateOperatorResultCommand(ctx, sqlc.CreateOperatorResultCommandParams{
		CommandID: scope.CommandID, TournamentID: scope.TournamentID, RosterID: rosterID,
		SeriesID: seriesID, ActorID: scope.Operator.ActorID, Action: string(action),
		ExpectedAuthorityRevision: expectedAuthorityRevision,
		RequestDigest:             append([]byte(nil), digest[:]...), RequestDocument: document,
		CommitID: commitID, ResultEventID: resultEventID, ExecutedAt: tstz(executedAt),
	})
	if err != nil {
		return mapRepositoryWriteError("TournamentAdminResultPostgres - create command evidence", err)
	}
	return nil
}

func operatorResultRequestDocument(
	action tournamentadmin.OperatorResultAction,
	command any,
	expectedDigest [sha256.Size]byte,
) ([]byte, error) {
	document, err := json.Marshal(struct {
		Action  tournamentadmin.OperatorResultAction `json:"action"`
		Command any                                  `json:"command"`
	}{Action: action, Command: command})
	if err != nil {
		return nil, fmt.Errorf("TournamentAdminResultPostgres - encode command evidence: %w", err)
	}
	if sha256.Sum256(document) != expectedDigest {
		return nil, domain.ErrConflict
	}
	return document, nil
}

func operatorResultID(commandID uuid.UUID, role string) uuid.UUID {
	return uuid.NewSHA1(commandID, []byte("operator-result:"+role))
}
