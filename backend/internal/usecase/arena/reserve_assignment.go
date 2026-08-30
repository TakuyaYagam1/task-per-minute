package arena

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/taskexec"
)

const reserveAssignmentAttempts = 2

var (
	ErrInvalidReserveAssignment  = errors.New("invalid reserve assignment")
	ErrReserveAssignmentConflict = errors.New("reserve assignment conflict")
)

type ReserveAssignmentMode string

const (
	ReserveAssignmentModeAutomatic ReserveAssignmentMode = "automatic"
	ReserveAssignmentModeOperator  ReserveAssignmentMode = "operator"
)

type ReserveAssignmentScope struct {
	TournamentID uuid.UUID
	AssignmentID uuid.UUID
	AttemptID    uuid.UUID
	SlotID       uuid.UUID
}

type ReserveAssignmentSourceRevisions struct {
	AssignmentRevision    int64
	PoolRevisionID        uuid.UUID
	PoolRevision          int64
	HistoryRevisionID     uuid.UUID
	HistoryRevision       int64
	ArtifactRevisionID    uuid.UUID
	ArtifactRevision      int64
	ReservationRevisionID uuid.UUID
	ReservationRevision   int64
	CategoryRevisionID    uuid.UUID
	CategoryRevision      int64
}

type ReserveCategoryExhaustionEvidence struct {
	RequiredCategory     domain.Category
	EligibleSameCategory []TaskVersionRef
	Reason               string
	ProofDigest          [sha256.Size]byte
}

type ReserveAssignmentAuthority struct {
	Scope                   ReserveAssignmentScope
	Revisions               ReserveAssignmentSourceRevisions
	CurrentSnapshotID       uuid.UUID
	RequiredCategory        domain.Category
	ParticipantIDs          []uuid.UUID
	ParticipantReservations []ExactNormalParticipantReservation
	Pool                    TaskPoolRevision
	ArenaHistory            []ArenaTaskReceiptRef
	CandidateHealth         TaskVersionHealth
	CandidateSnapshot       domain.ArenaTaskSnapshot
	CandidateContentDigest  [sha256.Size]byte
	CategoryExhaustion      *ReserveCategoryExhaustionEvidence
}

type ReserveAssignmentCommand struct {
	Scope              ReserveAssignmentScope
	ExpectedSnapshotID uuid.UUID
	EvidenceID         uuid.UUID
	Mode               ReserveAssignmentMode
	OperatorID         uuid.UUID
	Reason             string
	PromotedAt         time.Time
}

type ReserveAssignmentEvidence struct {
	ID         uuid.UUID
	Mode       ReserveAssignmentMode
	OperatorID uuid.UUID
	Reason     string
	TaskID     uuid.UUID
	Version    int
	SnapshotID uuid.UUID
	DecidedAt  time.Time
	Digest     [sha256.Size]byte
}

type ReserveAssignmentRecord struct {
	Scope                   ReserveAssignmentScope
	Revisions               ReserveAssignmentSourceRevisions
	FromSnapshotID          uuid.UUID
	RequiredCategory        domain.Category
	ParticipantIDs          []uuid.UUID
	ParticipantReservations []ExactNormalParticipantReservation
	Pool                    TaskPoolRevision
	ArenaHistory            []ArenaTaskReceiptRef
	CandidateHealth         TaskVersionHealth
	Snapshot                domain.ArenaTaskSnapshot
	ContentDigest           [sha256.Size]byte
	CategoryExhaustion      *ReserveCategoryExhaustionEvidence
	Evidence                ReserveAssignmentEvidence
	PromotedAt              time.Time
	ProofDigest             [sha256.Size]byte
}

// ReserveAssignmentRepository owns one transaction that locks the assignment
// and revalidates pool, Arena receipt history, artifact, participant
// reservations, category and candidate health before promoting one reserve.
type ReserveAssignmentRepository interface {
	LoadReserveAssignmentAuthority(
		ctx context.Context,
		scope ReserveAssignmentScope,
	) (ReserveAssignmentAuthority, error)
	CommitReserveAssignment(
		ctx context.Context,
		record ReserveAssignmentRecord,
	) (*ReserveAssignmentRecord, bool, error)
}

