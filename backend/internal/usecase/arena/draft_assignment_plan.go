package arena

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const (
	exactDraftBranchPlanAttempts = 2
	exactDraftBranchPlanProofV1  = "exact-draft-branch-plan-v1"
)

type ExactDraftBranchPlanState string

const (
	ExactDraftBranchPlanStatePlanned   ExactDraftBranchPlanState = "planned"
	ExactDraftBranchPlanStateCommitted ExactDraftBranchPlanState = "committed"
)

type ExactDraftBranchState string

const (
	ExactDraftBranchStateReserved ExactDraftBranchState = "reserved"
	ExactDraftBranchStateActive   ExactDraftBranchState = "active"
	ExactDraftBranchStateReleased ExactDraftBranchState = "released"
)

type ExactDraftReservationState string

const (
	ExactDraftReservationStateReserved  ExactDraftReservationState = "reserved"
	ExactDraftReservationStateCommitted ExactDraftReservationState = "committed"
	ExactDraftReservationStateReleased  ExactDraftReservationState = "released"
)

var (
	ErrInvalidExactDraftBranchPlan  = errors.New("invalid exact draft branch plan")
	ErrExactDraftBranchPlanConflict = errors.New("exact draft branch plan conflict")
	ErrExactDraftBranchPlanNotFound = errors.New("exact draft branch plan not found")
)

type ExactDraftBranchAction struct {
	Turn     int
	ActorID  uuid.UUID
	Action   domain.ArenaDraftActionType
	Category domain.Category
}

type ExactDraftBranchPath struct {
	Key        string
	Actions    []ExactDraftBranchAction
	Categories []domain.Category
}

type ExactDraftBranchCommand struct {
	BranchID    uuid.UUID
	Key         string
	Assignments []ExactNormalAssignmentCommand
}

type ExactDraftBranchPlanCommand struct {
	PlanID                  uuid.UUID
	PlanRevisionID          uuid.UUID
	DraftID                 uuid.UUID
	ExpectedDraftRevisionID uuid.UUID
	ExpectedDraftRevision   int64
	Branches                []ExactDraftBranchCommand
	CreatedAt               time.Time
}

type ExactDraftBranchAuthority struct {
	Key         string
	Assignments []ExactNormalAssignmentAuthority
}

type ExactDraftBranchPlanAuthority struct {
	Draft    DraftExecution
	Branches []ExactDraftBranchAuthority
}

type ExactDraftBranchAssignment struct {
	Position       int
	Plan           ExactNormalAssignmentPlan
	State          ExactDraftReservationState
	TransitionedAt time.Time
}

type ExactDraftBranch struct {
	ID            uuid.UUID
	Path          ExactDraftBranchPath
	State         ExactDraftBranchState
	Assignments   []ExactDraftBranchAssignment
	ActivatedAt   time.Time
	ReleasedAt    time.Time
	ReleaseReason string
}

type ExactDraftBranchPlan struct {
	ID                        uuid.UUID
	RevisionID                uuid.UUID
	SourceDraft               DraftExecution
	State                     ExactDraftBranchPlanState
	Branches                  []ExactDraftBranch
	ActiveBranchID            uuid.UUID
	CompletionDraftRevisionID uuid.UUID
	CompletionDraftRevision   int64
	ActivationCommandID       uuid.UUID
	CompletedCategories       []domain.Category
	CreatedAt                 time.Time
	CommittedAt               time.Time
	ProofHash                 string
}

type ExactDraftBranchActivationCommand struct {
	PlanID                  uuid.UUID
	DraftID                 uuid.UUID
	ExpectedPlanRevisionID  uuid.UUID
	ExpectedDraftRevisionID uuid.UUID
	ExpectedDraftRevision   int64
	CommandID               uuid.UUID
	CommittedAt             time.Time
	ReleaseReason           string
}

// ExactDraftBranchPlanRepository owns the atomic source-revision checks and
// reservation writes for the full branch set. Activation commits one branch
// and releases every still-undisclosed alternative in the same transaction.
type ExactDraftBranchPlanRepository interface {
	LoadExactDraftBranchPlanAuthority(
		ctx context.Context,
		draftID uuid.UUID,
	) (ExactDraftBranchPlanAuthority, error)
	CommitExactDraftBranchPlan(
		ctx context.Context,
		plan ExactDraftBranchPlan,
	) (*ExactDraftBranchPlan, bool, error)
	LoadExactDraftBranchActivation(
		ctx context.Context,
		planID uuid.UUID,
	) (*ExactDraftBranchPlan, *DraftExecution, error)
	CommitExactDraftBranchActivation(
		ctx context.Context,
		plan ExactDraftBranchPlan,
	) (*ExactDraftBranchPlan, bool, error)
}

type ExactDraftBranchPlanUseCase struct {
	repository ExactDraftBranchPlanRepository
}

func NewExactDraftBranchPlanUseCase(
	repository ExactDraftBranchPlanRepository,
) *ExactDraftBranchPlanUseCase {
	return &ExactDraftBranchPlanUseCase{repository: repository}
}

