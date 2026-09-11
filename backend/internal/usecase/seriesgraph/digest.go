package seriesgraph

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
)

type graphDigestDocument struct {
	Version                 string                     `json:"version"`
	CommandID               string                     `json:"command_id"`
	Series                  json.RawMessage            `json:"series"`
	CategoryRevision        json.RawMessage            `json:"category_revision"`
	SelectedCategories      json.RawMessage            `json:"selected_categories"`
	Assignments             []assignmentDigestDocument `json:"assignments"`
	ParticipantReservations json.RawMessage            `json:"participant_reservations"`
	TaskReservations        json.RawMessage            `json:"task_reservations"`
	Snapshots               json.RawMessage            `json:"snapshots"`
	DeliveryReceipts        json.RawMessage            `json:"delivery_receipts"`
}

type assignmentDigestDocument struct {
	ID               uuid.UUID               `json:"id"`
	AttemptID        uuid.UUID               `json:"attempt_id"`
	SlotID           uuid.UUID               `json:"slot_id"`
	Position         int                     `json:"position"`
	Plan             json.RawMessage         `json:"plan"`
	DeliveryReceipts json.RawMessage         `json:"delivery_receipts"`
	Assignment       assignmentValueDocument `json:"assignment"`
}

type assignmentValueDocument struct {
	ID             uuid.UUID       `json:"id"`
	AttemptID      uuid.UUID       `json:"attempt_id"`
	ActiveSnapshot json.RawMessage `json:"active_snapshot"`
	Receipts       json.RawMessage `json:"receipts"`
}

func graphDigest(graph SeriesGraph) ([sha256.Size]byte, error) {
	document, err := buildGraphDigestDocument(graph)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	payload, err := json.Marshal(document)
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("%w: encode graph digest: %w", ErrInvalidSeriesGraph, err)
	}
	return sha256.Sum256(payload), nil
}

func buildGraphDigestDocument(graph SeriesGraph) (graphDigestDocument, error) {
	series, err := marshalDigestValue(cloneDomainSeries(graph.Series))
	if err != nil {
		return graphDigestDocument{}, fmt.Errorf("encode series: %w", err)
	}
	categoryRevision, err := marshalDigestValue(cloneCategoryRevision(graph.CategoryRevision))
	if err != nil {
		return graphDigestDocument{}, fmt.Errorf("encode category authority: %w", err)
	}
	selectedCategories, err := marshalDigestValue(append([]domain.Category(nil), graph.SelectedCategories...))
	if err != nil {
		return graphDigestDocument{}, fmt.Errorf("encode selected categories: %w", err)
	}
	participantReservations, err := marshalDigestValue(cloneExactParticipantReservations(graph.ParticipantReservations))
	if err != nil {
		return graphDigestDocument{}, fmt.Errorf("encode participant reservations: %w", err)
	}
	taskReservations, err := marshalDigestValue(cloneTaskReservations(graph.TaskReservations))
	if err != nil {
		return graphDigestDocument{}, fmt.Errorf("encode task reservations: %w", err)
	}
	snapshots, err := marshalDigestValue(cloneSnapshotRecords(graph.Snapshots))
	if err != nil {
		return graphDigestDocument{}, fmt.Errorf("encode snapshots: %w", err)
	}
	deliveryReceipts, err := marshalDigestValue(append([]domain.TaskDeliveryReceipt(nil), graph.DeliveryReceipts...))
	if err != nil {
		return graphDigestDocument{}, fmt.Errorf("encode delivery receipts: %w", err)
	}
	assignments := make([]assignmentDigestDocument, len(graph.Assignments))
	for index, aggregate := range graph.Assignments {
		assignments[index], err = buildAssignmentDigestDocument(aggregate)
		if err != nil {
			return graphDigestDocument{}, err
		}
	}
	return graphDigestDocument{
		Version: seriesGraphVersion, CommandID: graph.CommandID.String(), Series: series,
		CategoryRevision: categoryRevision, SelectedCategories: selectedCategories,
		Assignments: assignments, ParticipantReservations: participantReservations,
		TaskReservations: taskReservations, Snapshots: snapshots, DeliveryReceipts: deliveryReceipts,
	}, nil
}

