package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/capacity"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"

	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
)

// ExactDraftBranchPlanPostgres persists every reachable BO1 or BO3 draft branch.
// The generic assignment tables retain the selected task chain for each
// category child, while the exact-draft tables preserve the parent path and
// its immutable source authority.
type ExactDraftBranchPlanPostgres struct {
	tx     *TxManager
	drafts *DraftPostgres
}

var _ assignmentusecase.ExactDraftBranchPlanRepository = (*ExactDraftBranchPlanPostgres)(nil)
var _ playoff.ExactDraftPlanAuthorityReader = (*ExactDraftBranchPlanPostgres)(nil)

func NewExactDraftBranchPlanPostgres(
	tx *TxManager,
	drafts *DraftPostgres,
) *ExactDraftBranchPlanPostgres {
	return &ExactDraftBranchPlanPostgres{tx: tx, drafts: drafts}
}

func (r *ExactDraftBranchPlanPostgres) LoadExactDraftBranchPlanAuthority(
	ctx context.Context,
	draftID uuid.UUID,
) (assignmentusecase.ExactDraftBranchPlanAuthority, error) {
	if ctx == nil || r == nil || r.tx == nil || r.drafts == nil || draftID == uuid.Nil {
		return assignmentusecase.ExactDraftBranchPlanAuthority{}, domain.ErrValidation
	}
	var authority assignmentusecase.ExactDraftBranchPlanAuthority
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		loaded, err := r.loadExactDraftBranchPlanAuthorityTx(txCtx, draftID)
		authority = loaded
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return assignmentusecase.ExactDraftBranchPlanAuthority{}, assignmentusecase.ErrExactDraftBranchPlanNotFound
	}
	if err != nil {
		return assignmentusecase.ExactDraftBranchPlanAuthority{}, fmt.Errorf(
			"ExactDraftBranchPlanPostgres - LoadExactDraftBranchPlanAuthority: %w", err,
		)
	}
	return authority, nil
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func (r *ExactDraftBranchPlanPostgres) loadExactDraftBranchPlanAuthorityTx(
	ctx context.Context,
	draftID uuid.UUID,
) (assignmentusecase.ExactDraftBranchPlanAuthority, error) {
	stage, err := r.lockExactDraftPlanningStage(ctx, draftID)
	if err != nil {
		return assignmentusecase.ExactDraftBranchPlanAuthority{}, err
	}
	aggregate, err := r.drafts.Get(ctx, draftID)
	if err != nil {
		return assignmentusecase.ExactDraftBranchPlanAuthority{}, err
	}
	if len(aggregate.Revisions) == 0 {
		return assignmentusecase.ExactDraftBranchPlanAuthority{}, domain.ErrConflict
	}
	draft, err := participantDraftExecution(aggregate, stage.CurrentDraftRevision)
	if err != nil {
		return assignmentusecase.ExactDraftBranchPlanAuthority{}, err
	}
	if draft == nil || draft.ID != draftID || draft.SeriesID != stage.FinalSeriesID ||
		draft.RevisionID != stage.CurrentDraftRevisionID || draft.Revision != stage.CurrentDraftRevision ||
		draft.State != draftusecase.ExecutionStateActive {
		return assignmentusecase.ExactDraftBranchPlanAuthority{}, domain.ErrConflict
	}
	participants, err := r.lockExactDraftPlanningParticipants(ctx, draftID)
	if err != nil {
		return assignmentusecase.ExactDraftBranchPlanAuthority{}, err
	}
	if err := r.ensureExactDraftPlanningHistoryHead(ctx, draftID); err != nil {
		return assignmentusecase.ExactDraftBranchPlanAuthority{}, err
	}
	historyHead, err := r.lockExactDraftPlanningHistoryHead(ctx, draftID)
	if err != nil {
		return assignmentusecase.ExactDraftBranchPlanAuthority{}, err
	}
	history, err := r.lockExactDraftPlanningHistory(ctx, draftID)
	if err != nil {
		return assignmentusecase.ExactDraftBranchPlanAuthority{}, err
	}
	ids, err := exactDraftIdentity(stage.CommandID, stage.FinalSeriesID, domain.SeriesFormat(stage.SeriesFormat))
	if err != nil {
		return assignmentusecase.ExactDraftBranchPlanAuthority{}, err
	}
	if err := r.lockExactDraftPlanningReservationKeys(ctx, draftID); err != nil {
		return assignmentusecase.ExactDraftBranchPlanAuthority{}, err
	}
	candidates, err := r.lockExactDraftPlanningCandidates(ctx, sqlc.LockExactDraftPlanningCandidatesParams{DraftID: draftID, PlanID: ids.DraftAssignmentPlanID})
	if err != nil {
		return assignmentusecase.ExactDraftBranchPlanAuthority{}, err
	}
	return exactDraftPlanningAuthority(stage, *draft, participants, historyHead, history, candidates)
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func exactDraftPlanningAuthority(
	stage sqlc.LockExactDraftPlanningStageRow,
	draft draftusecase.Execution,
	participants []sqlc.LockExactDraftPlanningParticipantsRow,
	historyHead sqlc.LockExactDraftPlanningHistoryHeadRow,
	history []sqlc.LockExactDraftPlanningHistoryRow,
	candidates []sqlc.LockExactDraftPlanningCandidatesRow,
) (assignmentusecase.ExactDraftBranchPlanAuthority, error) {
	if stage.CommandID == uuid.Nil || stage.TournamentID == uuid.Nil || stage.RosterID == uuid.Nil ||
		stage.FinalSeriesID == uuid.Nil || stage.CategoryRevisionID == uuid.Nil ||
		stage.SourcePoolRevisionID == uuid.Nil || stage.SeriesRevision < 1 || stage.RosterRevision < 1 ||
		stage.CategoryRevision < 1 || stage.PoolRevision < 1 ||
		stage.PublishedProjectionRevisionID == uuid.Nil || stage.PublishedProjectionRevision < 1 ||
		stage.CurrentDraftRevisionID == uuid.Nil || stage.CurrentDraftRevision < 1 ||
		historyHead.RevisionID == uuid.Nil || historyHead.Revision < 1 {
		return assignmentusecase.ExactDraftBranchPlanAuthority{}, domain.ErrConflict
	}
	ids, err := exactDraftIdentity(stage.CommandID, stage.FinalSeriesID, domain.SeriesFormat(stage.SeriesFormat))
	if err != nil || ids.DraftID != draft.ID || ids.FinalSeriesID != stage.FinalSeriesID ||
		ids.CategoryRevisionID != stage.CategoryRevisionID {
		return assignmentusecase.ExactDraftBranchPlanAuthority{}, domain.ErrConflict
	}
	graphDigest, err := exactDraftDigest(stage.GraphDigest)
	if err != nil {
		return assignmentusecase.ExactDraftBranchPlanAuthority{}, err
	}
	artifactDigest, err := exactDraftDigest(stage.ArtifactDigest)
	if err != nil {
		return assignmentusecase.ExactDraftBranchPlanAuthority{}, err
	}
	reservations, err := exactDraftParticipants(stage, draft, participants)
	if err != nil {
		return assignmentusecase.ExactDraftBranchPlanAuthority{}, err
	}
	uses, err := exactDraftHistory(draft, history)
	if err != nil {
		return assignmentusecase.ExactDraftBranchPlanAuthority{}, err
	}
	pool, taskVersions, err := exactDraftCandidates(stage, candidates)
	if err != nil {
		return assignmentusecase.ExactDraftBranchPlanAuthority{}, err
	}
	paths, err := assignmentusecase.ReachableExactDraftBranches(draft)
	if err != nil {
		return assignmentusecase.ExactDraftBranchPlanAuthority{}, err
	}
	authority := assignmentusecase.ExactDraftBranchPlanAuthority{
		Draft:                   draftusecase.CloneExecution(draft),
		Branches:                make([]assignmentusecase.ExactDraftBranchAuthority, len(paths)),
		UnavailableTaskVersions: exactDraftUnavailableCandidates(candidates),
	}
	for branchIndex, path := range paths {
		branch := assignmentusecase.ExactDraftBranchAuthority{
			Key:         path.Key,
			Assignments: make([]assignmentusecase.ExactNormalAssignmentAuthority, len(path.Categories)),
		}
		for categoryIndex, category := range path.Categories {
			position := categoryIndex + 1
			slotID, ok := exactDraftSlotID(ids, position)
			if !ok {
				return assignmentusecase.ExactDraftBranchPlanAuthority{}, domain.ErrConflict
			}
			branch.Assignments[categoryIndex] = assignmentusecase.ExactNormalAssignmentAuthority{
				Scope: assignmentusecase.ExactNormalAssignmentScope{
					TournamentID:   stage.TournamentID,
					RosterID:       stage.RosterID,
					SeriesID:       stage.FinalSeriesID,
					SlotID:         slotID,
					CategoryLockID: stage.CategoryRevisionID,
				},
				Category: category,
				Revisions: assignmentusecase.ExactNormalAssignmentSourceRevisions{
					SeriesRevision:     stage.SeriesRevision,
					PoolRevisionID:     stage.SourcePoolRevisionID,
					PoolRevision:       stage.PoolRevision,
					HistoryRevisionID:  historyHead.RevisionID,
					HistoryRevision:    historyHead.Revision,
					RosterRevision:     stage.RosterRevision,
					ArtifactRevisionID: stage.PublishedProjectionRevisionID,
					ArtifactRevision:   stage.PublishedProjectionRevision,
					CategoryRevisionID: stage.CategoryRevisionID,
					CategoryRevision:   stage.CategoryRevision,
				},
				Pool:                    domain.CloneTaskPool(pool),
				ParticipantIDs:          exactDraftParticipantIDs(reservations),
				ParticipantReservations: append([]assignmentusecase.ExactNormalParticipantReservation(nil), reservations...),
				History:                 append([]capacity.TaskUse(nil), uses...),
				Candidates:              append([]assignmentusecase.ExactNormalTaskVersion(nil), taskVersions...),
				GraphDigest:             graphDigest,
				ArtifactDigest:          artifactDigest,
			}
		}
		authority.Branches[branchIndex] = branch
	}
	return authority, nil
}

func exactDraftDigest(value []byte) ([sha256.Size]byte, error) {
	if len(value) != sha256.Size {
		return [sha256.Size]byte{}, domain.ErrConflict
	}
	var digest [sha256.Size]byte
	copy(digest[:], value)
	if digest == [sha256.Size]byte{} {
		return [sha256.Size]byte{}, domain.ErrConflict
	}
	return digest, nil
}

func exactDraftParticipants(
	stage sqlc.LockExactDraftPlanningStageRow,
	draft draftusecase.Execution,
	rows []sqlc.LockExactDraftPlanningParticipantsRow,
) ([]assignmentusecase.ExactNormalParticipantReservation, error) {
	if len(rows) != 2 {
		return nil, domain.ErrConflict
	}
	result := make([]assignmentusecase.ExactNormalParticipantReservation, len(rows))
	for index, row := range rows {
		if row.ParticipantID == uuid.Nil || row.PlayerID == uuid.Nil || row.ReservationID == uuid.Nil ||
			row.TournamentID != stage.TournamentID || row.ReservationRevision < 1 ||
			(row.ParticipantID != draft.FirstParticipantID && row.ParticipantID != draft.SecondParticipantID) {
			return nil, domain.ErrConflict
		}
		result[index] = assignmentusecase.ExactNormalParticipantReservation{
			ParticipantID: row.ParticipantID,
			PlayerID:      row.PlayerID,
			Reservation: domain.ParticipantReservation{
				PlayerID: row.PlayerID, ReservationID: row.ReservationID,
				TournamentID: row.TournamentID, Revision: row.ReservationRevision,
				AcquiredAt: row.AcquiredAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(),
			},
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ParticipantID.String() < result[j].ParticipantID.String() })
	if result[0].ParticipantID == result[1].ParticipantID {
		return nil, domain.ErrConflict
	}
	return result, nil
}

func exactDraftParticipantIDs(
	reservations []assignmentusecase.ExactNormalParticipantReservation,
) []uuid.UUID {
	ids := make([]uuid.UUID, len(reservations))
	for index, reservation := range reservations {
		ids[index] = reservation.ParticipantID
	}
	return ids
}

func exactDraftHistory(
	draft draftusecase.Execution,
	rows []sqlc.LockExactDraftPlanningHistoryRow,
) ([]capacity.TaskUse, error) {
	allowed := map[uuid.UUID]struct{}{
		draft.FirstParticipantID:  {},
		draft.SecondParticipantID: {},
	}
	result := make([]capacity.TaskUse, len(rows))
	type historyKey struct {
		participantID uuid.UUID
		ref           domain.TaskVersionRef
	}
	seen := make(map[historyKey]struct{}, len(rows))
	for index, row := range rows {
		if row.ParticipantID == uuid.Nil || row.TaskID == uuid.Nil || row.TaskVersion < 1 {
			return nil, domain.ErrConflict
		}
		if _, ok := allowed[row.ParticipantID]; !ok {
			return nil, domain.ErrConflict
		}
		ref := domain.TaskVersionRef{TaskID: row.TaskID, Version: int(row.TaskVersion)}
		key := historyKey{participantID: row.ParticipantID, ref: ref}
		if _, duplicate := seen[key]; duplicate {
			return nil, domain.ErrConflict
		}
		seen[key] = struct{}{}
		result[index] = capacity.TaskUse{ParticipantID: row.ParticipantID, TaskID: row.TaskID, Version: int(row.TaskVersion)}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ParticipantID != result[j].ParticipantID {
			return result[i].ParticipantID.String() < result[j].ParticipantID.String()
		}
		return domain.CompareTaskVersionRefs(
			domain.TaskVersionRef{TaskID: result[i].TaskID, Version: result[i].Version},
			domain.TaskVersionRef{TaskID: result[j].TaskID, Version: result[j].Version},
		) < 0
	})
	return result, nil
}

