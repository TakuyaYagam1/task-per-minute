package swiss

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
	ErrInvalidRoundLockProof = errors.New("invalid Swiss round lock proof")
	ErrRoundAlreadyStarted   = errors.New("swiss round already started")
	ErrRoundNotFound         = errors.New("swiss round not found")
)

type Clock interface {
	Now() time.Time
}

type RoundLockRevisions struct {
	SourceProjection int64 `json:"source_projection"`
	Roster           int64 `json:"roster"`
	Round            int64 `json:"round"`
	History          int64 `json:"history"`
	NormalPool       int64 `json:"normal_pool"`
	Wave             int64 `json:"wave"`
}

type LockedSeries struct {
	SeriesID                 uuid.UUID `json:"series_id"`
	PairingID                uuid.UUID `json:"pairing_id"`
	FirstParticipantID       uuid.UUID `json:"first_participant_id"`
	SecondParticipantID      uuid.UUID `json:"second_participant_id"`
	CategoryRevisionID       uuid.UUID `json:"category_revision_id"`
	CategoryRevision         int64     `json:"category_revision"`
	AssignmentID             uuid.UUID `json:"assignment_id"`
	AssignmentRevision       int64     `json:"assignment_revision"`
	AssignmentPlanID         uuid.UUID `json:"assignment_plan_id"`
	AssignmentPlanRevisionID uuid.UUID `json:"assignment_plan_revision_id"`
	ReservationID            uuid.UUID `json:"reservation_id"`
	ReservationRevision      int64     `json:"reservation_revision"`
}

type RoundLockProofInput struct {
	TournamentID               uuid.UUID
	RosterID                   uuid.UUID
	RoundID                    uuid.UUID
	Preset                     domain.TournamentPreset
	RoundNumber                int
	SourceProjectionRevisionID uuid.UUID
	PreflightRevisionID        uuid.UUID
	NormalPoolRevisionID       uuid.UUID
	WaveID                     uuid.UUID
	WaveRevisionID             domain.WaveRevisionID
	Revisions                  RoundLockRevisions
	RosterParticipantIDs       []uuid.UUID
	Series                     []LockedSeries
	ByeParticipantID           uuid.UUID
}

type RoundLockProof struct {
	TournamentID               uuid.UUID               `json:"tournament_id"`
	RosterID                   uuid.UUID               `json:"roster_id"`
	RoundID                    uuid.UUID               `json:"round_id"`
	Preset                     domain.TournamentPreset `json:"preset"`
	RoundNumber                int                     `json:"round_number"`
	SourceProjectionRevisionID uuid.UUID               `json:"source_projection_revision_id"`
	PreflightRevisionID        uuid.UUID               `json:"preflight_revision_id"`
	NormalPoolRevisionID       uuid.UUID               `json:"normal_pool_revision_id"`
	WaveID                     uuid.UUID               `json:"wave_id"`
	WaveRevisionID             domain.WaveRevisionID   `json:"wave_revision_id"`
	Revisions                  RoundLockRevisions      `json:"revisions"`
	RosterParticipantIDs       []uuid.UUID             `json:"roster_participant_ids"`
	Series                     []LockedSeries          `json:"series"`
	ByeParticipantID           uuid.UUID               `json:"bye_participant_id"`
	ProofHash                  string                  `json:"proof_hash"`
}

type RoundLockCommand struct {
	Proof RoundLockProof
}

type RoundLockInput struct {
	Proof    RoundLockProof
	LockedAt time.Time
}

type RoundLockRecord struct {
	Proof     RoundLockProof
	LockedAt  *time.Time
	StartedAt *time.Time
}

// RoundLockRepository owns the lock transaction. LockSwissRound must
// compare every proof revision and the complete derived membership before it
// persists the lock. It must reject a round that has started in the same CAS.
type RoundLockRepository interface {
	LockSwissRound(ctx context.Context, input RoundLockInput) (*RoundLockRecord, bool, error)
	GetSwissRoundLock(ctx context.Context, roundID uuid.UUID) (*RoundLockRecord, error)
}

