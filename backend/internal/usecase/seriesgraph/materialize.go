package seriesgraph

import (
	"fmt"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
)

// Materialize builds one complete executable graph. It is pure with respect
// to its inputs: no repository, clock, transport, or event publisher is
// involved. If an existing graph is supplied, it is validated before replay
// handling and is returned detached from the caller.
func Materialize(existing *SeriesGraph, input MaterializeInput) (SeriesGraph, bool, error) {
	normalized, err := normalizeInput(existing, input)
	if err != nil {
		return SeriesGraph{}, false, err
	}
	candidate, err := buildGraph(normalized)
	if err != nil {
		return SeriesGraph{}, false, err
	}

	if normalized.existing == nil {
		return cloneSeriesGraph(candidate), true, nil
	}
	existingGraph := cloneSeriesGraph(*normalized.existing)
	if err := existingGraph.Validate(); err != nil {
		return SeriesGraph{}, false, invalidSeriesGraph("existing graph: %v", err)
	}
	if existingGraph.CommandID != candidate.CommandID {
		return SeriesGraph{}, false, ErrSeriesGraphConflict
	}
	if existingGraph.Proof.GraphDigest != candidate.Proof.GraphDigest ||
		existingGraph.Proof.CommandDigest != candidate.Proof.CommandDigest ||
		existingGraph.Proof.ProofDigest != candidate.Proof.ProofDigest {
		return SeriesGraph{}, false, ErrSeriesGraphCommandReuse
	}
	return cloneSeriesGraph(existingGraph), false, nil
}

type normalizedInput struct {
	commandID          uuid.UUID
	deliveredAt        time.Time
	series             domain.Series
	categoryRevision   draftusecase.CategoryRevision
	selectedCategories []domain.Category
	assignmentPlans    []assignmentusecase.ExactNormalAssignmentPlan
	existing           *SeriesGraph
}

func normalizeInput(existing *SeriesGraph, input MaterializeInput) (normalizedInput, error) {
	deliveredAt, err := validateMaterializeInputHeader(input)
	if err != nil {
		return normalizedInput{}, err
	}
	categoryRevision := cloneCategoryRevision(input.CategoryRevision)
	plans := cloneAssignmentPlans(input.AssignmentPlans)
	if err := validateMaterializeSources(categoryRevision, plans); err != nil {
		return normalizedInput{}, err
	}

	series := cloneDomainSeries(input.Series)
	if err := validateMaterializeSeries(series); err != nil {
		return normalizedInput{}, err
	}

	if err := validateMaterializeCategoryAuthority(series, categoryRevision); err != nil {
		return normalizedInput{}, err
	}

	selected, err := normalizeSelectedCategories(
		series.Format, categoryRevision.CategoryPool.Categories, input.SelectedCategories,
	)
	if err != nil {
		return normalizedInput{}, err
	}
	if err := validatePlans(series, categoryRevision, selected, plans); err != nil {
		return normalizedInput{}, err
	}
	if err := validateMaterializeDeliveryTime(deliveredAt, categoryRevision, plans); err != nil {
		return normalizedInput{}, err
	}

	var resolvedExisting *SeriesGraph
	if existing != nil {
		clone := cloneSeriesGraph(*existing)
		resolvedExisting = &clone
	}
	return normalizedInput{
		commandID: input.CommandID, series: series,
		deliveredAt:        deliveredAt,
		categoryRevision:   cloneCategoryRevision(categoryRevision),
		selectedCategories: append([]domain.Category(nil), selected...),
		assignmentPlans:    plans, existing: resolvedExisting,
	}, nil
}

func validateMaterializeInputHeader(input MaterializeInput) (time.Time, error) {
	if input.CommandID == uuid.Nil {
		return time.Time{}, invalidSeriesGraph("missing command identity")
	}
	if input.DeliveredAt.IsZero() || input.DeliveredAt.Location() != time.UTC {
		return time.Time{}, invalidSeriesGraph("delivery timestamp must be server UTC")
	}
	return input.DeliveredAt.Round(0).UTC(), nil
}

