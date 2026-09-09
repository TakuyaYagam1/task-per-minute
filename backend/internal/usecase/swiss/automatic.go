package swiss

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const (
	automaticPairingParticipantPrefix = "participant:"
	automaticPairingMeetingPrefix     = "meeting:"
)

var ErrInvalidAutomaticPairing = errors.New("invalid automatic swiss pairing")

type AutomaticPairing struct {
	Pairings []Pair
	Evidence domain.DecisionEvidence
}

func GenerateAutomaticPairing(
	evidenceID uuid.UUID,
	roundID uuid.UUID,
	eligibleParticipantIDs []uuid.UUID,
	previousMeetings []Pair,
	decidedAt time.Time,
) (AutomaticPairing, error) {
	inputs, err := BuildAutomaticPairingInputs(eligibleParticipantIDs, previousMeetings)
	if err != nil {
		return AutomaticPairing{}, err
	}
	evidence, err := domain.NewDecisionEvidence(
		evidenceID,
		domain.DecisionPurposePairing,
		domain.DecisionAlgorithmV1,
		inputs,
		roundID,
		decidedAt,
	)
	if err != nil {
		return AutomaticPairing{}, fmt.Errorf("%w: %w", ErrInvalidAutomaticPairing, err)
	}
	pairings, err := replayAutomaticPairing(evidence)
	if err != nil {
		return AutomaticPairing{}, err
	}
	return AutomaticPairing{
		Pairings: append([]Pair(nil), pairings...),
		Evidence: evidence,
	}, nil
}

func ReplayAutomaticPairing(record AutomaticPairing) ([]Pair, error) {
	pairings, err := replayAutomaticPairing(record.Evidence)
	if err != nil {
		return nil, err
	}
	if !EqualPairs(pairings, record.Pairings) {
		return nil, domain.ErrDecisionReplayMismatch
	}
	return append([]Pair(nil), pairings...), nil
}

func BuildAutomaticPairingInputs(eligibleParticipantIDs []uuid.UUID, previousMeetings []Pair) ([]string, error) {
	if err := validateEligibleParticipants(eligibleParticipantIDs); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidAutomaticPairing, err)
	}
	eligible := make(map[uuid.UUID]struct{}, len(eligibleParticipantIDs))
	inputs := make([]string, 0, len(eligibleParticipantIDs)+len(previousMeetings))
	for _, participantID := range eligibleParticipantIDs {
		eligible[participantID] = struct{}{}
		inputs = append(inputs, automaticPairingParticipantPrefix+participantID.String())
	}
	seenMeetings := make(map[PairKey]struct{}, len(previousMeetings))
	for _, meeting := range previousMeetings {
		if meeting.FirstParticipantID == uuid.Nil || meeting.SecondParticipantID == uuid.Nil {
			return nil, fmt.Errorf("%w: previous meeting has missing participant identity", ErrInvalidAutomaticPairing)
		}
		if meeting.FirstParticipantID == meeting.SecondParticipantID {
			return nil, fmt.Errorf("%w: previous meeting is a self-pair", ErrInvalidAutomaticPairing)
		}
		if _, ok := eligible[meeting.FirstParticipantID]; !ok {
			return nil, fmt.Errorf("%w: previous meeting contains foreign participant", ErrInvalidAutomaticPairing)
		}
		if _, ok := eligible[meeting.SecondParticipantID]; !ok {
			return nil, fmt.Errorf("%w: previous meeting contains foreign participant", ErrInvalidAutomaticPairing)
		}
		key := NewPairKey(meeting.FirstParticipantID, meeting.SecondParticipantID)
		if _, duplicate := seenMeetings[key]; duplicate {
			return nil, fmt.Errorf("%w: duplicate previous meeting", ErrInvalidAutomaticPairing)
		}
		seenMeetings[key] = struct{}{}
		first, second := key.Participants()
		inputs = append(inputs, automaticPairingMeetingPrefix+first.String()+":"+second.String())
	}
	return inputs, nil
}

func replayAutomaticPairing(evidence domain.DecisionEvidence) ([]Pair, error) {
	if evidence.Purpose != domain.DecisionPurposePairing {
		return nil, fmt.Errorf("%w: decision purpose is %q", ErrInvalidAutomaticPairing, evidence.Purpose)
	}
	result, err := evidence.Replay()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidAutomaticPairing, err)
	}
	participants := make([]uuid.UUID, 0, len(result))
	previousMeetings := make([]Pair, 0, len(result))
	for _, value := range result {
		switch {
		case strings.HasPrefix(value, automaticPairingParticipantPrefix):
			participantID, parseErr := uuid.Parse(strings.TrimPrefix(value, automaticPairingParticipantPrefix))
			if parseErr != nil {
				return nil, fmt.Errorf("%w: invalid participant input", ErrInvalidAutomaticPairing)
			}
			participants = append(participants, participantID)
		case strings.HasPrefix(value, automaticPairingMeetingPrefix):
			meeting, parseErr := parseAutomaticPairingMeeting(strings.TrimPrefix(value, automaticPairingMeetingPrefix))
			if parseErr != nil {
				return nil, parseErr
			}
			previousMeetings = append(previousMeetings, meeting)
		default:
			return nil, fmt.Errorf("%w: unknown normalized input", ErrInvalidAutomaticPairing)
		}
	}
	if _, err := BuildAutomaticPairingInputs(participants, previousMeetings); err != nil {
		return nil, err
	}
	pairings, err := FindMatching(participants, previousMeetings)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidAutomaticPairing, err)
	}
	return pairings, nil
}

func parseAutomaticPairingMeeting(value string) (Pair, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return Pair{}, fmt.Errorf("%w: invalid previous meeting input", ErrInvalidAutomaticPairing)
	}
	first, firstErr := uuid.Parse(parts[0])
	second, secondErr := uuid.Parse(parts[1])
	if firstErr != nil || secondErr != nil {
		return Pair{}, fmt.Errorf("%w: invalid previous meeting input", ErrInvalidAutomaticPairing)
	}
	return Pair{FirstParticipantID: first, SecondParticipantID: second}, nil
}

func EqualPairs(first, second []Pair) bool {
	if len(first) != len(second) {
		return false
	}
	for i := range first {
		if !bytes.Equal(first[i].FirstParticipantID[:], second[i].FirstParticipantID[:]) ||
			!bytes.Equal(first[i].SecondParticipantID[:], second[i].SecondParticipantID[:]) {
			return false
		}
	}
	return true
}
