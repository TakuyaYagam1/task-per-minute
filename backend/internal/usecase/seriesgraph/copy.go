package seriesgraph

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
)

func cloneSeriesGraph(graph SeriesGraph) SeriesGraph {
	cloned := graph
	cloned.Series = cloneDomainSeries(graph.Series)
	cloned.CategoryRevision = cloneCategoryRevision(graph.CategoryRevision)
	cloned.SelectedCategories = append([]domain.Category(nil), graph.SelectedCategories...)
	cloned.Assignments = cloneAssignmentAggregates(graph.Assignments)
	cloned.ParticipantReservations = cloneExactParticipantReservations(graph.ParticipantReservations)
	cloned.TaskReservations = cloneTaskReservations(graph.TaskReservations)
	cloned.Snapshots = cloneSnapshotRecords(graph.Snapshots)
	cloned.DeliveryReceipts = append([]domain.TaskDeliveryReceipt(nil), graph.DeliveryReceipts...)
	cloned.Proof = cloneSeriesGraphProof(graph.Proof)
	return cloned
}

func cloneDomainSeries(series domain.Series) domain.Series {
	cloned := series
	cloned.WinnerID = cloneUUIDPointer(series.WinnerID)
	cloned.CurrentScoreRevisionID = cloneScoreRevisionPointer(series.CurrentScoreRevisionID)
	cloned.CurrentResultRevisionID = cloneResultRevisionPointer(series.CurrentResultRevisionID)
	cloned.Slots = cloneGameSlots(series.Slots)
	return cloned
}

func cloneGameSlots(slots []domain.GameSlot) []domain.GameSlot {
	if slots == nil {
		return nil
	}
	cloned := make([]domain.GameSlot, len(slots))
	for index, slot := range slots {
		cloned[index] = slot
		cloned[index].Attempts = make([]domain.Game, len(slot.Attempts))
		for attemptIndex, attempt := range slot.Attempts {
			cloned[index].Attempts[attemptIndex] = cloneGame(attempt)
		}
	}
	return cloned
}

func cloneGame(game domain.Game) domain.Game {
	cloned := game
	cloned.WinnerID = cloneUUIDPointer(game.WinnerID)
	cloned.ResultRevisionID = cloneResultRevisionPointer(game.ResultRevisionID)
	return cloned
}

func cloneUUIDPointer(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneScoreRevisionPointer(value *domain.SeriesScoreRevisionID) *domain.SeriesScoreRevisionID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneResultRevisionPointer(value *domain.OfficialResultRevisionID) *domain.OfficialResultRevisionID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneCategoryRevision(revision draftusecase.CategoryRevision) draftusecase.CategoryRevision {
	cloned := revision
	cloned.CategoryPool.Categories = append([]domain.Category(nil), revision.CategoryPool.Categories...)
	return cloned
}

func cloneAssignmentPlans(plans []assignmentusecase.ExactNormalAssignmentPlan) []assignmentusecase.ExactNormalAssignmentPlan {
	if plans == nil {
		return nil
	}
	cloned := make([]assignmentusecase.ExactNormalAssignmentPlan, len(plans))
	for index, plan := range plans {
		cloned[index] = assignmentusecase.CloneExactNormalAssignmentPlan(plan)
	}
	return cloned
}

func cloneAssignmentAggregates(aggregates []AssignmentAggregate) []AssignmentAggregate {
	if aggregates == nil {
		return nil
	}
	cloned := make([]AssignmentAggregate, len(aggregates))
	for index, aggregate := range aggregates {
		cloned[index] = aggregate
		cloned[index].Plan = assignmentusecase.CloneExactNormalAssignmentPlan(aggregate.Plan)
		cloned[index].DeliveryReceipts = append([]domain.TaskDeliveryReceipt(nil), aggregate.DeliveryReceipts...)
	}
	return cloned
}

func cloneExactParticipantReservations(values []assignmentusecase.ExactNormalParticipantReservation) []assignmentusecase.ExactNormalParticipantReservation {
	if values == nil {
		return nil
	}
	return append([]assignmentusecase.ExactNormalParticipantReservation(nil), values...)
}

func cloneTaskReservations(values []TaskReservation) []TaskReservation {
	if values == nil {
		return nil
	}
	return append([]TaskReservation(nil), values...)
}

func cloneSnapshotRecords(values []SnapshotRecord) []SnapshotRecord {
	if values == nil {
		return nil
	}
	cloned := make([]SnapshotRecord, len(values))
	for index, value := range values {
		cloned[index] = value
		cloned[index].Snapshot = cloneTaskSnapshot(value.Snapshot)
	}
	return cloned
}

func cloneTaskSnapshot(snapshot domain.AssignmentTaskSnapshot) domain.AssignmentTaskSnapshot {
	cloned := snapshot
	cloned.Hints = append([]string(nil), snapshot.Hints...)
	cloned.TaskURL = cloneStringPointer(snapshot.TaskURL)
	cloned.SourceFileURL = cloneStringPointer(snapshot.SourceFileURL)
	return cloned
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneSeriesGraphProof(proof SeriesGraphProof) SeriesGraphProof {
	cloned := proof
	cloned.AssignmentProofs = append([]string(nil), proof.AssignmentProofs...)
	return cloned
}