func exactDraftCandidates(
	stage sqlc.LockExactDraftPlanningStageRow,
	rows []sqlc.LockExactDraftPlanningCandidatesRow,
) (domain.TaskPoolRevision, []assignmentusecase.ExactNormalTaskVersion, error) {
	if len(rows) < domain.AssignmentReserveCount+1 {
		return domain.TaskPoolRevision{}, nil, domain.ErrConflict
	}
	pool := domain.TaskPoolRevision{
		ID: stage.SourcePoolRevisionID, Revision: stage.PoolRevision,
		Kind: domain.AssignmentTaskKindNormal, Versions: make([]domain.TaskVersionRef, len(rows)),
	}
	candidates := make([]assignmentusecase.ExactNormalTaskVersion, len(rows))
	seen := make(map[uuid.UUID]struct{}, len(rows))
	for index, row := range rows {
		if row.TaskID == uuid.Nil || row.TaskVersion < 1 || row.PoolRevisionID != pool.ID ||
			row.PoolRevision != pool.Revision {
			return domain.TaskPoolRevision{}, nil, domain.ErrConflict
		}
		if _, duplicate := seen[row.TaskID]; duplicate {
			return domain.TaskPoolRevision{}, nil, domain.ErrConflict
		}
		seen[row.TaskID] = struct{}{}
		pool.Versions[index] = domain.TaskVersionRef{TaskID: row.TaskID, Version: int(row.TaskVersion)}
		candidates[index] = assignmentusecase.ExactNormalTaskVersion{
			PoolRevisionID: row.PoolRevisionID,
			Version:        int(row.TaskVersion),
			Task: domain.Task{
				ID: row.TaskID, Title: row.Title, Description: row.Description,
				Category: domain.Category(row.Category), Difficulty: domain.Difficulty(row.Difficulty),
				TimeLimit: int(row.TimeLimit), Flag: row.Flag,
				Hints:   taskHintsToDomain(row.Hint1, row.Hint2, row.Hint3),
				TaskURL: row.TaskUrl, SourceFileURL: row.SourceFileUrl,
				Kind: domain.TaskKindNormal, Enabled: true, CurrentVersion: int(row.TaskVersion),
				CreatedAt: row.TaskCreatedAt.Time.UTC(),
			},
		}
	}
	normalized, err := domain.NormalizeTaskPoolRevision(pool, domain.AssignmentTaskKindNormal)
	if err != nil {
		return domain.TaskPoolRevision{}, nil, err
	}
	sort.Slice(candidates, func(i, j int) bool {
		return domain.CompareTaskVersionRefs(
			domain.TaskVersionRef{TaskID: candidates[i].Task.ID, Version: candidates[i].Version},
			domain.TaskVersionRef{TaskID: candidates[j].Task.ID, Version: candidates[j].Version},
		) < 0
	})
	return normalized, candidates, nil
}

func exactDraftSlotID(ids playoff.FinalStageIDs, position int) (uuid.UUID, bool) {
	switch position {
	case 1:
		return ids.FirstSlotID, true
	case 2:
		return ids.SecondSlotID, true
	case 3:
		return ids.ThirdSlotID, true
	default:
		return uuid.Nil, false
	}
}

func exactDraftCategoriesJSON(categories []domain.Category) ([]byte, error) {
	return categoryJSON(categories)
}

func exactDraftCategories(value []byte) ([]domain.Category, error) {
	var values []string
	if err := json.Unmarshal(value, &values); err != nil {
		return nil, err
	}
	result := make([]domain.Category, len(values))
	for index, value := range values {
		result[index] = domain.Category(value)
		if !result[index].IsValid() {
			return nil, domain.ErrConflict
		}
	}
	return result, nil
}

func (r *ExactDraftBranchPlanPostgres) CommitExactDraftBranchPlan(
	ctx context.Context,
	plan assignmentusecase.ExactDraftBranchPlan,
) (*assignmentusecase.ExactDraftBranchPlan, bool, error) {
	if ctx == nil || r == nil || r.tx == nil || r.drafts == nil || plan.Validate() != nil ||
		plan.State != assignmentusecase.ExactDraftBranchPlanStatePlanned {
		return nil, false, domain.ErrValidation
	}
	var result *assignmentusecase.ExactDraftBranchPlan
	changed := false
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		stored, storedChanged, err := r.commitExactDraftBranchPlanTx(txCtx, plan)
		if err != nil {
			return err
		}
		result = stored
		changed = storedChanged
		return nil
	})
	if err != nil {
		return nil, false, mapExactDraftRepositoryError("CommitExactDraftBranchPlan", err)
	}
	return result, changed, nil
}

func (r *ExactDraftBranchPlanPostgres) commitExactDraftBranchPlanTx(
	ctx context.Context,
	plan assignmentusecase.ExactDraftBranchPlan,
) (*assignmentusecase.ExactDraftBranchPlan, bool, error) {
	querier := r.tx.Querier(ctx)
	_, err := querier.LockExactDraftAssignmentPlan(ctx, plan.ID)
	if err == nil {
		stored, _, loadErr := r.loadExactDraftBranchActivationTx(ctx, plan.ID)
		if loadErr != nil {
			return nil, false, loadErr
		}
		if stored == nil || stored.RevisionID != plan.RevisionID || stored.ProofHash != plan.ProofHash {
			return nil, false, domain.ErrConflict
		}
		return stored, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, err
	}

	stage, err := r.lockExactDraftPlanningStage(ctx, plan.SourceDraft.ID)
	if err != nil {
		return nil, false, err
	}
	if err := exactDraftPlanMatchesStage(plan, stage); err != nil {
		return nil, false, err
	}
	if err := r.insertExactDraftBranchPlanTx(ctx, stage, plan); err != nil {
		return nil, false, err
	}
	cloned := cloneExactDraftPlan(plan)
	return &cloned, true, nil
}

