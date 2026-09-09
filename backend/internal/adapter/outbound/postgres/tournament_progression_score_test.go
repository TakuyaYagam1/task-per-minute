package postgres

import (
	"math"
	"math/big"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

func TestProgressionScoreMilliPostgresNormalization(t *testing.T) {
	for _, item := range []struct {
		value, want int64
		exponent    int32
	}{
		{0, 0, 0}, {3, 3000, 0}, {3000, 3000, -3}, {30, 3000, -1}, {30000, 3000, -4},
	} {
		value, err := progressionScoreMilli(pgtype.Numeric{Int: big.NewInt(item.value), Exp: item.exponent, Valid: true})
		require.NoError(t, err)
		require.Equal(t, item.want, *value)
	}
	for _, value := range []pgtype.Numeric{
		{}, {Int: big.NewInt(12), Exp: -4, Valid: true}, {Int: big.NewInt(math.MaxInt64), Valid: true},
		{NaN: true, Int: big.NewInt(0), Valid: true}, {InfinityModifier: pgtype.Infinity, Int: big.NewInt(0), Valid: true},
	} {
		_, err := progressionScoreMilli(value)
		require.Error(t, err)
	}
}
