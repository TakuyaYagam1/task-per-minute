//go:build integration

package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	auditrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/audit"
)

func listAuditRecords(
	ctx context.Context,
	tb testing.TB,
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
