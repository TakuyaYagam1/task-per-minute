package result

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

// resultProjectionTargetBinding is the immutable target selected before the
// outbox event is inserted. The publication itself follows in the same
// transaction so the deferred outbox foreign key sees a published revision.
type resultProjectionTargetBinding struct {
	ID       uuid.UUID
	Revision int64
}

func resultProjectionTarget(
	source sqlc.LockResultSourceProjectionRow,
	targetID uuid.UUID,
) (resultProjectionTargetBinding, bool) {
	if source.ID == uuid.Nil || source.RevisionNumber < 1 || targetID == uuid.Nil || targetID == source.ID {
		return resultProjectionTargetBinding{}, false
	}
	return resultProjectionTargetBinding{ID: targetID, Revision: source.RevisionNumber + 1}, true
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func publishResultProjectionWithFinalizer(
	ctx context.Context,
	tx *db.TxManager,
	in ResultSettlementInput,
	source sqlc.LockResultSourceProjectionRow,
	finalizer ProjectionFinalizer,
) error {
	querier := tx.Querier(ctx)
	target, ok := resultProjectionTarget(source, in.IDs.ProjectionEvidenceID)
	if !ok {
		return domain.ErrConflict
	}
	if err := persistTerminalProjectionNodes(ctx, tx, in); err != nil {
		return fmt.Errorf("terminal projection nodes: %w", err)
	}
	current, err := querier.GetCurrentProjectionRevision(ctx, sqlc.GetCurrentProjectionRevisionParams{
		TournamentID: in.Scope.TournamentID,
		RosterID:     in.Scope.RosterID,
	})
	if err != nil {
		return resultLookupError("Settle - load projection target", err)
	}
	if current.ID != source.ID || current.RevisionNumber != source.RevisionNumber ||
		current.CutoffID == uuid.Nil || target.Revision != current.RevisionNumber+1 {
		return domain.ErrConflict
	}
	artifacts, err := querier.ListProjectionRevisionArtifacts(ctx, sqlc.ListProjectionRevisionArtifactsParams{
		RevisionID: current.ID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
	})
	if err != nil {
		return resultLookupError("Settle - load projection artifacts", err)
	}
	if len(artifacts) == 0 {
		return domain.ErrConflict
	}
	cutoffID := uuid.NewSHA1(target.ID, []byte("result-projection-cutoff"))
	if _, err = querier.CreateProjectionCutoff(ctx, sqlc.CreateProjectionCutoffParams{
		ID: cutoffID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		SequenceNumber: target.Revision, PreviousCutoffID: nullableUUIDValue(current.CutoffID),
		SourceKind: "official_result", OfficialResultRevisionID: nullableUUIDValue(in.IDs.GameResultRevisionID),
		Reason: "result settlement", CutoffAt: tstz(in.SettledAt), CreatedAt: tstz(in.SettledAt),
	}); err != nil {
		return mapRepositoryWriteError("ResultPostgres - Settle - create projection cutoff", err)
	}
	if _, err = querier.CreateProjectionRevision(ctx, sqlc.CreateProjectionRevisionParams{
		ID: target.ID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		RevisionNumber: target.Revision, PreviousRevisionID: nullableUUIDValue(current.ID),
		CutoffID: cutoffID, CreatedAt: tstz(in.SettledAt),
	}); err != nil {
		return mapRepositoryWriteError("ResultPostgres - Settle - create projection revision", err)
	}
	materialized, err := materializeTerminalSwissStandings(ctx, tx, in)
	if err != nil {
		return fmt.Errorf("terminal Swiss standings: %w", err)
	}
	for _, artifact := range artifacts {
		if materialized && artifact.ArtifactKind == string(domain.ArtifactKindStandings) {
			continue
		}
		if _, err = querier.LinkProjectionArtifact(ctx, sqlc.LinkProjectionArtifactParams{
			RevisionID: target.ID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
			ArtifactKind: artifact.ArtifactKind, ArtifactID: artifact.ArtifactID,
			ChangeKind: "reused", CreatedAt: tstz(in.SettledAt),
		}); err != nil {
			return mapRepositoryWriteError("ResultPostgres - Settle - reuse projection artifact", err)
		}
	}
	if _, err = querier.SupersedeProjectionRevisionCAS(ctx, sqlc.SupersedeProjectionRevisionCASParams{
		SupersededByRevisionID: nullableUUIDValue(target.ID), SupersededAt: tstz(in.SettledAt),
		SupersessionReason: optionalTrimmedString("result settlement"), ID: current.ID,
		TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
	}); err != nil {
		return resultCASWriteError("supersede projection revision", err)
	}
	return publishCommittedResultProjection(ctx, tx, in.Scope.TournamentID, in.Scope.RosterID, target.ID, in.SettledAt, finalizer)
}

// All settlement paths reach this boundary only after their exact terminal
// heads and evidence exist. The receipt belongs to the same result outbox and
// transaction; a strict readback failure aborts the entire settlement.
func publishCommittedResultProjection(
	ctx context.Context,
	tx *db.TxManager,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	revisionID uuid.UUID,
	at time.Time,
	finalizer ProjectionFinalizer,
) error {
	if _, err := tx.Querier(ctx).PublishProjectionRevisionCAS(ctx, sqlc.PublishProjectionRevisionCASParams{
		ID: revisionID, TournamentID: tournamentID, RosterID: rosterID,
		PublishedAt: tstz(at),
	}); err != nil {
		return resultCASWriteError("publish projection revision", err)
	}
	if finalizer != nil {
		return finalizer(ctx, tx, tournamentID, rosterID, revisionID, at)
	}
	return nil
}
