package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
)

// TournamentAdminCorrectionPostgres keeps the correction command ledger and
// its result, projection, readiness, and outbox effects inside one database
// transaction. The public projection payload is always rebuilt from locked
// normalized rows; the command contains only expected lineage and digests.
type TournamentAdminCorrectionPostgres struct {
	tx         *TxManager
	correction *CorrectionPostgres
}

var _ tournamentadmin.CorrectionWorkflowRepository = (*TournamentAdminCorrectionPostgres)(nil)

func NewTournamentAdminCorrectionPostgres(tx *TxManager) *TournamentAdminCorrectionPostgres {
	return &TournamentAdminCorrectionPostgres{tx: tx, correction: NewCorrectionPostgres(tx)}
}

func (r *TournamentAdminCorrectionPostgres) FindCorrectionCommand(
	ctx context.Context,
	commandID uuid.UUID,
) (*tournamentadmin.CorrectionCommandRecord, error) {
	if r == nil || r.tx == nil || ctx == nil || commandID == uuid.Nil {
		return nil, domain.ErrValidation
	}
	record, err := r.tx.Querier(ctx).GetTournamentAdminCorrectionCommand(ctx, commandID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("TournamentAdminCorrectionPostgres - find command: %w", err)
	}
	return tournamentAdminCorrectionCommandRecord(record)
}

func (r *TournamentAdminCorrectionPostgres) ReadCorrectionTime(ctx context.Context) (time.Time, error) {
	if r == nil || r.tx == nil || ctx == nil {
		return time.Time{}, domain.ErrValidation
	}
	value, err := r.tx.Querier(ctx).GetTournamentAdminCorrectionTime(ctx)
	if err != nil || !value.Valid {
		if err != nil {
			return time.Time{}, fmt.Errorf("TournamentAdminCorrectionPostgres - read time: %w", err)
		}
		return time.Time{}, domain.ErrInternal
	}
	return value.Time.Round(0).UTC(), nil
}

func (r *TournamentAdminCorrectionPostgres) LockCorrectionAuthority(
	ctx context.Context,
	tournamentID, seriesID, gameID uuid.UUID,
) (tournamentadmin.CorrectionWorkflowAuthority, error) {
	if r == nil || r.tx == nil || ctx == nil || tournamentID == uuid.Nil || seriesID == uuid.Nil || gameID == uuid.Nil {
		return tournamentadmin.CorrectionWorkflowAuthority{}, domain.ErrValidation
	}
	return r.loadCorrectionAuthority(ctx, tournamentID, seriesID, gameID)
}

