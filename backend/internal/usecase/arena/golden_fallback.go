package arena

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var (
	ErrInvalidGoldenFallback           = errors.New("invalid Golden fallback allocation")
	ErrGoldenFallbackNotRequired       = errors.New("golden fallback allocation is not required")
	ErrGoldenFallbackAuthorityConflict = errors.New("golden fallback authority conflict")
	ErrGoldenFallbackConflict          = errors.New("golden fallback commit conflict")
)

type GoldenPositionKind string

const (
	GoldenPositionDirect         GoldenPositionKind = "direct"
	GoldenPositionNoShowFallback GoldenPositionKind = "no_show_fallback"
)

type GoldenPositionAllocation struct {
	Position      int
	ParticipantID uuid.UUID
	Kind          GoldenPositionKind
}

type GoldenFallbackOrderingInput struct {
	ParticipantID     uuid.UUID
	Points            int
	Buchholz          int
	HeadToHeadPoints  int
	HeadToHeadApplied bool
	EffectiveTime     time.Duration
	AcceptedSolveTime *time.Duration
	Seed              int
}

type GoldenFallbackCommand struct {
	Scope               GoldenStateScope
	CommandID           uuid.UUID
	AllocationID        uuid.UUID
	ExpectedState       GoldenStateExpectation
	NextStateRevisionID uuid.UUID
}

type GoldenAllocation struct {
	ID                    uuid.UUID
	CommandID             uuid.UUID
	Scope                 GoldenStateScope
	ExpectedState         GoldenStateExpectation
	ResultStateRevisionID uuid.UUID
	AllocatedAt           time.Time
	OrderingInputs        []GoldenFallbackOrderingInput
	Positions             []GoldenPositionAllocation
	PayloadDigest         [sha256.Size]byte
}

type GoldenFallbackUseCase struct {
	repository GoldenStateRepository
	clock      Clock
}

func NewGoldenFallbackUseCase(repository GoldenStateRepository, clock Clock) *GoldenFallbackUseCase {
	return &GoldenFallbackUseCase{repository: repository, clock: clock}
}

func (u *GoldenFallbackUseCase) Allocate(
	ctx context.Context,
	command GoldenFallbackCommand,
) (*GoldenState, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil {
		return nil, false, domain.ErrValidation
	}
	if err := validateGoldenFallbackCommand(command); err != nil {
		return nil, false, err
	}
	allocatedAt := u.clock.Now().Round(0).UTC()
	if !validArenaServerTime(allocatedAt) {
		return nil, false, domain.ErrValidation
	}
	for range goldenStateAttempts {
		state, changed, retry, err := u.allocateAttempt(ctx, command, allocatedAt)
		if retry {
			continue
		}
		return state, changed, err
	}
	return nil, false, ErrGoldenFallbackConflict
}

func (u *GoldenFallbackUseCase) allocateAttempt(
	ctx context.Context,
	command GoldenFallbackCommand,
	allocatedAt time.Time,
) (*GoldenState, bool, bool, error) {
	authority, err := u.repository.LoadGoldenState(ctx, command.Scope)
	if err != nil {
		return nil, false, false, fmt.Errorf("GoldenFallbackUseCase - load state: %w", err)
	}
	if err := authority.Validate(); err != nil {
		return nil, false, false, domain.ErrInternal
	}
	if authority.Allocation != nil {
		state, reconcileErr := reconcileGoldenFallback(authority, command, *authority.Allocation)
		return state, false, false, reconcileErr
	}
	if goldenFallbackCommandUsedElsewhere(authority, command.CommandID) {
		return nil, false, false, ErrGoldenCommandReuse
	}
	if !authority.Expectation().Equal(command.ExpectedState) {
		return nil, false, false, ErrGoldenFallbackAuthorityConflict
	}
	if err := validateGoldenFallbackEligibility(authority, allocatedAt); err != nil {
		return nil, false, false, err
	}
	if err := validateGoldenFreshIDs(
		authority,
		command.CommandID,
		command.AllocationID,
		command.NextStateRevisionID,
	); err != nil {
		return nil, false, false, err
	}
	next, err := buildGoldenFallbackSuccessor(authority, command, allocatedAt)
	if err != nil {
		return nil, false, false, err
	}
	return u.commitGoldenFallback(ctx, command, authority.Expectation(), next)
}

func goldenFallbackCommandUsedElsewhere(state GoldenState, commandID uuid.UUID) bool {
	if _, found := goldenReadyEventByCommand(state.ReadyEvents, commandID); found {
		return true
	}
	_, found := goldenNoShowByCommand(state.NoShows, commandID)
	return found
}

