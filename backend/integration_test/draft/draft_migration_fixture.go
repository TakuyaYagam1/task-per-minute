//go:build integration

package draft

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/draftseed"
)

func assertDraftTurnIdentityRejected(
	ctx context.Context, t *testing.T,
	fixture draftMigrationFixture,
) {
	t.Helper()

	for _, testCase := range []struct {
		name           string
		currentActorID uuid.UUID
		currentAction  string
	}{
		{
			name:           "wrong actor",
			currentActorID: fixture.participantIDs[1],
			currentAction:  "ban",
		},
		{
			name:           "wrong action",
			currentActorID: fixture.participantIDs[0],
			currentAction:  "pick",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := migrationPool.Exec(
				ctx, `
				INSERT INTO draft_revisions (
					draft_id, series_id, roster_id, revision,
					previous_revision_id, command_id, service_epoch,
					state, turn_number, current_actor_id, current_action,
					absolute_deadline, created_at
				)
				VALUES (
					$1, $2, $3, 2,
					$4, $5, $6,
					'active', 1, $7, $8,
					$9, $10
				)`,
				fixture.draftID,
				fixture.seriesID,
				fixture.rosterID,
				fixture.initialRevisionID,
				uuid.New(),
				fixture.initialServiceEpoch,
				testCase.currentActorID,
				testCase.currentAction,
				fixture.createdAt.Add(20*time.Second),
				fixture.createdAt.Add(time.Second),
			)
			require.Error(t, err)
		})
	}
}

func createDraftMigrationFixture(
	ctx context.Context, tb testing.TB,
) draftMigrationFixture {
	tb.Helper()
	return createDraftMigrationFixtureWithContentHook(ctx, tb, nil)
}

