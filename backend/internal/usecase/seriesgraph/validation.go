package seriesgraph

import (
	"encoding/hex"
	"reflect"
	"slices"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
)

// Validate checks the complete materialized graph, including every source
// proof, descendant binding, immutable snapshot digest, reservation edge, and
// delivery receipt. It intentionally returns one stable public error identity.
func (graph SeriesGraph) Validate() error {
	if err := validateGraphSeries(graph); err != nil {
		return err
	}
	selected, err := validateGraphCategory(graph)
	if err != nil {
		return err
	}
	currentSlot, err := validateGraphCurrentSlot(graph, selected)
	if err != nil {
		return err
	}
	evidence, err := validateGraphAssignments(graph, selected, currentSlot)
	if err != nil {
		return err
	}
	if err := validateGraphAggregates(graph, evidence); err != nil {
		return err
	}
	if err := validateIdentityRegistry(graph); err != nil {
		return err
	}
	if err := validateGraphProof(graph, evidence.proofs); err != nil {
		return err
	}
	return validateGraphDigests(graph)
}

func validateGraphSeries(graph SeriesGraph) error {
	if graph.CommandID == uuid.Nil {
		return invalidSeriesGraph("missing command identity")
	}
	if err := graph.Series.Validate(); err != nil {
		return invalidSeriesGraph("series: %v", err)
	}
	if graph.Series.State != domain.SeriesStatePlanned {
		return invalidSeriesGraph("series is not planned")
	}
	if graph.Series.Score != (domain.SeriesScore{}) || graph.Series.WinnerID != nil ||
		graph.Series.CurrentScoreRevisionID != nil || graph.Series.CurrentResultRevisionID != nil {
		return invalidSeriesGraph("series must be an empty planned series")
	}
	if len(graph.Series.Slots) != 1 {
		return invalidSeriesGraph("series must contain exactly one current slot")
	}
	return nil
}

func validateGraphCategory(graph SeriesGraph) ([]domain.Category, error) {
	if err := graph.CategoryRevision.Validate(); err != nil {
		return nil, invalidSeriesGraph("category authority: %v", err)
	}
	category := graph.CategoryRevision
	if category.TournamentID != graph.Series.TournamentID || category.SeriesID != graph.Series.ID ||
		category.Format != graph.Series.Format || category.CategoryPool.Format != graph.Series.Format {
		return nil, invalidSeriesGraph("category authority does not belong to series")
	}
	selected, err := normalizeSelectedCategories(graph.Series.Format, category.CategoryPool.Categories, graph.SelectedCategories)
	if err != nil {
		return nil, err
	}
	if !slices.Equal(selected, graph.SelectedCategories) {
		return nil, invalidSeriesGraph("selected category order is not retained")
	}
	return selected, nil
}

func validateGraphCurrentSlot(
	graph SeriesGraph,
	selected []domain.Category,
) (domain.GameSlot, error) {
	currentSlot := graph.Series.Slots[0]
	if currentSlot.Position != 1 || currentSlot.SeriesID != graph.Series.ID || currentSlot.Category != selected[0] ||
		currentSlot.ScoreBefore != (domain.SeriesScore{}) || len(currentSlot.Attempts) != 1 {
		return domain.GameSlot{}, invalidSeriesGraph("current slot is not the first planned slot")
	}
	currentAttempt := currentSlot.Attempts[0]
	if currentAttempt.State != domain.GameStatePlanned || currentAttempt.AttemptNo != 1 ||
		currentAttempt.SlotID != currentSlot.ID ||
		currentAttempt.ID != derivedID(currentSlot.ID, "series-graph-attempt-v1") {
		return domain.GameSlot{}, invalidSeriesGraph("current slot does not contain its initial planned attempt")
	}
	return currentSlot, nil
}

type graphValidationEvidence struct {
	participantReservations []assignmentusecase.ExactNormalParticipantReservation
	taskReservations        []TaskReservation
	snapshots               []SnapshotRecord
	receipts                []domain.TaskDeliveryReceipt
	proofs                  []string
}