func validateGoldenFallbackEligibility(state GoldenState, allocatedAt time.Time) error {
	terminal, eligible := goldenTerminalNoShow(state)
	if goldenAny(!eligible, allocatedAt.Before(terminal.ResolvedAt), allocatedAt.Before(terminal.Deadline)) {
		return ErrGoldenFallbackAuthorityConflict
	}
	activeCount := len(state.ActiveParticipantIDs())
	if state.Group.ParticipationEstablished && activeCount >= 2 {
		return ErrGoldenFallbackNotRequired
	}
	if !state.Group.ParticipationEstablished && activeCount != 0 {
		return ErrGoldenFallbackAuthorityConflict
	}
	return nil
}

func buildGoldenFallbackSuccessor(
	authority GoldenState,
	command GoldenFallbackCommand,
	allocatedAt time.Time,
) (GoldenState, error) {
	if authority.Revision == math.MaxInt64 {
		return GoldenState{}, fmt.Errorf("%w: fallback successor", ErrGoldenRevisionOverflow)
	}
	allocation, err := buildGoldenAllocation(authority, command, allocatedAt)
	if err != nil {
		return GoldenState{}, err
	}
	next := authority.Snapshot()
	next.PreviousRevisionID = goldenUUID(next.RevisionID)
	next.RevisionID = command.NextStateRevisionID
	next.Revision++
	next.Allocation = &allocation
	return BuildGoldenState(next)
}

func buildGoldenAllocation(
	state GoldenState,
	command GoldenFallbackCommand,
	allocatedAt time.Time,
) (GoldenAllocation, error) {
	allocation := GoldenAllocation{
		ID: command.AllocationID, CommandID: command.CommandID, Scope: command.Scope,
		ExpectedState: command.ExpectedState, ResultStateRevisionID: command.NextStateRevisionID,
		AllocatedAt: allocatedAt,
	}
	if state.Group.ParticipationEstablished {
		active := state.ActiveParticipantIDs()
		if len(active) > 1 {
			return GoldenAllocation{}, ErrGoldenFallbackNotRequired
		}
		excludedSeeds := make([]GoldenGroupMemberSeed, 0, len(state.Group.Members))
		excluded := make(map[uuid.UUID]struct{}, len(state.Group.Members))
		for _, member := range state.Group.Members {
			if member.Excluded {
				excluded[member.ParticipantID] = struct{}{}
			}
		}
		for _, member := range state.Topology.Members() {
			if _, eligible := excluded[member.ParticipantID]; eligible {
				excludedSeeds = append(excludedSeeds, member)
			}
		}
		ordered, orderErr := orderGoldenFallbackSeeds(excludedSeeds)
		if orderErr != nil {
			return GoldenAllocation{}, orderErr
		}
		allocation.OrderingInputs = goldenFallbackInputs(ordered)
		position := state.Group.PositionFrom
		if len(active) == 1 {
			allocation.Positions = append(allocation.Positions, GoldenPositionAllocation{
				Position: position, ParticipantID: active[0], Kind: GoldenPositionDirect,
			})
			position++
		}
		for _, member := range ordered {
			allocation.Positions = append(allocation.Positions, GoldenPositionAllocation{
				Position: position, ParticipantID: member.ParticipantID, Kind: GoldenPositionNoShowFallback,
			})
			position++
		}
	}
	payload, err := goldenAllocationPayload(allocation)
	if err != nil {
		return GoldenAllocation{}, goldenFallbackError("encode allocation: %v", err)
	}
	allocation.PayloadDigest = sha256.Sum256(payload)
	return allocation, nil
}

func (u *GoldenFallbackUseCase) commitGoldenFallback(
	ctx context.Context,
	command GoldenFallbackCommand,
	expected GoldenStateExpectation,
	next GoldenState,
) (*GoldenState, bool, bool, error) {
	committed, changed, err := u.repository.CommitGoldenState(ctx, GoldenStateCommit{Expected: expected, Next: next})
	if errors.Is(err, domain.ErrConflict) {
		return nil, false, true, nil
	}
	if err != nil {
		return nil, false, false, fmt.Errorf("GoldenFallbackUseCase - commit state: %w", err)
	}
	if committed == nil || committed.Validate() != nil || committed.Allocation == nil {
		return nil, false, false, domain.ErrInternal
	}
	result, reconcileErr := reconcileGoldenFallback(*committed, command, *committed.Allocation)
	if reconcileErr != nil || (changed && !committed.Expectation().Equal(next.Expectation())) {
		return nil, false, false, domain.ErrInternal
	}
	return result, changed, false, nil
}

