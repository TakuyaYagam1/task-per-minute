package arena

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var (
	ErrSwissPriorRoundIncomplete = errors.New("prior Swiss round is not officially complete")
	ErrInvalidSwissWave          = errors.New("invalid Swiss Wave membership")
	ErrSwissWaveNotFound         = errors.New("swiss wave not found")
)

type SwissSeriesOfficialCompletion struct {
	SeriesID            uuid.UUID
	FirstParticipantID  uuid.UUID
	SecondParticipantID uuid.UUID
	State               domain.ArenaSeriesState
	ResultRevisionID    domain.ArenaOfficialResultRevisionID
}

type SwissByeOfficialCompletion struct {
	ParticipantID uuid.UUID
	RevisionID    uuid.UUID
}

type SwissRoundOfficialCompletion struct {
	RoundID     uuid.UUID
	RoundNumber int
	RevisionID  uuid.UUID
	Series      []SwissSeriesOfficialCompletion
	Bye         *SwissByeOfficialCompletion
}

type SwissWavePlan struct {
	WaveID                uuid.UUID
	TournamentID          uuid.UUID
	RosterID              uuid.UUID
	RoundID               uuid.UUID
	RoundNumber           int
	RevisionID            domain.ArenaWaveRevisionID
	LockProofHash         string
	SeriesIDs             []uuid.UUID
	ByeParticipantIDs     []uuid.UUID
	ParticipantIDs        []uuid.UUID
	PriorRoundRevisionIDs []uuid.UUID
}

type SwissWaveCreateCommand struct {
	Round       SwissRoundLockRecord
	PriorRounds []SwissRoundOfficialCompletion
}

type SwissWaveCreateInput struct {
	Round       SwissRoundLockRecord
	Plan        SwissWavePlan
	PriorRounds []SwissRoundOfficialCompletion
}

// SwissWaveRepository owns the creation transaction. CreateSwissWave must
// revalidate the exact locked proof and every prior official result or bye
// revision before it inserts the Wave and its complete membership.
type SwissWaveRepository interface {
	CreateSwissWave(ctx context.Context, input SwissWaveCreateInput) (*SwissWavePlan, bool, error)
	GetSwissWave(ctx context.Context, waveID uuid.UUID) (*SwissWavePlan, error)
}

type SwissWaveUseCase struct {
	repository SwissWaveRepository
}

func NewSwissWaveUseCase(repository SwissWaveRepository) *SwissWaveUseCase {
	return &SwissWaveUseCase{repository: repository}
}

func (u *SwissWaveUseCase) Create(
	ctx context.Context,
	command SwissWaveCreateCommand,
) (*SwissWavePlan, bool, error) {
	if u == nil || u.repository == nil {
		return nil, false, domain.ErrValidation
	}
	if err := validateSwissRoundLockForWave(command.Round); err != nil {
		return nil, false, err
	}
	priorRounds := cloneSwissRoundOfficialCompletions(command.PriorRounds)
	canonicalizeSwissRoundOfficialCompletions(priorRounds)
	plan, err := buildSwissWavePlan(command.Round.Proof, priorRounds)
	if err != nil {
		return nil, false, err
	}
	record, changed, err := u.repository.CreateSwissWave(ctx, SwissWaveCreateInput{
		Round: *cloneSwissRoundLockRecord(&command.Round), Plan: plan, PriorRounds: priorRounds,
	})
	if err != nil {
		return nil, false, swissWaveMutationError(err)
	}
	if !changed {
		return u.reconcileCreate(ctx, plan)
	}
	if record == nil || record.Validate() != nil || !swissWavePlansEqual(*record, plan) {
		return nil, false, domain.ErrInternal
	}
	return cloneSwissWavePlan(record), true, nil
}

func (p SwissWavePlan) Validate() error {
	if err := validateSwissWavePlanIdentity(p); err != nil {
		return err
	}
	if err := validateSwissWavePlanMembership(p); err != nil {
		return err
	}
	return validateSwissWavePlanEvidence(p)
}

