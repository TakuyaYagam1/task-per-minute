//go:build integration

package tournament_test

import (
	"testing"

	tournamentintegration "github.com/TakuyaYagam1/task-per-minute/integration_test/tournament"
)

func TestTournamentMigration(t *testing.T) {
	tournamentintegration.RunTournamentMigration(t, sharedPool)
}
