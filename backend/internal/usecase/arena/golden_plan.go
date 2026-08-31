package arena

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/taskexec"
)

const goldenExactPlanAttempts = 2

var (
	ErrInvalidGoldenExactPlan      = errors.New("invalid Golden exact plan")
	ErrGoldenExactPlanStale        = errors.New("golden exact plan authority is stale")
	ErrGoldenExactPlanConflict     = errors.New("golden exact plan conflict")
	ErrGoldenExactPlanInsufficient = errors.New("insufficient Golden exact matching")
)

type GoldenExactPlanScope struct {
	TournamentID uuid.UUID `json:"tournament_id"`
	PlanSetID    uuid.UUID `json:"plan_set_id"`
}

type GoldenExactPlanRevisions struct {
	SourceProjectionRevisionID domain.ArenaDerivedRevisionID `json:"source_projection_revision_id"`
	GroupSetRevisionID         uuid.UUID                     `json:"group_set_revision_id"`
	GroupSetRevision           int64                         `json:"group_set_revision"`
	PoolRevisionID             uuid.UUID                     `json:"pool_revision_id"`
	PoolRevision               int64                         `json:"pool_revision"`
	HistoryRevisionID          uuid.UUID                     `json:"history_revision_id"`
	HistoryRevision            int64                         `json:"history_revision"`
	TaskHealthRevisionID       uuid.UUID                     `json:"task_health_revision_id"`
	TaskHealthRevision         int64                         `json:"task_health_revision"`
	ArtifactRevisionID         uuid.UUID                     `json:"artifact_revision_id"`
	ArtifactRevision           int64                         `json:"artifact_revision"`
	ReservationRevisionID      uuid.UUID                     `json:"reservation_revision_id"`
	ReservationRevision        int64                         `json:"reservation_revision"`
	MembershipRevisionID       uuid.UUID                     `json:"membership_revision_id"`
	MembershipRevision         int64                         `json:"membership_revision"`
}

type GoldenExactPlanExpectation struct {
	Revisions           GoldenExactPlanRevisions `json:"revisions"`
	SourcePayloadDigest [sha256.Size]byte        `json:"source_payload_digest"`
	GroupDigest         [sha256.Size]byte        `json:"group_digest"`
	PoolDigest          [sha256.Size]byte        `json:"pool_digest"`
	HistoryDigest       [sha256.Size]byte        `json:"history_digest"`
	TaskHealthDigest    [sha256.Size]byte        `json:"task_health_digest"`
	ArtifactDigest      [sha256.Size]byte        `json:"artifact_digest"`
	ReservationDigest   [sha256.Size]byte        `json:"reservation_digest"`
	MembershipDigest    [sha256.Size]byte        `json:"membership_digest"`
}

type GoldenPlanGroupAuthority struct {
	Revision             GoldenGroupRevision
	ActiveParticipantIDs []uuid.UUID
}

type GoldenParticipantReservation struct {
	ParticipantID uuid.UUID
	PlayerID      uuid.UUID
	Reservation   domain.ParticipantReservation
}

type GoldenExactTaskVersion struct {
	PoolRevisionID uuid.UUID
	Version        int
	Task           domain.Task
	Health         TaskVersionHealth
	ArtifactDigest [sha256.Size]byte
}

type GoldenTaskReservation struct {
	TaskVersion    TaskVersionRef
	ReservationID  uuid.UUID
	PlanID         uuid.UUID
	PlanRevisionID uuid.UUID
}

type GoldenExactPlanAuthority struct {
	Scope                    GoldenExactPlanScope
	Revisions                GoldenExactPlanRevisions
	Source                   GoldenStandingsProjection
	Groups                   []GoldenPlanGroupAuthority
	Pool                     TaskPoolRevision
	History                  []ArenaTaskReceiptRef
	Candidates               []GoldenExactTaskVersion
	ParticipantReservations  []GoldenParticipantReservation
	ExistingTaskReservations []GoldenTaskReservation
	Evidence                 GoldenExactPlanExpectation
}

type GoldenExactGroupCommand struct {
	GroupID         uuid.UUID
	GroupRevisionID domain.ArenaDerivedRevisionID
	EdgeIDs         [domain.ArenaAssignmentReserveCount + 1]uuid.UUID
	ReservationIDs  [domain.ArenaAssignmentReserveCount + 1]uuid.UUID
	SnapshotIDs     [domain.ArenaAssignmentReserveCount + 1]uuid.UUID
}

type GoldenExactPlanCommand struct {
	Scope          GoldenExactPlanScope
	PlanID         uuid.UUID
	PlanRevisionID uuid.UUID
	Expected       GoldenExactPlanExpectation
	GroupCommands  []GoldenExactGroupCommand
	CreatedAt      time.Time
}

type GoldenExactPlanEdge struct {
	ID            uuid.UUID
	ReservationID uuid.UUID
	Position      int
	Snapshot      domain.ArenaTaskSnapshot
	ContentDigest [sha256.Size]byte
}

type GoldenExactGroupPlan struct {
	GroupID                    uuid.UUID
	GroupRevisionID            domain.ArenaDerivedRevisionID
	SourceProjectionRevisionID domain.ArenaDerivedRevisionID
	PositionFrom               int
	PositionTo                 int
	ParticipantIDs             []uuid.UUID
	Edges                      []GoldenExactPlanEdge
}

type GoldenExactPlan struct {
	Scope          GoldenExactPlanScope
	PlanID         uuid.UUID
	PlanRevisionID uuid.UUID
	Expected       GoldenExactPlanExpectation
	Authority      GoldenExactPlanAuthority
	Groups         []GoldenExactGroupPlan
	CreatedAt      time.Time
	ProofHash      string
}

// GoldenExactPlanRepository owns one transaction. Commit must revalidate the
// full authority, compare every bound revision and digest, and atomically lock
// the plan plus every task reservation.
type GoldenExactPlanRepository interface {
	GetGoldenExactPlan(ctx context.Context, scope GoldenExactPlanScope, planID uuid.UUID) (*GoldenExactPlan, error)
	LoadGoldenExactPlanAuthority(ctx context.Context, scope GoldenExactPlanScope) (GoldenExactPlanAuthority, error)
	CommitGoldenExactPlan(ctx context.Context, plan GoldenExactPlan) (*GoldenExactPlan, bool, error)
}

type GoldenExactPlanUseCase struct {
	repository GoldenExactPlanRepository
}

func NewGoldenExactPlanUseCase(repository GoldenExactPlanRepository) *GoldenExactPlanUseCase {
	return &GoldenExactPlanUseCase{repository: repository}
}

func (u *GoldenExactPlanUseCase) PlanAndCommit(
	ctx context.Context,
	command GoldenExactPlanCommand,
) (*GoldenExactPlan, bool, error) {
	if u == nil || u.repository == nil {
		return nil, false, domain.ErrValidation
	}
	command.GroupCommands = append([]GoldenExactGroupCommand(nil), command.GroupCommands...)
	if err := validateGoldenExactPlanCommandIdentity(command); err != nil {
		return nil, false, err
	}
	stored, err := u.repository.GetGoldenExactPlan(ctx, command.Scope, command.PlanID)
	if err != nil {
		return nil, false, fmt.Errorf("GoldenExactPlanUseCase - get stored plan: %w", err)
	}
	if stored != nil {
		return reconcileGoldenExactPlan(command, stored)
	}
	for range goldenExactPlanAttempts {
		plan, changed, retry, attemptErr := u.planAndCommitAttempt(ctx, command)
		if attemptErr != nil {
			return nil, false, attemptErr
		}
		if retry {
			continue
		}
		return plan, changed, nil
	}
	return nil, false, ErrGoldenExactPlanConflict
}

