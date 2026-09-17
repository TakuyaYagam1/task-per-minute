//go:build integration

// Package swissseed provides reusable Swiss participant and pairing fixtures.
package swissseed

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CreateParticipants inserts checked-in participants with one-based seeds.
func CreateParticipants(
	ctx context.Context,
	pool *pgxpool.Pool,
	rosterID uuid.UUID,
	playerIDs []uuid.UUID,
) ([]uuid.UUID, error) {
	if pool == nil {
		return nil, fmt.Errorf("swiss seed: nil pool")
	}

	participantIDs := make([]uuid.UUID, len(playerIDs))
	for i, playerID := range playerIDs {
		if err := pool.QueryRow(ctx, `
			INSERT INTO participants (roster_id, player_id, seed, attendance)
			VALUES ($1, $2, $3, 'checked_in')
			RETURNING id`, rosterID, playerID, i+1).Scan(&participantIDs[i]); err != nil {
			return nil, fmt.Errorf("swiss seed: create participant %d: %w", i, err)
		}
	}
	return participantIDs, nil
}

// CreatePairing inserts a Swiss pairing with an optional repeat override.
func CreatePairing(
	ctx context.Context,
	pool *pgxpool.Pool,
	roundID uuid.UUID,
	rosterID uuid.UUID,
	slotNumber int,
	overrideID *uuid.UUID,
) (uuid.UUID, error) {
	if pool == nil {
		return uuid.Nil, fmt.Errorf("swiss seed: nil pool")
	}

	var override any
	if overrideID != nil {
		override = *overrideID
	}
	var pairingID uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO swiss_pairings (round_id, roster_id, slot_number, repeat_override_id)
		VALUES ($1, $2, $3, $4)
		RETURNING id`, roundID, rosterID, slotNumber, override).Scan(&pairingID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("swiss seed: create pairing: %w", err)
	}
	return pairingID, nil
}

// AddPairingMember inserts one participant seat into a Swiss pairing.
func AddPairingMember(
	ctx context.Context,
	pool *pgxpool.Pool,
	pairingID uuid.UUID,
	roundID uuid.UUID,
	rosterID uuid.UUID,
	seat int,
	participantID uuid.UUID,
) error {
	if pool == nil {
		return fmt.Errorf("swiss seed: nil pool")
	}

	_, err := pool.Exec(ctx, `
		INSERT INTO swiss_pairing_members (
			pairing_id, round_id, roster_id, seat, participant_id
		)
		VALUES ($1, $2, $3, $4, $5)`, pairingID, roundID, rosterID, seat, participantID)
	if err != nil {
		return fmt.Errorf("swiss seed: add pairing member: %w", err)
	}
	return nil
}
