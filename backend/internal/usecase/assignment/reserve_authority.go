package assignment

import (
	"bytes"
	"crypto/sha256"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/taskexec"
)

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
	decision, err := EvaluateTaskEligibility(TaskEligibilityInput{
		Pool: pool, Category: authority.CandidateSnapshot.Category,
		ParticipantIDs: participants,
		Candidate: TaskEligibilityCandidate{
			Version: authority.CandidateSnapshot.Version,
			Task:    taskFromSnapshot(authority.CandidateSnapshot),
			Health:  authority.CandidateHealth,
		},
		ReceiptHistory: authority.ReceiptHistory,
	})
	if err != nil {
		return ReserveAssignmentAuthority{}, TaskEligibilityDecision{}, reserveAssignmentError("eligibility: %v", err)
	}
	canonical := authority
	canonical.Pool = pool
	canonical.ParticipantIDs = participants
	canonical.ParticipantReservations = reservations
	canonical.ReceiptHistory = canonicalReserveHistory(authority.ReceiptHistory)
	canonical.CandidateSnapshot = cloneTaskSnapshot(authority.CandidateSnapshot)
	canonical.CategoryExhaustion = cloneReserveCategoryExhaustion(authority.CategoryExhaustion)
	return canonical, decision, nil
}

func validateReserveAuthorityIdentity(scope ReserveAssignmentScope, authority ReserveAssignmentAuthority) error {
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

func normalizeReserveAuthorityPool(authority ReserveAssignmentAuthority) (domain.TaskPoolRevision, error) {
	pool, err := domain.NormalizeTaskPoolRevision(authority.Pool, authority.CandidateSnapshot.Kind)
	if err != nil || pool.ID != authority.Revisions.PoolRevisionID ||
		pool.Revision != authority.Revisions.PoolRevision {
		return domain.TaskPoolRevision{}, reserveAssignmentError("invalid pool authority")
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
			item.Reservation.TournamentID != scope.TournamentID {
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

// ValidReserveAssignmentSourceRevisions exposes the source-revision check to
// the parent coordinator without coupling this package back to it.
func ValidReserveAssignmentSourceRevisions(revisions ReserveAssignmentSourceRevisions) bool {
	return validReserveAssignmentSourceRevisions(revisions)
}

// NormalizeReserveAssignmentAuthority canonicalizes retained authority for a
// larger atomic transition.
func NormalizeReserveAssignmentAuthority(
	scope ReserveAssignmentScope,
	authority ReserveAssignmentAuthority,
) (ReserveAssignmentAuthority, TaskEligibilityDecision, error) {
	return normalizeReserveAssignmentAuthority(scope, authority)
}