type RoundLockUseCase struct {
	repository RoundLockRepository
	clock      Clock
}

func NewRoundLockUseCase(
	repository RoundLockRepository,
	clock Clock,
) *RoundLockUseCase {
	return &RoundLockUseCase{repository: repository, clock: clock}
}

func NewRoundLockProof(input RoundLockProofInput) (RoundLockProof, error) {
	proof := RoundLockProof{
		TournamentID: input.TournamentID, RosterID: input.RosterID, RoundID: input.RoundID,
		Preset: input.Preset, RoundNumber: input.RoundNumber,
		SourceProjectionRevisionID: input.SourceProjectionRevisionID,
		PreflightRevisionID:        input.PreflightRevisionID, NormalPoolRevisionID: input.NormalPoolRevisionID,
		WaveID:         input.WaveID,
		WaveRevisionID: input.WaveRevisionID, Revisions: input.Revisions,
		RosterParticipantIDs: append([]uuid.UUID(nil), input.RosterParticipantIDs...),
		Series:               append([]LockedSeries(nil), input.Series...), ByeParticipantID: input.ByeParticipantID,
	}
	canonicalizeRoundLockProof(&proof)
	if err := validateRoundLockProofShape(proof); err != nil {
		return RoundLockProof{}, err
	}
	proofHash, err := roundLockProofHash(proof)
	if err != nil {
		return RoundLockProof{}, err
	}
	proof.ProofHash = proofHash
	return proof, nil
}

func (p RoundLockProof) Validate() error {
	if err := validateRoundLockProofShape(p); err != nil {
		return err
	}
	if !roundLockProofCanonical(p) {
		return roundLockProofError("proof is not canonical")
	}
	want, err := roundLockProofHash(p)
	if err != nil || p.ProofHash != want {
		return roundLockProofError("proof hash does not match the exact plan")
	}
	return nil
}

func (u *RoundLockUseCase) Lock(
	ctx context.Context,
	command RoundLockCommand,
) (*RoundLockRecord, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil {
		return nil, false, domain.ErrValidation
	}
	if err := command.Proof.Validate(); err != nil {
		return nil, false, err
	}
	lockedAt := u.clock.Now()
	if !domain.IsValidServerTime(lockedAt) {
		return nil, false, domain.ErrValidation
	}
	record, changed, err := u.repository.LockSwissRound(ctx, RoundLockInput{
		Proof: CloneRoundLockProof(command.Proof), LockedAt: lockedAt,
	})
	if err != nil {
		return nil, false, roundLockMutationError(err)
	}
	if !changed {
		return u.reconcileLock(ctx, command.Proof)
	}
	if !validRoundLockRecord(record, command.Proof, lockedAt) {
		return nil, false, domain.ErrInternal
	}
	return cloneRoundLockRecord(record), true, nil
}

func (u *RoundLockUseCase) reconcileLock(
	ctx context.Context,
	proof RoundLockProof,
) (*RoundLockRecord, bool, error) {
	current, err := u.repository.GetSwissRoundLock(ctx, proof.RoundID)
	if err != nil {
		return nil, false, roundLockLookupError(err)
	}
	if current == nil || current.Proof.RoundID != proof.RoundID {
		return nil, false, domain.ErrInternal
	}
	if current.StartedAt != nil {
		return nil, false, ErrRoundAlreadyStarted
	}
	if current.LockedAt != nil && current.Proof.ProofHash == proof.ProofHash && current.Proof.Validate() == nil {
		return cloneRoundLockRecord(current), false, nil
	}
	return nil, false, domain.ErrConflict
}

func validateRoundLockProofShape(proof RoundLockProof) error {
	if err := validateRoundLockProofIdentity(proof); err != nil {
		return err
	}
	if !validRoundLockRevisions(proof.Revisions) {
		return roundLockProofError("source revisions must be positive")
	}
	roundCount, err := proof.Preset.SwissRounds(len(proof.RosterParticipantIDs))
	if err != nil || proof.RoundNumber < 1 || proof.RoundNumber > roundCount {
		return roundLockProofError("round is outside the preset plan")
	}
	if err := validateRoundRoster(proof.RosterParticipantIDs); err != nil {
		return err
	}
	return validateLockedMembership(proof)
}