func (u *ExactDraftBranchPlanUseCase) PlanAndCommit(
	ctx context.Context,
	command ExactDraftBranchPlanCommand,
) (*ExactDraftBranchPlan, bool, error) {
	if u == nil || u.repository == nil {
		return nil, false, domain.ErrValidation
	}
	if err := validateExactDraftBranchPlanCommand(command); err != nil {
		return nil, false, err
	}

	for range exactDraftBranchPlanAttempts {
		authority, err := u.repository.LoadExactDraftBranchPlanAuthority(ctx, command.DraftID)
		if err != nil {
			return nil, false, fmt.Errorf("ExactDraftBranchPlanUseCase - load authority: %w", err)
		}
		plan, err := BuildExactDraftBranchPlan(command, authority)
		if err != nil {
			return nil, false, err
		}
		committed, changed, err := u.repository.CommitExactDraftBranchPlan(ctx, plan)
		if errors.Is(err, domain.ErrConflict) {
			continue
		}
		if err != nil {
			return nil, false, fmt.Errorf("ExactDraftBranchPlanUseCase - commit plan: %w", err)
		}
		if committed == nil || committed.Validate() != nil ||
			!exactDraftPlanMatchesCommand(*committed, command) ||
			(changed && committed.ProofHash != plan.ProofHash) {
			return nil, false, domain.ErrInternal
		}
		result := cloneExactDraftBranchPlan(*committed)
		return &result, changed, nil
	}
	return nil, false, ErrExactDraftBranchPlanConflict
}

func (u *ExactDraftBranchPlanUseCase) ActivateCompletedBranch(
	ctx context.Context,
	command ExactDraftBranchActivationCommand,
) (*ExactDraftBranchPlan, bool, error) {
	if u == nil || u.repository == nil {
		return nil, false, domain.ErrValidation
	}
	command.ReleaseReason = strings.TrimSpace(command.ReleaseReason)
	if err := validateExactDraftBranchActivationCommand(command); err != nil {
		return nil, false, err
	}

	for range exactDraftBranchPlanAttempts {
		current, completed, err := u.repository.LoadExactDraftBranchActivation(ctx, command.PlanID)
		if err != nil {
			return nil, false, fmt.Errorf("ExactDraftBranchPlanUseCase - load activation: %w", err)
		}
		if current == nil || completed == nil {
			return nil, false, ErrExactDraftBranchPlanNotFound
		}
		if current.State == ExactDraftBranchPlanStateCommitted {
			result, err := reconcileExactDraftBranchActivation(*current, command)
			return result, false, err
		}
		next, err := activateExactDraftBranch(*current, *completed, command)
		if err != nil {
			return nil, false, err
		}
		committed, changed, err := u.repository.CommitExactDraftBranchActivation(ctx, next)
		if errors.Is(err, domain.ErrConflict) {
			continue
		}
		if err != nil {
			return nil, false, fmt.Errorf("ExactDraftBranchPlanUseCase - commit activation: %w", err)
		}
		if committed == nil || committed.Validate() != nil {
			return nil, false, domain.ErrInternal
		}
		result, reconcileErr := reconcileExactDraftBranchActivation(*committed, command)
		if reconcileErr != nil {
			return nil, false, domain.ErrInternal
		}
		return result, changed, nil
	}
	return nil, false, ErrExactDraftBranchPlanConflict
}

func ReachableExactDraftBranches(draft DraftExecution) ([]ExactDraftBranchPath, error) {
	if err := draft.Validate(); err != nil || draft.State == DraftExecutionStateSuperseded {
		return nil, exactDraftBranchPlanError("invalid source draft")
	}
	domainDraft, err := draft.domainDraft()
	if err != nil {
		return nil, exactDraftBranchPlanError("source draft snapshot: %v", err)
	}
	paths := make([]ExactDraftBranchPath, 0)
	if err := enumerateExactDraftBranches(domainDraft, &paths); err != nil {
		return nil, err
	}
	sort.Slice(paths, func(i, j int) bool { return paths[i].Key < paths[j].Key })
	for index := 1; index < len(paths); index++ {
		if paths[index-1].Key == paths[index].Key {
			return nil, exactDraftBranchPlanError("duplicate reachable branch key")
		}
	}
	return cloneExactDraftBranchPaths(paths), nil
}

func BuildExactDraftBranchPlan(
	command ExactDraftBranchPlanCommand,
	authority ExactDraftBranchPlanAuthority,
) (ExactDraftBranchPlan, error) {
	if err := validateExactDraftBranchPlanCommand(command); err != nil {
		return ExactDraftBranchPlan{}, err
	}
	if err := validateExactDraftBranchSource(command, authority.Draft); err != nil {
		return ExactDraftBranchPlan{}, err
	}
	paths, err := ReachableExactDraftBranches(authority.Draft)
	if err != nil {
		return ExactDraftBranchPlan{}, err
	}
	commands, authorities, err := exactDraftBranchInputs(paths, command.Branches, authority.Branches)
	if err != nil {
		return ExactDraftBranchPlan{}, err
	}

	plan := ExactDraftBranchPlan{
		ID: command.PlanID, RevisionID: command.PlanRevisionID,
		SourceDraft: cloneDraftExecution(authority.Draft), State: ExactDraftBranchPlanStatePlanned,
		Branches: make([]ExactDraftBranch, len(paths)), CreatedAt: command.CreatedAt,
	}
	unavailable := make(map[TaskVersionRef]struct{})
	for branchIndex, path := range paths {
		branchCommand := commands[path.Key]
		branchAuthority := authorities[path.Key]
		if len(branchCommand.Assignments) != len(path.Categories) ||
			len(branchAuthority.Assignments) != len(path.Categories) {
			return ExactDraftBranchPlan{}, exactDraftBranchPlanError(
				"branch %q does not cover every selected category", path.Key,
			)
		}
		branch := ExactDraftBranch{
			ID: branchCommand.BranchID, Path: cloneExactDraftBranchPath(path),
			State:       ExactDraftBranchStateReserved,
			Assignments: make([]ExactDraftBranchAssignment, len(path.Categories)),
		}
		for position, category := range path.Categories {
			exactCommand := branchCommand.Assignments[position]
			exactAuthority := branchAuthority.Assignments[position]
			if err := validateExactDraftBranchAssignmentInput(
				command,
				authority.Draft.SeriesID,
				branch.ID,
				category,
				exactCommand,
				exactAuthority,
			); err != nil {
				return ExactDraftBranchPlan{}, err
			}
			exact, buildErr := buildExactNormalAssignment(exactCommand, exactAuthority, unavailable)
			if buildErr != nil {
				return ExactDraftBranchPlan{}, exactDraftBranchPlanError(
					"branch %q position %d: %v", path.Key, position+1, buildErr,
				)
			}
			for _, edge := range exact.SelectedEdges {
				ref := TaskVersionRef{TaskID: edge.Snapshot.TaskID, Version: edge.Snapshot.Version}
				unavailable[ref] = struct{}{}
			}
			branch.Assignments[position] = ExactDraftBranchAssignment{
				Position: position + 1, Plan: exact, State: ExactDraftReservationStateReserved,
			}
		}
		plan.Branches[branchIndex] = branch
	}
	plan.ProofHash = exactDraftBranchPlanProofHash(plan)
	if err := plan.Validate(); err != nil {
		return ExactDraftBranchPlan{}, err
	}
	return cloneExactDraftBranchPlan(plan), nil
}