func validateGraphAssignments(
	graph SeriesGraph,
	selected []domain.Category,
	currentSlot domain.GameSlot,
) (graphValidationEvidence, error) {
	assignments := graph.Assignments
	if len(assignments) != len(selected) {
		return graphValidationEvidence{}, invalidSeriesGraph("assignment count does not match selected categories")
	}
	evidence := graphValidationEvidence{
		participantReservations: make([]assignmentusecase.ExactNormalParticipantReservation, 0, 2),
		taskReservations:        make([]TaskReservation, 0, len(assignments)*3),
		snapshots:               make([]SnapshotRecord, 0, len(assignments)*3),
		receipts:                make([]domain.TaskDeliveryReceipt, 0, len(assignments)*2),
		proofs:                  make([]string, len(assignments)),
	}
	seenTasks := make(map[uuid.UUID]struct{}, len(assignments)*3)
	seenTaskVersions := make(map[domain.TaskVersionRef]struct{}, len(assignments)*3)
	seenSlots := make(map[uuid.UUID]struct{}, len(assignments))
	seenEvidence := make(map[uuid.UUID]struct{}, len(assignments)*10)
	seenReceiptIDs := make(map[uuid.UUID]struct{}, len(assignments)*2)
	canonicalReservations := make([]assignmentusecase.ExactNormalParticipantReservation, 0, 2)
	for index, aggregate := range assignments {
		if err := validateAssignmentAggregate(graph, aggregate, index+1, selected[index], index == 0, currentSlot); err != nil {
			return graphValidationEvidence{}, err
		}
		if err := validateAggregateIdentities(aggregate, seenSlots, seenEvidence); err != nil {
			return graphValidationEvidence{}, err
		}
		if err := validateAggregateReservations(index, aggregate, &canonicalReservations, &evidence); err != nil {
			return graphValidationEvidence{}, err
		}
		if err := appendAggregateTaskEvidence(aggregate, seenTasks, seenTaskVersions, &evidence); err != nil {
			return graphValidationEvidence{}, err
		}
		if err := appendAggregateReceiptEvidence(aggregate, seenReceiptIDs, &evidence); err != nil {
			return graphValidationEvidence{}, err
		}
		evidence.proofs[index] = aggregate.Plan.ProofHash
	}
	return evidence, nil
}

func validateAggregateIdentities(
	aggregate AssignmentAggregate,
	seenSlots map[uuid.UUID]struct{},
	seenEvidence map[uuid.UUID]struct{},
) error {
	if _, duplicate := seenSlots[aggregate.SlotID]; duplicate {
		return invalidSeriesGraph("duplicate assignment slot")
	}
	seenSlots[aggregate.SlotID] = struct{}{}
	for _, id := range assignmentPlanEvidenceIDs(aggregate.Plan) {
		if _, duplicate := seenEvidence[id]; duplicate {
			return invalidSeriesGraph("duplicate assignment evidence identity")
		}
		seenEvidence[id] = struct{}{}
	}
	return nil
}

func validateAggregateReservations(
	index int,
	aggregate AssignmentAggregate,
	canonical *[]assignmentusecase.ExactNormalParticipantReservation,
	evidence *graphValidationEvidence,
) error {
	if index == 0 {
		*canonical = append((*canonical)[:0], aggregate.Plan.ParticipantReservations...)
		evidence.participantReservations = append(
			evidence.participantReservations[:0], aggregate.Plan.ParticipantReservations...,
		)
		return nil
	}
	if !reflect.DeepEqual(*canonical, aggregate.Plan.ParticipantReservations) {
		return invalidSeriesGraph("assignment participant reservations are inconsistent")
	}
	return nil
}

func appendAggregateTaskEvidence(
	aggregate AssignmentAggregate,
	seenTasks map[uuid.UUID]struct{},
	seenTaskVersions map[domain.TaskVersionRef]struct{},
	evidence *graphValidationEvidence,
) error {
	for _, edge := range aggregate.Plan.SelectedEdges {
		reservation := taskReservationFromEdge(aggregate, edge)
		if _, duplicate := seenTasks[reservation.TaskID]; duplicate {
			return invalidSeriesGraph("duplicate task reservation")
		}
		seenTasks[reservation.TaskID] = struct{}{}
		ref := domain.TaskVersionRef{TaskID: reservation.TaskID, Version: reservation.Version}
		if _, duplicate := seenTaskVersions[ref]; duplicate {
			return invalidSeriesGraph("duplicate task version reservation")
		}
		seenTaskVersions[ref] = struct{}{}
		evidence.taskReservations = append(evidence.taskReservations, reservation)
		evidence.snapshots = append(evidence.snapshots, snapshotRecordFromEdge(aggregate, edge))
	}
	return nil
}

func appendAggregateReceiptEvidence(
	aggregate AssignmentAggregate,
	seenReceiptIDs map[uuid.UUID]struct{},
	evidence *graphValidationEvidence,
) error {
	for _, receipt := range aggregate.DeliveryReceipts {
		if receipt.ID == uuid.Nil {
			return invalidSeriesGraph("delivery receipt has an empty identity")
		}
		if _, duplicate := seenReceiptIDs[receipt.ID]; duplicate {
			return invalidSeriesGraph("duplicate delivery receipt identity")
		}
		seenReceiptIDs[receipt.ID] = struct{}{}
		evidence.receipts = append(evidence.receipts, receipt)
	}
	return nil
}

