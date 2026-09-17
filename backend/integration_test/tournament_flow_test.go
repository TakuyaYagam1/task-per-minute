//go:build integration

package integration_test

import (
	"testing"

	gameintegration "github.com/TakuyaYagam1/task-per-minute/integration_test/game"
	tournamentintegration "github.com/TakuyaYagam1/task-per-minute/integration_test/tournament"
)

func TestTournamentFlow(t *testing.T) {
	t.Run("tournament roster bounds and attendance", func(t *testing.T) {
		tournamentintegration.RunTournamentRosterMigration(t, sharedPool)
		TestTournamentUseCases(t)
	})

	t.Run("series modes and immutable game attempts", func(t *testing.T) {
		gameintegration.RunGameMigration(t, sharedPool)
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
