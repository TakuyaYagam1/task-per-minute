package arena

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

const SwissAlternativeSearchAlgorithmV1 = "complete-backtracking-v1"

var (
	ErrInvalidPairingRepeatOverride      = errors.New("invalid pairing repeat override")
	ErrPairingRepeatOverrideNotConfirmed = errors.New("pairing repeat override is not confirmed")
	ErrPairingRepeatOverrideNotRequired  = errors.New("pairing repeat override is not required")
	ErrPairingRepeatOverrideConflict     = errors.New("pairing repeat override conflicts with recorded command")
)

type PairingRepeatOverrideCommand struct {
	ID                   uuid.UUID
	ActorID              uuid.UUID
	Confirmed            bool
	Reason               string
	ConfirmedAt          time.Time
	RosterParticipantIDs []uuid.UUID
	Round                ManualSwissRound
	PreviousMeetings     []SwissPair
}

type PairingAlternativeSearch struct {
	AlgorithmVersion string
	Complete         bool
	Pairings         []SwissPair
}

type PairingRepeatOverride struct {
	CommandID            uuid.UUID
	RoundID              uuid.UUID
	ActorID              uuid.UUID
	Reason               string
	ConfirmedAt          time.Time
	RosterParticipantIDs []uuid.UUID
	ProposedPairings     []SwissPair
	ByeParticipantID     uuid.UUID
	PreviousMeetings     []SwissPair
	RepeatedPairings     []SwissPair
	AlternativeSearch    PairingAlternativeSearch
}

func ConfirmPairingRepeatOverride(
	existing *PairingRepeatOverride,
	command PairingRepeatOverrideCommand,
) (PairingRepeatOverride, bool, error) {
	if !command.Confirmed {
		return PairingRepeatOverride{}, false, ErrPairingRepeatOverrideNotConfirmed
	}
	record, err := newPairingRepeatOverride(command)
	if err != nil {
		return PairingRepeatOverride{}, false, err
	}
	if existing == nil {
		return clonePairingRepeatOverride(record), true, nil
	}
	if err := existing.Validate(); err != nil {
		return PairingRepeatOverride{}, false, fmt.Errorf("%w: existing record: %w", ErrPairingRepeatOverrideConflict, err)
	}
	if !reflect.DeepEqual(*existing, record) {
		return PairingRepeatOverride{}, false, ErrPairingRepeatOverrideConflict
	}
	return clonePairingRepeatOverride(*existing), false, nil
}

func newPairingRepeatOverride(command PairingRepeatOverrideCommand) (PairingRepeatOverride, error) {
	if command.ID == uuid.Nil || command.ActorID == uuid.Nil {
		return PairingRepeatOverride{}, fmt.Errorf("%w: missing command or actor identity", ErrInvalidPairingRepeatOverride)
	}
	reason := strings.TrimSpace(command.Reason)
	if reason == "" {
		return PairingRepeatOverride{}, fmt.Errorf("%w: blank reason", ErrInvalidPairingRepeatOverride)
	}
	if command.ConfirmedAt.IsZero() || command.ConfirmedAt.Location() != time.UTC {
		return PairingRepeatOverride{}, fmt.Errorf("%w: confirmation timestamp must be server UTC", ErrInvalidPairingRepeatOverride)
	}
	if err := validateManualSwissRoundShape(command.RosterParticipantIDs, command.Round); err != nil {
		return PairingRepeatOverride{}, fmt.Errorf("%w: %w", ErrInvalidPairingRepeatOverride, err)
	}

	previous, err := canonicalSwissPairs(command.PreviousMeetings)
	if err != nil {
		return PairingRepeatOverride{}, err
	}
	blocked, err := previousMeetingSet(previous)
	if err != nil {
		return PairingRepeatOverride{}, fmt.Errorf("%w: %w", ErrInvalidPairingRepeatOverride, err)
	}
	repeated, err := canonicalSwissPairs(repeatedPairings(command.Round.Pairings, blocked))
	if err != nil {
		return PairingRepeatOverride{}, err
	}
	if len(repeated) == 0 {
		return PairingRepeatOverride{}, ErrPairingRepeatOverrideNotRequired
	}

	roster, err := canonicalParticipantIDs(command.RosterParticipantIDs)
	if err != nil {
		return PairingRepeatOverride{}, err
	}
	proposed, err := canonicalSwissPairs(command.Round.Pairings)
	if err != nil {
		return PairingRepeatOverride{}, err
	}
	alternative, err := completeNonRepeatingAlternative(roster, command.Round.ByeParticipantID, previous)
	if err != nil {
		return PairingRepeatOverride{}, err
	}
	record := PairingRepeatOverride{
		CommandID:            command.ID,
		RoundID:              command.Round.ID,
		ActorID:              command.ActorID,
		Reason:               reason,
		ConfirmedAt:          command.ConfirmedAt,
		RosterParticipantIDs: roster,
		ProposedPairings:     proposed,
		ByeParticipantID:     command.Round.ByeParticipantID,
		PreviousMeetings:     previous,
		RepeatedPairings:     repeated,
		AlternativeSearch: PairingAlternativeSearch{
			AlgorithmVersion: SwissAlternativeSearchAlgorithmV1,
			Complete:         true,
			Pairings:         alternative,
		},
	}
	if err := record.Validate(); err != nil {
		return PairingRepeatOverride{}, err
	}
	return record, nil
}

