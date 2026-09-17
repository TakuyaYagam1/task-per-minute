//go:build integration

// Package seriesseed provides reusable series fixtures for integration tests.
package seriesseed

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Input identifies the two-participant series and its initial score source.
// A zero CreatedAt uses the current UTC time, matching the root fixture.
type Input struct {
	TournamentID   uuid.UUID
	RosterID       uuid.UUID
	ParticipantIDs []uuid.UUID
	Format         string
	CreatedAt      time.Time
}

// CreateSeries creates the series and its initial score projection in one
// transaction, preserving the fixture's deferred-constraint boundary.
func CreateSeries(ctx context.Context, pool *pgxpool.Pool, input Input) (uuid.UUID, error) {
	if pool == nil {
		return uuid.Nil, fmt.Errorf("series seed: nil pool")
	}
	if len(input.ParticipantIDs) != 2 {
		return uuid.Nil, fmt.Errorf("series seed: expected two participants, got %d", len(input.ParticipantIDs))
	}

	createdAt := input.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC().Truncate(time.Microsecond)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("series seed: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err = tx.Exec(ctx, "SET CONSTRAINTS ALL DEFERRED"); err != nil {
		return uuid.Nil, fmt.Errorf("series seed: defer constraints: %w", err)
	}

	sourceProjectionID, sourceProjectionRevision, err := genesisProjection(
		ctx,
		tx,
		input.TournamentID,
		input.RosterID,
		createdAt,
	)
	if err != nil {
		return uuid.Nil, err
	}

	var seriesID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO series (
			tournament_id, roster_id, first_participant_id, second_participant_id, format,
			created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $6)
		RETURNING id`,
		input.TournamentID,
		input.RosterID,
		input.ParticipantIDs[0],
		input.ParticipantIDs[1],
		input.Format,
		createdAt,
	).Scan(&seriesID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("series seed: create series: %w", err)
	}

	genesisScoreRevisionID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO series_score_revisions (
			id, tournament_id, roster_id, series_id,
			revision_number, operation, command_id, actor_kind,
			source_projection_revision_id, source_projection_revision,
			first_participant_wins, second_participant_wins, created_at
		)
		VALUES ($1, $2, $3, $4, 1, 'initialize', $5, 'server', $6, $7, 0, 0, $8)`,
		genesisScoreRevisionID,
		input.TournamentID,
		input.RosterID,
		seriesID,
		uuid.New(),
		sourceProjectionID,
		sourceProjectionRevision,
		createdAt,
	)
	if err != nil {
		return uuid.Nil, fmt.Errorf("series seed: create score revision: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO series_score_heads (
			series_id, roster_id, current_revision_id, updated_at
		)
		VALUES ($1, $2, $3, $4)`, seriesID, input.RosterID, genesisScoreRevisionID, createdAt)
	if err != nil {
		return uuid.Nil, fmt.Errorf("series seed: create score head: %w", err)
	}

	_, err = tx.Exec(ctx, `
		UPDATE series
		SET current_score_revision_id = $2,
			updated_at = $3
		WHERE id = $1`, seriesID, genesisScoreRevisionID, createdAt)
	if err != nil {
		return uuid.Nil, fmt.Errorf("series seed: update series score: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("series seed: commit transaction: %w", err)
	}
	return seriesID, nil
}

func genesisProjection(
	ctx context.Context,
	tx pgx.Tx,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	createdAt time.Time,
) (uuid.UUID, int64, error) {
	var projectionID uuid.UUID
	var revisionNumber int64
	err := tx.QueryRow(ctx, `
		SELECT id, revision_number
		FROM projection_revisions
		WHERE tournament_id = $1
			AND roster_id = $2
		ORDER BY revision_number DESC
		LIMIT 1
		FOR KEY SHARE`, tournamentID, rosterID).Scan(&projectionID, &revisionNumber)
	if err == nil {
		return projectionID, revisionNumber, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, 0, fmt.Errorf("series seed: load genesis projection: %w", err)
	}

	cutoffID := uuid.New()
	projectionID = uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO projection_cutoffs (
			id, tournament_id, roster_id, sequence_number, source_kind,
			reason, cutoff_at, created_at
		)
		VALUES ($1, $2, $3, 1, 'initial', 'series genesis source', $4, $4)`,
		cutoffID,
		tournamentID,
		rosterID,
		createdAt,
	)
	if err != nil {
		return uuid.Nil, 0, fmt.Errorf("series seed: create genesis cutoff: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO projection_revisions (
			id, tournament_id, roster_id, revision_number, cutoff_id, created_at
		)
		VALUES ($1, $2, $3, 1, $4, $5)`,
		projectionID,
		tournamentID,
		rosterID,
		cutoffID,
		createdAt,
	)
	if err != nil {
		return uuid.Nil, 0, fmt.Errorf("series seed: create genesis projection: %w", err)
	}
	return projectionID, 1, nil
}
