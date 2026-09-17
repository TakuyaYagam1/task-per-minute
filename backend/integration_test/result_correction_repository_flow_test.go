//go:build integration

package integration_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	resultintegration "github.com/TakuyaYagam1/task-per-minute/integration_test/result"
	projectionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/projection"
	resultrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result"
)

// correctionRepositoryFixture remains root-owned for adjacent audit and lock
// tests that still compose the historical fixture graph directly. The moved
// correction flow uses resultintegration's explicit-pool fixture runner.
type correctionRepositoryFixture struct {
	resultFixture      resultAuditMigrationFixture
	participants       []uuid.UUID
	result             *resultrepo.ResultCommitRecord
	projection         *projectionrepo.ProjectionRecord
	waveID             uuid.UUID
	windowID           uuid.UUID
	waveRevision       int64
	readinessRevisions map[uuid.UUID]int64
	deadline           time.Time
	nextTime           time.Time
}

func TestTournamentAdminCorrectionAuthorityHydratesPlayoffStage(t *testing.T) {
	resultintegration.RunTournamentAdminCorrectionAuthorityHydratesPlayoffStage(t, sharedPool)
}

func TestResultCorrectionRepository(t *testing.T) {
	resultintegration.RunResultCorrectionRepository(t, sharedPool)
}
