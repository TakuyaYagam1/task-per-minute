//go:build integration

package resultaudit

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AcceptSubmission appends the first accepted submission for a locked
// attempt. The sequence bump and event insert intentionally remain separate
// statements, matching the original integration fixture behavior.
func AcceptSubmission(
	ctx context.Context,
	pool *pgxpool.Pool,
	fixture Fixture,
	input SubmissionInput,
) (Submission, error) {
	_, err := pool.Exec(ctx, `
		UPDATE game_attempts
		SET submission_event_sequence = submission_event_sequence + 1
		WHERE id = $1`, fixture.Scope.AttemptID)
	if err != nil {
		return Submission{}, err
	}
	_, err = pool.Exec(
		ctx, `
		INSERT INTO submission_events (
			id, tournament_id, roster_id, series_id, attempt_id, assignment_id,
			participant_id, server_sequence, idempotency_key, status,
			payload_digest, intent_digest, submitted_at, received_at, created_at
		)
		VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, 1, $8, 'accepted',
			$9, $10, $11, $11, $11
		)`,
		input.ID,
		fixture.Scope.TournamentID,
		fixture.Scope.RosterID,
		fixture.Scope.SeriesID,
		fixture.Scope.AttemptID,
		fixture.Scope.AssignmentID,
		input.ParticipantID,
		input.IdempotencyKey,
		input.PayloadDigest,
		input.IntentDigest,
		input.ReceivedAt,
	)
	if err != nil {
		return Submission{}, err
	}
	return Submission{
		ID:             input.ID,
		IdempotencyKey: input.IdempotencyKey,
		ServerSequence: 1,
	}, nil
}
