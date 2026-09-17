package plan

import (
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/taskexec"
)

func cloneGoldenCandidates(input []TaskVersion) []TaskVersion {
	result := append([]TaskVersion(nil), input...)
	for index := range result {
		result[index].Task = *taskexec.CloneTask(&input[index].Task)
	}
	return result
}

// CloneTaskSnapshot returns a deep copy of an assignment snapshot owned by a plan edge.
func CloneTaskSnapshot(snapshot domain.AssignmentTaskSnapshot) domain.AssignmentTaskSnapshot {
	clone := snapshot
	clone.Hints = append([]string(nil), snapshot.Hints...)
	clone.TaskURL = cloneGoldenStringPointer(snapshot.TaskURL)
	clone.SourceFileURL = cloneGoldenStringPointer(snapshot.SourceFileURL)
	return clone
}

func cloneGoldenStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
