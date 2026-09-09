//go:build integration

package integration_test

import "testing"

func TestPlayoffFlow(t *testing.T) {
	t.Run("no solve replay retains the attempt chain", func(t *testing.T) {
		TestGameMigration(t)
	})

	t.Run("golden persistence schema enforces retained evidence", func(t *testing.T) {
		TestGoldenMigration(t)
	})

	t.Run("strength seeded bracket and champion lineage", func(t *testing.T) {
		TestProjectionMigration(t)
		TestProjectionRepositoryPublishesScopedStandingsAndBracketHistory(t)
	})

	t.Run("final ban pick persists one immutable draft", func(t *testing.T) {
		TestDraftMigration(t)
		TestDraftRepositoryPersistsOneImmutableRevisionChain(t)
	})

	t.Run("cancellation and correction preserve one terminal authority", func(t *testing.T) {
		TestTournamentLifecycleUseCase(t)
		TestResultCorrectionRepository(t)
	})
}