func (p ExactDraftBranchPlan) Validate() error {
	if err := validateExactDraftBranchPlanIdentity(p); err != nil {
		return err
	}
	paths, err := ReachableExactDraftBranches(p.SourceDraft)
	if err != nil || len(paths) != len(p.Branches) || len(paths) == 0 {
		return exactDraftBranchPlanError("branch set does not cover the source draft")
	}
	if err := validateExactDraftBranchPlanContents(p, paths); err != nil {
		return err
	}
	if err := validateExactDraftBranchPlanLifecycle(p); err != nil {
		return err
	}
	want := exactDraftBranchPlanProofHash(p)
	if !validCapacityDigest(p.ProofHash) || p.ProofHash != want {
		return exactDraftBranchPlanError("proof hash does not match branch reservations")
	}
	return nil
}

func validateExactDraftBranchPlanIdentity(plan ExactDraftBranchPlan) error {
	if plan.ID == uuid.Nil || plan.RevisionID == uuid.Nil || plan.ID == plan.RevisionID {
		return exactDraftBranchPlanError("invalid plan identity")
	}
	if !validArenaServerTime(plan.CreatedAt) || plan.SourceDraft.Validate() != nil {
		return exactDraftBranchPlanError("invalid plan timestamp or source draft")
	}
	if plan.SourceDraft.State == DraftExecutionStateCompleted ||
		plan.SourceDraft.State == DraftExecutionStateSuperseded ||
		plan.CreatedAt.Before(plan.SourceDraft.FirstActorDecision.DecidedAt) {
		return exactDraftBranchPlanError("source draft cannot be planned")
	}
	return nil
}

func enumerateExactDraftBranches(
	draft domain.ArenaDraft,
	paths *[]ExactDraftBranchPath,
) error {
	if draft.State == domain.ArenaDraftStateCompleted {
		*paths = append(*paths, exactDraftBranchPath(draft))
		return nil
	}
	turn, err := draft.CurrentTurn()
	if err != nil {
		return exactDraftBranchPlanError("current turn: %v", err)
	}
	for _, category := range draft.Pool {
		if exactDraftCategoryUsed(draft.Actions, category) {
			continue
		}
		next := cloneArenaDraft(draft)
		nextDeadline := turn.Deadline.Add(DraftTurnDuration)
		if draftFinalTurn(draft) {
			nextDeadline = time.Time{}
		}
		if err := next.ApplyAction(
			turn.Number,
			turn.ActorID,
			turn.Action,
			category,
			turn.Deadline,
			nextDeadline,
		); err != nil {
			return exactDraftBranchPlanError("enumerate turn %d: %v", turn.Number, err)
		}
		if err := enumerateExactDraftBranches(next, paths); err != nil {
			return err
		}
	}
	return nil
}

func exactDraftCategoryUsed(actions []domain.ArenaDraftAction, category domain.Category) bool {
	for _, action := range actions {
		if action.Category == category {
			return true
		}
	}
	return false
}

func exactDraftBranchPath(draft domain.ArenaDraft) ExactDraftBranchPath {
	actions := make([]ExactDraftBranchAction, len(draft.Actions))
	parts := make([]string, len(draft.Actions))
	for index, action := range draft.Actions {
		actions[index] = ExactDraftBranchAction{
			Turn: action.Turn, ActorID: action.ActorID, Action: action.Action, Category: action.Category,
		}
		parts[index] = fmt.Sprintf("%d:%s:%s", action.Turn, action.Action, action.Category)
	}
	return ExactDraftBranchPath{
		Key: strings.Join(parts, "/"), Actions: actions,
		Categories: append([]domain.Category(nil), draft.SelectedCategories...),
	}
}

func exactDraftBranchInputs(
	paths []ExactDraftBranchPath,
	commandInput []ExactDraftBranchCommand,
	authorityInput []ExactDraftBranchAuthority,
) (map[string]ExactDraftBranchCommand, map[string]ExactDraftBranchAuthority, error) {
	if len(commandInput) != len(paths) || len(authorityInput) != len(paths) {
		return nil, nil, exactDraftBranchPlanError("every reachable branch must be supplied")
	}
	commands := make(map[string]ExactDraftBranchCommand, len(commandInput))
	for _, branch := range commandInput {
		if branch.BranchID == uuid.Nil || branch.Key == "" || branch.Key != strings.TrimSpace(branch.Key) {
			return nil, nil, exactDraftBranchPlanError("invalid branch command")
		}
		if _, duplicate := commands[branch.Key]; duplicate {
			return nil, nil, exactDraftBranchPlanError("duplicate branch command %q", branch.Key)
		}
		commands[branch.Key] = branch
	}
	authorities := make(map[string]ExactDraftBranchAuthority, len(authorityInput))
	for _, branch := range authorityInput {
		if branch.Key == "" || branch.Key != strings.TrimSpace(branch.Key) {
			return nil, nil, exactDraftBranchPlanError("invalid branch authority")
		}
		if _, duplicate := authorities[branch.Key]; duplicate {
			return nil, nil, exactDraftBranchPlanError("duplicate branch authority %q", branch.Key)
		}
		authorities[branch.Key] = branch
	}
	for _, path := range paths {
		if _, exists := commands[path.Key]; !exists {
			return nil, nil, exactDraftBranchPlanError("missing branch command %q", path.Key)
		}
		if _, exists := authorities[path.Key]; !exists {
			return nil, nil, exactDraftBranchPlanError("missing branch authority %q", path.Key)
		}
	}
	return commands, authorities, nil
}

