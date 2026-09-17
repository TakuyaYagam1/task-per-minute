//go:build integration

// Package tournamentseed provides reusable tournament and roster fixtures for integration tests.
package tournamentseed

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RosterSeed contains the roster identity and revision returned by PostgreSQL.
type RosterSeed struct {
	ID       uuid.UUID
	Revision int64
}

// CreateTournament inserts a tournament using database defaults.
func CreateTournament(ctx context.Context, pool *pgxpool.Pool) (uuid.UUID, error) {
	if pool == nil {
		return uuid.Nil, fmt.Errorf("tournament seed: nil pool")
	}

	var id uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO tournaments DEFAULT VALUES
		RETURNING id`).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("tournament seed: create tournament: %w", err)
	}
	return id, nil
}

// CreateRoster inserts a roster with only its tournament relationship and
// returns the database-assigned revision for the root fixture to assert.
func CreateRoster(ctx context.Context, pool *pgxpool.Pool, tournamentID uuid.UUID) (RosterSeed, error) {
	if pool == nil {
		return RosterSeed{}, fmt.Errorf("tournament seed: nil pool")
	}

	var seed RosterSeed
	err := pool.QueryRow(ctx, `
		INSERT INTO rosters (tournament_id)
		VALUES ($1)
		RETURNING id, revision`, tournamentID).Scan(&seed.ID, &seed.Revision)
	if err != nil {
		return RosterSeed{}, fmt.Errorf("tournament seed: create roster: %w", err)
	}
	return seed, nil
}

// CreatePlayers inserts count players using the caller prefix, a short random
// suffix, and the stable loop index in each username.
func CreatePlayers(ctx context.Context, pool *pgxpool.Pool, prefix string, count int) ([]uuid.UUID, error) {
	if pool == nil {
		return nil, fmt.Errorf("tournament seed: nil pool")
	}
	if count < 0 {
		return nil, fmt.Errorf("tournament seed: negative player count")
	}

	ids := make([]uuid.UUID, count)
	for i := range ids {
		username := fmt.Sprintf("%s_%s_%d", prefix, uuid.NewString()[:8], i)
		if err := pool.QueryRow(ctx, `
			INSERT INTO players (username)
			VALUES ($1)
			RETURNING id`, username).Scan(&ids[i]); err != nil {
			return nil, fmt.Errorf("tournament seed: create player %d: %w", i, err)
		}
	}
	return ids, nil
}
