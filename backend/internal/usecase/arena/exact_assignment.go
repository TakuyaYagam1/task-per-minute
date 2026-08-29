package arena

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/taskexec"
)

const exactNormalAssignmentAttempts = 2

var (
	ErrInvalidExactNormalAssignment  = errors.New("invalid exact normal assignment")
	ErrExactNormalAssignmentConflict = errors.New("exact normal assignment conflict")
)

type ExactNormalAssignmentScope struct {
	TournamentID   uuid.UUID
	RosterID       uuid.UUID
	SeriesID       uuid.UUID
	SlotID         uuid.UUID
	CategoryLockID uuid.UUID
}

type ExactNormalAssignmentSourceRevisions struct {
	SlotRevisionID        uuid.UUID
	SlotRevision          int64
	GraphRevisionID       uuid.UUID
	GraphRevision         int64
	PoolRevisionID        uuid.UUID
	PoolRevision          int64
	HistoryRevisionID     uuid.UUID
	HistoryRevision       int64
	ReservationRevisionID uuid.UUID
	ReservationRevision   int64
	ArtifactRevisionID    uuid.UUID
	ArtifactRevision      int64
	CategoryRevisionID    uuid.UUID
	CategoryRevision      int64
}

type ExactNormalParticipantReservation struct {
	ParticipantID uuid.UUID
	PlayerID      uuid.UUID
	Reservation   domain.ParticipantReservation
}

type ExactNormalTaskVersion struct {
	PoolRevisionID uuid.UUID
	Version        int
	Task           domain.Task
}

type ExactNormalAssignmentAuthority struct {
	Scope                   ExactNormalAssignmentScope
	Category                domain.Category
	Revisions               ExactNormalAssignmentSourceRevisions
	Pool                    TaskPoolRevision
	ParticipantIDs          []uuid.UUID
	ParticipantReservations []ExactNormalParticipantReservation
	History                 []CapacityTaskUse
	Candidates              []ExactNormalTaskVersion
	GraphDigest             [sha256.Size]byte
	ArtifactDigest          [sha256.Size]byte
}

type ExactNormalAssignmentCommand struct {
	Scope              ExactNormalAssignmentScope
	PlanID             uuid.UUID
	PlanRevisionID     uuid.UUID
	BranchID           uuid.UUID
	DecisionEvidenceID uuid.UUID
	EdgeIDs            [domain.ArenaAssignmentReserveCount + 1]uuid.UUID
	ReservationIDs     [domain.ArenaAssignmentReserveCount + 1]uuid.UUID
	SnapshotIDs        [domain.ArenaAssignmentReserveCount + 1]uuid.UUID
	CreatedAt          time.Time
}

type ExactNormalAssignmentEdge struct {
	ID            uuid.UUID
	ReservationID uuid.UUID
	Position      int
	Snapshot      domain.ArenaTaskSnapshot
	ContentDigest [sha256.Size]byte
}

type ExactNormalAssignmentPlan struct {
	Scope                   ExactNormalAssignmentScope
	PlanID                  uuid.UUID
	PlanRevisionID          uuid.UUID
	BranchID                uuid.UUID
	Category                domain.Category
	Revisions               ExactNormalAssignmentSourceRevisions
	Pool                    TaskPoolRevision
	ParticipantIDs          []uuid.UUID
	ParticipantReservations []ExactNormalParticipantReservation
	History                 []CapacityTaskUse
	CandidateTaskVersions   []TaskVersionRef
	GraphDigest             [sha256.Size]byte
	ArtifactDigest          [sha256.Size]byte
	DecisionEvidence        domain.ArenaDecisionEvidence
	SelectedEdges           []ExactNormalAssignmentEdge
	CreatedAt               time.Time
	ProofHash               string
}

// ExactNormalAssignmentRepository owns one transaction that compares every
// source revision and digest before writing the plan, branch, selected edges,
// snapshots, and all three task reservations.
type ExactNormalAssignmentRepository interface {
	LoadExactNormalAssignmentAuthority(
		ctx context.Context,
		scope ExactNormalAssignmentScope,
	) (ExactNormalAssignmentAuthority, error)
	CommitExactNormalAssignment(
		ctx context.Context,
		plan ExactNormalAssignmentPlan,
	) (*ExactNormalAssignmentPlan, bool, error)
}

