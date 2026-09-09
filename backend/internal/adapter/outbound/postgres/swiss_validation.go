package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

func (r *SwissPostgres) validateLoadedAggregate(
	ctx context.Context,
	aggregate *SwissRoundAggregate,
) error {
	participants, err := r.checkedInParticipantIDs(ctx, aggregate.Round.RosterID)
	if err != nil {
		return err
	}
	pairs := make([]swissusecase.Pair, len(aggregate.Pairings))
	for index, pairing := range aggregate.Pairings {
		pairs[index] = pairing.Pair
	}
	switch aggregate.Round.GenerationKind {
	case "automatic":
		return validateLoadedAutomaticRound(aggregate, participants, pairs)
	case "manual":
		return validateLoadedManualRound(aggregate, participants, pairs)
	default:
		return domain.ErrValidation
	}
}

func validateLoadedAutomaticRound(
	aggregate *SwissRoundAggregate,
	participants []uuid.UUID,
	pairs []swissusecase.Pair,
) error {
	if aggregate.Round.AutomaticEvidence == nil {
		return domain.ErrValidation
	}
	if _, err := swissusecase.ReplayAutomaticPairing(swissusecase.AutomaticPairing{
		Pairings: pairs,
		Evidence: *aggregate.Round.AutomaticEvidence,
	}); err != nil {
		return err
	}
	for _, pairing := range aggregate.Pairings {
		if pairing.PriorMeetingCount != 0 || pairing.RepeatOverrideID != nil {
			return domain.ErrValidation
		}
	}
	return validateSwissCoverage(participants, pairs, aggregate.Bye)
}

func validateLoadedManualRound(
	aggregate *SwissRoundAggregate,
	participants []uuid.UUID,
	pairs []swissusecase.Pair,
) error {
	for _, pairing := range aggregate.Pairings {
		isRepeated := aggregate.Override != nil &&
			containsSwissPair(aggregate.Override.RepeatedPairings, pairing.Pair)
		if (pairing.PriorMeetingCount > 0) != isRepeated ||
			(isRepeated && pairing.RepeatOverrideID == nil) ||
			(!isRepeated && pairing.RepeatOverrideID != nil) {
			return domain.ErrValidation
		}
	}
	byeID := uuid.Nil
	if aggregate.Bye != nil {
		byeID = aggregate.Bye.ParticipantID
	}
	return swissusecase.ValidateManualPairing(
		participants,
		swissusecase.ManualRound{ID: aggregate.Round.ID, Pairings: pairs, ByeParticipantID: byeID},
		overridePreviousMeetings(aggregate.Override),
		aggregate.Override,
	)
}

func validateAutomaticSwissRoundInput(
	in AutomaticSwissRoundInput,
	participants []uuid.UUID,
) error {
	if err := validateSwissRoundMeta(in.Meta, in.Pairing.Pairings); err != nil {
		return err
	}
	if _, err := swissusecase.ReplayAutomaticPairing(in.Pairing); err != nil {
		return fmt.Errorf("%w: automatic pairing evidence: %w", domain.ErrValidation, err)
	}
	if in.Pairing.Evidence.OwnerID != in.Meta.ID || !in.Pairing.Evidence.DecidedAt.Equal(in.Meta.GeneratedAt) {
		return domain.ErrValidation
	}
	for _, count := range in.Meta.PriorMeetingCounts {
		if count != 0 {
			return domain.ErrValidation
		}
	}
	return validateSwissCoverage(participants, in.Pairing.Pairings, in.Meta.Bye)
}

func validateManualSwissRoundInput(in ManualSwissRoundInput, participants []uuid.UUID) error {
	if err := validateSwissRoundMeta(in.Meta, in.Round.Pairings); err != nil {
		return err
	}
	if in.Round.ID != in.Meta.ID || len(in.PairingInputs) == 0 {
		return domain.ErrValidation
	}
	if err := swissusecase.ValidateManualPairing(
		participants,
		in.Round,
		overridePreviousMeetings(in.Override),
		in.Override,
	); err != nil {
		return fmt.Errorf("%w: manual pairing: %w", domain.ErrValidation, err)
	}
	for index, count := range in.Meta.PriorMeetingCounts {
		isRepeated := in.Override != nil && containsSwissPair(in.Override.RepeatedPairings, in.Round.Pairings[index])
		if (count > 0) != isRepeated {
			return domain.ErrValidation
		}
	}
	if in.Override != nil {
		if err := in.Override.Validate(); err != nil {
			return fmt.Errorf("%w: repeat override: %w", domain.ErrValidation, err)
		}
		if in.Override.RoundID != in.Meta.ID {
			return domain.ErrValidation
		}
	}
	return validateSwissCoverage(participants, in.Round.Pairings, in.Meta.Bye)
}

