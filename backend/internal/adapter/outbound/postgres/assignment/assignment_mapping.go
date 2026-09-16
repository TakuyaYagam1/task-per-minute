package assignment

import (
	"crypto/sha256"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func taskSnapshotParams(
	edge AssignmentEdgeInput,
	createdAt time.Time,
) (sqlc.CreateAssignmentTaskSnapshotParams, error) {
	snapshot := edge.Snapshot
	hints, err := marshalJSON("AssignmentPostgres - CreateExactPlan - task hints", snapshot.Hints)
	if err != nil {
		return sqlc.CreateAssignmentTaskSnapshotParams{}, err
	}
	return sqlc.CreateAssignmentTaskSnapshotParams{
		ID:            snapshot.SnapshotID,
		ReservationID: edge.ReservationID,
		TaskID:        snapshot.TaskID,
		TaskVersion:   int32(snapshot.Version), //nolint:gosec // exact-plan validation bounds versions to PostgreSQL int4.
		Kind:          string(snapshot.Kind),
		Title:         snapshot.Title,
		Description:   snapshot.Description,
		Category:      string(snapshot.Category),
		Difficulty:    string(snapshot.Difficulty),
		TimeLimit:     int32(snapshot.TimeLimit), //nolint:gosec // exact-plan validation bounds time limits to PostgreSQL int4.
		Flag:          snapshot.Flag,
		Hints:         hints,
		TaskUrl:       snapshot.TaskURL,
		SourceFileUrl: snapshot.SourceFileURL,
		ContentDigest: append([]byte(nil), edge.ContentDigest[:]...),
		CreatedAt:     tstz(createdAt),
	}, nil
}

func assignmentPlanAggregate(
	plan sqlc.GetAssignmentPlanRow,
	branches []sqlc.ListAssignmentBranchesRow,
	edges []sqlc.AssignmentPlanEdge,
	reservations []sqlc.ListAssignmentTaskVersionReservationsRow,
	snapshots []sqlc.TaskSnapshot,
) (*AssignmentPlanAggregate, error) {
	planRecord, err := assignmentPlanRecord(plan)
	if err != nil {
		return nil, err
	}
	aggregate := &AssignmentPlanAggregate{Plan: planRecord}
	aggregate.Branches = make([]AssignmentBranchRecord, len(branches))
	for index, branch := range branches {
		categories, decodeErr := decodeCategories(branch.CategorySequence)
		if decodeErr != nil {
			return nil, decodeErr
		}
		aggregate.Branches[index] = AssignmentBranchRecord{
			ID: branch.ID, PlanID: branch.PlanID, DraftID: branch.DraftID.UUID,
			DraftRevisionID: branch.DraftRevisionID.UUID, Key: branch.BranchKey, Categories: categories,
			State: branch.State, ActivatedAt: nullableTime(branch.ActivatedAt), ReleasedAt: nullableTime(branch.ReleasedAt),
			ReleaseReason: branch.ReleaseReason, SupersededAt: nullableTime(branch.SupersededAt),
			SupersessionReason: branch.SupersessionReason, CreatedAt: branch.CreatedAt.Time,
		}
	}
	aggregate.Edges = make([]AssignmentEdgeRecord, len(edges))
	for index, edge := range edges {
		selectionEvidence := make(map[string]any)
		if err := json.Unmarshal(edge.SelectionEvidence, &selectionEvidence); err != nil {
			return nil, err
		}
		aggregate.Edges[index] = AssignmentEdgeRecord{
			ID: edge.ID, PlanID: edge.PlanID, BranchID: edge.BranchID, Position: int(edge.Position),
			TaskID: edge.TaskID, TaskVersion: int(edge.TaskVersion), SelectionEvidence: selectionEvidence,
			CreatedAt: edge.CreatedAt.Time,
		}
	}
	aggregate.Reservations = make([]TaskReservationRecord, len(reservations))
	for index, reservation := range reservations {
		aggregate.Reservations[index] = taskReservationRecord(reservation)
	}
	aggregate.Snapshots = make([]TaskSnapshotRecord, len(snapshots))
	for index, snapshot := range snapshots {
		record, mapErr := taskSnapshotRecord(snapshot)
		if mapErr != nil {
			return nil, mapErr
		}
		aggregate.Snapshots[index] = record
	}
	return aggregate, nil
}

func assignmentPlanRecord(row sqlc.GetAssignmentPlanRow) (AssignmentPlanRecord, error) {
	constraintGraph := make(map[string]any)
	proofEvidence := make(map[string]any)
	if err := json.Unmarshal(row.ConstraintGraph, &constraintGraph); err != nil {
		return AssignmentPlanRecord{}, err
	}
	if err := json.Unmarshal(row.ProofEvidence, &proofEvidence); err != nil {
		return AssignmentPlanRecord{}, err
	}
	record := AssignmentPlanRecord{
		ID: row.ID, TournamentID: row.TournamentID, RosterID: row.RosterID, Kind: row.Kind,
		RevisionID: row.RevisionID, SourceRosterRevision: row.SourceRosterRevision,
		SourcePoolRevisionID: row.SourcePoolRevisionID, ReachableBranchCount: int(row.ReachableBranchCount),
		ConstraintGraph: constraintGraph, ProofEvidence: proofEvidence, State: row.State,
		CommittedAt: nullableTime(row.CommittedAt), SupersededAt: nullableTime(row.SupersededAt),
		SupersessionReason: row.SupersessionReason, CreatedAt: row.CreatedAt.Time,
	}
	if row.ParentPlanID.Valid {
		value := row.ParentPlanID.UUID
		record.ParentPlanID = &value
	}
	if row.SourceDraftRevisionID.Valid {
		value := row.SourceDraftRevisionID.UUID
		record.SourceDraftRevisionID = &value
	}
	if row.ActiveBranchID.Valid {
		value := row.ActiveBranchID.UUID
		record.ActiveBranchID = &value
	}
	if row.DecisionEvidenceID.Valid {
		if row.DecisionAlgorithmVersion == nil || !row.DecisionOwnerID.Valid || !row.DecidedAt.Valid {
			return AssignmentPlanRecord{}, domain.ErrValidation
		}
		evidence, err := decisionEvidenceFromStorage(
			row.DecisionEvidenceID.UUID,
			domain.DecisionPurposeTask,
			*row.DecisionAlgorithmVersion,
			row.DecisionInputs,
			row.DecisionSeed,
			row.DecisionResult,
			row.DecisionReplayDigest,
			row.DecisionOwnerID.UUID,
			row.DecidedAt.Time,
		)
		if err != nil {
			return AssignmentPlanRecord{}, err
		}
		record.DecisionEvidence = &evidence
	}
	return record, nil
}

func taskReservationRecord(row sqlc.ListAssignmentTaskVersionReservationsRow) TaskReservationRecord {
	return TaskReservationRecord{
		ID: row.ID, EdgeID: row.EdgeID, PlanID: row.PlanID, BranchID: row.BranchID,
		TaskID: row.TaskID, TaskVersion: int(row.TaskVersion), State: row.State,
		DisclosedAt: nullableTime(row.DisclosedAt), CommittedAt: nullableTime(row.CommittedAt),
		ReleasedAt: nullableTime(row.ReleasedAt), ReleaseReason: row.ReleaseReason,
		SupersededAt: nullableTime(row.SupersededAt), SupersessionReason: row.SupersessionReason,
		CreatedAt: row.CreatedAt.Time,
	}
}

func taskSnapshotRecord(row sqlc.TaskSnapshot) (TaskSnapshotRecord, error) {
	var hints []string
	if err := json.Unmarshal(row.Hints, &hints); err != nil {
		return TaskSnapshotRecord{}, err
	}
	snapshot := domain.AssignmentTaskSnapshot{
		SnapshotID: row.ID, TaskID: row.TaskID, Version: int(row.TaskVersion), Kind: domain.AssignmentTaskKind(row.Kind),
		Title: row.Title, Description: row.Description, Category: domain.Category(row.Category),
		Difficulty: domain.Difficulty(row.Difficulty), TimeLimit: int(row.TimeLimit), Flag: row.Flag,
		Hints: hints, TaskURL: row.TaskUrl, SourceFileURL: row.SourceFileUrl,
	}
	if err := snapshot.Validate(); err != nil {
		return TaskSnapshotRecord{}, err
	}
	if len(row.ContentDigest) != sha256.Size {
		return TaskSnapshotRecord{}, domain.ErrValidation
	}
	record := TaskSnapshotRecord{ReservationID: row.ReservationID, Snapshot: snapshot, CreatedAt: row.CreatedAt.Time}
	copy(record.ContentDigest[:], row.ContentDigest)
	return record, nil
}

func assignmentRecord(
	assignment sqlc.Assignment,
	snapshot sqlc.TaskSnapshot,
	receipts []sqlc.TaskDeliveryReceipt,
) (*AssignmentRecord, error) {
	snapshotRecord, err := taskSnapshotRecord(snapshot)
	if err != nil {
		return nil, err
	}
	record := &AssignmentRecord{
		ID: assignment.ID, AttemptID: assignment.AttemptID, SeriesID: assignment.SeriesID,
		RosterID: assignment.RosterID, PlanID: assignment.PlanID, BranchID: assignment.BranchID,
		ReservationID: assignment.ReservationID, Snapshot: snapshotRecord, State: assignment.State,
		Revision: assignment.Revision, CreatedAt: assignment.CreatedAt.Time, UpdatedAt: assignment.UpdatedAt.Time,
		CompletedAt: nullableTime(assignment.CompletedAt), SupersededAt: nullableTime(assignment.SupersededAt),
		SupersessionReason: assignment.SupersessionReason,
	}
	if assignment.SupersedesAssignmentID.Valid {
		value := assignment.SupersedesAssignmentID.UUID
		record.SupersedesAssignmentID = &value
	}
	record.Receipts = make([]domain.TaskDeliveryReceipt, len(receipts))
	for index, receipt := range receipts {
		record.Receipts[index] = deliveryReceipt(receipt)
	}
	return record, nil
}

func deliveryReceipt(row sqlc.TaskDeliveryReceipt) domain.TaskDeliveryReceipt {
	return domain.TaskDeliveryReceipt{
		ID: row.ID, AssignmentID: row.AssignmentID, AttemptID: row.AttemptID,
		ParticipantID: row.ParticipantID, InstanceID: row.InstanceID,
		SnapshotID: row.SnapshotID, TaskID: row.TaskID,
		DeliveredAt: row.DeliveredAt.Time,
	}
}

func findReservation(
	reservations []sqlc.ListAssignmentTaskVersionReservationsRow,
	id uuid.UUID,
) (sqlc.ListAssignmentTaskVersionReservationsRow, bool) {
	for _, reservation := range reservations {
		if reservation.ID == id {
			return reservation, true
		}
	}
	return sqlc.ListAssignmentTaskVersionReservationsRow{}, false
}

func reservationsAreSequential(
	edges []sqlc.AssignmentPlanEdge,
	current sqlc.ListAssignmentTaskVersionReservationsRow,
	next sqlc.ListAssignmentTaskVersionReservationsRow,
) bool {
	currentPosition, currentFound := reservationPosition(edges, current)
	nextPosition, nextFound := reservationPosition(edges, next)
	return currentFound && nextFound && nextPosition == currentPosition+1
}

func reservationPosition(
	edges []sqlc.AssignmentPlanEdge,
	reservation sqlc.ListAssignmentTaskVersionReservationsRow,
) (int16, bool) {
	for _, edge := range edges {
		if edge.ID == reservation.EdgeID && edge.BranchID == reservation.BranchID {
			return edge.Position, true
		}
	}
	return 0, false
}

func TaskSnapshotParams(
	edge AssignmentEdgeInput,
	createdAt time.Time,
) (sqlc.CreateAssignmentTaskSnapshotParams, error) {
	return taskSnapshotParams(edge, createdAt)
}

func MapTaskSnapshotRecord(row sqlc.TaskSnapshot) (TaskSnapshotRecord, error) {
	return taskSnapshotRecord(row)
}
