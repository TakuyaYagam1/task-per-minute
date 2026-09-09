package swiss

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

const AlternativeSearchAlgorithmV1 = "complete-backtracking-v1"

var (
	ErrInvalidRepeatOverride      = errors.New("invalid pairing repeat override")
	ErrRepeatOverrideNotConfirmed = errors.New("pairing repeat override is not confirmed")
	ErrRepeatOverrideNotRequired  = errors.New("pairing repeat override is not required")
	ErrRepeatOverrideConflict     = errors.New("pairing repeat override conflicts with recorded command")
)

type RepeatOverrideCommand struct {
	ID                   uuid.UUID
	ActorID              uuid.UUID
	Confirmed            bool
	Reason               string
	ConfirmedAt          time.Time
	RosterParticipantIDs []uuid.UUID
	Round                ManualRound
	PreviousMeetings     []Pair
}

type AlternativeSearch struct {
	AlgorithmVersion string
	Complete         bool
	Pairings         []Pair
}

type RepeatOverride struct {
	CommandID            uuid.UUID
	RoundID              uuid.UUID
	ActorID              uuid.UUID
	Reason               string
	ConfirmedAt          time.Time
	RosterParticipantIDs []uuid.UUID
	ProposedPairings     []Pair
	ByeParticipantID     uuid.UUID
	PreviousMeetings     []Pair
	RepeatedPairings     []Pair
	AlternativeSearch    AlternativeSearch
}

func ConfirmRepeatOverride(
	existing *RepeatOverride,
	command RepeatOverrideCommand,
) (RepeatOverride, bool, error) {
	if !command.Confirmed {
		return RepeatOverride{}, false, ErrRepeatOverrideNotConfirmed
	}
	record, err := newRepeatOverride(command)
	if err != nil {
		return RepeatOverride{}, false, err
	}
	if existing == nil {
		return cloneRepeatOverride(record), true, nil
	}
	if err := existing.Validate(); err != nil {
		return RepeatOverride{}, false, fmt.Errorf("%w: existing record: %w", ErrRepeatOverrideConflict, err)
	}
	if !reflect.DeepEqual(*existing, record) {
		return RepeatOverride{}, false, ErrRepeatOverrideConflict
	}
	return cloneRepeatOverride(*existing), false, nil
}

func newRepeatOverride(command RepeatOverrideCommand) (RepeatOverride, error) {
	if command.ID == uuid.Nil || command.ActorID == uuid.Nil {
		return RepeatOverride{}, fmt.Errorf("%w: missing command or actor identity", ErrInvalidRepeatOverride)
	}
	reason := strings.TrimSpace(command.Reason)
	if reason == "" {
		return RepeatOverride{}, fmt.Errorf("%w: blank reason", ErrInvalidRepeatOverride)
	}
	if command.ConfirmedAt.IsZero() || command.ConfirmedAt.Location() != time.UTC {
		return RepeatOverride{}, fmt.Errorf("%w: confirmation timestamp must be server UTC", ErrInvalidRepeatOverride)
	}
	if err := validateManualRoundShape(command.RosterParticipantIDs, command.Round); err != nil {
		return RepeatOverride{}, fmt.Errorf("%w: %w", ErrInvalidRepeatOverride, err)
	}

	previous, err := canonicalPairs(command.PreviousMeetings)
	if err != nil {
		return RepeatOverride{}, err
	}
	blocked, err := PreviousMeetingSet(previous)
	if err != nil {
		return RepeatOverride{}, fmt.Errorf("%w: %w", ErrInvalidRepeatOverride, err)
	}
	repeated, err := canonicalPairs(repeatedPairings(command.Round.Pairings, blocked))
	if err != nil {
		return RepeatOverride{}, err
	}
	if len(repeated) == 0 {
		return RepeatOverride{}, ErrRepeatOverrideNotRequired
	}

	roster, err := canonicalParticipantIDs(command.RosterParticipantIDs)
	if err != nil {
		return RepeatOverride{}, err
	}
	proposed, err := canonicalPairs(command.Round.Pairings)
	if err != nil {
		return RepeatOverride{}, err
	}
	alternative, err := completeNonRepeatingAlternative(roster, command.Round.ByeParticipantID, previous)
	if err != nil {
		return RepeatOverride{}, err
	}
	record := RepeatOverride{
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
		AlternativeSearch: AlternativeSearch{
			AlgorithmVersion: AlternativeSearchAlgorithmV1,
			Complete:         true,
			Pairings:         alternative,
		},
	}
	if err := record.Validate(); err != nil {
		return RepeatOverride{}, err
	}
	return record, nil
}

func (o RepeatOverride) Validate() error {
	if err := o.validateMetadata(); err != nil {
		return err
	}
	if err := o.validatePairingEvidence(); err != nil {
		return err
	}
	return o.validateAlternativeEvidence()
}

func (o RepeatOverride) validateMetadata() error {
	if o.CommandID == uuid.Nil || o.RoundID == uuid.Nil || o.ActorID == uuid.Nil {
		return fmt.Errorf("%w: missing identity", ErrInvalidRepeatOverride)
	}
	if o.Reason == "" || o.Reason != strings.TrimSpace(o.Reason) {
		return fmt.Errorf("%w: invalid reason", ErrInvalidRepeatOverride)
	}
	if o.ConfirmedAt.IsZero() || o.ConfirmedAt.Location() != time.UTC {
		return fmt.Errorf("%w: confirmation timestamp must be server UTC", ErrInvalidRepeatOverride)
	}
	return nil
}