func validateSwissWavePlanIdentity(p SwissWavePlan) error {
	if p.WaveID == uuid.Nil || p.TournamentID == uuid.Nil || p.RosterID == uuid.Nil ||
		p.RoundID == uuid.Nil || p.RoundNumber < 1 || p.RevisionID.IsZero() ||
		!validCapacityDigest(p.LockProofHash) {
		return swissWaveError("missing Wave or lock identity")
	}
	return nil
}

func validateSwissWavePlanMembership(p SwissWavePlan) error {
	if len(p.SeriesIDs) < 2 || len(p.SeriesIDs)*2+len(p.ByeParticipantIDs) != len(p.ParticipantIDs) ||
		len(p.ByeParticipantIDs) > 1 || len(p.PriorRoundRevisionIDs) != p.RoundNumber-1 {
		return swissWaveError("membership cardinality does not match the locked round")
	}
	return nil
}

func validateSwissWavePlanEvidence(p SwissWavePlan) error {
	if !sortedUniqueUUIDs(p.SeriesIDs) || !sortedUniqueUUIDs(p.ParticipantIDs) ||
		!uniqueNonZeroUUIDs(p.PriorRoundRevisionIDs) {
		return swissWaveError("Wave evidence is missing, duplicated, or not canonical")
	}
	if len(p.ByeParticipantIDs) == 1 {
		byeID := p.ByeParticipantIDs[0]
		if byeID == uuid.Nil || !slices.Contains(p.ParticipantIDs, byeID) {
			return swissWaveError("bye membership is invalid")
		}
	}
	return nil
}

func (u *SwissWaveUseCase) reconcileCreate(
	ctx context.Context,
	plan SwissWavePlan,
) (*SwissWavePlan, bool, error) {
	current, err := u.repository.GetSwissWave(ctx, plan.WaveID)
	if errors.Is(err, ErrSwissWaveNotFound) {
		return nil, false, domain.ErrConflict
	}
	if err != nil {
		return nil, false, swissWaveLookupError(err)
	}
	if current == nil || current.Validate() != nil {
		return nil, false, domain.ErrInternal
	}
	if !swissWavePlansEqual(*current, plan) {
		return nil, false, domain.ErrConflict
	}
	return cloneSwissWavePlan(current), false, nil
}

func buildSwissWavePlan(
	proof SwissRoundLockProof,
	priorRounds []SwissRoundOfficialCompletion,
) (SwissWavePlan, error) {
	if err := validatePriorSwissRounds(proof, priorRounds); err != nil {
		return SwissWavePlan{}, err
	}
	seriesIDs := make([]uuid.UUID, 0, len(proof.Series))
	for _, series := range proof.Series {
		seriesIDs = append(seriesIDs, series.SeriesID)
	}
	byeParticipantIDs := make([]uuid.UUID, 0, 1)
	if proof.ByeParticipantID != uuid.Nil {
		byeParticipantIDs = append(byeParticipantIDs, proof.ByeParticipantID)
	}
	priorRevisionIDs := make([]uuid.UUID, 0, len(priorRounds))
	for _, round := range priorRounds {
		priorRevisionIDs = append(priorRevisionIDs, round.RevisionID)
	}
	plan := SwissWavePlan{
		WaveID: proof.WaveID, TournamentID: proof.TournamentID, RosterID: proof.RosterID,
		RoundID: proof.RoundID, RoundNumber: proof.RoundNumber, RevisionID: proof.WaveRevisionID,
		LockProofHash: proof.ProofHash, SeriesIDs: seriesIDs, ByeParticipantIDs: byeParticipantIDs,
		ParticipantIDs:        append([]uuid.UUID(nil), proof.RosterParticipantIDs...),
		PriorRoundRevisionIDs: priorRevisionIDs,
	}
	if err := plan.Validate(); err != nil {
		return SwissWavePlan{}, err
	}
	return plan, nil
}