func (u *GoldenExactPlanUseCase) planAndCommitAttempt(
	ctx context.Context,
	command GoldenExactPlanCommand,
) (*GoldenExactPlan, bool, bool, error) {
	authority, err := u.repository.LoadGoldenExactPlanAuthority(ctx, command.Scope)
	if err != nil {
		return nil, false, false, fmt.Errorf("GoldenExactPlanUseCase - load authority: %w", err)
	}
	plan, err := BuildGoldenExactPlan(command, authority)
	if err != nil {
		return nil, false, false, err
	}
	committed, changed, err := u.repository.CommitGoldenExactPlan(ctx, plan)
	if errors.Is(err, domain.ErrConflict) {
		stored, getErr := u.repository.GetGoldenExactPlan(ctx, command.Scope, command.PlanID)
		if getErr != nil {
			return nil, false, false, fmt.Errorf("GoldenExactPlanUseCase - reconcile conflict: %w", getErr)
		}
		if stored != nil {
			result, storedChanged, reconcileErr := reconcileGoldenExactPlan(command, stored)
			return result, storedChanged, false, reconcileErr
		}
		return nil, false, true, nil
	}
	if err != nil {
		return nil, false, false, fmt.Errorf("GoldenExactPlanUseCase - commit plan: %w", err)
	}
	result, err := validateCommittedGoldenExactPlan(plan, committed)
	return result, changed, false, err
}

func validateCommittedGoldenExactPlan(
	plan GoldenExactPlan,
	committed *GoldenExactPlan,
) (*GoldenExactPlan, error) {
	if committed == nil {
		return nil, domain.ErrInternal
	}
	if committed.Scope != plan.Scope || committed.PlanID != plan.PlanID ||
		committed.PlanRevisionID != plan.PlanRevisionID || !committed.CreatedAt.Equal(plan.CreatedAt) {
		return nil, ErrGoldenExactPlanConflict
	}
	if err := committed.Validate(); err != nil {
		return nil, domain.ErrInternal
	}
	if committed.ProofHash != plan.ProofHash {
		return nil, ErrGoldenExactPlanConflict
	}
	result := committed.Snapshot()
	return &result, nil
}

func reconcileGoldenExactPlan(
	command GoldenExactPlanCommand,
	stored *GoldenExactPlan,
) (*GoldenExactPlan, bool, error) {
	if stored == nil || stored.Scope != command.Scope || stored.PlanID != command.PlanID {
		return nil, false, domain.ErrInternal
	}
	if err := stored.Validate(); err != nil {
		return nil, false, domain.ErrInternal
	}
	if !goldenExactPlanMatchesCommand(*stored, command) {
		return nil, false, ErrGoldenExactPlanConflict
	}
	result := stored.Snapshot()
	return &result, false, nil
}

func goldenExactPlanMatchesCommand(plan GoldenExactPlan, command GoldenExactPlanCommand) bool {
	if !goldenExactPlanBaseMatchesCommand(plan, command) {
		return false
	}
	byGroup := make(map[uuid.UUID]GoldenExactGroupCommand, len(command.GroupCommands))
	for _, group := range command.GroupCommands {
		byGroup[group.GroupID] = group
	}
	for _, group := range plan.Groups {
		value, exists := byGroup[group.GroupID]
		if !exists || !goldenExactGroupMatchesCommand(group, value) {
			return false
		}
	}
	return true
}

func goldenExactPlanBaseMatchesCommand(plan GoldenExactPlan, command GoldenExactPlanCommand) bool {
	return plan.Scope == command.Scope && plan.PlanID == command.PlanID &&
		plan.PlanRevisionID == command.PlanRevisionID && plan.Expected == command.Expected &&
		plan.CreatedAt.Equal(command.CreatedAt) && len(plan.Groups) == len(command.GroupCommands)
}

func goldenExactGroupMatchesCommand(group GoldenExactGroupPlan, command GoldenExactGroupCommand) bool {
	if command.GroupRevisionID != group.GroupRevisionID || len(group.Edges) != len(command.EdgeIDs) {
		return false
	}
	for index, edge := range group.Edges {
		if command.EdgeIDs[index] != edge.ID || command.ReservationIDs[index] != edge.ReservationID ||
			command.SnapshotIDs[index] != edge.Snapshot.SnapshotID {
			return false
		}
	}
	return true
}

func BuildGoldenExactPlanAuthority(input GoldenExactPlanAuthority) (GoldenExactPlanAuthority, error) {
	canonical, evidence, err := normalizeGoldenExactPlanAuthority(input)
	if err != nil {
		return GoldenExactPlanAuthority{}, err
	}
	canonical.Evidence = evidence
	return canonical.Snapshot(), nil
}

func (a GoldenExactPlanAuthority) Expectation() GoldenExactPlanExpectation { return a.Evidence }

func (a GoldenExactPlanAuthority) Snapshot() GoldenExactPlanAuthority {
	clone := a
	clone.Source = a.Source.Snapshot()
	clone.Groups = make([]GoldenPlanGroupAuthority, len(a.Groups))
	for index, group := range a.Groups {
		clone.Groups[index] = GoldenPlanGroupAuthority{
			Revision:             group.Revision.Snapshot(),
			ActiveParticipantIDs: append([]uuid.UUID(nil), group.ActiveParticipantIDs...),
		}
	}
	clone.Pool = cloneTaskPool(a.Pool)
	clone.History = append([]ArenaTaskReceiptRef(nil), a.History...)
	clone.Candidates = cloneGoldenCandidates(a.Candidates)
	clone.ParticipantReservations = append([]GoldenParticipantReservation(nil), a.ParticipantReservations...)
	clone.ExistingTaskReservations = append([]GoldenTaskReservation(nil), a.ExistingTaskReservations...)
	return clone
}

func BuildGoldenExactPlan(
	command GoldenExactPlanCommand,
	authority GoldenExactPlanAuthority,
) (GoldenExactPlan, error) {
	if err := validateGoldenExactPlanCommandIdentity(command); err != nil {
		return GoldenExactPlan{}, err
	}
	canonical, evidence, err := normalizeGoldenExactPlanAuthority(authority)
	if err != nil {
		return GoldenExactPlan{}, err
	}
	canonical.Evidence = evidence
	if command.Scope != canonical.Scope || command.Expected != canonical.Expectation() {
		return GoldenExactPlan{}, fmt.Errorf("%w: command expectation does not match current authority", ErrGoldenExactPlanStale)
	}
	if err := validateGoldenCommandAuthorityAliases(command, canonical); err != nil {
		return GoldenExactPlan{}, err
	}
	commands, err := canonicalGoldenGroupCommands(command.GroupCommands, canonical.Groups)
	if err != nil {
		return GoldenExactPlan{}, err
	}
	matching, err := goldenExactMatching(canonical)
	if err != nil {
		return GoldenExactPlan{}, err
	}
	plan := GoldenExactPlan{
		Scope: command.Scope, PlanID: command.PlanID, PlanRevisionID: command.PlanRevisionID,
		Expected: command.Expected, Authority: canonical.Snapshot(), CreatedAt: command.CreatedAt,
		Groups: make([]GoldenExactGroupPlan, len(canonical.Groups)),
	}
	for groupIndex, group := range canonical.Groups {
		from, to := group.Revision.Positions()
		groupPlan := GoldenExactGroupPlan{
			GroupID: group.Revision.GroupID(), GroupRevisionID: group.Revision.RevisionID(),
			SourceProjectionRevisionID: group.Revision.SourceProjectionRevisionID(),
			PositionFrom:               from, PositionTo: to,
			ParticipantIDs: append([]uuid.UUID(nil), group.ActiveParticipantIDs...),
			Edges:          make([]GoldenExactPlanEdge, domain.ArenaAssignmentReserveCount+1),
		}
		for edgeIndex := range groupPlan.Edges {
			candidate := canonical.Candidates[matching[groupIndex][edgeIndex]]
			snapshot, buildErr := taskexec.BuildSnapshot(taskexec.SnapshotInput{
				SnapshotID: commands[groupIndex].SnapshotIDs[edgeIndex],
				Version:    candidate.Version, Kind: domain.ArenaTaskKindGolden, Task: candidate.Task,
			})
			if buildErr != nil {
				return GoldenExactPlan{}, goldenExactPlanError("snapshot: %v", buildErr)
			}
			digest, digestErr := taskexec.SnapshotDigest(snapshot)
			if digestErr != nil {
				return GoldenExactPlan{}, goldenExactPlanError("snapshot digest: %v", digestErr)
			}
			groupPlan.Edges[edgeIndex] = GoldenExactPlanEdge{
				ID:            commands[groupIndex].EdgeIDs[edgeIndex],
				ReservationID: commands[groupIndex].ReservationIDs[edgeIndex],
				Position:      edgeIndex + 1, Snapshot: snapshot, ContentDigest: digest,
			}
		}
		plan.Groups[groupIndex] = groupPlan
	}
	plan.ProofHash, err = goldenExactPlanProofHash(plan)
	if err != nil {
		return GoldenExactPlan{}, err
	}
	if err := plan.Validate(); err != nil {
		return GoldenExactPlan{}, err
	}
	return plan.Snapshot(), nil
}