func validateExactDraftBranchPlanCommand(command ExactDraftBranchPlanCommand) error {
	if err := validateExactDraftBranchPlanCommandIdentity(command); err != nil {
		return err
	}
	seen := map[uuid.UUID]struct{}{command.PlanID: {}, command.PlanRevisionID: {}}
	for _, branch := range command.Branches {
		if err := validateExactDraftBranchCommand(command, branch, seen); err != nil {
			return err
		}
	}
	return nil
}

func validateExactDraftBranchPlanCommandIdentity(command ExactDraftBranchPlanCommand) error {
	if command.PlanID == uuid.Nil || command.PlanRevisionID == uuid.Nil || command.DraftID == uuid.Nil ||
		command.ExpectedDraftRevisionID == uuid.Nil || command.ExpectedDraftRevision < 1 {
		return exactDraftBranchPlanError("invalid command identity")
	}
	if !validArenaServerTime(command.CreatedAt) || len(command.Branches) == 0 {
		return exactDraftBranchPlanError("invalid command timestamp or empty branch set")
	}
	if command.PlanID == command.PlanRevisionID || command.PlanID == command.DraftID ||
		command.PlanRevisionID == command.DraftID {
		return exactDraftBranchPlanError("reused plan identity")
	}
	return nil
}

func validateExactDraftBranchCommand(
	command ExactDraftBranchPlanCommand,
	branch ExactDraftBranchCommand,
	seen map[uuid.UUID]struct{},
) error {
	if branch.BranchID == uuid.Nil {
		return exactDraftBranchPlanError("missing branch identity")
	}
	if _, duplicate := seen[branch.BranchID]; duplicate {
		return exactDraftBranchPlanError("reused branch identity")
	}
	seen[branch.BranchID] = struct{}{}
	for _, exact := range branch.Assignments {
		if err := validateExactDraftCommandIdentity(command, branch.BranchID, exact, seen); err != nil {
			return err
		}
	}
	return nil
}

func validateExactDraftCommandIdentity(
	command ExactDraftBranchPlanCommand,
	branchID uuid.UUID,
	exact ExactNormalAssignmentCommand,
	seen map[uuid.UUID]struct{},
) error {
	if exact.PlanID != command.PlanID || exact.PlanRevisionID != command.PlanRevisionID ||
		exact.BranchID != branchID || !exact.CreatedAt.Equal(command.CreatedAt) {
		return exactDraftBranchPlanError("exact assignment identity does not match plan")
	}
	for _, id := range exactDraftCommandEvidenceIDs(exact) {
		if id == uuid.Nil {
			return exactDraftBranchPlanError("missing exact assignment identity")
		}
		if _, duplicate := seen[id]; duplicate {
			return exactDraftBranchPlanError("reused exact assignment identity")
		}
		seen[id] = struct{}{}
	}
	return nil
}

func exactDraftCommandEvidenceIDs(command ExactNormalAssignmentCommand) []uuid.UUID {
	capacity := 1 + len(command.EdgeIDs) + len(command.ReservationIDs) + len(command.SnapshotIDs)
	ids := make([]uuid.UUID, 0, capacity)
	ids = append(ids, command.DecisionEvidenceID)
	ids = append(ids, command.EdgeIDs[:]...)
	ids = append(ids, command.ReservationIDs[:]...)
	return append(ids, command.SnapshotIDs[:]...)
}

func validateExactDraftBranchSource(
	command ExactDraftBranchPlanCommand,
	draft DraftExecution,
) error {
	if err := draft.Validate(); err != nil || draft.State == DraftExecutionStateCompleted ||
		draft.State == DraftExecutionStateSuperseded ||
		draft.ID != command.DraftID || draft.RevisionID != command.ExpectedDraftRevisionID ||
		draft.Revision != command.ExpectedDraftRevision || command.CreatedAt.Before(draft.FirstActorDecision.DecidedAt) {
		return exactDraftBranchPlanError("source draft revision does not match command")
	}
	return nil
}

func validateExactDraftBranchAssignmentInput(
	command ExactDraftBranchPlanCommand,
	seriesID uuid.UUID,
	branchID uuid.UUID,
	category domain.Category,
	exactCommand ExactNormalAssignmentCommand,
	exactAuthority ExactNormalAssignmentAuthority,
) error {
	if exactCommand.PlanID != command.PlanID || exactCommand.PlanRevisionID != command.PlanRevisionID ||
		exactCommand.BranchID != branchID || !exactCommand.CreatedAt.Equal(command.CreatedAt) ||
		exactCommand.Scope.SeriesID != seriesID {
		return exactDraftBranchPlanError("exact assignment command does not match branch")
	}
	if exactAuthority.Scope != exactCommand.Scope || exactAuthority.Category != category {
		return exactDraftBranchPlanError("exact assignment authority does not match branch category")
	}
	return nil
}

