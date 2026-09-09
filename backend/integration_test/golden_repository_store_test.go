//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestGoldenRepositoryStorePreservesScopeRevisionAndReplay(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	fixture := createGoldenExactPlanAuthorityFixture(ctx, t)
	scopeID := uuid.New()
	revisionID := uuid.New()
	commandID := uuid.New()
	digest := bytes.Repeat([]byte{1}, 32)
	payload := []byte(fmt.Sprintf(
		`{"schema":"golden-aggregate-v1","kind":"state","revision_id":"%s","revision_number":"1","payload_digest":"%x","document":{"state":"ready"}}`,
		revisionID,
		digest,
	))

	_, err := sharedPool.Exec(ctx, `
		INSERT INTO golden_repository_scopes (
			id, aggregate_kind, tournament_id, roster_id, group_id, group_revision_id,
			plan_set_id, attempt_id, wave_id, assignment_id, snapshot_id, task_id, created_at
		)
		VALUES ($1, 'state', $2, $3, $4, $5, NULL, NULL, NULL, NULL, NULL, NULL, $6)`,
		scopeID,
		fixture.golden.tournamentID,
		fixture.golden.rosterID,
		fixture.groupID,
		fixture.groupRevisionID,
		fixture.golden.createdAt,
	)
	require.NoError(t, err)

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO golden_repository_revisions (
			scope_id, revision_id, revision_number, previous_revision_id,
			payload, payload_digest, created_at
		)
		VALUES ($1, $2, 1, NULL, $3::JSONB, $4, $5)`,
		scopeID,
		revisionID,
		payload,
		digest,
		fixture.golden.createdAt,
	)
	require.NoError(t, err)

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO golden_repository_heads (
			scope_id, revision_id, revision_number, payload_digest, updated_at
		)
		VALUES ($1, $2, 1, $3, $4)`,
		scopeID,
		revisionID,
		digest,
		fixture.golden.createdAt,
	)
	require.NoError(t, err)

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO golden_repository_command_journal (
			scope_id, tournament_id, command_id, command_kind, command_digest,
			result_revision_id, occurred_at, created_at
		)
		VALUES ($1, $2, $3, 'state_ready', $4, $5, $6, $6)`,
		scopeID,
		fixture.golden.tournamentID,
		commandID,
		digest,
		revisionID,
		fixture.golden.createdAt,
	)
	require.NoError(t, err)

	t.Run("revision is immutable", func(t *testing.T) {
		_, updateErr := sharedPool.Exec(ctx, `
			UPDATE golden_repository_revisions
			SET payload = '{"schema":"golden-aggregate-v1","kind":"state","revision_id":"00000000-0000-0000-0000-000000000000","revision_number":"2","payload_digest":"0000000000000000000000000000000000000000000000000000000000000000","document":{"state":"changed"}}'::JSONB
			WHERE scope_id = $1 AND revision_id = $2`, scopeID, revisionID)
		require.ErrorContains(t, updateErr, "immutable")
	})

	t.Run("stale compare-and-set changes no head", func(t *testing.T) {
		result, updateErr := sharedPool.Exec(ctx, `
			UPDATE golden_repository_heads
			SET updated_at = updated_at
			WHERE scope_id = $1 AND revision_id = $2 AND payload_digest = $3`,
			scopeID,
			revisionID,
			bytes.Repeat([]byte{2}, 32),
		)
		require.NoError(t, updateErr)
		require.EqualValues(t, 0, result.RowsAffected())
	})

	t.Run("journal cannot cross scope", func(t *testing.T) {
		otherScopeID := uuid.New()
		_, createErr := sharedPool.Exec(ctx, `
			INSERT INTO golden_repository_scopes (
				id, aggregate_kind, tournament_id, roster_id, group_id, group_revision_id,
				plan_set_id, attempt_id, wave_id, assignment_id, snapshot_id, task_id, created_at
			)
			VALUES ($1, 'prestart', $2, $3, $4, $5, NULL, NULL, NULL, NULL, NULL, NULL, $6)`,
			otherScopeID,
			fixture.golden.tournamentID,
			fixture.golden.rosterID,
			fixture.groupID,
			fixture.groupRevisionID,
			fixture.golden.createdAt,
		)
		require.NoError(t, createErr)

		_, journalErr := sharedPool.Exec(ctx, `
			INSERT INTO golden_repository_command_journal (
				scope_id, tournament_id, command_id, command_kind, command_digest,
				result_revision_id, occurred_at, created_at
			)
			VALUES ($1, $2, $3, 'prestart_pause', $4, $5, $6, $6)`,
			otherScopeID,
			fixture.golden.tournamentID,
			uuid.New(),
			digest,
			revisionID,
			fixture.golden.createdAt,
		)
		require.Error(t, journalErr)
	})
}