func (o PairingRepeatOverride) Validate() error {
	if err := o.validateMetadata(); err != nil {
		return err
	}
	if err := o.validatePairingEvidence(); err != nil {
		return err
	}
	return o.validateAlternativeEvidence()
}

func (o PairingRepeatOverride) validateMetadata() error {
	if o.CommandID == uuid.Nil || o.RoundID == uuid.Nil || o.ActorID == uuid.Nil {
		return fmt.Errorf("%w: missing identity", ErrInvalidPairingRepeatOverride)
	}
	if o.Reason == "" || o.Reason != strings.TrimSpace(o.Reason) {
		return fmt.Errorf("%w: invalid reason", ErrInvalidPairingRepeatOverride)
	}
	if o.ConfirmedAt.IsZero() || o.ConfirmedAt.Location() != time.UTC {
		return fmt.Errorf("%w: confirmation timestamp must be server UTC", ErrInvalidPairingRepeatOverride)
	}
	return nil
}

func (o PairingRepeatOverride) validatePairingEvidence() error {
	roster, err := canonicalParticipantIDs(o.RosterParticipantIDs)
	if err != nil {
		return err
	}
	if !equalUUIDs(roster, o.RosterParticipantIDs) {
		return fmt.Errorf("%w: roster evidence is not canonical", ErrInvalidPairingRepeatOverride)
	}
	proposed, err := canonicalSwissPairs(o.ProposedPairings)
	if err != nil {
		return err
	}
	if !equalSwissPairs(proposed, o.ProposedPairings) {
		return fmt.Errorf("%w: proposed pairing evidence is not canonical", ErrInvalidPairingRepeatOverride)
	}
	round := ManualSwissRound{ID: o.RoundID, Pairings: proposed, ByeParticipantID: o.ByeParticipantID}
	if err := validateManualSwissRoundShape(roster, round); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidPairingRepeatOverride, err)
	}
	previous, err := canonicalSwissPairs(o.PreviousMeetings)
	if err != nil {
		return err
	}
	if !equalSwissPairs(previous, o.PreviousMeetings) {
		return fmt.Errorf("%w: previous meeting evidence is not canonical", ErrInvalidPairingRepeatOverride)
	}
	blocked, err := previousMeetingSet(previous)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidPairingRepeatOverride, err)
	}
	repeated, err := canonicalSwissPairs(repeatedPairings(proposed, blocked))
	if err != nil {
		return err
	}
	if len(repeated) == 0 || !equalSwissPairs(repeated, o.RepeatedPairings) {
		return fmt.Errorf("%w: repeated pairing evidence does not match", ErrInvalidPairingRepeatOverride)
	}
	return nil
}

func (o PairingRepeatOverride) validateAlternativeEvidence() error {
	if !o.AlternativeSearch.Complete || o.AlternativeSearch.AlgorithmVersion != SwissAlternativeSearchAlgorithmV1 {
		return fmt.Errorf("%w: incomplete alternative search", ErrInvalidPairingRepeatOverride)
	}
	alternative, err := completeNonRepeatingAlternative(
		o.RosterParticipantIDs,
		o.ByeParticipantID,
		o.PreviousMeetings,
	)
	if err != nil {
		return err
	}
	if !equalSwissPairs(alternative, o.AlternativeSearch.Pairings) {
		return fmt.Errorf("%w: alternative search evidence does not match", ErrInvalidPairingRepeatOverride)
	}
	return nil
}

