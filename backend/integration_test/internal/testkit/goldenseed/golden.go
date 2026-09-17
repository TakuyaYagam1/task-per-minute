//go:build integration

// Package goldenseed provides small Golden migration row seeds for integration
// tests. It accepts explicit database inputs and does not depend on test
// handles or the root integration package.
package goldenseed

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AttemptInput identifies one Golden attempt row. Nullable SQL values remain
// any so callers preserve the database driver's existing null semantics.
type AttemptInput struct {
	TournamentID      uuid.UUID
	RosterID          uuid.UUID
	AttemptNumber     int
	PreviousAttemptID any
	CreatedAt         time.Time
}

// MembershipInput identifies one Golden membership row. Nullable values are
// intentionally represented as any to preserve the original Exec contract.
type MembershipInput struct {
	AttemptID       uuid.UUID
	TournamentID    uuid.UUID
	RosterID        uuid.UUID
	ParticipantID   uuid.UUID
	SelectionKind   string
	ReservePosition any
	SelectedAt      time.Time
	ReadyAt         any
	NoShowAt        any
	ExcludedAt      any
	ExclusionReason any
}

// CreateAttempt inserts exactly one Golden attempt row in one statement.
func CreateAttempt(ctx context.Context, pool *pgxpool.Pool, input AttemptInput) (uuid.UUID, error) {
	id := uuid.New()
	_, err := pool.Exec(
		ctx, `
		INSERT INTO golden_attempts (
			id, tournament_id, roster_id, attempt_number,
			previous_attempt_id, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		id,
		input.TournamentID,
		input.RosterID,
		input.AttemptNumber,
		input.PreviousAttemptID,
		input.CreatedAt,
	)
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// CreateMembership inserts exactly one Golden membership row in one statement.
func CreateMembership(ctx context.Context, pool *pgxpool.Pool, input MembershipInput) (uuid.UUID, error) {
	id := uuid.New()
	_, err := pool.Exec(
		ctx, `
		INSERT INTO golden_memberships (
			id, attempt_id, tournament_id, roster_id, participant_id,
			selection_kind, reserve_position, selected_at,
			ready_at, no_show_at, excluded_at, exclusion_reason
		)
		VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8,
			$9, $10, $11, $12
		)`,
		id,
		input.AttemptID,
		input.TournamentID,
		input.RosterID,
		input.ParticipantID,
		input.SelectionKind,
		input.ReservePosition,
		input.SelectedAt,
		input.ReadyAt,
		input.NoShowAt,
		input.ExcludedAt,
		input.ExclusionReason,
	)
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}