func validateExactDraftBranchPlanContents(
	plan ExactDraftBranchPlan,
	paths []ExactDraftBranchPath,
) error {
	validation := exactDraftBranchPlanValidation{
		seenIDs:  map[uuid.UUID]struct{}{plan.ID: {}, plan.RevisionID: {}},
		selected: make(map[TaskVersionRef]struct{}),
		slots:    make(map[int]uuid.UUID),
	}
	for index, branch := range plan.Branches {
		if err := validation.validateBranch(plan, paths[index], branch, index); err != nil {
			return err
		}
	}
	return nil
}

type exactDraftBranchPlanValidation struct {
	seenIDs        map[uuid.UUID]struct{}
	selected       map[TaskVersionRef]struct{}
	commonScope    ExactNormalAssignmentScope
	commonScopeSet bool
	slots          map[int]uuid.UUID
}

func (v *exactDraftBranchPlanValidation) validateBranch(
	plan ExactDraftBranchPlan,
	path ExactDraftBranchPath,
	branch ExactDraftBranch,
	index int,
) error {
	if branch.ID == uuid.Nil || !equalExactDraftBranchPath(branch.Path, path) ||
		len(branch.Assignments) != len(branch.Path.Categories) {
		return exactDraftBranchPlanError("branch %d does not match reachable path", index+1)
	}
	if _, duplicate := v.seenIDs[branch.ID]; duplicate {
		return exactDraftBranchPlanError("duplicate branch identity")
	}
	v.seenIDs[branch.ID] = struct{}{}
	for position, assignment := range branch.Assignments {
		if err := v.validateAssignment(plan, branch, assignment, position); err != nil {
			return err
		}
	}
	return nil
}

func (v *exactDraftBranchPlanValidation) validateAssignment(
	plan ExactDraftBranchPlan,
	branch ExactDraftBranch,
	assignment ExactDraftBranchAssignment,
	position int,
) error {
	exact := assignment.Plan
	if err := validateExactDraftAssignmentPlan(plan, branch, assignment, position); err != nil {
		return err
	}
	if !v.commonScopeSet {
		v.commonScope = exact.Scope
		v.commonScopeSet = true
	}
	if !sameExactDraftAssignmentScope(exact.Scope, v.commonScope) {
		return exactDraftBranchPlanError("assignment scopes do not share one locked Series")
	}
	if slotID, exists := v.slots[position]; exists && slotID != exact.Scope.SlotID {
		return exactDraftBranchPlanError("slot identity changed across draft branches")
	}
	v.slots[position] = exact.Scope.SlotID
	return v.recordAssignmentEvidence(exact)
}

func validateExactDraftAssignmentPlan(
	plan ExactDraftBranchPlan,
	branch ExactDraftBranch,
	assignment ExactDraftBranchAssignment,
	position int,
) error {
	exact := assignment.Plan
	if assignment.Position != position+1 || exact.Validate() != nil {
		return exactDraftBranchPlanError("branch %q assignment %d is invalid", branch.Path.Key, position+1)
	}
	if exact.PlanID != plan.ID || exact.PlanRevisionID != plan.RevisionID || exact.BranchID != branch.ID ||
		exact.Category != branch.Path.Categories[position] {
		return exactDraftBranchPlanError("branch %q assignment %d has wrong identity", branch.Path.Key, position+1)
	}
	if exact.Scope.SeriesID != plan.SourceDraft.SeriesID || !exact.CreatedAt.Equal(plan.CreatedAt) ||
		!exactParticipantsMatchDraft(exact.ParticipantIDs, plan.SourceDraft) {
		return exactDraftBranchPlanError("branch %q assignment %d has wrong authority", branch.Path.Key, position+1)
	}
	return nil
}

func sameExactDraftAssignmentScope(first, second ExactNormalAssignmentScope) bool {
	return first.TournamentID == second.TournamentID && first.RosterID == second.RosterID &&
		first.SeriesID == second.SeriesID && first.CategoryLockID == second.CategoryLockID
}

func (v *exactDraftBranchPlanValidation) recordAssignmentEvidence(exact ExactNormalAssignmentPlan) error {
	if _, duplicate := v.seenIDs[exact.DecisionEvidence.ID]; duplicate {
		return exactDraftBranchPlanError("duplicate decision evidence identity")
	}
	v.seenIDs[exact.DecisionEvidence.ID] = struct{}{}
	for _, edge := range exact.SelectedEdges {
		for _, id := range []uuid.UUID{edge.ID, edge.ReservationID, edge.Snapshot.SnapshotID} {
			if _, duplicate := v.seenIDs[id]; duplicate {
				return exactDraftBranchPlanError("duplicate reservation evidence identity")
			}
			v.seenIDs[id] = struct{}{}
		}
		ref := TaskVersionRef{TaskID: edge.Snapshot.TaskID, Version: edge.Snapshot.Version}
		if _, duplicate := v.selected[ref]; duplicate {
			return exactDraftBranchPlanError("task version reserved by more than one branch")
		}
		v.selected[ref] = struct{}{}
	}
	return nil
}

func validateExactDraftBranchPlanLifecycle(plan ExactDraftBranchPlan) error {
	activeCount := 0
	for _, branch := range plan.Branches {
		if err := validateExactDraftBranchLifecycle(plan, branch); err != nil {
			return err
		}
		if branch.State == ExactDraftBranchStateActive {
			activeCount++
		}
	}
	return validateExactDraftPlanState(plan, activeCount)
}

func validateExactDraftBranchLifecycle(plan ExactDraftBranchPlan, branch ExactDraftBranch) error {
	switch branch.State {
	case ExactDraftBranchStateReserved:
		return validateReservedExactDraftBranch(branch)
	case ExactDraftBranchStateActive:
		return validateActiveExactDraftBranch(plan, branch)
	case ExactDraftBranchStateReleased:
		return validateReleasedExactDraftBranch(plan, branch)
	default:
		return exactDraftBranchPlanError("unknown branch state %q", branch.State)
	}
}