type ReserveAssignmentUseCase struct {
	repository ReserveAssignmentRepository
}

func NewReserveAssignmentUseCase(
	repository ReserveAssignmentRepository,
) *ReserveAssignmentUseCase {
	return &ReserveAssignmentUseCase{repository: repository}
}

func (u *ReserveAssignmentUseCase) Promote(
	ctx context.Context,
	command ReserveAssignmentCommand,
) (*ReserveAssignmentRecord, bool, error) {
	if u == nil || u.repository == nil {
		return nil, false, domain.ErrValidation
	}
	command.Reason = strings.TrimSpace(command.Reason)
	if err := validateReserveAssignmentCommand(command); err != nil {
		return nil, false, err
	}

	for range reserveAssignmentAttempts {
		authority, err := u.repository.LoadReserveAssignmentAuthority(ctx, command.Scope)
		if err != nil {
			return nil, false, fmt.Errorf("ReserveAssignmentUseCase - load authority: %w", err)
		}
		record, err := buildReserveAssignmentRecord(command, authority)
		if err != nil {
			return nil, false, err
		}
		committed, changed, err := u.repository.CommitReserveAssignment(ctx, record)
		if errors.Is(err, domain.ErrConflict) {
			continue
		}
		if err != nil {
			return nil, false, fmt.Errorf("ReserveAssignmentUseCase - commit reserve: %w", err)
		}
		if committed == nil || committed.Validate() != nil || committed.ProofDigest != record.ProofDigest {
			return nil, false, domain.ErrInternal
		}
		result := cloneReserveAssignmentRecord(*committed)
		return &result, changed, nil
	}
	return nil, false, ErrReserveAssignmentConflict
}

func (r ReserveAssignmentRecord) Validate() error {
	authority := ReserveAssignmentAuthority{
		Scope: r.Scope, Revisions: r.Revisions, CurrentSnapshotID: r.FromSnapshotID,
		RequiredCategory:        r.RequiredCategory,
		ParticipantIDs:          append([]uuid.UUID(nil), r.ParticipantIDs...),
		ParticipantReservations: cloneExactNormalParticipantReservations(r.ParticipantReservations),
		Pool:                    cloneTaskPool(r.Pool), ArenaHistory: append([]ArenaTaskReceiptRef(nil), r.ArenaHistory...),
		CandidateHealth: r.CandidateHealth, CandidateSnapshot: cloneTaskSnapshot(r.Snapshot),
		CandidateContentDigest: r.ContentDigest,
		CategoryExhaustion:     cloneReserveCategoryExhaustion(r.CategoryExhaustion),
	}
	canonical, decision, err := normalizeReserveAssignmentAuthority(r.Scope, authority)
	if err != nil || !decision.Eligible {
		return reserveAssignmentError("invalid retained authority")
	}
	if canonical.CurrentSnapshotID == canonical.CandidateSnapshot.SnapshotID ||
		r.Evidence.TaskID != canonical.CandidateSnapshot.TaskID ||
		r.Evidence.Version != canonical.CandidateSnapshot.Version ||
		r.Evidence.SnapshotID != canonical.CandidateSnapshot.SnapshotID ||
		!r.PromotedAt.Equal(r.Evidence.DecidedAt) {
		return reserveAssignmentError("evidence does not match the promoted reserve")
	}
	if err := validateReserveCategoryTransition(canonical); err != nil {
		return err
	}
	if err := validateReserveAssignmentEvidence(r.Evidence); err != nil {
		return err
	}
	want, err := reserveAssignmentProofDigest(r)
	if err != nil || r.ProofDigest == [sha256.Size]byte{} || want != r.ProofDigest {
		return reserveAssignmentError("proof digest does not match")
	}
	return nil
}

