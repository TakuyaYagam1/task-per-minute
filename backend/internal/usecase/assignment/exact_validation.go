package assignment

import (
	"bytes"
	"crypto/sha256"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/capacity"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/taskexec"
)

func (p ExactNormalAssignmentPlan) Validate() error {
	command := ExactNormalAssignmentCommand{
		Scope: p.Scope, PlanID: p.PlanID, PlanRevisionID: p.PlanRevisionID,
		BranchID: p.BranchID, DecisionEvidenceID: p.DecisionEvidence.ID, CreatedAt: p.CreatedAt,
	}
	if len(p.SelectedEdges) != len(command.EdgeIDs) {
		return exactNormalAssignmentError("exactly three selected edges are required")
	}
	for index, edge := range p.SelectedEdges {
		command.EdgeIDs[index] = edge.ID
		command.ReservationIDs[index] = edge.ReservationID
		command.SnapshotIDs[index] = edge.Snapshot.SnapshotID
	}
	if err := validateExactNormalAssignmentCommand(command); err != nil {
		return err
	}
	if err := validateExactNormalAssignmentPlanSources(p); err != nil {
		return err
	}
	if err := validateExactNormalAssignmentDecision(p); err != nil {
		return err
	}
	if err := validateExactNormalAssignmentEdges(p); err != nil {
		return err
	}
	want, err := exactNormalAssignmentProofHash(p)
	if err != nil || !capacity.ValidProofDigest(p.ProofHash) || p.ProofHash != want {
		return exactNormalAssignmentError("proof hash does not match the exact plan")
	}
	return nil
}

func normalizeExactNormalAssignmentAuthority(
	scope ExactNormalAssignmentScope,
	authority ExactNormalAssignmentAuthority,
) (ExactNormalAssignmentAuthority, []ExactNormalTaskVersion, error) {
	if err := validateExactNormalAuthorityIdentity(scope, authority); err != nil {
		return ExactNormalAssignmentAuthority{}, nil, err
	}
	pool, err := domain.NormalizeTaskPoolRevision(authority.Pool, domain.AssignmentTaskKindNormal)
	if err != nil || pool.ID != authority.Revisions.PoolRevisionID || pool.Revision != authority.Revisions.PoolRevision {
		return ExactNormalAssignmentAuthority{}, nil, exactNormalAssignmentError("invalid normal pool authority")
	}
	participants, err := normalizeExactNormalParticipants(authority.ParticipantIDs)
	if err != nil {
		return ExactNormalAssignmentAuthority{}, nil, err
	}
	reservations, err := normalizeExactNormalParticipantReservations(scope, participants, authority.ParticipantReservations)
	if err != nil {
		return ExactNormalAssignmentAuthority{}, nil, err
	}
	history, ok := capacity.NormalizeHistory(participants, authority.History)
	if !ok {
		return ExactNormalAssignmentAuthority{}, nil, exactNormalAssignmentError("invalid task receipt history")
	}
	candidates, err := normalizeExactNormalTaskVersions(pool, authority.Candidates)
	if err != nil {
		return ExactNormalAssignmentAuthority{}, nil, err
	}
	eligible := exactNormalEligibleTaskVersions(candidates, authority.Category, participants, history)
	if len(eligible) < domain.AssignmentReserveCount+1 {
		return ExactNormalAssignmentAuthority{}, nil, exactNormalCapacityError("fewer than three eligible task versions")
	}
	canonical := authority
	canonical.Pool = pool
	canonical.ParticipantIDs = participants
	canonical.ParticipantReservations = reservations
	canonical.History = canonicalExactNormalHistory(authority.History)
	canonical.Candidates = candidates
	return canonical, eligible, nil
}

func validateExactNormalAuthorityIdentity(
	scope ExactNormalAssignmentScope,
	authority ExactNormalAssignmentAuthority,
) error {
	if authority.Scope != scope || !validExactNormalAssignmentScope(scope) ||
		!authority.Category.IsValid() || !validExactNormalAssignmentSourceRevisions(authority.Revisions) ||
		authority.Revisions.CategoryRevisionID != scope.CategoryLockID ||
		authority.GraphDigest == [sha256.Size]byte{} || authority.ArtifactDigest == [sha256.Size]byte{} {
		return exactNormalAssignmentError("invalid authority identity")
	}
	return nil
}

