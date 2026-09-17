//go:build integration

package resultaudit

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// LockInput identifies the planned series and the timestamp of its durable
// transition to locked state.
type LockInput struct {
	Scope    Scope
	LockedAt time.Time
}

// LockSeries locks the planned series and its score head, returning the
// revision that becomes the parent of the first result score revision.
func LockSeries(ctx context.Context, pool *pgxpool.Pool, input LockInput) (Fixture, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return Fixture{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		initialScoreRevisionID uuid.UUID
		revisionNumber         int64
	)
	err = tx.QueryRow(ctx, `
		SELECT score_head.current_revision_id, score_revision.revision_number
		FROM series
		INNER JOIN series_score_heads AS score_head
			ON score_head.series_id = series.id
			AND score_head.roster_id = series.roster_id
		INNER JOIN series_score_revisions AS score_revision
			ON score_revision.id = score_head.current_revision_id
			AND score_revision.series_id = series.id
			AND score_revision.roster_id = series.roster_id
		WHERE series.id = $1
			AND series.tournament_id = $2
			AND series.roster_id = $3
			AND series.state = 'planned'
			AND series.current_score_revision_id = score_head.current_revision_id
			AND score_revision.revision_number = 1
			AND score_revision.operation = 'initialize'
			AND score_revision.result_event_id IS NULL
			AND score_revision.previous_revision_id IS NULL
			AND score_revision.command_attempt_id IS NULL
			AND score_revision.first_participant_wins = 0
			AND score_revision.second_participant_wins = 0
		FOR UPDATE OF series, score_head`,
		input.Scope.SeriesID,
		input.Scope.TournamentID,
		input.Scope.RosterID,
	).Scan(&initialScoreRevisionID, &revisionNumber)
	if err != nil {
		return Fixture{}, err
	}
	if revisionNumber != 1 {
		return Fixture{}, fmt.Errorf("initial score revision number = %d, want 1", revisionNumber)
	}

	_, err = tx.Exec(ctx, `
		UPDATE series
		SET state = 'locked',
			revision = revision + 1,
			updated_at = $2
		WHERE id = $1`, input.Scope.SeriesID, input.LockedAt)
	if err != nil {
		return Fixture{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Fixture{}, err
	}
	return Fixture{
		Scope:                  input.Scope,
		InitialScoreRevisionID: initialScoreRevisionID,
		LockedAt:               input.LockedAt,
	}, nil
}

// BeginLockProbe starts a transaction with the same short lock timeout used
// by the migration lock-order tests. The caller owns rollback.
func BeginLockProbe(ctx context.Context, pool *pgxpool.Pool) (pgx.Tx, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, "SET LOCAL lock_timeout = '100ms'"); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}
