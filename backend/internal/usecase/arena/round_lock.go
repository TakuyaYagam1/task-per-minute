package arena

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var (
	ErrInvalidSwissRoundLockProof = errors.New("invalid Swiss round lock proof")
	ErrSwissRoundAlreadyStarted   = errors.New("swiss round already started")
	ErrSwissRoundNotFound         = errors.New("swiss round not found")
)

type SwissRoundPlanRevisions struct {
	Round       int64 `json:"round"`
	Source      int64 `json:"source"`
	Category    int64 `json:"category"`
	Pool        int64 `json:"pool"`
	History     int64 `json:"history"`
	Reservation int64 `json:"reservation"`
	Membership  int64 `json:"membership"`
}

type SwissLockedSeries struct {
	SeriesID            uuid.UUID `json:"series_id"`
	PairingID           uuid.UUID `json:"pairing_id"`
	FirstParticipantID  uuid.UUID `json:"first_participant_id"`
	SecondParticipantID uuid.UUID `json:"second_participant_id"`
}

type SwissRoundLockProofInput struct {
	TournamentID         uuid.UUID
	RosterID             uuid.UUID
	RoundID              uuid.UUID
	Preset               domain.ArenaPreset
	RoundNumber          int
	SourceRevisionID     uuid.UUID
	CategoryRevisionID   uuid.UUID
	PoolRevisionID       uuid.UUID
	PlanRevisionID       uuid.UUID
	PreflightRevisionID  uuid.UUID
	WaveID               uuid.UUID
	WaveRevisionID       domain.ArenaWaveRevisionID
	Revisions            SwissRoundPlanRevisions
	RosterParticipantIDs []uuid.UUID
	Series               []SwissLockedSeries
	ByeParticipantID     uuid.UUID
}

type SwissRoundLockProof struct {
	TournamentID         uuid.UUID                  `json:"tournament_id"`
	RosterID             uuid.UUID                  `json:"roster_id"`
	RoundID              uuid.UUID                  `json:"round_id"`
	Preset               domain.ArenaPreset         `json:"preset"`
	RoundNumber          int                        `json:"round_number"`
	SourceRevisionID     uuid.UUID                  `json:"source_revision_id"`
	CategoryRevisionID   uuid.UUID                  `json:"category_revision_id"`
	PoolRevisionID       uuid.UUID                  `json:"pool_revision_id"`
	PlanRevisionID       uuid.UUID                  `json:"plan_revision_id"`
	PreflightRevisionID  uuid.UUID                  `json:"preflight_revision_id"`
	WaveID               uuid.UUID                  `json:"wave_id"`
	WaveRevisionID       domain.ArenaWaveRevisionID `json:"wave_revision_id"`
	Revisions            SwissRoundPlanRevisions    `json:"revisions"`
	RosterParticipantIDs []uuid.UUID                `json:"roster_participant_ids"`
	Series               []SwissLockedSeries        `json:"series"`
	ByeParticipantID     uuid.UUID                  `json:"bye_participant_id"`
	ProofHash            string                     `json:"proof_hash"`
}

type SwissRoundLockCommand struct {
	Proof SwissRoundLockProof
}

type SwissRoundLockInput struct {
	Proof    SwissRoundLockProof
	LockedAt time.Time
}

type SwissRoundLockRecord struct {
	Proof     SwissRoundLockProof
	LockedAt  *time.Time
	StartedAt *time.Time
}

// SwissRoundLockRepository owns the lock transaction. LockSwissRound must
// compare every proof revision and the complete derived membership before it
// persists the lock. It must reject a round that has started in the same CAS.
type SwissRoundLockRepository interface {
	LockSwissRound(ctx context.Context, input SwissRoundLockInput) (*SwissRoundLockRecord, bool, error)
	GetSwissRoundLock(ctx context.Context, roundID uuid.UUID) (*SwissRoundLockRecord, error)
}

type SwissRoundLockUseCase struct {
	repository SwissRoundLockRepository
	clock      Clock
}

func NewSwissRoundLockUseCase(
	repository SwissRoundLockRepository,
	clock Clock,
) *SwissRoundLockUseCase {
	return &SwissRoundLockUseCase{repository: repository, clock: clock}
}