func buildReserveAssignmentRecord(
	command ReserveAssignmentCommand,
	authority ReserveAssignmentAuthority,
) (ReserveAssignmentRecord, error) {
	canonical, decision, err := normalizeReserveAssignmentAuthority(command.Scope, authority)
	if err != nil {
		return ReserveAssignmentRecord{}, err
	}
	if !decision.Eligible {
		return ReserveAssignmentRecord{}, fmt.Errorf("%w: %v", ErrTaskIneligible, decision.Reasons)
	}
	if canonical.CurrentSnapshotID != command.ExpectedSnapshotID {
		return ReserveAssignmentRecord{}, reserveAssignmentError("active snapshot changed")
	}
	if err := validateReserveCategoryTransition(canonical); err != nil {
		return ReserveAssignmentRecord{}, err
	}
	evidence, err := newReserveAssignmentEvidence(command, canonical.CandidateSnapshot)
	if err != nil {
		return ReserveAssignmentRecord{}, err
	}
	record := ReserveAssignmentRecord{
		Scope: canonical.Scope, Revisions: canonical.Revisions,
		FromSnapshotID: canonical.CurrentSnapshotID, RequiredCategory: canonical.RequiredCategory,
		ParticipantIDs:          append([]uuid.UUID(nil), canonical.ParticipantIDs...),
		ParticipantReservations: cloneExactNormalParticipantReservations(canonical.ParticipantReservations),
		Pool:                    cloneTaskPool(canonical.Pool),
		ArenaHistory:            append([]ArenaTaskReceiptRef(nil), canonical.ArenaHistory...),
		CandidateHealth:         canonical.CandidateHealth,
		Snapshot:                cloneTaskSnapshot(canonical.CandidateSnapshot),
		ContentDigest:           canonical.CandidateContentDigest,
		CategoryExhaustion:      cloneReserveCategoryExhaustion(canonical.CategoryExhaustion),
		Evidence:                evidence, PromotedAt: command.PromotedAt,
	}
	record.ProofDigest, err = reserveAssignmentProofDigest(record)
	if err != nil {
		return ReserveAssignmentRecord{}, err
	}
	if err := record.Validate(); err != nil {
		return ReserveAssignmentRecord{}, err
	}
	return cloneReserveAssignmentRecord(record), nil
}

func normalizeReserveAssignmentAuthority(
	scope ReserveAssignmentScope,
	authority ReserveAssignmentAuthority,
) (ReserveAssignmentAuthority, TaskEligibilityDecision, error) {
	if err := validateReserveAuthorityIdentity(scope, authority); err != nil {
		return ReserveAssignmentAuthority{}, TaskEligibilityDecision{}, err
	}
	pool, err := normalizeReserveAuthorityPool(authority)
	if err != nil {
		return ReserveAssignmentAuthority{}, TaskEligibilityDecision{}, err
	}
	participants, err := normalizeExactNormalParticipants(authority.ParticipantIDs)
	if err != nil {
		return ReserveAssignmentAuthority{}, TaskEligibilityDecision{}, reserveAssignmentError("participants: %v", err)
	}
	reservations, err := normalizeReserveParticipantReservations(scope, participants, authority.ParticipantReservations)
	if err != nil {
		return ReserveAssignmentAuthority{}, TaskEligibilityDecision{}, err
	}
	if err := validateReserveCandidateArtifact(authority); err != nil {
		return ReserveAssignmentAuthority{}, TaskEligibilityDecision{}, err
	}
	eligibility := TaskEligibilityInput{
		Pool: pool, Category: authority.CandidateSnapshot.Category,
		ParticipantIDs: participants,
		Candidate: TaskEligibilityCandidate{
			Version: authority.CandidateSnapshot.Version,
			Task:    taskFromSnapshot(authority.CandidateSnapshot),
			Health:  authority.CandidateHealth,
		},
		ArenaHistory: authority.ArenaHistory,
	}
	decision, err := EvaluateTaskEligibility(eligibility)
	if err != nil {
		return ReserveAssignmentAuthority{}, TaskEligibilityDecision{}, reserveAssignmentError("eligibility: %v", err)
	}
	canonical := authority
	canonical.Pool = pool
	canonical.ParticipantIDs = participants
	canonical.ParticipantReservations = reservations
	canonical.ArenaHistory = canonicalReserveArenaHistory(authority.ArenaHistory)
	canonical.CandidateSnapshot = cloneTaskSnapshot(authority.CandidateSnapshot)
	canonical.CategoryExhaustion = cloneReserveCategoryExhaustion(authority.CategoryExhaustion)
	return canonical, decision, nil
}

