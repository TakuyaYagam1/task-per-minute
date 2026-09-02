//go:build integration

package integration_test

import "testing"

func TestArenaRestartPauseRecovery(t *testing.T) {
	scenarios := []struct {
		name string
		run  func(*testing.T)
	}{
		{
			name: "tournament nonterminal states survive persistence reload",
			run:  TestArenaTournamentMigration,
		},
		{
			name: "lifecycle authority resumes from PostgreSQL with idempotent CAS",
			run:  TestArenaTournamentLifecycleUseCase,
		},
		{
			name: "wave nonterminal states and ready deadlines survive persistence reload",
			run:  TestArenaWaveMigration,
		},
		{
			name: "wave readiness and start recover exactly once",
			run:  TestArenaWaveRepositorySerializesReadinessAndStart,
		},
		{
			name: "draft execution epoch break restores the authoritative deadline",
			run:  TestArenaDraftMigration,
		},
		{
			name: "series and game epoch-break outcomes survive persistence reload",
			run:  TestArenaSeriesGameMigration,
		},
		{
			name: "game attempts retain stable history behind scoped CAS",
			run:  TestArenaGameRepositoryUsesScopedCASAndStableAttemptHistory,
		},
		{
			name: "nested pause graph restores reconnect ownership and deadlines",
			run:  TestArenaReconnectMigration,
		},
		{
			name: "reconnect resume race accepts one authoritative decision",
			run:  TestArenaReconnectMigrationResumeCAS,
		},
		{
			name: "repeated recovery admits one reconnect root per presence epoch",
			run:  TestArenaReconnectContinuationRootPresenceEpoch,
		},
		{
			name: "wave pause and reconnect suspension commit atomically",
			run:  TestArenaReconnectContinuationPauseAtomicity,
		},
		{
			name: "restart migration retains the exact reconnect deadline boundary",
			run:  TestArenaReconnectContinuationMigrationAcceptsRetainedDeadlineBoundary,
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, scenario.run)
	}
}
