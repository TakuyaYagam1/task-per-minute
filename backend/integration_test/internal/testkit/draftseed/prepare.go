//go:build integration

package draftseed

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/projectionseed"
	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/swissseed"
	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/tournamentseed"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

// ContentSeed identifies the published normal task pool needed by the draft.
// TaskIDs are returned so a caller can apply a typed content hook before the
// final pool/configuration publication.
type ContentSeed struct {
	TaskIDs              []uuid.UUID
	NormalPoolRevisionID uuid.UUID
}

// ContentBuilder prepares task content and returns the normal pool revision
// that CreateDraft should pin. The callback receives explicit database inputs
// and never needs a testing handle or the root integration package.
type ContentBuilder func(
	context.Context,
	*pgxpool.Pool,
	uuid.UUID,
	time.Time,
) (ContentSeed, error)

// PrepareInput identifies the common tournament, roster, participant,
// projection and draft composition. Content remains an explicit callback so
// hook-specific task/configuration behavior stays outside this package.
type PrepareInput struct {
	CreatedAt time.Time
	Content   ContentBuilder
}

// PreparedScope contains the graph identities returned by the common draft
// preparation path. Its Draft field can be passed to resultaudit.PrepareScope
// after assignment-plan setup is added by the caller.
type PreparedScope struct {
	TournamentID       uuid.UUID
	RosterID           uuid.UUID
	SeriesID           uuid.UUID
	ParticipantIDs     []uuid.UUID
	ProjectionID       uuid.UUID
	ProjectionRevision int64
	Content            ContentSeed
	Draft              DraftSeed
	CreatedAt          time.Time
}

// Prepare creates the common draft migration graph without importing the root
// integration package. It owns tournament/roster/participant setup, the round
// proof projection, the wave series and the generated draft rows. Content
// preparation is delegated through the typed callback to preserve hook
// behavior without duplicating root content setup.
func Prepare(
	ctx context.Context,
	pool *pgxpool.Pool,
	input PrepareInput,
) (PreparedScope, error) {
	if pool == nil {
		return PreparedScope{}, fmt.Errorf("draft seed: nil pool")
	}
	if input.CreatedAt.IsZero() || input.Content == nil {
		return PreparedScope{}, fmt.Errorf("draft seed: invalid prepare input")
	}

	tournamentID, err := tournamentseed.CreateTournament(ctx, pool)
	if err != nil {
		return PreparedScope{}, fmt.Errorf("draft seed: create tournament: %w", err)
	}
	roster, err := tournamentseed.CreateRoster(ctx, pool, tournamentID)
	if err != nil {
		return PreparedScope{}, fmt.Errorf("draft seed: create roster: %w", err)
	}
	playerIDs, err := tournamentseed.CreatePlayers(ctx, pool, "tournament_migration", 4)
	if err != nil {
		return PreparedScope{}, fmt.Errorf("draft seed: create players: %w", err)
	}
	participantIDs, err := swissseed.CreateParticipants(ctx, pool, roster.ID, playerIDs)
	if err != nil {
		return PreparedScope{}, fmt.Errorf("draft seed: create participants: %w", err)
	}
	if len(participantIDs) != 4 {
		return PreparedScope{}, fmt.Errorf("draft seed: expected four participants, got %d", len(participantIDs))
	}

	proof, err := projectionseed.PublishRoundProof(ctx, pool, projectionseed.RoundProofInput{
		TournamentID:  tournamentID,
		RosterID:      roster.ID,
		ParticipantID: participantIDs[0],
		At:            input.CreatedAt,
	})
	if err != nil {
		return PreparedScope{}, fmt.Errorf("draft seed: publish round proof: %w", err)
	}

	seriesID := uuid.New()
	_, err = CreateWaveSeries(ctx, pool, WaveInput{
		TournamentID:             tournamentID,
		RosterID:                 roster.ID,
		ParticipantIDs:           participantIDs[:2],
		SeriesID:                 seriesID,
		FirstParticipantID:       participantIDs[0],
		SecondParticipantID:      participantIDs[1],
		SourceProjectionID:       proof.ProjectionID,
		SourceProjectionRevision: proof.ProjectionRevision,
		Format:                   domain.SeriesFormatBO1,
		CreatedAt:                input.CreatedAt,
	})
	if err != nil {
		return PreparedScope{}, fmt.Errorf("draft seed: create wave series: %w", err)
	}

	content, err := input.Content(ctx, pool, tournamentID, input.CreatedAt)
	if err != nil {
		return PreparedScope{}, fmt.Errorf("draft seed: prepare content: %w", err)
	}
	if content.NormalPoolRevisionID == uuid.Nil {
		return PreparedScope{}, fmt.Errorf("draft seed: content returned empty normal pool revision")
	}

	draft, err := CreateDraft(ctx, pool, DraftInput{
		SeriesID:             seriesID,
		RosterID:             roster.ID,
		SourcePoolRevisionID: content.NormalPoolRevisionID,
		FirstParticipantID:   participantIDs[0],
		SecondParticipantID:  participantIDs[1],
		Format:               domain.SeriesFormatBO1,
		CategoryPool: []domain.Category{
			domain.CategoryWeb,
			domain.CategoryCrypto,
			domain.CategoryPwn,
		},
		AbsoluteDeadline: input.CreatedAt.Add(15 * time.Second),
		CreatedAt:        input.CreatedAt,
	})
	if err != nil {
		return PreparedScope{}, fmt.Errorf("draft seed: create draft: %w", err)
	}

	return PreparedScope{
		TournamentID:       tournamentID,
		RosterID:           roster.ID,
		SeriesID:           seriesID,
		ParticipantIDs:     append([]uuid.UUID(nil), participantIDs[:2]...),
		ProjectionID:       proof.ProjectionID,
		ProjectionRevision: proof.ProjectionRevision,
		Content: ContentSeed{
			TaskIDs:              append([]uuid.UUID(nil), content.TaskIDs...),
			NormalPoolRevisionID: content.NormalPoolRevisionID,
		},
		Draft:     draft,
		CreatedAt: input.CreatedAt,
	}, nil
}