func validateMaterializeSources(
	categoryRevision draftusecase.CategoryRevision,
	plans []assignmentusecase.ExactNormalAssignmentPlan,
) error {
	if categoryRevision.ID == uuid.Nil {
		return invalidSeriesGraph("missing category authority")
	}
	if len(plans) == 0 {
		return invalidSeriesGraph("missing assignment plans")
	}
	return nil
}

func validateMaterializeSeries(series domain.Series) error {
	if err := series.Validate(); err != nil {
		return invalidSeriesGraph("series: %v", err)
	}
	if series.State != domain.SeriesStatePlanned || len(series.Slots) != 0 ||
		series.Score != (domain.SeriesScore{}) || series.WinnerID != nil ||
		series.CurrentScoreRevisionID != nil || series.CurrentResultRevisionID != nil {
		return invalidSeriesGraph("series must be an empty planned series")
	}
	return nil
}

func validateMaterializeCategoryAuthority(
	series domain.Series,
	categoryRevision draftusecase.CategoryRevision,
) error {
	if err := categoryRevision.Validate(); err != nil {
		return invalidSeriesGraph("category authority: %v", err)
	}
	if categoryRevision.TournamentID != series.TournamentID ||
		categoryRevision.SeriesID != series.ID || categoryRevision.Format != series.Format ||
		categoryRevision.CategoryPool.Format != series.Format {
		return invalidSeriesGraph("category authority does not belong to series")
	}
	return nil
}

func validateMaterializeDeliveryTime(
	deliveredAt time.Time,
	categoryRevision draftusecase.CategoryRevision,
	plans []assignmentusecase.ExactNormalAssignmentPlan,
) error {
	if deliveredAt.Before(categoryRevision.CreatedAt) {
		return invalidSeriesGraph("delivery timestamp precedes category authority")
	}
	for _, plan := range plans {
		if deliveredAt.Before(plan.CreatedAt) {
			return invalidSeriesGraph("delivery timestamp precedes assignment plan")
		}
	}
	return nil
}

func normalizeSelectedCategories(
	format domain.SeriesFormat,
	pool []domain.Category,
	selected []domain.Category,
) ([]domain.Category, error) {
	want := format.WinsRequired()*2 - 1
	if !format.IsValid() || want < 1 || len(selected) != want {
		return nil, invalidSeriesGraph("selected categories do not match series format")
	}
	poolSet := make(map[domain.Category]struct{}, len(pool))
	for _, category := range pool {
		poolSet[category] = struct{}{}
	}
	result := append([]domain.Category(nil), selected...)
	seen := make(map[domain.Category]struct{}, len(result))
	for _, category := range result {
		if !category.IsValid() {
			return nil, invalidSeriesGraph("selected category is invalid")
		}
		if _, ok := poolSet[category]; !ok {
			return nil, invalidSeriesGraph("selected category is outside the authority pool")
		}
		if _, duplicate := seen[category]; duplicate {
			return nil, invalidSeriesGraph("selected categories contain a duplicate")
		}
		seen[category] = struct{}{}
	}
	return result, nil
}

func validatePlans(
	series domain.Series,
	categoryRevision draftusecase.CategoryRevision,
	selected []domain.Category,
	plans []assignmentusecase.ExactNormalAssignmentPlan,
) error {
	want := series.Format.WinsRequired()*2 - 1
	if len(plans) != want {
		return invalidSeriesGraph("expected %d assignment plans, got %d", want, len(plans))
	}

	participants := [2]uuid.UUID{series.FirstParticipantID, series.SecondParticipantID}
	seenCategories := make(map[domain.Category]struct{}, len(plans))
	seenSlots := make(map[uuid.UUID]struct{}, len(plans))
	seenEvidence := make(map[uuid.UUID]struct{}, len(plans)*10)
	seenTasks := make(map[uuid.UUID]struct{}, len(plans)*3)
	seenTaskVersions := make(map[domain.TaskVersionRef]struct{}, len(plans)*3)
	var canonicalReservations []assignmentusecase.ExactNormalParticipantReservation
	for index, plan := range plans {
		if err := validatePlanSource(series, categoryRevision, participants, plan); err != nil {
			return err
		}
		if err := validatePlanReservations(index, plan, &canonicalReservations); err != nil {
			return err
		}
		if err := validatePlanUniqueness(plan, seenCategories, seenSlots, seenEvidence); err != nil {
			return err
		}
		if err := validatePlanTaskUsage(plan, participants, seenTasks, seenTaskVersions); err != nil {
			return err
		}
	}
	return validateSelectedPlanCoverage(selected, seenCategories)
}

