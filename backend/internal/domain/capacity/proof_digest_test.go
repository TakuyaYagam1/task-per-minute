package capacity_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain/capacity"
)

func TestValidProofDigest(t *testing.T) {
	t.Parallel()

	require.True(t, capacity.ValidProofDigest(strings.Repeat("ab", 32)))
	require.False(t, capacity.ValidProofDigest(strings.Repeat("ab", 31)))
	require.False(t, capacity.ValidProofDigest(strings.Repeat("zz", 32)))
	require.False(t, capacity.ValidProofDigest(""))
}