func validateGraphAggregates(graph SeriesGraph, evidence graphValidationEvidence) error {
	if !reflect.DeepEqual(evidence.participantReservations, graph.ParticipantReservations) {
		return invalidSeriesGraph("participant reservation aggregate is incomplete")
	}
	if !reflect.DeepEqual(evidence.taskReservations, graph.TaskReservations) {
		return invalidSeriesGraph("task reservation aggregate is incomplete")
	}
	if !reflect.DeepEqual(evidence.snapshots, graph.Snapshots) {
		return invalidSeriesGraph("snapshot aggregate is incomplete")
	}
	if !reflect.DeepEqual(evidence.receipts, graph.DeliveryReceipts) {
		return invalidSeriesGraph("delivery receipt aggregate is incomplete")
	}
	return nil
}

func validateGraphDigests(graph SeriesGraph) error {
	digest, err := graphDigest(graph)
	if err != nil {
		return invalidSeriesGraph("graph digest: %v", err)
	}
	contentDigest, err := graphContentDigest(graph)
	if err != nil {
		return invalidSeriesGraph("content digest: %v", err)
	}
	commandEvidence := commandDigest(digest)
	proofEvidence := proofDigest(digest, contentDigest)
	if graph.Proof.GraphDigest != digest || graph.Proof.ContentDigest != contentDigest ||
		graph.Proof.CommandDigest != commandEvidence || graph.Proof.ProofDigest != proofEvidence {
		return invalidSeriesGraph("graph digest evidence does not match content")
	}
	if graph.Proof.ProofHash == "" || graph.Proof.ProofHash != hex.EncodeToString(graph.Proof.ProofDigest[:]) {
		return invalidSeriesGraph("invalid graph proof hash")
	}
	return nil
}

// identityRegistry claims identities owned by the materialized graph. Scope
// references and external participant, player, task, pool, and resource IDs
// are deliberately excluded because those values may legitimately repeat.
type identityRegistry struct {
	owners map[uuid.UUID]string
}

func validateIdentityRegistry(graph SeriesGraph) error {
	registry := identityRegistry{owners: make(map[uuid.UUID]string)}
	claims := []struct {
		id   uuid.UUID
		kind string
	}{
		{graph.CommandID, "command"},
		{graph.CategoryRevision.ID, "category revision"},
		{graph.Series.ID, "series"},
	}
	for _, claim := range claims {
		if err := registry.claim(claim.id, claim.kind); err != nil {
			return err
		}
	}
	for _, aggregate := range graph.Assignments {
		plan := aggregate.Plan
		claims := []struct {
			id   uuid.UUID
			kind string
		}{
			{plan.PlanID, "assignment plan"},
			{plan.PlanRevisionID, "assignment plan revision"},
			{plan.BranchID, "assignment branch"},
			{plan.DecisionEvidence.ID, "decision"},
			{plan.Scope.SlotID, "slot"},
			{aggregate.AttemptID, "attempt"},
			{aggregate.ID, "assignment"},
		}
		for _, claim := range claims {
			if err := registry.claim(claim.id, claim.kind); err != nil {
				return err
			}
		}
		for _, edge := range plan.SelectedEdges {
			claims := []struct {
				id   uuid.UUID
				kind string
			}{
				{edge.ID, "assignment edge"},
				{edge.ReservationID, "task reservation"},
				{edge.Snapshot.SnapshotID, "snapshot"},
			}
			for _, claim := range claims {
				if err := registry.claim(claim.id, claim.kind); err != nil {
					return err
				}
			}
		}
		for _, receipt := range aggregate.DeliveryReceipts {
			if err := registry.claim(receipt.ID, "delivery receipt"); err != nil {
				return err
			}
		}
	}
	return nil
}

func (registry *identityRegistry) claim(id uuid.UUID, kind string) error {
	if id == uuid.Nil {
		return invalidSeriesGraph("%s has an empty identity", kind)
	}
	if previous, exists := registry.owners[id]; exists {
		return invalidSeriesGraph("duplicate graph identity %s shared by %s and %s", id, previous, kind)
	}
	registry.owners[id] = kind
	return nil
}