func validatePlanSource(
	series domain.Series,
	categoryRevision draftusecase.CategoryRevision,
	participants [2]uuid.UUID,
	plan assignmentusecase.ExactNormalAssignmentPlan,
) error {
	if err := plan.Validate(); err != nil {
		return invalidSeriesGraph("assignment plan: %v", err)
	}
	if plan.Scope.TournamentID != series.TournamentID ||
		plan.Scope.RosterID != categoryRevision.RosterID ||
		plan.Scope.SeriesID != series.ID ||
		plan.Scope.CategoryLockID != categoryRevision.ID ||
		plan.Revisions.CategoryRevisionID != categoryRevision.ID {
		return invalidSeriesGraph("assignment plan scope does not bind the series authority")
	}
	if plan.ParticipantIDs == nil || len(plan.ParticipantIDs) != 2 ||
		!sameParticipantSet(plan.ParticipantIDs, participants) {
		return invalidSeriesGraph("assignment plan participants do not match series")
	}
	return nil
}

func validatePlanReservations(
	index int,
	plan assignmentusecase.ExactNormalAssignmentPlan,
	canonical *[]assignmentusecase.ExactNormalParticipantReservation,
) error {
	if index == 0 {
		*canonical = cloneExactParticipantReservations(plan.ParticipantReservations)
		return nil
	}
	if !reflect.DeepEqual(*canonical, plan.ParticipantReservations) {
		return invalidSeriesGraph("assignment plans contain inconsistent participant reservations")
	}
	return nil
}

func validatePlanUniqueness(
	plan assignmentusecase.ExactNormalAssignmentPlan,
	seenCategories map[domain.Category]struct{},
	seenSlots map[uuid.UUID]struct{},
	seenEvidence map[uuid.UUID]struct{},
) error {
	if _, duplicate := seenCategories[plan.Category]; duplicate {
		return invalidSeriesGraph("assignment plans contain a duplicate category")
	}
	seenCategories[plan.Category] = struct{}{}
	if _, duplicate := seenSlots[plan.Scope.SlotID]; duplicate {
		return invalidSeriesGraph("assignment plans contain a duplicate slot")
	}
	seenSlots[plan.Scope.SlotID] = struct{}{}

	for _, id := range assignmentPlanEvidenceIDs(plan) {
		if id == uuid.Nil {
			return invalidSeriesGraph("assignment plan contains an empty evidence identity")
		}
		if _, duplicate := seenEvidence[id]; duplicate {
			return invalidSeriesGraph("assignment plans contain duplicate evidence identity")
		}
		seenEvidence[id] = struct{}{}
	}
	return nil
}

func validatePlanTaskUsage(
	plan assignmentusecase.ExactNormalAssignmentPlan,
	participants [2]uuid.UUID,
	seenTasks map[uuid.UUID]struct{},
	seenTaskVersions map[domain.TaskVersionRef]struct{},
) error {
	for _, edge := range plan.SelectedEdges {
		taskID := edge.Snapshot.TaskID
		if _, duplicate := seenTasks[taskID]; duplicate {
			return invalidSeriesGraph("assignment plans reuse a task")
		}
		seenTasks[taskID] = struct{}{}
		ref := domain.TaskVersionRef{TaskID: taskID, Version: edge.Snapshot.Version}
		if _, duplicate := seenTaskVersions[ref]; duplicate {
			return invalidSeriesGraph("assignment plans reuse a task version")
		}
		seenTaskVersions[ref] = struct{}{}
		if taskUsedByParticipants(plan, participants, taskID) {
			return invalidSeriesGraph("assignment plan delivers a task already in participant history")
		}
	}
	return nil
}

func taskUsedByParticipants(
	plan assignmentusecase.ExactNormalAssignmentPlan,
	participants [2]uuid.UUID,
	taskID uuid.UUID,
) bool {
	for _, participantID := range participants {
		for _, history := range plan.History {
			if history.ParticipantID == participantID && history.TaskID == taskID {
				return true
			}
		}
	}
	return false
}