func normalizeExactNormalParticipants(input []uuid.UUID) ([]uuid.UUID, error) {
	participants := append([]uuid.UUID(nil), input...)
	sort.Slice(participants, func(i, j int) bool {
		return bytes.Compare(participants[i][:], participants[j][:]) < 0
	})
	if len(participants) != 2 || participants[0] == uuid.Nil || participants[1] == uuid.Nil ||
		participants[0] == participants[1] {
		return nil, exactNormalAssignmentError("one two-participant slot is required")
	}
	return participants, nil
}

func normalizeExactNormalParticipantReservations(
	scope ExactNormalAssignmentScope,
	participants []uuid.UUID,
	input []ExactNormalParticipantReservation,
) ([]ExactNormalParticipantReservation, error) {
	result := append([]ExactNormalParticipantReservation(nil), input...)
	sort.Slice(result, func(i, j int) bool {
		return bytes.Compare(result[i].ParticipantID[:], result[j].ParticipantID[:]) < 0
	})
	if len(result) != len(participants) {
		return nil, exactNormalAssignmentError("participant reservations do not cover the slot")
	}
	for index, item := range result {
		if item.ParticipantID != participants[index] || item.PlayerID == uuid.Nil ||
			item.Reservation.PlayerID != item.PlayerID || !item.Reservation.IsValid() ||
			item.Reservation.TournamentID != scope.TournamentID {
			return nil, exactNormalAssignmentError("invalid participant reservation authority")
		}
	}
	return result, nil
}

func normalizeExactNormalTaskVersions(
	pool domain.TaskPoolRevision,
	input []ExactNormalTaskVersion,
) ([]ExactNormalTaskVersion, error) {
	result := append([]ExactNormalTaskVersion(nil), input...)
	sort.Slice(result, func(i, j int) bool {
		return domain.CompareTaskVersionRefs(
			domain.TaskVersionRef{TaskID: result[i].Task.ID, Version: result[i].Version},
			domain.TaskVersionRef{TaskID: result[j].Task.ID, Version: result[j].Version},
		) < 0
	})
	if len(result) != len(pool.Versions) {
		return nil, exactNormalAssignmentError("candidate content does not cover the pool")
	}
	for index, candidate := range result {
		if candidate.PoolRevisionID != pool.ID || candidate.Version != pool.Versions[index].Version ||
			candidate.Task.ID != pool.Versions[index].TaskID {
			return nil, exactNormalAssignmentError("candidate content does not match the pool")
		}
	}
	return result, nil
}

func exactNormalEligibleTaskVersions(
	candidates []ExactNormalTaskVersion,
	category domain.Category,
	participants []uuid.UUID,
	history map[uuid.UUID]map[domain.TaskVersionRef]struct{},
) []ExactNormalTaskVersion {
	result := make([]ExactNormalTaskVersion, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Task.Category != category {
			continue
		}
		used := false
		for _, participantID := range participants {
			ref := domain.TaskVersionRef{TaskID: candidate.Task.ID, Version: candidate.Version}
			if _, exists := history[participantID][ref]; exists {
				used = true
				break
			}
			if _, exists := history[participantID][domain.TaskVersionRef{TaskID: candidate.Task.ID}]; exists {
				used = true
				break
			}
		}
		if !used {
			result = append(result, candidate)
		}
	}
	return result
}

// ValidateExactNormalAssignmentCandidates rechecks the complete candidate
// authority without drawing a new decision seed or changing retained evidence.
func ValidateExactNormalAssignmentCandidates(plan ExactNormalAssignmentPlan, candidates []ExactNormalTaskVersion, unavailable map[domain.TaskVersionRef]struct{}) error {
	if err := plan.Validate(); err != nil {
		return err
	}
	_, eligible, err := normalizeExactNormalAssignmentAuthority(plan.Scope, ExactNormalAssignmentAuthority{
		Scope: plan.Scope, Category: plan.Category, Revisions: plan.Revisions, Pool: plan.Pool,
		ParticipantIDs: plan.ParticipantIDs, ParticipantReservations: plan.ParticipantReservations,
		History: plan.History, Candidates: candidates, GraphDigest: plan.GraphDigest, ArtifactDigest: plan.ArtifactDigest,
	})
	if err != nil {
		return err
	}
	eligible = exactNormalAvailableCandidates(eligible, unavailable)
	if !slices.Equal(plan.CandidateTaskVersions, exactNormalTaskVersionRefs(eligible)) {
		return exactNormalAssignmentError("retained candidate set differs from current eligible authority")
	}
	return nil
}

