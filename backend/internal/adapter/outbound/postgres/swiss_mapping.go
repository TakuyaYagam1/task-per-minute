package postgres

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

func (r *SwissPostgres) mapSwissWriteError(operation string, err error) error {
	switch {
	case errors.Is(err, errSwissRoundCAS):
		return domain.ErrConflict
	case errors.Is(err, pgx.ErrNoRows):
		return ErrSwissRoundNotFound
	case isSwissConflict(err):
		return domain.WrapError(err, domain.ErrConflict)
	default:
		return fmt.Errorf("SwissPostgres - %s: %w", operation, err)
	}
}

var errSwissRoundCAS = errors.New("swiss round compare-and-set failed")

func isSwissConflict(err error) bool {
	constraints := []string{
		"swiss_rounds_pkey",
		"swiss_rounds_roster_number_key",
		"swiss_rounds_decision_evidence_id_key",
		"swiss_repeat_overrides_round_key",
		"swiss_pairings_round_slot_key",
		"swiss_pairing_members_round_participant_key",
		"swiss_byes_roster_participant_key",
	}
	for _, constraint := range constraints {
		if isUniqueViolation(err, constraint) {
			return true
		}
	}
	return false
}

func automaticRoundCreateParams(in AutomaticSwissRoundInput) (sqlc.CreateAutomaticSwissRoundParams, error) {
	algorithm := in.Pairing.Evidence.AlgorithmVersion
	inputs, err := marshalJSON("SwissPostgres - automatic round - decision inputs", in.Pairing.Evidence.NormalizedInputs)
	if err != nil {
		return sqlc.CreateAutomaticSwissRoundParams{}, err
	}
	result, err := marshalJSON("SwissPostgres - automatic round - decision result", in.Pairing.Evidence.Result)
	if err != nil {
		return sqlc.CreateAutomaticSwissRoundParams{}, err
	}
	return sqlc.CreateAutomaticSwissRoundParams{
		ID:                       in.Meta.ID,
		TournamentID:             in.Meta.TournamentID,
		RosterID:                 in.Meta.RosterID,
		RoundNumber:              in.Meta.RoundNumber,
		SourceRosterRevision:     in.Meta.SourceRosterRevision,
		SourceHistoryRevision:    in.Meta.SourceHistoryRevision,
		PairingInputs:            inputs,
		DecisionEvidenceID:       uuid.NullUUID{UUID: in.Pairing.Evidence.ID, Valid: true},
		DecisionAlgorithmVersion: &algorithm,
		DecisionSeed:             append([]byte(nil), in.Pairing.Evidence.Seed[:]...),
		DecisionResult:           result,
		DecisionReplayDigest:     append([]byte(nil), in.Pairing.Evidence.ReplayDigest[:]...),
		DecisionOwnerID:          uuid.NullUUID{UUID: in.Pairing.Evidence.OwnerID, Valid: true},
		GeneratedAt:              tstz(in.Meta.GeneratedAt),
		CreatedAt:                tstz(in.Meta.RecordedAt),
	}, nil
}

func automaticRoundUpdateParams(
	in AutomaticSwissRoundInput,
	expectedRevision int64,
) (sqlc.UpdateAutomaticSwissRoundCASParams, error) {
	created, err := automaticRoundCreateParams(in)
	if err != nil {
		return sqlc.UpdateAutomaticSwissRoundCASParams{}, err
	}
	return sqlc.UpdateAutomaticSwissRoundCASParams{
		SourceRosterRevision:     created.SourceRosterRevision,
		SourceHistoryRevision:    created.SourceHistoryRevision,
		PairingInputs:            created.PairingInputs,
		DecisionEvidenceID:       created.DecisionEvidenceID,
		DecisionAlgorithmVersion: created.DecisionAlgorithmVersion,
		DecisionSeed:             created.DecisionSeed,
		DecisionResult:           created.DecisionResult,
		DecisionReplayDigest:     created.DecisionReplayDigest,
		DecisionOwnerID:          created.DecisionOwnerID,
		GeneratedAt:              created.GeneratedAt,
		UpdatedAt:                tstz(in.Meta.RecordedAt),
		ID:                       in.Meta.ID,
		ExpectedRevision:         expectedRevision,
	}, nil
}

