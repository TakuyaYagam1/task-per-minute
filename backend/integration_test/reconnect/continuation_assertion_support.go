//go:build integration

package reconnect

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func assertPauseResumeRejected(
	ctx context.Context,
	tb testing.TB,
	pauseID uuid.UUID,
	previousRevisionID uuid.UUID,
	resumedAt time.Time,
	expected string,
) {
	tb.Helper()

	tx, err := sharedPool.Begin(ctx)
	require.NoError(tb, err)
	defer func() { _ = tx.Rollback(ctx) }()
	revisionID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO pause_revisions (
			id, pause_id, previous_revision_id, revision_number,
			state, transition_reason, created_at
		)
		VALUES ($1, $2, $3, 2, 'resumed', 'presence restored', $4)`,
		revisionID,
		pauseID,
		previousRevisionID,
		resumedAt,
	)
	require.NoError(tb, err)
	_, err = tx.Exec(ctx, `
		UPDATE pauses
		SET state = 'resumed',
			current_revision_id = $2,
			revision = revision + 1,
			resolved_at = $3,
			updated_at = $3
		WHERE id = $1`, pauseID, revisionID, resumedAt)
	require.ErrorContains(tb, err, expected)
}

func setReconnectDeadlockTimeouts(ctx context.Context, executor reconnectExecutor) error {
	for _, statement := range []string{
		"SET LOCAL deadlock_timeout = '100ms'",
		"SET LOCAL lock_timeout = '2s'",
		"SET LOCAL statement_timeout = '3s'",
	} {
		if _, err := executor.Exec(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func waitReconnectBackendLock(ctx context.Context, tb testing.TB, backendPID int) {
	tb.Helper()

	require.Eventually(tb, func() bool {
		var waitEventType string
		err := sharedPool.QueryRow(ctx, `
			SELECT COALESCE(wait_event_type, '')
			FROM pg_stat_activity
			WHERE pid = $1`, backendPID).Scan(&waitEventType)
		return err == nil && waitEventType == "Lock"
	}, 2*time.Second, 10*time.Millisecond)
}
