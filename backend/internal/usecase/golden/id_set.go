package golden

import (
	"bytes"
	"crypto/sha256"
	"sort"

	"github.com/google/uuid"
)

func SortIDs(values []uuid.UUID) {
	sort.Slice(values, func(i, j int) bool { return bytes.Compare(values[i][:], values[j][:]) < 0 })
}

func IDsCanonical(values []uuid.UUID) bool {
	for index, value := range values {
		if value == uuid.Nil || (index > 0 && bytes.Compare(values[index-1][:], value[:]) >= 0) {
			return false
		}
	}
	return true
}

func IDsSubset(values, superset []uuid.UUID) bool {
	for _, value := range values {
		if !ContainsID(superset, value) {
			return false
		}
	}
	return true
}

func EqualIDs(first, second []uuid.UUID) bool {
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

func ContainsID(values []uuid.UUID, target uuid.UUID) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func IDSet(values []uuid.UUID) map[uuid.UUID]bool {
	result := make(map[uuid.UUID]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func WithoutID(values []uuid.UUID, target uuid.UUID) []uuid.UUID {
	result := make([]uuid.UUID, 0, len(values))
	for _, value := range values {
		if value != target {
			result = append(result, value)
		}
	}
	return result
}

func DigestIDs(values []uuid.UUID) [sha256.Size]byte {
	canonical := append([]uuid.UUID(nil), values...)
	SortIDs(canonical)
	payload, _ := Encode(canonical)
	return sha256.Sum256(payload)
}

func UUIDPointer(value uuid.UUID) *uuid.UUID {
	return &value
}
