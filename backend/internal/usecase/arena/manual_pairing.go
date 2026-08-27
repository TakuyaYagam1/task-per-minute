package arena

import (
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var (
	ErrInvalidManualSwissPairing           = errors.New("invalid manual swiss pairing")
	ErrManualPairingMissingParticipant     = errors.New("manual swiss pairing has missing participant")
	ErrManualPairingSelfPair               = errors.New("manual swiss pairing has self-pair")
	ErrManualPairingForeignParticipant     = errors.New("manual swiss pairing has foreign participant")
	ErrManualPairingDuplicateParticipant   = errors.New("manual swiss pairing uses a participant more than once")
	ErrManualPairingIncomplete             = errors.New("manual swiss pairing is incomplete")
	ErrManualPairingInvalidBye             = errors.New("manual swiss pairing has invalid bye")
	ErrManualPairingRepeatRequiresOverride = errors.New("manual swiss pairing repeat requires override")
	ErrManualPairingOverrideMismatch       = errors.New("manual swiss pairing override does not match round evidence")
)

type ManualSwissRound struct {
	ID               uuid.UUID
	Pairings         []SwissPair
	ByeParticipantID uuid.UUID
}

func ValidateManualSwissPairing(
	rosterParticipantIDs []uuid.UUID,
	round ManualSwissRound,
	previousMeetings []SwissPair,
	override *PairingRepeatOverride,
) error {
	if err := validateManualSwissRoundShape(rosterParticipantIDs, round); err != nil {
		return err
	}
	blocked, err := previousMeetingSet(previousMeetings)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidManualSwissPairing, err)
	}
	repeated := repeatedPairings(round.Pairings, blocked)
	if len(repeated) == 0 {
		if override != nil {
			return ErrManualPairingOverrideMismatch
		}
		return nil
	}
	if override == nil {
		return ErrManualPairingRepeatRequiresOverride
	}
	if err := override.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrManualPairingOverrideMismatch, err)
	}
	if !override.matches(rosterParticipantIDs, round, previousMeetings, repeated) {
		return ErrManualPairingOverrideMismatch
	}
	return nil
}

func validateManualSwissRoundShape(rosterParticipantIDs []uuid.UUID, round ManualSwissRound) error {
	if round.ID == uuid.Nil {
		return fmt.Errorf("%w: missing round identity", ErrInvalidManualSwissPairing)
	}
	roster, err := manualPairingRosterSet(rosterParticipantIDs)
	if err != nil {
		return err
	}
	if len(round.Pairings) != len(rosterParticipantIDs)/2 {
		return ErrManualPairingIncomplete
	}

	used, err := validateManualPairingParticipants(roster, round.Pairings)
	if err != nil {
		return err
	}
	if err := validateManualPairingBye(rosterParticipantIDs, roster, used, round.ByeParticipantID); err != nil {
		return err
	}
	if len(used) != len(rosterParticipantIDs) {
		return ErrManualPairingIncomplete
	}
	return nil
}

func validateManualPairingParticipants(
	roster map[uuid.UUID]struct{},
	pairings []SwissPair,
) (map[uuid.UUID]struct{}, error) {
	used := make(map[uuid.UUID]struct{}, len(roster))
	for _, pairing := range pairings {
		if pairing.FirstParticipantID == uuid.Nil || pairing.SecondParticipantID == uuid.Nil {
			return nil, ErrManualPairingMissingParticipant
		}
		if pairing.FirstParticipantID == pairing.SecondParticipantID {
			return nil, ErrManualPairingSelfPair
		}
		for _, participantID := range []uuid.UUID{pairing.FirstParticipantID, pairing.SecondParticipantID} {
			if _, ok := roster[participantID]; !ok {
				return nil, ErrManualPairingForeignParticipant
			}
			if _, duplicate := used[participantID]; duplicate {
				return nil, ErrManualPairingDuplicateParticipant
			}
			used[participantID] = struct{}{}
		}
	}
	return used, nil
}

func validateManualPairingBye(
	rosterParticipantIDs []uuid.UUID,
	roster map[uuid.UUID]struct{},
	used map[uuid.UUID]struct{},
	byeParticipantID uuid.UUID,
) error {
	if len(rosterParticipantIDs)%2 == 0 {
		if byeParticipantID != uuid.Nil {
			return ErrManualPairingInvalidBye
		}
		return nil
	}
	if byeParticipantID == uuid.Nil {
		return ErrManualPairingInvalidBye
	}
	if _, ok := roster[byeParticipantID]; !ok {
		return ErrManualPairingInvalidBye
	}
	if _, duplicate := used[byeParticipantID]; duplicate {
		return ErrManualPairingDuplicateParticipant
	}
	used[byeParticipantID] = struct{}{}
	return nil
}

func manualPairingRosterSet(participantIDs []uuid.UUID) (map[uuid.UUID]struct{}, error) {
	if len(participantIDs) < domain.ArenaMinParticipants || len(participantIDs) > domain.ArenaMaxParticipants {
		return nil, fmt.Errorf("%w: roster size must be between %d and %d",
			ErrInvalidManualSwissPairing, domain.ArenaMinParticipants, domain.ArenaMaxParticipants)
	}
	roster := make(map[uuid.UUID]struct{}, len(participantIDs))
	for _, participantID := range participantIDs {
		if participantID == uuid.Nil {
			return nil, ErrManualPairingMissingParticipant
		}
		if _, duplicate := roster[participantID]; duplicate {
			return nil, ErrManualPairingDuplicateParticipant
		}
		roster[participantID] = struct{}{}
	}
	return roster, nil
}

func repeatedPairings(pairings []SwissPair, previous map[swissPairKey]struct{}) []SwissPair {
	repeated := make([]SwissPair, 0)
	for _, pairing := range pairings {
		if _, ok := previous[newSwissPairKey(pairing.FirstParticipantID, pairing.SecondParticipantID)]; ok {
			repeated = append(repeated, pairing)
		}
	}
	return repeated
}