func NewSwissRoundLockProof(input SwissRoundLockProofInput) (SwissRoundLockProof, error) {
	proof := SwissRoundLockProof{
		TournamentID: input.TournamentID, RosterID: input.RosterID, RoundID: input.RoundID,
		Preset: input.Preset, RoundNumber: input.RoundNumber,
		SourceRevisionID: input.SourceRevisionID, CategoryRevisionID: input.CategoryRevisionID,
		PoolRevisionID: input.PoolRevisionID, PlanRevisionID: input.PlanRevisionID,
		PreflightRevisionID: input.PreflightRevisionID, WaveID: input.WaveID,
		WaveRevisionID: input.WaveRevisionID, Revisions: input.Revisions,
		RosterParticipantIDs: append([]uuid.UUID(nil), input.RosterParticipantIDs...),
		Series:               append([]SwissLockedSeries(nil), input.Series...), ByeParticipantID: input.ByeParticipantID,
	}
	canonicalizeSwissRoundLockProof(&proof)
	if err := validateSwissRoundLockProofShape(proof); err != nil {
		return SwissRoundLockProof{}, err
	}
	proofHash, err := swissRoundLockProofHash(proof)
	if err != nil {
		return SwissRoundLockProof{}, err
	}
	proof.ProofHash = proofHash
	return proof, nil
}

func (p SwissRoundLockProof) Validate() error {
	if err := validateSwissRoundLockProofShape(p); err != nil {
		return err
	}
	if !swissRoundLockProofCanonical(p) {
		return swissRoundLockProofError("proof is not canonical")
	}
	want, err := swissRoundLockProofHash(p)
	if err != nil || p.ProofHash != want {
		return swissRoundLockProofError("proof hash does not match the exact plan")
	}
	return nil
}

func (u *SwissRoundLockUseCase) Lock(
	ctx context.Context,
	command SwissRoundLockCommand,
) (*SwissRoundLockRecord, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil {
		return nil, false, domain.ErrValidation
	}
	if err := command.Proof.Validate(); err != nil {
		return nil, false, err
	}
	lockedAt := u.clock.Now()
	if !validArenaServerTime(lockedAt) {
		return nil, false, domain.ErrValidation
	}
	record, changed, err := u.repository.LockSwissRound(ctx, SwissRoundLockInput{
		Proof: cloneSwissRoundLockProof(command.Proof), LockedAt: lockedAt,
	})
	if err != nil {
		return nil, false, swissRoundLockMutationError(err)
	}
	if !changed {
		return u.reconcileLock(ctx, command.Proof)
	}
	if !validSwissRoundLockRecord(record, command.Proof, lockedAt) {
		return nil, false, domain.ErrInternal
	}
	return cloneSwissRoundLockRecord(record), true, nil
}

func (u *SwissRoundLockUseCase) reconcileLock(
	ctx context.Context,
	proof SwissRoundLockProof,
) (*SwissRoundLockRecord, bool, error) {
	current, err := u.repository.GetSwissRoundLock(ctx, proof.RoundID)
	if err != nil {
		return nil, false, swissRoundLockLookupError(err)
	}
	if current == nil || current.Proof.RoundID != proof.RoundID {
		return nil, false, domain.ErrInternal
	}
	if current.StartedAt != nil {
		return nil, false, ErrSwissRoundAlreadyStarted
	}
	if current.LockedAt != nil && current.Proof.ProofHash == proof.ProofHash && current.Proof.Validate() == nil {
		return cloneSwissRoundLockRecord(current), false, nil
	}
	return nil, false, domain.ErrConflict
}

func validateSwissRoundLockProofShape(proof SwissRoundLockProof) error {
	if err := validateSwissRoundLockProofIdentity(proof); err != nil {
		return err
	}
	if !validSwissRoundPlanRevisions(proof.Revisions) {
		return swissRoundLockProofError("source revisions must be positive")
	}
	plan, err := BuildSwissPlan(proof.Preset, len(proof.RosterParticipantIDs))
	if err != nil || proof.RoundNumber < 1 || proof.RoundNumber > len(plan.Rounds) {
		return swissRoundLockProofError("round is outside the preset plan")
	}
	if err := validateSwissRoundRoster(proof.RosterParticipantIDs); err != nil {
		return err
	}
	return validateSwissLockedMembership(proof)
}

