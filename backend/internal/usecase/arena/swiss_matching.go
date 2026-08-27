package arena

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var (
	ErrInvalidSwissMatching    = errors.New("invalid swiss matching input")
	ErrSwissMatchingImpossible = errors.New("complete non-repeating swiss matching is impossible")
)

type SwissPair struct {
	FirstParticipantID  uuid.UUID
	SecondParticipantID uuid.UUID
}

func FindSwissMatching(eligibleParticipantIDs []uuid.UUID, previousMeetings []SwissPair) ([]SwissPair, error) {
	if err := validateEligibleParticipants(eligibleParticipantIDs); err != nil {
		return nil, err
	}

	blocked, err := previousMeetingSet(previousMeetings)
	if err != nil {
		return nil, err
	}
	matching, ok := findSwissMatching(eligibleParticipantIDs, blocked)
	if !ok {
		return nil, ErrSwissMatchingImpossible
	}
	return matching, nil
}

func validateEligibleParticipants(participantIDs []uuid.UUID) error {
	if len(participantIDs) < domain.ArenaMinParticipants ||
		len(participantIDs) > domain.ArenaMaxParticipants ||
		len(participantIDs)%2 != 0 {
		return fmt.Errorf("%w: eligible participant count must be even and between %d and %d",
			ErrInvalidSwissMatching, domain.ArenaMinParticipants, domain.ArenaMaxParticipants)
	}

	seen := make(map[uuid.UUID]struct{}, len(participantIDs))
	for _, participantID := range participantIDs {
		if participantID == uuid.Nil {
			return fmt.Errorf("%w: missing eligible participant identity", ErrInvalidSwissMatching)
		}
		if _, exists := seen[participantID]; exists {
			return fmt.Errorf("%w: duplicate eligible participant", ErrInvalidSwissMatching)
		}
		seen[participantID] = struct{}{}
	}
	return nil
}

type swissPairKey struct {
	first  uuid.UUID
	second uuid.UUID
}

func previousMeetingSet(previousMeetings []SwissPair) (map[swissPairKey]struct{}, error) {
	meetings := make(map[swissPairKey]struct{}, len(previousMeetings))
	for _, meeting := range previousMeetings {
		if meeting.FirstParticipantID == uuid.Nil || meeting.SecondParticipantID == uuid.Nil {
			return nil, fmt.Errorf("%w: previous meeting has missing participant identity", ErrInvalidSwissMatching)
		}
		if meeting.FirstParticipantID == meeting.SecondParticipantID {
			return nil, fmt.Errorf("%w: previous meeting is a self-pair", ErrInvalidSwissMatching)
		}
		meetings[newSwissPairKey(meeting.FirstParticipantID, meeting.SecondParticipantID)] = struct{}{}
	}
	return meetings, nil
}

func newSwissPairKey(first, second uuid.UUID) swissPairKey {
	if bytes.Compare(first[:], second[:]) > 0 {
		first, second = second, first
	}
	return swissPairKey{first: first, second: second}
}

func findSwissMatching(participantIDs []uuid.UUID, blocked map[swissPairKey]struct{}) ([]SwissPair, bool) {
	if len(participantIDs) == 0 {
		return []SwissPair{}, true
	}

	first := participantIDs[0]
	for candidateIndex := 1; candidateIndex < len(participantIDs); candidateIndex++ {
		second := participantIDs[candidateIndex]
		if _, metBefore := blocked[newSwissPairKey(first, second)]; metBefore {
			continue
		}

		remaining := make([]uuid.UUID, 0, len(participantIDs)-2)
		remaining = append(remaining, participantIDs[1:candidateIndex]...)
		remaining = append(remaining, participantIDs[candidateIndex+1:]...)
		tail, ok := findSwissMatching(remaining, blocked)
		if ok {
			return append([]SwissPair{{
				FirstParticipantID:  first,
				SecondParticipantID: second,
			}}, tail...), true
		}
	}
	return nil, false
}
