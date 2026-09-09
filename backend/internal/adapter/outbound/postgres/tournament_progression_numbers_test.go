package postgres

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestProgressionInt16Bounds(t *testing.T) {
	for _, value := range []int{math.MinInt16, 0, math.MaxInt16} {
		got, err := progressionInt16(value)
		require.NoError(t, err)
		require.Equal(t, value, int(got))
	}
	for _, value := range []int{math.MinInt16 - 1, math.MaxInt16 + 1} {
		got, err := progressionInt16(value)
		require.ErrorIs(t, err, domain.ErrConflict)
		require.Zero(t, got)
	}
}

func TestProgressionInt32Bounds(t *testing.T) {
	for _, value := range []int{math.MinInt32, 0, math.MaxInt32} {
		got, err := progressionInt32(value)
		require.NoError(t, err)
		require.Equal(t, value, int(got))
	}
	for _, value := range []int{math.MinInt32 - 1, math.MaxInt32 + 1} {
		got, err := progressionInt32(value)
		require.ErrorIs(t, err, domain.ErrConflict)
		require.Zero(t, got)
	}
}
