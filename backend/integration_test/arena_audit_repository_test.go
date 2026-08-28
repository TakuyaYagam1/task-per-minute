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
)

func TestArenaAuditRepository(t *testing.T) {
	ctx := context.Background()
	resetArenaMigrationTables(t)
	t.Cleanup(func() { resetArenaMigrationTables(t) })

	fixture := createArenaCorrectionRepositoryFixture(t, ctx)

	correctionRepository := postgres.NewArenaCorrectionPostgres(postgres.NewTxManager(sharedPool))
	firstInput := newArenaCorrectionInput(
		t, ctx, fixture, fixture.result, fixture.projection, 1, fixture.nextTime,
	)
	first, err := correctionRepository.Rebuild(ctx, firstInput)
	require.NoError(t, err)
	secondInput := newArenaCorrectionInput(
		t, ctx, fixture, first.ResultCommit, first.Projection, 0, fixture.nextTime.Add(time.Second),
	)
	second, err := correctionRepository.Rebuild(ctx, secondInput)
	require.NoError(t, err)

	repository := postgres.NewArenaAuditPostgres(postgres.NewTxManager(sharedPool))
	records := listArenaAuditRecords(t, ctx, repository, postgres.ArenaAuditFilter{
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
			require.Contains(t, arenaAuditAllowedPayloadKeys(), payloadKey)
		}
		require.NotContains(t, payload, "secret_token")
		require.NotContains(t, payload, "ip_address")
	}
	require.Equal(t, 2, currentCount)

	attemptID := fixture.resultFixture.attemptID
	require.Len(t, listArenaAuditRecords(t, ctx, repository, postgres.ArenaAuditFilter{
		TournamentID: fixture.resultFixture.draft.tournamentID,
		EntityKind:   "game_attempt",
		EntityID:     &attemptID,
	}), 3)
	seriesID := fixture.resultFixture.draft.seriesID
	require.Len(t, listArenaAuditRecords(t, ctx, repository, postgres.ArenaAuditFilter{
		TournamentID: fixture.resultFixture.draft.tournamentID,
		EntityKind:   "series",
		EntityID:     &seriesID,
	}), 3)
	require.Len(t, listArenaAuditRecords(t, ctx, repository, postgres.ArenaAuditFilter{
		TournamentID: fixture.resultFixture.draft.tournamentID,
		EventType:    "arena.result.corrected",
	}), 4)
	require.Len(t, listArenaAuditRecords(t, ctx, repository, postgres.ArenaAuditFilter{
		TournamentID: fixture.resultFixture.draft.tournamentID,
		ActorKind:    "operator",
		ActorID:      &firstInput.OperatorID,
	}), 2)
	require.Len(t, listArenaAuditRecords(t, ctx, repository, postgres.ArenaAuditFilter{
		TournamentID: fixture.resultFixture.draft.tournamentID,
		ResultReason: "surrender",
	}), 4)
	firstOccurredAt := first.ResultCommit.Audit.OccurredAt.Time.UTC()
	require.Len(t, listArenaAuditRecords(t, ctx, repository, postgres.ArenaAuditFilter{
		TournamentID: fixture.resultFixture.draft.tournamentID,
		OccurredFrom: &firstOccurredAt,
		OccurredTo:   &firstOccurredAt,
	}), 2)
}

func listArenaAuditRecords(
	t testing.TB,
	ctx context.Context,
	repository *postgres.ArenaAuditPostgres,
	filter postgres.ArenaAuditFilter,
) []postgres.ArenaAuditRecord {
	t.Helper()

	records := make([]postgres.ArenaAuditRecord, 0)
	for {
		page, err := repository.List(ctx, filter)
		require.NoError(t, err)
		records = append(records, page.Records...)
		if page.NextCursor == nil {
			return records
		}
		filter.Cursor = page.NextCursor
	}
}

func arenaAuditAllowedPayloadKeys() map[string]struct{} {
	return map[string]struct{}{
		"attempt_id": {}, "entity_id": {}, "entity_kind": {}, "previous_revision_id": {},
		"projection_revision_id": {}, "reason": {}, "result_reason": {}, "revision_number": {},
		"series_id": {}, "source_projection_revision_id": {}, "state": {}, "tournament_id": {},
		"winner_id": {},
	}
}
