//go:build integration

// Package goldenseed provides small Golden migration row seeds for integration
// tests. It accepts explicit database inputs and does not depend on test
// handles or the root integration package.
package goldenseed

import (
	"bytes"
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

// SubmissionInput identifies one Golden provisional submission row. Nullable
// rejection reasons remain any so callers preserve the database driver's
// existing null semantics.
type SubmissionInput struct {
	AttemptID       uuid.UUID
	TournamentID    uuid.UUID
	RosterID        uuid.UUID
	MembershipID    uuid.UUID
	ParticipantID   uuid.UUID
	ServerSequence  int
	Position        int
	Status          string
	RejectionReason any
	CreatedAt       time.Time
}

// PositionCommitInput identifies one Golden position commit row. Nullable
// previous commit IDs remain any to preserve the original Exec contract.
type PositionCommitInput struct {
	AttemptID                uuid.UUID
	TournamentID             uuid.UUID
	RosterID                 uuid.UUID
	MembershipID             uuid.UUID
	ParticipantID            uuid.UUID
	SubmissionID             uuid.UUID
	PreviousPositionCommitID any
	Position                 int
	CreatedAt                time.Time
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

// CreateSubmission inserts exactly one Golden provisional submission row in
// one statement.
func CreateSubmission(ctx context.Context, pool *pgxpool.Pool, input SubmissionInput) (uuid.UUID, error) {
	id := uuid.New()
	_, err := pool.Exec(
		ctx, `
		INSERT INTO golden_provisional_submissions (
			id, attempt_id, tournament_id, roster_id, membership_id, participant_id,
			server_sequence, idempotency_key, provisional_position,
			elapsed_milliseconds, status, rejection_reason, payload_digest,
			submitted_at, received_at, created_at
		)
		VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9,
			1000, $10, $11, $12,
			$13, $13, $13
		)`,
		id,
		input.AttemptID,
		input.TournamentID,
		input.RosterID,
		input.MembershipID,
		input.ParticipantID,
		input.ServerSequence,
		uuid.New(),
		input.Position,
		input.Status,
		input.RejectionReason,
		bytes.Repeat([]byte{byte(input.ServerSequence + 1)}, 32),
		input.CreatedAt,
	)
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// CreatePositionCommit inserts exactly one Golden position commit row in one
// statement.
func CreatePositionCommit(ctx context.Context, pool *pgxpool.Pool, input PositionCommitInput) (uuid.UUID, error) {
	id := uuid.New()
	_, err := pool.Exec(
		ctx, `
		INSERT INTO golden_position_commits (
			id, attempt_id, tournament_id, roster_id, membership_id, participant_id,
			provisional_submission_id, previous_position_commit_id,
			position, committed_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)`,
		id,
		input.AttemptID,
		input.TournamentID,
		input.RosterID,
		input.MembershipID,
		input.ParticipantID,
		input.SubmissionID,
		input.PreviousPositionCommitID,
		input.Position,
		input.CreatedAt,
	)
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}
