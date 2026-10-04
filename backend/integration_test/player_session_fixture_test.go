//go:build integration

package integration_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// ensureVerifiedPlayerAccount links legacy tournament fixtures to the account
// required by the real HTTP and WebSocket session readers.
func ensureVerifiedPlayerAccount(ctx context.Context, t *testing.T, playerID uuid.UUID) {
	t.Helper()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO player_accounts (
			player_id, username, username_normalized, email, email_normalized,
			password_hash, email_verified_at
		)
		SELECT id, username, lower(username), id::text || '@example.test',
			id::text || '@example.test', 'integration-test-hash', now()
		FROM players WHERE id = $1
		ON CONFLICT (player_id) DO NOTHING`, playerID)
	require.NoError(t, err)
	result, err := sharedPool.Exec(ctx, `
		INSERT INTO player_username_reservations (normalized_username, legacy_count, account_id)
		SELECT username_normalized, 0, id FROM player_accounts WHERE player_id = $1
		ON CONFLICT (normalized_username) DO UPDATE
		SET legacy_count = 0, account_id = EXCLUDED.account_id`, playerID)
	require.NoError(t, err)
	require.EqualValues(t, 1, result.RowsAffected())
}
