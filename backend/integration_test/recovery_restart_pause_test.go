//go:build integration

package integration_test

import (
	"testing"

	draftintegration "github.com/TakuyaYagam1/task-per-minute/integration_test/draft"
	executionintegration "github.com/TakuyaYagam1/task-per-minute/integration_test/execution"
	gameintegration "github.com/TakuyaYagam1/task-per-minute/integration_test/game"
	reconnectintegration "github.com/TakuyaYagam1/task-per-minute/integration_test/reconnect"
	tournamentintegration "github.com/TakuyaYagam1/task-per-minute/integration_test/tournament"
)

func TestRecoveryRestartPause(t *testing.T) {
	scenarios := []struct {
		name string
		run  func(*testing.T)
	}{
		{
			name: "tournament nonterminal states survive persistence reload",
			run: func(t *testing.T) {
				tournamentintegration.RunTournamentMigration(t, sharedPool)
			},
		},
		{
			name: "lifecycle authority resumes from PostgreSQL with idempotent CAS",
			run:  TestTournamentLifecycleUseCase,
		},
		{
			name: "wave nonterminal states and ready deadlines survive persistence reload",
			run: func(t *testing.T) {
				executionintegration.RunExecutionMigration(t, sharedPool)
			},
		},
		{
			name: "wave readiness and start recover exactly once",
			run:  TestExecutionRepositorySerializesReadinessAndStart,
		},
		{
			name: "draft execution epoch break restores the authoritative deadline",
			run: func(t *testing.T) {
				draftintegration.RunDraftMigration(t, sharedPool)
			},
		},
		{
			name: "series and game epoch-break outcomes survive persistence reload",
			run: func(t *testing.T) {
				gameintegration.RunGameMigration(t, sharedPool)
			},
		},
		{
			name: "nested pause graph restores reconnect ownership and deadlines",
			run: func(t *testing.T) {
				reconnectintegration.RunReconnectMigration(t, sharedPool)
			},
		},
		{
			name: "reconnect resume race accepts one authoritative decision",
			run: func(t *testing.T) {
				reconnectintegration.RunReconnectMigrationResumeCAS(t, sharedPool)
			},
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