type ExactNormalAssignmentUseCase struct {
	repository ExactNormalAssignmentRepository
}

func NewExactNormalAssignmentUseCase(
	repository ExactNormalAssignmentRepository,
) *ExactNormalAssignmentUseCase {
	return &ExactNormalAssignmentUseCase{repository: repository}
}

func (u *ExactNormalAssignmentUseCase) PlanAndCommit(
	ctx context.Context,
	command ExactNormalAssignmentCommand,
) (*ExactNormalAssignmentPlan, bool, error) {
	if u == nil || u.repository == nil {
		return nil, false, domain.ErrValidation
	}
	if err := validateExactNormalAssignmentCommand(command); err != nil {
		return nil, false, err
	}

	for range exactNormalAssignmentAttempts {
		authority, err := u.repository.LoadExactNormalAssignmentAuthority(ctx, command.Scope)
		if err != nil {
			return nil, false, fmt.Errorf("ExactNormalAssignmentUseCase - load authority: %w", err)
		}
		plan, err := BuildExactNormalAssignment(command, authority)
		if err != nil {
			return nil, false, err
		}
		committed, changed, err := u.repository.CommitExactNormalAssignment(ctx, plan)
		if errors.Is(err, domain.ErrConflict) {
			continue
		}
		if err != nil {
			return nil, false, fmt.Errorf("ExactNormalAssignmentUseCase - commit exact plan: %w", err)
		}
		if committed == nil || committed.Validate() != nil || committed.ProofHash != plan.ProofHash {
			return nil, false, domain.ErrInternal
		}
		result := cloneExactNormalAssignmentPlan(*committed)
		return &result, changed, nil
	}
	return nil, false, ErrExactNormalAssignmentConflict
}

func BuildExactNormalAssignment(
	command ExactNormalAssignmentCommand,
	authority ExactNormalAssignmentAuthority,
) (ExactNormalAssignmentPlan, error) {
	if err := validateExactNormalAssignmentCommand(command); err != nil {
		return ExactNormalAssignmentPlan{}, err
	}
	canonical, eligible, err := normalizeExactNormalAssignmentAuthority(command.Scope, authority)
	if err != nil {
		return ExactNormalAssignmentPlan{}, err
	}

	inputs := make([]string, len(eligible))
	byEvidence := make(map[string]ExactNormalTaskVersion, len(eligible))
	for index, candidate := range eligible {
		value := exactNormalTaskVersionEvidence(candidate.Task.ID, candidate.Version)
		inputs[index] = value
		byEvidence[value] = candidate
	}
	evidence, err := domain.NewArenaDecisionEvidence(
		command.DecisionEvidenceID,
		domain.ArenaDecisionPurposeTask,
		domain.ArenaDecisionAlgorithmV1,
		inputs,
		command.PlanID,
		command.CreatedAt,
	)
	if err != nil {
		return ExactNormalAssignmentPlan{}, exactNormalAssignmentError("task selection: %v", err)
	}

	plan := ExactNormalAssignmentPlan{
		Scope: command.Scope, PlanID: command.PlanID, PlanRevisionID: command.PlanRevisionID,
		BranchID: command.BranchID, Category: canonical.Category, Revisions: canonical.Revisions,
		Pool: cloneTaskPool(canonical.Pool), ParticipantIDs: append([]uuid.UUID(nil), canonical.ParticipantIDs...),
		ParticipantReservations: cloneExactNormalParticipantReservations(canonical.ParticipantReservations),
		History:                 append([]CapacityTaskUse(nil), canonical.History...),
		CandidateTaskVersions:   exactNormalTaskVersionRefs(eligible),
		GraphDigest:             canonical.GraphDigest, ArtifactDigest: canonical.ArtifactDigest,
		DecisionEvidence: evidence, CreatedAt: command.CreatedAt,
		SelectedEdges: make([]ExactNormalAssignmentEdge, domain.ArenaAssignmentReserveCount+1),
	}
	for index := range plan.SelectedEdges {
		candidate := byEvidence[evidence.Result[index]]
		snapshot, buildErr := taskexec.BuildSnapshot(taskexec.SnapshotInput{
			SnapshotID: command.SnapshotIDs[index], Version: candidate.Version,
			Kind: domain.ArenaTaskKindNormal, Task: candidate.Task,
		})
		if buildErr != nil {
			return ExactNormalAssignmentPlan{}, exactNormalAssignmentError("snapshot %d: %v", index+1, buildErr)
		}
		digest, digestErr := taskexec.SnapshotDigest(snapshot)
		if digestErr != nil {
			return ExactNormalAssignmentPlan{}, exactNormalAssignmentError("snapshot digest %d: %v", index+1, digestErr)
		}
		plan.SelectedEdges[index] = ExactNormalAssignmentEdge{
			ID: command.EdgeIDs[index], ReservationID: command.ReservationIDs[index],
			Position: index + 1, Snapshot: snapshot, ContentDigest: digest,
		}
	}
	plan.ProofHash, err = exactNormalAssignmentProofHash(plan)
	if err != nil {
		return ExactNormalAssignmentPlan{}, err
	}
	if err := plan.Validate(); err != nil {
		return ExactNormalAssignmentPlan{}, err
	}
	return cloneExactNormalAssignmentPlan(plan), nil
}

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
	if err != nil || !validCapacityDigest(p.ProofHash) || p.ProofHash != want {
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
	pool, err := normalizeTaskPoolRevision(authority.Pool, domain.ArenaTaskKindNormal)
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
	history, ok := normalizedCapacityHistory(participants, authority.History)
	if !ok {
		return ExactNormalAssignmentAuthority{}, nil, exactNormalAssignmentError("invalid Arena task history")
	}
	candidates, err := normalizeExactNormalTaskVersions(pool, authority.Candidates)
	if err != nil {
		return ExactNormalAssignmentAuthority{}, nil, err
	}
	eligible := exactNormalEligibleTaskVersions(candidates, authority.Category, participants, history)
	if len(eligible) < domain.ArenaAssignmentReserveCount+1 {
		return ExactNormalAssignmentAuthority{}, nil, exactNormalAssignmentError("fewer than three eligible task versions")
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
			item.Reservation.OwnerKind != domain.ParticipantReservationOwnerArena ||
			item.Reservation.OwnerID != scope.TournamentID {
			return nil, exactNormalAssignmentError("invalid participant reservation authority")
		}
	}
	return result, nil
}

