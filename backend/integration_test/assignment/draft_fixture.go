//go:build integration

package assignment

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/draftseed"
)

type draftMigrationFixture struct {
	tournamentID         uuid.UUID
	rosterID             uuid.UUID
	seriesID             uuid.UUID
	draftID              uuid.UUID
	categoryRevisionID   uuid.UUID
	normalPoolRevisionID uuid.UUID
	initialRevisionID    uuid.UUID
	participantIDs       []uuid.UUID
	initialServiceEpoch  uuid.UUID
	createdAt            time.Time
}

func createDraftMigrationFixture(ctx context.Context, tb testing.TB) draftMigrationFixture {
	tb.Helper()
	createdAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	prepared, err := draftseed.Prepare(ctx, migrationPool, draftseed.PrepareInput{
		CreatedAt: createdAt,
		Content:   prepareAssignmentDraftContent,
	})
	require.NoError(tb, err)

	return draftMigrationFixture{
		tournamentID:         prepared.TournamentID,
		rosterID:             prepared.RosterID,
		seriesID:             prepared.SeriesID,
		draftID:              prepared.Draft.DraftID,
		categoryRevisionID:   prepared.Draft.CategoryRevisionID,
		normalPoolRevisionID: prepared.Content.NormalPoolRevisionID,
		initialRevisionID:    prepared.Draft.InitialRevisionID,
		participantIDs:       append([]uuid.UUID(nil), prepared.Draft.FirstParticipantID, prepared.Draft.SecondParticipantID),
		initialServiceEpoch:  prepared.Draft.InitialServiceEpoch,
		createdAt:            prepared.CreatedAt,
	}
}

func prepareAssignmentDraftContent(
	ctx context.Context,
	pool *pgxpool.Pool,
	tournamentID uuid.UUID,
	at time.Time,
) (draftseed.ContentSeed, error) {
	taskIDs, err := createAssignmentDraftTasks(ctx, pool)
	if err != nil {
		return draftseed.ContentSeed{}, err
	}
	normalPoolID, err := createAssignmentContentConfiguration(ctx, pool, tournamentID, at, taskIDs)
	if err != nil {
		return draftseed.ContentSeed{}, err
	}
	return draftseed.ContentSeed{
		TaskIDs:              taskIDs,
		NormalPoolRevisionID: normalPoolID,
	}, nil
}

func createAssignmentDraftTasks(ctx context.Context, pool *pgxpool.Pool) ([]uuid.UUID, error) {
	normalTaskIDs := make([]uuid.UUID, 0, 24)
	for _, category := range []string{"web", "crypto", "pwn"} {
		for ordinal := 1; ordinal <= 8; ordinal++ {
			var taskID uuid.UUID
			err := pool.QueryRow(ctx, `
				INSERT INTO tasks (title, description, category, difficulty, time_limit, flag, kind)
				VALUES ($1, 'draft fixture task', $2, 'easy', 60, $3, 'normal')
				RETURNING id`,
				"draft_fixture_"+category+"_"+uuid.NewString()[:8],
				category,
				fmt.Sprintf("FLAG{draft-%s-%s}", category, uuid.NewString()[:8]),
			).Scan(&taskID)
			if err != nil {
				return nil, fmt.Errorf("assignment draft seed: create %s task %d: %w", category, ordinal, err)
			}
			normalTaskIDs = append(normalTaskIDs, taskID)
		}
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO tasks (title, description, category, difficulty, time_limit, flag, kind)
		VALUES ($1, 'draft fixture golden task', 'web', 'easy', 60, $2, 'golden')`,
		fmt.Sprintf("draft_fixture_golden_%s", uuid.NewString()[:8]),
		fmt.Sprintf("FLAG{draft-golden-%s}", uuid.NewString()[:8]),
	); err != nil {
		return nil, fmt.Errorf("assignment draft seed: create golden task: %w", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO task_version_health_attestations (task_id, task_version, revision, healthy, source)
		SELECT version.task_id, version.version, 1, true, 'content_validation'
		FROM task_versions AS version
		WHERE NOT EXISTS (
			SELECT 1
			FROM task_version_health_attestations AS attestation
			WHERE attestation.task_id = version.task_id
				AND attestation.task_version = version.version
		)`); err != nil {
		return nil, fmt.Errorf("assignment draft seed: attest task versions: %w", err)
	}
	if _, err := pool.Exec(ctx, `SELECT publish_task_pool_heads()`); err != nil {
		return nil, fmt.Errorf("assignment draft seed: publish task pools: %w", err)
	}
	return normalTaskIDs, nil
}