func (p GoldenExactPlan) Validate() error {
	canonical, matching, err := validateGoldenExactPlanAuthority(p)
	if err != nil {
		return err
	}
	if err := validateGoldenExactPlanGroups(p.Groups, canonical, matching); err != nil {
		return err
	}
	return validateGoldenExactPlanProof(p)
}

func validateGoldenExactPlanAuthority(
	plan GoldenExactPlan,
) (GoldenExactPlanAuthority, [][]int, error) {
	if err := validateGoldenExactPlanIdentity(plan); err != nil {
		return GoldenExactPlanAuthority{}, nil, err
	}
	canonical, evidence, err := normalizeGoldenExactPlanAuthority(plan.Authority)
	if err != nil {
		return GoldenExactPlanAuthority{}, nil, err
	}
	canonical.Evidence = evidence
	if plan.Expected != canonical.Expectation() || !reflect.DeepEqual(canonical, plan.Authority) {
		return GoldenExactPlanAuthority{}, nil, goldenExactPlanError("retained authority is not canonical")
	}
	if len(plan.Groups) != len(canonical.Groups) {
		return GoldenExactPlanAuthority{}, nil, goldenExactPlanError("plan does not cover every active group")
	}
	retainedCommand, err := goldenExactPlanCommandFromPlan(plan)
	if err != nil {
		return GoldenExactPlanAuthority{}, nil, err
	}
	if err := validateGoldenExactPlanCommandIdentity(retainedCommand); err != nil {
		return GoldenExactPlanAuthority{}, nil, err
	}
	if err := validateGoldenCommandAuthorityAliases(retainedCommand, canonical); err != nil {
		return GoldenExactPlanAuthority{}, nil, err
	}
	matching, err := goldenExactMatching(canonical)
	if err != nil {
		return GoldenExactPlanAuthority{}, nil, err
	}
	return canonical, matching, nil
}

func validateGoldenExactPlanIdentity(plan GoldenExactPlan) error {
	if plan.Scope != plan.Authority.Scope || plan.PlanID == uuid.Nil || plan.PlanRevisionID == uuid.Nil ||
		plan.PlanID == plan.PlanRevisionID || plan.CreatedAt.IsZero() || plan.CreatedAt.Location() != time.UTC {
		return goldenExactPlanError("invalid plan identity")
	}
	return nil
}

func validateGoldenExactPlanGroups(
	groups []GoldenExactGroupPlan,
	authority GoldenExactPlanAuthority,
	matching [][]int,
) error {
	selected := make(map[TaskVersionRef]struct{}, len(groups)*(domain.ArenaAssignmentReserveCount+1))
	for groupIndex, group := range groups {
		if err := validateGoldenExactGroupPlan(groupIndex, group, authority, matching, selected); err != nil {
			return err
		}
	}
	return nil
}

func validateGoldenExactGroupPlan(
	groupIndex int,
	group GoldenExactGroupPlan,
	authority GoldenExactPlanAuthority,
	matching [][]int,
	selected map[TaskVersionRef]struct{},
) error {
	authorityGroup := authority.Groups[groupIndex]
	if !goldenExactGroupMatchesAuthority(group, authorityGroup) {
		return goldenExactPlanError("group plan does not match current membership")
	}
	for edgeIndex, edge := range group.Edges {
		if err := validateGoldenExactPlanEdge(groupIndex, edgeIndex, edge, authority, matching, selected); err != nil {
			return err
		}
	}
	return nil
}

func goldenExactGroupMatchesAuthority(
	group GoldenExactGroupPlan,
	authority GoldenPlanGroupAuthority,
) bool {
	from, to := authority.Revision.Positions()
	return group.GroupID == authority.Revision.GroupID() &&
		group.GroupRevisionID == authority.Revision.RevisionID() &&
		group.SourceProjectionRevisionID == authority.Revision.SourceProjectionRevisionID() &&
		group.PositionFrom == from && group.PositionTo == to &&
		reflect.DeepEqual(group.ParticipantIDs, authority.ActiveParticipantIDs) &&
		len(group.Edges) == domain.ArenaAssignmentReserveCount+1
}

func validateGoldenExactPlanEdge(
	groupIndex int,
	edgeIndex int,
	edge GoldenExactPlanEdge,
	authority GoldenExactPlanAuthority,
	matching [][]int,
	selected map[TaskVersionRef]struct{},
) error {
	if !validGoldenExactPlanEdgeIdentity(edgeIndex, edge) {
		return goldenExactPlanError("invalid selected edge")
	}
	candidate := authority.Candidates[matching[groupIndex][edgeIndex]]
	wantSnapshot, buildErr := taskexec.BuildSnapshot(taskexec.SnapshotInput{
		SnapshotID: edge.Snapshot.SnapshotID, Version: candidate.Version,
		Kind: domain.ArenaTaskKindGolden, Task: candidate.Task,
	})
	if buildErr != nil || !reflect.DeepEqual(wantSnapshot, edge.Snapshot) {
		return goldenExactPlanError("selected snapshot does not follow canonical matching")
	}
	digest, digestErr := taskexec.SnapshotDigest(wantSnapshot)
	if digestErr != nil || digest != edge.ContentDigest {
		return goldenExactPlanError("selected artifact digest changed")
	}
	ref := TaskVersionRef{TaskID: edge.Snapshot.TaskID, Version: edge.Snapshot.Version}
	if _, duplicate := selected[ref]; duplicate {
		return goldenExactPlanError("task version is reserved more than once")
	}
	candidateIndex := goldenCandidateIndex(authority.Candidates, ref)
	if candidateIndex != matching[groupIndex][edgeIndex] ||
		!goldenCandidateEligibleForGroup(authority, groupIndex, candidateIndex) {
		return goldenExactPlanError("selected task is not eligible for every active member")
	}
	selected[ref] = struct{}{}
	return nil
}

func validGoldenExactPlanEdgeIdentity(edgeIndex int, edge GoldenExactPlanEdge) bool {
	return edge.ID != uuid.Nil && edge.ReservationID != uuid.Nil && edge.Position == edgeIndex+1 &&
		edge.Snapshot.Kind == domain.ArenaTaskKindGolden && edge.Snapshot.Validate() == nil
}

func validateGoldenExactPlanProof(plan GoldenExactPlan) error {
	want, err := goldenExactPlanProofHash(plan)
	if err != nil || len(plan.ProofHash) != sha256.Size*2 || want != plan.ProofHash {
		return goldenExactPlanError("proof hash does not match")
	}
	return nil
}

func (p GoldenExactPlan) Snapshot() GoldenExactPlan {
	clone := p
	clone.Authority = p.Authority.Snapshot()
	clone.Groups = make([]GoldenExactGroupPlan, len(p.Groups))
	for groupIndex, group := range p.Groups {
		clone.Groups[groupIndex] = group
		clone.Groups[groupIndex].ParticipantIDs = append([]uuid.UUID(nil), group.ParticipantIDs...)
		clone.Groups[groupIndex].Edges = append([]GoldenExactPlanEdge(nil), group.Edges...)
		for edgeIndex := range clone.Groups[groupIndex].Edges {
			clone.Groups[groupIndex].Edges[edgeIndex].Snapshot = cloneTaskSnapshot(group.Edges[edgeIndex].Snapshot)
		}
	}
	return clone
}