func validateSelectedPlanCoverage(
	selected []domain.Category,
	seenCategories map[domain.Category]struct{},
) error {
	for _, category := range selected {
		if _, ok := seenCategories[category]; !ok {
			return invalidSeriesGraph("assignment plans do not cover selected categories")
		}
	}
	return nil
}

func assignmentPlanEvidenceIDs(plan assignmentusecase.ExactNormalAssignmentPlan) []uuid.UUID {
	ids := make([]uuid.UUID, 0, 4+len(plan.SelectedEdges)*3)
	ids = append(ids, plan.PlanID, plan.PlanRevisionID, plan.BranchID, plan.DecisionEvidence.ID)
	for _, edge := range plan.SelectedEdges {
		ids = append(ids, edge.ID, edge.ReservationID, edge.Snapshot.SnapshotID)
	}
	return ids
}

func sameParticipantSet(values []uuid.UUID, expected [2]uuid.UUID) bool {
	if len(values) != len(expected) {
		return false
	}
	seen := make(map[uuid.UUID]struct{}, len(values))
	for _, value := range values {
		if value == uuid.Nil {
			return false
		}
		seen[value] = struct{}{}
	}
	if len(seen) != 2 {
		return false
	}
	_, firstFound := seen[expected[0]]
	_, secondFound := seen[expected[1]]
	return firstFound && secondFound
}

func buildGraph(input normalizedInput) (SeriesGraph, error) {
	want := input.series.Format.WinsRequired()*2 - 1
	plansByCategory := make(map[domain.Category]assignmentusecase.ExactNormalAssignmentPlan, len(input.assignmentPlans))
	for _, plan := range input.assignmentPlans {
		plansByCategory[plan.Category] = assignmentusecase.CloneExactNormalAssignmentPlan(plan)
	}

	graph := SeriesGraph{
		CommandID:               input.commandID,
		Series:                  cloneDomainSeries(input.series),
		CategoryRevision:        cloneCategoryRevision(input.categoryRevision),
		SelectedCategories:      append([]domain.Category(nil), input.selectedCategories...),
		Assignments:             make([]AssignmentAggregate, 0, want),
		ParticipantReservations: make([]assignmentusecase.ExactNormalParticipantReservation, 0, want*2),
		TaskReservations:        make([]TaskReservation, 0, want*3),
		Snapshots:               make([]SnapshotRecord, 0, want*3),
		DeliveryReceipts:        make([]domain.TaskDeliveryReceipt, 0, want*4),
	}

	for position, category := range input.selectedCategories {
		plan, ok := plansByCategory[category]
		if !ok {
			return SeriesGraph{}, invalidSeriesGraph("missing plan for selected category")
		}
		executable := position == 0
		slot, aggregate, err := buildSlotAggregate(input.series, plan, position+1, input.deliveredAt, executable)
		if err != nil {
			return SeriesGraph{}, err
		}
		graph.Assignments = append(graph.Assignments, aggregate)
		if executable {
			graph.Series.Slots = append(graph.Series.Slots, slot)
			graph.ParticipantReservations = append(graph.ParticipantReservations, aggregate.Plan.ParticipantReservations...)
		}
		for _, edge := range aggregate.Plan.SelectedEdges {
			graph.TaskReservations = append(graph.TaskReservations, taskReservationFromEdge(aggregate, edge))
			graph.Snapshots = append(graph.Snapshots, snapshotRecordFromEdge(aggregate, edge))
		}
		graph.DeliveryReceipts = append(graph.DeliveryReceipts, aggregate.DeliveryReceipts...)
	}
	if err := finalizeDigests(&graph); err != nil {
		return SeriesGraph{}, err
	}
	if err := graph.Validate(); err != nil {
		return SeriesGraph{}, err
	}
	return cloneSeriesGraph(graph), nil
}

