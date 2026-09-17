//go:build integration

package tournament_test

import (
	"testing"

	tournamentintegration "github.com/TakuyaYagam1/task-per-minute/integration_test/tournament"
)

func TestTournamentRosterMigration(t *testing.T) {
	tournamentintegration.RunTournamentRosterMigration(t, sharedPool)
}
