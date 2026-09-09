package golden

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/taskexec"
)

const goldenExactPlanAttempts = 2

var (
	ErrInvalidExactPlan      = errors.New("invalid Golden exact plan")
	ErrExactPlanStale        = errors.New("golden exact plan authority is stale")
	ErrExactPlanConflict     = errors.New("golden exact plan conflict")
	ErrExactPlanInsufficient = errors.New("insufficient Golden exact matching")
)

type UseCase struct {
	repository PlanRepository
}

func NewUseCase(repository PlanRepository) *UseCase {
	return &UseCase{repository: repository}
}

func (u *UseCase) PlanAndCommit(
	ctx context.Context,
	command Command,
) (*ExactPlan, bool, error) {
	if u == nil || u.repository == nil {
		return nil, false, domain.ErrValidation
	}
	command.GroupCommands = append([]GroupCommand(nil), command.GroupCommands...)
	if err := validateGoldenExactPlanCommandIdentity(command); err != nil {
		return nil, false, err
	}
	stored, err := u.repository.Get(ctx, command.Scope, command.PlanID)
	if err != nil {
		return nil, false, fmt.Errorf("PlanUseCase - get stored plan: %w", err)
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
	return nil, false, ErrExactPlanConflict
}

func (u *UseCase) planAndCommitAttempt(
	ctx context.Context,
	command Command,
) (*ExactPlan, bool, bool, error) {
	authority, err := u.repository.LoadAuthority(ctx, command.Scope)
	if err != nil {
		return nil, false, false, fmt.Errorf("PlanUseCase - load authority: %w", err)
	}
	plan, err := BuildExactPlan(command, authority)
	if err != nil {
		return nil, false, false, err
	}
	committed, changed, err := u.repository.Commit(ctx, plan)
	if errors.Is(err, domain.ErrConflict) {
		stored, getErr := u.repository.Get(ctx, command.Scope, command.PlanID)
		if getErr != nil {
			return nil, false, false, fmt.Errorf("PlanUseCase - reconcile conflict: %w", getErr)
		}
		if stored != nil {
			result, storedChanged, reconcileErr := reconcileGoldenExactPlan(command, stored)
			return result, storedChanged, false, reconcileErr
		}
		return nil, false, true, nil
	}
	if err != nil {
		return nil, false, false, fmt.Errorf("PlanUseCase - commit plan: %w", err)
	}
	result, err := validateCommittedGoldenExactPlan(plan, committed)
	return result, changed, false, err
}

func validateCommittedGoldenExactPlan(
	plan ExactPlan,
	committed *ExactPlan,
) (*ExactPlan, error) {
	if committed == nil {
		return nil, domain.ErrInternal
	}
	if committed.Scope != plan.Scope || committed.PlanID != plan.PlanID ||
		committed.PlanRevisionID != plan.PlanRevisionID || !committed.CreatedAt.Equal(plan.CreatedAt) {
		return nil, ErrExactPlanConflict
	}
	if err := committed.Validate(); err != nil {
		return nil, domain.ErrInternal
	}
	if committed.ProofHash != plan.ProofHash {
		return nil, ErrExactPlanConflict
	}
	result := committed.Snapshot()
	return &result, nil
}

func reconcileGoldenExactPlan(
	command Command,
	stored *ExactPlan,
) (*ExactPlan, bool, error) {
	if stored == nil || stored.Scope != command.Scope || stored.PlanID != command.PlanID {
		return nil, false, domain.ErrInternal
	}
	if err := stored.Validate(); err != nil {
		return nil, false, domain.ErrInternal
	}
	if !goldenExactPlanMatchesCommand(*stored, command) {
		return nil, false, ErrExactPlanConflict
	}
	result := stored.Snapshot()
	return &result, false, nil
}

func goldenExactPlanMatchesCommand(plan ExactPlan, command Command) bool {
	if !goldenExactPlanBaseMatchesCommand(plan, command) {
		return false
	}
	byGroup := make(map[uuid.UUID]GroupCommand, len(command.GroupCommands))
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

func goldenExactPlanBaseMatchesCommand(plan ExactPlan, command Command) bool {
	return plan.Scope == command.Scope && plan.PlanID == command.PlanID &&
		plan.PlanRevisionID == command.PlanRevisionID && plan.Expected == command.Expected &&
		plan.CreatedAt.Equal(command.CreatedAt) && len(plan.Groups) == len(command.GroupCommands)
}

func goldenExactGroupMatchesCommand(group Group, command GroupCommand) bool {
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

func BuildExactPlan(
	command Command,
	authority Authority,
) (ExactPlan, error) {
	if err := validateGoldenExactPlanCommandIdentity(command); err != nil {
		return ExactPlan{}, err
	}
	canonical, evidence, err := normalizeGoldenExactPlanAuthority(authority)
	if err != nil {
		return ExactPlan{}, err
	}
	canonical.Evidence = evidence
	if command.Scope != canonical.Scope || command.Expected != canonical.Expectation() {
		return ExactPlan{}, fmt.Errorf("%w: command expectation does not match current authority", ErrExactPlanStale)
	}
	if err := validateGoldenCommandAuthorityAliases(command, canonical); err != nil {
		return ExactPlan{}, err
	}
	commands, err := canonicalGoldenGroupCommands(command.GroupCommands, canonical.Groups)
	if err != nil {
		return ExactPlan{}, err
	}
	matching, err := goldenExactMatching(canonical)
	if err != nil {
		return ExactPlan{}, err
	}
	plan := ExactPlan{
		Scope: command.Scope, PlanID: command.PlanID, PlanRevisionID: command.PlanRevisionID,
		Expected: command.Expected, Authority: canonical.Snapshot(), CreatedAt: command.CreatedAt,
		Groups: make([]Group, len(canonical.Groups)),
	}
	for groupIndex, group := range canonical.Groups {
		from, to := group.Revision.Positions()
		groupPlan := Group{
			GroupID: group.Revision.GroupID(), GroupRevisionID: group.Revision.RevisionID(),
			SourceProjectionRevisionID: group.Revision.SourceProjectionRevisionID(),
			PositionFrom:               from, PositionTo: to,
			ParticipantIDs: append([]uuid.UUID(nil), group.ActiveParticipantIDs...),
			Edges:          make([]Edge, domain.AssignmentReserveCount+1),
		}
		for edgeIndex := range groupPlan.Edges {
			candidate := canonical.Candidates[matching[groupIndex][edgeIndex]]
			snapshot, buildErr := taskexec.BuildSnapshot(taskexec.SnapshotInput{
				SnapshotID: commands[groupIndex].SnapshotIDs[edgeIndex],
				Version:    candidate.Version, Kind: domain.AssignmentTaskKindGolden, Task: candidate.Task,
			})
			if buildErr != nil {
				return ExactPlan{}, goldenExactPlanError("snapshot: %v", buildErr)
			}
			digest, digestErr := taskexec.SnapshotDigest(snapshot)
			if digestErr != nil {
				return ExactPlan{}, goldenExactPlanError("snapshot digest: %v", digestErr)
			}
			groupPlan.Edges[edgeIndex] = Edge{
				ID:            commands[groupIndex].EdgeIDs[edgeIndex],
				ReservationID: commands[groupIndex].ReservationIDs[edgeIndex],
				Position:      edgeIndex + 1, Snapshot: snapshot, ContentDigest: digest,
			}
		}
		plan.Groups[groupIndex] = groupPlan
	}
	plan.ProofHash, err = goldenExactPlanProofHash(plan)
	if err != nil {
		return ExactPlan{}, err
	}
	if err := plan.Validate(); err != nil {
		return ExactPlan{}, err
	}
	return plan.Snapshot(), nil
}

func canonicalGoldenGroupCommands(
	input []GroupCommand,
	groups []GroupAuthority,
) ([]GroupCommand, error) {
	byID := make(map[uuid.UUID]GroupCommand, len(input))
	for _, command := range input {
		byID[command.GroupID] = command
	}
	if len(byID) != len(groups) {
		return nil, goldenExactPlanError("commands do not cover every active group")
	}
	result := make([]GroupCommand, len(groups))
	for index, group := range groups {
		command, exists := byID[group.Revision.GroupID()]
		if !exists || command.GroupRevisionID != group.Revision.RevisionID() {
			return nil, goldenExactPlanError("group command is stale or missing")
		}
		result[index] = command
	}
	return result, nil
}

func goldenExactPlanError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidExactPlan, fmt.Sprintf(format, arguments...))
}