func buildSlotAggregate(
	series domain.Series,
	plan assignmentusecase.ExactNormalAssignmentPlan,
	position int,
	deliveredAt time.Time,
	executable bool,
) (domain.GameSlot, AssignmentAggregate, error) {
	attemptID := derivedID(plan.Scope.SlotID, "series-graph-attempt-v1")
	assignmentID := derivedID(plan.PlanID, "series-graph-assignment-v1")
	attempt := domain.Game{ID: attemptID, SlotID: plan.Scope.SlotID, AttemptNo: 1, State: domain.GameStatePlanned}
	slot := domain.GameSlot{
		ID: plan.Scope.SlotID, SeriesID: series.ID, Position: position,
		Category: plan.Category, ScoreBefore: domain.SeriesScore{}, Attempts: []domain.Game{attempt},
	}

	if len(plan.SelectedEdges) != domain.AssignmentReserveCount+1 {
		return domain.GameSlot{}, AssignmentAggregate{}, invalidSeriesGraph("assignment plan does not contain primary and reserve edges")
	}
	primary := plan.SelectedEdges[0].Snapshot
	reserves := make([]domain.AssignmentTaskSnapshot, domain.AssignmentReserveCount)
	for index := range reserves {
		reserves[index] = plan.SelectedEdges[index+1].Snapshot
	}
	assignment, err := domain.NewAssignment(
		assignmentID, attemptID, series.FirstParticipantID, series.SecondParticipantID,
		primary, reserves,
	)
	if err != nil {
		return domain.GameSlot{}, AssignmentAggregate{}, invalidSeriesGraph("assignment aggregate: %v", err)
	}
	receipts := make([]domain.TaskDeliveryReceipt, 0, 2)
	if executable {
		history, historyErr := domain.NewParticipantTaskHistory(nil)
		if historyErr != nil {
			return domain.GameSlot{}, AssignmentAggregate{}, invalidSeriesGraph("assignment history: %v", historyErr)
		}
		for _, participantID := range [2]uuid.UUID{series.FirstParticipantID, series.SecondParticipantID} {
			receiptID := derivedDeliveryID(assignmentID, participantID)
			receipt, deliverErr := assignment.DeliverTo(receiptID, participantID, deliveredAt, &history)
			if deliverErr != nil {
				return domain.GameSlot{}, AssignmentAggregate{}, invalidSeriesGraph("assignment delivery: %v", deliverErr)
			}
			receipts = append(receipts, receipt)
		}
	}

	aggregate := AssignmentAggregate{
		ID: assignmentID, AttemptID: attemptID, SlotID: slot.ID, Position: position,
		Plan: assignmentusecase.CloneExactNormalAssignmentPlan(plan), Assignment: assignment,
		DeliveryReceipts: append([]domain.TaskDeliveryReceipt(nil), receipts...),
	}
	return slot, aggregate, nil
}

func taskReservationFromEdge(aggregate AssignmentAggregate, edge assignmentusecase.ExactNormalAssignmentEdge) TaskReservation {
	return TaskReservation{
		ReservationID: edge.ReservationID, EdgeID: edge.ID,
		AssignmentID: aggregate.ID, AttemptID: aggregate.AttemptID, SlotID: aggregate.SlotID,
		Position: edge.Position, SnapshotID: edge.Snapshot.SnapshotID, TaskID: edge.Snapshot.TaskID,
		Version: edge.Snapshot.Version, Kind: edge.Snapshot.Kind, ContentDigest: edge.ContentDigest,
	}
}

func snapshotRecordFromEdge(aggregate AssignmentAggregate, edge assignmentusecase.ExactNormalAssignmentEdge) SnapshotRecord {
	return SnapshotRecord{
		AssignmentID: aggregate.ID, AttemptID: aggregate.AttemptID,
		SlotID: aggregate.SlotID, Position: edge.Position, Snapshot: cloneTaskSnapshot(edge.Snapshot),
		ContentDigest: edge.ContentDigest,
	}
}

func derivedID(namespace uuid.UUID, label string) uuid.UUID {
	return uuid.NewSHA1(namespace, []byte(label))
}

func derivedDeliveryID(assignmentID, participantID uuid.UUID) uuid.UUID {
	return uuid.NewSHA1(assignmentID, []byte("series-graph-delivery-v1:"+participantID.String()))
}

func invalidSeriesGraph(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidSeriesGraph, fmt.Sprintf(format, args...))
}
