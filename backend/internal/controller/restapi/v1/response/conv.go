package response

import (
	"math"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// UUIDPtr clones the pointed-at uuid so the openapi-generated alias does not
// alias the caller's slot.
func UUIDPtr(id *uuid.UUID) *openapi_types.UUID {
	if id == nil {
		return nil
	}
	out := *id
	return &out
}

// IntToInt32 saturates value to the int32 range. Useful at the transport
// boundary where openapi-generated types are int32 but domain uses int.
func IntToInt32(value int) int32 {
	if value < math.MinInt32 {
		return math.MinInt32
	}
	if value > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(value)
}

// Int64ToInt32 saturates value to the int32 range.
func Int64ToInt32(value int64) int32 {
	if value < math.MinInt32 {
		return math.MinInt32
	}
	if value > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(value)
}
