//go:build integration

package waveseed

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ReadyWindowSeed contains the ready window and its initial revision identities.
type ReadyWindowSeed struct {
	WindowID   uuid.UUID
	RevisionID uuid.UUID
}

// OpenReadyWindow creates a ready window and advances the wave/readiness rows
// in one transaction, preserving the migration fixture's ordering.
func OpenReadyWindow(
	ctx context.Context,
	pool *pgxpool.Pool,
	waveID uuid.UUID,
	rosterID uuid.UUID,
	openedAt time.Time,
	deadline time.Time,
) (ReadyWindowSeed, error) {
	if pool == nil {
		return ReadyWindowSeed{}, fmt.Errorf("wave seed: nil pool")
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return ReadyWindowSeed{}, fmt.Errorf("wave seed: begin ready window transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	windowID := uuid.New()
	revisionID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO ready_windows (
			id, wave_id, roster_id, revision_id,
			opened_at, deadline, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $5)`,
		windowID, waveID, rosterID, revisionID, openedAt, deadline)
	if err != nil {
		return ReadyWindowSeed{}, fmt.Errorf("wave seed: create ready window: %w", err)
	}

	_, err = tx.Exec(ctx, `
		UPDATE wave_readiness
		SET ready_window_id = $2, revision = revision + 1, updated_at = $3
		WHERE wave_id = $1 AND ready_window_id IS NULL`, waveID, windowID, openedAt)
	if err != nil {
		return ReadyWindowSeed{}, fmt.Errorf("wave seed: attach ready window: %w", err)
	}

	_, err = tx.Exec(ctx, `
		UPDATE waves
		SET state = 'ready_window_open', revision = revision + 1, updated_at = $2
		WHERE id = $1`, waveID, openedAt)
	if err != nil {
		return ReadyWindowSeed{}, fmt.Errorf("wave seed: open wave: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return ReadyWindowSeed{}, fmt.Errorf("wave seed: commit ready window transaction: %w", err)
	}
	return ReadyWindowSeed{WindowID: windowID, RevisionID: revisionID}, nil
}

// MarkReady updates one participant readiness row and returns the affected
// row count so the root wrapper can keep its CAS assertion.
func MarkReady(
	ctx context.Context,
	pool *pgxpool.Pool,
	windowID uuid.UUID,
	waveID uuid.UUID,
	rosterID uuid.UUID,
	participantID uuid.UUID,
	readyAt time.Time,
) (int64, error) {
	if pool == nil {
		return 0, fmt.Errorf("wave seed: nil pool")
	}

	result, err := pool.Exec(ctx, `
		UPDATE wave_readiness
		SET ready = true,
			ready_at = $5,
			revision = revision + 1,
			updated_at = $5
		WHERE ready_window_id = $1
			AND wave_id = $2
			AND roster_id = $3
			AND participant_id = $4
			AND revision = 2
			AND NOT ready`, windowID, waveID, rosterID, participantID, readyAt)
	if err != nil {
		return 0, fmt.Errorf("wave seed: mark readiness: %w", err)
	}
	return result.RowsAffected(), nil
}