func validateReservedExactDraftBranch(branch ExactDraftBranch) error {
	if !branch.ActivatedAt.IsZero() || !branch.ReleasedAt.IsZero() || branch.ReleaseReason != "" {
		return exactDraftBranchPlanError("reserved branch contains transition evidence")
	}
	for _, assignment := range branch.Assignments {
		if assignment.State != ExactDraftReservationStateReserved || !assignment.TransitionedAt.IsZero() {
			return exactDraftBranchPlanError("reserved branch contains transitioned assignment")
		}
	}
	return nil
}

func validateActiveExactDraftBranch(plan ExactDraftBranchPlan, branch ExactDraftBranch) error {
	if branch.ID != plan.ActiveBranchID || branch.ActivatedAt.IsZero() ||
		!branch.ActivatedAt.Equal(plan.CommittedAt) || !branch.ReleasedAt.IsZero() ||
		branch.ReleaseReason != "" || !slices.Equal(branch.Path.Categories, plan.CompletedCategories) {
		return exactDraftBranchPlanError("active branch evidence does not match completion")
	}
	for _, assignment := range branch.Assignments {
		if assignment.State != ExactDraftReservationStateCommitted ||
			!assignment.TransitionedAt.Equal(plan.CommittedAt) {
			return exactDraftBranchPlanError("active branch assignment is not committed")
		}
	}
	return nil
}

func validateReleasedExactDraftBranch(plan ExactDraftBranchPlan, branch ExactDraftBranch) error {
	if !branch.ActivatedAt.IsZero() || !branch.ReleasedAt.Equal(plan.CommittedAt) ||
		branch.ReleaseReason == "" || branch.ReleaseReason != strings.TrimSpace(branch.ReleaseReason) {
		return exactDraftBranchPlanError("released branch evidence is invalid")
	}
	for _, assignment := range branch.Assignments {
		if assignment.State != ExactDraftReservationStateReleased ||
			!assignment.TransitionedAt.Equal(plan.CommittedAt) {
			return exactDraftBranchPlanError("released branch assignment is not released")
		}
	}
	return nil
}

func validateExactDraftPlanState(plan ExactDraftBranchPlan, activeCount int) error {
	switch plan.State {
	case ExactDraftBranchPlanStatePlanned:
		return validatePlannedExactDraftPlan(plan, activeCount)
	case ExactDraftBranchPlanStateCommitted:
		return validateCommittedExactDraftPlan(plan, activeCount)
	default:
		return exactDraftBranchPlanError("unknown plan state %q", plan.State)
	}
}

func validatePlannedExactDraftPlan(plan ExactDraftBranchPlan, activeCount int) error {
	if plan.ActiveBranchID != uuid.Nil || plan.CompletionDraftRevisionID != uuid.Nil ||
		plan.CompletionDraftRevision != 0 || plan.ActivationCommandID != uuid.Nil ||
		len(plan.CompletedCategories) != 0 || !plan.CommittedAt.IsZero() || activeCount != 0 {
		return exactDraftBranchPlanError("planned state contains completion evidence")
	}
	for _, branch := range plan.Branches {
		if branch.State != ExactDraftBranchStateReserved {
			return exactDraftBranchPlanError("planned state contains transitioned branch")
		}
	}
	return nil
}

func validateCommittedExactDraftPlan(plan ExactDraftBranchPlan, activeCount int) error {
	if plan.ActiveBranchID == uuid.Nil || plan.CompletionDraftRevisionID == uuid.Nil ||
		plan.CompletionDraftRevision < plan.SourceDraft.Revision || plan.ActivationCommandID == uuid.Nil {
		return exactDraftBranchPlanError("committed state lacks identity evidence")
	}
	if len(plan.CompletedCategories) == 0 || !validArenaServerTime(plan.CommittedAt) ||
		plan.CommittedAt.Before(plan.CreatedAt) || activeCount != 1 {
		return exactDraftBranchPlanError("committed state lacks transition evidence")
	}
	return nil
}

func activateExactDraftBranch(
	current ExactDraftBranchPlan,
	completed DraftExecution,
	command ExactDraftBranchActivationCommand,
) (ExactDraftBranchPlan, error) {
	if !validExactDraftPlanActivationSource(current, command) {
		return ExactDraftBranchPlan{}, ErrExactDraftBranchPlanConflict
	}
	if !validExactDraftCompletion(current.SourceDraft, completed, command) {
		return ExactDraftBranchPlan{}, ErrExactDraftBranchPlanConflict
	}
	completedPath := exactDraftBranchPathFromExecution(completed)
	activeIndex := exactDraftActiveBranchIndex(current.Branches, completedPath)
	if activeIndex < 0 {
		return ExactDraftBranchPlan{}, exactDraftBranchPlanError("completed branch was not reserved")
	}

	next := cloneExactDraftBranchPlan(current)
	applyExactDraftPlanCompletion(&next, completed, command, activeIndex)
	if err := next.Validate(); err != nil {
		return ExactDraftBranchPlan{}, err
	}
	return next, nil
}

func validExactDraftPlanActivationSource(
	current ExactDraftBranchPlan,
	command ExactDraftBranchActivationCommand,
) bool {
	return current.Validate() == nil && current.State == ExactDraftBranchPlanStatePlanned &&
		current.ID == command.PlanID && current.RevisionID == command.ExpectedPlanRevisionID
}

func validExactDraftCompletion(
	source DraftExecution,
	completed DraftExecution,
	command ExactDraftBranchActivationCommand,
) bool {
	if completed.Validate() != nil || completed.State != DraftExecutionStateCompleted {
		return false
	}
	if completed.ID != command.DraftID || completed.ID != source.ID ||
		completed.RevisionID != command.ExpectedDraftRevisionID ||
		completed.Revision != command.ExpectedDraftRevision {
		return false
	}
	lastAction := completed.Actions[len(completed.Actions)-1]
	return !command.CommittedAt.Before(lastAction.OccurredAt) &&
		exactDraftCompletionExtendsSource(source, completed)
}