// RestoreExactNormalDecisionCandidates restores the retained eligible set from
// validated decision evidence. The pool remains the complete immutable source;
// this does not rerun eligibility against later task or participant state.
func RestoreExactNormalDecisionCandidates(pool domain.TaskPoolRevision, evidence domain.DecisionEvidence) ([]domain.TaskVersionRef, error) {
	normalized, err := domain.NormalizeTaskPoolRevision(pool, domain.AssignmentTaskKindNormal)
	if err != nil || evidence.Validate() != nil || evidence.Purpose != domain.DecisionPurposeTask {
		return nil, exactNormalAssignmentError("invalid retained candidate evidence")
	}
	inputs := make(map[string]struct{}, len(evidence.NormalizedInputs))
	for _, input := range evidence.NormalizedInputs {
		inputs[input] = struct{}{}
	}
	refs := make([]domain.TaskVersionRef, 0, len(inputs))
	for _, ref := range normalized.Versions {
		if _, ok := inputs[exactNormalTaskVersionEvidence(ref.TaskID, ref.Version)]; ok {
			refs = append(refs, ref)
		}
	}
	if len(refs) != len(inputs) || len(refs) < domain.AssignmentReserveCount+1 {
		return nil, exactNormalAssignmentError("retained candidates are not covered by the immutable pool")
	}
	return refs, nil
}

func exactNormalAvailableCandidates(candidates []ExactNormalTaskVersion, unavailable map[domain.TaskVersionRef]struct{}) []ExactNormalTaskVersion {
	if len(unavailable) == 0 {
		return candidates
	}
	available := make([]ExactNormalTaskVersion, 0, len(candidates))
	for _, candidate := range candidates {
		ref := domain.TaskVersionRef{TaskID: candidate.Task.ID, Version: candidate.Version}
		if _, reserved := unavailable[ref]; !reserved {
			available = append(available, candidate)
		}
	}
	return available
}

func validateExactNormalAssignmentCommand(command ExactNormalAssignmentCommand) error {
	if !validExactNormalAssignmentScope(command.Scope) || command.PlanID == uuid.Nil ||
		command.PlanRevisionID == uuid.Nil || command.BranchID == uuid.Nil ||
		command.DecisionEvidenceID == uuid.Nil || command.CreatedAt.IsZero() ||
		command.CreatedAt.Location() != time.UTC {
		return exactNormalAssignmentError("invalid command identity or timestamp")
	}
	seen := make(map[uuid.UUID]struct{}, 12)
	for _, id := range []uuid.UUID{command.PlanID, command.PlanRevisionID, command.BranchID, command.DecisionEvidenceID} {
		seen[id] = struct{}{}
	}
	for index := range command.EdgeIDs {
		for _, id := range []uuid.UUID{command.EdgeIDs[index], command.ReservationIDs[index], command.SnapshotIDs[index]} {
			if id == uuid.Nil {
				return exactNormalAssignmentError("missing edge identity")
			}
			if _, duplicate := seen[id]; duplicate {
				return exactNormalAssignmentError("reused plan identity")
			}
			seen[id] = struct{}{}
		}
	}
	return nil
}

func validExactNormalAssignmentScope(scope ExactNormalAssignmentScope) bool {
	return scope.TournamentID != uuid.Nil && scope.RosterID != uuid.Nil && scope.SeriesID != uuid.Nil &&
		scope.SlotID != uuid.Nil && scope.CategoryLockID != uuid.Nil
}

func validExactNormalAssignmentSourceRevisions(revisions ExactNormalAssignmentSourceRevisions) bool {
	return revisions.SeriesRevision >= 1 &&
		revisions.PoolRevisionID != uuid.Nil && revisions.PoolRevision >= 1 &&
		revisions.HistoryRevisionID != uuid.Nil && revisions.HistoryRevision >= 1 &&
		revisions.RosterRevision >= 1 &&
		revisions.ArtifactRevisionID != uuid.Nil && revisions.ArtifactRevision >= 1 &&
		revisions.CategoryRevisionID != uuid.Nil && revisions.CategoryRevision >= 1
}

func validateExactNormalAssignmentPlanSources(plan ExactNormalAssignmentPlan) error {
	if err := validateExactNormalPlanRevisionEvidence(plan); err != nil {
		return err
	}
	if err := validateExactNormalPlanPool(plan); err != nil {
		return err
	}
	if err := validateExactNormalPlanParticipants(plan); err != nil {
		return err
	}
	return validateExactNormalPlanCandidates(plan)
}

