//go:build integration

package result

import "testing"

func TestTournamentAdminCorrectionAuthorityHydratesPlayoffStage(t *testing.T) {
	RunTournamentAdminCorrectionAuthorityHydratesPlayoffStage(t, sharedPool)
}

func TestResultCorrectionRepository(t *testing.T) {
	RunResultCorrectionRepository(t, sharedPool)
}