func validateSwissRoundLockProofIdentity(proof SwissRoundLockProof) error {
	if proof.TournamentID == uuid.Nil || proof.RosterID == uuid.Nil || proof.RoundID == uuid.Nil ||
		proof.SourceRevisionID == uuid.Nil || proof.CategoryRevisionID == uuid.Nil ||
		proof.PoolRevisionID == uuid.Nil || proof.PlanRevisionID == uuid.Nil ||
		proof.PreflightRevisionID == uuid.Nil || proof.WaveID == uuid.Nil || proof.WaveRevisionID.IsZero() {
		return swissRoundLockProofError("missing exact-plan identity")
	}
	if proof.PlanRevisionID == proof.PreflightRevisionID {
		return swissRoundLockProofError("plan and preflight revisions must differ")
	}
	return nil
}

func validSwissRoundPlanRevisions(revisions SwissRoundPlanRevisions) bool {
	return revisions.Round >= 1 && revisions.Source >= 1 && revisions.Category >= 1 &&
		revisions.Pool >= 1 && revisions.History >= 1 && revisions.Reservation >= 1 &&
		revisions.Membership >= 1
}

func validateSwissRoundRoster(participantIDs []uuid.UUID) error {
	if len(participantIDs) < domain.ArenaMinParticipants || len(participantIDs) > domain.ArenaMaxParticipants {
		return swissRoundLockProofError("invalid roster size")
	}
	for index, participantID := range participantIDs {
		if participantID == uuid.Nil || (index > 0 && participantID == participantIDs[index-1]) {
			return swissRoundLockProofError("roster membership is missing or duplicated")
		}
	}
	return nil
}

func validateSwissLockedMembership(proof SwissRoundLockProof) error {
	if len(proof.Series) != len(proof.RosterParticipantIDs)/2 {
		return swissRoundLockProofError("locked Series count does not cover the roster")
	}
	roster := make(map[uuid.UUID]struct{}, len(proof.RosterParticipantIDs))
	for _, participantID := range proof.RosterParticipantIDs {
		roster[participantID] = struct{}{}
	}
	seenSeries := make(map[uuid.UUID]struct{}, len(proof.Series))
	seenPairings := make(map[uuid.UUID]struct{}, len(proof.Series))
	used := make(map[uuid.UUID]struct{}, len(roster))
	for _, series := range proof.Series {
		if err := validateSwissLockedSeries(series, roster, seenSeries, seenPairings, used); err != nil {
			return err
		}
	}
	if err := validateSwissLockedBye(proof, roster, used); err != nil {
		return err
	}
	if len(used) != len(roster) {
		return swissRoundLockProofError("locked membership is incomplete")
	}
	return nil
}

func validateSwissLockedSeries(
	series SwissLockedSeries,
	roster map[uuid.UUID]struct{},
	seenSeries map[uuid.UUID]struct{},
	seenPairings map[uuid.UUID]struct{},
	used map[uuid.UUID]struct{},
) error {
	if series.SeriesID == uuid.Nil || series.PairingID == uuid.Nil ||
		series.FirstParticipantID == uuid.Nil || series.SecondParticipantID == uuid.Nil ||
		series.FirstParticipantID == series.SecondParticipantID {
		return swissRoundLockProofError("locked Series has invalid identity")
	}
	if _, duplicate := seenSeries[series.SeriesID]; duplicate {
		return swissRoundLockProofError("locked Series is duplicated")
	}
	if _, duplicate := seenPairings[series.PairingID]; duplicate {
		return swissRoundLockProofError("pairing is duplicated")
	}
	seenSeries[series.SeriesID] = struct{}{}
	seenPairings[series.PairingID] = struct{}{}
	for _, participantID := range []uuid.UUID{series.FirstParticipantID, series.SecondParticipantID} {
		if _, exists := roster[participantID]; !exists {
			return swissRoundLockProofError("locked Series contains a foreign participant")
		}
		if _, duplicate := used[participantID]; duplicate {
			return swissRoundLockProofError("participant appears more than once")
		}
		used[participantID] = struct{}{}
	}
	return nil
}

