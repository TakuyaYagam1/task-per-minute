package leaderboard

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEscapeLeaderboardSearchTreatsWildcardCharactersLiterally(t *testing.T) {
	t.Parallel()

	require.Equal(t, `a\\\%\_`, escapeLeaderboardSearch(`a\%_`))
}