func validateRoundLockProofIdentity(proof RoundLockProof) error {
	if proof.TournamentID == uuid.Nil || proof.RosterID == uuid.Nil || proof.RoundID == uuid.Nil ||
		proof.SourceProjectionRevisionID == uuid.Nil || proof.NormalPoolRevisionID == uuid.Nil ||
		proof.PreflightRevisionID == uuid.Nil ||
		proof.WaveID == uuid.Nil || proof.WaveRevisionID.IsZero() {
		return roundLockProofError("missing exact-plan identity")
	}
	return nil
}

func validRoundLockRevisions(revisions RoundLockRevisions) bool {
	return revisions.SourceProjection >= 1 && revisions.Roster >= 1 && revisions.Round >= 1 &&
		revisions.History >= 0 && revisions.NormalPool >= 1 && revisions.Wave >= 1
}

func validateRoundRoster(participantIDs []uuid.UUID) error {
	if len(participantIDs) < domain.TournamentMinParticipants || len(participantIDs) > domain.TournamentMaxParticipants {
		return roundLockProofError("invalid roster size")
	}
	for index, participantID := range participantIDs {
		if participantID == uuid.Nil || (index > 0 && participantID == participantIDs[index-1]) {
			return roundLockProofError("roster membership is missing or duplicated")
		}
	}
	return nil
}

func validateLockedMembership(proof RoundLockProof) error {
	if len(proof.Series) != len(proof.RosterParticipantIDs)/2 {
		return roundLockProofError("locked Series count does not cover the roster")
	}
	roster := make(map[uuid.UUID]struct{}, len(proof.RosterParticipantIDs))
	for _, participantID := range proof.RosterParticipantIDs {
		roster[participantID] = struct{}{}
	}
	seenSeries := make(map[uuid.UUID]struct{}, len(proof.Series))
	seenPairings := make(map[uuid.UUID]struct{}, len(proof.Series))
	used := make(map[uuid.UUID]struct{}, len(roster))
	for _, series := range proof.Series {
		if err := validateLockedSeries(series, roster, seenSeries, seenPairings, used); err != nil {
			return err
		}
	}
	if err := validateLockedBye(proof, roster, used); err != nil {
		return err
	}
	if len(used) != len(roster) {
		return roundLockProofError("locked membership is incomplete")
	}
	return nil
}

func validateLockedSeries(
	series LockedSeries,
	roster map[uuid.UUID]struct{},
	seenSeries map[uuid.UUID]struct{},
	seenPairings map[uuid.UUID]struct{},
	used map[uuid.UUID]struct{},
) error {
	if series.SeriesID == uuid.Nil || series.PairingID == uuid.Nil ||
		series.FirstParticipantID == uuid.Nil || series.SecondParticipantID == uuid.Nil ||
		series.FirstParticipantID == series.SecondParticipantID || series.CategoryRevisionID == uuid.Nil ||
		series.CategoryRevision < 1 || series.AssignmentID == uuid.Nil || series.AssignmentRevision < 1 ||
		series.AssignmentPlanID == uuid.Nil || series.AssignmentPlanRevisionID == uuid.Nil ||
		series.ReservationID == uuid.Nil || series.ReservationRevision < 1 {
		return roundLockProofError("locked Series has invalid identity")
	}
	if _, duplicate := seenSeries[series.SeriesID]; duplicate {
		return roundLockProofError("locked Series is duplicated")
	}
	if _, duplicate := seenPairings[series.PairingID]; duplicate {
		return roundLockProofError("pairing is duplicated")
	}
	seenSeries[series.SeriesID] = struct{}{}
	seenPairings[series.PairingID] = struct{}{}
	for _, participantID := range []uuid.UUID{series.FirstParticipantID, series.SecondParticipantID} {
		if _, exists := roster[participantID]; !exists {
			return roundLockProofError("locked Series contains a foreign participant")
		}
		if _, duplicate := used[participantID]; duplicate {
			return roundLockProofError("participant appears more than once")
		}
		used[participantID] = struct{}{}
	}
	return nil
}

