//go:build integration

// Package gameseed provides reusable game graph fixtures for integration tests.
package gameseed

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	gamerepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/execution/game"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

// SlotInput identifies a game slot to create. A zero CreatedAt uses the
// current UTC time, which keeps the legacy slot-only fixture convenient while
// allowing combined seeds to use one explicit lifecycle timestamp.
type SlotInput struct {
	SeriesID   uuid.UUID
	RosterID   uuid.UUID
	SlotNumber int
	Category   domain.Category
	CreatedAt  time.Time
}

// ActiveAttemptInput identifies a slot and its first active attempt.
type ActiveAttemptInput struct {
	SeriesID   uuid.UUID
	RosterID   uuid.UUID
	SlotNumber int
	Category   domain.Category
	CreatedAt  time.Time
}

// ActiveAttemptSeed contains the durable identities created by
// CreateActiveAttempt.
type ActiveAttemptSeed struct {
	SlotID    uuid.UUID
	AttemptID uuid.UUID
}

// CreateSlot persists one empty game slot through the production game
// adapter. The write remains an independent statement, matching the legacy
// migration fixture's slot helper.
func CreateSlot(ctx context.Context, pool *pgxpool.Pool, input SlotInput) (uuid.UUID, error) {
	if pool == nil {
		return uuid.Nil, fmt.Errorf("game seed: nil pool")
	}
	if input.SeriesID == uuid.Nil || input.RosterID == uuid.Nil || input.SlotNumber < 1 {
		return uuid.Nil, fmt.Errorf("game seed: invalid slot identity")
	}

	createdAt := input.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC().Truncate(time.Microsecond)
	}
	if createdAt.Location() != time.UTC {
		return uuid.Nil, fmt.Errorf("game seed: slot timestamp must be UTC")
	}

	slotID := uuid.New()
	_, err := gamerepo.NewGamePostgres(postgres.NewTxManager(pool)).CreateSlot(
		ctx,
		gamerepo.GameSlotInput{
			Slot: domain.GameSlot{
				ID:          slotID,
				SeriesID:    input.SeriesID,
				Position:    input.SlotNumber,
				Category:    input.Category,
				ScoreBefore: domain.SeriesScore{},
			},
			RosterID:  input.RosterID,
			CreatedAt: createdAt,
		},
	)
	if err != nil {
		return uuid.Nil, fmt.Errorf("game seed: create slot: %w", err)
	}
	return slotID, nil
}

// CreateAttempt appends the first active attempt to an existing slot through
// the production game adapter. The write remains independent from slot
// creation, preserving the legacy two-step fixture contract.
func CreateAttempt(
	ctx context.Context,
	pool *pgxpool.Pool,
	slotID uuid.UUID,
	seriesID uuid.UUID,
	rosterID uuid.UUID,
	createdAt time.Time,
) (uuid.UUID, error) {
	if pool == nil {
		return uuid.Nil, fmt.Errorf("game seed: nil pool")
	}
	if slotID == uuid.Nil || seriesID == uuid.Nil || rosterID == uuid.Nil {
		return uuid.Nil, fmt.Errorf("game seed: invalid attempt identity")
	}
	if createdAt.IsZero() || createdAt.Location() != time.UTC {
		return uuid.Nil, fmt.Errorf("game seed: attempt timestamp must be UTC")
	}

	attemptID := uuid.New()
	startedAt := createdAt
	_, err := gamerepo.NewGamePostgres(postgres.NewTxManager(pool)).AppendAttempt(
		ctx,
		gamerepo.GameAttemptInput{
			Game: domain.Game{
				ID:        attemptID,
				SlotID:    slotID,
				AttemptNo: 1,
				State:     domain.GameStateActive,
			},
			SeriesID:  seriesID,
			RosterID:  rosterID,
			CreatedAt: createdAt,
			StartedAt: &startedAt,
		},
	)
	if err != nil {
		return uuid.Nil, fmt.Errorf("game seed: create active attempt: %w", err)
	}
	return attemptID, nil
}

// CreateActiveAttempt creates an empty slot and its first active attempt as
// two independent production-adapter writes, returning both identities.
func CreateActiveAttempt(
	ctx context.Context,
	pool *pgxpool.Pool,
	input ActiveAttemptInput,
) (ActiveAttemptSeed, error) {
	if input.CreatedAt.IsZero() || input.CreatedAt.Location() != time.UTC {
		return ActiveAttemptSeed{}, fmt.Errorf("game seed: attempt timestamp must be UTC")
	}

	slotID, err := CreateSlot(ctx, pool, SlotInput{
		SeriesID:   input.SeriesID,
		RosterID:   input.RosterID,
		SlotNumber: input.SlotNumber,
		Category:   input.Category,
		CreatedAt:  input.CreatedAt,
	})
	if err != nil {
		return ActiveAttemptSeed{}, err
	}
	attemptID, err := CreateAttempt(ctx, pool, slotID, input.SeriesID, input.RosterID, input.CreatedAt)
	if err != nil {
		return ActiveAttemptSeed{}, err
	}
	return ActiveAttemptSeed{SlotID: slotID, AttemptID: attemptID}, nil
}