func validateAssignmentAggregate(
	graph SeriesGraph,
	aggregate AssignmentAggregate,
	position int,
	category domain.Category,
	executable bool,
	currentSlot domain.GameSlot,
) error {
	if err := validateAssignmentBinding(graph, aggregate, position, category, executable, currentSlot); err != nil {
		return err
	}
	if err := validateAssignmentState(graph, aggregate); err != nil {
		return err
	}
	if err := validatePlanEdges(aggregate); err != nil {
		return err
	}
	if err := validateDeliveryReceipts(graph, aggregate, executable); err != nil {
		return err
	}
	return nil
}

func validateAssignmentBinding(
	graph SeriesGraph,
	aggregate AssignmentAggregate,
	position int,
	category domain.Category,
	executable bool,
	currentSlot domain.GameSlot,
) error {
	if aggregate.ID != derivedID(aggregate.Plan.PlanID, "series-graph-assignment-v1") ||
		aggregate.AttemptID != derivedID(aggregate.SlotID, "series-graph-attempt-v1") ||
		aggregate.SlotID == uuid.Nil || aggregate.Position != position {
		return invalidSeriesGraph("assignment aggregate has an invalid scope or identity")
	}
	if aggregate.Plan.Category != category {
		return invalidSeriesGraph("assignment aggregate has an invalid scope or identity")
	}
	if err := validateAssignmentScope(graph, aggregate); err != nil {
		return err
	}
	if executable && aggregate.SlotID != currentSlot.ID {
		return invalidSeriesGraph("current assignment does not bind the first Series slot")
	}
	return nil
}

func validateAssignmentScope(graph SeriesGraph, aggregate AssignmentAggregate) error {
	if aggregate.Plan.Scope.TournamentID != graph.Series.TournamentID ||
		aggregate.Plan.Scope.RosterID != graph.CategoryRevision.RosterID ||
		aggregate.Plan.Scope.SeriesID != graph.Series.ID || aggregate.Plan.Scope.SlotID != aggregate.SlotID ||
		aggregate.Plan.Scope.CategoryLockID != graph.CategoryRevision.ID {
		return invalidSeriesGraph("assignment aggregate has an invalid scope or identity")
	}
	return nil
}

func validateAssignmentState(graph SeriesGraph, aggregate AssignmentAggregate) error {
	if err := aggregate.Plan.Validate(); err != nil {
		return invalidSeriesGraph("assignment plan: %v", err)
	}
	participants := [2]uuid.UUID{graph.Series.FirstParticipantID, graph.Series.SecondParticipantID}
	if !sameParticipantSet(aggregate.Plan.ParticipantIDs, participants) {
		return invalidSeriesGraph("assignment plan participants do not match series")
	}
	if err := aggregate.Assignment.Validate(); err != nil {
		return invalidSeriesGraph("assignment aggregate: %v", err)
	}
	if aggregate.Assignment.ID() != aggregate.ID || aggregate.Assignment.AttemptID() != aggregate.AttemptID ||
		aggregate.Assignment.UndisclosedReserveCount() != domain.AssignmentReserveCount {
		return invalidSeriesGraph("assignment aggregate identity does not match attempt")
	}
	if !reflect.DeepEqual(aggregate.Assignment.ActiveSnapshot(), aggregate.Plan.SelectedEdges[0].Snapshot) {
		return invalidSeriesGraph("assignment primary snapshot does not match plan")
	}
	return nil
}

func validatePlanEdges(aggregate AssignmentAggregate) error {
	seenTasks := make(map[uuid.UUID]struct{}, len(aggregate.Plan.SelectedEdges))
	for _, edge := range aggregate.Plan.SelectedEdges {
		if err := validatePlanEdgeSnapshot(aggregate, edge); err != nil {
			return err
		}
		reservation := taskReservationFromEdge(aggregate, edge)
		if err := validatePlanEdgeReservation(reservation, aggregate, edge); err != nil {
			return err
		}
		if _, duplicate := seenTasks[reservation.TaskID]; duplicate {
			return invalidSeriesGraph("assignment repeats a task")
		}
		seenTasks[reservation.TaskID] = struct{}{}
	}
	return nil
}

func validatePlanEdgeSnapshot(
	aggregate AssignmentAggregate,
	edge assignmentusecase.ExactNormalAssignmentEdge,
) error {
	snapshot := snapshotRecordFromEdge(aggregate, edge)
	if !snapshotRecordMatchesEdge(snapshot, aggregate, edge) {
		return invalidSeriesGraph("immutable snapshot does not match selected edge")
	}
	if err := snapshot.Snapshot.Validate(); err != nil {
		return invalidSeriesGraph("immutable snapshot: %v", err)
	}
	return nil
}

