//go:build integration

package resultaudit

import (
	"bytes"
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
)

// ProjectionSource identifies the published projection used as immutable
// source evidence for a result commit.
type ProjectionSource struct {
	ID       uuid.UUID
	Revision int64
}

// CurrentPublishedProjection returns the latest published projection for a
// tournament roster pair. Ordering by revision and ID preserves the root
// integration helper's deterministic selection.
func CurrentPublishedProjection(
	ctx context.Context,
	pool *pgxpool.Pool,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
) (ProjectionSource, error) {
	var source ProjectionSource
	err := pool.QueryRow(ctx, `
		SELECT id, revision_number
		FROM projection_revisions
		WHERE tournament_id = $1
			AND roster_id = $2
			AND state = 'published'
		ORDER BY revision_number DESC, id DESC
		LIMIT 1`, tournamentID, rosterID).Scan(&source.ID, &source.Revision)
	return source, err
}

// CommitSolvedResult appends one solved result event and its complete
// official/audit/projection/outbox lineage in a single explicit transaction.
// The SQLC outbox query is intentionally used unchanged because it allocates
// the sequence and inserts the normalized result source atomically.
func CommitSolvedResult(
	ctx context.Context,
	pool *pgxpool.Pool,
	fixture Fixture,
	input CommitInput,
) (CommitResult, error) {
	source, err := CurrentPublishedProjection(
		ctx,
		pool,
		fixture.Scope.TournamentID,
		fixture.Scope.RosterID,
	)
	if err != nil {
		return CommitResult{}, err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return CommitResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `
		UPDATE game_attempts
		SET result_event_sequence = result_event_sequence + 1
		WHERE id = $1`, fixture.Scope.AttemptID)
	if err != nil {
		return CommitResult{}, err
	}

	_, err = tx.Exec(
		ctx, `
		INSERT INTO result_events (
			id, tournament_id, roster_id, series_id, attempt_id,
			submission_event_id, server_sequence, idempotency_key,
			result_state, result_reason, winner_id, occurred_at, created_at
		)
		VALUES (
			$1, $2, $3, $4, $5,
			$6, 1, $7,
			'completed', 'solved', $8, $9, $9
		)`,
		input.IDs.ResultEventID,
		fixture.Scope.TournamentID,
		fixture.Scope.RosterID,
		fixture.Scope.SeriesID,
		fixture.Scope.AttemptID,
		input.SubmissionID,
		input.IDs.ResultEventIdempotencyKey,
		input.WinnerID,
		input.SettledAt,
	)
	if err != nil {
		return CommitResult{}, err
	}

	_, err = tx.Exec(
		ctx, `
		INSERT INTO official_result_revisions (
			id, tournament_id, roster_id, entity_kind, entity_id,
			series_id, game_attempt_id, result_event_id, revision_number,
			command_id, actor_kind, source_projection_revision_id, source_projection_revision,
			result_state, result_reason, winner_id, created_at
		)
		VALUES (
			$1, $2, $3, 'game_attempt', $4,
			$5, $4, $6, 1,
			$7, 'server', $8, $9,
			'completed', 'solved', $10, $11
		)`,
		input.IDs.GameResultRevisionID,
		fixture.Scope.TournamentID,
		fixture.Scope.RosterID,
		fixture.Scope.AttemptID,
		fixture.Scope.SeriesID,
		input.IDs.ResultEventID,
		uuid.New(),
		source.ID,
		source.Revision,
		input.WinnerID,
		input.SettledAt,
	)
	if err != nil {
		return CommitResult{}, err
	}

	_, err = tx.Exec(
		ctx, `
		INSERT INTO official_result_revisions (
			id, tournament_id, roster_id, entity_kind, entity_id,
			series_id, result_event_id, revision_number,
			command_id, actor_kind, source_projection_revision_id, source_projection_revision,
			result_state, result_reason, winner_id, created_at
		)
		VALUES (
			$1, $2, $3, 'series', $4,
			$4, $5, 1,
			$6, 'server', $7, $8,
			'completed', 'score_complete', $9, $10
		)`,
		input.IDs.SeriesResultRevisionID,
		fixture.Scope.TournamentID,
		fixture.Scope.RosterID,
		fixture.Scope.SeriesID,
		input.IDs.ResultEventID,
		uuid.New(),
		source.ID,
		source.Revision,
		input.WinnerID,
		input.SettledAt,
	)
	if err != nil {
		return CommitResult{}, err
	}

	_, err = tx.Exec(
		ctx, `
		INSERT INTO series_score_revisions (
			id, tournament_id, roster_id, series_id, result_event_id,
			previous_revision_id, revision_number,
			operation, command_id, actor_kind, command_attempt_id,
			source_projection_revision_id, source_projection_revision,
			first_participant_wins, second_participant_wins, created_at
		)
		VALUES (
			$1, $2, $3, $4, $5,
			$6, 2,
			'append_attempt', $7, 'server', $8,
			$9, $10,
			1, 0, $11
		)`,
		input.IDs.SeriesScoreRevisionID,
		fixture.Scope.TournamentID,
		fixture.Scope.RosterID,
		fixture.Scope.SeriesID,
		input.IDs.ResultEventID,
		fixture.InitialScoreRevisionID,
		uuid.New(),
		fixture.Scope.AttemptID,
		source.ID,
		source.Revision,
		input.SettledAt,
	)
	if err != nil {
		return CommitResult{}, err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO series_score_revision_attempts (
			score_revision_id, tournament_id, roster_id, series_id,
			position, slot_id, slot_position, game_attempt_id, attempt_number,
			game_result_revision_id, result_event_id, result_state, result_reason,
			winner_id, occurred_at, created_at
		)
		SELECT $1, $2, $3, $4,
			1, attempt.slot_id, slot.slot_number, attempt.id, attempt.attempt_number,
			$5, $6, 'completed', 'solved',
			$7, $8, $8
		FROM game_attempts AS attempt
		INNER JOIN game_slots AS slot ON slot.id = attempt.slot_id
		WHERE attempt.id = $9`,
		input.IDs.SeriesScoreRevisionID,
		fixture.Scope.TournamentID,
		fixture.Scope.RosterID,
		fixture.Scope.SeriesID,
		input.IDs.GameResultRevisionID,
		input.IDs.ResultEventID,
		input.WinnerID,
		input.SettledAt,
		fixture.Scope.AttemptID,
	)
	if err != nil {
		return CommitResult{}, err
	}

	_, err = tx.Exec(
		ctx, `
		INSERT INTO audit_events (
			id, tournament_id, roster_id, series_id, result_event_id,
			actor_kind, action, payload, occurred_at, created_at
		)
		VALUES (
			$1, $2, $3, $4, $5,
			'server', 'tournament.result.committed',
			jsonb_build_object('reason', 'solved'), $6, $6
		)`,
		input.IDs.AuditEventID,
		fixture.Scope.TournamentID,
		fixture.Scope.RosterID,
		fixture.Scope.SeriesID,
		input.IDs.ResultEventID,
		input.SettledAt,
	)
	if err != nil {
		return CommitResult{}, err
	}

	_, err = tx.Exec(
		ctx, `
		INSERT INTO result_projection_evidence (
			id, tournament_id, roster_id, series_id, result_event_id,
			artifact_kinds, payload_digest, created_at
		)
		VALUES (
			$1, $2, $3, $4, $5,
			'["game_result","series_score","series_result"]'::JSONB,
			$6, $7
		)`,
		input.IDs.ProjectionEvidenceID,
		fixture.Scope.TournamentID,
		fixture.Scope.RosterID,
		fixture.Scope.SeriesID,
		input.IDs.ResultEventID,
		bytes.Repeat([]byte{31}, 32),
		input.SettledAt,
	)
	if err != nil {
		return CommitResult{}, err
	}

	outbox, err := sqlc.New(tx).CreateResultOutboxEvent(ctx, sqlc.CreateResultOutboxEventParams{
		ID:                   input.IDs.OutboxEventID,
		TournamentID:         fixture.Scope.TournamentID,
		RosterID:             fixture.Scope.RosterID,
		ProjectionRevisionID: source.ID,
		ProjectionRevision:   source.Revision,
		SeriesID:             fixture.Scope.SeriesID,
		ResultEventID:        input.IDs.ResultEventID,
		ProjectionEvidenceID: input.IDs.ProjectionEvidenceID,
		IdempotencyKey:       input.IDs.OutboxIdempotencyKey,
		Topic:                "tournament.result.committed",
		Payload:              []byte(`{"event":"result.committed"}`),
		CreatedAt:            pgtype.Timestamptz{Time: input.SettledAt, Valid: true},
	})
	if err != nil {
		return CommitResult{}, err
	}

	_, err = tx.Exec(
		ctx, `
		INSERT INTO result_commits (
			tournament_id, roster_id, series_id, attempt_id, result_event_id,
			game_result_revision_id, series_score_revision_id,
			series_result_revision_id, audit_event_id, outbox_event_id,
			projection_evidence_id, idempotency_key, created_at
		)
		VALUES (
			$1, $2, $3, $4, $5,
			$6, $7,
			$8, $9, $10,
			$11, $12, $13
		)`,
		fixture.Scope.TournamentID,
		fixture.Scope.RosterID,
		fixture.Scope.SeriesID,
		fixture.Scope.AttemptID,
		input.IDs.ResultEventID,
		input.IDs.GameResultRevisionID,
		input.IDs.SeriesScoreRevisionID,
		input.IDs.SeriesResultRevisionID,
		input.IDs.AuditEventID,
		input.IDs.OutboxEventID,
		input.IDs.ProjectionEvidenceID,
		input.IDs.CommitIdempotencyKey,
		input.SettledAt,
	)
	if err != nil {
		return CommitResult{}, err
	}

	if !input.AdvanceCurrentHeads {
		if err = tx.Commit(ctx); err != nil {
			return CommitResult{}, err
		}
		return CommitResult{
			IDs:                      input.IDs,
			SourceProjectionID:       source.ID,
			SourceProjectionRevision: source.Revision,
			ProjectionOrdinal:        outbox.ProjectionOrdinal,
			SettledAt:                input.SettledAt,
		}, nil
	}

	_, err = tx.Exec(
		ctx, `
		INSERT INTO official_result_heads (
			entity_kind, entity_id, series_id, roster_id,
			game_attempt_id, current_revision_id, updated_at
		)
		VALUES ('game_attempt', $1, $2, $3, $1, $4, $5)`,
		fixture.Scope.AttemptID,
		fixture.Scope.SeriesID,
		fixture.Scope.RosterID,
		input.IDs.GameResultRevisionID,
		input.SettledAt,
	)
	if err != nil {
		return CommitResult{}, err
	}
	_, err = tx.Exec(
		ctx, `
		INSERT INTO official_result_heads (
			entity_kind, entity_id, series_id, roster_id,
			current_revision_id, updated_at
		)
		VALUES ('series', $1, $1, $2, $3, $4)`,
		fixture.Scope.SeriesID,
		fixture.Scope.RosterID,
		input.IDs.SeriesResultRevisionID,
		input.SettledAt,
	)
	if err != nil {
		return CommitResult{}, err
	}
	_, err = tx.Exec(
		ctx, `
		UPDATE series_score_heads
		SET current_revision_id = $2,
			revision = revision + 1,
			updated_at = $3
		WHERE series_id = $1`,
		fixture.Scope.SeriesID,
		input.IDs.SeriesScoreRevisionID,
		input.SettledAt,
	)
	if err != nil {
		return CommitResult{}, err
	}

	_, err = tx.Exec(
		ctx, `
		UPDATE game_attempts
		SET state = 'completed',
			result_reason = 'solved',
			winner_id = $2,
			result_revision_id = $3,
			revision = revision + 1,
			updated_at = $4,
			finished_at = $4
		WHERE id = $1`,
		fixture.Scope.AttemptID,
		input.WinnerID,
		input.IDs.GameResultRevisionID,
		input.SettledAt,
	)
	if err != nil {
		return CommitResult{}, err
	}
	_, err = tx.Exec(
		ctx, `
		UPDATE series
		SET state = 'completed',
			first_participant_wins = 1,
			winner_id = $2,
			current_score_revision_id = $3,
			current_result_revision_id = $4,
			revision = revision + 1,
			updated_at = $5,
			started_at = $6,
			finished_at = $5
		WHERE id = $1`,
		fixture.Scope.SeriesID,
		input.WinnerID,
		input.IDs.SeriesScoreRevisionID,
		input.IDs.SeriesResultRevisionID,
		input.SettledAt,
		fixture.LockedAt,
	)
	if err != nil {
		return CommitResult{}, err
	}

	if err = tx.Commit(ctx); err != nil {
		return CommitResult{}, err
	}
	return CommitResult{
		IDs:                      input.IDs,
		SourceProjectionID:       source.ID,
		SourceProjectionRevision: source.Revision,
		ProjectionOrdinal:        outbox.ProjectionOrdinal,
		SettledAt:                input.SettledAt,
	}, nil
}