func (r *TournamentAdminCorrectionPostgres) CommitCorrection(
	ctx context.Context,
	mutation tournamentadmin.CorrectionMutation,
) (tournamentadmin.CorrectionEvidence, bool, error) {
	if r == nil || r.tx == nil || r.correction == nil || ctx == nil {
		return tournamentadmin.CorrectionEvidence{}, false, domain.ErrValidation
	}
	if !validTournamentAdminCorrectionMutation(mutation) {
		return tournamentadmin.CorrectionEvidence{}, false, fmt.Errorf(
			"%w: correction mutation command=%t authority=%t request=%t plan=%v evidence=%t",
			domain.ErrValidation,
			mutation.Command.CommandID != uuid.Nil && mutation.Command.TournamentID != uuid.Nil &&
				mutation.Command.SeriesID != uuid.Nil && mutation.Command.GameID != uuid.Nil &&
				mutation.Command.Operator.ActorID != uuid.Nil,
			mutation.Authority.RosterID != uuid.Nil && mutation.Authority.ProjectionRevisionID != uuid.Nil &&
				mutation.Authority.ProjectionRevision > 0,
			mutation.RequestDigest != ([sha256.Size]byte{}),
			mutation.Plan.Validate(),
			validTournamentAdminCorrectionEvidence(mutation.Evidence),
		)
	}

	var evidence tournamentadmin.CorrectionEvidence
	changed := false
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		scope := ResultScope{
			TournamentID: mutation.Command.TournamentID,
			RosterID:     mutation.Authority.RosterID,
			SeriesID:     mutation.Command.SeriesID,
			AttemptID:    mutation.Command.GameID,
		}
		querier := r.tx.Querier(txCtx)

		// The command idempotency lookup is repeated after the scope locks. This
		// makes a concurrent exact replay return durable evidence before any CAS.
		if err := lockCorrectionScope(txCtx, querier, scope); err != nil {
			return err
		}
		recorded, err := r.FindCorrectionCommand(txCtx, mutation.Command.CommandID)
		if err != nil {
			return err
		}
		if recorded != nil {
			if correctionReplayMatches(*recorded, mutation) {
				evidence = cloneTournamentAdminCorrectionEvidence(recorded.Evidence)
				return nil
			}
			return domain.ErrConflict
		}
		current, err := querier.GetCurrentProjectionRevision(txCtx, sqlc.GetCurrentProjectionRevisionParams{
			TournamentID: scope.TournamentID, RosterID: scope.RosterID,
		})
		if err != nil {
			return tournamentAdminCorrectionError("recheck current projection after scope lock", err)
		}
		if current.ID != mutation.Authority.ProjectionRevisionID ||
			current.RevisionNumber != mutation.Authority.ProjectionRevision {
			return domain.ErrConflict
		}
		stageSnapshot, tournament, err := lockTournamentAdminCorrectionStage(txCtx, querier, mutation, scope)
		if err != nil {
			return fmt.Errorf("TournamentAdminCorrectionPostgres - validate stage: %w", err)
		}
		progression := NewTournamentProgressionPostgres(r.tx)
		predecessor, err := progression.prepareCorrectionFinalSwissReceiptPredecessor(
			txCtx,
			ProjectionScope{TournamentID: scope.TournamentID, RosterID: scope.RosterID},
			mutation.Authority.ProjectionRevisionID,
			mutation.Authority.ProjectionRevision,
			mutation.Command.CommandID,
		)
		if err != nil {
			return fmt.Errorf("TournamentAdminCorrectionPostgres - lock Final Swiss predecessor: %w", err)
		}

		input, logical, err := r.buildCorrectionCommit(txCtx, querier, mutation, scope)
		if err != nil {
			return fmt.Errorf("TournamentAdminCorrectionPostgres - build commit: %w", err)
		}
		record, err := r.correction.rebuildLocked(txCtx, input)
		if err != nil {
			return fmt.Errorf("TournamentAdminCorrectionPostgres - rebuild: %w", err)
		}
		if record == nil || record.ResultCommit == nil || record.Projection == nil ||
			record.ResultCommit.Commit.ID != input.IDs.CommitID ||
			record.Projection.Revision.ID != input.ProjectionIDs.RevisionID ||
			record.Projection.Revision.RevisionNumber != mutation.Authority.ProjectionRevision+1 {
			return domain.ErrConflict
		}
		if err := persistTournamentAdminCorrectionSwissLedger(txCtx, querier, mutation, scope); err != nil {
			return fmt.Errorf("TournamentAdminCorrectionPostgres - persist Swiss ledger: %w", err)
		}
		if err := persistTournamentAdminCorrectionCommand(txCtx, querier, mutation, input); err != nil {
			return fmt.Errorf("TournamentAdminCorrectionPostgres - persist command: %w", err)
		}
		if err := persistTournamentAdminCorrectionStage(
			txCtx, querier, mutation, input, stageSnapshot, tournament,
		); err != nil {
			return fmt.Errorf("TournamentAdminCorrectionPostgres - persist stage: %w", err)
		}
		if err := persistTournamentAdminCorrectionLogicalPlan(txCtx, querier, mutation, logical); err != nil {
			return fmt.Errorf("TournamentAdminCorrectionPostgres - persist logical plan: %w", err)
		}
		if err := progression.persistCorrectionFinalSwissReceipt(
			txCtx,
			ProjectionScope{TournamentID: scope.TournamentID, RosterID: scope.RosterID},
			input.ProjectionIDs.RevisionID,
			mutation.Command.CommandID,
			predecessor,
			mutation.Evidence.RequestedAt,
		); err != nil {
			return fmt.Errorf("TournamentAdminCorrectionPostgres - persist Final Swiss receipt: %w", err)
		}
		evidence = cloneTournamentAdminCorrectionEvidence(mutation.Evidence)
		changed = true
		return nil
	})
	if err != nil {
		return tournamentadmin.CorrectionEvidence{}, false, err
	}
	return evidence, changed, nil
}

type tournamentAdminCorrectionLogicalPlan struct {
	bindings []tournamentAdminCorrectionBinding
}

type tournamentAdminCorrectionBinding struct {
	kind     domain.ArtifactKind
	entityID uuid.UUID
	sourceID uuid.UUID
	nodeID   uuid.UUID
}