func reconcileGoldenFallback(
	state GoldenState,
	command GoldenFallbackCommand,
	allocation GoldenAllocation,
) (*GoldenState, error) {
	if allocation.ID != command.AllocationID || allocation.CommandID != command.CommandID ||
		allocation.Scope != command.Scope || !allocation.ExpectedState.Equal(command.ExpectedState) ||
		allocation.ResultStateRevisionID != command.NextStateRevisionID {
		return nil, ErrGoldenCommandReuse
	}
	clone := state.Snapshot()
	return &clone, nil
}

func validateGoldenAllocation(state GoldenState) error {
	if state.Allocation == nil {
		return nil
	}
	allocation := *state.Allocation
	if err := validateGoldenAllocationIdentity(state, allocation); err != nil {
		return err
	}
	terminal, eligible := goldenTerminalNoShow(state)
	if goldenAny(
		!eligible,
		allocation.AllocatedAt.Before(terminal.ResolvedAt),
		allocation.AllocatedAt.Before(terminal.Deadline),
	) {
		return goldenFallbackError("allocation lacks causal terminal no-show evidence")
	}
	if !allocation.ExpectedState.Equal(goldenAllocationPredecessorExpectation(state)) {
		return goldenFallbackError("allocation predecessor authority changed")
	}
	if goldenAllocationCommandIsReused(state, allocation.CommandID) {
		return goldenFallbackError("allocation command identity is reused")
	}
	want, err := buildGoldenAllocation(state, GoldenFallbackCommand{
		Scope: state.Scope, CommandID: allocation.CommandID, AllocationID: allocation.ID,
		ExpectedState: allocation.ExpectedState, NextStateRevisionID: allocation.ResultStateRevisionID,
	}, allocation.AllocatedAt)
	if goldenAny(err != nil, !goldenAllocationsEqual(allocation, want)) {
		return goldenFallbackError("allocation does not follow frozen fallback ordering")
	}
	return nil
}

func validateGoldenAllocationIdentity(state GoldenState, allocation GoldenAllocation) error {
	wrongPredecessor := state.PreviousRevisionID == nil
	if state.PreviousRevisionID != nil {
		wrongPredecessor = *state.PreviousRevisionID != allocation.ExpectedState.RevisionID
	}
	if goldenAny(
		allocation.ID == uuid.Nil, allocation.CommandID == uuid.Nil, allocation.Scope != state.Scope,
		allocation.ExpectedState.Scope != state.Scope,
		allocation.ExpectedState.PayloadDigest == [sha256.Size]byte{},
		allocation.ResultStateRevisionID != state.RevisionID,
		allocation.ExpectedState.Revision == math.MaxInt64,
		allocation.ExpectedState.Revision+1 != state.Revision,
		allocation.ExpectedState.RevisionID == state.RevisionID,
		wrongPredecessor, !validArenaServerTime(allocation.AllocatedAt),
	) {
		return goldenFallbackError("invalid allocation identity or state lineage")
	}
	return nil
}

func goldenAllocationCommandIsReused(state GoldenState, commandID uuid.UUID) bool {
	for _, event := range state.ReadyEvents {
		if event.CommandID == commandID {
			return true
		}
	}
	for _, resolution := range state.NoShows {
		if resolution.CommandID == commandID {
			return true
		}
	}
	return false
}

func goldenTerminalNoShow(state GoldenState) (GoldenNoShowResolution, bool) {
	if goldenAny(len(state.Windows) == 0, len(state.NoShows) == 0) {
		return GoldenNoShowResolution{}, false
	}
	window := state.Windows[len(state.Windows)-1]
	terminal := state.NoShows[len(state.NoShows)-1]
	if goldenAny(
		window.State != GoldenReadyWindowExpired,
		terminal.WindowID != window.ID,
		terminal.ResultWindowRevisionID != window.RevisionID,
	) {
		return GoldenNoShowResolution{}, false
	}
	return terminal, true
}

func goldenAllocationPredecessorExpectation(state GoldenState) GoldenStateExpectation {
	expected := state.Expectation()
	allocation := state.Allocation
	if allocation == nil {
		return expected
	}
	expected.RevisionID = allocation.ExpectedState.RevisionID
	expected.Revision = allocation.ExpectedState.Revision
	expected.PayloadDigest = allocation.ExpectedState.PayloadDigest
	return expected
}

func OrderGoldenFallbackMembers(members []GoldenGroupMemberSeed) ([]uuid.UUID, error) {
	ordered, err := orderGoldenFallbackSeeds(members)
	if err != nil {
		return nil, err
	}
	result := make([]uuid.UUID, len(ordered))
	for index, member := range ordered {
		result[index] = member.ParticipantID
	}
	return result, nil
}