func validateSwissLockedBye(
	proof SwissRoundLockProof,
	roster map[uuid.UUID]struct{},
	used map[uuid.UUID]struct{},
) error {
	wantsBye := len(roster)%2 == 1
	if !wantsBye && proof.ByeParticipantID != uuid.Nil {
		return swissRoundLockProofError("even roster has a bye")
	}
	if !wantsBye {
		return nil
	}
	if _, exists := roster[proof.ByeParticipantID]; !exists {
		return swissRoundLockProofError("odd roster has no valid bye")
	}
	if _, duplicate := used[proof.ByeParticipantID]; duplicate {
		return swissRoundLockProofError("bye participant also appears in a Series")
	}
	used[proof.ByeParticipantID] = struct{}{}
	return nil
}

func canonicalizeSwissRoundLockProof(proof *SwissRoundLockProof) {
	sort.Slice(proof.RosterParticipantIDs, func(i, j int) bool {
		return bytes.Compare(proof.RosterParticipantIDs[i][:], proof.RosterParticipantIDs[j][:]) < 0
	})
	sort.Slice(proof.Series, func(i, j int) bool {
		return swissLockedSeriesCompare(proof.Series[i], proof.Series[j]) < 0
	})
}

func swissRoundLockProofCanonical(proof SwissRoundLockProof) bool {
	return slices.IsSortedFunc(proof.RosterParticipantIDs, func(first, second uuid.UUID) int {
		return bytes.Compare(first[:], second[:])
	}) && slices.IsSortedFunc(proof.Series, swissLockedSeriesCompare)
}

func swissLockedSeriesCompare(first, second SwissLockedSeries) int {
	if comparison := bytes.Compare(first.SeriesID[:], second.SeriesID[:]); comparison != 0 {
		return comparison
	}
	return bytes.Compare(first.PairingID[:], second.PairingID[:])
}

func swissRoundLockProofHash(proof SwissRoundLockProof) (string, error) {
	proof.ProofHash = ""
	payload, err := json.Marshal(proof)
	if err != nil {
		return "", fmt.Errorf("%w: encode exact plan: %w", ErrInvalidSwissRoundLockProof, err)
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func validSwissRoundLockRecord(
	record *SwissRoundLockRecord,
	proof SwissRoundLockProof,
	lockedAt time.Time,
) bool {
	return record != nil && record.LockedAt != nil && record.LockedAt.Equal(lockedAt) &&
		record.StartedAt == nil && record.Proof.ProofHash == proof.ProofHash && record.Proof.Validate() == nil
}

func cloneSwissRoundLockProof(proof SwissRoundLockProof) SwissRoundLockProof {
	cloned := proof
	cloned.RosterParticipantIDs = append([]uuid.UUID(nil), proof.RosterParticipantIDs...)
	cloned.Series = append([]SwissLockedSeries(nil), proof.Series...)
	return cloned
}

func cloneSwissRoundLockRecord(record *SwissRoundLockRecord) *SwissRoundLockRecord {
	if record == nil {
		return nil
	}
	cloned := *record
	cloned.Proof = cloneSwissRoundLockProof(record.Proof)
	cloned.LockedAt = cloneTimePointer(record.LockedAt)
	cloned.StartedAt = cloneTimePointer(record.StartedAt)
	return &cloned
}

func swissRoundLockProofError(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidSwissRoundLockProof, message)
}

func swissRoundLockMutationError(err error) error {
	if errors.Is(err, domain.ErrConflict) || errors.Is(err, ErrSwissRoundNotFound) ||
		errors.Is(err, ErrSwissRoundAlreadyStarted) {
		return err
	}
	return fmt.Errorf("SwissRoundLockUseCase - Lock - SwissRoundLockRepository.LockSwissRound: %w", err)
}

func swissRoundLockLookupError(err error) error {
	if errors.Is(err, ErrSwissRoundNotFound) {
		return err
	}
	return fmt.Errorf("SwissRoundLockUseCase - Lock - SwissRoundLockRepository.GetSwissRoundLock: %w", err)
}