func validateSwissRoundMeta(meta SwissRoundMeta, pairs []swissusecase.Pair) error {
	if err := validateSwissRoundIdentity(meta, pairs); err != nil {
		return err
	}
	if err := validateSwissRoundTimes(meta); err != nil {
		return err
	}
	if err := validateSwissPairingMetadata(meta); err != nil {
		return err
	}
	return validateSwissByeMetadata(meta)
}

func validateSwissRoundIdentity(meta SwissRoundMeta, pairs []swissusecase.Pair) error {
	if meta.ID == uuid.Nil || meta.RosterID == uuid.Nil || meta.RoundNumber < 1 || meta.RoundNumber > 4 ||
		meta.SourceRosterRevision < 1 || meta.SourceHistoryRevision < 0 ||
		len(meta.PairingIDs) != len(pairs) || len(meta.PriorMeetingCounts) != len(pairs) {
		return domain.ErrValidation
	}
	return nil
}

func validateSwissRoundTimes(meta SwissRoundMeta) error {
	if !validServerTime(meta.GeneratedAt) || !validServerTime(meta.RecordedAt) ||
		meta.GeneratedAt.After(meta.RecordedAt) {
		return domain.ErrValidation
	}
	if meta.LockedAt != nil && (!validServerTime(*meta.LockedAt) || meta.LockedAt.Before(meta.GeneratedAt)) {
		return domain.ErrValidation
	}
	return nil
}

func validateSwissPairingMetadata(meta SwissRoundMeta) error {
	seen := make(map[uuid.UUID]struct{}, len(meta.PairingIDs))
	for index, pairingID := range meta.PairingIDs {
		if pairingID == uuid.Nil || meta.PriorMeetingCounts[index] < 0 {
			return domain.ErrValidation
		}
		if _, duplicate := seen[pairingID]; duplicate {
			return domain.ErrValidation
		}
		seen[pairingID] = struct{}{}
	}
	return nil
}

func validateSwissByeMetadata(meta SwissRoundMeta) error {
	if meta.Bye != nil {
		if _, err := swissusecase.ReplayBye(*meta.Bye); err != nil || meta.Bye.Evidence.OwnerID != meta.ID ||
			meta.Bye.Evidence.DecidedAt.After(meta.RecordedAt) {
			return domain.ErrValidation
		}
	}
	return nil
}

func validateSwissCoverage(
	participants []uuid.UUID,
	pairs []swissusecase.Pair,
	bye *swissusecase.ByeSelection,
) error {
	expected := make(map[uuid.UUID]struct{}, len(participants))
	for _, participantID := range participants {
		if participantID == uuid.Nil {
			return domain.ErrValidation
		}
		expected[participantID] = struct{}{}
	}
	used := make(map[uuid.UUID]struct{}, len(participants))
	for _, pair := range pairs {
		if pair.FirstParticipantID == uuid.Nil || pair.SecondParticipantID == uuid.Nil ||
			pair.FirstParticipantID == pair.SecondParticipantID {
			return domain.ErrValidation
		}
		for _, participantID := range []uuid.UUID{pair.FirstParticipantID, pair.SecondParticipantID} {
			if _, ok := expected[participantID]; !ok {
				return domain.ErrValidation
			}
			if _, duplicate := used[participantID]; duplicate {
				return domain.ErrValidation
			}
			used[participantID] = struct{}{}
		}
	}
	if bye != nil {
		if _, ok := expected[bye.ParticipantID]; !ok {
			return domain.ErrValidation
		}
		if _, duplicate := used[bye.ParticipantID]; duplicate {
			return domain.ErrValidation
		}
		used[bye.ParticipantID] = struct{}{}
	}
	if len(used) != len(expected) || (len(expected)%2 == 1) != (bye != nil) {
		return domain.ErrValidation
	}
	return nil
}