func validTournamentAdminCorrectionMutation(mutation tournamentadmin.CorrectionMutation) bool {
	return mutation.Command.CommandID != uuid.Nil && mutation.Command.TournamentID != uuid.Nil &&
		mutation.Command.SeriesID != uuid.Nil && mutation.Command.GameID != uuid.Nil &&
		mutation.Command.Operator.ActorID != uuid.Nil && mutation.Authority.RosterID != uuid.Nil &&
		mutation.Authority.ProjectionRevisionID != uuid.Nil && mutation.Authority.ProjectionRevision > 0 &&
		mutation.RequestDigest != ([sha256.Size]byte{}) && mutation.Plan.Validate() == nil &&
		validTournamentAdminCorrectionEvidence(mutation.Evidence)
}

func correctionReplayMatches(
	record tournamentadmin.CorrectionCommandRecord,
	mutation tournamentadmin.CorrectionMutation,
) bool {
	return record.CommandID == mutation.Command.CommandID &&
		record.TournamentID == mutation.Command.TournamentID &&
		record.RosterID == mutation.Authority.RosterID &&
		record.SeriesID == mutation.Command.SeriesID && record.GameID == mutation.Command.GameID &&
		record.OperatorID == mutation.Command.Operator.ActorID &&
		record.ExpectedProjectionRevision == mutation.Command.ExpectedProjectionRevision &&
		record.RequestDigest == mutation.RequestDigest &&
		correctionEvidenceEqual(record.Evidence, mutation.Evidence)
}

func correctionLogicalProjectionIsCurrent(
	logical correctionLogicalGraph,
	projection domain.ProjectionRevision,
) bool {
	current, found := logical.current[projection.Revision().Artifact()]
	return found && current.ID() == projection.Revision().ID()
}

func correctionEvidenceEqual(first, second tournamentadmin.CorrectionEvidence) bool {
	return first.CommandID == second.CommandID && first.TournamentID == second.TournamentID &&
		first.SeriesID == second.SeriesID && first.GameID == second.GameID && first.OperatorID == second.OperatorID &&
		first.Reason == second.Reason && first.RequestedAt.Equal(second.RequestedAt) &&
		first.ValidationDigest == second.ValidationDigest &&
		slicesEqual(first.Fields, second.Fields) &&
		correctionSupersessionsEqual(first.Supersessions, second.Supersessions) &&
		correctionUnlocksEqual(first.UnlockIntents, second.UnlockIntents)
}

func slicesEqual(first, second []string) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if first[index] != second[index] {
			return false
		}
	}
	return true
}

func correctionSupersessionsEqual(first, second []tournamentadmin.ProjectionSupersessionView) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if first[index].ArtifactKind != second[index].ArtifactKind || first[index].ArtifactID != second[index].ArtifactID ||
			first[index].PreviousRevisionID != second[index].PreviousRevisionID ||
			first[index].SuccessorRevisionID != second[index].SuccessorRevisionID ||
			first[index].ReplacementDecisionID != second[index].ReplacementDecisionID ||
			!sameUUIDPointer(first[index].PreviousDecisionID, second[index].PreviousDecisionID) {
			return false
		}
	}
	return true
}

func correctionUnlocksEqual(first, second []tournamentadmin.CorrectionUnlockIntent) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if first[index] != second[index] {
			return false
		}
	}
	return true
}

func sameUUIDPointer(first, second *uuid.UUID) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}

func cloneTournamentAdminCorrectionEvidence(value tournamentadmin.CorrectionEvidence) tournamentadmin.CorrectionEvidence {
	clone := value
	clone.Fields = append([]string(nil), value.Fields...)
	clone.Supersessions = make([]tournamentadmin.ProjectionSupersessionView, len(value.Supersessions))
	for index, item := range value.Supersessions {
		clone.Supersessions[index] = item
		if item.PreviousDecisionID != nil {
			previous := *item.PreviousDecisionID
			clone.Supersessions[index].PreviousDecisionID = &previous
		}
	}
	clone.UnlockIntents = append(make([]tournamentadmin.CorrectionUnlockIntent, 0, len(value.UnlockIntents)), value.UnlockIntents...)
	return clone
}

func correctionWorkflowUUID(commandID uuid.UUID, role string) uuid.UUID {
	return uuid.NewSHA1(commandID, []byte("tournament-admin-correction:"+role))
}

func correctionLogicalBaselineUUID(kind domain.ArtifactKind, sourceID uuid.UUID) uuid.UUID {
	return uuid.NewSHA1(sourceID, []byte("tournament-admin-correction-baseline:"+string(kind)))
}

