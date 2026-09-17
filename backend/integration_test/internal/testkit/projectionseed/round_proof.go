//go:build integration

// Package projectionseed provides reusable projection source fixtures for
// integration tests. It returns errors instead of depending on test handles so
// capability-specific test packages can compose the same database setup.
package projectionseed

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	projectionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/projection"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

// RoundProofInput identifies the tournament graph that receives a published
// Golden-backed source projection.
type RoundProofInput struct {
	TournamentID  uuid.UUID
	RosterID      uuid.UUID
	ParticipantID uuid.UUID
	At            time.Time
}

// RoundProofSeed contains the durable source identities needed by downstream
// wave, draft, and result fixtures.
type RoundProofSeed struct {
	GoldenPositionCommitID uuid.UUID
	ProjectionID           uuid.UUID
	ProjectionRevision     int64
}

// PublishRoundProof creates the same Golden source and published projection
// used by the root integration fixtures. Golden rows use the generated SQLC
// operations and preserve the original final state and timestamps, while
// projection publication keeps the production transaction and CAS path.
func PublishRoundProof(
	ctx context.Context,
	pool *pgxpool.Pool,
	input RoundProofInput,
) (RoundProofSeed, error) {
	if pool == nil {
		return RoundProofSeed{}, fmt.Errorf("projection seed: nil pool")
	}
	if input.TournamentID == uuid.Nil || input.RosterID == uuid.Nil ||
		input.ParticipantID == uuid.Nil || input.At.IsZero() {
		return RoundProofSeed{}, fmt.Errorf("projection seed: invalid round proof input")
	}

	participantIDs, err := loadParticipants(ctx, pool, input.RosterID)
	if err != nil {
		return RoundProofSeed{}, err
	}
	if len(participantIDs) != 4 {
		return RoundProofSeed{}, fmt.Errorf("projection seed: expected 4 participants, got %d", len(participantIDs))
	}
	if !containsParticipant(participantIDs, input.ParticipantID) {
		return RoundProofSeed{}, fmt.Errorf("projection seed: participant %s is outside roster", input.ParticipantID)
	}

	goldenPositionCommitID, err := createGoldenSource(ctx, pool, input, participantIDs)
	if err != nil {
		return RoundProofSeed{}, err
	}
	artifacts, err := roundProofArtifacts(participantIDs, goldenPositionCommitID, "round-proof")
	if err != nil {
		return RoundProofSeed{}, err
	}

	record, err := projectionrepo.NewProjectionPostgres(postgres.NewTxManager(pool)).Publish(
		ctx,
		projectionrepo.ProjectionPublishInput{
			IDs: projectionrepo.ProjectionIDs{RevisionID: uuid.New(), CutoffID: uuid.New()},
			Scope: projectionrepo.ProjectionScope{
				TournamentID: input.TournamentID,
				RosterID:     input.RosterID,
			},
			Source: projectionrepo.ProjectionSource{
				Kind:                   "golden_position",
				GoldenPositionCommitID: &goldenPositionCommitID,
				Reason:                 "publish round proof source projection",
			},
			Artifacts:          artifacts,
			SupersessionReason: "replace round proof source projection",
			CutoffAt:           input.At,
			CreatedAt:          input.At,
			PublishedAt:        input.At.Add(time.Microsecond),
		},
	)
	if err != nil {
		return RoundProofSeed{}, err
	}
	return RoundProofSeed{
		GoldenPositionCommitID: goldenPositionCommitID,
		ProjectionID:           record.Revision.ID,
		ProjectionRevision:     record.Revision.RevisionNumber,
	}, nil
}

