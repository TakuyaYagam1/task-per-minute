//go:build integration

package projection

import "testing"

func TestProjectionRepositoryPublishesScopedStandingsAndBracketHistory(t *testing.T) {
	RunProjectionRepositoryPublishesScopedStandingsAndBracketHistory(t, sharedPool)
}

func TestProjectionRepositoryRejectsChampionBeforeTerminalFinal(t *testing.T) {
	RunProjectionRepositoryRejectsChampionBeforeTerminalFinal(t, sharedPool)
}
