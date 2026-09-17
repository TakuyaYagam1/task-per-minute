//go:build integration

// Package pauseseed provides reusable reconnect pause fixtures.
package pauseseed

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Input identifies a pause and the participant presence snapshot it captures.
// Nullable IDs preserve root and nested pause shapes.
type Input struct {
	TournamentID    uuid.UUID
	RosterID        uuid.UUID
	SeriesID        uuid.UUID
	ParticipantIDs  []uuid.UUID
	ScopeKind       string
	ScopeID         uuid.UUID
	GameAttemptID   *uuid.UUID
	ParentPauseID   *uuid.UUID
	Depth           int
	Reason          string
	PausedFromState string
	PausedAt        time.Time
	SlotLimit       int
}

// Seed contains the pause and active revision identities.
type Seed struct {
	PauseID    uuid.UUID
	RevisionID uuid.UUID
}

// CreatePause persists the pause aggregate and its reconnect state in one
// transaction, preserving the fixture's raw statements and nullable links.
func CreatePause(ctx context.Context, pool *pgxpool.Pool, input Input) (Seed, error) {
	if pool == nil {
		return Seed{}, fmt.Errorf("pause seed: nil pool")
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return Seed{}, fmt.Errorf("pause seed: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	pauseID := uuid.New()
	revisionID := uuid.New()
	_, err = tx.Exec(
		ctx, `
		INSERT INTO pauses (
			id, tournament_id, roster_id, scope_kind, scope_id,
			series_id, game_attempt_id, parent_pause_id, depth,
			reason, paused_from_state, current_revision_id,
			started_at, created_at, updated_at
		)
		VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9,
			$10, $11, $12,
			$13, $13, $13
		)`,
		pauseID,
		input.TournamentID,
		input.RosterID,
		input.ScopeKind,
		input.ScopeID,
		input.SeriesID,
		input.GameAttemptID,
		input.ParentPauseID,
		input.Depth,
		input.Reason,
		input.PausedFromState,
		revisionID,
		input.PausedAt,
	)
	if err != nil {
		return Seed{}, fmt.Errorf("pause seed: create pause: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO pause_revisions (
			id, pause_id, revision_number, state, created_at
		)
		VALUES ($1, $2, 1, 'active', $3)`, revisionID, pauseID, input.PausedAt)
	if err != nil {
		return Seed{}, fmt.Errorf("pause seed: create pause revision: %w", err)
	}

	for i, participantID := range input.ParticipantIDs {
		_, err = tx.Exec(
			ctx, `
			INSERT INTO pause_presence_snapshots (
				pause_id, roster_id, series_id, participant_id,
				presence_state, presence_epoch, presence_revision,
				captured_at, created_at
			)
			VALUES ($1, $2, $3, $4, 'connected', 1, 1, $5, $5)`,
			pauseID,
			input.RosterID,
			input.SeriesID,
			participantID,
			input.PausedAt,
		)
		if err != nil {
			return Seed{}, fmt.Errorf("pause seed: create presence snapshot %d: %w", i, err)
		}

		_, err = tx.Exec(
			ctx, `
			INSERT INTO reconnect_slot_counters (
				pause_id, roster_id, participant_id, slot_limit,
				created_at, updated_at
			)
			VALUES ($1, $2, $3, $4, $5, $5)`,
			pauseID,
			input.RosterID,
			participantID,
			input.SlotLimit,
			input.PausedAt,
		)
		if err != nil {
			return Seed{}, fmt.Errorf("pause seed: create slot counter %d: %w", i, err)
		}
	}

	if input.GameAttemptID != nil {
		_, err = tx.Exec(
			ctx, `
			INSERT INTO pause_clocks (
				pause_id, game_attempt_id, original_deadline,
				frozen_at, frozen_remaining_ms, created_at, updated_at
			)
			VALUES ($1, $2, $3, $4, 300000, $4, $4)`,
			pauseID,
			*input.GameAttemptID,
			input.PausedAt.Add(5*time.Minute),
			input.PausedAt,
		)
		if err != nil {
			return Seed{}, fmt.Errorf("pause seed: create pause clock: %w", err)
		}

		_, err = tx.Exec(ctx, `
			UPDATE game_attempts
			SET state = 'paused', revision = revision + 1, updated_at = $2
			WHERE id = $1`, *input.GameAttemptID, input.PausedAt)
		if err != nil {
			return Seed{}, fmt.Errorf("pause seed: pause game attempt: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return Seed{}, fmt.Errorf("pause seed: commit transaction: %w", err)
	}
	return Seed{PauseID: pauseID, RevisionID: revisionID}, nil
}