func goldenExactPlanCommandFromPlan(plan GoldenExactPlan) (GoldenExactPlanCommand, error) {
	command := GoldenExactPlanCommand{
		Scope: plan.Scope, PlanID: plan.PlanID, PlanRevisionID: plan.PlanRevisionID,
		Expected: plan.Expected, CreatedAt: plan.CreatedAt,
		GroupCommands: make([]GoldenExactGroupCommand, len(plan.Groups)),
	}
	for groupIndex, group := range plan.Groups {
		if len(group.Edges) != domain.ArenaAssignmentReserveCount+1 {
			return GoldenExactPlanCommand{}, goldenExactPlanError("group does not contain one primary and two reserves")
		}
		command.GroupCommands[groupIndex].GroupID = group.GroupID
		command.GroupCommands[groupIndex].GroupRevisionID = group.GroupRevisionID
		for edgeIndex, edge := range group.Edges {
			command.GroupCommands[groupIndex].EdgeIDs[edgeIndex] = edge.ID
			command.GroupCommands[groupIndex].ReservationIDs[edgeIndex] = edge.ReservationID
			command.GroupCommands[groupIndex].SnapshotIDs[edgeIndex] = edge.Snapshot.SnapshotID
		}
	}
	return command, nil
}

func GoldenTaskArtifactDigest(task domain.Task, version int) [sha256.Size]byte {
	cloned := taskexec.CloneTask(&task)
	document := goldenTaskArtifactDocument{
		Version: version,
		Kind:    domain.ArenaTaskKindGolden,
		Task: goldenTaskArtifactTask{
			ID: cloned.ID, Title: cloned.Title, Description: cloned.Description,
			Category: cloned.Category, Difficulty: cloned.Difficulty, TimeLimit: cloned.TimeLimit,
			Flag: cloned.Flag, Hints: cloned.Hints, TaskURL: cloned.TaskURL,
			SourceFileURL: cloned.SourceFileURL, CreatedAt: cloned.CreatedAt,
		},
	}
	payload, err := json.Marshal(document)
	if err != nil {
		return [sha256.Size]byte{}
	}
	return sha256.Sum256(payload)
}

type goldenTaskArtifactDocument struct {
	Version int                    `json:"version"`
	Kind    domain.ArenaTaskKind   `json:"kind"`
	Task    goldenTaskArtifactTask `json:"task"`
}

type goldenTaskArtifactTask struct {
	ID            uuid.UUID         `json:"id"`
	Title         string            `json:"title"`
	Description   string            `json:"description"`
	Category      domain.Category   `json:"category"`
	Difficulty    domain.Difficulty `json:"difficulty"`
	TimeLimit     int               `json:"time_limit"`
	Flag          string            `json:"flag"`
	Hints         []string          `json:"hints"`
	TaskURL       *string           `json:"task_url"`
	SourceFileURL *string           `json:"source_file_url"`
	CreatedAt     time.Time         `json:"created_at"`
}

func normalizeGoldenExactPlanAuthority(
	input GoldenExactPlanAuthority,
) (GoldenExactPlanAuthority, GoldenExactPlanExpectation, error) {
	if err := validateGoldenSourceAuthority(input); err != nil {
		return GoldenExactPlanAuthority{}, GoldenExactPlanExpectation{}, err
	}
	if err := validateGoldenRevisionIdentitySet(input.Scope, input.Revisions); err != nil {
		return GoldenExactPlanAuthority{}, GoldenExactPlanExpectation{}, err
	}
	groups, participants, err := normalizeGoldenPlanGroups(input.Source, input.Groups)
	if err != nil {
		return GoldenExactPlanAuthority{}, GoldenExactPlanExpectation{}, err
	}
	if err := validateGoldenAuthorityAliases(input.Scope, input.Revisions, input.Source, groups); err != nil {
		return GoldenExactPlanAuthority{}, GoldenExactPlanExpectation{}, err
	}
	pool, err := normalizeTaskPoolRevision(input.Pool, domain.ArenaTaskKindGolden)
	if err != nil || pool.ID != input.Revisions.PoolRevisionID || pool.Revision != input.Revisions.PoolRevision {
		return GoldenExactPlanAuthority{}, GoldenExactPlanExpectation{}, goldenExactPlanError("invalid Golden pool authority")
	}
	candidates, err := normalizeGoldenExactCandidates(pool, input.Candidates)
	if err != nil {
		return GoldenExactPlanAuthority{}, GoldenExactPlanExpectation{}, err
	}
	history, err := normalizeGoldenHistory(participants, input.History)
	if err != nil {
		return GoldenExactPlanAuthority{}, GoldenExactPlanExpectation{}, err
	}
	participantReservations, err := normalizeGoldenParticipantReservations(input.Scope, participants, input.ParticipantReservations)
	if err != nil {
		return GoldenExactPlanAuthority{}, GoldenExactPlanExpectation{}, err
	}
	taskReservations, err := normalizeGoldenTaskReservations(pool, input.ExistingTaskReservations)
	if err != nil {
		return GoldenExactPlanAuthority{}, GoldenExactPlanExpectation{}, err
	}
	canonical := input
	canonical.Source = input.Source.Snapshot()
	canonical.Groups = groups
	canonical.Pool = pool
	canonical.History = history
	canonical.Candidates = candidates
	canonical.ParticipantReservations = participantReservations
	canonical.ExistingTaskReservations = taskReservations
	if err := validateGoldenRetainedAuthorityAliases(canonical); err != nil {
		return GoldenExactPlanAuthority{}, GoldenExactPlanExpectation{}, err
	}
	evidence, err := goldenAuthorityEvidence(canonical)
	if err != nil {
		return GoldenExactPlanAuthority{}, GoldenExactPlanExpectation{}, err
	}
	return canonical, evidence, nil
}

func validateGoldenSourceAuthority(input GoldenExactPlanAuthority) error {
	if !validGoldenExactPlanScope(input.Scope) || !validGoldenExactPlanRevisions(input.Revisions) ||
		input.Source.Validate() != nil || input.Source.TournamentID != input.Scope.TournamentID ||
		input.Revisions.SourceProjectionRevisionID != input.Source.RevisionID {
		return goldenExactPlanError("invalid source authority")
	}
	return nil
}

func validateGoldenRetainedAuthorityAliases(authority GoldenExactPlanAuthority) error {
	ids := goldenAuthorityBoundIDs(authority)
	seen := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		if id == uuid.Nil {
			return goldenExactPlanError("missing retained authority identity")
		}
		if _, duplicate := seen[id]; duplicate {
			return goldenExactPlanError("retained authority identity is cross-aliased")
		}
		seen[id] = struct{}{}
	}
	ownerPlans := make(map[uuid.UUID]uuid.UUID, len(authority.ExistingTaskReservations))
	ownerRevisions := make(map[uuid.UUID]uuid.UUID, len(authority.ExistingTaskReservations))
	for _, reservation := range authority.ExistingTaskReservations {
		if _, alias := seen[reservation.PlanID]; alias {
			return goldenExactPlanError("task reservation owner aliases retained authority")
		}
		if _, alias := seen[reservation.PlanRevisionID]; alias {
			return goldenExactPlanError("task reservation owner revision aliases retained authority")
		}
		if _, crossRole := ownerRevisions[reservation.PlanID]; crossRole {
			return goldenExactPlanError("task reservation owner identity changes role")
		}
		if _, crossRole := ownerPlans[reservation.PlanRevisionID]; crossRole {
			return goldenExactPlanError("task reservation owner revision changes role")
		}
		if revisionID, exists := ownerPlans[reservation.PlanID]; exists && revisionID != reservation.PlanRevisionID {
			return goldenExactPlanError("task reservation owner has multiple revisions")
		}
		if planID, exists := ownerRevisions[reservation.PlanRevisionID]; exists && planID != reservation.PlanID {
			return goldenExactPlanError("task reservation revision has multiple owners")
		}
		ownerPlans[reservation.PlanID] = reservation.PlanRevisionID
		ownerRevisions[reservation.PlanRevisionID] = reservation.PlanID
	}
	return nil
}