func validateSwissRoundLockForWave(record SwissRoundLockRecord) error {
	if err := record.Proof.Validate(); err != nil {
		return err
	}
	if record.LockedAt == nil || !validArenaServerTime(*record.LockedAt) {
		return swissWaveError("round is not locked")
	}
	if record.StartedAt != nil {
		return ErrSwissRoundAlreadyStarted
	}
	return nil
}

func validatePriorSwissRounds(
	proof SwissRoundLockProof,
	rounds []SwissRoundOfficialCompletion,
) error {
	if len(rounds) != proof.RoundNumber-1 {
		return ErrSwissPriorRoundIncomplete
	}
	seenRoundIDs := make(map[uuid.UUID]struct{}, len(rounds))
	seenRevisionIDs := make(map[uuid.UUID]struct{}, len(rounds))
	for index, round := range rounds {
		if round.RoundNumber != index+1 || round.RoundID == uuid.Nil || round.RevisionID == uuid.Nil {
			return ErrSwissPriorRoundIncomplete
		}
		if _, duplicate := seenRoundIDs[round.RoundID]; duplicate {
			return ErrSwissPriorRoundIncomplete
		}
		if _, duplicate := seenRevisionIDs[round.RevisionID]; duplicate {
			return ErrSwissPriorRoundIncomplete
		}
		seenRoundIDs[round.RoundID] = struct{}{}
		seenRevisionIDs[round.RevisionID] = struct{}{}
		if err := validateOfficialSwissRoundMembership(proof, round); err != nil {
			return err
		}
	}
	return nil
}

func validateOfficialSwissRoundMembership(
	proof SwissRoundLockProof,
	round SwissRoundOfficialCompletion,
) error {
	if len(round.Series) != len(proof.Series) {
		return ErrSwissPriorRoundIncomplete
	}
	roster := make(map[uuid.UUID]struct{}, len(proof.RosterParticipantIDs))
	for _, participantID := range proof.RosterParticipantIDs {
		roster[participantID] = struct{}{}
	}
	seenSeries := make(map[uuid.UUID]struct{}, len(round.Series))
	usedParticipants := make(map[uuid.UUID]struct{}, len(roster))
	for _, series := range round.Series {
		if err := validateOfficialSwissSeries(series, roster, seenSeries, usedParticipants); err != nil {
			return err
		}
	}
	if err := validateOfficialSwissBye(proof, round.Bye, roster, usedParticipants); err != nil {
		return err
	}
	if len(usedParticipants) != len(roster) {
		return ErrSwissPriorRoundIncomplete
	}
	return nil
}

func validateOfficialSwissSeries(
	series SwissSeriesOfficialCompletion,
	roster map[uuid.UUID]struct{},
	seenSeries map[uuid.UUID]struct{},
	usedParticipants map[uuid.UUID]struct{},
) error {
	if series.SeriesID == uuid.Nil || !series.State.IsTerminal() || series.ResultRevisionID.IsZero() ||
		series.FirstParticipantID == uuid.Nil || series.SecondParticipantID == uuid.Nil ||
		series.FirstParticipantID == series.SecondParticipantID {
		return ErrSwissPriorRoundIncomplete
	}
	if _, duplicate := seenSeries[series.SeriesID]; duplicate {
		return ErrSwissPriorRoundIncomplete
	}
	seenSeries[series.SeriesID] = struct{}{}
	for _, participantID := range []uuid.UUID{series.FirstParticipantID, series.SecondParticipantID} {
		if _, exists := roster[participantID]; !exists {
			return ErrSwissPriorRoundIncomplete
		}
		if _, duplicate := usedParticipants[participantID]; duplicate {
			return ErrSwissPriorRoundIncomplete
		}
		usedParticipants[participantID] = struct{}{}
	}
	return nil
}