func manualRoundCreateParams(in ManualSwissRoundInput) (sqlc.CreateManualSwissRoundParams, error) {
	inputs, err := marshalJSON("SwissPostgres - manual round - pairing inputs", in.PairingInputs)
	if err != nil {
		return sqlc.CreateManualSwissRoundParams{}, err
	}
	return sqlc.CreateManualSwissRoundParams{
		ID:                    in.Meta.ID,
		TournamentID:          in.Meta.TournamentID,
		RosterID:              in.Meta.RosterID,
		RoundNumber:           in.Meta.RoundNumber,
		SourceRosterRevision:  in.Meta.SourceRosterRevision,
		SourceHistoryRevision: in.Meta.SourceHistoryRevision,
		PairingInputs:         inputs,
		GeneratedAt:           tstz(in.Meta.GeneratedAt),
		CreatedAt:             tstz(in.Meta.RecordedAt),
	}, nil
}

func manualRoundUpdateParams(
	in ManualSwissRoundInput,
	expectedRevision int64,
) (sqlc.UpdateManualSwissRoundCASParams, error) {
	created, err := manualRoundCreateParams(in)
	if err != nil {
		return sqlc.UpdateManualSwissRoundCASParams{}, err
	}
	return sqlc.UpdateManualSwissRoundCASParams{
		SourceRosterRevision:  created.SourceRosterRevision,
		SourceHistoryRevision: created.SourceHistoryRevision,
		PairingInputs:         created.PairingInputs,
		GeneratedAt:           created.GeneratedAt,
		UpdatedAt:             tstz(in.Meta.RecordedAt),
		ID:                    in.Meta.ID,
		ExpectedRevision:      expectedRevision,
	}, nil
}

func repeatOverrideParams(
	meta SwissRoundMeta,
	override swissusecase.RepeatOverride,
) (sqlc.CreateSwissRepeatOverrideParams, error) {
	if err := override.Validate(); err != nil {
		return sqlc.CreateSwissRepeatOverrideParams{}, err
	}
	rosterIDs, err := marshalJSON("SwissPostgres - repeat override - roster participants", override.RosterParticipantIDs)
	if err != nil {
		return sqlc.CreateSwissRepeatOverrideParams{}, err
	}
	proposed, err := marshalJSON("SwissPostgres - repeat override - proposed pairings", override.ProposedPairings)
	if err != nil {
		return sqlc.CreateSwissRepeatOverrideParams{}, err
	}
	previous, err := marshalJSON("SwissPostgres - repeat override - previous meetings", override.PreviousMeetings)
	if err != nil {
		return sqlc.CreateSwissRepeatOverrideParams{}, err
	}
	repeated, err := marshalJSON("SwissPostgres - repeat override - repeated pairings", override.RepeatedPairings)
	if err != nil {
		return sqlc.CreateSwissRepeatOverrideParams{}, err
	}
	alternative, err := marshalJSON("SwissPostgres - repeat override - alternative pairings", override.AlternativeSearch.Pairings)
	if err != nil {
		return sqlc.CreateSwissRepeatOverrideParams{}, err
	}
	return sqlc.CreateSwissRepeatOverrideParams{
		ID:                          override.CommandID,
		RoundID:                     meta.ID,
		RosterID:                    meta.RosterID,
		ActorID:                     override.ActorID,
		Reason:                      override.Reason,
		ConfirmedAt:                 tstz(override.ConfirmedAt),
		RosterParticipantIds:        rosterIDs,
		ProposedPairings:            proposed,
		ByeParticipantID:            nullableUUIDValue(override.ByeParticipantID),
		PreviousMeetings:            previous,
		RepeatedPairings:            repeated,
		AlternativeAlgorithmVersion: override.AlternativeSearch.AlgorithmVersion,
		AlternativeSearchComplete:   override.AlternativeSearch.Complete,
		AlternativePairings:         alternative,
		CreatedAt:                   tstz(meta.RecordedAt),
	}, nil
}