func goldenAuthorityBoundIDs(authority GoldenExactPlanAuthority) []uuid.UUID {
	historicalCount := 0
	if authority.Source.PreviousRevisionID != nil {
		historicalCount++
	}
	for _, group := range authority.Groups {
		if group.Revision.PreviousRevisionID() != nil {
			historicalCount++
		}
	}
	capacity := 11 + 2*len(authority.Groups) + len(authority.Candidates) +
		3*len(authority.ParticipantReservations) + len(authority.ExistingTaskReservations) + historicalCount
	ids := make([]uuid.UUID, 0, capacity)
	ids = append(ids,
		authority.Scope.TournamentID, authority.Scope.PlanSetID, authority.Source.ProjectionID,
		authority.Revisions.SourceProjectionRevisionID.UUID(), authority.Revisions.GroupSetRevisionID,
		authority.Revisions.PoolRevisionID, authority.Revisions.HistoryRevisionID,
		authority.Revisions.TaskHealthRevisionID, authority.Revisions.ArtifactRevisionID,
		authority.Revisions.ReservationRevisionID, authority.Revisions.MembershipRevisionID,
	)
	if authority.Source.PreviousRevisionID != nil {
		ids = append(ids, authority.Source.PreviousRevisionID.UUID())
	}
	for _, group := range authority.Groups {
		ids = append(ids, group.Revision.GroupID(), group.Revision.RevisionID().UUID())
		if previousRevisionID := group.Revision.PreviousRevisionID(); previousRevisionID != nil {
			ids = append(ids, previousRevisionID.UUID())
		}
	}
	for _, candidate := range authority.Candidates {
		ids = append(ids, candidate.Task.ID)
	}
	for _, reservation := range authority.ParticipantReservations {
		ids = append(ids, reservation.ParticipantID, reservation.PlayerID, reservation.Reservation.ReservationID)
	}
	for _, reservation := range authority.ExistingTaskReservations {
		ids = append(ids, reservation.ReservationID)
	}
	return ids
}

func validateGoldenCommandAuthorityAliases(
	command GoldenExactPlanCommand,
	authority GoldenExactPlanAuthority,
) error {
	seen := make(map[uuid.UUID]struct{})
	for _, id := range goldenAuthorityBoundIDs(authority) {
		seen[id] = struct{}{}
	}
	for _, reservation := range authority.ExistingTaskReservations {
		seen[reservation.PlanID] = struct{}{}
		seen[reservation.PlanRevisionID] = struct{}{}
	}
	stable := []uuid.UUID{command.PlanID, command.PlanRevisionID}
	for _, group := range command.GroupCommands {
		for index := range group.EdgeIDs {
			stable = append(stable, group.EdgeIDs[index], group.ReservationIDs[index], group.SnapshotIDs[index])
		}
	}
	for _, id := range stable {
		if _, alias := seen[id]; alias {
			return goldenExactPlanError("stable plan identity aliases retained authority")
		}
		seen[id] = struct{}{}
	}
	return nil
}

func validGoldenExactPlanScope(scope GoldenExactPlanScope) bool {
	return scope.TournamentID != uuid.Nil && scope.PlanSetID != uuid.Nil && scope.TournamentID != scope.PlanSetID
}

func validGoldenExactPlanRevisions(r GoldenExactPlanRevisions) bool {
	return !r.SourceProjectionRevisionID.IsZero() &&
		r.GroupSetRevisionID != uuid.Nil && r.GroupSetRevision >= 1 &&
		r.PoolRevisionID != uuid.Nil && r.PoolRevision >= 1 &&
		r.HistoryRevisionID != uuid.Nil && r.HistoryRevision >= 1 &&
		r.TaskHealthRevisionID != uuid.Nil && r.TaskHealthRevision >= 1 &&
		r.ArtifactRevisionID != uuid.Nil && r.ArtifactRevision >= 1 &&
		r.ReservationRevisionID != uuid.Nil && r.ReservationRevision >= 1 &&
		r.MembershipRevisionID != uuid.Nil && r.MembershipRevision >= 1
}

func validateGoldenRevisionIdentitySet(scope GoldenExactPlanScope, revisions GoldenExactPlanRevisions) error {
	ids := []uuid.UUID{
		scope.TournamentID, scope.PlanSetID, revisions.SourceProjectionRevisionID.UUID(),
		revisions.GroupSetRevisionID, revisions.PoolRevisionID, revisions.HistoryRevisionID,
		revisions.TaskHealthRevisionID, revisions.ArtifactRevisionID,
		revisions.ReservationRevisionID, revisions.MembershipRevisionID,
	}
	seen := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		if _, duplicate := seen[id]; duplicate {
			return goldenExactPlanError("authority revision identity is reused")
		}
		seen[id] = struct{}{}
	}
	return nil
}

func validateGoldenAuthorityAliases(
	scope GoldenExactPlanScope,
	revisions GoldenExactPlanRevisions,
	source GoldenStandingsProjection,
	groups []GoldenPlanGroupAuthority,
) error {
	ids := make([]uuid.UUID, 0, 11+2*len(groups))
	ids = append(ids,
		scope.TournamentID, scope.PlanSetID, source.ProjectionID,
		revisions.SourceProjectionRevisionID.UUID(), revisions.GroupSetRevisionID,
		revisions.PoolRevisionID, revisions.HistoryRevisionID, revisions.TaskHealthRevisionID,
		revisions.ArtifactRevisionID, revisions.ReservationRevisionID, revisions.MembershipRevisionID,
	)
	for _, group := range groups {
		ids = append(ids, group.Revision.GroupID(), group.Revision.RevisionID().UUID())
	}
	seen := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		if id == uuid.Nil {
			return goldenExactPlanError("missing authority identity")
		}
		if _, duplicate := seen[id]; duplicate {
			return goldenExactPlanError("authority identity is aliased")
		}
		seen[id] = struct{}{}
	}
	return nil
}

func normalizeGoldenPlanGroups(
	source GoldenStandingsProjection,
	input []GoldenPlanGroupAuthority,
) ([]GoldenPlanGroupAuthority, []uuid.UUID, error) {
	partition, err := PartitionGoldenTies(source)
	if err != nil {
		return nil, nil, goldenExactPlanError("partition: %v", err)
	}
	groups := canonicalGoldenPlanGroups(input)
	if len(groups) == 0 {
		return nil, nil, goldenExactPlanError("active group set is empty")
	}
	state := newGoldenPlanGroupNormalization(source, partition.GoldenGroups(), len(groups))
	for _, group := range groups {
		if err := state.add(group); err != nil {
			return nil, nil, err
		}
	}
	sort.Slice(state.participants, func(i, j int) bool {
		return bytes.Compare(state.participants[i][:], state.participants[j][:]) < 0
	})
	return groups, state.participants, nil
}

func canonicalGoldenPlanGroups(input []GoldenPlanGroupAuthority) []GoldenPlanGroupAuthority {
	groups := make([]GoldenPlanGroupAuthority, len(input))
	for index, group := range input {
		groups[index] = GoldenPlanGroupAuthority{
			Revision:             group.Revision.Snapshot(),
			ActiveParticipantIDs: append([]uuid.UUID(nil), group.ActiveParticipantIDs...),
		}
		sort.Slice(groups[index].ActiveParticipantIDs, func(i, j int) bool {
			return bytes.Compare(groups[index].ActiveParticipantIDs[i][:], groups[index].ActiveParticipantIDs[j][:]) < 0
		})
	}
	sort.Slice(groups, func(i, j int) bool {
		left, _ := groups[i].Revision.Positions()
		right, _ := groups[j].Revision.Positions()
		return left < right
	})
	return groups
}

type goldenPlanGroupNormalization struct {
	source           GoldenStandingsProjection
	seeds            []GoldenTieGroupSeed
	participants     []uuid.UUID
	seenGroups       map[uuid.UUID]struct{}
	seenRevisions    map[domain.ArenaDerivedRevisionID]struct{}
	seenParticipants map[uuid.UUID]struct{}
	matchedSeeds     map[int]struct{}
	lastPosition     int
}

