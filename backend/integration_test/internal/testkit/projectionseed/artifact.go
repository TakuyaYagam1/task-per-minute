//go:build integration

package projectionseed

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ArtifactInput identifies one immutable projection artifact row.
type ArtifactInput struct {
	TournamentID uuid.UUID
	RosterID     uuid.UUID
	RevisionID   uuid.UUID
	Kind         string
	Key          string
	Payload      string
	DigestByte   byte
	CreatedAt    time.Time
}

// ArtifactMembersInput identifies the ordered participant members for an
// artifact. Scores remain nullable except for standings artifacts.
type ArtifactMembersInput struct {
	ArtifactID     uuid.UUID
	TournamentID   uuid.UUID
	RosterID       uuid.UUID
	Kind           string
	ParticipantIDs []uuid.UUID
}

// CreateArtifact persists one artifact row. Member persistence is separate so
// callers retain the original statement boundary between artifact and members.
func CreateArtifact(ctx context.Context, pool *pgxpool.Pool, input ArtifactInput) (uuid.UUID, error) {
	if pool == nil {
		return uuid.Nil, fmt.Errorf("projection seed: nil pool")
	}

	id := uuid.New()
	_, err := pool.Exec(
		ctx, `
		INSERT INTO projection_artifacts (
			id, tournament_id, roster_id, produced_by_revision_id,
			artifact_kind, artifact_key, payload, payload_digest, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7::JSONB, $8, $9)`,
		id,
		input.TournamentID,
		input.RosterID,
		input.RevisionID,
		input.Kind,
		input.Key,
		input.Payload,
		bytes.Repeat([]byte{input.DigestByte}, 32),
		input.CreatedAt,
	)
	if err != nil {
		return uuid.Nil, fmt.Errorf("projection seed: create artifact: %w", err)
	}
	return id, nil
}

// CreateArtifactMembers persists ordered artifact members as independent
// statements, preserving nullable score handling and champion cardinality.
func CreateArtifactMembers(ctx context.Context, pool *pgxpool.Pool, input ArtifactMembersInput) error {
	if pool == nil {
		return fmt.Errorf("projection seed: nil pool")
	}

	memberCount := len(input.ParticipantIDs)
	if input.Kind == "champion" {
		memberCount = 1
	}
	if memberCount > len(input.ParticipantIDs) {
		return fmt.Errorf("projection seed: insufficient participants for %s artifact", input.Kind)
	}

	for index, participantID := range input.ParticipantIDs[:memberCount] {
		var score any
		if input.Kind == "standings" {
			score = memberCount - index
		}
		_, err := pool.Exec(
			ctx, `
			INSERT INTO projection_artifact_members (
				artifact_id, tournament_id, roster_id, artifact_kind,
				participant_id, position, score
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			input.ArtifactID,
			input.TournamentID,
			input.RosterID,
			input.Kind,
			participantID,
			index+1,
			score,
		)
		if err != nil {
			return fmt.Errorf("projection seed: create artifact member %d: %w", index, err)
		}
	}
	return nil
}
