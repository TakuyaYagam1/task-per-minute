package golden

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/taskexec"
)

func validateGoldenExactPlanProof(plan ExactPlan) error {
	want, err := goldenExactPlanProofHash(plan)
	if err != nil || len(plan.ProofHash) != sha256.Size*2 || want != plan.ProofHash {
		return goldenExactPlanError("proof hash does not match")
	}
	return nil
}

func TaskArtifactDigest(task domain.Task, version int) [sha256.Size]byte {
	cloned := taskexec.CloneTask(&task)
	document := goldenTaskArtifactDocument{
		Version: version,
		Kind:    domain.AssignmentTaskKindGolden,
		Task: goldenTaskArtifactTask{
			ID: cloned.ID, Title: cloned.Title, Description: cloned.Description,
			Category: cloned.Category, Difficulty: cloned.Difficulty, TimeLimit: cloned.TimeLimit,
			Flag: cloned.Flag, Hints: cloned.Hints, TaskURL: cloned.TaskURL,
			SourceFileURL: cloned.SourceFileURL, CreatedAt: cloned.CreatedAt,
		},
	}
	payload, err := json.Marshal(document)
	if err != nil {
		return [sha256.Size]byte{}
	}
	return sha256.Sum256(payload)
}

type goldenTaskArtifactDocument struct {
	Version int                       `json:"version"`
	Kind    domain.AssignmentTaskKind `json:"kind"`
	Task    goldenTaskArtifactTask    `json:"task"`
}

type goldenTaskArtifactTask struct {
	ID            uuid.UUID         `json:"id"`
	Title         string            `json:"title"`
	Description   string            `json:"description"`
	Category      domain.Category   `json:"category"`
	Difficulty    domain.Difficulty `json:"difficulty"`
	TimeLimit     int               `json:"time_limit"`
	Flag          string            `json:"flag"`
	Hints         []string          `json:"hints"`
	TaskURL       *string           `json:"task_url"`
	SourceFileURL *string           `json:"source_file_url"`
	CreatedAt     time.Time         `json:"created_at"`
}

func goldenAuthorityEvidence(authority Authority) (Expectation, error) {
	groupDocument := make([]any, len(authority.Groups))
	membershipDocument := make([]any, len(authority.Groups))
	for index, group := range authority.Groups {
		groupDocument[index] = []any{group.Revision.ProofHash(), group.ActiveParticipantIDs}
		membershipDocument[index] = []any{group.Revision.GroupID(), group.Revision.RevisionID().UUID(), group.ActiveParticipantIDs}
	}
	healthDocument := make([]domain.TaskVersionHealth, len(authority.Candidates))
	artifactDocument := make([]any, len(authority.Candidates))
	for index, candidate := range authority.Candidates {
		health := candidate.Health
		health.InternalHealthDetail = ""
		healthDocument[index] = health
		artifactDocument[index] = []any{candidate.Task.ID, candidate.Version, candidate.ArtifactDigest}
	}
	reservationDocument := struct {
		Participants []ParticipantReservation
		Tasks        []TaskReservation
	}{authority.ParticipantReservations, authority.ExistingTaskReservations}
	groupDigest, err := goldenHashDocument(groupDocument)
	if err != nil {
		return Expectation{}, err
	}
	poolDigest, err := goldenHashDocument(authority.Pool)
	if err != nil {
		return Expectation{}, err
	}
	historyDigest, err := goldenHashDocument(authority.History)
	if err != nil {
		return Expectation{}, err
	}
	healthDigest, err := goldenHashDocument(healthDocument)
	if err != nil {
		return Expectation{}, err
	}
	artifactDigest, err := goldenHashDocument(artifactDocument)
	if err != nil {
		return Expectation{}, err
	}
	reservationDigest, err := goldenHashDocument(reservationDocument)
	if err != nil {
		return Expectation{}, err
	}
	membershipDigest, err := goldenHashDocument(membershipDocument)
	if err != nil {
		return Expectation{}, err
	}
	return Expectation{
		Revisions: authority.Revisions, SourcePayloadDigest: authority.Source.PayloadDigest,
		GroupDigest: groupDigest, PoolDigest: poolDigest, HistoryDigest: historyDigest,
		TaskHealthDigest: healthDigest, ArtifactDigest: artifactDigest,
		ReservationDigest: reservationDigest, MembershipDigest: membershipDigest,
	}, nil
}

type goldenExactPlanProofDocument struct {
	Scope          Scope                   `json:"scope"`
	PlanID         uuid.UUID               `json:"plan_id"`
	PlanRevisionID uuid.UUID               `json:"plan_revision_id"`
	Expected       Expectation             `json:"expected"`
	Groups         []goldenExactGroupProof `json:"groups"`
	CreatedAt      time.Time               `json:"created_at"`
}

type goldenExactGroupProof struct {
	GroupID                    uuid.UUID                  `json:"group_id"`
	GroupRevisionID            uuid.UUID                  `json:"group_revision_id"`
	SourceProjectionRevisionID uuid.UUID                  `json:"source_projection_revision_id"`
	PositionFrom               int                        `json:"position_from"`
	PositionTo                 int                        `json:"position_to"`
	ParticipantIDs             []uuid.UUID                `json:"participant_ids"`
	Edges                      []goldenExactPlanEdgeProof `json:"edges"`
}

type goldenExactPlanEdgeProof struct {
	ID            uuid.UUID         `json:"id"`
	ReservationID uuid.UUID         `json:"reservation_id"`
	Position      int               `json:"position"`
	SnapshotID    uuid.UUID         `json:"snapshot_id"`
	TaskID        uuid.UUID         `json:"task_id"`
	Version       int               `json:"version"`
	ContentDigest [sha256.Size]byte `json:"content_digest"`
}

func goldenExactPlanProofHash(plan ExactPlan) (string, error) {
	document := goldenExactPlanProofDocument{
		Scope: plan.Scope, PlanID: plan.PlanID, PlanRevisionID: plan.PlanRevisionID,
		Expected: plan.Expected, Groups: make([]goldenExactGroupProof, len(plan.Groups)), CreatedAt: plan.CreatedAt,
	}
	for groupIndex, group := range plan.Groups {
		proof := goldenExactGroupProof{
			GroupID: group.GroupID, GroupRevisionID: group.GroupRevisionID.UUID(),
			SourceProjectionRevisionID: group.SourceProjectionRevisionID.UUID(),
			PositionFrom:               group.PositionFrom, PositionTo: group.PositionTo,
			ParticipantIDs: append([]uuid.UUID(nil), group.ParticipantIDs...),
			Edges:          make([]goldenExactPlanEdgeProof, len(group.Edges)),
		}
		for edgeIndex, edge := range group.Edges {
			proof.Edges[edgeIndex] = goldenExactPlanEdgeProof{
				ID: edge.ID, ReservationID: edge.ReservationID, Position: edge.Position,
				SnapshotID: edge.Snapshot.SnapshotID, TaskID: edge.Snapshot.TaskID,
				Version: edge.Snapshot.Version, ContentDigest: edge.ContentDigest,
			}
		}
		document.Groups[groupIndex] = proof
	}
	payload, err := json.Marshal(document)
	if err != nil {
		return "", goldenExactPlanError("encode proof: %v", err)
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func goldenHashDocument(value any) ([sha256.Size]byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return [sha256.Size]byte{}, goldenExactPlanError("encode authority evidence: %v", err)
	}
	return sha256.Sum256(payload), nil
}