func newGoldenPlanGroupNormalization(
	source GoldenStandingsProjection,
	seeds []GoldenTieGroupSeed,
	groupCount int,
) *goldenPlanGroupNormalization {
	return &goldenPlanGroupNormalization{
		source: source, seeds: seeds,
		participants:     make([]uuid.UUID, 0, len(source.Standings)),
		seenGroups:       make(map[uuid.UUID]struct{}, groupCount),
		seenRevisions:    make(map[domain.ArenaDerivedRevisionID]struct{}, groupCount),
		seenParticipants: make(map[uuid.UUID]struct{}, len(source.Standings)),
		matchedSeeds:     make(map[int]struct{}, groupCount),
	}
}

func (state *goldenPlanGroupNormalization) add(group GoldenPlanGroupAuthority) error {
	if err := validateGoldenPlanGroupSource(state.source, group); err != nil {
		return err
	}
	if err := state.claimTopology(group); err != nil {
		return err
	}
	return state.collectActiveParticipants(group)
}

func validateGoldenPlanGroupSource(
	source GoldenStandingsProjection,
	group GoldenPlanGroupAuthority,
) error {
	if group.Revision.Validate() != nil || group.Revision.TournamentID() != source.TournamentID ||
		group.Revision.SourceProjectionID() != source.ProjectionID ||
		group.Revision.SourceProjectionRevisionID() != source.RevisionID ||
		group.Revision.SourceProjectionRevisionNo() != source.RevisionNo ||
		!equalGoldenPlanRevisionPointers(
			group.Revision.SourceProjectionPreviousRevisionID(),
			source.PreviousRevisionID,
		) ||
		group.Revision.SourceProjectionPayloadDigest() != source.PayloadDigest {
		return goldenExactPlanError("group revision does not match source")
	}
	return nil
}

func equalGoldenPlanRevisionPointers(
	first *domain.ArenaDerivedRevisionID,
	second *domain.ArenaDerivedRevisionID,
) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}

func (state *goldenPlanGroupNormalization) claimTopology(group GoldenPlanGroupAuthority) error {
	from, to := group.Revision.Positions()
	seedIndex := goldenPlanGroupSeedIndex(group.Revision, state.seeds)
	if from <= state.lastPosition || seedIndex < 0 {
		return goldenExactPlanError("group range or seed is not current")
	}
	if _, duplicate := state.matchedSeeds[seedIndex]; duplicate {
		return goldenExactPlanError("current Golden topology seed is reused")
	}
	if _, duplicate := state.seenGroups[group.Revision.GroupID()]; duplicate {
		return goldenExactPlanError("duplicate active group")
	}
	if _, duplicate := state.seenRevisions[group.Revision.RevisionID()]; duplicate {
		return goldenExactPlanError("duplicate active group revision")
	}
	state.matchedSeeds[seedIndex] = struct{}{}
	state.seenGroups[group.Revision.GroupID()] = struct{}{}
	state.seenRevisions[group.Revision.RevisionID()] = struct{}{}
	state.lastPosition = to
	return nil
}

func goldenPlanGroupSeedIndex(revision GoldenGroupRevision, seeds []GoldenTieGroupSeed) int {
	from, to := revision.Positions()
	members := revision.Members()
	for index, seed := range seeds {
		if from == seed.PositionFrom && to == seed.PositionTo && reflect.DeepEqual(members, seed.Members) {
			return index
		}
	}
	return -1
}

func (state *goldenPlanGroupNormalization) collectActiveParticipants(group GoldenPlanGroupAuthority) error {
	members := group.Revision.Members()
	memberSet := make(map[uuid.UUID]struct{}, len(members))
	for _, member := range members {
		memberSet[member.ParticipantID] = struct{}{}
	}
	if len(group.ActiveParticipantIDs) < 2 {
		return goldenExactPlanError("active group has fewer than two members")
	}
	for index, participantID := range group.ActiveParticipantIDs {
		if participantID == uuid.Nil || (index > 0 && participantID == group.ActiveParticipantIDs[index-1]) {
			return goldenExactPlanError("active membership is duplicate")
		}
		if _, belongs := memberSet[participantID]; !belongs {
			return goldenExactPlanError("active membership contains a foreign participant")
		}
		if _, duplicate := state.seenParticipants[participantID]; duplicate {
			return goldenExactPlanError("participant belongs to multiple active groups")
		}
		state.seenParticipants[participantID] = struct{}{}
		state.participants = append(state.participants, participantID)
	}
	return nil
}

func normalizeGoldenExactCandidates(pool TaskPoolRevision, input []GoldenExactTaskVersion) ([]GoldenExactTaskVersion, error) {
	result := cloneGoldenCandidates(input)
	sort.Slice(result, func(i, j int) bool {
		return taskVersionRefLess(
			TaskVersionRef{TaskID: result[i].Task.ID, Version: result[i].Version},
			TaskVersionRef{TaskID: result[j].Task.ID, Version: result[j].Version},
		)
	})
	if len(result) != len(pool.Versions) {
		return nil, goldenExactPlanError("candidate inventory does not cover the Golden pool")
	}
	for index, candidate := range result {
		candidate.Health.InternalHealthDetail = ""
		result[index].Health.InternalHealthDetail = ""
		ref := TaskVersionRef{TaskID: candidate.Task.ID, Version: candidate.Version}
		if ref != pool.Versions[index] || candidate.PoolRevisionID != pool.ID || candidate.Version < 1 ||
			!validTaskEligibilityContent(candidate.Task) || candidate.Health.TaskID != candidate.Task.ID ||
			candidate.Health.Version != candidate.Version || candidate.Health.PoolRevisionID != pool.ID ||
			candidate.Health.PoolKind != domain.ArenaTaskKindGolden ||
			candidate.ArtifactDigest == [sha256.Size]byte{} ||
			candidate.ArtifactDigest != GoldenTaskArtifactDigest(candidate.Task, candidate.Version) {
			return nil, goldenExactPlanError("candidate pool, health, or artifact evidence changed")
		}
	}
	return result, nil
}

func normalizeGoldenHistory(participants []uuid.UUID, input []ArenaTaskReceiptRef) ([]ArenaTaskReceiptRef, error) {
	participantSet := make(map[uuid.UUID]struct{}, len(participants))
	for _, participantID := range participants {
		participantSet[participantID] = struct{}{}
	}
	result := append([]ArenaTaskReceiptRef(nil), input...)
	sort.Slice(result, func(i, j int) bool {
		if comparison := bytes.Compare(result[i].ParticipantID[:], result[j].ParticipantID[:]); comparison != 0 {
			return comparison < 0
		}
		if comparison := bytes.Compare(result[i].TaskID[:], result[j].TaskID[:]); comparison != 0 {
			return comparison < 0
		}
		return result[i].Version < result[j].Version
	})
	for index, receipt := range result {
		if receipt.TaskID == uuid.Nil || receipt.Version < 1 {
			return nil, goldenExactPlanError("invalid Arena history evidence")
		}
		if _, belongs := participantSet[receipt.ParticipantID]; !belongs {
			return nil, goldenExactPlanError("Arena history contains a foreign participant")
		}
		if index > 0 && result[index-1].ParticipantID == receipt.ParticipantID &&
			result[index-1].TaskID == receipt.TaskID {
			return nil, goldenExactPlanError("duplicate Arena history evidence")
		}
	}
	return result, nil
}

func normalizeGoldenParticipantReservations(
	scope GoldenExactPlanScope,
	participants []uuid.UUID,
	input []GoldenParticipantReservation,
) ([]GoldenParticipantReservation, error) {
	result := append([]GoldenParticipantReservation(nil), input...)
	sort.Slice(result, func(i, j int) bool {
		return bytes.Compare(result[i].ParticipantID[:], result[j].ParticipantID[:]) < 0
	})
	if len(result) != len(participants) {
		return nil, goldenExactPlanError("participant reservations do not cover active membership")
	}
	seenReservations := make(map[uuid.UUID]struct{}, len(result))
	seenPlayers := make(map[uuid.UUID]struct{}, len(result))
	for index, item := range result {
		if item.ParticipantID != participants[index] || item.PlayerID == uuid.Nil ||
			item.Reservation.PlayerID != item.PlayerID || !item.Reservation.IsValid() ||
			item.Reservation.OwnerKind != domain.ParticipantReservationOwnerArena ||
			item.Reservation.OwnerID != scope.TournamentID {
			return nil, goldenExactPlanError("invalid participant reservation authority")
		}
		if _, duplicate := seenReservations[item.Reservation.ReservationID]; duplicate {
			return nil, goldenExactPlanError("participant reservation identity is reused")
		}
		if _, duplicate := seenPlayers[item.PlayerID]; duplicate {
			return nil, goldenExactPlanError("participant player identity is reused")
		}
		seenReservations[item.Reservation.ReservationID] = struct{}{}
		seenPlayers[item.PlayerID] = struct{}{}
	}
	return result, nil
}