func buildAssignmentDigestDocument(aggregate AssignmentAggregate) (assignmentDigestDocument, error) {
	plan, err := marshalDigestValue(assignmentusecase.CloneExactNormalAssignmentPlan(aggregate.Plan))
	if err != nil {
		return assignmentDigestDocument{}, fmt.Errorf("encode assignment plan: %w", err)
	}
	deliveryReceipts, err := marshalDigestValue(append([]domain.TaskDeliveryReceipt(nil), aggregate.DeliveryReceipts...))
	if err != nil {
		return assignmentDigestDocument{}, fmt.Errorf("encode assignment delivery receipts: %w", err)
	}
	assignment, err := buildAssignmentValueDocument(aggregate)
	if err != nil {
		return assignmentDigestDocument{}, err
	}
	return assignmentDigestDocument{
		ID: aggregate.ID, AttemptID: aggregate.AttemptID, SlotID: aggregate.SlotID, Position: aggregate.Position,
		Plan: plan, DeliveryReceipts: deliveryReceipts, Assignment: assignment,
	}, nil
}

func buildAssignmentValueDocument(aggregate AssignmentAggregate) (assignmentValueDocument, error) {
	activeSnapshot, err := marshalDigestValue(cloneTaskSnapshot(aggregate.Assignment.ActiveSnapshot()))
	if err != nil {
		return assignmentValueDocument{}, fmt.Errorf("encode active snapshot: %w", err)
	}
	receipts, err := marshalDigestValue(append([]domain.TaskDeliveryReceipt(nil), aggregate.Assignment.Receipts()...))
	if err != nil {
		return assignmentValueDocument{}, fmt.Errorf("encode assignment receipts: %w", err)
	}
	return assignmentValueDocument{
		ID: aggregate.Assignment.ID(), AttemptID: aggregate.Assignment.AttemptID(),
		ActiveSnapshot: activeSnapshot, Receipts: receipts,
	}, nil
}

func marshalDigestValue(value any) (json.RawMessage, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(payload), nil
}

func graphContentDigest(graph SeriesGraph) ([sha256.Size]byte, error) {
	content := make([]SnapshotRecord, len(graph.Snapshots))
	for index, snapshot := range graph.Snapshots {
		content[index] = cloneSnapshotRecord(snapshot)
	}
	snapshots, err := marshalDigestValue(content)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	payload, err := json.Marshal(struct {
		Version   string          `json:"version"`
		Snapshots json.RawMessage `json:"snapshots"`
	}{Version: seriesGraphVersion, Snapshots: snapshots})
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(payload), nil
}

func commandDigest(graph [sha256.Size]byte) [sha256.Size]byte {
	payload := append([]byte("series-graph-command-v1:"), graph[:]...)
	return sha256.Sum256(payload)
}

func proofDigest(graph, content [sha256.Size]byte) [sha256.Size]byte {
	payload := make([]byte, 0, len("series-graph-proof-v1:")+sha256.Size*2)
	payload = append(payload, []byte("series-graph-proof-v1:")...)
	payload = append(payload, graph[:]...)
	payload = append(payload, content[:]...)
	return sha256.Sum256(payload)
}

func finalizeDigests(graph *SeriesGraph) error {
	if graph == nil {
		return invalidSeriesGraph("nil graph")
	}
	content, err := graphContentDigest(*graph)
	if err != nil {
		return invalidSeriesGraph("content digest: %v", err)
	}
	root, err := graphDigest(*graph)
	if err != nil {
		return invalidSeriesGraph("graph digest: %v", err)
	}
	command := commandDigest(root)
	proof := proofDigest(root, content)
	proofHash := hex.EncodeToString(proof[:])
	proofs := make([]string, len(graph.Assignments))
	for index, aggregate := range graph.Assignments {
		proofs[index] = aggregate.Plan.ProofHash
	}
	graph.Proof = SeriesGraphProof{
		Version: seriesGraphVersion, CommandID: graph.CommandID,
		CommandDigest: command, GraphDigest: root, ContentDigest: content,
		ProofDigest: proof, ProofHash: proofHash, AssignmentProofs: proofs,
	}
	return nil
}