func exactDraftActiveBranchIndex(
	branches []ExactDraftBranch,
	completed ExactDraftBranchPath,
) int {
	for index, branch := range branches {
		if equalExactDraftBranchPath(branch.Path, completed) {
			return index
		}
	}
	return -1
}

func applyExactDraftPlanCompletion(
	plan *ExactDraftBranchPlan,
	completed DraftExecution,
	command ExactDraftBranchActivationCommand,
	activeIndex int,
) {
	if plan == nil {
		return
	}
	plan.State = ExactDraftBranchPlanStateCommitted
	plan.ActiveBranchID = plan.Branches[activeIndex].ID
	plan.CompletionDraftRevisionID = completed.RevisionID
	plan.CompletionDraftRevision = completed.Revision
	plan.ActivationCommandID = command.CommandID
	plan.CompletedCategories = append([]domain.Category(nil), completed.SelectedCategories...)
	plan.CommittedAt = command.CommittedAt
	for index := range plan.Branches {
		if index == activeIndex {
			activateExactDraftBranchReservations(&plan.Branches[index], command.CommittedAt)
			continue
		}
		releaseExactDraftBranchReservations(
			&plan.Branches[index],
			command.CommittedAt,
			command.ReleaseReason,
		)
	}
}

func activateExactDraftBranchReservations(branch *ExactDraftBranch, committedAt time.Time) {
	branch.State = ExactDraftBranchStateActive
	branch.ActivatedAt = committedAt
	for index := range branch.Assignments {
		branch.Assignments[index].State = ExactDraftReservationStateCommitted
		branch.Assignments[index].TransitionedAt = committedAt
	}
}

func releaseExactDraftBranchReservations(
	branch *ExactDraftBranch,
	committedAt time.Time,
	reason string,
) {
	branch.State = ExactDraftBranchStateReleased
	branch.ReleasedAt = committedAt
	branch.ReleaseReason = reason
	for index := range branch.Assignments {
		branch.Assignments[index].State = ExactDraftReservationStateReleased
		branch.Assignments[index].TransitionedAt = committedAt
	}
}

func validateExactDraftBranchActivationCommand(command ExactDraftBranchActivationCommand) error {
	ids := []uuid.UUID{
		command.PlanID, command.DraftID, command.ExpectedPlanRevisionID,
		command.ExpectedDraftRevisionID, command.CommandID,
	}
	seen := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		if id == uuid.Nil {
			return domain.ErrValidation
		}
		if _, duplicate := seen[id]; duplicate {
			return domain.ErrValidation
		}
		seen[id] = struct{}{}
	}
	if command.ExpectedDraftRevision < 1 || !validArenaServerTime(command.CommittedAt) ||
		command.ReleaseReason == "" || command.ReleaseReason != strings.TrimSpace(command.ReleaseReason) {
		return domain.ErrValidation
	}
	return nil
}

func reconcileExactDraftBranchActivation(
	recorded ExactDraftBranchPlan,
	command ExactDraftBranchActivationCommand,
) (*ExactDraftBranchPlan, error) {
	if err := recorded.Validate(); err != nil || recorded.State != ExactDraftBranchPlanStateCommitted ||
		recorded.ID != command.PlanID || recorded.RevisionID != command.ExpectedPlanRevisionID ||
		recorded.SourceDraft.ID != command.DraftID ||
		recorded.CompletionDraftRevisionID != command.ExpectedDraftRevisionID ||
		recorded.CompletionDraftRevision != command.ExpectedDraftRevision ||
		recorded.ActivationCommandID != command.CommandID || !recorded.CommittedAt.Equal(command.CommittedAt) {
		return nil, ErrExactDraftBranchPlanConflict
	}
	for _, branch := range recorded.Branches {
		if branch.State == ExactDraftBranchStateReleased && branch.ReleaseReason != command.ReleaseReason {
			return nil, ErrExactDraftBranchPlanConflict
		}
	}
	result := cloneExactDraftBranchPlan(recorded)
	return &result, nil
}

func exactDraftCompletionExtendsSource(source, completed DraftExecution) bool {
	if !sameExactDraftIdentity(source, completed) || len(source.Actions) > len(completed.Actions) {
		return false
	}
	for index := range source.Actions {
		if !sameExactDraftAction(source.Actions[index], completed.Actions[index]) {
			return false
		}
	}
	return true
}

func sameExactDraftIdentity(first, second DraftExecution) bool {
	return first.ID == second.ID && first.SeriesID == second.SeriesID && first.Format == second.Format &&
		first.FirstParticipantID == second.FirstParticipantID &&
		first.SecondParticipantID == second.SecondParticipantID &&
		slices.Equal(first.Pool, second.Pool) &&
		equalExactDraftDecisionEvidence(first.FirstActorDecision, second.FirstActorDecision)
}

func sameExactDraftAction(first, second DraftActionRecord) bool {
	if first.ID != second.ID || first.ResultRevisionID != second.ResultRevisionID ||
		first.CommandID != second.CommandID || first.Turn != second.Turn ||
		first.ActorID != second.ActorID || first.Action != second.Action || first.Category != second.Category {
		return false
	}
	return first.OccurredAt.Equal(second.OccurredAt) &&
		first.ScheduledDeadline.Equal(second.ScheduledDeadline) && first.Automatic == second.Automatic &&
		equalExactDraftDecisionEvidencePointer(first.DecisionEvidence, second.DecisionEvidence)
}

