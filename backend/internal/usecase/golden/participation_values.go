package golden

import (
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func goldenAny(conditions ...bool) bool {
	for _, condition := range conditions {
		if condition {
			return true
		}
	}
	return false
}

func goldenStatePayload(state GoldenState) ([]byte, error) {
	type payloadDocument struct {
		Scope              GoldenStateScope
		TopologyDigest     [sha256.Size]byte
		Group              domain.GoldenGroupState
		Plan               GoldenPlanStateBinding
		Membership         GoldenMembershipRevision
		RevisionID         uuid.UUID
		Revision           int64
		PreviousRevisionID *uuid.UUID
		Windows            []GoldenReadyWindow
		ReadyEvents        []GoldenReadyEvent
		NoShows            []GoldenNoShowResolution
		Allocation         *GoldenAllocation
	}
	return goldenEncode(payloadDocument{
		Scope: state.Scope, TopologyDigest: state.Topology.PayloadDigest(), Group: state.Group,
		Plan: state.Plan, Membership: state.Membership, RevisionID: state.RevisionID,
		Revision: state.Revision, PreviousRevisionID: state.PreviousRevisionID,
		Windows: state.Windows, ReadyEvents: state.ReadyEvents, NoShows: state.NoShows,
		Allocation: state.Allocation,
	})
}

func goldenMembershipDigest(members []domain.GoldenMember) [sha256.Size]byte {
	type memberDocument struct {
		ParticipantID uuid.UUID
		Excluded      bool
	}
	document := make([]memberDocument, len(members))
	for index, member := range members {
		document[index] = memberDocument{ParticipantID: member.ParticipantID, Excluded: member.Excluded}
	}
	sort.Slice(document, func(i, j int) bool {
		return bytes.Compare(document[i].ParticipantID[:], document[j].ParticipantID[:]) < 0
	})
	payload, _ := goldenEncode(document)
	return sha256.Sum256(payload)
}

func goldenParticipantSetDigest(participantIDs []uuid.UUID) [sha256.Size]byte {
	canonical := append([]uuid.UUID(nil), participantIDs...)
	canonicalGoldenIDs(canonical)
	payload, _ := goldenEncode(canonical)
	return sha256.Sum256(payload)
}

func goldenEncode(value any) ([]byte, error) {
	var buffer bytes.Buffer
	if err := gob.NewEncoder(&buffer).Encode(value); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func canonicalGoldenIDs(values []uuid.UUID) {
	sort.Slice(values, func(i, j int) bool { return bytes.Compare(values[i][:], values[j][:]) < 0 })
}

func goldenIDsAreCanonical(values []uuid.UUID) bool {
	for index, value := range values {
		if value == uuid.Nil || (index > 0 && bytes.Compare(values[index-1][:], value[:]) >= 0) {
			return false
		}
	}
	return true
}

func goldenIDsSubset(values, superset []uuid.UUID) bool {
	for _, value := range values {
		if !goldenIDsContain(superset, value) {
			return false
		}
	}
	return true
}

func equalGoldenIDs(first, second []uuid.UUID) bool {
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

func goldenIDsContain(values []uuid.UUID, target uuid.UUID) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func removeGoldenID(values []uuid.UUID, target uuid.UUID) []uuid.UUID {
	result := make([]uuid.UUID, 0, len(values))
	for _, value := range values {
		if value != target {
			result = append(result, value)
		}
	}
	return result
}

func goldenIDState(values []uuid.UUID) map[uuid.UUID]bool {
	result := make(map[uuid.UUID]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func goldenStateIDs(values map[uuid.UUID]bool) []uuid.UUID {
	result := make([]uuid.UUID, 0, len(values))
	for value, present := range values {
		if present {
			result = append(result, value)
		}
	}
	canonicalGoldenIDs(result)
	return result
}

func FindMember(members []domain.GoldenMember, participantID uuid.UUID) (domain.GoldenMember, bool) {
	for _, member := range members {
		if member.ParticipantID == participantID {
			return member, true
		}
	}
	return domain.GoldenMember{}, false
}

func goldenAttempt(attempts []domain.GoldenAttempt, attemptID uuid.UUID) (domain.GoldenAttempt, bool) {
	for _, attempt := range attempts {
		if attempt.ID == attemptID {
			return attempt, true
		}
	}
	return domain.GoldenAttempt{}, false
}

func goldenAttemptContains(attempts []domain.GoldenAttempt, attemptID, participantID uuid.UUID) bool {
	attempt, found := goldenAttempt(attempts, attemptID)
	return found && goldenIDsContain(attempt.ParticipantIDs, participantID)
}

func goldenWindowIndex(windows []GoldenReadyWindow, windowID uuid.UUID) int {
	for index := range windows {
		if windows[index].ID == windowID {
			return index
		}
	}
	return -1
}

func goldenReadyEventByCommand(events []GoldenReadyEvent, commandID uuid.UUID) (GoldenReadyEvent, bool) {
	for _, event := range events {
		if event.CommandID == commandID {
			return event, true
		}
	}
	return GoldenReadyEvent{}, false
}

func goldenCommandIDRetainedOutsideReady(state GoldenState, commandID uuid.UUID) bool {
	if _, found := goldenNoShowByCommand(state.NoShows, commandID); found {
		return true
	}
	return state.Allocation != nil && state.Allocation.CommandID == commandID
}

func CloneGroup(state domain.GoldenGroupState) domain.GoldenGroupState {
	clone := state
	clone.Members = append([]domain.GoldenMember(nil), state.Members...)
	clone.Attempts = make([]domain.GoldenAttempt, len(state.Attempts))
	for index, attempt := range state.Attempts {
		clone.Attempts[index] = CloneAttempt(attempt)
	}
	return clone
}

func CloneAttempt(attempt domain.GoldenAttempt) domain.GoldenAttempt {
	clone := attempt
	clone.PreviousAttemptID = cloneGoldenUUID(attempt.PreviousAttemptID)
	clone.ParticipantIDs = append([]uuid.UUID(nil), attempt.ParticipantIDs...)
	clone.RetainedAt = cloneGoldenTime(attempt.RetainedAt)
	clone.StartedAt = cloneGoldenTime(attempt.StartedAt)
	clone.FinishedAt = cloneGoldenTime(attempt.FinishedAt)
	return clone
}

func CloneReadyWindow(window GoldenReadyWindow) GoldenReadyWindow {
	clone := window
	clone.PreviousRevisionID = cloneGoldenUUID(window.PreviousRevisionID)
	clone.ReadinessPreviousRevisionID = cloneGoldenUUID(window.ReadinessPreviousRevisionID)
	clone.PresencePreviousRevisionID = cloneGoldenUUID(window.PresencePreviousRevisionID)
	clone.ReadyParticipantIDs = append([]uuid.UUID(nil), window.ReadyParticipantIDs...)
	clone.BasePresentParticipantIDs = append([]uuid.UUID(nil), window.BasePresentParticipantIDs...)
	clone.PresentParticipantIDs = append([]uuid.UUID(nil), window.PresentParticipantIDs...)
	return clone
}

func CloneExpectation(input GoldenStateExpectation) GoldenStateExpectation {
	clone := input
	clone.Membership.PreviousRevisionID = cloneGoldenUUID(input.Membership.PreviousRevisionID)
	return clone
}

func cloneGoldenUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneGoldenTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func goldenUUID(value uuid.UUID) *uuid.UUID { return &value }

func goldenStateError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidGoldenState, fmt.Sprintf(format, arguments...))
}
