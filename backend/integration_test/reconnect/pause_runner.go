//go:build integration

package reconnect

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// RunReconnectPauseAtomicity runs the pause/reconnect transaction scenarios
// against the caller's already-initialized integration pool. The explicit
// pool boundary lets aggregate integration tests reuse this capability without
// importing root integration_test fixtures.
func RunReconnectPauseAtomicity(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	require.NotNil(t, pool)

	migrationPoolMu.Lock()
	previousShared := sharedPool
	previousMigration := migrationPool
	sharedPool = pool
	migrationPool = pool
	defer func() {
		sharedPool = previousShared
		migrationPool = previousMigration
		migrationPoolMu.Unlock()
	}()

	testReconnectContinuationPauseCommit(t)
	testReconnectContinuationPauseConcurrency(t)
}

// RunReconnectWaveMembershipFence runs the wave membership scenarios against
// an explicit integration pool for callers outside this test package.
func RunReconnectWaveMembershipFence(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	require.NotNil(t, pool)

	migrationPoolMu.Lock()
	previousShared := sharedPool
	previousMigration := migrationPool
	sharedPool = pool
	migrationPool = pool
	defer func() {
		sharedPool = previousShared
		migrationPool = previousMigration
		migrationPoolMu.Unlock()
	}()

	runReconnectContinuationWaveMembershipFence(t)
}
