package wavestart

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNormalizeSwissRoundProofTimeUsesPostgresPrecision(t *testing.T) {
	input := time.Date(2026, time.September, 27, 17, 2, 3, 456789123, time.FixedZone("test", 2*60*60))

	got := normalizeSwissRoundProofTime(input)

	require.Equal(t, input.UTC().Truncate(time.Microsecond), got)
	require.Equal(t, 0, got.Nanosecond()%1000)
}
