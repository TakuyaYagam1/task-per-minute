package golden

import (
	"bytes"
	"encoding/gob"
	"sort"

	"github.com/google/uuid"

	goldenexecution "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/execution"
)

func connectionEncode(value any) ([]byte, error) {
	var buffer bytes.Buffer
	if err := gob.NewEncoder(&buffer).Encode(value); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func sortConnectionIDs(values []uuid.UUID) {
	sort.Slice(values, func(i, j int) bool { return bytes.Compare(values[i][:], values[j][:]) < 0 })
}

func connectionIDsCanonical(values []uuid.UUID) bool {
	for index, value := range values {
		if value == uuid.Nil || (index > 0 && bytes.Compare(values[index-1][:], value[:]) >= 0) {
			return false
		}
	}
	return true
}

func connectionIDsSubset(values, superset []uuid.UUID) bool {
	for _, value := range values {
		if !connectionContainsID(superset, value) {
			return false
		}
	}
	return true
}

func connectionEqualIDs(first, second []uuid.UUID) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if first[index] != second[index] {
			return false
		}
	}
	return true
}

func connectionContainsID(values []uuid.UUID, target uuid.UUID) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func connectionWithoutID(values []uuid.UUID, target uuid.UUID) []uuid.UUID {
	result := make([]uuid.UUID, 0, len(values))
	for _, value := range values {
		if value != target {
			result = append(result, value)
		}
	}
	return result
}

func connectionUUIDPointer(value uuid.UUID) *uuid.UUID {
	return &value
}

func connectionValidIdentitySet(values []uuid.UUID) bool {
	seen := make(map[uuid.UUID]struct{}, len(values))
	for _, value := range values {
		if value == uuid.Nil {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func connectionRetainedIdentitySet(execution GoldenWaveExecution) map[uuid.UUID]struct{} {
	return goldenexecution.RetainedIdentitySet(execution)
}

func cloneConnectionExecutionExpectation(input GoldenWaveExecutionExpectation) GoldenWaveExecutionExpectation {
	return goldenexecution.CloneExecutionExpectation(input)
}