func validateReserveAuthorityIdentity(
	scope ReserveAssignmentScope,
	authority ReserveAssignmentAuthority,
) error {
	if authority.Scope != scope || !validReserveAssignmentScope(scope) ||
		!validReserveAssignmentSourceRevisions(authority.Revisions) ||
		authority.CurrentSnapshotID == uuid.Nil || !authority.RequiredCategory.IsValid() {
		return reserveAssignmentError("invalid authority identity")
	}
	if authority.CandidateSnapshot.Validate() != nil ||
		authority.CurrentSnapshotID == authority.CandidateSnapshot.SnapshotID {
		return reserveAssignmentError("invalid reserve snapshot")
	}
	return nil
}

func normalizeReserveAuthorityPool(
	authority ReserveAssignmentAuthority,
) (TaskPoolRevision, error) {
	pool, err := normalizeTaskPoolRevision(authority.Pool, authority.CandidateSnapshot.Kind)
	if err != nil || pool.ID != authority.Revisions.PoolRevisionID ||
		pool.Revision != authority.Revisions.PoolRevision {
		return TaskPoolRevision{}, reserveAssignmentError("invalid pool authority")
	}
	return pool, nil
}

func validateReserveCandidateArtifact(authority ReserveAssignmentAuthority) error {
	digest, err := taskexec.SnapshotDigest(authority.CandidateSnapshot)
	if err != nil || digest != authority.CandidateContentDigest {
		return reserveAssignmentError("candidate artifact changed")
	}
	return nil
}

func normalizeReserveParticipantReservations(
	scope ReserveAssignmentScope,
	participants []uuid.UUID,
	input []ExactNormalParticipantReservation,
) ([]ExactNormalParticipantReservation, error) {
	result := cloneExactNormalParticipantReservations(input)
	sort.Slice(result, func(i, j int) bool {
		return bytes.Compare(result[i].ParticipantID[:], result[j].ParticipantID[:]) < 0
	})
	if len(result) != len(participants) {
		return nil, reserveAssignmentError("participant reservations do not cover the assignment")
	}
	for index, item := range result {
		if item.ParticipantID != participants[index] || item.PlayerID == uuid.Nil ||
			item.Reservation.PlayerID != item.PlayerID || !item.Reservation.IsValid() ||
			item.Reservation.OwnerKind != domain.ParticipantReservationOwnerArena ||
			item.Reservation.OwnerID != scope.TournamentID {
			return nil, reserveAssignmentError("invalid participant reservation authority")
		}
	}
	return result, nil
}

func validateReserveCategoryTransition(authority ReserveAssignmentAuthority) error {
	if authority.CandidateSnapshot.Category == authority.RequiredCategory {
		if authority.CategoryExhaustion != nil {
			return reserveAssignmentError("same-category reserve has unnecessary exhaustion evidence")
		}
		return nil
	}
	exhaustion := authority.CategoryExhaustion
	if exhaustion == nil || exhaustion.RequiredCategory != authority.RequiredCategory ||
		len(exhaustion.EligibleSameCategory) != 0 || strings.TrimSpace(exhaustion.Reason) == "" ||
		exhaustion.ProofDigest == [sha256.Size]byte{} {
		return reserveAssignmentError("category changed without verified exhaustion")
	}
	return nil
}

