//go:build integration

package projectionseed

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ArtifactLinkInput identifies one revision-to-artifact link.
type ArtifactLinkInput struct {
	RevisionID   uuid.UUID
	TournamentID uuid.UUID
	RosterID     uuid.UUID
	Kind         string
	ArtifactID   uuid.UUID
	ChangeKind   string
}

// LinkArtifact inserts one projection revision artifact link.
func LinkArtifact(ctx context.Context, pool *pgxpool.Pool, input ArtifactLinkInput) error {
	if pool == nil {
		return fmt.Errorf("projection seed: nil pool")
	}
	_, err := pool.Exec(
		ctx, `
		INSERT INTO projection_revision_artifacts (
			revision_id, tournament_id, roster_id,
			artifact_kind, artifact_id, change_kind
		)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		input.RevisionID,
		input.TournamentID,
		input.RosterID,
		input.Kind,
		input.ArtifactID,
		input.ChangeKind,
	)
	if err != nil {
		return fmt.Errorf("projection seed: link artifact: %w", err)
	}
	return nil
}

// ArtifactDependencyInput identifies an artifact-to-artifact dependency.
type ArtifactDependencyInput struct {
	ArtifactID          uuid.UUID
	TournamentID        uuid.UUID
	RosterID            uuid.UUID
	DependsOnArtifactID uuid.UUID
}

// CreateArtifactDependency inserts one artifact dependency.
func CreateArtifactDependency(ctx context.Context, pool *pgxpool.Pool, input ArtifactDependencyInput) error {
	if pool == nil {
		return fmt.Errorf("projection seed: nil pool")
	}
	_, err := pool.Exec(
		ctx, `
		INSERT INTO projection_dependencies (
			artifact_id, tournament_id, roster_id,
			dependency_kind, depends_on_artifact_id
		)
		VALUES ($1, $2, $3, 'artifact', $4)`,
		input.ArtifactID,
		input.TournamentID,
		input.RosterID,
		input.DependsOnArtifactID,
	)
	if err != nil {
		return fmt.Errorf("projection seed: create artifact dependency: %w", err)
	}
	return nil
}

// GoldenDependencyInput identifies an artifact dependency on a Golden commit.
type GoldenDependencyInput struct {
	ArtifactID             uuid.UUID
	TournamentID           uuid.UUID
	RosterID               uuid.UUID
	GoldenPositionCommitID uuid.UUID
}

// CreateGoldenDependency inserts one Golden position dependency.
func CreateGoldenDependency(ctx context.Context, pool *pgxpool.Pool, input GoldenDependencyInput) error {
	if pool == nil {
		return fmt.Errorf("projection seed: nil pool")
	}
	_, err := pool.Exec(
		ctx, `
		INSERT INTO projection_dependencies (
			artifact_id, tournament_id, roster_id,
			dependency_kind, golden_position_commit_id
		)
		VALUES ($1, $2, $3, 'golden_position', $4)`,
		input.ArtifactID,
		input.TournamentID,
		input.RosterID,
		input.GoldenPositionCommitID,
	)
	if err != nil {
		return fmt.Errorf("projection seed: create Golden dependency: %w", err)
	}
	return nil
}

// PublishRevision updates one projection revision to published.
func PublishRevision(ctx context.Context, pool *pgxpool.Pool, revisionID uuid.UUID, publishedAt time.Time) error {
	if pool == nil {
		return fmt.Errorf("projection seed: nil pool")
	}
	_, err := pool.Exec(ctx, `
		UPDATE projection_revisions
		SET state = 'published', published_at = $2
		WHERE id = $1`, revisionID, publishedAt)
	if err != nil {
		return fmt.Errorf("projection seed: publish revision: %w", err)
	}
	return nil
}

// SupersedeInput identifies an atomic previous/replacement revision transition.
type SupersedeInput struct {
	PreviousRevisionID    uuid.UUID
	ReplacementRevisionID uuid.UUID
	TransitionAt          time.Time
}

// SupersedeRevision applies the two revision state changes in one transaction.
func SupersedeRevision(ctx context.Context, pool *pgxpool.Pool, input SupersedeInput) error {
	if pool == nil {
		return fmt.Errorf("projection seed: nil pool")
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("projection seed: begin supersession transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `
		UPDATE projection_revisions
		SET state = 'superseded',
			superseded_by_revision_id = $2,
			superseded_at = $3,
			supersession_reason = 'affected descendants rebuilt'
		WHERE id = $1`, input.PreviousRevisionID, input.ReplacementRevisionID, input.TransitionAt)
	if err != nil {
		return fmt.Errorf("projection seed: supersede previous revision: %w", err)
	}
	_, err = tx.Exec(ctx, `
		UPDATE projection_revisions
		SET state = 'published', published_at = $2
		WHERE id = $1`, input.ReplacementRevisionID, input.TransitionAt)
	if err != nil {
		return fmt.Errorf("projection seed: publish replacement revision: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("projection seed: commit supersession transaction: %w", err)
	}
	return nil
}

// CrossRosterCutoffInput identifies the negative scope-isolation probe.
type CrossRosterCutoffInput struct {
	TournamentID           uuid.UUID
	RosterID               uuid.UUID
	GoldenPositionCommitID uuid.UUID
	CreatedAt              time.Time
}

// CreateCrossRosterCutoff attempts the intentionally invalid cutoff insert and
// returns the database error for the root wrapper's failure assertion.
func CreateCrossRosterCutoff(ctx context.Context, pool *pgxpool.Pool, input CrossRosterCutoffInput) error {
	if pool == nil {
		return fmt.Errorf("projection seed: nil pool")
	}
	_, err := pool.Exec(
		ctx, `
		INSERT INTO projection_cutoffs (
			tournament_id, roster_id, sequence_number,
			source_kind, golden_position_commit_id,
			reason, cutoff_at, created_at
		)
		VALUES ($1, $2, 1, 'golden_position', $3, 'cross-roster probe', $4, $4)`,
		input.TournamentID,
		input.RosterID,
		input.GoldenPositionCommitID,
		input.CreatedAt,
	)
	if err != nil {
		return err
	}
	return nil
}
