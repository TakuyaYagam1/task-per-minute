//go:build integration

// Package waveseed provides reusable wave fixtures for integration tests.
package waveseed

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Input identifies a wave and its participant roster.
type Input struct {
	TournamentID   uuid.UUID
	RosterID       uuid.UUID
	ParticipantIDs []uuid.UUID
	CreatedAt      time.Time
}

// Seed contains the wave and initial wave revision identities.
type Seed struct {
	WaveID     uuid.UUID
	RevisionID uuid.UUID
}

// CreateWave writes a wave, its members, and readiness rows as independent
// statements. It intentionally does not add a transaction so partial writes
// retain the legacy fixture behavior.
func CreateWave(ctx context.Context, pool *pgxpool.Pool, input Input) (Seed, error) {
	if pool == nil {
		return Seed{}, fmt.Errorf("wave seed: nil pool")
	}
	if len(input.ParticipantIDs) < 2 {
		return Seed{}, fmt.Errorf("wave seed: expected at least two participants, got %d", len(input.ParticipantIDs))
	}

	waveID := uuid.New()
	revisionID := uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO waves (
			id, tournament_id, roster_id, revision_id, replaces_wave_id,
			created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $6)`,
		waveID,
		input.TournamentID,
		input.RosterID,
		revisionID,
		nil,
		input.CreatedAt,
	)
	if err != nil {
		return Seed{}, fmt.Errorf("wave seed: create wave: %w", err)
	}

	for i, participantID := range input.ParticipantIDs {
		_, err = pool.Exec(ctx, `
			INSERT INTO wave_members (
				wave_id, roster_id, participant_id, created_at
			)
			VALUES ($1, $2, $3, $4)`, waveID, input.RosterID, participantID, input.CreatedAt)
		if err != nil {
			return Seed{}, fmt.Errorf("wave seed: create member %d: %w", i, err)
		}

		_, err = pool.Exec(ctx, `
			INSERT INTO wave_readiness (
				wave_id, roster_id, participant_id, created_at, updated_at
			)
			VALUES ($1, $2, $3, $4, $4)`, waveID, input.RosterID, participantID, input.CreatedAt)
		if err != nil {
			return Seed{}, fmt.Errorf("wave seed: create readiness %d: %w", i, err)
		}
	}
	return Seed{WaveID: waveID, RevisionID: revisionID}, nil
}