func newReserveAssignmentEvidence(
	command ReserveAssignmentCommand,
	snapshot domain.ArenaTaskSnapshot,
) (ReserveAssignmentEvidence, error) {
	evidence := ReserveAssignmentEvidence{
		ID: command.EvidenceID, Mode: command.Mode, OperatorID: command.OperatorID,
		Reason: command.Reason, TaskID: snapshot.TaskID, Version: snapshot.Version,
		SnapshotID: snapshot.SnapshotID, DecidedAt: command.PromotedAt,
	}
	digest, err := reserveAssignmentEvidenceDigest(evidence)
	if err != nil {
		return ReserveAssignmentEvidence{}, reserveAssignmentError("evidence digest: %v", err)
	}
	evidence.Digest = digest
	return evidence, nil
}

func validateReserveAssignmentEvidence(evidence ReserveAssignmentEvidence) error {
	if evidence.ID == uuid.Nil || evidence.TaskID == uuid.Nil || evidence.Version < 1 ||
		evidence.SnapshotID == uuid.Nil || evidence.DecidedAt.IsZero() ||
		evidence.DecidedAt.Location() != time.UTC {
		return reserveAssignmentError("invalid decision evidence identity")
	}
	if err := validateReserveAssignmentEvidenceMode(evidence); err != nil {
		return err
	}
	want, err := reserveAssignmentEvidenceDigest(evidence)
	if err != nil || evidence.Digest == [sha256.Size]byte{} || want != evidence.Digest {
		return reserveAssignmentError("decision evidence digest does not match")
	}
	return nil
}

func validateReserveAssignmentEvidenceMode(evidence ReserveAssignmentEvidence) error {
	switch evidence.Mode {
	case ReserveAssignmentModeAutomatic:
		if evidence.OperatorID != uuid.Nil || evidence.Reason != "" {
			return reserveAssignmentError("automatic evidence cannot claim an operator")
		}
	case ReserveAssignmentModeOperator:
		if evidence.OperatorID == uuid.Nil || strings.TrimSpace(evidence.Reason) == "" {
			return reserveAssignmentError("operator evidence requires actor and reason")
		}
	default:
		return reserveAssignmentError("unknown reserve assignment mode")
	}
	return nil
}

func validateReserveAssignmentCommand(command ReserveAssignmentCommand) error {
	if !validReserveAssignmentScope(command.Scope) || command.ExpectedSnapshotID == uuid.Nil ||
		command.EvidenceID == uuid.Nil || command.PromotedAt.IsZero() ||
		command.PromotedAt.Location() != time.UTC {
		return reserveAssignmentError("invalid command identity or timestamp")
	}
	switch command.Mode {
	case ReserveAssignmentModeAutomatic:
		if command.OperatorID != uuid.Nil || command.Reason != "" {
			return reserveAssignmentError("automatic promotion cannot claim an operator")
		}
	case ReserveAssignmentModeOperator:
		if command.OperatorID == uuid.Nil || command.Reason == "" {
			return reserveAssignmentError("operator promotion requires actor and reason")
		}
	default:
		return reserveAssignmentError("unknown promotion mode")
	}
	return nil
}

func validReserveAssignmentScope(scope ReserveAssignmentScope) bool {
	return scope.TournamentID != uuid.Nil && scope.AssignmentID != uuid.Nil &&
		scope.AttemptID != uuid.Nil && scope.SlotID != uuid.Nil
}

func validReserveAssignmentSourceRevisions(revisions ReserveAssignmentSourceRevisions) bool {
	return revisions.AssignmentRevision >= 1 &&
		revisions.PoolRevisionID != uuid.Nil && revisions.PoolRevision >= 1 &&
		revisions.HistoryRevisionID != uuid.Nil && revisions.HistoryRevision >= 1 &&
		revisions.ArtifactRevisionID != uuid.Nil && revisions.ArtifactRevision >= 1 &&
		revisions.ReservationRevisionID != uuid.Nil && revisions.ReservationRevision >= 1 &&
		revisions.CategoryRevisionID != uuid.Nil && revisions.CategoryRevision >= 1
}

func taskFromSnapshot(snapshot domain.ArenaTaskSnapshot) domain.Task {
	return domain.Task{
		ID: snapshot.TaskID, Title: snapshot.Title, Description: snapshot.Description,
		Category: snapshot.Category, Difficulty: snapshot.Difficulty,
		TimeLimit: snapshot.TimeLimit, Flag: snapshot.Flag,
		Hints:         append([]string(nil), snapshot.Hints...),
		TaskURL:       cloneArenaStringPointer(snapshot.TaskURL),
		SourceFileURL: cloneArenaStringPointer(snapshot.SourceFileURL),
	}
}