func normalizeExactNormalTaskVersions(
	pool TaskPoolRevision,
	input []ExactNormalTaskVersion,
) ([]ExactNormalTaskVersion, error) {
	result := append([]ExactNormalTaskVersion(nil), input...)
	sort.Slice(result, func(i, j int) bool {
		return taskVersionRefLess(
			TaskVersionRef{TaskID: result[i].Task.ID, Version: result[i].Version},
			TaskVersionRef{TaskID: result[j].Task.ID, Version: result[j].Version},
		)
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
	history map[uuid.UUID]map[uuid.UUID]struct{},
) []ExactNormalTaskVersion {
	result := make([]ExactNormalTaskVersion, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Task.Category != category {
			continue
		}
		used := false
		for _, participantID := range participants {
			if _, exists := history[participantID][candidate.Task.ID]; exists {
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
	return revisions.SlotRevisionID != uuid.Nil && revisions.SlotRevision >= 1 &&
		revisions.GraphRevisionID != uuid.Nil && revisions.GraphRevision >= 1 &&
		revisions.PoolRevisionID != uuid.Nil && revisions.PoolRevision >= 1 &&
		revisions.HistoryRevisionID != uuid.Nil && revisions.HistoryRevision >= 1 &&
		revisions.ReservationRevisionID != uuid.Nil && revisions.ReservationRevision >= 1 &&
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
	pool, err := normalizeTaskPoolRevision(plan.Pool, domain.ArenaTaskKindNormal)
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
	if _, ok := normalizedCapacityHistory(plan.ParticipantIDs, plan.History); !ok ||
		!slices.Equal(canonicalExactNormalHistory(plan.History), plan.History) {
		return exactNormalAssignmentError("invalid history evidence")
	}
	return nil
}

func validateExactNormalPlanCandidates(plan ExactNormalAssignmentPlan) error {
	if len(plan.CandidateTaskVersions) < domain.ArenaAssignmentReserveCount+1 ||
		!slices.IsSortedFunc(plan.CandidateTaskVersions, func(first, second TaskVersionRef) int {
			if taskVersionRefLess(first, second) {
				return -1
			}
			if taskVersionRefLess(second, first) {
				return 1
			}
			return 0
		}) {
		return exactNormalAssignmentError("candidate task versions are not canonical")
	}
	return nil
}

func validateExactNormalAssignmentDecision(plan ExactNormalAssignmentPlan) error {
	if err := plan.DecisionEvidence.Validate(); err != nil ||
		plan.DecisionEvidence.Purpose != domain.ArenaDecisionPurposeTask ||
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
		if edge.Position != index+1 || edge.Snapshot.Kind != domain.ArenaTaskKindNormal ||
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

type exactNormalAssignmentProofDocument struct {
	PlanID                  string                                           `json:"plan_id"`
	PlanRevisionID          string                                           `json:"plan_revision_id"`
	BranchID                string                                           `json:"branch_id"`
	Scope                   exactNormalAssignmentScopeProofDocument          `json:"scope"`
	Category                domain.Category                                  `json:"category"`
	Revisions               exactNormalAssignmentRevisionsProofDocument      `json:"revisions"`
	ParticipantIDs          []string                                         `json:"participant_ids"`
	ParticipantReservations []exactNormalParticipantReservationProofDocument `json:"participant_reservations"`
	History                 []exactNormalHistoryProofDocument                `json:"history"`
	CandidateTaskVersions   []exactNormalTaskVersionProofDocument            `json:"candidate_task_versions"`
	GraphDigest             string                                           `json:"graph_digest"`
	ArtifactDigest          string                                           `json:"artifact_digest"`
	DecisionEvidenceID      string                                           `json:"decision_evidence_id"`
	DecisionReplayDigest    string                                           `json:"decision_replay_digest"`
	SelectedEdges           []exactNormalAssignmentEdgeProofDocument         `json:"selected_edges"`
	CreatedAt               time.Time                                        `json:"created_at"`
}

type exactNormalAssignmentScopeProofDocument struct {
	TournamentID   string `json:"tournament_id"`
	RosterID       string `json:"roster_id"`
	SeriesID       string `json:"series_id"`
	SlotID         string `json:"slot_id"`
	CategoryLockID string `json:"category_lock_id"`
}

type exactNormalAssignmentRevisionsProofDocument struct {
	SlotRevisionID        string `json:"slot_revision_id"`
	SlotRevision          int64  `json:"slot_revision"`
	GraphRevisionID       string `json:"graph_revision_id"`
	GraphRevision         int64  `json:"graph_revision"`
	PoolRevisionID        string `json:"pool_revision_id"`
	PoolRevision          int64  `json:"pool_revision"`
	HistoryRevisionID     string `json:"history_revision_id"`
	HistoryRevision       int64  `json:"history_revision"`
	ReservationRevisionID string `json:"reservation_revision_id"`
	ReservationRevision   int64  `json:"reservation_revision"`
	ArtifactRevisionID    string `json:"artifact_revision_id"`
	ArtifactRevision      int64  `json:"artifact_revision"`
	CategoryRevisionID    string `json:"category_revision_id"`
	CategoryRevision      int64  `json:"category_revision"`
}

type exactNormalParticipantReservationProofDocument struct {
	ParticipantID string    `json:"participant_id"`
	PlayerID      string    `json:"player_id"`
	ReservationID string    `json:"reservation_id"`
	OwnerKind     string    `json:"owner_kind"`
	OwnerID       string    `json:"owner_id"`
	Revision      int64     `json:"revision"`
	AcquiredAt    time.Time `json:"acquired_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type exactNormalHistoryProofDocument struct {
	ParticipantID string `json:"participant_id"`
	TaskID        string `json:"task_id"`
}

type exactNormalTaskVersionProofDocument struct {
	TaskID  string `json:"task_id"`
	Version int    `json:"version"`
}

type exactNormalAssignmentEdgeProofDocument struct {
	ID            string `json:"id"`
	ReservationID string `json:"reservation_id"`
	Position      int    `json:"position"`
	SnapshotID    string `json:"snapshot_id"`
	TaskID        string `json:"task_id"`
	TaskVersion   int    `json:"task_version"`
	ContentDigest string `json:"content_digest"`
}

func exactNormalAssignmentProofHash(plan ExactNormalAssignmentPlan) (string, error) {
	document := exactNormalAssignmentProofDocument{
		PlanID: plan.PlanID.String(), PlanRevisionID: plan.PlanRevisionID.String(), BranchID: plan.BranchID.String(),
		Scope: exactNormalScopeProofDocument(plan.Scope), Category: plan.Category,
		Revisions:               exactNormalRevisionsProofDocument(plan.Revisions),
		ParticipantIDs:          exactNormalParticipantIDDocuments(plan.ParticipantIDs),
		ParticipantReservations: exactNormalParticipantReservationDocuments(plan.ParticipantReservations),
		History:                 exactNormalHistoryDocuments(plan.History),
		CandidateTaskVersions:   exactNormalTaskVersionDocuments(plan.CandidateTaskVersions),
		GraphDigest:             hex.EncodeToString(plan.GraphDigest[:]), ArtifactDigest: hex.EncodeToString(plan.ArtifactDigest[:]),
		DecisionEvidenceID:   plan.DecisionEvidence.ID.String(),
		DecisionReplayDigest: hex.EncodeToString(plan.DecisionEvidence.ReplayDigest[:]),
		SelectedEdges:        make([]exactNormalAssignmentEdgeProofDocument, len(plan.SelectedEdges)),
		CreatedAt:            plan.CreatedAt,
	}
	for index, edge := range plan.SelectedEdges {
		document.SelectedEdges[index] = exactNormalAssignmentEdgeProofDocument{
			ID: edge.ID.String(), ReservationID: edge.ReservationID.String(), Position: edge.Position,
			SnapshotID: edge.Snapshot.SnapshotID.String(), TaskID: edge.Snapshot.TaskID.String(),
			TaskVersion: edge.Snapshot.Version, ContentDigest: hex.EncodeToString(edge.ContentDigest[:]),
		}
	}
	payload, err := json.Marshal(document)
	if err != nil {
		return "", exactNormalAssignmentError("encode proof: %v", err)
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func exactNormalScopeProofDocument(
	scope ExactNormalAssignmentScope,
) exactNormalAssignmentScopeProofDocument {
	return exactNormalAssignmentScopeProofDocument{
		TournamentID: scope.TournamentID.String(), RosterID: scope.RosterID.String(),
		SeriesID: scope.SeriesID.String(), SlotID: scope.SlotID.String(),
		CategoryLockID: scope.CategoryLockID.String(),
	}
}

func exactNormalRevisionsProofDocument(
	revisions ExactNormalAssignmentSourceRevisions,
) exactNormalAssignmentRevisionsProofDocument {
	return exactNormalAssignmentRevisionsProofDocument{
		SlotRevisionID: revisions.SlotRevisionID.String(), SlotRevision: revisions.SlotRevision,
		GraphRevisionID: revisions.GraphRevisionID.String(), GraphRevision: revisions.GraphRevision,
		PoolRevisionID: revisions.PoolRevisionID.String(), PoolRevision: revisions.PoolRevision,
		HistoryRevisionID: revisions.HistoryRevisionID.String(), HistoryRevision: revisions.HistoryRevision,
		ReservationRevisionID: revisions.ReservationRevisionID.String(),
		ReservationRevision:   revisions.ReservationRevision,
		ArtifactRevisionID:    revisions.ArtifactRevisionID.String(), ArtifactRevision: revisions.ArtifactRevision,
		CategoryRevisionID: revisions.CategoryRevisionID.String(), CategoryRevision: revisions.CategoryRevision,
	}
}

func exactNormalParticipantIDDocuments(participantIDs []uuid.UUID) []string {
	documents := make([]string, len(participantIDs))
	for index, participantID := range participantIDs {
		documents[index] = participantID.String()
	}
	return documents
}

func exactNormalParticipantReservationDocuments(
	reservations []ExactNormalParticipantReservation,
) []exactNormalParticipantReservationProofDocument {
	documents := make([]exactNormalParticipantReservationProofDocument, len(reservations))
	for index, item := range reservations {
		documents[index] = exactNormalParticipantReservationProofDocument{
			ParticipantID: item.ParticipantID.String(), PlayerID: item.PlayerID.String(),
			ReservationID: item.Reservation.ReservationID.String(), OwnerKind: string(item.Reservation.OwnerKind),
			OwnerID: item.Reservation.OwnerID.String(), Revision: item.Reservation.Revision,
			AcquiredAt: item.Reservation.AcquiredAt, UpdatedAt: item.Reservation.UpdatedAt,
		}
	}
	return documents
}

func exactNormalHistoryDocuments(history []CapacityTaskUse) []exactNormalHistoryProofDocument {
	documents := make([]exactNormalHistoryProofDocument, len(history))
	for index, item := range history {
		documents[index] = exactNormalHistoryProofDocument{
			ParticipantID: item.ParticipantID.String(), TaskID: item.TaskID.String(),
		}
	}
	return documents
}

func exactNormalTaskVersionDocuments(
	versions []TaskVersionRef,
) []exactNormalTaskVersionProofDocument {
	documents := make([]exactNormalTaskVersionProofDocument, len(versions))
	for index, item := range versions {
		documents[index] = exactNormalTaskVersionProofDocument{
			TaskID: item.TaskID.String(), Version: item.Version,
		}
	}
	return documents
}

func exactNormalTaskVersionRefs(candidates []ExactNormalTaskVersion) []TaskVersionRef {
	result := make([]TaskVersionRef, len(candidates))
	for index, candidate := range candidates {
		result[index] = TaskVersionRef{TaskID: candidate.Task.ID, Version: candidate.Version}
	}
	return result
}

func canonicalExactNormalHistory(history []CapacityTaskUse) []CapacityTaskUse {
	result := append([]CapacityTaskUse(nil), history...)
	sort.Slice(result, func(i, j int) bool {
		if comparison := bytes.Compare(result[i].ParticipantID[:], result[j].ParticipantID[:]); comparison != 0 {
			return comparison < 0
		}
		return bytes.Compare(result[i].TaskID[:], result[j].TaskID[:]) < 0
	})
	return result
}

func cloneExactNormalParticipantReservations(
	input []ExactNormalParticipantReservation,
) []ExactNormalParticipantReservation {
	return append([]ExactNormalParticipantReservation(nil), input...)
}

func cloneExactNormalAssignmentPlan(plan ExactNormalAssignmentPlan) ExactNormalAssignmentPlan {
	cloned := plan
	cloned.Pool = cloneTaskPool(plan.Pool)
	cloned.ParticipantIDs = append([]uuid.UUID(nil), plan.ParticipantIDs...)
	cloned.ParticipantReservations = cloneExactNormalParticipantReservations(plan.ParticipantReservations)
	cloned.History = append([]CapacityTaskUse(nil), plan.History...)
	cloned.CandidateTaskVersions = append([]TaskVersionRef(nil), plan.CandidateTaskVersions...)
	cloned.DecisionEvidence.NormalizedInputs = append([]string(nil), plan.DecisionEvidence.NormalizedInputs...)
	cloned.DecisionEvidence.Result = append([]string(nil), plan.DecisionEvidence.Result...)
	cloned.SelectedEdges = append([]ExactNormalAssignmentEdge(nil), plan.SelectedEdges...)
	for index := range cloned.SelectedEdges {
		task := taskexec.CloneTask(&domain.Task{
			ID:            cloned.SelectedEdges[index].Snapshot.TaskID,
			Title:         cloned.SelectedEdges[index].Snapshot.Title,
			Description:   cloned.SelectedEdges[index].Snapshot.Description,
			Category:      cloned.SelectedEdges[index].Snapshot.Category,
			Difficulty:    cloned.SelectedEdges[index].Snapshot.Difficulty,
			TimeLimit:     cloned.SelectedEdges[index].Snapshot.TimeLimit,
			Flag:          cloned.SelectedEdges[index].Snapshot.Flag,
			Hints:         cloned.SelectedEdges[index].Snapshot.Hints,
			TaskURL:       cloned.SelectedEdges[index].Snapshot.TaskURL,
			SourceFileURL: cloned.SelectedEdges[index].Snapshot.SourceFileURL,
		})
		cloned.SelectedEdges[index].Snapshot.Hints = task.Hints
		cloned.SelectedEdges[index].Snapshot.TaskURL = task.TaskURL
		cloned.SelectedEdges[index].Snapshot.SourceFileURL = task.SourceFileURL
	}
	return cloned
}

func exactNormalTaskVersionEvidence(taskID uuid.UUID, version int) string {
	return fmt.Sprintf("%s@%d", taskID, version)
}

func exactNormalAssignmentError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidExactNormalAssignment, fmt.Sprintf(format, arguments...))
}
