package golden

import (
	"crypto/sha256"
	"math"
	"sort"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func applyGoldenReadyEvent(
	event GoldenReadyEvent,
	ready map[uuid.UUID]bool,
	present map[uuid.UUID]bool,
) (bool, error) {
	switch event.Type {
	case GoldenReadyEventAccepted:
		if goldenAny(event.ResultPresenceRevisionID != event.ExpectedWindow.PresenceRevisionID, !present[event.ParticipantID]) {
			return false, goldenStateError("invalid accepted-ready receipt")
		}
		ready[event.ParticipantID] = true
		return true, nil
	case GoldenReadyEventDisconnected:
		if event.ResultPresenceRevisionID == uuid.Nil {
			return false, goldenStateError("disconnect lacks presence revision")
		}
		delete(ready, event.ParticipantID)
		delete(present, event.ParticipantID)
	case GoldenReadyEventAlreadyReady:
		if goldenAny(
			!ready[event.ParticipantID],
			event.ResultWindowRevisionID != event.ExpectedWindow.RevisionID,
			event.ResultReadinessRevisionID != event.ExpectedWindow.ReadinessRevisionID,
			event.ResultPresenceRevisionID != event.ExpectedWindow.PresenceRevisionID,
		) {
			return false, goldenStateError("invalid already-ready receipt")
		}
	case GoldenReadyEventAlreadyAbsent:
		if goldenAny(
			present[event.ParticipantID],
			event.ResultWindowRevisionID != event.ExpectedWindow.RevisionID,
			event.ResultReadinessRevisionID != event.ExpectedWindow.ReadinessRevisionID,
			event.ResultPresenceRevisionID != event.ExpectedWindow.PresenceRevisionID,
		) {
			return false, goldenStateError("invalid already-absent receipt")
		}
	default:
		return false, goldenStateError("unknown retained ready event")
	}
	return false, nil
}

func validateGoldenReadyEventSuccessor(event GoldenReadyEvent) error {
	changesWindow := goldenReadyEventChangesWindow(event.Type)
	if goldenAny(
		event.ExpectedState.Revision == math.MaxInt64,
		event.ResultStateRevisionID == event.ExpectedState.RevisionID,
		changesWindow && event.ExpectedWindow.Revision == math.MaxInt64,
		changesWindow && event.ExpectedWindow.ReadinessRevision == math.MaxInt64,
		changesWindow && event.ResultWindowRevisionID == event.ExpectedWindow.RevisionID,
		changesWindow && event.ResultReadinessRevisionID == event.ExpectedWindow.ReadinessRevisionID,
	) {
		return goldenStateError("invalid retained ready revision successor")
	}
	return nil
}

func validateGoldenReadyEventChain(event, prior GoldenReadyEvent) error {
	windowStep := int64(0)
	if goldenReadyEventChangesWindow(prior.Type) {
		windowStep = 1
	}
	if goldenAny(
		event.ExpectedState.RevisionID != prior.ResultStateRevisionID,
		event.ExpectedState.Revision != prior.ExpectedState.Revision+1,
		event.ExpectedWindow.RevisionID != prior.ResultWindowRevisionID,
		event.ExpectedWindow.Revision != prior.ExpectedWindow.Revision+windowStep,
		event.ExpectedWindow.ReadinessRevisionID != prior.ResultReadinessRevisionID,
		event.ExpectedWindow.ReadinessRevision != prior.ExpectedWindow.ReadinessRevision+windowStep,
	) {
		return goldenStateError("retained ready revision chain is broken")
	}
	presenceStep := int64(0)
	if prior.Type == GoldenReadyEventDisconnected {
		presenceStep = 1
	}
	if goldenAny(
		event.ExpectedWindow.PresenceRevisionID != prior.ResultPresenceRevisionID,
		event.ExpectedWindow.PresenceRevision != prior.ExpectedWindow.PresenceRevision+presenceStep,
	) {
		return goldenStateError("retained presence revision chain is broken")
	}
	return nil
}

type goldenRetainedTransition struct {
	expected GoldenStateExpectation
	result   uuid.UUID
	ready    *GoldenReadyEvent
	noShow   *GoldenNoShowResolution
	allocate *GoldenAllocation
}

func validateGoldenTransitionChain(state GoldenState) error {
	transitions := goldenRetainedTransitions(state)
	if len(transitions) == 0 {
		return validateGoldenEmptyTransitionState(state)
	}
	sort.Slice(transitions, func(i, j int) bool {
		return transitions[i].expected.Revision < transitions[j].expected.Revision
	})
	if goldenAny(
		transitions[0].expected.Revision < 1,
		transitions[0].expected.Revision > math.MaxInt64-int64(len(transitions)),
		transitions[0].expected.Revision+int64(len(transitions)) != state.Revision,
	) {
		return goldenStateError("retained state transition count is inconsistent")
	}
	members := CloneGroup(state.Group).Members
	for index := range members {
		members[index].Excluded = false
	}
	membership := transitions[0].expected.Membership
	if membership.PayloadDigest != goldenMembershipDigest(members) {
		return goldenStateError("initial retained membership digest is inconsistent")
	}
	for index, transition := range transitions {
		if err := validateGoldenTransitionAuthority(state, transition, membership); err != nil {
			return err
		}
		if index > 0 {
			prior := transitions[index-1]
			if goldenAny(
				transition.expected.Revision != prior.expected.Revision+1,
				transition.expected.RevisionID != prior.result,
			) {
				return goldenStateError("retained state transition chain is broken")
			}
		}
		if goldenTransitionHasExcludedReady(transition, members) {
			return goldenNoShowError("excluded participant has a later ready receipt")
		}
		if transition.noShow != nil {
			var err error
			membership, err = advanceGoldenTransitionMembership(membership, members, *transition.noShow)
			if err != nil {
				return err
			}
		}
	}
	final := transitions[len(transitions)-1]
	if goldenAny(
		final.result != state.RevisionID,
		final.expected.Revision+1 != state.Revision,
		goldenWrongPreviousState(state.PreviousRevisionID, final.expected.RevisionID),
		!MembershipRevisionsEqual(membership, state.Membership),
	) {
		return goldenStateError("final retained transition does not link current state")
	}
	return nil
}

func validateGoldenEmptyTransitionState(state GoldenState) error {
	if state.Revision != 1 {
		return goldenStateError("state revision has no retained transition")
	}
	return nil
}

func goldenTransitionHasExcludedReady(
	transition goldenRetainedTransition,
	members []domain.GoldenMember,
) bool {
	return transition.ready != nil && goldenMemberIsExcluded(members, transition.ready.ParticipantID)
}

func goldenWrongPreviousState(previous *uuid.UUID, expected uuid.UUID) bool {
	return previous == nil || *previous != expected
}

func goldenRetainedTransitions(state GoldenState) []goldenRetainedTransition {
	transitions := make([]goldenRetainedTransition, 0, len(state.ReadyEvents)+len(state.NoShows)+1)
	for index := range state.ReadyEvents {
		event := &state.ReadyEvents[index]
		transitions = append(transitions, goldenRetainedTransition{
			expected: event.ExpectedState, result: event.ResultStateRevisionID, ready: event,
		})
	}
	for index := range state.NoShows {
		resolution := &state.NoShows[index]
		transitions = append(transitions, goldenRetainedTransition{
			expected: resolution.ExpectedState, result: resolution.ResultStateRevisionID, noShow: resolution,
		})
	}
	if state.Allocation != nil {
		transitions = append(transitions, goldenRetainedTransition{
			expected: state.Allocation.ExpectedState,
			result:   state.Allocation.ResultStateRevisionID,
			allocate: state.Allocation,
		})
	}
	return transitions
}

func validateGoldenTransitionAuthority(
	state GoldenState,
	transition goldenRetainedTransition,
	membership GoldenMembershipRevision,
) error {
	expected := transition.expected
	if goldenAny(
		expected.Scope != state.Scope, expected.Plan != state.Plan,
		expected.SourceProjectionRevisionID != state.Topology.SourceProjectionRevisionID(),
		expected.SourceProjectionPayloadDigest != state.Topology.SourceProjectionPayloadDigest(),
		expected.TopologyPayloadDigest != state.Topology.PayloadDigest(),
		expected.PayloadDigest == [sha256.Size]byte{}, transition.result == uuid.Nil,
		transition.result == expected.RevisionID,
		!MembershipRevisionsEqual(expected.Membership, membership),
	) {
		return goldenStateError("retained transition authority changed")
	}
	return nil
}

func goldenMemberIsExcluded(members []domain.GoldenMember, participantID uuid.UUID) bool {
	for _, member := range members {
		if member.ParticipantID == participantID {
			return member.Excluded
		}
	}
	return false
}

func advanceGoldenTransitionMembership(
	membership GoldenMembershipRevision,
	members []domain.GoldenMember,
	resolution GoldenNoShowResolution,
) (GoldenMembershipRevision, error) {
	if goldenAny(
		membership.Revision == math.MaxInt64,
		resolution.ResultMembershipRevisionID == membership.RevisionID,
	) {
		return GoldenMembershipRevision{}, goldenNoShowError("invalid retained membership successor")
	}
	previousRevisionID := membership.RevisionID
	membership.RevisionID = resolution.ResultMembershipRevisionID
	membership.Revision++
	membership.PreviousRevisionID = goldenUUID(previousRevisionID)
	for _, participantID := range resolution.ExcludedParticipantIDs {
		for memberIndex := range members {
			if members[memberIndex].ParticipantID == participantID {
				members[memberIndex].Excluded = true
			}
		}
	}
	membership.PayloadDigest = goldenMembershipDigest(members)
	return membership, nil
}

func stateValidGoldenRevisionPredecessor(current uuid.UUID, revision int64, previous *uuid.UUID) bool {
	return (revision == 1 && previous == nil) ||
		(revision > 1 && previous != nil && *previous != uuid.Nil && *previous != current)
}

func MembershipRevisionsEqual(first, second GoldenMembershipRevision) bool {
	if first.RevisionID != second.RevisionID || first.Revision != second.Revision ||
		first.PayloadDigest != second.PayloadDigest {
		return false
	}
	if first.PreviousRevisionID == nil || second.PreviousRevisionID == nil {
		return first.PreviousRevisionID == nil && second.PreviousRevisionID == nil
	}
	return *first.PreviousRevisionID == *second.PreviousRevisionID
}
