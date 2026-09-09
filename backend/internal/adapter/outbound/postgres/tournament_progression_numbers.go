package postgres

import (
	"math"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func progressionInt16(value int) (int16, error) {
	if value < math.MinInt16 || value > math.MaxInt16 {
		return 0, domain.ErrConflict
	}
	return int16(value), nil
}

func progressionInt32(value int) (int32, error) {
	if value < math.MinInt32 || value > math.MaxInt32 {
		return 0, domain.ErrConflict
	}
	return int32(value), nil
}