func canonicalReserveArenaHistory(history []ArenaTaskReceiptRef) []ArenaTaskReceiptRef {
	result := append([]ArenaTaskReceiptRef(nil), history...)
	sort.Slice(result, func(i, j int) bool {
		if comparison := bytes.Compare(result[i].ParticipantID[:], result[j].ParticipantID[:]); comparison != 0 {
			return comparison < 0
		}
		if comparison := bytes.Compare(result[i].TaskID[:], result[j].TaskID[:]); comparison != 0 {
			return comparison < 0
		}
		return result[i].Version < result[j].Version
	})
	return result
}

func cloneReserveCategoryExhaustion(
	evidence *ReserveCategoryExhaustionEvidence,
) *ReserveCategoryExhaustionEvidence {
	if evidence == nil {
		return nil
	}
	clone := *evidence
	clone.EligibleSameCategory = append([]TaskVersionRef(nil), evidence.EligibleSameCategory...)
	return &clone
}

func cloneReserveAssignmentRecord(record ReserveAssignmentRecord) ReserveAssignmentRecord {
	clone := record
	clone.ParticipantIDs = append([]uuid.UUID(nil), record.ParticipantIDs...)
	clone.ParticipantReservations = cloneExactNormalParticipantReservations(record.ParticipantReservations)
	clone.Pool = cloneTaskPool(record.Pool)
	clone.ArenaHistory = append([]ArenaTaskReceiptRef(nil), record.ArenaHistory...)
	clone.Snapshot = cloneTaskSnapshot(record.Snapshot)
	clone.CategoryExhaustion = cloneReserveCategoryExhaustion(record.CategoryExhaustion)
	return clone
}

func reserveAssignmentEvidenceDigest(
	evidence ReserveAssignmentEvidence,
) ([sha256.Size]byte, error) {
	document := struct {
		ID         uuid.UUID             `json:"id"`
		Mode       ReserveAssignmentMode `json:"mode"`
		OperatorID uuid.UUID             `json:"operator_id"`
		Reason     string                `json:"reason"`
		TaskID     uuid.UUID             `json:"task_id"`
		Version    int                   `json:"version"`
		SnapshotID uuid.UUID             `json:"snapshot_id"`
		DecidedAt  time.Time             `json:"decided_at"`
	}{
		ID: evidence.ID, Mode: evidence.Mode, OperatorID: evidence.OperatorID,
		Reason: evidence.Reason, TaskID: evidence.TaskID, Version: evidence.Version,
		SnapshotID: evidence.SnapshotID, DecidedAt: evidence.DecidedAt,
	}
	payload, err := json.Marshal(document)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(payload), nil
}

func reserveAssignmentProofDigest(record ReserveAssignmentRecord) ([sha256.Size]byte, error) {
	document := map[string]any{
		"scope":                    record.Scope,
		"revisions":                record.Revisions,
		"from_snapshot_id":         record.FromSnapshotID,
		"required_category":        record.RequiredCategory,
		"participant_ids":          record.ParticipantIDs,
		"participant_reservations": record.ParticipantReservations,
		"pool":                     record.Pool,
		"arena_history":            record.ArenaHistory,
		"candidate_health":         record.CandidateHealth,
		"snapshot":                 record.Snapshot,
		"content_digest":           record.ContentDigest,
		"category_exhaustion":      record.CategoryExhaustion,
		"evidence":                 record.Evidence,
		"promoted_at":              record.PromotedAt,
	}
	payload, err := json.Marshal(document)
	if err != nil {
		return [sha256.Size]byte{}, reserveAssignmentError("encode proof: %v", err)
	}
	return sha256.Sum256(payload), nil
}

func reserveAssignmentError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidReserveAssignment, fmt.Sprintf(format, arguments...))
}