func correctionTime(value time.Time) time.Time {
	return value.Round(0).UTC()
}

func maxCorrectionTime(values ...time.Time) time.Time {
	var current time.Time
	for _, value := range values {
		value = correctionTime(value)
		if current.IsZero() || value.After(current) {
			current = value
		}
	}
	return current
}

func correctionDigestBytes(digest [sha256.Size]byte) []byte {
	return append([]byte(nil), digest[:]...)
}

func correctionDigestFromBytes(value []byte) ([sha256.Size]byte, bool) {
	var digest [sha256.Size]byte
	if len(value) != len(digest) {
		return digest, false
	}
	copy(digest[:], value)
	return digest, digest != ([sha256.Size]byte{})
}

func correctionArtifactKind(kind string) (domain.ArtifactKind, bool) {
	value := domain.ArtifactKind(kind)
	return value, value.IsValid()
}

func correctionArtifactKindSQL(kind domain.ArtifactKind) string {
	return string(kind)
}

func correctionArtifactKindFromSQL(kind string) (domain.ArtifactKind, bool) {
	value := domain.ArtifactKind(kind)
	return value, value.IsValid()
}

func correctionPointer(value uuid.UUID) *uuid.UUID {
	if value == uuid.Nil {
		return nil
	}
	clone := value
	return &clone
}

func correctionNullPointer(value uuid.NullUUID) *uuid.UUID {
	if !value.Valid || value.UUID == uuid.Nil {
		return nil
	}
	return correctionPointer(value.UUID)
}

func correctionDerivedPointer(value uuid.NullUUID) *domain.DerivedRevisionID {
	if !value.Valid || value.UUID == uuid.Nil {
		return nil
	}
	derived := domain.DerivedRevisionID(value.UUID)
	return &derived
}

func correctionOfficialPointer(value uuid.NullUUID) *domain.OfficialResultRevisionID {
	if !value.Valid || value.UUID == uuid.Nil {
		return nil
	}
	result := domain.OfficialResultRevisionID(value.UUID)
	return &result
}

func correctionScorePointer(value uuid.NullUUID) *domain.SeriesScoreRevisionID {
	if !value.Valid || value.UUID == uuid.Nil {
		return nil
	}
	score := domain.SeriesScoreRevisionID(value.UUID)
	return &score
}

func correctionJSONDocument(value any) ([]byte, [sha256.Size]byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, [sha256.Size]byte{}, err
	}
	return payload, sha256.Sum256(payload), nil
}

func correctionHexDigest(value [sha256.Size]byte) string {
	return hex.EncodeToString(value[:])
}

func correctionBytesEqual(first, second []byte) bool {
	return bytes.Equal(first, second)
}

func sortCorrectionProjections(projections []domain.ProjectionRevision) {
	sort.Slice(projections, func(first, second int) bool {
		left := projections[first].Revision()
		right := projections[second].Revision()
		if left.RevisionNo() != right.RevisionNo() {
			return left.RevisionNo() < right.RevisionNo()
		}
		return left.ID().UUID().String() < right.ID().UUID().String()
	})
}

func tournamentAdminCorrectionError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrConflict
	}
	return fmt.Errorf("TournamentAdminCorrectionPostgres - %s: %w", operation, err)
}

func correctionProjectionState(kind domain.ArtifactKind, entityID, sourceID uuid.UUID, revision int64, at time.Time) (domain.ProjectionRevision, error) {
	payload, _, err := correctionJSONDocument(struct {
		Schema   string `json:"schema"`
		Kind     string `json:"kind"`
		EntityID string `json:"entity_id"`
		SourceID string `json:"source_id"`
	}{
		Schema: "tournament-correction-baseline-v1", Kind: string(kind),
		EntityID: entityID.String(), SourceID: sourceID.String(),
	})
	if err != nil {
		return domain.ProjectionRevision{}, err
	}
	if revision < 1 {
		return domain.ProjectionRevision{}, domain.ErrValidation
	}
	var previous *domain.DerivedRevisionID
	if revision > 1 {
		previousValue := domain.DerivedRevisionID(correctionLogicalBaselineUUID(kind, uuid.NewSHA1(sourceID, []byte("previous"))))
		previous = &previousValue
	}
	return domain.NewProjectionRevision(
		domain.DerivedRevisionID(correctionLogicalBaselineUUID(kind, sourceID)), uuid.Nil,
		domain.ArtifactRef{Kind: kind, EntityID: entityID}, int(revision), previous, correctionTime(at), payload,
	)
}
