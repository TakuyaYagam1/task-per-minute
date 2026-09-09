//go:build integration && capacity

package integration_test

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

func runTournamentCapacityDisconnect(
	ctx context.Context,
	fixture reconnectMigrationFixture,
	participantID uuid.UUID,
	disconnectedAt time.Time,
) (uuid.UUID, error) {
	intervalID := uuid.New()
	tx, err := sharedPool.Begin(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("begin disconnect: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `
		SELECT 1
		FROM pauses
		WHERE id = $1
		FOR UPDATE`, fixture.gamePauseID); err != nil {
		return uuid.Nil, fmt.Errorf("lock pause: %w", err)
	}
	presenceResult, err := tx.Exec(ctx, `
		UPDATE presence_states
		SET state = 'disconnected',
			presence_epoch = presence_epoch + 1,
			revision = revision + 1,
			disconnected_at = $3,
			updated_at = $3
		WHERE series_id = $1 AND participant_id = $2`,
		fixture.draft.seriesID,
		participantID,
		disconnectedAt,
	)
	if err != nil || presenceResult.RowsAffected() != 1 {
		if err != nil {
			return uuid.Nil, fmt.Errorf("update disconnect presence: %w", err)
		}
		return uuid.Nil, fmt.Errorf("update disconnect presence: affected=%d", presenceResult.RowsAffected())
	}
	var presenceEpoch int64
	if err = tx.QueryRow(ctx, `
		SELECT presence_epoch
		FROM presence_states
		WHERE series_id = $1 AND participant_id = $2`,
		fixture.draft.seriesID,
		participantID,
	).Scan(&presenceEpoch); err != nil {
		return uuid.Nil, fmt.Errorf("load disconnect epoch: %w", err)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO reconnect_intervals (
			id, pause_id, roster_id, series_id, game_attempt_id,
			participant_id, presence_epoch, interval_number,
			opened_at, deadline_at, created_at, updated_at
		)
		VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, 1,
			$8, $9, $8, $8
		)`,
		intervalID,
		fixture.gamePauseID,
		fixture.draft.rosterID,
		fixture.draft.seriesID,
		fixture.attemptID,
		participantID,
		presenceEpoch,
		disconnectedAt,
		disconnectedAt.Add(time.Minute),
	); err != nil {
		return uuid.Nil, fmt.Errorf("insert reconnect interval: %w", err)
	}
	counterResult, err := tx.Exec(ctx, `
		UPDATE reconnect_slot_counters
		SET slots_used = slots_used + 1,
			revision = revision + 1,
			updated_at = $3
		WHERE pause_id = $1 AND participant_id = $2`,
		fixture.gamePauseID,
		participantID,
		disconnectedAt,
	)
	if err != nil || counterResult.RowsAffected() != 1 {
		if err != nil {
			return uuid.Nil, fmt.Errorf("update reconnect counter: %w", err)
		}
		return uuid.Nil, fmt.Errorf("update reconnect counter: affected=%d", counterResult.RowsAffected())
	}
	if err = tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("commit disconnect: %w", err)
	}
	return intervalID, nil
}

func runTournamentCapacityReconnect(
	ctx context.Context,
	fixture reconnectMigrationFixture,
	participantID uuid.UUID,
	intervalID uuid.UUID,
	reconnectedAt time.Time,
) error {
	tx, err := sharedPool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin reconnect: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	intervalResult, err := tx.Exec(ctx, `
		UPDATE reconnect_intervals
		SET state = 'reconnected',
			closed_at = $2,
			revision = revision + 1,
			updated_at = $2
		WHERE id = $1`, intervalID, reconnectedAt)
	if err != nil || intervalResult.RowsAffected() != 1 {
		if err != nil {
			return fmt.Errorf("close reconnect interval: %w", err)
		}
		return fmt.Errorf("close reconnect interval: affected=%d", intervalResult.RowsAffected())
	}
	presenceResult, err := tx.Exec(ctx, `
		UPDATE presence_states
		SET state = 'connected',
			presence_epoch = presence_epoch + 1,
			revision = revision + 1,
			connected_at = $3,
			disconnected_at = NULL,
			updated_at = $3
		WHERE series_id = $1 AND participant_id = $2`,
		fixture.draft.seriesID,
		participantID,
		reconnectedAt,
	)
	if err != nil || presenceResult.RowsAffected() != 1 {
		if err != nil {
			return fmt.Errorf("update reconnect presence: %w", err)
		}
		return fmt.Errorf("update reconnect presence: affected=%d", presenceResult.RowsAffected())
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit reconnect: %w", err)
	}
	return nil
}
