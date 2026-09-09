package assignment

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/capacity"
)

type exactNormalAssignmentProofDocument struct {
	PlanID                  string                                           `json:"plan_id"`
	PlanRevisionID          string                                           `json:"plan_revision_id"`
	BranchID                string                                           `json:"branch_id"`
	Scope                   exactNormalAssignmentScopeProofDocument          `json:"scope"`
	Category                domain.Category                                  `json:"category"`
	Revisions               exactNormalAssignmentRevisionsProofDocument      `json:"revisions"`
	ParticipantIDs          []string                                         `json:"participant_ids"`
	ParticipantReservations []exactNormalParticipantReservationProofDocument `json:"participant_reservations"`
	History                 []exactNormalHistoryProofDocument                `json:"history"`
	CandidateTaskVersions   []exactNormalTaskVersionProofDocument            `json:"candidate_task_versions"`
	GraphDigest             string                                           `json:"graph_digest"`
	ArtifactDigest          string                                           `json:"artifact_digest"`
	DecisionEvidenceID      string                                           `json:"decision_evidence_id"`
	DecisionReplayDigest    string                                           `json:"decision_replay_digest"`
	SelectedEdges           []exactNormalAssignmentEdgeProofDocument         `json:"selected_edges"`
	CreatedAt               time.Time                                        `json:"created_at"`
}

type exactNormalAssignmentScopeProofDocument struct {
	TournamentID   string `json:"tournament_id"`
	RosterID       string `json:"roster_id"`
	SeriesID       string `json:"series_id"`
	SlotID         string `json:"slot_id"`
	CategoryLockID string `json:"category_lock_id"`
}

type exactNormalAssignmentRevisionsProofDocument struct {
	SeriesRevision     int64  `json:"series_revision"`
	PoolRevisionID     string `json:"pool_revision_id"`
	PoolRevision       int64  `json:"pool_revision"`
	HistoryRevisionID  string `json:"history_revision_id"`
	HistoryRevision    int64  `json:"history_revision"`
	RosterRevision     int64  `json:"roster_revision"`
	ArtifactRevisionID string `json:"artifact_revision_id"`
	ArtifactRevision   int64  `json:"artifact_revision"`
	CategoryRevisionID string `json:"category_revision_id"`
	CategoryRevision   int64  `json:"category_revision"`
}

