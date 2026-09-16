//go:build integration

package integration_test

import "testing"

func TestRecoveryRestartPause(t *testing.T) {
	scenarios := []struct {
		name string
		run  func(*testing.T)
	}{
		{
			name: "tournament nonterminal states survive persistence reload",
			run:  TestTournamentMigration,
		},
		{
			name: "lifecycle authority resumes from PostgreSQL with idempotent CAS",
			run:  TestTournamentLifecycleUseCase,
		},
		{
			name: "wave nonterminal states and ready deadlines survive persistence reload",
			run:  TestExecutionMigration,
		},
		{
			name: "wave readiness and start recover exactly once",
			run:  TestExecutionRepositorySerializesReadinessAndStart,
		},
		{
			name: "draft execution epoch break restores the authoritative deadline",
			run:  TestDraftMigration,
		},
		{
			name: "series and game epoch-break outcomes survive persistence reload",
			run:  TestGameMigration,
		},
		{
			name: "nested pause graph restores reconnect ownership and deadlines",
			run:  TestReconnectMigration,
		},
		{
			name: "reconnect resume race accepts one authoritative decision",
			run:  TestReconnectMigrationResumeCAS,
		},
		{
			name: "repeated recovery admits one reconnect root per presence epoch",
			run:  TestReconnectContinuationRootPresenceEpoch,
		},
		{
			name: "wave pause and reconnect suspension commit atomically",
			run:  TestReconnectContinuationPauseAtomicity,
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, scenario.run)
	}
}
