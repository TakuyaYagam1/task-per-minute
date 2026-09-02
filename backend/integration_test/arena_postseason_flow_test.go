//go:build integration

package integration_test

import "testing"

func TestArenaPostseasonFlow(t *testing.T) {
	t.Run("no solve replay retains the attempt chain", func(t *testing.T) {
		TestArenaSeriesGameMigration(t)
	})

	t.Run("golden resolution retains submissions positions and retries", func(t *testing.T) {
		TestArenaGoldenMigration(t)
		TestArenaGoldenRepositoryRestoresRetainedExecutionEvidence(t)
	})

	t.Run("strength seeded bracket and champion lineage", func(t *testing.T) {
		TestArenaProjectionMigration(t)
		TestArenaProjectionRepositoryPublishesScopedStandingsAndBracketHistory(t)
	})

	t.Run("final ban pick persists one immutable draft", func(t *testing.T) {
		TestArenaDraftMigration(t)
		TestArenaDraftRepositoryPersistsOneImmutableRevisionChain(t)
	})

	t.Run("cancellation and correction preserve one terminal authority", func(t *testing.T) {
		TestArenaTournamentLifecycleUseCase(t)
		TestArenaCorrectionRepository(t)
	})
}