type exactNormalParticipantReservationProofDocument struct {
	ParticipantID string    `json:"participant_id"`
	PlayerID      string    `json:"player_id"`
	ReservationID string    `json:"reservation_id"`
	TournamentID  string    `json:"tournament_id"`
	Revision      int64     `json:"revision"`
	AcquiredAt    time.Time `json:"acquired_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type exactNormalHistoryProofDocument struct {
	ParticipantID string `json:"participant_id"`
	TaskID        string `json:"task_id"`
}

type exactNormalTaskVersionProofDocument struct {
	TaskID  string `json:"task_id"`
	Version int    `json:"version"`
}

type exactNormalAssignmentEdgeProofDocument struct {
	ID            string `json:"id"`
	ReservationID string `json:"reservation_id"`
	Position      int    `json:"position"`
	SnapshotID    string `json:"snapshot_id"`
	TaskID        string `json:"task_id"`
	TaskVersion   int    `json:"task_version"`
	ContentDigest string `json:"content_digest"`
}

func exactNormalAssignmentProofHash(plan ExactNormalAssignmentPlan) (string, error) {
	document := exactNormalAssignmentProofDocument{
		PlanID: plan.PlanID.String(), PlanRevisionID: plan.PlanRevisionID.String(), BranchID: plan.BranchID.String(),
		Scope: exactNormalScopeProofDocument(plan.Scope), Category: plan.Category,
		Revisions:               exactNormalRevisionsProofDocument(plan.Revisions),
		ParticipantIDs:          exactNormalParticipantIDDocuments(plan.ParticipantIDs),
		ParticipantReservations: exactNormalParticipantReservationDocuments(plan.ParticipantReservations),
		History:                 exactNormalHistoryDocuments(plan.History),
		CandidateTaskVersions:   exactNormalTaskVersionDocuments(plan.CandidateTaskVersions),
		GraphDigest:             hex.EncodeToString(plan.GraphDigest[:]), ArtifactDigest: hex.EncodeToString(plan.ArtifactDigest[:]),
		DecisionEvidenceID:   plan.DecisionEvidence.ID.String(),
		DecisionReplayDigest: hex.EncodeToString(plan.DecisionEvidence.ReplayDigest[:]),
		SelectedEdges:        make([]exactNormalAssignmentEdgeProofDocument, len(plan.SelectedEdges)),
		CreatedAt:            plan.CreatedAt,
	}
	for index, edge := range plan.SelectedEdges {
		document.SelectedEdges[index] = exactNormalAssignmentEdgeProofDocument{
			ID: edge.ID.String(), ReservationID: edge.ReservationID.String(), Position: edge.Position,
			SnapshotID: edge.Snapshot.SnapshotID.String(), TaskID: edge.Snapshot.TaskID.String(),
			TaskVersion: edge.Snapshot.Version, ContentDigest: hex.EncodeToString(edge.ContentDigest[:]),
		}
	}
	payload, err := json.Marshal(document)
	if err != nil {
		return "", exactNormalAssignmentError("encode proof: %v", err)
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func exactNormalScopeProofDocument(
	scope ExactNormalAssignmentScope,
) exactNormalAssignmentScopeProofDocument {
	return exactNormalAssignmentScopeProofDocument{
		TournamentID: scope.TournamentID.String(), RosterID: scope.RosterID.String(),
		SeriesID: scope.SeriesID.String(), SlotID: scope.SlotID.String(),
		CategoryLockID: scope.CategoryLockID.String(),
	}
}

func exactNormalRevisionsProofDocument(
	revisions ExactNormalAssignmentSourceRevisions,
) exactNormalAssignmentRevisionsProofDocument {
	return exactNormalAssignmentRevisionsProofDocument{
		SeriesRevision: revisions.SeriesRevision,
		PoolRevisionID: revisions.PoolRevisionID.String(), PoolRevision: revisions.PoolRevision,
		HistoryRevisionID: revisions.HistoryRevisionID.String(), HistoryRevision: revisions.HistoryRevision,
		RosterRevision:     revisions.RosterRevision,
		ArtifactRevisionID: revisions.ArtifactRevisionID.String(), ArtifactRevision: revisions.ArtifactRevision,
		CategoryRevisionID: revisions.CategoryRevisionID.String(), CategoryRevision: revisions.CategoryRevision,
	}
}

func exactNormalParticipantIDDocuments(participantIDs []uuid.UUID) []string {
	documents := make([]string, len(participantIDs))
	for index, participantID := range participantIDs {
		documents[index] = participantID.String()
	}
	return documents
}

func exactNormalParticipantReservationDocuments(
	reservations []ExactNormalParticipantReservation,
) []exactNormalParticipantReservationProofDocument {
	documents := make([]exactNormalParticipantReservationProofDocument, len(reservations))
	for index, item := range reservations {
		documents[index] = exactNormalParticipantReservationProofDocument{
			ParticipantID: item.ParticipantID.String(), PlayerID: item.PlayerID.String(),
			ReservationID: item.Reservation.ReservationID.String(),
			TournamentID:  item.Reservation.TournamentID.String(), Revision: item.Reservation.Revision,
			AcquiredAt: item.Reservation.AcquiredAt, UpdatedAt: item.Reservation.UpdatedAt,
		}
	}
	return documents
}

func exactNormalHistoryDocuments(history []capacity.TaskUse) []exactNormalHistoryProofDocument {
	documents := make([]exactNormalHistoryProofDocument, len(history))
	for index, item := range history {
		documents[index] = exactNormalHistoryProofDocument{
			ParticipantID: item.ParticipantID.String(), TaskID: item.TaskID.String(),
		}
	}
	return documents
}

func exactNormalTaskVersionDocuments(
	versions []domain.TaskVersionRef,
) []exactNormalTaskVersionProofDocument {
	documents := make([]exactNormalTaskVersionProofDocument, len(versions))
	for index, item := range versions {
		documents[index] = exactNormalTaskVersionProofDocument{
			TaskID: item.TaskID.String(), Version: item.Version,
		}
	}
	return documents
}
