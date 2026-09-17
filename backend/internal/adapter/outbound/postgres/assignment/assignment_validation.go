package assignment

import (
	"crypto/sha256"
	"encoding/json"
	"math"
	"strings"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func validateConservativePlanInput(in ConservativePlanInput) ([]byte, []byte, error) {
	if in.ID == uuid.Nil || in.TournamentID == uuid.Nil || in.RosterID == uuid.Nil || in.RevisionID == uuid.Nil ||
		in.SourceRosterRevision < 1 || in.SourcePoolRevisionID == uuid.Nil || !validServerTime(in.CreatedAt) {
		return nil, nil, domain.ErrValidation
	}
	constraintGraph, err := requiredJSONObject(in.ConstraintGraph)
	if err != nil {
		return nil, nil, err
	}
	proofEvidence, err := requiredJSONObject(in.ProofEvidence)
	if err != nil {
		return nil, nil, err
	}
	return constraintGraph, proofEvidence, nil
}

func validateExactPlanInput(in ExactPlanInput) ([]byte, []byte, error) {
	if !validExactPlanMetadata(in) || !validExactPlanDecision(in) {
		return nil, nil, domain.ErrValidation
	}
	constraintGraph, err := requiredJSONObject(in.ConstraintGraph)
	if err != nil {
		return nil, nil, err
	}
	proofEvidence, err := requiredJSONObject(in.ProofEvidence)
	if err != nil {
		return nil, nil, err
	}
	tracker := newExactPlanIdentityTracker(len(in.Branches))
	for _, branch := range in.Branches {
		if err := tracker.validateBranch(in.SourceDraftRevision, branch); err != nil {
			return nil, nil, err
		}
	}
	return constraintGraph, proofEvidence, nil
}

type exactPlanIdentityTracker struct {
	branchIDs  map[uuid.UUID]struct{}
	branchKeys map[string]struct{}
	taskIDs    map[uuid.UUID]struct{}
	identities map[uuid.UUID]struct{}
}

func newExactPlanIdentityTracker(branchCount int) *exactPlanIdentityTracker {
	return &exactPlanIdentityTracker{
		branchIDs:  make(map[uuid.UUID]struct{}, branchCount),
		branchKeys: make(map[string]struct{}, branchCount),
		taskIDs:    make(map[uuid.UUID]struct{}, branchCount*3),
		identities: make(map[uuid.UUID]struct{}, branchCount*9),
	}
}

func validExactPlanMetadata(in ExactPlanInput) bool {
	return in.ID != uuid.Nil && in.TournamentID != uuid.Nil && in.RosterID != uuid.Nil &&
		in.ParentPlanID != uuid.Nil && in.RevisionID != uuid.Nil && in.SourceRosterRevision >= 1 &&
		in.SourcePoolRevisionID != uuid.Nil && in.SourceDraftRevision != uuid.Nil && len(in.Branches) > 0 &&
		len(in.Branches) <= math.MaxInt32 && validServerTime(in.CreatedAt)
}

func validExactPlanDecision(in ExactPlanInput) bool {
	if err := in.DecisionEvidence.Validate(); err != nil {
		return false
	}
	return in.DecisionEvidence.Purpose == domain.DecisionPurposeTask &&
		in.DecisionEvidence.OwnerID == in.ID && !in.DecisionEvidence.DecidedAt.After(in.CreatedAt)
}

func (tracker *exactPlanIdentityTracker) validateBranch(
	sourceDraftRevision uuid.UUID,
	branch AssignmentBranchInput,
) error {
	key := strings.TrimSpace(branch.Key)
	if branch.ID == uuid.Nil || branch.DraftID == uuid.Nil || branch.DraftRevisionID != sourceDraftRevision ||
		key == "" || key != branch.Key || len(branch.Categories) == 0 || len(branch.Categories) > 3 ||
		len(branch.Edges) != domain.AssignmentReserveCount+1 {
		return domain.ErrValidation
	}
	if tracker.hasBranchIdentity(branch.ID, branch.Key) || !validAssignmentCategories(branch.Categories) {
		return domain.ErrValidation
	}
	tracker.branchIDs[branch.ID] = struct{}{}
	tracker.branchKeys[branch.Key] = struct{}{}
	positions := make(map[int]struct{}, domain.AssignmentReserveCount+1)
	for _, edge := range branch.Edges {
		if err := tracker.validateEdge(edge, positions); err != nil {
			return err
		}
	}
	return nil
}

func (tracker *exactPlanIdentityTracker) hasBranchIdentity(id uuid.UUID, key string) bool {
	_, duplicateID := tracker.branchIDs[id]
	_, duplicateKey := tracker.branchKeys[key]
	return duplicateID || duplicateKey
}

func validAssignmentCategories(categories []domain.Category) bool {
	for _, category := range categories {
		if !category.IsValid() {
			return false
		}
	}
	return true
}

func (tracker *exactPlanIdentityTracker) validateEdge(
	edge AssignmentEdgeInput,
	positions map[int]struct{},
) error {
	if !validExactPlanEdgeMetadata(edge) {
		return domain.ErrValidation
	}
	if err := edge.Snapshot.Validate(); err != nil {
		return domain.WrapError(err, domain.ErrValidation)
	}
	if _, err := requiredJSONObject(edge.SelectionEvidence); err != nil {
		return err
	}
	if _, duplicate := positions[edge.Position]; duplicate {
		return domain.ErrValidation
	}
	positions[edge.Position] = struct{}{}
	if tracker.hasEdgeIdentity(edge) {
		return domain.ErrValidation
	}
	tracker.taskIDs[edge.Snapshot.TaskID] = struct{}{}
	for _, id := range []uuid.UUID{edge.ID, edge.ReservationID, edge.Snapshot.SnapshotID} {
		tracker.identities[id] = struct{}{}
	}
	return nil
}

func validExactPlanEdgeMetadata(edge AssignmentEdgeInput) bool {
	return edge.ID != uuid.Nil && edge.ReservationID != uuid.Nil && edge.Position >= 1 && edge.Position <= 3 &&
		edge.Snapshot.SnapshotID != uuid.Nil && edge.Snapshot.Version <= math.MaxInt32 &&
		edge.Snapshot.TimeLimit <= math.MaxInt32 && edge.ContentDigest != [sha256.Size]byte{}
}

func (tracker *exactPlanIdentityTracker) hasEdgeIdentity(edge AssignmentEdgeInput) bool {
	if _, duplicate := tracker.taskIDs[edge.Snapshot.TaskID]; duplicate {
		return true
	}
	for _, id := range []uuid.UUID{edge.ID, edge.ReservationID, edge.Snapshot.SnapshotID} {
		if _, duplicate := tracker.identities[id]; duplicate {
			return true
		}
	}
	return false
}

func validateAssignmentCreateInput(in AssignmentCreateInput) error {
	if in.ID == uuid.Nil || in.AttemptID == uuid.Nil || in.SeriesID == uuid.Nil || in.RosterID == uuid.Nil ||
		in.PlanID == uuid.Nil || in.BranchID == uuid.Nil || in.ReservationID == uuid.Nil || in.SnapshotID == uuid.Nil ||
		!validServerTime(in.CreatedAt) {
		return domain.ErrValidation
	}
	return nil
}

func requiredJSONObject(value map[string]any) ([]byte, error) {
	if len(value) == 0 {
		return nil, domain.ErrValidation
	}
	data, err := json.Marshal(value)
	if err != nil || string(data) == "{}" {
		return nil, domain.ErrValidation
	}
	return data, nil
}

func RequiredJSONObject(value map[string]any) ([]byte, error) {
	return requiredJSONObject(value)
}