func snapshotRecordMatchesEdge(
	record SnapshotRecord,
	aggregate AssignmentAggregate,
	edge assignmentusecase.ExactNormalAssignmentEdge,
) bool {
	return record.Snapshot.SnapshotID == edge.Snapshot.SnapshotID &&
		record.AssignmentID == aggregate.ID && record.AttemptID == aggregate.AttemptID &&
		record.SlotID == aggregate.SlotID && record.Position == edge.Position &&
		reflect.DeepEqual(record.Snapshot, edge.Snapshot) && record.ContentDigest == edge.ContentDigest
}

func validatePlanEdgeReservation(
	reservation TaskReservation,
	aggregate AssignmentAggregate,
	edge assignmentusecase.ExactNormalAssignmentEdge,
) error {
	if !taskReservationMatchesEdge(reservation, aggregate, edge) {
		return invalidSeriesGraph("task reservation does not match selected edge")
	}
	return nil
}

func taskReservationMatchesEdge(
	reservation TaskReservation,
	aggregate AssignmentAggregate,
	edge assignmentusecase.ExactNormalAssignmentEdge,
) bool {
	return reservation.ReservationID == edge.ReservationID && reservation.EdgeID == edge.ID &&
		reservation.AssignmentID == aggregate.ID && reservation.AttemptID == aggregate.AttemptID &&
		reservation.SlotID == aggregate.SlotID && reservation.Position == edge.Position &&
		reservation.SnapshotID == edge.Snapshot.SnapshotID && reservation.TaskID == edge.Snapshot.TaskID &&
		reservation.Version == edge.Snapshot.Version && reservation.Kind == edge.Snapshot.Kind &&
		reservation.ContentDigest == edge.ContentDigest
}

func validateDeliveryReceipts(graph SeriesGraph, aggregate AssignmentAggregate, executable bool) error {
	if err := validateDeliveryReceiptCount(aggregate, executable); err != nil {
		return err
	}
	assignmentReceipts := aggregate.Assignment.Receipts()
	if !reflect.DeepEqual(assignmentReceipts, aggregate.DeliveryReceipts) {
		return invalidSeriesGraph("assignment delivery history is not retained")
	}
	if !executable {
		return nil
	}
	return validateExecutableDeliveryReceipts(graph, aggregate)
}

func validateDeliveryReceiptCount(aggregate AssignmentAggregate, executable bool) error {
	want := 0
	if executable {
		want = 2
	}
	if len(aggregate.DeliveryReceipts) == want {
		return nil
	}
	if executable {
		return invalidSeriesGraph("assignment must deliver to both participants")
	}
	return invalidSeriesGraph("future assignment must not be delivered")
}

func validateExecutableDeliveryReceipts(
	graph SeriesGraph,
	aggregate AssignmentAggregate,
) error {
	participants := [2]uuid.UUID{graph.Series.FirstParticipantID, graph.Series.SecondParticipantID}
	reference := aggregate.DeliveryReceipts[0]
	for index, participantID := range participants {
		receipt := aggregate.DeliveryReceipts[index]
		if err := validateDeliveryReceiptBinding(aggregate, receipt, participantID, reference); err != nil {
			return err
		}
		if err := receipt.Validate(); err != nil {
			return invalidSeriesGraph("delivery receipt: %v", err)
		}
	}
	return nil
}

func validateDeliveryReceiptBinding(
	aggregate AssignmentAggregate,
	receipt domain.TaskDeliveryReceipt,
	participantID uuid.UUID,
	reference domain.TaskDeliveryReceipt,
) error {
	primary := aggregate.Plan.SelectedEdges[0].Snapshot
	if receipt.ID != derivedDeliveryID(aggregate.ID, participantID) || receipt.AssignmentID != aggregate.ID ||
		receipt.AttemptID != aggregate.AttemptID || receipt.ParticipantID != participantID ||
		receipt.InstanceID != domain.ParticipantTaskInstanceID(aggregate.ID, participantID) ||
		receipt.SnapshotID != primary.SnapshotID || receipt.TaskID != primary.TaskID ||
		!receipt.DeliveredAt.Equal(reference.DeliveredAt) {
		return invalidSeriesGraph("delivery receipt identity or history conflict")
	}
	return nil
}

func validateGraphProof(graph SeriesGraph, proofs []string) error {
	if graph.Proof.Version != seriesGraphVersion || graph.Proof.CommandID != graph.CommandID ||
		!slices.Equal(graph.Proof.AssignmentProofs, proofs) {
		return invalidSeriesGraph("invalid graph proof evidence")
	}
	return nil
}

func cloneSnapshotRecord(record SnapshotRecord) SnapshotRecord {
	clone := record
	clone.Snapshot = cloneTaskSnapshot(record.Snapshot)
	return clone
}