func (o PairingRepeatOverride) matches(
	rosterParticipantIDs []uuid.UUID,
	round ManualSwissRound,
	previousMeetings []SwissPair,
	repeated []SwissPair,
) bool {
	roster, rosterErr := canonicalParticipantIDs(rosterParticipantIDs)
	proposed, proposedErr := canonicalSwissPairs(round.Pairings)
	previous, previousErr := canonicalSwissPairs(previousMeetings)
	repeatedCanonical, repeatedErr := canonicalSwissPairs(repeated)
	return rosterErr == nil && proposedErr == nil && previousErr == nil && repeatedErr == nil &&
		o.RoundID == round.ID && o.ByeParticipantID == round.ByeParticipantID &&
		equalUUIDs(o.RosterParticipantIDs, roster) &&
		equalSwissPairs(o.ProposedPairings, proposed) &&
		equalSwissPairs(o.PreviousMeetings, previous) &&
		equalSwissPairs(o.RepeatedPairings, repeatedCanonical)
}

func completeNonRepeatingAlternative(
	rosterParticipantIDs []uuid.UUID,
	byeParticipantID uuid.UUID,
	previousMeetings []SwissPair,
) ([]SwissPair, error) {
	eligible := make([]uuid.UUID, 0, len(rosterParticipantIDs))
	for _, participantID := range rosterParticipantIDs {
		if participantID != byeParticipantID {
			eligible = append(eligible, participantID)
		}
	}
	pairings, err := FindSwissMatching(eligible, previousMeetings)
	if errors.Is(err, ErrSwissMatchingImpossible) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w: alternative search: %w", ErrInvalidPairingRepeatOverride, err)
	}
	return canonicalSwissPairs(pairings)
}

func canonicalParticipantIDs(participantIDs []uuid.UUID) ([]uuid.UUID, error) {
	if _, err := manualPairingRosterSet(participantIDs); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidPairingRepeatOverride, err)
	}
	result := append([]uuid.UUID(nil), participantIDs...)
	sort.Slice(result, func(i, j int) bool {
		return bytes.Compare(result[i][:], result[j][:]) < 0
	})
	return result, nil
}

func canonicalSwissPairs(pairings []SwissPair) ([]SwissPair, error) {
	result := make([]SwissPair, len(pairings))
	seen := make(map[swissPairKey]struct{}, len(pairings))
	for i, pairing := range pairings {
		if pairing.FirstParticipantID == uuid.Nil || pairing.SecondParticipantID == uuid.Nil {
			return nil, fmt.Errorf("%w: pairing has missing participant identity", ErrInvalidPairingRepeatOverride)
		}
		if pairing.FirstParticipantID == pairing.SecondParticipantID {
			return nil, fmt.Errorf("%w: pairing is a self-pair", ErrInvalidPairingRepeatOverride)
		}
		key := newSwissPairKey(pairing.FirstParticipantID, pairing.SecondParticipantID)
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("%w: duplicate pairing evidence", ErrInvalidPairingRepeatOverride)
		}
		seen[key] = struct{}{}
		result[i] = SwissPair{FirstParticipantID: key.first, SecondParticipantID: key.second}
	}
	sort.Slice(result, func(i, j int) bool {
		if comparison := bytes.Compare(result[i].FirstParticipantID[:], result[j].FirstParticipantID[:]); comparison != 0 {
			return comparison < 0
		}
		return bytes.Compare(result[i].SecondParticipantID[:], result[j].SecondParticipantID[:]) < 0
	})
	return result, nil
}

func equalUUIDs(first, second []uuid.UUID) bool {
	if len(first) != len(second) {
		return false
	}
	for i := range first {
		if first[i] != second[i] {
			return false
		}
	}
	return true
}

func clonePairingRepeatOverride(source PairingRepeatOverride) PairingRepeatOverride {
	result := source
	result.RosterParticipantIDs = append([]uuid.UUID(nil), source.RosterParticipantIDs...)
	result.ProposedPairings = append([]SwissPair(nil), source.ProposedPairings...)
	result.PreviousMeetings = append([]SwissPair(nil), source.PreviousMeetings...)
	result.RepeatedPairings = append([]SwissPair(nil), source.RepeatedPairings...)
	result.AlternativeSearch.Pairings = append([]SwissPair(nil), source.AlternativeSearch.Pairings...)
	return result
}
