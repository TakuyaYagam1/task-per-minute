//go:build integration

package integration_test

import (
	"testing"

	draftintegration "github.com/TakuyaYagam1/task-per-minute/integration_test/draft"
	gameintegration "github.com/TakuyaYagam1/task-per-minute/integration_test/game"
	goldenintegration "github.com/TakuyaYagam1/task-per-minute/integration_test/golden"
	projectionintegration "github.com/TakuyaYagam1/task-per-minute/integration_test/projection"
	resultintegration "github.com/TakuyaYagam1/task-per-minute/integration_test/result"
)

func TestPlayoffFlow(t *testing.T) {
	t.Run("no solve replay retains the attempt chain", func(t *testing.T) {
		gameintegration.RunGameMigration(t, sharedPool)
	})

	t.Run("golden persistence schema enforces retained evidence", func(t *testing.T) {
		goldenintegration.RunGoldenMigration(t, sharedPool)
	})

	t.Run("strength seeded bracket and champion lineage", func(t *testing.T) {
		projectionintegration.RunProjectionMigration(t, sharedPool)
		TestProjectionRepositoryPublishesScopedStandingsAndBracketHistory(t)
	})

	t.Run("final ban pick persists one immutable draft", func(t *testing.T) {
		draftintegration.RunDraftMigration(t, sharedPool)
		TestDraftRepositoryPersistsOneImmutableRevisionChain(t)
	})

	t.Run("cancellation and correction preserve one terminal authority", func(t *testing.T) {
		TestTournamentLifecycleUseCase(t)
		resultintegration.RunResultCorrectionRepository(t, sharedPool)
	})
}