func validateExactNormalPlanRevisionEvidence(plan ExactNormalAssignmentPlan) error {
	if !plan.Category.IsValid() || !validExactNormalAssignmentSourceRevisions(plan.Revisions) ||
		plan.Revisions.CategoryRevisionID != plan.Scope.CategoryLockID ||
		plan.GraphDigest == [sha256.Size]byte{} || plan.ArtifactDigest == [sha256.Size]byte{} {
		return exactNormalAssignmentError("invalid source evidence")
	}
	return nil
}

func validateExactNormalPlanPool(plan ExactNormalAssignmentPlan) error {
	pool, err := domain.NormalizeTaskPoolRevision(plan.Pool, domain.AssignmentTaskKindNormal)
	if err != nil || !slices.Equal(pool.Versions, plan.Pool.Versions) ||
		pool.ID != plan.Revisions.PoolRevisionID || pool.Revision != plan.Revisions.PoolRevision {
		return exactNormalAssignmentError("invalid pool evidence")
	}
	return nil
}

func validateExactNormalPlanParticipants(plan ExactNormalAssignmentPlan) error {
	if len(plan.ParticipantIDs) != 2 || !slices.IsSortedFunc(plan.ParticipantIDs, func(first, second uuid.UUID) int {
		return bytes.Compare(first[:], second[:])
	}) {
		return exactNormalAssignmentError("participants are not canonical")
	}
	reservations, err := normalizeExactNormalParticipantReservations(
		plan.Scope,
		plan.ParticipantIDs,
		plan.ParticipantReservations,
	)
	if err != nil {
		return err
	}
	if !slices.Equal(reservations, plan.ParticipantReservations) {
		return exactNormalAssignmentError("participant reservations are not canonical")
	}
	if _, ok := capacity.NormalizeHistory(plan.ParticipantIDs, plan.History); !ok ||
		!slices.Equal(canonicalExactNormalHistory(plan.History), plan.History) {
		return exactNormalAssignmentError("invalid history evidence")
	}
	return nil
}

func validateExactNormalPlanCandidates(plan ExactNormalAssignmentPlan) error {
	if len(plan.CandidateTaskVersions) < domain.AssignmentReserveCount+1 ||
		!slices.IsSortedFunc(plan.CandidateTaskVersions, domain.CompareTaskVersionRefs) {
		return exactNormalAssignmentError("candidate task versions are not canonical")
	}
	return nil
}

func validateExactNormalAssignmentDecision(plan ExactNormalAssignmentPlan) error {
	if err := plan.DecisionEvidence.Validate(); err != nil ||
		plan.DecisionEvidence.Purpose != domain.DecisionPurposeTask ||
		plan.DecisionEvidence.OwnerID != plan.PlanID ||
		!plan.DecisionEvidence.DecidedAt.Equal(plan.CreatedAt) {
		return exactNormalAssignmentError("invalid task decision evidence")
	}
	wantInputs := make([]string, len(plan.CandidateTaskVersions))
	for index, candidate := range plan.CandidateTaskVersions {
		wantInputs[index] = exactNormalTaskVersionEvidence(candidate.TaskID, candidate.Version)
	}
	if !slices.Equal(plan.DecisionEvidence.NormalizedInputs, wantInputs) {
		return exactNormalAssignmentError("decision inputs do not match eligible candidates")
	}
	return nil
}

func validateExactNormalAssignmentEdges(plan ExactNormalAssignmentPlan) error {
	selectedTasks := make(map[uuid.UUID]struct{}, len(plan.SelectedEdges))
	for index, edge := range plan.SelectedEdges {
		if edge.Position != index+1 || edge.Snapshot.Kind != domain.AssignmentTaskKindNormal ||
			edge.Snapshot.Category != plan.Category || edge.Snapshot.Validate() != nil {
			return exactNormalAssignmentError("invalid selected edge")
		}
		digest, err := taskexec.SnapshotDigest(edge.Snapshot)
		if err != nil || digest != edge.ContentDigest {
			return exactNormalAssignmentError("selected snapshot digest does not match")
		}
		if _, duplicate := selectedTasks[edge.Snapshot.TaskID]; duplicate {
			return exactNormalAssignmentError("selected task is duplicated")
		}
		selectedTasks[edge.Snapshot.TaskID] = struct{}{}
		want := exactNormalTaskVersionEvidence(edge.Snapshot.TaskID, edge.Snapshot.Version)
		if plan.DecisionEvidence.Result[index] != want {
			return exactNormalAssignmentError("selected edge does not follow decision order")
		}
	}
	return nil
}
