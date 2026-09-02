//go:build integration

package integration_test

import "testing"

func TestArenaCoreFlow(t *testing.T) {
	t.Run("tournament roster bounds and attendance", func(t *testing.T) {
		TestArenaRosterMigration(t)
		TestArenaTournamentUseCases(t)
	})

	t.Run("series modes and immutable game attempts", func(t *testing.T) {
		TestArenaSeriesGameMigration(t)
		TestArenaDraftRepositoryPersistsOneImmutableRevisionChain(t)
		TestArenaAssignmentRepositoryCommitsProofAndDeliversExactlyOnce(t)
	})

	t.Run("swiss pairing and wave readiness", func(t *testing.T) {
		TestArenaSwissRepository(t)
		TestArenaWaveRepositorySerializesReadinessAndStart(t)
	})

	t.Run("bo1 settlement standings cutoff and playoff entry", func(t *testing.T) {
		TestArenaConcurrentSettlement(t)
		TestArenaProjectionRepositoryPublishesScopedStandingsAndBracketHistory(t)
	})
}
