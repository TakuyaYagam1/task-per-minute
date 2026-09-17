//go:build integration

package projectionseed

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/goldenseed"
)

// GoldenSourceInput identifies the minimal Golden evidence graph used as a
// projection source. The first two participants form the committed position.
type GoldenSourceInput struct {
	TournamentID   uuid.UUID
	RosterID       uuid.UUID
	ParticipantIDs []uuid.UUID
	CreatedAt      time.Time
}

// CreateGoldenSource preserves the root fixture's independent Golden writes
// and state transitions, returning the committed position identity.
func CreateGoldenSource(ctx context.Context, pool *pgxpool.Pool, input GoldenSourceInput) (uuid.UUID, error) {
	if pool == nil {
		return uuid.Nil, fmt.Errorf("projection seed: nil pool")
	}
	if len(input.ParticipantIDs) < 2 {
		return uuid.Nil, fmt.Errorf("projection seed: expected at least two participants, got %d", len(input.ParticipantIDs))
	}

	attemptID, err := goldenseed.CreateAttempt(ctx, pool, goldenseed.AttemptInput{
		TournamentID:      input.TournamentID,
		RosterID:          input.RosterID,
		AttemptNumber:     1,
		PreviousAttemptID: nil,
		CreatedAt:         input.CreatedAt,
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("projection seed: create Golden attempt: %w", err)
	}

	readyAt := input.CreatedAt.Add(time.Second)
	membershipIDs := make([]uuid.UUID, 2)
	for index := range membershipIDs {
		membershipIDs[index], err = goldenseed.CreateMembership(ctx, pool, goldenseed.MembershipInput{
			AttemptID:     attemptID,
			TournamentID:  input.TournamentID,
			RosterID:      input.RosterID,
			ParticipantID: input.ParticipantIDs[index],
			SelectionKind: "direct",
			SelectedAt:    input.CreatedAt,
			ReadyAt:       readyAt,
		})
		if err != nil {
			return uuid.Nil, fmt.Errorf("projection seed: create Golden membership %d: %w", index, err)
		}
	}

	if _, err = pool.Exec(ctx, `
		UPDATE golden_attempts
		SET state = 'ready', disclosed_at = $2, ready_at = $3
		WHERE id = $1`, attemptID, input.CreatedAt.Add(2*time.Second), input.CreatedAt.Add(3*time.Second)); err != nil {
		return uuid.Nil, fmt.Errorf("projection seed: advance Golden attempt to ready: %w", err)
	}
	for index, membershipID := range membershipIDs {
		if _, err = pool.Exec(ctx, `
			UPDATE golden_memberships
			SET participation_established_at = $2
			WHERE id = $1`, membershipID, input.CreatedAt.Add(4*time.Second)); err != nil {
			return uuid.Nil, fmt.Errorf("projection seed: establish Golden participation %d: %w", index, err)
		}
	}
	if _, err = pool.Exec(ctx, `
		UPDATE golden_attempts
		SET state = 'active', started_at = $2
		WHERE id = $1`, attemptID, input.CreatedAt.Add(5*time.Second)); err != nil {
		return uuid.Nil, fmt.Errorf("projection seed: advance Golden attempt to active: %w", err)
	}

	submissionID, err := goldenseed.CreateSubmission(ctx, pool, goldenseed.SubmissionInput{
		AttemptID:       attemptID,
		TournamentID:    input.TournamentID,
		RosterID:        input.RosterID,
		MembershipID:    membershipIDs[0],
		ParticipantID:   input.ParticipantIDs[0],
		ServerSequence:  1,
		Position:        1,
		Status:          "accepted",
		RejectionReason: nil,
		CreatedAt:       input.CreatedAt.Add(6 * time.Second),
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("projection seed: create Golden submission: %w", err)
	}
	positionCommitID, err := goldenseed.CreatePositionCommit(ctx, pool, goldenseed.PositionCommitInput{
		AttemptID:                attemptID,
		TournamentID:             input.TournamentID,
		RosterID:                 input.RosterID,
		MembershipID:             membershipIDs[0],
		ParticipantID:            input.ParticipantIDs[0],
		SubmissionID:             submissionID,
		PreviousPositionCommitID: nil,
		Position:                 1,
		CreatedAt:                input.CreatedAt.Add(7 * time.Second),
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("projection seed: create Golden position commit: %w", err)
	}
	return positionCommitID, nil
}

// CutoffInput identifies one projection cutoff row. Nullable IDs remain any
// so callers preserve the database driver's existing null semantics.
type CutoffInput struct {
	TournamentID             uuid.UUID
	RosterID                 uuid.UUID
	SequenceNumber           int
	PreviousCutoffID         any
	SourceKind               string
	OfficialResultRevisionID any
	GoldenPositionCommitID   any
	Reason                   string
	CreatedAt                time.Time
}

// CreateCutoff inserts one projection cutoff row.
func CreateCutoff(ctx context.Context, pool *pgxpool.Pool, input CutoffInput) (uuid.UUID, error) {
	if pool == nil {
		return uuid.Nil, fmt.Errorf("projection seed: nil pool")
	}
	id := uuid.New()
	_, err := pool.Exec(
		ctx, `
		INSERT INTO projection_cutoffs (
			id, tournament_id, roster_id, sequence_number, previous_cutoff_id,
			source_kind, official_result_revision_id, golden_position_commit_id,
			reason, cutoff_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)`,
		id,
		input.TournamentID,
		input.RosterID,
		input.SequenceNumber,
		input.PreviousCutoffID,
		input.SourceKind,
		input.OfficialResultRevisionID,
		input.GoldenPositionCommitID,
		input.Reason,
		input.CreatedAt,
	)
	if err != nil {
		return uuid.Nil, fmt.Errorf("projection seed: create cutoff: %w", err)
	}
	return id, nil
}

// RevisionInput identifies one projection revision row.
type RevisionInput struct {
	TournamentID       uuid.UUID
	RosterID           uuid.UUID
	RevisionNumber     int
	PreviousRevisionID any
	CutoffID           uuid.UUID
	CreatedAt          time.Time
}

// CreateRevision inserts one projection revision row.
func CreateRevision(ctx context.Context, pool *pgxpool.Pool, input RevisionInput) (uuid.UUID, error) {
	if pool == nil {
		return uuid.Nil, fmt.Errorf("projection seed: nil pool")
	}
	id := uuid.New()
	_, err := pool.Exec(
		ctx, `
		INSERT INTO projection_revisions (
			id, tournament_id, roster_id, revision_number,
			previous_revision_id, cutoff_id, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		id,
		input.TournamentID,
		input.RosterID,
		input.RevisionNumber,
		input.PreviousRevisionID,
		input.CutoffID,
		input.CreatedAt,
	)
	if err != nil {
		return uuid.Nil, fmt.Errorf("projection seed: create revision: %w", err)
	}
	return id, nil
}