func equalExactDraftDecisionEvidencePointer(
	first *domain.ArenaDecisionEvidence,
	second *domain.ArenaDecisionEvidence,
) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return equalExactDraftDecisionEvidence(*first, *second)
}

func equalExactDraftDecisionEvidence(
	first domain.ArenaDecisionEvidence,
	second domain.ArenaDecisionEvidence,
) bool {
	return first.ID == second.ID && first.Purpose == second.Purpose &&
		first.AlgorithmVersion == second.AlgorithmVersion && first.Seed == second.Seed &&
		first.ReplayDigest == second.ReplayDigest && first.OwnerID == second.OwnerID &&
		first.DecidedAt.Equal(second.DecidedAt) &&
		slices.Equal(first.NormalizedInputs, second.NormalizedInputs) &&
		slices.Equal(first.Result, second.Result)
}

func exactDraftBranchPathFromExecution(draft DraftExecution) ExactDraftBranchPath {
	actions := make([]ExactDraftBranchAction, len(draft.Actions))
	parts := make([]string, len(draft.Actions))
	for index, action := range draft.Actions {
		actions[index] = ExactDraftBranchAction{
			Turn: action.Turn, ActorID: action.ActorID, Action: action.Action, Category: action.Category,
		}
		parts[index] = fmt.Sprintf("%d:%s:%s", action.Turn, action.Action, action.Category)
	}
	return ExactDraftBranchPath{
		Key: strings.Join(parts, "/"), Actions: actions,
		Categories: append([]domain.Category(nil), draft.SelectedCategories...),
	}
}

func exactDraftPlanMatchesCommand(
	plan ExactDraftBranchPlan,
	command ExactDraftBranchPlanCommand,
) bool {
	if plan.ID != command.PlanID || plan.RevisionID != command.PlanRevisionID ||
		plan.SourceDraft.ID != command.DraftID ||
		plan.SourceDraft.RevisionID != command.ExpectedDraftRevisionID ||
		plan.SourceDraft.Revision != command.ExpectedDraftRevision ||
		!plan.CreatedAt.Equal(command.CreatedAt) || len(plan.Branches) != len(command.Branches) {
		return false
	}
	branches := make(map[string]uuid.UUID, len(command.Branches))
	for _, branch := range command.Branches {
		branches[branch.Key] = branch.BranchID
	}
	for _, branch := range plan.Branches {
		if branches[branch.Path.Key] != branch.ID {
			return false
		}
	}
	return true
}

func exactParticipantsMatchDraft(participants []uuid.UUID, draft DraftExecution) bool {
	if len(participants) != 2 {
		return false
	}
	return (participants[0] == draft.FirstParticipantID && participants[1] == draft.SecondParticipantID) ||
		(participants[0] == draft.SecondParticipantID && participants[1] == draft.FirstParticipantID)
}

func exactDraftBranchPlanProofHash(plan ExactDraftBranchPlan) string {
	hash := sha256.New()
	writeCapacityField(hash, exactDraftBranchPlanProofV1)
	writeCapacityField(hash, plan.ID.String())
	writeCapacityField(hash, plan.RevisionID.String())
	writeCapacityField(hash, plan.SourceDraft.ID.String())
	writeCapacityField(hash, plan.SourceDraft.RevisionID.String())
	writeCapacityField(hash, fmt.Sprintf("draft-revision:%d", plan.SourceDraft.Revision))
	writeCapacityField(hash, plan.CreatedAt.Format(time.RFC3339Nano))
	for _, branch := range plan.Branches {
		writeCapacityField(hash, branch.ID.String())
		writeCapacityField(hash, branch.Path.Key)
		for _, category := range branch.Path.Categories {
			writeCapacityField(hash, category.String())
		}
		for _, assignment := range branch.Assignments {
			writeCapacityField(hash, fmt.Sprintf("position:%d", assignment.Position))
			writeCapacityField(hash, assignment.Plan.ProofHash)
		}
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func equalExactDraftBranchPath(first, second ExactDraftBranchPath) bool {
	return first.Key == second.Key && slices.Equal(first.Actions, second.Actions) &&
		slices.Equal(first.Categories, second.Categories)
}

func cloneExactDraftBranchPath(path ExactDraftBranchPath) ExactDraftBranchPath {
	cloned := path
	cloned.Actions = append([]ExactDraftBranchAction(nil), path.Actions...)
	cloned.Categories = append([]domain.Category(nil), path.Categories...)
	return cloned
}

func cloneExactDraftBranchPaths(paths []ExactDraftBranchPath) []ExactDraftBranchPath {
	cloned := make([]ExactDraftBranchPath, len(paths))
	for index, path := range paths {
		cloned[index] = cloneExactDraftBranchPath(path)
	}
	return cloned
}

func cloneExactDraftBranchPlan(plan ExactDraftBranchPlan) ExactDraftBranchPlan {
	cloned := plan
	cloned.SourceDraft = cloneDraftExecution(plan.SourceDraft)
	cloned.CompletedCategories = append([]domain.Category(nil), plan.CompletedCategories...)
	cloned.Branches = make([]ExactDraftBranch, len(plan.Branches))
	for branchIndex, branch := range plan.Branches {
		clonedBranch := branch
		clonedBranch.Path = cloneExactDraftBranchPath(branch.Path)
		clonedBranch.Assignments = make([]ExactDraftBranchAssignment, len(branch.Assignments))
		for assignmentIndex, assignment := range branch.Assignments {
			clonedAssignment := assignment
			clonedAssignment.Plan = cloneExactNormalAssignmentPlan(assignment.Plan)
			clonedBranch.Assignments[assignmentIndex] = clonedAssignment
		}
		cloned.Branches[branchIndex] = clonedBranch
	}
	return cloned
}

func exactDraftBranchPlanError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidExactDraftBranchPlan, fmt.Sprintf(format, arguments...))
}