func validateOfficialSwissBye(
	proof SwissRoundLockProof,
	bye *SwissByeOfficialCompletion,
	roster map[uuid.UUID]struct{},
	usedParticipants map[uuid.UUID]struct{},
) error {
	if proof.ByeParticipantID == uuid.Nil {
		if bye != nil {
			return ErrSwissPriorRoundIncomplete
		}
		return nil
	}
	if bye == nil || bye.ParticipantID == uuid.Nil || bye.RevisionID == uuid.Nil {
		return ErrSwissPriorRoundIncomplete
	}
	if _, exists := roster[bye.ParticipantID]; !exists {
		return ErrSwissPriorRoundIncomplete
	}
	if _, duplicate := usedParticipants[bye.ParticipantID]; duplicate {
		return ErrSwissPriorRoundIncomplete
	}
	usedParticipants[bye.ParticipantID] = struct{}{}
	return nil
}

func canonicalizeSwissRoundOfficialCompletions(rounds []SwissRoundOfficialCompletion) {
	sort.Slice(rounds, func(i, j int) bool { return rounds[i].RoundNumber < rounds[j].RoundNumber })
	for index := range rounds {
		sort.Slice(rounds[index].Series, func(i, j int) bool {
			return bytes.Compare(rounds[index].Series[i].SeriesID[:], rounds[index].Series[j].SeriesID[:]) < 0
		})
	}
}

func cloneSwissRoundOfficialCompletions(
	rounds []SwissRoundOfficialCompletion,
) []SwissRoundOfficialCompletion {
	cloned := make([]SwissRoundOfficialCompletion, len(rounds))
	for index, round := range rounds {
		cloned[index] = round
		cloned[index].Series = append([]SwissSeriesOfficialCompletion(nil), round.Series...)
		if round.Bye != nil {
			bye := *round.Bye
			cloned[index].Bye = &bye
		}
	}
	return cloned
}

func cloneSwissWavePlan(plan *SwissWavePlan) *SwissWavePlan {
	if plan == nil {
		return nil
	}
	cloned := *plan
	cloned.SeriesIDs = append([]uuid.UUID(nil), plan.SeriesIDs...)
	cloned.ByeParticipantIDs = append([]uuid.UUID(nil), plan.ByeParticipantIDs...)
	cloned.ParticipantIDs = append([]uuid.UUID(nil), plan.ParticipantIDs...)
	cloned.PriorRoundRevisionIDs = append([]uuid.UUID(nil), plan.PriorRoundRevisionIDs...)
	return &cloned
}

func swissWavePlansEqual(first, second SwissWavePlan) bool {
	return first.WaveID == second.WaveID && first.TournamentID == second.TournamentID &&
		first.RosterID == second.RosterID && first.RoundID == second.RoundID &&
		first.RoundNumber == second.RoundNumber && first.RevisionID == second.RevisionID &&
		first.LockProofHash == second.LockProofHash && slices.Equal(first.SeriesIDs, second.SeriesIDs) &&
		slices.Equal(first.ByeParticipantIDs, second.ByeParticipantIDs) &&
		slices.Equal(first.ParticipantIDs, second.ParticipantIDs) &&
		slices.Equal(first.PriorRoundRevisionIDs, second.PriorRoundRevisionIDs)
}

func sortedUniqueUUIDs(values []uuid.UUID) bool {
	if len(values) == 0 {
		return true
	}
	for index, value := range values {
		if value == uuid.Nil || (index > 0 && bytes.Compare(values[index-1][:], value[:]) >= 0) {
			return false
		}
	}
	return true
}

func uniqueNonZeroUUIDs(values []uuid.UUID) bool {
	seen := make(map[uuid.UUID]struct{}, len(values))
	for _, value := range values {
		if value == uuid.Nil {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func swissWaveError(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidSwissWave, message)
}

func swissWaveMutationError(err error) error {
	if errors.Is(err, domain.ErrConflict) || errors.Is(err, ErrSwissWaveNotFound) {
		return err
	}
	return fmt.Errorf("SwissWaveUseCase - Create - SwissWaveRepository.CreateSwissWave: %w", err)
}

func swissWaveLookupError(err error) error {
	if errors.Is(err, ErrSwissWaveNotFound) {
		return err
	}
	return fmt.Errorf("SwissWaveUseCase - Create - SwissWaveRepository.GetSwissWave: %w", err)
}
