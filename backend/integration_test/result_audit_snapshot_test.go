//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	resultintegration "github.com/TakuyaYagam1/task-per-minute/integration_test/result"
)

func TestResultAuditRepositoryPinsSnapshotAcrossPages(t *testing.T) {
	resultintegration.RunResultAuditRepositoryPinsSnapshotAcrossPages(t, sharedPool)
}

// settleCursorSafeAuditRecord remains a thin compatibility bridge for the
// incident snapshot tests, which still compose the root correction fixture.
func settleCursorSafeAuditRecord(
	ctx context.Context,
	tb testing.TB,
	fixture correctionRepositoryFixture,
	settledAt time.Time,
	auditEventID uuid.UUID,
) uuid.UUID {
	tb.Helper()
	return resultintegration.SettleCursorSafeAuditRecord(tb, sharedPool, resultintegration.CursorAuditSeedInput{
		TournamentID: fixture.resultFixture.draft.tournamentID,
		RosterID:     fixture.resultFixture.draft.rosterID,
		ParticipantIDs: [2]uuid.UUID{
			fixture.resultFixture.draft.participantIDs[0],
			fixture.resultFixture.draft.participantIDs[1],
		},
		SettledAt:    settledAt,
		AuditEventID: auditEventID,
	})
}