func createAssignmentContentConfiguration(
	ctx context.Context,
	pool *pgxpool.Pool,
	tournamentID uuid.UUID,
	at time.Time,
	taskIDs []uuid.UUID,
) (uuid.UUID, error) {
	var publicationID, normalPoolID, goldenPoolID uuid.UUID
	var normalPoolRevision int64
	err := pool.QueryRow(ctx, `
		SELECT publication.id, normal_pool.id, normal_pool.revision, golden_pool.id
		FROM task_pool_publications AS publication
		INNER JOIN task_pool_revisions AS normal_pool
			ON normal_pool.publication_id = publication.id AND normal_pool.kind = 'normal'
		INNER JOIN task_pool_revisions AS golden_pool
			ON golden_pool.publication_id = publication.id AND golden_pool.kind = 'golden'
		WHERE (
			SELECT COUNT(DISTINCT membership.task_id)
			FROM task_pool_version_memberships AS membership
			WHERE membership.task_pool_revision_id = normal_pool.id
				AND membership.task_id = ANY($1::UUID[])
		) = cardinality($1::UUID[])
		AND EXISTS (
			SELECT 1
			FROM task_pool_version_memberships AS membership
			INNER JOIN tasks AS task
				ON task.id = membership.task_id
			INNER JOIN task_versions AS version
				ON version.task_id = membership.task_id
				AND version.version = membership.task_version
			LEFT JOIN LATERAL (
				SELECT attestation.healthy
				FROM task_version_health_attestations AS attestation
				WHERE attestation.task_id = membership.task_id
					AND attestation.task_version = membership.task_version
				ORDER BY attestation.revision DESC
				LIMIT 1
			) AS health ON true
			WHERE membership.task_pool_revision_id = golden_pool.id
				AND task.kind = 'golden'
				AND task.enabled
				AND task.deleted_at IS NULL
				AND health.healthy
		)
		ORDER BY publication.revision ASC
		LIMIT 1`, taskIDs).Scan(
		&publicationID, &normalPoolID, &normalPoolRevision, &goldenPoolID,
	)
	if err != nil {
		return uuid.Nil, fmt.Errorf("assignment draft seed: find task pool publication: %w", err)
	}

	configurationID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO tournament_content_configurations (
			id, tournament_id, revision, state, pool_publication_id,
			normal_pool_revision_id, golden_pool_revision_id, created_at
		)
		VALUES ($1, $2, 1, 'draft', $3, $4, $5, $6)`,
		configurationID, tournamentID, publicationID, normalPoolID, goldenPoolID, at,
	); err != nil {
		return uuid.Nil, fmt.Errorf("assignment draft seed: create content configuration: %w", err)
	}

	bo1PoolID := uuid.New()
	bo3PoolID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO tournament_category_pool_revisions (
			id, configuration_id, format, revision, created_at
		)
		VALUES ($1, $2, 'bo1', 1, $3), ($4, $2, 'bo3', 1, $3)`,
		bo1PoolID, configurationID, at, bo3PoolID,
	); err != nil {
		return uuid.Nil, fmt.Errorf("assignment draft seed: create category pools: %w", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO tournament_category_pool_memberships (category_pool_revision_id, category, created_at)
		VALUES
			($1, 'web', $3), ($1, 'crypto', $3), ($1, 'forensics', $3),
			($2, 'web', $3), ($2, 'crypto', $3), ($2, 'forensics', $3),
			($2, 'reverse', $3), ($2, 'pwn', $3)`,
		bo1PoolID, bo3PoolID, at,
	); err != nil {
		return uuid.Nil, fmt.Errorf("assignment draft seed: create category memberships: %w", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO tournament_content_stage_defaults (
			configuration_id, stage, format, category_mode, categories,
			category_pool_revision_id, task_pool_kind, created_at
		)
		VALUES
			($1, 'swiss', 'bo1', 'random', '["web"]'::jsonb, $2, 'normal', $4),
			($1, 'golden', 'bo1', 'random', '["web"]'::jsonb, $2, 'golden', $4),
			($1, 'semifinal', 'bo1', 'draft', '["web", "crypto", "forensics"]'::jsonb, $2, 'normal', $4),
			($1, 'final', 'bo3', 'draft', '["web", "crypto", "forensics", "reverse", "pwn"]'::jsonb, $3, 'normal', $4)`,
		configurationID, bo1PoolID, bo3PoolID, at,
	); err != nil {
		return uuid.Nil, fmt.Errorf("assignment draft seed: create stage defaults: %w", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE tournament_content_configurations
		SET state = 'published', published_at = $2
		WHERE id = $1`, configurationID, at); err != nil {
		return uuid.Nil, fmt.Errorf("assignment draft seed: publish content configuration: %w", err)
	}

	_ = normalPoolRevision
	return normalPoolID, nil
}
