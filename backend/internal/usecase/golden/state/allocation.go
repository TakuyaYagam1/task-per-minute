package state

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenplan "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/plan"
)

type allocationCommand struct {
	Scope               GoldenStateScope
	CommandID           uuid.UUID
	AllocationID        uuid.UUID
	ExpectedState       GoldenStateExpectation
	NextStateRevisionID uuid.UUID
}

func buildGoldenAllocation(
	state GoldenState,
	command allocationCommand,
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
		excludedSeeds := make([]goldenplan.GroupMemberSeed, 0, len(state.Group.Members))
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
	want, err := buildGoldenAllocation(state, allocationCommand{
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
		wrongPredecessor, !domain.IsValidServerTime(allocation.AllocatedAt),
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

func orderGoldenFallbackSeeds(members []goldenplan.GroupMemberSeed) ([]goldenplan.GroupMemberSeed, error) {
	ordered := goldenplan.CloneGroupMemberSeeds(members)
	seenIDs := make(map[uuid.UUID]struct{}, len(ordered))
	seenSeeds := make(map[int]struct{}, len(ordered))
	for _, member := range ordered {
		if err := goldenplan.ValidateGroupMember(member); err != nil {
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

func goldenFallbackLess(first, second goldenplan.GroupMemberSeed) bool {
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

func goldenFallbackInputs(members []goldenplan.GroupMemberSeed) []GoldenFallbackOrderingInput {
	result := make([]GoldenFallbackOrderingInput, len(members))
	for index, member := range members {
		result[index] = GoldenFallbackOrderingInput{
			ParticipantID: member.ParticipantID, Points: member.Points, Buchholz: member.Buchholz,
			HeadToHeadPoints: member.HeadToHeadPoints, HeadToHeadApplied: member.HeadToHeadApplied,
			EffectiveTime: member.EffectiveTime, AcceptedSolveTime: cloneGoldenFallbackDuration(member.AcceptedSolveTime),
			Seed: member.Seed,
		}
	}
	return result
}

func cloneGoldenFallbackDuration(value *time.Duration) *time.Duration {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
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

func cloneGoldenAllocation(input *GoldenAllocation) *GoldenAllocation {
	if input == nil {
		return nil
	}
	clone := *input
	clone.ExpectedState.Membership.PreviousRevisionID = cloneGoldenUUID(input.ExpectedState.Membership.PreviousRevisionID)
	clone.OrderingInputs = append([]GoldenFallbackOrderingInput(nil), input.OrderingInputs...)
	for index := range clone.OrderingInputs {
		clone.OrderingInputs[index].AcceptedSolveTime = cloneGoldenFallbackDuration(input.OrderingInputs[index].AcceptedSolveTime)
	}
	clone.Positions = append([]GoldenPositionAllocation(nil), input.Positions...)
	return &clone
}

func goldenFallbackError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %w: %s", ErrInvalidGoldenState, ErrInvalidGoldenFallback, fmt.Sprintf(format, arguments...))
}
