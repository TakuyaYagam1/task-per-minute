package arena

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

var ErrInvalidAutomaticSwissPairing = errors.New("invalid automatic swiss pairing")

type AutomaticSwissPairing struct {
	Pairings []SwissPair
	Evidence domain.ArenaDecisionEvidence
}

func GenerateAutomaticSwissPairing(
	evidenceID uuid.UUID,
	roundID uuid.UUID,
	eligibleParticipantIDs []uuid.UUID,
	previousMeetings []SwissPair,
	decidedAt time.Time,
) (AutomaticSwissPairing, error) {
	inputs, err := automaticPairingInputs(eligibleParticipantIDs, previousMeetings)
	if err != nil {
		return AutomaticSwissPairing{}, err
	}
	evidence, err := domain.NewArenaDecisionEvidence(
		evidenceID,
		domain.ArenaDecisionPurposePairing,
		domain.ArenaDecisionAlgorithmV1,
		inputs,
		roundID,
		decidedAt,
	)
	if err != nil {
		return AutomaticSwissPairing{}, fmt.Errorf("%w: %w", ErrInvalidAutomaticSwissPairing, err)
	}
	pairings, err := replayAutomaticSwissPairing(evidence)
	if err != nil {
		return AutomaticSwissPairing{}, err
	}
	return AutomaticSwissPairing{
		Pairings: append([]SwissPair(nil), pairings...),
		Evidence: evidence,
	}, nil
}

func ReplayAutomaticSwissPairing(record AutomaticSwissPairing) ([]SwissPair, error) {
	pairings, err := replayAutomaticSwissPairing(record.Evidence)
	if err != nil {
		return nil, err
	}
	if !equalSwissPairs(pairings, record.Pairings) {
		return nil, domain.ErrArenaDecisionReplayMismatch
	}
	return append([]SwissPair(nil), pairings...), nil
}

func automaticPairingInputs(eligibleParticipantIDs []uuid.UUID, previousMeetings []SwissPair) ([]string, error) {
	if err := validateEligibleParticipants(eligibleParticipantIDs); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidAutomaticSwissPairing, err)
	}
	eligible := make(map[uuid.UUID]struct{}, len(eligibleParticipantIDs))
	inputs := make([]string, 0, len(eligibleParticipantIDs)+len(previousMeetings))
	for _, participantID := range eligibleParticipantIDs {
		eligible[participantID] = struct{}{}
		inputs = append(inputs, automaticPairingParticipantPrefix+participantID.String())
	}
	seenMeetings := make(map[swissPairKey]struct{}, len(previousMeetings))
	for _, meeting := range previousMeetings {
		if meeting.FirstParticipantID == uuid.Nil || meeting.SecondParticipantID == uuid.Nil {
			return nil, fmt.Errorf("%w: previous meeting has missing participant identity", ErrInvalidAutomaticSwissPairing)
		}
		if meeting.FirstParticipantID == meeting.SecondParticipantID {
			return nil, fmt.Errorf("%w: previous meeting is a self-pair", ErrInvalidAutomaticSwissPairing)
		}
		if _, ok := eligible[meeting.FirstParticipantID]; !ok {
			return nil, fmt.Errorf("%w: previous meeting contains foreign participant", ErrInvalidAutomaticSwissPairing)
		}
		if _, ok := eligible[meeting.SecondParticipantID]; !ok {
			return nil, fmt.Errorf("%w: previous meeting contains foreign participant", ErrInvalidAutomaticSwissPairing)
		}
		key := newSwissPairKey(meeting.FirstParticipantID, meeting.SecondParticipantID)
		if _, duplicate := seenMeetings[key]; duplicate {
			return nil, fmt.Errorf("%w: duplicate previous meeting", ErrInvalidAutomaticSwissPairing)
		}
		seenMeetings[key] = struct{}{}
		inputs = append(inputs, automaticPairingMeetingPrefix+key.first.String()+":"+key.second.String())
	}
	return inputs, nil
}

func replayAutomaticSwissPairing(evidence domain.ArenaDecisionEvidence) ([]SwissPair, error) {
	if evidence.Purpose != domain.ArenaDecisionPurposePairing {
		return nil, fmt.Errorf("%w: decision purpose is %q", ErrInvalidAutomaticSwissPairing, evidence.Purpose)
	}
	result, err := evidence.Replay()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidAutomaticSwissPairing, err)
	}
	participants := make([]uuid.UUID, 0, len(result))
	previousMeetings := make([]SwissPair, 0, len(result))
	for _, value := range result {
		switch {
		case strings.HasPrefix(value, automaticPairingParticipantPrefix):
			participantID, parseErr := uuid.Parse(strings.TrimPrefix(value, automaticPairingParticipantPrefix))
			if parseErr != nil {
				return nil, fmt.Errorf("%w: invalid participant input", ErrInvalidAutomaticSwissPairing)
			}
			participants = append(participants, participantID)
		case strings.HasPrefix(value, automaticPairingMeetingPrefix):
			meeting, parseErr := parseAutomaticPairingMeeting(strings.TrimPrefix(value, automaticPairingMeetingPrefix))
			if parseErr != nil {
				return nil, parseErr
			}
			previousMeetings = append(previousMeetings, meeting)
		default:
			return nil, fmt.Errorf("%w: unknown normalized input", ErrInvalidAutomaticSwissPairing)
		}
	}
	if _, err := automaticPairingInputs(participants, previousMeetings); err != nil {
		return nil, err
	}
	pairings, err := FindSwissMatching(participants, previousMeetings)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidAutomaticSwissPairing, err)
	}
	return pairings, nil
}

func parseAutomaticPairingMeeting(value string) (SwissPair, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return SwissPair{}, fmt.Errorf("%w: invalid previous meeting input", ErrInvalidAutomaticSwissPairing)
	}
	first, firstErr := uuid.Parse(parts[0])
	second, secondErr := uuid.Parse(parts[1])
	if firstErr != nil || secondErr != nil {
		return SwissPair{}, fmt.Errorf("%w: invalid previous meeting input", ErrInvalidAutomaticSwissPairing)
	}
	return SwissPair{FirstParticipantID: first, SecondParticipantID: second}, nil
}

func equalSwissPairs(first, second []SwissPair) bool {
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