func createDraftMigrationFixtureWithContentHook(
	ctx context.Context,
	tb testing.TB,
	afterContentPrepared func([]uuid.UUID),
) draftMigrationFixture {
	tb.Helper()

	createdAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	prepared, err := draftseed.Prepare(ctx, migrationPool, draftseed.PrepareInput{
		CreatedAt: createdAt,
		Content: func(
			contentCtx context.Context,
			pool *pgxpool.Pool,
			tournamentID uuid.UUID,
			at time.Time,
		) (draftseed.ContentSeed, error) {
			normalTaskIDs := prepareDraftMigrationContent(ctx, tb)
			var normalPoolRevisionID uuid.UUID
			if afterContentPrepared != nil {
				afterContentPrepared(normalTaskIDs)
			}
			normalPoolRevisionID = createDraftContentConfigurationFromCurrentTasks(
				ctx, tb, tournamentID, at, normalTaskIDs,
			)
			return draftseed.ContentSeed{
				TaskIDs:              normalTaskIDs,
				NormalPoolRevisionID: normalPoolRevisionID,
			}, nil
		},
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

func createDraftContentConfigurationFromCurrentTasks(
	ctx context.Context,
	tb testing.TB,
	tournamentID uuid.UUID,
	at time.Time,
	taskIDs []uuid.UUID,
) uuid.UUID {
	tb.Helper()

	var publicationID, normalPoolID, goldenPoolID uuid.UUID
	err := migrationPool.QueryRow(ctx, `
		SELECT publication.id, normal_pool.id, golden_pool.id
		FROM task_pool_publications AS publication
		INNER JOIN task_pool_revisions AS normal_pool
			ON normal_pool.publication_id = publication.id AND normal_pool.kind = 'normal'
		INNER JOIN task_pool_revisions AS golden_pool
			ON golden_pool.publication_id = publication.id AND golden_pool.kind = 'golden'
		WHERE (
			SELECT COUNT(*)
			FROM task_pool_version_memberships AS membership
			INNER JOIN tasks AS task
				ON task.id = membership.task_id
				AND task.current_version = membership.task_version
			WHERE membership.task_pool_revision_id = normal_pool.id
				AND membership.task_id = ANY($1::UUID[])
		) = cardinality($1::UUID[])
		AND EXISTS (
			SELECT 1
			FROM task_pool_version_memberships AS membership
			INNER JOIN tasks AS task ON task.id = membership.task_id
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
		ORDER BY publication.revision DESC
		LIMIT 1`, taskIDs).Scan(&publicationID, &normalPoolID, &goldenPoolID)
	require.NoError(tb, err)

	configurationID := uuid.New()
	_, err = migrationPool.Exec(ctx, `
		INSERT INTO tournament_content_configurations (
			id, tournament_id, revision, state, pool_publication_id,
			normal_pool_revision_id, golden_pool_revision_id, created_at
		)
		VALUES ($1, $2, 1, 'draft', $3, $4, $5, $6)`,
		configurationID, tournamentID, publicationID, normalPoolID, goldenPoolID, at)
	require.NoError(tb, err)

	bo1PoolID := uuid.New()
	bo3PoolID := uuid.New()
	_, err = migrationPool.Exec(ctx, `
		INSERT INTO tournament_category_pool_revisions (
			id, configuration_id, format, revision, created_at
		)
		VALUES ($1, $2, 'bo1', 1, $3), ($4, $2, 'bo3', 1, $3)`,
		bo1PoolID, configurationID, at, bo3PoolID)
	require.NoError(tb, err)
	_, err = migrationPool.Exec(ctx, `
		INSERT INTO tournament_category_pool_memberships (category_pool_revision_id, category, created_at)
		VALUES
			($1, 'web', $3), ($1, 'crypto', $3), ($1, 'forensics', $3),
			($2, 'web', $3), ($2, 'crypto', $3), ($2, 'forensics', $3),
			($2, 'reverse', $3), ($2, 'pwn', $3)`,
		bo1PoolID, bo3PoolID, at)
	require.NoError(tb, err)
	insertTournamentContentStageDefaults(ctx, tb, configurationID, bo1PoolID, bo3PoolID, at)
	_, err = migrationPool.Exec(ctx, `
		UPDATE tournament_content_configurations
		SET state = 'published', published_at = $2
		WHERE id = $1`, configurationID, at)
	require.NoError(tb, err)
	return normalPoolID
}

func prepareDraftMigrationContent(ctx context.Context, tb testing.TB) []uuid.UUID {
	tb.Helper()

	normalTaskIDs := make([]uuid.UUID, 0, 24)
	for _, category := range []string{"web", "crypto", "pwn"} {
		for ordinal := 1; ordinal <= 8; ordinal++ {
			var taskID uuid.UUID
			err := migrationPool.QueryRow(ctx, `
				INSERT INTO tasks (title, description, category, difficulty, time_limit, flag, kind)
				VALUES ($1, 'draft fixture task', $2, 'easy', 60, $3, 'normal')
				RETURNING id`,
				"draft_fixture_"+category+"_"+uuid.NewString()[:8],
				category,
				"FLAG{draft-"+category+"-"+uuid.NewString()[:8]+"}",
			).Scan(&taskID)
			require.NoError(tb, err)
			normalTaskIDs = append(normalTaskIDs, taskID)
		}
	}
	_, err := migrationPool.Exec(ctx, `
		INSERT INTO tasks (title, description, category, difficulty, time_limit, flag, kind)
		VALUES ($1, 'draft fixture golden task', 'web', 'easy', 60, $2, 'golden')`,
		"draft_fixture_golden_"+uuid.NewString()[:8],
		"FLAG{draft-golden-"+uuid.NewString()[:8]+"}",
	)
	require.NoError(tb, err)
	_, err = migrationPool.Exec(ctx, `
		INSERT INTO task_version_health_attestations (task_id, task_version, revision, healthy, source)
		SELECT version.task_id, version.version, 1, true, 'content_validation'
		FROM task_versions AS version
		WHERE NOT EXISTS (
			SELECT 1
			FROM task_version_health_attestations AS attestation
			WHERE attestation.task_id = version.task_id
				AND attestation.task_version = version.version
		)`)
	require.NoError(tb, err)
	_, err = migrationPool.Exec(ctx, `SELECT publish_task_pool_heads()`)
	require.NoError(tb, err)
	return normalTaskIDs
}

func insertTournamentContentStageDefaults(
	ctx context.Context,
	tb testing.TB,
	configurationID uuid.UUID,
	bo1PoolID uuid.UUID,
	bo3PoolID uuid.UUID,
	createdAt time.Time,
) {
	tb.Helper()
	_, err := migrationPool.Exec(ctx, `
		INSERT INTO tournament_content_stage_defaults (
			configuration_id, stage, format, category_mode, categories,
			category_pool_revision_id, task_pool_kind, created_at
		)
		VALUES
			($1, 'swiss', 'bo1', 'random', '["web"]'::jsonb, $2, 'normal', $4),
			($1, 'golden', 'bo1', 'random', '["web"]'::jsonb, $2, 'golden', $4),
			($1, 'semifinal', 'bo1', 'draft', '["web", "crypto", "forensics"]'::jsonb, $2, 'normal', $4),
			($1, 'final', 'bo3', 'draft', '["web", "crypto", "forensics", "reverse", "pwn"]'::jsonb, $3, 'normal', $4)`,
		configurationID, bo1PoolID, bo3PoolID, createdAt)
	require.NoError(tb, err)
}
