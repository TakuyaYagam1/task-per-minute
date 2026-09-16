//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	auditrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/audit"
	correctionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/correction"
)

func TestResultAuditRepository(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	fixture := createCorrectionRepositoryFixture(ctx, t)

	correctionRepository := correctionrepo.NewCorrectionPostgres(postgres.NewTxManager(sharedPool))
	firstInput := newCorrectionInput(
		ctx, t, fixture, fixture.result, fixture.projection, 1, fixture.nextTime,
	)
	first, err := correctionRepository.Rebuild(ctx, firstInput)
	require.NoError(t, err)
	secondInput := newCorrectionInput(
		ctx, t, fixture, first.ResultCommit, first.Projection, 0, fixture.nextTime.Add(time.Second),
	)
	second, err := correctionRepository.Rebuild(ctx, secondInput)
	require.NoError(t, err)

	repository := auditrepo.NewAuditPostgres(postgres.NewTxManager(sharedPool))
	records := listAuditRecords(ctx, t, repository, auditrepo.AuditFilter{
		TournamentID: fixture.resultFixture.draft.tournamentID,
		PageSize:     2,
	})
	require.Len(t, records, 6)
	seen := make(map[string]struct{}, len(records))
	current := map[uuid.UUID]struct{}{
		second.ResultCommit.GameRevision.ID:   {},
		second.ResultCommit.SeriesRevision.ID: {},
	}
	currentCount := 0
	for _, record := range records {
		key := fmt.Sprintf("%s/%s", record.AuditEventID, record.OfficialResultRevisionID)
		_, duplicate := seen[key]
		require.False(t, duplicate)
		seen[key] = struct{}{}

		_, expectedCurrent := current[record.OfficialResultRevisionID]
		require.Equal(t, expectedCurrent, record.IsCurrent)
		require.Equal(t, !expectedCurrent, record.IsSuperseded)
		if record.IsCurrent {
			currentCount++
		}

		var payload map[string]any
		require.NoError(t, json.Unmarshal(record.RedactedPayload, &payload))
		for payloadKey := range payload {
			require.Contains(t, auditAllowedPayloadKeys(), payloadKey)
		}
		require.NotContains(t, payload, "secret_token")
		require.NotContains(t, payload, "ip_address")
	}
	require.Equal(t, 2, currentCount)

	attemptID := fixture.resultFixture.attemptID
	require.Len(t, listAuditRecords(ctx, t, repository, auditrepo.AuditFilter{
		TournamentID: fixture.resultFixture.draft.tournamentID,
		EntityKind:   "game_attempt",
		EntityID:     &attemptID,
	}), 3)
	seriesID := fixture.resultFixture.draft.seriesID
	require.Len(t, listAuditRecords(ctx, t, repository, auditrepo.AuditFilter{
		TournamentID: fixture.resultFixture.draft.tournamentID,
		EntityKind:   "series",
		EntityID:     &seriesID,
	}), 3)
	require.Len(t, listAuditRecords(ctx, t, repository, auditrepo.AuditFilter{
		TournamentID: fixture.resultFixture.draft.tournamentID,
		EventType:    "tournament.result.corrected",
	}), 4)
	require.Len(t, listAuditRecords(ctx, t, repository, auditrepo.AuditFilter{
		TournamentID: fixture.resultFixture.draft.tournamentID,
		ActorKind:    "operator",
		ActorID:      &firstInput.OperatorID,
	}), 2)
	require.Len(t, listAuditRecords(ctx, t, repository, auditrepo.AuditFilter{
		TournamentID: fixture.resultFixture.draft.tournamentID,
		ResultReason: "surrender",
	}), 4)
	firstOccurredAt := first.ResultCommit.Audit.OccurredAt.Time.UTC()
	require.Len(t, listAuditRecords(ctx, t, repository, auditrepo.AuditFilter{
		TournamentID: fixture.resultFixture.draft.tournamentID,
		OccurredFrom: &firstOccurredAt,
		OccurredTo:   &firstOccurredAt,
	}), 2)
}

func listAuditRecords(
	ctx context.Context, tb testing.TB,
	repository *auditrepo.AuditPostgres,
	filter auditrepo.AuditFilter,
) []auditrepo.AuditRecord {
	tb.Helper()

	records := make([]auditrepo.AuditRecord, 0)
	for {
		page, err := repository.List(ctx, filter)
		require.NoError(tb, err)
		records = append(records, page.Records...)
		if page.NextCursor == nil {
			return records
		}
		filter.Cursor = page.NextCursor
	}
}

func auditAllowedPayloadKeys() map[string]struct{} {
	return map[string]struct{}{
		"attempt_id": {}, "entity_id": {}, "entity_kind": {}, "previous_revision_id": {},
		"projection_revision_id": {}, "reason": {}, "result_reason": {}, "revision_number": {},
		"series_id": {}, "source_projection_revision_id": {}, "state": {}, "tournament_id": {},
		"winner_id": {},
	}
}