func normalizeGoldenTaskReservations(pool TaskPoolRevision, input []GoldenTaskReservation) ([]GoldenTaskReservation, error) {
	result := append([]GoldenTaskReservation(nil), input...)
	sort.Slice(result, func(i, j int) bool { return taskVersionRefLess(result[i].TaskVersion, result[j].TaskVersion) })
	seenIDs := make(map[uuid.UUID]struct{}, len(result))
	ownerRevisions := make(map[uuid.UUID]uuid.UUID, len(result))
	for index, reservation := range result {
		if !poolContainsTaskVersion(pool, reservation.TaskVersion.TaskID, reservation.TaskVersion.Version) ||
			reservation.ReservationID == uuid.Nil || reservation.PlanID == uuid.Nil || reservation.PlanRevisionID == uuid.Nil ||
			reservation.ReservationID == reservation.PlanID || reservation.ReservationID == reservation.PlanRevisionID ||
			reservation.PlanID == reservation.PlanRevisionID {
			return nil, goldenExactPlanError("invalid existing task reservation")
		}
		if index > 0 && result[index-1].TaskVersion == reservation.TaskVersion {
			return nil, goldenExactPlanError("task version is globally reserved twice")
		}
		if _, duplicate := seenIDs[reservation.ReservationID]; duplicate {
			return nil, goldenExactPlanError("task reservation identity is reused")
		}
		if revisionID, exists := ownerRevisions[reservation.PlanID]; exists && revisionID != reservation.PlanRevisionID {
			return nil, goldenExactPlanError("task reservations disagree on owner revision")
		}
		seenIDs[reservation.ReservationID] = struct{}{}
		ownerRevisions[reservation.PlanID] = reservation.PlanRevisionID
	}
	return result, nil
}

func goldenAuthorityEvidence(authority GoldenExactPlanAuthority) (GoldenExactPlanExpectation, error) {
	groupDocument := make([]any, len(authority.Groups))
	membershipDocument := make([]any, len(authority.Groups))
	for index, group := range authority.Groups {
		groupDocument[index] = []any{group.Revision.ProofHash(), group.ActiveParticipantIDs}
		membershipDocument[index] = []any{group.Revision.GroupID(), group.Revision.RevisionID().UUID(), group.ActiveParticipantIDs}
	}
	healthDocument := make([]TaskVersionHealth, len(authority.Candidates))
	artifactDocument := make([]any, len(authority.Candidates))
	for index, candidate := range authority.Candidates {
		health := candidate.Health
		health.InternalHealthDetail = ""
		healthDocument[index] = health
		artifactDocument[index] = []any{candidate.Task.ID, candidate.Version, candidate.ArtifactDigest}
	}
	reservationDocument := struct {
		Participants []GoldenParticipantReservation
		Tasks        []GoldenTaskReservation
	}{authority.ParticipantReservations, authority.ExistingTaskReservations}
	groupDigest, err := goldenHashDocument(groupDocument)
	if err != nil {
		return GoldenExactPlanExpectation{}, err
	}
	poolDigest, err := goldenHashDocument(authority.Pool)
	if err != nil {
		return GoldenExactPlanExpectation{}, err
	}
	historyDigest, err := goldenHashDocument(authority.History)
	if err != nil {
		return GoldenExactPlanExpectation{}, err
	}
	healthDigest, err := goldenHashDocument(healthDocument)
	if err != nil {
		return GoldenExactPlanExpectation{}, err
	}
	artifactDigest, err := goldenHashDocument(artifactDocument)
	if err != nil {
		return GoldenExactPlanExpectation{}, err
	}
	reservationDigest, err := goldenHashDocument(reservationDocument)
	if err != nil {
		return GoldenExactPlanExpectation{}, err
	}
	membershipDigest, err := goldenHashDocument(membershipDocument)
	if err != nil {
		return GoldenExactPlanExpectation{}, err
	}
	return GoldenExactPlanExpectation{
		Revisions: authority.Revisions, SourcePayloadDigest: authority.Source.PayloadDigest,
		GroupDigest: groupDigest, PoolDigest: poolDigest, HistoryDigest: historyDigest,
		TaskHealthDigest: healthDigest, ArtifactDigest: artifactDigest,
		ReservationDigest: reservationDigest, MembershipDigest: membershipDigest,
	}, nil
}

func validateGoldenExactPlanCommandIdentity(command GoldenExactPlanCommand) error {
	if err := validateGoldenExactPlanCommandBase(command); err != nil {
		return err
	}
	boundIDs := goldenExactPlanCommandBoundIDs(command)
	seenStable := make(map[uuid.UUID]struct{}, len(boundIDs)+len(command.GroupCommands)*11)
	if err := addGoldenCommandStableIDs(seenStable, boundIDs, "bound command identity"); err != nil {
		return err
	}
	seenGroups := make(map[uuid.UUID]struct{}, len(command.GroupCommands))
	for _, group := range command.GroupCommands {
		if err := validateGoldenExactGroupCommandIdentity(group, seenGroups, seenStable); err != nil {
			return err
		}
	}
	return nil
}

func validateGoldenExactPlanCommandBase(command GoldenExactPlanCommand) error {
	if !validGoldenExactPlanScope(command.Scope) || command.PlanID == uuid.Nil ||
		command.PlanRevisionID == uuid.Nil || command.PlanID == command.PlanRevisionID ||
		command.CreatedAt.IsZero() || command.CreatedAt.Location() != time.UTC ||
		!validGoldenExactPlanRevisions(command.Expected.Revisions) ||
		goldenExpectationHasZeroDigest(command.Expected) || len(command.GroupCommands) == 0 {
		return goldenExactPlanError("invalid command identity or expectation")
	}
	return nil
}

func goldenExactPlanCommandBoundIDs(command GoldenExactPlanCommand) []uuid.UUID {
	return []uuid.UUID{
		command.Scope.TournamentID, command.Scope.PlanSetID, command.PlanID, command.PlanRevisionID,
		command.Expected.Revisions.SourceProjectionRevisionID.UUID(),
		command.Expected.Revisions.GroupSetRevisionID, command.Expected.Revisions.PoolRevisionID,
		command.Expected.Revisions.HistoryRevisionID, command.Expected.Revisions.TaskHealthRevisionID,
		command.Expected.Revisions.ArtifactRevisionID, command.Expected.Revisions.ReservationRevisionID,
		command.Expected.Revisions.MembershipRevisionID,
	}
}

func validateGoldenExactGroupCommandIdentity(
	group GoldenExactGroupCommand,
	seenGroups map[uuid.UUID]struct{},
	seenStable map[uuid.UUID]struct{},
) error {
	if group.GroupID == uuid.Nil || group.GroupRevisionID.IsZero() {
		return goldenExactPlanError("missing group command identity")
	}
	if _, duplicate := seenGroups[group.GroupID]; duplicate {
		return goldenExactPlanError("duplicate group command")
	}
	seenGroups[group.GroupID] = struct{}{}
	if err := addGoldenCommandStableIDs(
		seenStable,
		[]uuid.UUID{group.GroupID, group.GroupRevisionID.UUID()},
		"group command identity",
	); err != nil {
		return err
	}
	for index := range group.EdgeIDs {
		if err := addGoldenCommandStableIDs(
			seenStable,
			[]uuid.UUID{group.EdgeIDs[index], group.ReservationIDs[index], group.SnapshotIDs[index]},
			"stable edge identity",
		); err != nil {
			return err
		}
	}
	return nil
}