func exactDraftPlanMatchesStage(
	plan assignmentusecase.ExactDraftBranchPlan,
	stage sqlc.LockExactDraftPlanningStageRow,
) error {
	ids, err := exactDraftIdentity(stage.CommandID, stage.FinalSeriesID, domain.SeriesFormat(stage.SeriesFormat))
	if err != nil || stage.FinalSeriesID != plan.SourceDraft.SeriesID || stage.DraftID != plan.SourceDraft.ID ||
		stage.CurrentDraftRevisionID != plan.SourceDraft.RevisionID ||
		stage.CurrentDraftRevision != plan.SourceDraft.Revision ||
		ids.DraftAssignmentPlanID != plan.ID || ids.DraftAssignmentRevisionID != plan.RevisionID ||
		stage.CurrentDraftState != string(draftusecase.ExecutionStateActive) ||
		plan.SourceDraft.State != draftusecase.ExecutionStateActive || plan.CreatedAt.Before(stage.CreatedAt.Time.UTC()) {
		return domain.ErrConflict
	}
	return nil
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func (r *ExactDraftBranchPlanPostgres) insertExactDraftBranchPlanTx(
	ctx context.Context,
	stage sqlc.LockExactDraftPlanningStageRow,
	plan assignmentusecase.ExactDraftBranchPlan,
) error {
	querier := r.tx.Querier(ctx)
	constraintGraph, proofEvidence, err := exactDraftPlanDocuments(plan)
	if err != nil {
		return err
	}
	if err := querier.CreateExactDraftAssignmentPlan(ctx, sqlc.CreateExactDraftAssignmentPlanParams{
		ID:                    plan.ID,
		TournamentID:          stage.TournamentID,
		RosterID:              stage.RosterID,
		RevisionID:            plan.RevisionID,
		SourceRosterRevision:  stage.RosterRevision,
		SourcePoolRevisionID:  stage.SourcePoolRevisionID,
		SourceDraftRevisionID: uuid.NullUUID{UUID: plan.SourceDraft.RevisionID, Valid: true},
		ReachableBranchCount:  int32(len(plan.Branches)), //nolint:gosec // the domain already bounded the reachable branch set.
		ConstraintGraph:       constraintGraph,
		ProofEvidence:         proofEvidence,
		ProofHash:             &plan.ProofHash,
		CreatedAt:             tstz(plan.CreatedAt),
	}); err != nil {
		return err
	}
	participants, err := r.lockExactDraftPlanningParticipants(ctx, plan.SourceDraft.ID)
	if err != nil {
		return err
	}
	currentParticipants, err := exactDraftParticipants(stage, plan.SourceDraft, participants)
	if err != nil || !exactDraftPlanHasParticipants(plan, currentParticipants) {
		return domain.ErrConflict
	}
	if err := r.ensureExactDraftPlanningHistoryHead(ctx, plan.SourceDraft.ID); err != nil {
		return err
	}
	historyHead, err := r.lockExactDraftPlanningHistoryHead(ctx, plan.SourceDraft.ID)
	if err != nil || !exactDraftPlanHasHistoryHead(plan, historyHead) {
		return domain.ErrConflict
	}
	history, err := r.lockExactDraftPlanningHistory(ctx, plan.SourceDraft.ID)
	if err != nil {
		return err
	}
	currentHistory, err := exactDraftHistory(plan.SourceDraft, history)
	if err != nil || !exactDraftPlanHasHistory(plan, currentHistory) {
		return domain.ErrConflict
	}
	if err := r.lockExactDraftPlanningReservationKeys(ctx, plan.SourceDraft.ID); err != nil {
		return err
	}
	candidates, err := r.lockExactDraftPlanningCandidates(ctx, sqlc.LockExactDraftPlanningCandidatesParams{DraftID: plan.SourceDraft.ID, PlanID: plan.ID})
	if err != nil {
		return err
	}
	_, currentCandidates, err := exactDraftCandidates(stage, candidates)
	if err != nil || validateExactDraftPlanCandidates(plan, currentCandidates, exactDraftUnavailableCandidates(candidates)) != nil {
		return domain.ErrConflict
	}
	for _, branch := range plan.Branches {
		if err := r.insertExactDraftBranchTx(ctx, stage, plan, branch, participants, candidates); err != nil {
			return err
		}
	}
	return nil
}

func exactDraftPlanDocuments(plan assignmentusecase.ExactDraftBranchPlan) ([]byte, []byte, error) {
	constraintGraph, err := requiredJSONObject(map[string]any{
		"schema_version":           1,
		"kind":                     "exact_draft",
		"source_draft_revision_id": plan.SourceDraft.RevisionID.String(),
		"reachable_branch_count":   len(plan.Branches),
	})
	if err != nil {
		return nil, nil, err
	}
	proofEvidence, err := requiredJSONObject(map[string]any{
		"schema_version": 1,
		"proof_hash":     plan.ProofHash,
	})
	if err != nil {
		return nil, nil, err
	}
	return constraintGraph, proofEvidence, nil
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func (r *ExactDraftBranchPlanPostgres) insertExactDraftBranchTx(
	ctx context.Context,
	stage sqlc.LockExactDraftPlanningStageRow,
	plan assignmentusecase.ExactDraftBranchPlan,
	branch assignmentusecase.ExactDraftBranch,
	participants []sqlc.LockExactDraftPlanningParticipantsRow,
	candidates []sqlc.LockExactDraftPlanningCandidatesRow,
) error {
	querier := r.tx.Querier(ctx)
	categories, err := exactDraftCategoriesJSON(branch.Path.Categories)
	if err != nil {
		return err
	}
	if err := querier.CreateExactDraftAssignmentBranch(ctx, sqlc.CreateExactDraftAssignmentBranchParams{
		ID: branch.ID, PlanID: plan.ID, DraftID: plan.SourceDraft.ID,
		DraftRevisionID: plan.SourceDraft.RevisionID, BranchKey: branch.Path.Key,
		CategorySequence: categories, CreatedAt: tstz(plan.CreatedAt),
	}); err != nil {
		return err
	}
	ids, err := exactDraftIdentity(stage.CommandID, stage.FinalSeriesID, domain.SeriesFormat(stage.SeriesFormat))
	if err != nil {
		return err
	}
	if len(branch.Assignments) != exactDraftCategoryCount(plan.SourceDraft.Format) {
		return domain.ErrConflict
	}
	for assignmentIndex, assignment := range branch.Assignments {
		position := assignmentIndex + 1
		positionValue := int16(position)
		algorithmVersion := assignment.Plan.DecisionEvidence.AlgorithmVersion
		childID := assignment.Plan.BranchID
		if childID != branch.ChildBranchIDs[assignmentIndex] ||
			childID != ids.DraftAssignmentChildBranchID(branch.Path.Key, position) {
			return domain.ErrConflict
		}
		childCategories, categoryErr := exactDraftCategoriesJSON([]domain.Category{assignment.Plan.Category})
		if categoryErr != nil {
			return categoryErr
		}
		inputs, inputErr := marshalJSON(
			"ExactDraftBranchPlanPostgres - decision inputs",
			assignment.Plan.DecisionEvidence.NormalizedInputs,
		)
		if inputErr != nil {
			return inputErr
		}
		result, resultErr := marshalJSON(
			"ExactDraftBranchPlanPostgres - decision result",
			assignment.Plan.DecisionEvidence.Result,
		)
		if resultErr != nil {
			return resultErr
		}
		if err := querier.CreateExactDraftAssignmentChild(ctx, sqlc.CreateExactDraftAssignmentChildParams{
			ID: childID, PlanID: plan.ID, DraftID: nullableUUID(&plan.SourceDraft.ID),
			DraftRevisionID: nullableUUID(&plan.SourceDraft.RevisionID),
			BranchKey:       exactDraftChildKey(branch.Path.Key, position), CategorySequence: childCategories,
			ExactDraftBranchID:       nullableUUIDValue(branch.ID),
			ExactDraftPosition:       &positionValue,
			DecisionEvidenceID:       nullableUUIDValue(assignment.Plan.DecisionEvidence.ID),
			DecisionAlgorithmVersion: &algorithmVersion,
			DecisionInputs:           inputs, DecisionSeed: append([]byte(nil), assignment.Plan.DecisionEvidence.Seed[:]...),
			DecisionResult:       result,
			DecisionReplayDigest: append([]byte(nil), assignment.Plan.DecisionEvidence.ReplayDigest[:]...),
			DecisionOwnerID:      nullableUUIDValue(assignment.Plan.DecisionEvidence.OwnerID),
			DecidedAt:            tstz(assignment.Plan.DecisionEvidence.DecidedAt), CreatedAt: tstz(plan.CreatedAt),
		}); err != nil {
			return err
		}
		if err := r.insertExactDraftChildSourceTx(
			ctx, plan, childID, assignment.Plan, participants, candidates,
		); err != nil {
			return err
		}
		for _, edge := range assignment.Plan.SelectedEdges {
			selectionEvidence, evidenceErr := requiredJSONObject(map[string]any{
				"schema_version":       1,
				"decision_evidence_id": assignment.Plan.DecisionEvidence.ID.String(),
				"position":             edge.Position,
			})
			if evidenceErr != nil {
				return evidenceErr
			}
			if _, err := querier.CreateAssignmentPlanEdge(ctx, sqlc.CreateAssignmentPlanEdgeParams{
				//nolint:gosec // Domain validation bounds this value before the storage conversion.
				ID: edge.ID, PlanID: plan.ID, BranchID: childID, Position: int16(edge.Position),
				//nolint:gosec // Domain validation bounds this value before the storage conversion.
				TaskID: edge.Snapshot.TaskID, TaskVersion: int32(edge.Snapshot.Version),
				SelectionEvidence: selectionEvidence, CreatedAt: tstz(plan.CreatedAt),
			}); err != nil {
				return err
			}
			if _, err := querier.CreateAssignmentTaskVersionReservation(ctx, sqlc.CreateAssignmentTaskVersionReservationParams{
				ID: edge.ReservationID, EdgeID: edge.ID, PlanID: plan.ID, BranchID: childID,
				//nolint:gosec // Domain validation bounds this value before the storage conversion.
				TaskID: edge.Snapshot.TaskID, TaskVersion: int32(edge.Snapshot.Version), CreatedAt: tstz(plan.CreatedAt),
			}); err != nil {
				return err
			}
			snapshotParams, snapshotErr := taskSnapshotParams(AssignmentEdgeInput{
				ID: edge.ID, ReservationID: edge.ReservationID, Position: edge.Position,
				Snapshot: edge.Snapshot, ContentDigest: edge.ContentDigest,
			}, plan.CreatedAt)
			if snapshotErr != nil {
				return snapshotErr
			}
			if _, err := querier.CreateAssignmentTaskSnapshot(ctx, snapshotParams); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *ExactDraftBranchPlanPostgres) insertExactDraftChildSourceTx(
	ctx context.Context,
	plan assignmentusecase.ExactDraftBranchPlan,
	childID uuid.UUID,
	exact assignmentusecase.ExactNormalAssignmentPlan,
	participants []sqlc.LockExactDraftPlanningParticipantsRow,
	candidates []sqlc.LockExactDraftPlanningCandidatesRow,
) error {
	querier := r.tx.Querier(ctx)
	if exact.Validate() != nil {
		return domain.ErrConflict
	}
	if err := querier.CreateExactDraftAssignmentChildSource(ctx, sqlc.CreateExactDraftAssignmentChildSourceParams{
		ChildBranchID: childID, PlanID: plan.ID, TournamentID: exact.Scope.TournamentID,
		RosterID: exact.Scope.RosterID, SeriesID: exact.Scope.SeriesID, SlotID: exact.Scope.SlotID,
		CategoryLockID: exact.Scope.CategoryLockID,
		SeriesRevision: exact.Revisions.SeriesRevision,
		PoolRevisionID: exact.Revisions.PoolRevisionID, PoolRevision: exact.Revisions.PoolRevision,
		HistoryRevisionID: exact.Revisions.HistoryRevisionID, HistoryRevision: exact.Revisions.HistoryRevision,
		RosterRevision:     exact.Revisions.RosterRevision,
		ArtifactRevisionID: exact.Revisions.ArtifactRevisionID, ArtifactRevision: exact.Revisions.ArtifactRevision,
		CategoryRevisionID: exact.Revisions.CategoryRevisionID, CategoryRevision: exact.Revisions.CategoryRevision,
		GraphDigest:    append([]byte(nil), exact.GraphDigest[:]...),
		ArtifactDigest: append([]byte(nil), exact.ArtifactDigest[:]...),
		ProofHash:      exact.ProofHash, CreatedAt: tstz(plan.CreatedAt),
	}); err != nil {
		return err
	}
	for _, participant := range participants {
		if err := querier.CreateExactDraftAssignmentChildParticipant(ctx, sqlc.CreateExactDraftAssignmentChildParticipantParams{
			ChildBranchID: childID, PlanID: plan.ID, ParticipantID: participant.ParticipantID,
			PlayerID: participant.PlayerID, ReservationID: participant.ReservationID,
			TournamentID: participant.TournamentID, ReservationRevision: participant.ReservationRevision,
			AcquiredAt: participant.AcquiredAt, UpdatedAt: participant.UpdatedAt, CreatedAt: tstz(plan.CreatedAt),
		}); err != nil {
			return err
		}
	}
	for _, use := range exact.History {
		version, err := progressionInt32(use.Version)
		if err != nil || version < 1 {
			return domain.ErrConflict
		}
		if err := querier.CreateExactDraftAssignmentChildHistory(ctx, sqlc.CreateExactDraftAssignmentChildHistoryParams{
			ChildBranchID: childID, PlanID: plan.ID, ParticipantID: use.ParticipantID,
			TaskID: use.TaskID, TaskVersion: version, CreatedAt: tstz(plan.CreatedAt),
		}); err != nil {
			return err
		}
	}
	for _, candidate := range candidates {
		if err := querier.CreateExactDraftAssignmentChildCandidate(ctx, sqlc.CreateExactDraftAssignmentChildCandidateParams{
			ChildBranchID: childID, PlanID: plan.ID, TaskID: candidate.TaskID,
			TaskVersion: candidate.TaskVersion, PoolRevisionID: candidate.PoolRevisionID,
			CreatedAt: tstz(plan.CreatedAt),
		}); err != nil {
			return err
		}
	}
	return nil
}

func exactDraftChildKey(key string, position int) string {
	return fmt.Sprintf("%s#%d", key, position)
}

func exactDraftPlanHasHistory(plan assignmentusecase.ExactDraftBranchPlan, history []capacity.TaskUse) bool {
	for _, branch := range plan.Branches {
		for _, assignment := range branch.Assignments {
			if len(assignment.Plan.History) != len(history) {
				return false
			}
			for index, use := range history {
				if assignment.Plan.History[index] != use {
					return false
				}
			}
		}
	}
	return true
}

func exactDraftPlanHasHistoryHead(
	plan assignmentusecase.ExactDraftBranchPlan,
	head sqlc.LockExactDraftPlanningHistoryHeadRow,
) bool {
	if head.RevisionID == uuid.Nil || head.Revision < 1 {
		return false
	}
	for _, branch := range plan.Branches {
		for _, assignment := range branch.Assignments {
			revisions := assignment.Plan.Revisions
			if revisions.HistoryRevisionID != head.RevisionID || revisions.HistoryRevision != head.Revision {
				return false
			}
		}
	}
	return true
}

func exactDraftPlanHasParticipants(
	plan assignmentusecase.ExactDraftBranchPlan,
	participants []assignmentusecase.ExactNormalParticipantReservation,
) bool {
	for _, branch := range plan.Branches {
		for _, assignment := range branch.Assignments {
			if len(assignment.Plan.ParticipantReservations) != len(participants) {
				return false
			}
			for index, participant := range participants {
				if assignment.Plan.ParticipantReservations[index] != participant {
					return false
				}
			}
		}
	}
	return true
}

func exactDraftPlanHasCandidates(
	plan assignmentusecase.ExactDraftBranchPlan,
	candidates []assignmentusecase.ExactNormalTaskVersion,
) bool {
	return validateExactDraftPlanCandidates(plan, candidates) == nil
}

func exactDraftUnavailableCandidates(rows []sqlc.LockExactDraftPlanningCandidatesRow) []domain.TaskVersionRef {
	var refs []domain.TaskVersionRef
	for _, row := range rows {
		if row.Unavailable {
			refs = append(refs, domain.TaskVersionRef{TaskID: row.TaskID, Version: int(row.TaskVersion)})
		}
	}
	return refs
}

func validateExactDraftPlanCandidates(plan assignmentusecase.ExactDraftBranchPlan, candidates []assignmentusecase.ExactNormalTaskVersion, exclusions ...[]domain.TaskVersionRef) error {
	for _, branch := range plan.Branches {
		unavailable := make(map[domain.TaskVersionRef]struct{})
		for _, refs := range exclusions {
			for _, ref := range refs {
				if ref.TaskID == uuid.Nil || ref.Version < 1 {
					return domain.ErrConflict
				}
				if _, duplicate := unavailable[ref]; duplicate {
					return domain.ErrConflict
				}
				found := false
				for _, candidate := range candidates {
					if candidate.Task.ID == ref.TaskID && candidate.Version == ref.Version {
						found = true
						break
					}
				}
				if !found {
					return domain.ErrConflict
				}
				unavailable[ref] = struct{}{}
			}
		}
		for _, assignment := range branch.Assignments {
			exact := assignment.Plan
			if err := assignmentusecase.ValidateExactNormalAssignmentCandidates(exact, candidates, unavailable); err != nil {
				return err
			}
			for _, edge := range exact.SelectedEdges {
				unavailable[domain.TaskVersionRef{TaskID: edge.Snapshot.TaskID, Version: edge.Snapshot.Version}] = struct{}{}
			}
		}
	}
	return nil
}

func cloneExactDraftPlan(plan assignmentusecase.ExactDraftBranchPlan) assignmentusecase.ExactDraftBranchPlan {
	cloned := plan
	cloned.SourceDraft = draftusecase.CloneExecution(plan.SourceDraft)
	cloned.CompletedCategories = append([]domain.Category(nil), plan.CompletedCategories...)
	cloned.Branches = append([]assignmentusecase.ExactDraftBranch(nil), plan.Branches...)
	return cloned
}

func mapExactDraftRepositoryError(operation string, err error) error {
	if errors.Is(err, domain.ErrConflict) || errors.Is(err, domain.ErrValidation) ||
		errors.Is(err, assignmentusecase.ErrExactDraftBranchPlanNotFound) {
		return err
	}
	return mapRepositoryWriteError("ExactDraftBranchPlanPostgres - "+operation, err)
}

func (r *ExactDraftBranchPlanPostgres) LoadExactDraftBranchActivation(
	ctx context.Context,
	planID uuid.UUID,
) (*assignmentusecase.ExactDraftBranchPlan, *draftusecase.Execution, error) {
	if ctx == nil || r == nil || r.tx == nil || r.drafts == nil || planID == uuid.Nil {
		return nil, nil, domain.ErrValidation
	}
	var plan *assignmentusecase.ExactDraftBranchPlan
	var current *draftusecase.Execution
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		var err error
		plan, current, err = r.loadExactDraftBranchActivationTx(txCtx, planID)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, assignmentusecase.ErrExactDraftBranchPlanNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("ExactDraftBranchPlanPostgres - LoadExactDraftBranchActivation: %w", err)
	}
	return plan, current, nil
}

func (r *ExactDraftBranchPlanPostgres) CommitExactDraftBranchActivation(
	ctx context.Context,
	next assignmentusecase.ExactDraftBranchPlan,
) (*assignmentusecase.ExactDraftBranchPlan, bool, error) {
	if ctx == nil || r == nil || r.tx == nil || r.drafts == nil || next.Validate() != nil ||
		next.State != assignmentusecase.ExactDraftBranchPlanStateCommitted {
		return nil, false, domain.ErrValidation
	}
	var result *assignmentusecase.ExactDraftBranchPlan
	changed := false
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		var commitErr error
		result, changed, commitErr = r.commitExactDraftBranchActivationTx(txCtx, next)
		return commitErr
	})
	if err != nil {
		return nil, false, mapExactDraftRepositoryError("CommitExactDraftBranchActivation", err)
	}
	return result, changed, nil
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func (r *ExactDraftBranchPlanPostgres) commitExactDraftBranchActivationTx(
	ctx context.Context,
	next assignmentusecase.ExactDraftBranchPlan,
) (*assignmentusecase.ExactDraftBranchPlan, bool, error) {
	stored, current, err := r.loadExactDraftBranchActivationTx(ctx, next.ID)
	if err != nil {
		return nil, false, err
	}
	if stored == nil || current == nil || stored.RevisionID != next.RevisionID ||
		stored.ProofHash != next.ProofHash || stored.SourceDraft.ID != next.SourceDraft.ID ||
		stored.SourceDraft.RevisionID != next.SourceDraft.RevisionID {
		return nil, false, domain.ErrConflict
	}
	if stored.State == assignmentusecase.ExactDraftBranchPlanStateCommitted {
		if !sameExactDraftActivation(*stored, next) {
			return nil, false, domain.ErrConflict
		}
		cloned := cloneExactDraftPlan(*stored)
		return &cloned, false, nil
	}
	if stored.State != assignmentusecase.ExactDraftBranchPlanStatePlanned || current.Validate() != nil ||
		current.State != draftusecase.ExecutionStateCompleted || current.ID != next.SourceDraft.ID ||
		current.RevisionID != next.CompletionDraftRevisionID || current.Revision != next.CompletionDraftRevision ||
		!sameCategories(current.SelectedCategories, next.CompletedCategories) {
		return nil, false, domain.ErrConflict
	}
	active, ok := activeExactDraftBranch(next)
	if !ok {
		return nil, false, domain.ErrConflict
	}
	activeChildID := active.ChildBranchIDs[0]
	if activeChildID == uuid.Nil || activeChildID != active.Assignments[0].Plan.BranchID {
		return nil, false, domain.ErrConflict
	}
	querier := r.tx.Querier(ctx)
	releaseReason := exactDraftReleaseReason(next)
	activeGroupID := nullableUUIDValue(active.ID)
	releasedReservations, err := querier.ReleaseLosingExactDraftChildReservations(ctx, sqlc.ReleaseLosingExactDraftChildReservationsParams{
		ReleasedAt: tstz(next.CommittedAt), ReleaseReason: &releaseReason,
		PlanID: next.ID, ExactDraftBranchID: activeGroupID,
	})
	if err != nil || len(releasedReservations) != (len(next.Branches)-1)*exactDraftCategoryCount(next.SourceDraft.Format)*(domain.AssignmentReserveCount+1) {
		return nil, false, exactDraftRowsError(err)
	}
	releasedChildren, err := querier.ReleaseLosingExactDraftChildren(ctx, sqlc.ReleaseLosingExactDraftChildrenParams{
		ReleasedAt: tstz(next.CommittedAt), ReleaseReason: &releaseReason,
		PlanID: next.ID, ExactDraftBranchID: activeGroupID,
	})
	if err != nil || len(releasedChildren) != (len(next.Branches)-1)*exactDraftCategoryCount(next.SourceDraft.Format) {
		return nil, false, exactDraftRowsError(err)
	}
	releasedGroups, err := querier.ReleaseLosingExactDraftBranches(ctx, sqlc.ReleaseLosingExactDraftBranchesParams{
		PlanID: next.ID, ExactDraftBranchID: active.ID, ReleasedAt: tstz(next.CommittedAt),
		ReleaseReason: &releaseReason,
	})
	if err != nil || releasedGroups != int64(len(next.Branches)-1) {
		return nil, false, exactDraftRowsError(err)
	}
	committedReservations, err := querier.CommitExactDraftChildReservations(ctx, sqlc.CommitExactDraftChildReservationsParams{
		CommittedAt: tstz(next.CommittedAt), PlanID: next.ID, ExactDraftBranchID: activeGroupID,
	})
	if err != nil || len(committedReservations) != exactDraftCategoryCount(next.SourceDraft.Format)*(domain.AssignmentReserveCount+1) {
		return nil, false, exactDraftRowsError(err)
	}
	activatedChildren, err := querier.ActivateExactDraftChildren(ctx, sqlc.ActivateExactDraftChildrenParams{
		ActivatedAt: tstz(next.CommittedAt), PlanID: next.ID, ExactDraftBranchID: activeGroupID,
	})
	if err != nil || len(activatedChildren) != exactDraftCategoryCount(next.SourceDraft.Format) {
		return nil, false, exactDraftRowsError(err)
	}
	changedGroups, err := querier.ActivateExactDraftBranch(ctx, sqlc.ActivateExactDraftBranchParams{
		ExactDraftBranchID: active.ID, PlanID: next.ID, ActivatedAt: tstz(next.CommittedAt),
	})
	if err != nil || changedGroups != 1 {
		return nil, false, exactDraftRowsError(err)
	}
	completedCategories, err := exactDraftCategoriesJSON(next.CompletedCategories)
	if err != nil {
		return nil, false, err
	}
	committedPlans, err := querier.CommitExactDraftAssignmentPlan(ctx, sqlc.CommitExactDraftAssignmentPlanParams{
		PlanID: next.ID, ExpectedPlanRevisionID: next.RevisionID,
		ExpectedDraftRevisionID:   nullableUUIDValue(next.SourceDraft.RevisionID),
		ActiveChildBranchID:       nullableUUIDValue(activeChildID),
		ActiveDraftBranchID:       nullableUUIDValue(active.ID),
		CompletionDraftRevisionID: nullableUUIDValue(next.CompletionDraftRevisionID),
		CompletionDraftRevision:   &next.CompletionDraftRevision,
		ActivationCommandID:       nullableUUIDValue(next.ActivationCommandID),
		CompletedCategories:       completedCategories,
		CommittedAt:               tstz(next.CommittedAt),
	})
	if err != nil || committedPlans != 1 {
		return nil, false, exactDraftRowsError(err)
	}
	committed, _, reloadErr := r.loadExactDraftBranchActivationTx(ctx, next.ID)
	if reloadErr != nil || committed == nil || !sameExactDraftActivation(*committed, next) {
		return nil, false, exactDraftRowsError(reloadErr)
	}
	return committed, true, nil
}

func activeExactDraftBranch(plan assignmentusecase.ExactDraftBranchPlan) (assignmentusecase.ExactDraftBranch, bool) {
	for _, branch := range plan.Branches {
		if branch.ID == plan.ActiveBranchID && branch.State == assignmentusecase.ExactDraftBranchStateActive {
			return branch, true
		}
	}
	return assignmentusecase.ExactDraftBranch{}, false
}

func exactDraftReleaseReason(plan assignmentusecase.ExactDraftBranchPlan) string {
	for _, branch := range plan.Branches {
		if branch.State == assignmentusecase.ExactDraftBranchStateReleased {
			return branch.ReleaseReason
		}
	}
	return ""
}

func sameExactDraftActivation(
	stored assignmentusecase.ExactDraftBranchPlan,
	next assignmentusecase.ExactDraftBranchPlan,
) bool {
	return stored.State == assignmentusecase.ExactDraftBranchPlanStateCommitted &&
		stored.ID == next.ID && stored.RevisionID == next.RevisionID &&
		stored.ProofHash == next.ProofHash && stored.ActiveBranchID == next.ActiveBranchID &&
		stored.CompletionDraftRevisionID == next.CompletionDraftRevisionID &&
		stored.CompletionDraftRevision == next.CompletionDraftRevision &&
		stored.ActivationCommandID == next.ActivationCommandID &&
		sameCategories(stored.CompletedCategories, next.CompletedCategories) &&
		stored.CommittedAt.Equal(next.CommittedAt) && exactDraftReleaseReason(stored) == exactDraftReleaseReason(next)
}

func exactDraftRowsError(err error) error {
	if err != nil {
		return err
	}
	return domain.ErrConflict
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func (r *ExactDraftBranchPlanPostgres) loadExactDraftBranchActivationTx(
	ctx context.Context,
	planID uuid.UUID,
) (*assignmentusecase.ExactDraftBranchPlan, *draftusecase.Execution, error) {
	querier := r.tx.Querier(ctx)
	planRow, err := querier.LockExactDraftAssignmentPlan(ctx, planID)
	if err != nil {
		return nil, nil, err
	}
	if !planRow.SourceDraftRevisionID.Valid {
		return nil, nil, domain.ErrConflict
	}
	source, err := r.lockExactDraftAssignmentSource(ctx, planID)
	if err != nil {
		return nil, nil, err
	}
	if source.DraftID == uuid.Nil || source.PlanRevisionID != planRow.RevisionID ||
		source.SourceDraftRevisionID != planRow.SourceDraftRevisionID {
		return nil, nil, domain.ErrConflict
	}
	ids, err := exactDraftIdentity(source.StageCommandID, source.FinalSeriesID, domain.SeriesFormat(source.SeriesFormat))
	if err != nil || ids.DraftAssignmentPlanID != planID || ids.DraftAssignmentRevisionID != planRow.RevisionID {
		return nil, nil, domain.ErrConflict
	}
	aggregate, err := r.drafts.Get(ctx, source.DraftID)
	if err != nil {
		return nil, nil, err
	}
	sourceDraftRevision, ok := exactDraftRevisionNumber(aggregate, planRow.SourceDraftRevisionID.UUID)
	if !ok {
		return nil, nil, domain.ErrConflict
	}
	sourceDraft, err := participantDraftExecution(aggregate, sourceDraftRevision)
	if err != nil || sourceDraft == nil || sourceDraft.State != draftusecase.ExecutionStateActive {
		return nil, nil, domain.ErrConflict
	}
	current, err := participantDraftExecution(aggregate, source.CurrentDraftRevision)
	if err != nil || current == nil || current.RevisionID != source.CurrentDraftRevisionID {
		return nil, nil, domain.ErrConflict
	}
	groups, err := querier.LockExactDraftAssignmentBranches(ctx, planID)
	if err != nil {
		return nil, nil, err
	}
	children, err := querier.LockExactDraftAssignmentChildren(ctx, planID)
	if err != nil {
		return nil, nil, err
	}
	sources, err := querier.LockExactDraftAssignmentChildSources(ctx, planID)
	if err != nil {
		return nil, nil, err
	}
	participants, err := querier.LockExactDraftAssignmentChildParticipants(ctx, planID)
	if err != nil {
		return nil, nil, err
	}
	history, err := querier.LockExactDraftAssignmentChildHistory(ctx, planID)
	if err != nil {
		return nil, nil, err
	}
	candidates, err := querier.LockExactDraftAssignmentChildCandidates(ctx, planID)
	if err != nil {
		return nil, nil, err
	}
	edges, err := querier.ListAssignmentPlanEdges(ctx, planID)
	if err != nil {
		return nil, nil, err
	}
	reservations, err := querier.ListAssignmentTaskVersionReservations(ctx, planID)
	if err != nil {
		return nil, nil, err
	}
	snapshots, err := querier.ListAssignmentTaskSnapshotsForPlan(ctx, planID)
	if err != nil {
		return nil, nil, err
	}
	plan, err := rehydrateExactDraftBranchPlan(
		planRow, *sourceDraft, ids, groups, children, sources, participants, history, candidates, edges, reservations, snapshots,
	)
	if err != nil {
		return nil, nil, err
	}
	return plan, current, nil
}

func exactDraftRevisionNumber(aggregate *DraftAggregate, revisionID uuid.UUID) (int64, bool) {
	if aggregate == nil || revisionID == uuid.Nil {
		return 0, false
	}
	for _, revision := range aggregate.Revisions {
		if revision.ID == revisionID {
			return revision.Revision, true
		}
	}
	return 0, false
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func rehydrateExactDraftBranchPlan(
	planRow sqlc.LockExactDraftAssignmentPlanRow,
	sourceDraft draftusecase.Execution,
	ids playoff.FinalStageIDs,
	groups []sqlc.LockExactDraftAssignmentBranchesRow,
	children []sqlc.LockExactDraftAssignmentChildrenRow,
	sources []sqlc.ExactDraftAssignmentChildSource,
	participants []sqlc.ExactDraftAssignmentChildParticipant,
	history []sqlc.ExactDraftAssignmentChildHistory,
	candidates []sqlc.ExactDraftAssignmentChildCandidate,
	edges []sqlc.AssignmentPlanEdge,
	reservations []sqlc.ListAssignmentTaskVersionReservationsRow,
	snapshots []sqlc.TaskSnapshot,
) (*assignmentusecase.ExactDraftBranchPlan, error) {
	if !ids.Valid() || planRow.ID != ids.DraftAssignmentPlanID || planRow.RevisionID != ids.DraftAssignmentRevisionID ||
		!planRow.SourceDraftRevisionID.Valid ||
		planRow.SourceDraftRevisionID.UUID != sourceDraft.RevisionID || planRow.ReachableBranchCount < 1 ||
		planRow.ProofHash == nil || *planRow.ProofHash == "" {
		return nil, domain.ErrConflict
	}
	paths, err := assignmentusecase.ReachableExactDraftBranches(sourceDraft)
	if err != nil || len(paths) != len(groups) || len(groups) != int(planRow.ReachableBranchCount) {
		return nil, domain.ErrConflict
	}
	groupsByKey := make(map[string]sqlc.LockExactDraftAssignmentBranchesRow, len(groups))
	for _, group := range groups {
		categories, categoryErr := exactDraftCategories(group.CategorySequence)
		if categoryErr != nil || group.ID == uuid.Nil || group.PlanID != planRow.ID ||
			group.DraftID != sourceDraft.ID || group.DraftRevisionID != sourceDraft.RevisionID ||
			len(categories) != exactDraftCategoryCount(sourceDraft.Format) || group.BranchKey == "" {
			return nil, domain.ErrConflict
		}
		if _, duplicate := groupsByKey[group.BranchKey]; duplicate {
			return nil, domain.ErrConflict
		}
		groupsByKey[group.BranchKey] = group
	}

	childrenByGroupPosition := make(map[exactDraftChildPosition]sqlc.LockExactDraftAssignmentChildrenRow, len(children))
	for _, child := range children {
		if !child.ExactDraftBranchID.Valid || child.ExactDraftPosition == nil {
			return nil, domain.ErrConflict
		}
		key := exactDraftChildPosition{groupID: child.ExactDraftBranchID.UUID, position: int(*child.ExactDraftPosition)}
		if child.ID == uuid.Nil || child.PlanID != planRow.ID || !child.DraftID.Valid || child.DraftID.UUID != sourceDraft.ID ||
			!child.DraftRevisionID.Valid || child.DraftRevisionID.UUID != sourceDraft.RevisionID || *child.ExactDraftPosition < 1 || int(*child.ExactDraftPosition) > exactDraftCategoryCount(sourceDraft.Format) ||
			!child.DecisionEvidenceID.Valid || child.DecisionAlgorithmVersion == nil ||
			!child.DecisionOwnerID.Valid || child.DecisionOwnerID.UUID != planRow.ID || !child.DecidedAt.Valid {
			return nil, domain.ErrConflict
		}
		if _, duplicate := childrenByGroupPosition[key]; duplicate {
			return nil, domain.ErrConflict
		}
		childrenByGroupPosition[key] = child
	}

	sourcesByChild := make(map[uuid.UUID]sqlc.ExactDraftAssignmentChildSource, len(sources))
	for _, source := range sources {
		if source.ChildBranchID == uuid.Nil || source.PlanID != planRow.ID || source.ProofHash == "" {
			return nil, domain.ErrConflict
		}
		if _, duplicate := sourcesByChild[source.ChildBranchID]; duplicate {
			return nil, domain.ErrConflict
		}
		sourcesByChild[source.ChildBranchID] = source
	}
	participantsByChild := make(map[uuid.UUID][]sqlc.ExactDraftAssignmentChildParticipant)
	for _, participant := range participants {
		if participant.PlanID != planRow.ID || participant.ChildBranchID == uuid.Nil {
			return nil, domain.ErrConflict
		}
		participantsByChild[participant.ChildBranchID] = append(participantsByChild[participant.ChildBranchID], participant)
	}
	historyByChild := make(map[uuid.UUID][]sqlc.ExactDraftAssignmentChildHistory)
	for _, item := range history {
		if item.PlanID != planRow.ID || item.ChildBranchID == uuid.Nil {
			return nil, domain.ErrConflict
		}
		historyByChild[item.ChildBranchID] = append(historyByChild[item.ChildBranchID], item)
	}
	candidatesByChild := make(map[uuid.UUID][]sqlc.ExactDraftAssignmentChildCandidate)
	for _, candidate := range candidates {
		if candidate.PlanID != planRow.ID || candidate.ChildBranchID == uuid.Nil {
			return nil, domain.ErrConflict
		}
		candidatesByChild[candidate.ChildBranchID] = append(candidatesByChild[candidate.ChildBranchID], candidate)
	}
	edgesByChild := make(map[uuid.UUID][]sqlc.AssignmentPlanEdge)
	for _, edge := range edges {
		if edge.PlanID != planRow.ID || edge.BranchID == uuid.Nil {
			return nil, domain.ErrConflict
		}
		edgesByChild[edge.BranchID] = append(edgesByChild[edge.BranchID], edge)
	}
	reservationsByEdge := make(map[uuid.UUID]sqlc.ListAssignmentTaskVersionReservationsRow, len(reservations))
	for _, reservation := range reservations {
		if reservation.PlanID != planRow.ID || reservation.ID == uuid.Nil || reservation.EdgeID == uuid.Nil {
			return nil, domain.ErrConflict
		}
		if _, duplicate := reservationsByEdge[reservation.EdgeID]; duplicate {
			return nil, domain.ErrConflict
		}
		reservationsByEdge[reservation.EdgeID] = reservation
	}
	snapshotsByReservation := make(map[uuid.UUID]TaskSnapshotRecord, len(snapshots))
	for _, snapshot := range snapshots {
		record, snapshotErr := taskSnapshotRecord(snapshot)
		if snapshotErr != nil || record.ReservationID == uuid.Nil {
			return nil, domain.ErrConflict
		}
		if _, duplicate := snapshotsByReservation[record.ReservationID]; duplicate {
			return nil, domain.ErrConflict
		}
		snapshotsByReservation[record.ReservationID] = record
	}

	plan := assignmentusecase.ExactDraftBranchPlan{
		ID: planRow.ID, RevisionID: planRow.RevisionID, SourceDraft: draftusecase.CloneExecution(sourceDraft),
		State:     assignmentusecase.ExactDraftBranchPlanState(planRow.State),
		Branches:  make([]assignmentusecase.ExactDraftBranch, len(paths)),
		CreatedAt: planRow.CreatedAt.Time.UTC(), ProofHash: *planRow.ProofHash,
	}
	if err := hydrateExactDraftPlanCompletion(&plan, planRow); err != nil {
		return nil, err
	}
	for index, path := range paths {
		group, ok := groupsByKey[path.Key]
		if !ok {
			return nil, domain.ErrConflict
		}
		categories, categoryErr := exactDraftCategories(group.CategorySequence)
		if categoryErr != nil || !sameCategories(categories, path.Categories) {
			return nil, domain.ErrConflict
		}
		branch, branchErr := rehydrateExactDraftBranch(
			plan, ids, path, group, childrenByGroupPosition, sourcesByChild, participantsByChild,
			historyByChild, candidatesByChild, edgesByChild, reservationsByEdge, snapshotsByReservation,
		)
		if branchErr != nil {
			return nil, branchErr
		}
		plan.Branches[index] = branch
	}
	if !exactDraftPersistedActivationMatches(planRow, plan) {
		return nil, domain.ErrConflict
	}
	if err := plan.Validate(); err != nil {
		return nil, domain.ErrConflict
	}
	cloned := cloneExactDraftPlan(plan)
	return &cloned, nil
}

func exactDraftPersistedActivationMatches(
	row sqlc.LockExactDraftAssignmentPlanRow,
	plan assignmentusecase.ExactDraftBranchPlan,
) bool {
	if plan.State == assignmentusecase.ExactDraftBranchPlanStatePlanned {
		return !row.ActiveBranchID.Valid && !row.ActiveDraftBranchID.Valid &&
			!row.CompletionDraftRevisionID.Valid && row.CompletionDraftRevision == nil &&
			!row.ActivationCommandID.Valid && !row.CommittedAt.Valid && len(row.CompletedCategories) == 0
	}
	if plan.State != assignmentusecase.ExactDraftBranchPlanStateCommitted || !row.ActiveBranchID.Valid {
		return false
	}
	for _, branch := range plan.Branches {
		if branch.ID == plan.ActiveBranchID {
			return row.ActiveBranchID.UUID == branch.ChildBranchIDs[0]
		}
	}
	return false
}

type exactDraftChildPosition struct {
	groupID  uuid.UUID
	position int
}

func hydrateExactDraftPlanCompletion(
	plan *assignmentusecase.ExactDraftBranchPlan,
	row sqlc.LockExactDraftAssignmentPlanRow,
) error {
	if plan == nil {
		return domain.ErrConflict
	}
	switch plan.State {
	case assignmentusecase.ExactDraftBranchPlanStatePlanned:
		return nil
	case assignmentusecase.ExactDraftBranchPlanStateCommitted:
		if !row.ActiveDraftBranchID.Valid || !row.CompletionDraftRevisionID.Valid ||
			row.CompletionDraftRevision == nil || !row.ActivationCommandID.Valid ||
			!row.CommittedAt.Valid {
			return domain.ErrConflict
		}
		categories, err := exactDraftCategories(row.CompletedCategories)
		if err != nil || len(categories) != exactDraftCategoryCount(plan.SourceDraft.Format) {
			return domain.ErrConflict
		}
		plan.ActiveBranchID = row.ActiveDraftBranchID.UUID
		plan.CompletionDraftRevisionID = row.CompletionDraftRevisionID.UUID
		plan.CompletionDraftRevision = *row.CompletionDraftRevision
		plan.ActivationCommandID = row.ActivationCommandID.UUID
		plan.CompletedCategories = categories
		plan.CommittedAt = row.CommittedAt.Time.UTC()
		return nil
	default:
		return domain.ErrConflict
	}
}

func rehydrateExactDraftBranch(
	plan assignmentusecase.ExactDraftBranchPlan,
	ids playoff.FinalStageIDs,
	path assignmentusecase.ExactDraftBranchPath,
	group sqlc.LockExactDraftAssignmentBranchesRow,
	children map[exactDraftChildPosition]sqlc.LockExactDraftAssignmentChildrenRow,
	sources map[uuid.UUID]sqlc.ExactDraftAssignmentChildSource,
	participants map[uuid.UUID][]sqlc.ExactDraftAssignmentChildParticipant,
	history map[uuid.UUID][]sqlc.ExactDraftAssignmentChildHistory,
	candidates map[uuid.UUID][]sqlc.ExactDraftAssignmentChildCandidate,
	edges map[uuid.UUID][]sqlc.AssignmentPlanEdge,
	reservations map[uuid.UUID]sqlc.ListAssignmentTaskVersionReservationsRow,
	snapshots map[uuid.UUID]TaskSnapshotRecord,
) (assignmentusecase.ExactDraftBranch, error) {
	if group.ID != ids.DraftAssignmentBranchID(path.Key) {
		return assignmentusecase.ExactDraftBranch{}, domain.ErrConflict
	}
	branch := assignmentusecase.ExactDraftBranch{
		ID:            group.ID,
		Path:          path,
		State:         assignmentusecase.ExactDraftBranchState(group.State),
		Assignments:   make([]assignmentusecase.ExactDraftBranchAssignment, len(path.Categories)),
		ActivatedAt:   timeValue(group.ActivatedAt),
		ReleasedAt:    timeValue(group.ReleasedAt),
		ReleaseReason: optionalString(group.ReleaseReason),
	}
	for index, category := range path.Categories {
		position := index + 1
		child, ok := children[exactDraftChildPosition{groupID: group.ID, position: position}]
		if !ok {
			return assignmentusecase.ExactDraftBranch{}, domain.ErrConflict
		}
		branch.ChildBranchIDs[index] = child.ID
		exact, state, transitionedAt, err := rehydrateExactDraftChild(
			plan, ids, branch, category, position, child, sources[child.ID], participants[child.ID], history[child.ID],
			candidates[child.ID], edges[child.ID], reservations, snapshots,
		)
		if err != nil {
			return assignmentusecase.ExactDraftBranch{}, err
		}
		branch.Assignments[index] = assignmentusecase.ExactDraftBranchAssignment{
			Position: position, Plan: exact, State: state, TransitionedAt: transitionedAt,
		}
	}
	return branch, nil
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func rehydrateExactDraftChild(
	plan assignmentusecase.ExactDraftBranchPlan,
	ids playoff.FinalStageIDs,
	branch assignmentusecase.ExactDraftBranch,
	category domain.Category,
	position int,
	child sqlc.LockExactDraftAssignmentChildrenRow,
	source sqlc.ExactDraftAssignmentChildSource,
	participantRows []sqlc.ExactDraftAssignmentChildParticipant,
	historyRows []sqlc.ExactDraftAssignmentChildHistory,
	candidateRows []sqlc.ExactDraftAssignmentChildCandidate,
	edgeRows []sqlc.AssignmentPlanEdge,
	reservations map[uuid.UUID]sqlc.ListAssignmentTaskVersionReservationsRow,
	snapshots map[uuid.UUID]TaskSnapshotRecord,
) (assignmentusecase.ExactNormalAssignmentPlan, assignmentusecase.ExactDraftReservationState, time.Time, error) {
	if child.ID != ids.DraftAssignmentChildBranchID(branch.Path.Key, position) ||
		!child.DecisionEvidenceID.Valid ||
		child.DecisionEvidenceID.UUID != ids.DraftAssignmentDecisionID(branch.Path.Key, position) ||
		source.ChildBranchID != child.ID || source.PlanID != plan.ID ||
		source.TournamentID == uuid.Nil || source.RosterID == uuid.Nil || source.SeriesID != plan.SourceDraft.SeriesID ||
		source.ProofHash == "" || !source.CreatedAt.Valid || !source.CreatedAt.Time.UTC().Equal(plan.CreatedAt) ||
		len(edgeRows) != domain.AssignmentReserveCount+1 {
		return assignmentusecase.ExactNormalAssignmentPlan{}, "", time.Time{}, domain.ErrConflict
	}
	childCategories, err := exactDraftCategories(child.CategorySequence)
	if err != nil || len(childCategories) != 1 || childCategories[0] != category ||
		child.BranchKey != exactDraftChildKey(branch.Path.Key, position) {
		return assignmentusecase.ExactNormalAssignmentPlan{}, "", time.Time{}, domain.ErrConflict
	}
	evidence, err := decisionEvidenceFromStorage(
		child.DecisionEvidenceID.UUID, domain.DecisionPurposeTask, *child.DecisionAlgorithmVersion,
		child.DecisionInputs, child.DecisionSeed, child.DecisionResult, child.DecisionReplayDigest,
		child.DecisionOwnerID.UUID, child.DecidedAt.Time,
	)
	if err != nil {
		return assignmentusecase.ExactNormalAssignmentPlan{}, "", time.Time{}, domain.ErrConflict
	}
	participantReservations, err := rehydrateExactDraftParticipants(participantRows, source)
	if err != nil {
		return assignmentusecase.ExactNormalAssignmentPlan{}, "", time.Time{}, err
	}
	historyItems, err := rehydrateExactDraftHistory(historyRows, participantReservations)
	if err != nil {
		return assignmentusecase.ExactNormalAssignmentPlan{}, "", time.Time{}, err
	}
	pool, _, err := rehydrateExactDraftCandidates(candidateRows, source)
	if err != nil {
		return assignmentusecase.ExactNormalAssignmentPlan{}, "", time.Time{}, err
	}
	candidateRefs, err := assignmentusecase.RestoreExactNormalDecisionCandidates(pool, evidence)
	if err != nil {
		return assignmentusecase.ExactNormalAssignmentPlan{}, "", time.Time{}, domain.ErrConflict
	}
	selectedEdges, state, transitionedAt, err := rehydrateExactDraftEdges(
		ids, branch.Path.Key, position, child, edgeRows, reservations, snapshots,
	)
	if err != nil {
		return assignmentusecase.ExactNormalAssignmentPlan{}, "", time.Time{}, err
	}
	graphDigest, graphErr := exactDraftDigest(source.GraphDigest)
	artifactDigest, artifactErr := exactDraftDigest(source.ArtifactDigest)
	if graphErr != nil || artifactErr != nil {
		return assignmentusecase.ExactNormalAssignmentPlan{}, "", time.Time{}, domain.ErrConflict
	}
	exact := assignmentusecase.ExactNormalAssignmentPlan{
		Scope: assignmentusecase.ExactNormalAssignmentScope{
			TournamentID: source.TournamentID, RosterID: source.RosterID, SeriesID: source.SeriesID,
			SlotID: source.SlotID, CategoryLockID: source.CategoryLockID,
		},
		PlanID: plan.ID, PlanRevisionID: plan.RevisionID, BranchID: child.ID, Category: category,
		Revisions: assignmentusecase.ExactNormalAssignmentSourceRevisions{
			SeriesRevision: source.SeriesRevision,
			PoolRevisionID: source.PoolRevisionID, PoolRevision: source.PoolRevision,
			HistoryRevisionID: source.HistoryRevisionID, HistoryRevision: source.HistoryRevision,
			RosterRevision:     source.RosterRevision,
			ArtifactRevisionID: source.ArtifactRevisionID, ArtifactRevision: source.ArtifactRevision,
			CategoryRevisionID: source.CategoryRevisionID, CategoryRevision: source.CategoryRevision,
		},
		Pool: pool, ParticipantIDs: exactDraftParticipantIDs(participantReservations),
		ParticipantReservations: participantReservations, History: historyItems,
		CandidateTaskVersions: candidateRefs, GraphDigest: graphDigest, ArtifactDigest: artifactDigest,
		DecisionEvidence: evidence, SelectedEdges: selectedEdges, CreatedAt: source.CreatedAt.Time.UTC(),
		ProofHash: source.ProofHash,
	}
	if err := exact.Validate(); err != nil {
		return assignmentusecase.ExactNormalAssignmentPlan{}, "", time.Time{}, domain.ErrConflict
	}
	return exact, state, transitionedAt, nil
}

func timeValue(value pgtype.Timestamptz) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return value.Time.UTC()
}

func optionalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func sameCategories(first, second []domain.Category) bool {
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

func rehydrateExactDraftParticipants(
	rows []sqlc.ExactDraftAssignmentChildParticipant,
	source sqlc.ExactDraftAssignmentChildSource,
) ([]assignmentusecase.ExactNormalParticipantReservation, error) {
	if len(rows) != 2 {
		return nil, domain.ErrConflict
	}
	result := make([]assignmentusecase.ExactNormalParticipantReservation, len(rows))
	seen := make(map[uuid.UUID]struct{}, len(rows))
	for index, row := range rows {
		if row.ChildBranchID != source.ChildBranchID || row.PlanID != source.PlanID ||
			row.TournamentID != source.TournamentID || row.ParticipantID == uuid.Nil || row.PlayerID == uuid.Nil ||
			row.ReservationID == uuid.Nil || row.ReservationRevision < 1 || !row.AcquiredAt.Valid || !row.UpdatedAt.Valid {
			return nil, domain.ErrConflict
		}
		if _, duplicate := seen[row.ParticipantID]; duplicate {
			return nil, domain.ErrConflict
		}
		seen[row.ParticipantID] = struct{}{}
		result[index] = assignmentusecase.ExactNormalParticipantReservation{
			ParticipantID: row.ParticipantID,
			PlayerID:      row.PlayerID,
			Reservation: domain.ParticipantReservation{
				PlayerID: row.PlayerID, ReservationID: row.ReservationID, TournamentID: row.TournamentID,
				Revision: row.ReservationRevision, AcquiredAt: row.AcquiredAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(),
			},
		}
	}
	sort.Slice(result, func(i, j int) bool { return bytes.Compare(result[i].ParticipantID[:], result[j].ParticipantID[:]) < 0 })
	return result, nil
}

func rehydrateExactDraftHistory(
	rows []sqlc.ExactDraftAssignmentChildHistory,
	participants []assignmentusecase.ExactNormalParticipantReservation,
) ([]capacity.TaskUse, error) {
	allowed := make(map[uuid.UUID]struct{}, len(participants))
	for _, participant := range participants {
		allowed[participant.ParticipantID] = struct{}{}
	}
	result := make([]capacity.TaskUse, len(rows))
	type historyKey struct {
		participantID uuid.UUID
		ref           domain.TaskVersionRef
	}
	seen := make(map[historyKey]struct{}, len(rows))
	for index, row := range rows {
		if row.ParticipantID == uuid.Nil || row.TaskID == uuid.Nil || row.TaskVersion < 0 {
			return nil, domain.ErrConflict
		}
		if _, ok := allowed[row.ParticipantID]; !ok {
			return nil, domain.ErrConflict
		}
		ref := domain.TaskVersionRef{TaskID: row.TaskID, Version: int(row.TaskVersion)}
		key := historyKey{participantID: row.ParticipantID, ref: ref}
		if _, duplicate := seen[key]; duplicate {
			return nil, domain.ErrConflict
		}
		seen[key] = struct{}{}
		result[index] = capacity.TaskUse{ParticipantID: row.ParticipantID, TaskID: row.TaskID, Version: int(row.TaskVersion)}
	}
	sort.Slice(result, func(i, j int) bool {
		if compare := bytes.Compare(result[i].ParticipantID[:], result[j].ParticipantID[:]); compare != 0 {
			return compare < 0
		}
		if compare := bytes.Compare(result[i].TaskID[:], result[j].TaskID[:]); compare != 0 {
			return compare < 0
		}
		return result[i].Version < result[j].Version
	})
	return result, nil
}

func rehydrateExactDraftCandidates(
	rows []sqlc.ExactDraftAssignmentChildCandidate,
	source sqlc.ExactDraftAssignmentChildSource,
) (domain.TaskPoolRevision, []domain.TaskVersionRef, error) {
	if len(rows) < domain.AssignmentReserveCount+1 {
		return domain.TaskPoolRevision{}, nil, domain.ErrConflict
	}
	pool := domain.TaskPoolRevision{
		ID: source.PoolRevisionID, Revision: source.PoolRevision, Kind: domain.AssignmentTaskKindNormal,
		Versions: make([]domain.TaskVersionRef, len(rows)),
	}
	seen := make(map[domain.TaskVersionRef]struct{}, len(rows))
	for index, row := range rows {
		ref := domain.TaskVersionRef{TaskID: row.TaskID, Version: int(row.TaskVersion)}
		if row.ChildBranchID != source.ChildBranchID || row.PlanID != source.PlanID ||
			row.PoolRevisionID != source.PoolRevisionID || ref.TaskID == uuid.Nil || ref.Version < 1 {
			return domain.TaskPoolRevision{}, nil, domain.ErrConflict
		}
		if _, duplicate := seen[ref]; duplicate {
			return domain.TaskPoolRevision{}, nil, domain.ErrConflict
		}
		seen[ref] = struct{}{}
		pool.Versions[index] = ref
	}
	normalized, err := domain.NormalizeTaskPoolRevision(pool, domain.AssignmentTaskKindNormal)
	if err != nil {
		return domain.TaskPoolRevision{}, nil, err
	}
	return normalized, append([]domain.TaskVersionRef(nil), normalized.Versions...), nil
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func rehydrateExactDraftEdges(
	ids playoff.FinalStageIDs,
	branchKey string,
	childPosition int,
	child sqlc.LockExactDraftAssignmentChildrenRow,
	edgeRows []sqlc.AssignmentPlanEdge,
	reservations map[uuid.UUID]sqlc.ListAssignmentTaskVersionReservationsRow,
	snapshots map[uuid.UUID]TaskSnapshotRecord,
) ([]assignmentusecase.ExactNormalAssignmentEdge, assignmentusecase.ExactDraftReservationState, time.Time, error) {
	sort.Slice(edgeRows, func(i, j int) bool { return edgeRows[i].Position < edgeRows[j].Position })
	state, err := exactDraftChildReservationState(child.State)
	if err != nil {
		return nil, "", time.Time{}, err
	}
	transitionedAt := time.Time{}
	switch state {
	case assignmentusecase.ExactDraftReservationStateReserved:
	case assignmentusecase.ExactDraftReservationStateCommitted:
		transitionedAt = timeValue(child.ActivatedAt)
	case assignmentusecase.ExactDraftReservationStateReleased:
		transitionedAt = timeValue(child.ReleasedAt)
	}
	if state != assignmentusecase.ExactDraftReservationStateReserved && transitionedAt.IsZero() {
		return nil, "", time.Time{}, domain.ErrConflict
	}
	result := make([]assignmentusecase.ExactNormalAssignmentEdge, len(edgeRows))
	for index, edge := range edgeRows {
		position := index + 1
		reservation, ok := reservations[edge.ID]
		if !ok || edge.ID != ids.DraftAssignmentEdgeID(branchKey, childPosition, position) ||
			reservation.ID != ids.DraftAssignmentReservationID(branchKey, childPosition, position) ||
			edge.BranchID != child.ID || edge.Position != int16(position) ||
			reservation.BranchID != child.ID || reservation.TaskID != edge.TaskID ||
			reservation.TaskVersion != edge.TaskVersion || !sameExactDraftReservationState(reservation.State, state) {
			return nil, "", time.Time{}, domain.ErrConflict
		}
		snapshot, ok := snapshots[reservation.ID]
		if !ok || snapshot.Snapshot.SnapshotID != ids.DraftAssignmentSnapshotID(branchKey, childPosition, position) ||
			snapshot.Snapshot.TaskID != edge.TaskID || snapshot.Snapshot.Version != int(edge.TaskVersion) {
			return nil, "", time.Time{}, domain.ErrConflict
		}
		if state == assignmentusecase.ExactDraftReservationStateCommitted &&
			(!reservation.CommittedAt.Valid || !reservation.CommittedAt.Time.UTC().Equal(transitionedAt)) {
			return nil, "", time.Time{}, domain.ErrConflict
		}
		if state == assignmentusecase.ExactDraftReservationStateReleased &&
			(!reservation.ReleasedAt.Valid || !reservation.ReleasedAt.Time.UTC().Equal(transitionedAt)) {
			return nil, "", time.Time{}, domain.ErrConflict
		}
		result[index] = assignmentusecase.ExactNormalAssignmentEdge{
			ID: edge.ID, ReservationID: reservation.ID, Position: position,
			Snapshot: snapshot.Snapshot, ContentDigest: snapshot.ContentDigest,
		}
	}
	return result, state, transitionedAt, nil
}

func exactDraftChildReservationState(state string) (assignmentusecase.ExactDraftReservationState, error) {
	switch state {
	case string(assignmentusecase.ExactDraftBranchStateReserved):
		return assignmentusecase.ExactDraftReservationStateReserved, nil
	case string(assignmentusecase.ExactDraftBranchStateActive):
		return assignmentusecase.ExactDraftReservationStateCommitted, nil
	case string(assignmentusecase.ExactDraftBranchStateReleased):
		return assignmentusecase.ExactDraftReservationStateReleased, nil
	default:
		return "", domain.ErrConflict
	}
}

func sameExactDraftReservationState(
	persisted string,
	expected assignmentusecase.ExactDraftReservationState,
) bool {
	switch expected {
	case assignmentusecase.ExactDraftReservationStateReserved:
		return persisted == "reserved"
	case assignmentusecase.ExactDraftReservationStateCommitted:
		return persisted == "committed"
	case assignmentusecase.ExactDraftReservationStateReleased:
		return persisted == "released"
	default:
		return false
	}
}