func orderGoldenFallbackSeeds(members []GoldenGroupMemberSeed) ([]GoldenGroupMemberSeed, error) {
	ordered := cloneGoldenMemberSeeds(members)
	seenIDs := make(map[uuid.UUID]struct{}, len(ordered))
	seenSeeds := make(map[int]struct{}, len(ordered))
	for _, member := range ordered {
		if err := validateGoldenGroupMember(member); err != nil {
			return nil, goldenFallbackError("ordering input: %v", err)
		}
		if _, duplicate := seenIDs[member.ParticipantID]; duplicate {
			return nil, goldenFallbackError("duplicate ordering participant")
		}
		if _, duplicate := seenSeeds[member.Seed]; duplicate {
			return nil, goldenFallbackError("duplicate frozen seed")
		}
		seenIDs[member.ParticipantID] = struct{}{}
		seenSeeds[member.Seed] = struct{}{}
	}
	sort.SliceStable(ordered, func(i, j int) bool { return goldenFallbackLess(ordered[i], ordered[j]) })
	return ordered, nil
}

func goldenFallbackLess(first, second GoldenGroupMemberSeed) bool {
	if first.Points != second.Points {
		return first.Points > second.Points
	}
	if first.Buchholz != second.Buchholz {
		return first.Buchholz > second.Buchholz
	}
	if first.HeadToHeadApplied && second.HeadToHeadApplied && first.HeadToHeadPoints != second.HeadToHeadPoints {
		return first.HeadToHeadPoints > second.HeadToHeadPoints
	}
	if first.EffectiveTime != second.EffectiveTime {
		return first.EffectiveTime < second.EffectiveTime
	}
	return first.Seed < second.Seed
}

func goldenFallbackInputs(members []GoldenGroupMemberSeed) []GoldenFallbackOrderingInput {
	result := make([]GoldenFallbackOrderingInput, len(members))
	for index, member := range members {
		result[index] = GoldenFallbackOrderingInput{
			ParticipantID: member.ParticipantID, Points: member.Points, Buchholz: member.Buchholz,
			HeadToHeadPoints: member.HeadToHeadPoints, HeadToHeadApplied: member.HeadToHeadApplied,
			EffectiveTime: member.EffectiveTime, AcceptedSolveTime: cloneGoldenDuration(member.AcceptedSolveTime),
			Seed: member.Seed,
		}
	}
	return result
}

func goldenAllocationPayload(allocation GoldenAllocation) ([]byte, error) {
	clone := allocation
	clone.PayloadDigest = [sha256.Size]byte{}
	return goldenEncode(clone)
}

func goldenAllocationsEqual(first, second GoldenAllocation) bool {
	if first.ID != second.ID || first.CommandID != second.CommandID || first.Scope != second.Scope ||
		!first.ExpectedState.Equal(second.ExpectedState) || first.ResultStateRevisionID != second.ResultStateRevisionID ||
		!first.AllocatedAt.Equal(second.AllocatedAt) || first.PayloadDigest != second.PayloadDigest ||
		len(first.OrderingInputs) != len(second.OrderingInputs) || len(first.Positions) != len(second.Positions) {
		return false
	}
	firstPayload, firstErr := goldenAllocationPayload(first)
	secondPayload, secondErr := goldenAllocationPayload(second)
	return firstErr == nil && secondErr == nil && bytes.Equal(firstPayload, secondPayload)
}

func validateGoldenFallbackCommand(command GoldenFallbackCommand) error {
	if !validGoldenStateScope(command.Scope) || command.CommandID == uuid.Nil || command.AllocationID == uuid.Nil ||
		command.NextStateRevisionID == uuid.Nil || command.CommandID == command.AllocationID ||
		command.CommandID == command.NextStateRevisionID || command.AllocationID == command.NextStateRevisionID {
		return goldenFallbackError("invalid command identity")
	}
	return nil
}

func cloneGoldenAllocation(input *GoldenAllocation) *GoldenAllocation {
	if input == nil {
		return nil
	}
	clone := *input
	clone.ExpectedState.Membership.PreviousRevisionID = cloneGoldenUUID(input.ExpectedState.Membership.PreviousRevisionID)
	clone.OrderingInputs = append([]GoldenFallbackOrderingInput(nil), input.OrderingInputs...)
	for index := range clone.OrderingInputs {
		clone.OrderingInputs[index].AcceptedSolveTime = cloneGoldenDuration(input.OrderingInputs[index].AcceptedSolveTime)
	}
	clone.Positions = append([]GoldenPositionAllocation(nil), input.Positions...)
	return &clone
}

func goldenFallbackError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %w: %s", ErrInvalidGoldenState, ErrInvalidGoldenFallback, fmt.Sprintf(format, arguments...))
}