func (o RepeatOverride) validatePairingEvidence() error {
	roster, err := canonicalParticipantIDs(o.RosterParticipantIDs)
	if err != nil {
		return err
	}
	if !equalUUIDs(roster, o.RosterParticipantIDs) {
		return fmt.Errorf("%w: roster evidence is not canonical", ErrInvalidRepeatOverride)
	}
	proposed, err := canonicalPairs(o.ProposedPairings)
	if err != nil {
		return err
	}
	if !EqualPairs(proposed, o.ProposedPairings) {
		return fmt.Errorf("%w: proposed pairing evidence is not canonical", ErrInvalidRepeatOverride)
	}
	round := ManualRound{ID: o.RoundID, Pairings: proposed, ByeParticipantID: o.ByeParticipantID}
	if err := validateManualRoundShape(roster, round); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidRepeatOverride, err)
	}
	previous, err := canonicalPairs(o.PreviousMeetings)
	if err != nil {
		return err
	}
	if !EqualPairs(previous, o.PreviousMeetings) {
		return fmt.Errorf("%w: previous meeting evidence is not canonical", ErrInvalidRepeatOverride)
	}
	blocked, err := PreviousMeetingSet(previous)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidRepeatOverride, err)
	}
	repeated, err := canonicalPairs(repeatedPairings(proposed, blocked))
	if err != nil {
		return err
	}
	if len(repeated) == 0 || !EqualPairs(repeated, o.RepeatedPairings) {
		return fmt.Errorf("%w: repeated pairing evidence does not match", ErrInvalidRepeatOverride)
	}
	return nil
}

func (o RepeatOverride) validateAlternativeEvidence() error {
	if !o.AlternativeSearch.Complete || o.AlternativeSearch.AlgorithmVersion != AlternativeSearchAlgorithmV1 {
		return fmt.Errorf("%w: incomplete alternative search", ErrInvalidRepeatOverride)
	}
	alternative, err := completeNonRepeatingAlternative(
		o.RosterParticipantIDs,
		o.ByeParticipantID,
		o.PreviousMeetings,
	)
	if err != nil {
		return err
	}
	if !EqualPairs(alternative, o.AlternativeSearch.Pairings) {
		return fmt.Errorf("%w: alternative search evidence does not match", ErrInvalidRepeatOverride)
	}
	return nil
}

func (o RepeatOverride) matches(
	rosterParticipantIDs []uuid.UUID,
	round ManualRound,
	previousMeetings []Pair,
	repeated []Pair,
) bool {
	roster, rosterErr := canonicalParticipantIDs(rosterParticipantIDs)
	proposed, proposedErr := canonicalPairs(round.Pairings)
	previous, previousErr := canonicalPairs(previousMeetings)
	repeatedCanonical, repeatedErr := canonicalPairs(repeated)
	return rosterErr == nil && proposedErr == nil && previousErr == nil && repeatedErr == nil &&
		o.RoundID == round.ID && o.ByeParticipantID == round.ByeParticipantID &&
		equalUUIDs(o.RosterParticipantIDs, roster) &&
		EqualPairs(o.ProposedPairings, proposed) &&
		EqualPairs(o.PreviousMeetings, previous) &&
		EqualPairs(o.RepeatedPairings, repeatedCanonical)
}

func completeNonRepeatingAlternative(
	rosterParticipantIDs []uuid.UUID,
	byeParticipantID uuid.UUID,
	previousMeetings []Pair,
) ([]Pair, error) {
	eligible := make([]uuid.UUID, 0, len(rosterParticipantIDs))
	for _, participantID := range rosterParticipantIDs {
		if participantID != byeParticipantID {
			eligible = append(eligible, participantID)
		}
	}
	pairings, err := FindMatching(eligible, previousMeetings)
	if errors.Is(err, ErrMatchingImpossible) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w: alternative search: %w", ErrInvalidRepeatOverride, err)
	}
	return canonicalPairs(pairings)
}

func canonicalParticipantIDs(participantIDs []uuid.UUID) ([]uuid.UUID, error) {
	if _, err := manualPairingRosterSet(participantIDs); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidRepeatOverride, err)
	}
	result := append([]uuid.UUID(nil), participantIDs...)
	sort.Slice(result, func(i, j int) bool {
		return bytes.Compare(result[i][:], result[j][:]) < 0
	})
	return result, nil
}

func canonicalPairs(pairings []Pair) ([]Pair, error) {
	result := make([]Pair, len(pairings))
	seen := make(map[PairKey]struct{}, len(pairings))
	for i, pairing := range pairings {
		if pairing.FirstParticipantID == uuid.Nil || pairing.SecondParticipantID == uuid.Nil {
			return nil, fmt.Errorf("%w: pairing has missing participant identity", ErrInvalidRepeatOverride)
		}
		if pairing.FirstParticipantID == pairing.SecondParticipantID {
			return nil, fmt.Errorf("%w: pairing is a self-pair", ErrInvalidRepeatOverride)
		}
		key := NewPairKey(pairing.FirstParticipantID, pairing.SecondParticipantID)
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("%w: duplicate pairing evidence", ErrInvalidRepeatOverride)
		}
		seen[key] = struct{}{}
		first, second := key.Participants()
		result[i] = Pair{FirstParticipantID: first, SecondParticipantID: second}
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

func cloneRepeatOverride(source RepeatOverride) RepeatOverride {
	result := source
	result.RosterParticipantIDs = append([]uuid.UUID(nil), source.RosterParticipantIDs...)
	result.ProposedPairings = append([]Pair(nil), source.ProposedPairings...)
	result.PreviousMeetings = append([]Pair(nil), source.PreviousMeetings...)
	result.RepeatedPairings = append([]Pair(nil), source.RepeatedPairings...)
	result.AlternativeSearch.Pairings = append([]Pair(nil), source.AlternativeSearch.Pairings...)
	return result
}