func byeParams(meta SwissRoundMeta, bye swissusecase.ByeSelection) (sqlc.CreateSwissByeParams, error) {
	if _, err := swissusecase.ReplayBye(bye); err != nil {
		return sqlc.CreateSwissByeParams{}, err
	}
	inputs, err := marshalJSON("SwissPostgres - bye - decision inputs", bye.Evidence.NormalizedInputs)
	if err != nil {
		return sqlc.CreateSwissByeParams{}, err
	}
	result, err := marshalJSON("SwissPostgres - bye - decision result", bye.Evidence.Result)
	if err != nil {
		return sqlc.CreateSwissByeParams{}, err
	}
	return sqlc.CreateSwissByeParams{
		RoundID:                  meta.ID,
		RosterID:                 meta.RosterID,
		ParticipantID:            bye.ParticipantID,
		PointsAwarded:            int16(swissusecase.PairingByePoints),
		DecisionEvidenceID:       bye.Evidence.ID,
		DecisionAlgorithmVersion: bye.Evidence.AlgorithmVersion,
		DecisionInputs:           inputs,
		DecisionSeed:             append([]byte(nil), bye.Evidence.Seed[:]...),
		DecisionResult:           result,
		DecisionReplayDigest:     append([]byte(nil), bye.Evidence.ReplayDigest[:]...),
		DecisionOwnerID:          bye.Evidence.OwnerID,
		DecidedAt:                tstz(bye.Evidence.DecidedAt),
		CreatedAt:                tstz(meta.RecordedAt),
	}, nil
}

func swissRoundRecord(row sqlc.SwissRound) (*SwissRoundRecord, error) {
	var inputs []string
	if err := json.Unmarshal(row.PairingInputs, &inputs); err != nil || len(inputs) == 0 {
		return nil, domain.ErrValidation
	}
	out := &SwissRoundRecord{
		ID:                    row.ID,
		RosterID:              row.RosterID,
		RoundNumber:           int(row.RoundNumber),
		Revision:              row.Revision,
		SourceRosterRevision:  row.SourceRosterRevision,
		SourceHistoryRevision: row.SourceHistoryRevision,
		GenerationKind:        row.GenerationKind,
		PairingInputs:         inputs,
		GeneratedAt:           row.GeneratedAt.Time,
		LockRevision:          row.LockRevision,
		LockedAt:              nullableTime(row.LockedAt),
		CreatedAt:             row.CreatedAt.Time,
		UpdatedAt:             row.UpdatedAt.Time,
	}
	if row.GenerationKind == "automatic" {
		evidence, err := roundEvidenceFromRow(row)
		if err != nil {
			return nil, err
		}
		out.AutomaticEvidence = evidence
	}
	return out, nil
}

func roundEvidenceFromRow(row sqlc.SwissRound) (*domain.DecisionEvidence, error) {
	if !row.DecisionEvidenceID.Valid || row.DecisionAlgorithmVersion == nil || !row.DecisionOwnerID.Valid {
		return nil, domain.ErrValidation
	}
	evidence, err := decisionEvidence(
		row.DecisionEvidenceID.UUID,
		*row.DecisionAlgorithmVersion,
		row.PairingInputs,
		row.DecisionSeed,
		row.DecisionResult,
		row.DecisionReplayDigest,
		row.DecisionOwnerID.UUID,
		row.GeneratedAt.Time,
	)
	if err != nil {
		return nil, err
	}
	return &evidence, nil
}

