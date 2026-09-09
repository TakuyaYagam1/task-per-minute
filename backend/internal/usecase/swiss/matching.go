package swiss

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var (
	ErrInvalidMatching    = errors.New("invalid swiss matching input")
	ErrMatchingImpossible = errors.New("complete non-repeating swiss matching is impossible")
)

type Pair struct {
	FirstParticipantID  uuid.UUID
	SecondParticipantID uuid.UUID
}

func FindMatching(eligibleParticipantIDs []uuid.UUID, previousMeetings []Pair) ([]Pair, error) {
	if err := validateEligibleParticipants(eligibleParticipantIDs); err != nil {
		return nil, err
	}

	blocked, err := PreviousMeetingSet(previousMeetings)
	if err != nil {
		return nil, err
	}
	matching, ok := findMatching(eligibleParticipantIDs, blocked)
	if !ok {
		return nil, ErrMatchingImpossible
	}
	return matching, nil
}

func validateEligibleParticipants(participantIDs []uuid.UUID) error {
	if len(participantIDs) < domain.TournamentMinParticipants ||
		len(participantIDs) > domain.TournamentMaxParticipants ||
		len(participantIDs)%2 != 0 {
		return fmt.Errorf("%w: eligible participant count must be even and between %d and %d",
			ErrInvalidMatching, domain.TournamentMinParticipants, domain.TournamentMaxParticipants)
	}

	seen := make(map[uuid.UUID]struct{}, len(participantIDs))
	for _, participantID := range participantIDs {
		if participantID == uuid.Nil {
			return fmt.Errorf("%w: missing eligible participant identity", ErrInvalidMatching)
		}
		if _, exists := seen[participantID]; exists {
			return fmt.Errorf("%w: duplicate eligible participant", ErrInvalidMatching)
		}
		seen[participantID] = struct{}{}
	}
	return nil
}

type PairKey struct {
	first  uuid.UUID
	second uuid.UUID
}

func NewPairKey(first, second uuid.UUID) PairKey {
	if bytes.Compare(first[:], second[:]) > 0 {
		first, second = second, first
	}
	return PairKey{first: first, second: second}
}

func (k PairKey) Participants() (uuid.UUID, uuid.UUID) {
	return k.first, k.second
}

func PreviousMeetingSet(previousMeetings []Pair) (map[PairKey]struct{}, error) {
	meetings := make(map[PairKey]struct{}, len(previousMeetings))
	for _, meeting := range previousMeetings {
		if meeting.FirstParticipantID == uuid.Nil || meeting.SecondParticipantID == uuid.Nil {
			return nil, fmt.Errorf("%w: previous meeting has missing participant identity", ErrInvalidMatching)
		}
		if meeting.FirstParticipantID == meeting.SecondParticipantID {
			return nil, fmt.Errorf("%w: previous meeting is a self-pair", ErrInvalidMatching)
		}
		meetings[NewPairKey(meeting.FirstParticipantID, meeting.SecondParticipantID)] = struct{}{}
	}
	return meetings, nil
}

func findMatching(participantIDs []uuid.UUID, blocked map[PairKey]struct{}) ([]Pair, bool) {
	if len(participantIDs) == 0 {
		return []Pair{}, true
	}

	first := participantIDs[0]
	for candidateIndex := 1; candidateIndex < len(participantIDs); candidateIndex++ {
		second := participantIDs[candidateIndex]
		if _, metBefore := blocked[NewPairKey(first, second)]; metBefore {
			continue
		}

		remaining := make([]uuid.UUID, 0, len(participantIDs)-2)
		remaining = append(remaining, participantIDs[1:candidateIndex]...)
		remaining = append(remaining, participantIDs[candidateIndex+1:]...)
		tail, ok := findMatching(remaining, blocked)
		if ok {
			return append([]Pair{{
				FirstParticipantID:  first,
				SecondParticipantID: second,
			}}, tail...), true
		}
	}
	return nil, false
}
