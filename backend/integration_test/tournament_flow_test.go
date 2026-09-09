//go:build integration

package integration_test

import "testing"

func TestTournamentFlow(t *testing.T) {
	t.Run("tournament roster bounds and attendance", func(t *testing.T) {
		TestTournamentRosterMigration(t)
		TestTournamentUseCases(t)
	})

	t.Run("series modes and immutable game attempts", func(t *testing.T) {
		TestGameMigration(t)
		TestDraftRepositoryPersistsOneImmutableRevisionChain(t)
		TestAssignmentRepositoryCommitsProofAndDeliversExactlyOnce(t)
	})

	t.Run("swiss pairing and wave readiness", func(t *testing.T) {
		TestSwissRepository(t)
		TestExecutionRepositorySerializesReadinessAndStart(t)
	})

	t.Run("bo1 settlement standings cutoff and playoff entry", func(t *testing.T) {
		TestConcurrentResultSettlement(t)
		TestProjectionRepositoryPublishesScopedStandingsAndBracketHistory(t)
	})
}