func repeatOverrideFromRow(row sqlc.SwissRepeatOverride) (*swissusecase.RepeatOverride, error) {
	override := &swissusecase.RepeatOverride{
		CommandID:        row.ID,
		RoundID:          row.RoundID,
		ActorID:          row.ActorID,
		Reason:           row.Reason,
		ConfirmedAt:      row.ConfirmedAt.Time.UTC(),
		ByeParticipantID: row.ByeParticipantID.UUID,
		AlternativeSearch: swissusecase.AlternativeSearch{
			AlgorithmVersion: row.AlternativeAlgorithmVersion,
			Complete:         row.AlternativeSearchComplete,
		},
	}
	decodes := []struct {
		data []byte
		out  any
	}{
		{row.RosterParticipantIds, &override.RosterParticipantIDs},
		{row.ProposedPairings, &override.ProposedPairings},
		{row.PreviousMeetings, &override.PreviousMeetings},
		{row.RepeatedPairings, &override.RepeatedPairings},
		{row.AlternativePairings, &override.AlternativeSearch.Pairings},
	}
	for _, item := range decodes {
		if err := json.Unmarshal(item.data, item.out); err != nil {
			return nil, err
		}
	}
	if err := override.Validate(); err != nil {
		return nil, err
	}
	return override, nil
}

func byeFromRow(row sqlc.SwissBye) (*swissusecase.ByeSelection, error) {
	evidence, err := decisionEvidence(
		row.DecisionEvidenceID,
		row.DecisionAlgorithmVersion,
		row.DecisionInputs,
		row.DecisionSeed,
		row.DecisionResult,
		row.DecisionReplayDigest,
		row.DecisionOwnerID,
		row.DecidedAt.Time,
	)
	if err != nil {
		return nil, err
	}
	selection := &swissusecase.ByeSelection{
		ParticipantID: row.ParticipantID,
		PointsAwarded: int(row.PointsAwarded),
		Evidence:      evidence,
	}
	if _, err := swissusecase.ReplayBye(*selection); err != nil {
		return nil, err
	}
	return selection, nil
}

func decisionEvidence(
	id uuid.UUID,
	algorithm string,
	inputsJSON []byte,
	seed []byte,
	resultJSON []byte,
	digest []byte,
	ownerID uuid.UUID,
	decidedAt time.Time,
) (domain.DecisionEvidence, error) {
	if len(seed) != domain.DecisionSeedSize || len(digest) != domain.DecisionSeedSize {
		return domain.DecisionEvidence{}, domain.ErrValidation
	}
	var inputs, result []string
	if err := json.Unmarshal(inputsJSON, &inputs); err != nil {
		return domain.DecisionEvidence{}, err
	}
	if err := json.Unmarshal(resultJSON, &result); err != nil {
		return domain.DecisionEvidence{}, err
	}
	evidence := domain.DecisionEvidence{
		ID:               id,
		Purpose:          domain.DecisionPurposePairing,
		AlgorithmVersion: algorithm,
		NormalizedInputs: inputs,
		Result:           result,
		OwnerID:          ownerID,
		DecidedAt:        decidedAt.UTC(),
	}
	copy(evidence.Seed[:], seed)
	copy(evidence.ReplayDigest[:], digest)
	if err := evidence.Validate(); err != nil {
		return domain.DecisionEvidence{}, err
	}
	return evidence, nil
}

func overridePreviousMeetings(override *swissusecase.RepeatOverride) []swissusecase.Pair {
	if override == nil {
		return nil
	}
	return override.PreviousMeetings
}

func containsSwissPair(pairs []swissusecase.Pair, target swissusecase.Pair) bool {
	for _, pair := range pairs {
		if (pair.FirstParticipantID == target.FirstParticipantID &&
			pair.SecondParticipantID == target.SecondParticipantID) ||
			(pair.FirstParticipantID == target.SecondParticipantID &&
				pair.SecondParticipantID == target.FirstParticipantID) {
			return true
		}
	}
	return false
}

func nullableUUIDValue(value uuid.UUID) uuid.NullUUID {
	return uuid.NullUUID{UUID: value, Valid: value != uuid.Nil}
}