func loadParticipants(
	ctx context.Context,
	pool *pgxpool.Pool,
	rosterID uuid.UUID,
) ([]uuid.UUID, error) {
	rows, err := pool.Query(ctx, `
		SELECT id
		FROM participants
		WHERE roster_id = $1
		ORDER BY seed, id`, rosterID)
	if err != nil {
		return nil, fmt.Errorf("projection seed: load participants: %w", err)
	}
	defer rows.Close()

	participantIDs := make([]uuid.UUID, 0, 4)
	for rows.Next() {
		var participantID uuid.UUID
		if err := rows.Scan(&participantID); err != nil {
			return nil, fmt.Errorf("projection seed: scan participant: %w", err)
		}
		participantIDs = append(participantIDs, participantID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("projection seed: read participants: %w", err)
	}
	return participantIDs, nil
}

func containsParticipant(participantIDs []uuid.UUID, want uuid.UUID) bool {
	for _, participantID := range participantIDs {
		if participantID == want {
			return true
		}
	}
	return false
}

func createGoldenSource(
	ctx context.Context,
	pool *pgxpool.Pool,
	input RoundProofInput,
	participantIDs []uuid.UUID,
) (uuid.UUID, error) {
	queries := sqlc.New(pool)
	createdAt := input.At.Add(-10 * time.Minute)
	attempt, err := queries.CreateGoldenAttempt(ctx, sqlc.CreateGoldenAttemptParams{
		ID:                uuid.New(),
		TournamentID:      input.TournamentID,
		RosterID:          input.RosterID,
		AttemptNumber:     1,
		PreviousAttemptID: uuid.NullUUID{},
		CreatedAt:         timestamptz(createdAt),
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("projection seed: create golden attempt: %w", err)
	}

	membershipIDs := make([]uuid.UUID, 2)
	for index := range membershipIDs {
		membership, createErr := queries.CreateGoldenMembership(ctx, sqlc.CreateGoldenMembershipParams{
			ID:            uuid.New(),
			AttemptID:     attempt.ID,
			TournamentID:  input.TournamentID,
			RosterID:      input.RosterID,
			ParticipantID: participantIDs[index],
			SelectionKind: "direct",
			SelectedAt:    timestamptz(createdAt),
			CreatedAt:     timestamptz(createdAt),
		})
		if createErr != nil {
			return uuid.Nil, fmt.Errorf("projection seed: create golden membership: %w", createErr)
		}
		membershipIDs[index] = membership.ID
	}

	readyAt := createdAt.Add(time.Second)
	for _, membershipID := range membershipIDs {
		if _, err = queries.MarkGoldenMembershipReady(ctx, sqlc.MarkGoldenMembershipReadyParams{
			ReadyAt:      timestamptz(readyAt),
			ID:           membershipID,
			AttemptID:    attempt.ID,
			TournamentID: input.TournamentID,
			RosterID:     input.RosterID,
		}); err != nil {
			return uuid.Nil, fmt.Errorf("projection seed: mark golden membership ready: %w", err)
		}
	}
	if _, err = queries.UpdateGoldenAttemptCAS(ctx, sqlc.UpdateGoldenAttemptCASParams{
		NextState:     "ready",
		DisclosedAt:   timestamptz(createdAt.Add(2 * time.Second)),
		ReadyAt:       timestamptz(createdAt.Add(3 * time.Second)),
		ID:            attempt.ID,
		TournamentID:  input.TournamentID,
		RosterID:      input.RosterID,
		ExpectedState: "prepared",
	}); err != nil {
		return uuid.Nil, fmt.Errorf("projection seed: advance golden attempt to ready: %w", err)
	}
	for _, membershipID := range membershipIDs {
		if _, err = queries.EstablishGoldenParticipation(ctx, sqlc.EstablishGoldenParticipationParams{
			EstablishedAt: timestamptz(createdAt.Add(4 * time.Second)),
			ID:            membershipID,
			AttemptID:     attempt.ID,
			TournamentID:  input.TournamentID,
			RosterID:      input.RosterID,
		}); err != nil {
			return uuid.Nil, fmt.Errorf("projection seed: establish golden participation: %w", err)
		}
	}
	if _, err = queries.UpdateGoldenAttemptCAS(ctx, sqlc.UpdateGoldenAttemptCASParams{
		NextState:     "active",
		DisclosedAt:   timestamptz(createdAt.Add(2 * time.Second)),
		ReadyAt:       timestamptz(createdAt.Add(3 * time.Second)),
		StartedAt:     timestamptz(createdAt.Add(5 * time.Second)),
		ID:            attempt.ID,
		TournamentID:  input.TournamentID,
		RosterID:      input.RosterID,
		ExpectedState: "ready",
	}); err != nil {
		return uuid.Nil, fmt.Errorf("projection seed: advance golden attempt to active: %w", err)
	}

	submittedAt := createdAt.Add(6 * time.Second)
	submission, err := queries.CreateGoldenProvisionalSubmission(ctx, sqlc.CreateGoldenProvisionalSubmissionParams{
		ID:                  uuid.New(),
		AttemptID:           attempt.ID,
		TournamentID:        input.TournamentID,
		RosterID:            input.RosterID,
		MembershipID:        membershipIDs[0],
		ParticipantID:       participantIDs[0],
		ServerSequence:      1,
		IdempotencyKey:      uuid.New(),
		ProvisionalPosition: 1,
		ElapsedMilliseconds: 1000,
		Status:              "accepted",
		PayloadDigest:       bytesForDigest(2),
		SubmittedAt:         timestamptz(submittedAt),
		ReceivedAt:          timestamptz(submittedAt),
		CreatedAt:           timestamptz(submittedAt),
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("projection seed: create golden submission: %w", err)
	}
	positionCommit, err := queries.CreateGoldenPositionCommit(ctx, sqlc.CreateGoldenPositionCommitParams{
		ID:                       uuid.New(),
		AttemptID:                attempt.ID,
		TournamentID:             input.TournamentID,
		RosterID:                 input.RosterID,
		MembershipID:             membershipIDs[0],
		ParticipantID:            participantIDs[0],
		ProvisionalSubmissionID:  submission.ID,
		PreviousPositionCommitID: uuid.NullUUID{},
		Position:                 1,
		CommittedAt:              timestamptz(createdAt.Add(7 * time.Second)),
		CreatedAt:                timestamptz(createdAt.Add(7 * time.Second)),
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("projection seed: create golden position commit: %w", err)
	}
	return positionCommit.ID, nil
}

func roundProofArtifacts(
	participantIDs []uuid.UUID,
	goldenPositionCommitID uuid.UUID,
	version string,
) ([]projectionrepo.ProjectionArtifactInput, error) {
	if len(participantIDs) != 4 {
		return nil, fmt.Errorf("projection seed: expected 4 artifact participants, got %d", len(participantIDs))
	}
	standingsID := uuid.New()
	bracketID := uuid.New()
	topFourID := uuid.New()
	standingsMembers := make([]projectionrepo.ProjectionMemberInput, 0, len(participantIDs))
	positionMembers := make([]projectionrepo.ProjectionMemberInput, 0, len(participantIDs))
	entries := make([]map[string]any, 0, len(participantIDs))
	for index, participantID := range participantIDs {
		points := len(participantIDs) - index
		score := int64(points * 1000)
		position := int32(index + 1)
		standingsMembers = append(standingsMembers, projectionrepo.ProjectionMemberInput{
			ParticipantID: participantID, Position: position, ScoreMilli: &score,
		})
		positionMembers = append(positionMembers, projectionrepo.ProjectionMemberInput{
			ParticipantID: participantID, Position: position,
		})
		entries = append(entries, map[string]any{
			"participant_id": participantID,
			"position":       position,
			"points":         points,
			"buchholz":       len(participantIDs) - index - 1,
			"effective_time": int64(index+1) * int64(time.Second),
		})
	}
	standingsPayload, err := json.Marshal(map[string]any{
		"version": version,
		"entries": entries,
	})
	if err != nil {
		return nil, fmt.Errorf("projection seed: marshal standings: %w", err)
	}
	bracketPayload, err := json.Marshal(map[string]any{
		"version": version,
		"rounds": []map[string]any{
			{
				"position":              1,
				"series_id":             uuid.New(),
				"first_participant_id":  participantIDs[0],
				"second_participant_id": participantIDs[3],
				"format":                "bo1",
				"state":                 "planned",
				"first_wins":            0,
				"second_wins":           0,
			},
			{
				"position":              2,
				"series_id":             uuid.New(),
				"first_participant_id":  participantIDs[1],
				"second_participant_id": participantIDs[2],
				"format":                "bo1",
				"state":                 "planned",
				"first_wins":            0,
				"second_wins":           0,
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("projection seed: marshal bracket: %w", err)
	}
	return []projectionrepo.ProjectionArtifactInput{
		projectionArtifact(
			standingsID,
			domain.ArtifactKindStandings,
			version,
			standingsPayload,
			standingsMembers,
			projectionrepo.ProjectionDependencyInput{
				ID: uuid.New(), Kind: "golden_position", GoldenPositionCommitID: &goldenPositionCommitID,
			},
		),
		projectionArtifact(
			bracketID,
			domain.ArtifactKindBracket,
			version,
			bracketPayload,
			positionMembers,
			projectionrepo.ProjectionDependencyInput{
				ID: uuid.New(), Kind: "artifact", DependsOnArtifactID: &standingsID,
			},
		),
		projectionArtifact(
			topFourID,
			domain.ArtifactKindTopFour,
			version,
			json.RawMessage(fmt.Sprintf(
				`{"participants":[%q,%q,%q,%q]}`,
				participantIDs[0], participantIDs[1], participantIDs[2], participantIDs[3],
			)),
			positionMembers,
			projectionrepo.ProjectionDependencyInput{
				ID: uuid.New(), Kind: "artifact", DependsOnArtifactID: &bracketID,
			},
		),
	}, nil
}

func projectionArtifact(
	id uuid.UUID,
	kind domain.ArtifactKind,
	version string,
	payload json.RawMessage,
	members []projectionrepo.ProjectionMemberInput,
	dependency projectionrepo.ProjectionDependencyInput,
) projectionrepo.ProjectionArtifactInput {
	return projectionrepo.ProjectionArtifactInput{
		ID: id, Kind: kind, Key: string(kind) + "-" + version, Payload: payload,
		PayloadDigest: sha256.Sum256(payload), Members: members,
		Dependencies: []projectionrepo.ProjectionDependencyInput{dependency},
	}
}

func bytesForDigest(value byte) []byte {
	digest := make([]byte, sha256.Size)
	for index := range digest {
		digest[index] = value
	}
	return digest
}

func timestamptz(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}
