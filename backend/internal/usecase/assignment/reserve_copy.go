package assignment

import (
	"bytes"
	"sort"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func taskFromSnapshot(snapshot domain.AssignmentTaskSnapshot) domain.Task {
	return domain.Task{
		ID: snapshot.TaskID, Title: snapshot.Title, Description: snapshot.Description,
		Category: snapshot.Category, Difficulty: snapshot.Difficulty,
		TimeLimit: snapshot.TimeLimit, Flag: snapshot.Flag,
		Hints:         append([]string(nil), snapshot.Hints...),
		TaskURL:       cloneStringPointer(snapshot.TaskURL),
		SourceFileURL: cloneStringPointer(snapshot.SourceFileURL),
	}
}

func canonicalReserveHistory(history []TaskReceiptRef) []TaskReceiptRef {
	result := append([]TaskReceiptRef(nil), history...)
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
	clone.EligibleSameCategory = append([]domain.TaskVersionRef(nil), evidence.EligibleSameCategory...)
	return &clone
}

func cloneReserveAssignmentRecord(record ReserveAssignmentRecord) ReserveAssignmentRecord {
	clone := record
	clone.ParticipantIDs = append([]uuid.UUID(nil), record.ParticipantIDs...)
	clone.ParticipantReservations = cloneExactNormalParticipantReservations(record.ParticipantReservations)
	clone.Pool = domain.CloneTaskPool(record.Pool)
	clone.ReceiptHistory = append([]TaskReceiptRef(nil), record.ReceiptHistory...)
	clone.Snapshot = cloneTaskSnapshot(record.Snapshot)
	clone.CategoryExhaustion = cloneReserveCategoryExhaustion(record.CategoryExhaustion)
	return clone
}

// CloneReserveAssignmentRecord returns a detached copy for retained state.
func CloneReserveAssignmentRecord(record ReserveAssignmentRecord) ReserveAssignmentRecord {
	return cloneReserveAssignmentRecord(record)
}