func addGoldenCommandStableIDs(
	seen map[uuid.UUID]struct{},
	ids []uuid.UUID,
	kind string,
) error {
	for _, id := range ids {
		if id == uuid.Nil {
			return goldenExactPlanError("missing %s", kind)
		}
		if _, duplicate := seen[id]; duplicate {
			return goldenExactPlanError("%s is reused", kind)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func goldenExpectationHasZeroDigest(expected GoldenExactPlanExpectation) bool {
	zero := [sha256.Size]byte{}
	return expected.SourcePayloadDigest == zero || expected.GroupDigest == zero ||
		expected.PoolDigest == zero || expected.HistoryDigest == zero ||
		expected.TaskHealthDigest == zero || expected.ArtifactDigest == zero ||
		expected.ReservationDigest == zero || expected.MembershipDigest == zero
}

func canonicalGoldenGroupCommands(
	input []GoldenExactGroupCommand,
	groups []GoldenPlanGroupAuthority,
) ([]GoldenExactGroupCommand, error) {
	byID := make(map[uuid.UUID]GoldenExactGroupCommand, len(input))
	for _, command := range input {
		byID[command.GroupID] = command
	}
	if len(byID) != len(groups) {
		return nil, goldenExactPlanError("commands do not cover every active group")
	}
	result := make([]GoldenExactGroupCommand, len(groups))
	for index, group := range groups {
		command, exists := byID[group.Revision.GroupID()]
		if !exists || command.GroupRevisionID != group.Revision.RevisionID() {
			return nil, goldenExactPlanError("group command is stale or missing")
		}
		result[index] = command
	}
	return result, nil
}

func goldenExactMatching(authority GoldenExactPlanAuthority) ([][]int, error) {
	demandCount := len(authority.Groups) * (domain.ArenaAssignmentReserveCount + 1)
	assignedCandidate := make([]int, demandCount)
	assignedDemand := make([]int, len(authority.Candidates))
	for index := range assignedCandidate {
		assignedCandidate[index] = -1
	}
	for index := range assignedDemand {
		assignedDemand[index] = -1
	}
	var assign func(int, []bool) bool
	assign = func(demand int, seen []bool) bool {
		groupIndex := demand / (domain.ArenaAssignmentReserveCount + 1)
		for candidateIndex := range authority.Candidates {
			if seen[candidateIndex] || !goldenCandidateEligibleForGroup(authority, groupIndex, candidateIndex) {
				continue
			}
			seen[candidateIndex] = true
			previousDemand := assignedDemand[candidateIndex]
			if previousDemand == -1 || assign(previousDemand, seen) {
				assignedDemand[candidateIndex] = demand
				assignedCandidate[demand] = candidateIndex
				return true
			}
		}
		return false
	}
	for demand := 0; demand < demandCount; demand++ {
		if !assign(demand, make([]bool, len(authority.Candidates))) {
			return nil, fmt.Errorf("%w: no complete primary and reserve matching", ErrGoldenExactPlanInsufficient)
		}
	}
	result := make([][]int, len(authority.Groups))
	for groupIndex := range result {
		result[groupIndex] = append([]int(nil), assignedCandidate[groupIndex*(domain.ArenaAssignmentReserveCount+1):(groupIndex+1)*(domain.ArenaAssignmentReserveCount+1)]...)
	}
	return result, nil
}

func goldenCandidateEligibleForGroup(authority GoldenExactPlanAuthority, groupIndex, candidateIndex int) bool {
	candidate := authority.Candidates[candidateIndex]
	health := candidate.Health
	if !health.Exists || !health.Enabled || !health.Healthy || !health.MutationLocked || health.PubliclyExposed {
		return false
	}
	ref := TaskVersionRef{TaskID: candidate.Task.ID, Version: candidate.Version}
	for _, reservation := range authority.ExistingTaskReservations {
		if reservation.TaskVersion == ref {
			return false
		}
	}
	participants := make(map[uuid.UUID]struct{}, len(authority.Groups[groupIndex].ActiveParticipantIDs))
	for _, participantID := range authority.Groups[groupIndex].ActiveParticipantIDs {
		participants[participantID] = struct{}{}
	}
	for _, receipt := range authority.History {
		if receipt.TaskID == candidate.Task.ID {
			if _, belongs := participants[receipt.ParticipantID]; belongs {
				return false
			}
		}
	}
	return true
}

func goldenCandidateIndex(candidates []GoldenExactTaskVersion, ref TaskVersionRef) int {
	for index, candidate := range candidates {
		if candidate.Task.ID == ref.TaskID && candidate.Version == ref.Version {
			return index
		}
	}
	return -1
}

type goldenExactPlanProofDocument struct {
	Scope          GoldenExactPlanScope       `json:"scope"`
	PlanID         uuid.UUID                  `json:"plan_id"`
	PlanRevisionID uuid.UUID                  `json:"plan_revision_id"`
	Expected       GoldenExactPlanExpectation `json:"expected"`
	Groups         []goldenExactGroupProof    `json:"groups"`
	CreatedAt      time.Time                  `json:"created_at"`
}

type goldenExactGroupProof struct {
	GroupID                    uuid.UUID                  `json:"group_id"`
	GroupRevisionID            uuid.UUID                  `json:"group_revision_id"`
	SourceProjectionRevisionID uuid.UUID                  `json:"source_projection_revision_id"`
	PositionFrom               int                        `json:"position_from"`
	PositionTo                 int                        `json:"position_to"`
	ParticipantIDs             []uuid.UUID                `json:"participant_ids"`
	Edges                      []goldenExactPlanEdgeProof `json:"edges"`
}

type goldenExactPlanEdgeProof struct {
	ID            uuid.UUID         `json:"id"`
	ReservationID uuid.UUID         `json:"reservation_id"`
	Position      int               `json:"position"`
	SnapshotID    uuid.UUID         `json:"snapshot_id"`
	TaskID        uuid.UUID         `json:"task_id"`
	Version       int               `json:"version"`
	ContentDigest [sha256.Size]byte `json:"content_digest"`
}

func goldenExactPlanProofHash(plan GoldenExactPlan) (string, error) {
	document := goldenExactPlanProofDocument{
		Scope: plan.Scope, PlanID: plan.PlanID, PlanRevisionID: plan.PlanRevisionID,
		Expected: plan.Expected, Groups: make([]goldenExactGroupProof, len(plan.Groups)), CreatedAt: plan.CreatedAt,
	}
	for groupIndex, group := range plan.Groups {
		proof := goldenExactGroupProof{
			GroupID: group.GroupID, GroupRevisionID: group.GroupRevisionID.UUID(),
			SourceProjectionRevisionID: group.SourceProjectionRevisionID.UUID(),
			PositionFrom:               group.PositionFrom, PositionTo: group.PositionTo,
			ParticipantIDs: append([]uuid.UUID(nil), group.ParticipantIDs...),
			Edges:          make([]goldenExactPlanEdgeProof, len(group.Edges)),
		}
		for edgeIndex, edge := range group.Edges {
			proof.Edges[edgeIndex] = goldenExactPlanEdgeProof{
				ID: edge.ID, ReservationID: edge.ReservationID, Position: edge.Position,
				SnapshotID: edge.Snapshot.SnapshotID, TaskID: edge.Snapshot.TaskID,
				Version: edge.Snapshot.Version, ContentDigest: edge.ContentDigest,
			}
		}
		document.Groups[groupIndex] = proof
	}
	payload, err := json.Marshal(document)
	if err != nil {
		return "", goldenExactPlanError("encode proof: %v", err)
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func goldenHashDocument(value any) ([sha256.Size]byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return [sha256.Size]byte{}, goldenExactPlanError("encode authority evidence: %v", err)
	}
	return sha256.Sum256(payload), nil
}

func cloneGoldenCandidates(input []GoldenExactTaskVersion) []GoldenExactTaskVersion {
	result := append([]GoldenExactTaskVersion(nil), input...)
	for index := range result {
		result[index].Task = *taskexec.CloneTask(&input[index].Task)
	}
	return result
}

func goldenExactPlanError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidGoldenExactPlan, fmt.Sprintf(format, arguments...))
}
