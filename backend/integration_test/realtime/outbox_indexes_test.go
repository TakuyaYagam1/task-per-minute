//go:build integration

package realtime_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestRealtimeOutboxPostgresUsesDeliveryIndexes(t *testing.T) {
	ctx := context.Background()
	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, tx.Rollback(ctx)) })
	_, err = tx.Exec(ctx, "SET LOCAL enable_seqscan = off")
	require.NoError(t, err)
	var claimIndexDefinition, resumeIndexDefinition string
	require.NoError(t, tx.QueryRow(ctx, `
		SELECT indexdef
		FROM pg_indexes
		WHERE schemaname = current_schema()
			AND indexname = 'outbox_events_claim_idx'`,
	).Scan(&claimIndexDefinition))
	require.Contains(t, claimIndexDefinition, "WHERE (published_at IS NULL)")
	require.NoError(t, tx.QueryRow(ctx, `
		SELECT indexdef
		FROM pg_indexes
		WHERE schemaname = current_schema()
			AND indexname = 'outbox_events_resume_idx'`,
	).Scan(&resumeIndexDefinition))
	require.Contains(t, resumeIndexDefinition, "(tournament_id, sequence)")

	tests := []struct {
		name          string
		query         string
		args          []any
		acceptedIndex []string
	}{
		{
			name: "unpublished claim",
			query: `EXPLAIN (FORMAT JSON, COSTS OFF)
				SELECT event.id
				FROM outbox_events AS event
				INNER JOIN projection_revisions AS revision
					ON revision.id = event.projection_revision_id
					AND revision.tournament_id = event.tournament_id
					AND revision.roster_id = event.roster_id
					AND revision.revision_number = event.projection_revision
				WHERE event.published_at IS NULL
					AND revision.state IN ('published', 'superseded')
					AND event.available_at <= $1
					AND (event.claimed_until IS NULL OR event.claimed_until <= $1)
					AND NOT EXISTS (
						SELECT 1
						FROM outbox_events AS predecessor
						WHERE predecessor.tournament_id = event.tournament_id
							AND predecessor.sequence < event.sequence
							AND predecessor.published_at IS NULL
					)
				ORDER BY event.tournament_id, event.sequence
				LIMIT $2
				FOR UPDATE OF event SKIP LOCKED`,
			args: []any{time.Now().UTC(), int32(32)},
			acceptedIndex: []string{
				"outbox_events_claim_idx",
				"outbox_events_resume_idx",
			},
		},
		{
			name: "tournament resume",
			query: `EXPLAIN (FORMAT JSON, COSTS OFF)
				SELECT event.id
				FROM outbox_events AS event
				INNER JOIN projection_revisions AS revision
					ON revision.id = event.projection_revision_id
					AND revision.tournament_id = event.tournament_id
					AND revision.roster_id = event.roster_id
					AND revision.revision_number = event.projection_revision
				WHERE event.tournament_id = $1
					AND event.sequence > $2
					AND revision.state IN ('published', 'superseded')
					AND (
						event.audience = 'all'
						OR (event.audience = 'public' AND event.principal_id IS NULL)
					)
				ORDER BY event.tournament_id, event.sequence
				LIMIT $3`,
			args: []any{uuid.New(), int64(10), int32(128)},
			acceptedIndex: []string{
				"outbox_events_resume_idx",
				"outbox_events_sequence_key",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var rawPlan []byte
			err := tx.QueryRow(ctx, test.query, test.args...).Scan(&rawPlan)
			require.NoError(t, err)

			var plan any
			require.NoError(t, json.Unmarshal(rawPlan, &plan))
			indexes := postgresPlanIndexes(plan)
			require.NotEmpty(t, firstMatchingIndex(indexes, test.acceptedIndex), "plan indexes: %v", indexes)
		})
	}
}

func postgresPlanIndexes(value any) []string {
	indexes := make([]string, 0, 2)
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			indexes = append(indexes, postgresPlanIndexes(item)...)
		}
	case map[string]any:
		for key, item := range typed {
			if key == "Index Name" {
				if index, ok := item.(string); ok {
					indexes = append(indexes, index)
				}
			}
			indexes = append(indexes, postgresPlanIndexes(item)...)
		}
	}
	return indexes
}

func firstMatchingIndex(actual, accepted []string) string {
	for _, candidate := range actual {
		for _, expected := range accepted {
			if candidate == expected {
				return candidate
			}
		}
	}
	return ""
}