func validateLockedBye(
	proof RoundLockProof,
	roster map[uuid.UUID]struct{},
	used map[uuid.UUID]struct{},
) error {
	wantsBye := len(roster)%2 == 1
	if !wantsBye && proof.ByeParticipantID != uuid.Nil {
		return roundLockProofError("even roster has a bye")
	}
	if !wantsBye {
		return nil
	}
	if _, exists := roster[proof.ByeParticipantID]; !exists {
		return roundLockProofError("odd roster has no valid bye")
	}
	if _, duplicate := used[proof.ByeParticipantID]; duplicate {
		return roundLockProofError("bye participant also appears in a Series")
	}
	used[proof.ByeParticipantID] = struct{}{}
	return nil
}

func canonicalizeRoundLockProof(proof *RoundLockProof) {
	sort.Slice(proof.RosterParticipantIDs, func(i, j int) bool {
		return bytes.Compare(proof.RosterParticipantIDs[i][:], proof.RosterParticipantIDs[j][:]) < 0
	})
	sort.Slice(proof.Series, func(i, j int) bool {
		return lockedSeriesCompare(proof.Series[i], proof.Series[j]) < 0
	})
}

func roundLockProofCanonical(proof RoundLockProof) bool {
	return slices.IsSortedFunc(proof.RosterParticipantIDs, func(first, second uuid.UUID) int {
		return bytes.Compare(first[:], second[:])
	}) && slices.IsSortedFunc(proof.Series, lockedSeriesCompare)
}

func lockedSeriesCompare(first, second LockedSeries) int {
	if comparison := bytes.Compare(first.SeriesID[:], second.SeriesID[:]); comparison != 0 {
		return comparison
	}
	return bytes.Compare(first.PairingID[:], second.PairingID[:])
}

func roundLockProofHash(proof RoundLockProof) (string, error) {
	proof.ProofHash = ""
	payload, err := json.Marshal(proof)
	if err != nil {
		return "", fmt.Errorf("%w: encode exact plan: %w", ErrInvalidRoundLockProof, err)
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func validRoundLockRecord(
	record *RoundLockRecord,
	proof RoundLockProof,
	lockedAt time.Time,
) bool {
	return record != nil && record.LockedAt != nil && record.LockedAt.Equal(lockedAt) &&
		record.StartedAt == nil && record.Proof.ProofHash == proof.ProofHash && record.Proof.Validate() == nil
}

func CloneRoundLockProof(proof RoundLockProof) RoundLockProof {
	cloned := proof
	cloned.RosterParticipantIDs = append([]uuid.UUID(nil), proof.RosterParticipantIDs...)
	cloned.Series = append([]LockedSeries(nil), proof.Series...)
	return cloned
}

func cloneRoundLockRecord(record *RoundLockRecord) *RoundLockRecord {
	if record == nil {
		return nil
	}
	cloned := *record
	cloned.Proof = CloneRoundLockProof(record.Proof)
	cloned.LockedAt = cloneTimePointer(record.LockedAt)
	cloned.StartedAt = cloneTimePointer(record.StartedAt)
	return &cloned
}

// CloneLockRecord returns an independent copy of a lock record.
func CloneLockRecord(record *RoundLockRecord) *RoundLockRecord {
	return cloneRoundLockRecord(record)
}

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func roundLockProofError(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidRoundLockProof, message)
}

func roundLockMutationError(err error) error {
	if errors.Is(err, domain.ErrConflict) || errors.Is(err, ErrRoundNotFound) ||
		errors.Is(err, ErrRoundAlreadyStarted) {
		return err
	}
	return fmt.Errorf("SwissRoundLockUseCase - Lock - SwissRoundLockRepository.LockSwissRound: %w", err)
}

func roundLockLookupError(err error) error {
	if errors.Is(err, ErrRoundNotFound) {
		return err
	}
	return fmt.Errorf("SwissRoundLockUseCase - Lock - SwissRoundLockRepository.GetSwissRoundLock: %w", err)
}
