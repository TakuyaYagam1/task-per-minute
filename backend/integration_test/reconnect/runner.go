//go:build integration

package reconnect

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/draftseed"
	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/gameseed"
	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/pauseseed"
	resultaudit "github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/resultaudit"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var (
	migrationPool   *pgxpool.Pool
	migrationPoolMu sync.Mutex
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

type reconnectMigrationFixture struct {
	draft               draftMigrationFixture
	attemptID           uuid.UUID
	normalPauseID       uuid.UUID
	normalWaveID        uuid.UUID
	rootPauseID         uuid.UUID
	rootPauseRevisionID uuid.UUID
	gamePauseID         uuid.UUID
	gamePauseRevisionID uuid.UUID
	firstIntervalID     uuid.UUID
	secondIntervalID    uuid.UUID
	firstDeadline       time.Time
	secondDeadline      time.Time
	pausedAt            time.Time
}

// runReconnectMigration binds an explicit pool to the migration helpers for
// one test. The binding mirrors the existing integration runner pattern while
// keeping the child package independent from the root integration package.
func runReconnectMigration(t *testing.T, pool *pgxpool.Pool, run func(context.Context, *testing.T)) {
	t.Helper()
	require.NotNil(t, pool)
	migrationPoolMu.Lock()
	previous := migrationPool
	migrationPool = pool
	// Register before the test body so the body's cleanup callbacks run first
	// while the explicit pool binding is still available.
	t.Cleanup(func() {
		migrationPool = previous
		migrationPoolMu.Unlock()
	})
	run(context.Background(), t)
}

func resetMigrationTables(ctx context.Context, tb testing.TB) {
	tb.Helper()
	_, err := migrationPool.Exec(ctx, `TRUNCATE TABLE participant_reservations, tournaments CASCADE`)
	require.NoError(tb, err)
}

func createReconnectMigrationFixture(ctx context.Context, tb testing.TB) reconnectMigrationFixture {
	tb.Helper()
	return createReconnectMigrationFixtureWithSlotLimit(ctx, tb, 2)
}

func createReconnectMigrationFixtureWithSlotLimit(
	ctx context.Context,
	tb testing.TB,
	slotLimit int,
) reconnectMigrationFixture {
	tb.Helper()

	draft := createDraftMigrationFixture(ctx, tb)
	lockedAt := time.Now().UTC().Add(5 * time.Second).Truncate(time.Microsecond)
	lockMigrationSeries(ctx, tb, draft, lockedAt)
	slotID, err := gameseed.CreateSlot(ctx, migrationPool, gameseed.SlotInput{
		SeriesID: draft.seriesID, RosterID: draft.rosterID, SlotNumber: 1,
		Category: domain.CategoryWeb, CreatedAt: lockedAt,
	})
	require.NoError(tb, err)
	attemptID, err := gameseed.CreateAttempt(ctx, migrationPool, slotID, draft.seriesID, draft.rosterID, lockedAt.Add(time.Second))
	require.NoError(tb, err)
	presenceAt := lockedAt.Add(2 * time.Second)
	for _, participantID := range draft.participantIDs {
		_, err = migrationPool.Exec(
			ctx, `
			INSERT INTO presence_states (
				tournament_id, roster_id, series_id, participant_id,
				state, connected_at, updated_at
			)
			VALUES ($1, $2, $3, $4, 'connected', $5, $5)`,
			draft.tournamentID, draft.rosterID, draft.seriesID, participantID, presenceAt,
		)
		require.NoError(tb, err)
	}

	rootPauseID, rootRevisionID := createMigrationPause(ctx, tb, draft, "series", draft.seriesID, nil, nil, 0, "operator", "locked", presenceAt.Add(time.Second), slotLimit)
	gamePauseID, gameRevisionID := createMigrationPause(ctx, tb, draft, "game_attempt", attemptID, &attemptID, &rootPauseID, 1, "disconnect", "active", presenceAt.Add(2*time.Second), slotLimit)
	return reconnectMigrationFixture{
		draft: draft, attemptID: attemptID,
		rootPauseID: rootPauseID, rootPauseRevisionID: rootRevisionID,
		gamePauseID: gamePauseID, gamePauseRevisionID: gameRevisionID,
		pausedAt: presenceAt.Add(2 * time.Second),
	}
}

func createDraftMigrationFixture(ctx context.Context, tb testing.TB) draftMigrationFixture {
	tb.Helper()
	createdAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	prepared, err := draftseed.Prepare(ctx, migrationPool, draftseed.PrepareInput{
		CreatedAt: createdAt,
		Content: func(contentCtx context.Context, pool *pgxpool.Pool, tournamentID uuid.UUID, at time.Time) (draftseed.ContentSeed, error) {
			return prepareReconnectContent(contentCtx, pool, tournamentID, at)
		},
	})
	require.NoError(tb, err)
	return draftMigrationFixture{
		tournamentID: prepared.TournamentID, rosterID: prepared.RosterID, seriesID: prepared.SeriesID,
		draftID: prepared.Draft.DraftID, categoryRevisionID: prepared.Draft.CategoryRevisionID,
		normalPoolRevisionID: prepared.Content.NormalPoolRevisionID,
		initialRevisionID:    prepared.Draft.InitialRevisionID,
		participantIDs:       append([]uuid.UUID(nil), prepared.Draft.FirstParticipantID, prepared.Draft.SecondParticipantID),
		initialServiceEpoch:  prepared.Draft.InitialServiceEpoch, createdAt: prepared.CreatedAt,
	}
}

func prepareReconnectContent(ctx context.Context, pool *pgxpool.Pool, tournamentID uuid.UUID, at time.Time) (draftseed.ContentSeed, error) {
	taskIDs := make([]uuid.UUID, 0, 24)
	for _, category := range []string{"web", "crypto", "pwn"} {
		for ordinal := 1; ordinal <= 8; ordinal++ {
			var taskID uuid.UUID
			err := pool.QueryRow(ctx, `
				INSERT INTO tasks (title, description, category, difficulty, time_limit, flag, kind)
				VALUES ($1, 'reconnect fixture task', $2, 'easy', 60, $3, 'normal')
				RETURNING id`,
				fmt.Sprintf("reconnect_fixture_%s_%d_%s", category, ordinal, uuid.NewString()[:8]),
				category, fmt.Sprintf("FLAG{reconnect-%s-%s}", category, uuid.NewString()[:8]),
			).Scan(&taskID)
			if err != nil {
				return draftseed.ContentSeed{}, err
			}
			taskIDs = append(taskIDs, taskID)
		}
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO tasks (title, description, category, difficulty, time_limit, flag, kind)
		VALUES ($1, 'reconnect fixture golden task', 'web', 'easy', 60, $2, 'golden')`,
		fmt.Sprintf("reconnect_fixture_golden_%s", uuid.NewString()[:8]), fmt.Sprintf("FLAG{reconnect-golden-%s}", uuid.NewString()[:8])); err != nil {
		return draftseed.ContentSeed{}, err
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO task_version_health_attestations (task_id, task_version, revision, healthy, source)
		SELECT version.task_id, version.version, 1, true, 'content_validation'
		FROM task_versions AS version
		WHERE NOT EXISTS (
			SELECT 1 FROM task_version_health_attestations AS attestation
			WHERE attestation.task_id = version.task_id AND attestation.task_version = version.version
		)`); err != nil {
		return draftseed.ContentSeed{}, err
	}
	if _, err := pool.Exec(ctx, `SELECT publish_task_pool_heads()`); err != nil {
		return draftseed.ContentSeed{}, err
	}

	var publicationID, normalPoolID, goldenPoolID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT publication.id, normal_pool.id, golden_pool.id
		FROM task_pool_publications AS publication
		INNER JOIN task_pool_revisions AS normal_pool
			ON normal_pool.publication_id = publication.id AND normal_pool.kind = 'normal'
		INNER JOIN task_pool_revisions AS golden_pool
			ON golden_pool.publication_id = publication.id AND golden_pool.kind = 'golden'
		ORDER BY publication.revision DESC
		LIMIT 1`).Scan(&publicationID, &normalPoolID, &goldenPoolID); err != nil {
		return draftseed.ContentSeed{}, err
	}
	configurationID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO tournament_content_configurations (
			id, tournament_id, revision, state, pool_publication_id,
			normal_pool_revision_id, golden_pool_revision_id, created_at
		)
		VALUES ($1, $2, 1, 'draft', $3, $4, $5, $6)`,
		configurationID, tournamentID, publicationID, normalPoolID, goldenPoolID, at); err != nil {
		return draftseed.ContentSeed{}, err
	}
	bo1PoolID, bo3PoolID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO tournament_category_pool_revisions (id, configuration_id, format, revision, created_at)
		VALUES ($1, $2, 'bo1', 1, $3), ($4, $2, 'bo3', 1, $3)`, bo1PoolID, configurationID, at, bo3PoolID); err != nil {
		return draftseed.ContentSeed{}, err
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO tournament_category_pool_memberships (category_pool_revision_id, category, created_at)
		VALUES
			($1, 'web', $3), ($1, 'crypto', $3), ($1, 'forensics', $3),
			($2, 'web', $3), ($2, 'crypto', $3), ($2, 'forensics', $3),
			($2, 'reverse', $3), ($2, 'pwn', $3)`, bo1PoolID, bo3PoolID, at); err != nil {
		return draftseed.ContentSeed{}, err
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
		configurationID, bo1PoolID, bo3PoolID, at); err != nil {
		return draftseed.ContentSeed{}, err
	}
	if _, err := pool.Exec(ctx, `
		UPDATE tournament_content_configurations
		SET state = 'published', published_at = $2
		WHERE id = $1`, configurationID, at); err != nil {
		return draftseed.ContentSeed{}, err
	}
	return draftseed.ContentSeed{TaskIDs: taskIDs, NormalPoolRevisionID: normalPoolID}, nil
}

func lockMigrationSeries(ctx context.Context, tb testing.TB, draft draftMigrationFixture, lockedAt time.Time) {
	tb.Helper()
	_, err := resultaudit.LockSeries(ctx, migrationPool, resultaudit.LockInput{
		Scope: resultaudit.Scope{
			TournamentID: draft.tournamentID, RosterID: draft.rosterID, SeriesID: draft.seriesID,
			ParticipantIDs: [2]uuid.UUID{draft.participantIDs[0], draft.participantIDs[1]},
		}, LockedAt: lockedAt,
	})
	require.NoError(tb, err)
}

func createMigrationPause(
	ctx context.Context,
	tb testing.TB,
	draft draftMigrationFixture,
	scopeKind string,
	scopeID uuid.UUID,
	gameAttemptID *uuid.UUID,
	parentPauseID *uuid.UUID,
	depth int,
	reason string,
	pausedFromState string,
	pausedAt time.Time,
	slotLimit int,
) (uuid.UUID, uuid.UUID) {
	tb.Helper()
	seed, err := pauseseed.CreatePause(ctx, migrationPool, pauseseed.Input{
		TournamentID: draft.tournamentID, RosterID: draft.rosterID, SeriesID: draft.seriesID,
		ParticipantIDs: draft.participantIDs, ScopeKind: scopeKind, ScopeID: scopeID,
		GameAttemptID: gameAttemptID, ParentPauseID: parentPauseID, Depth: depth,
		Reason: reason, PausedFromState: pausedFromState, PausedAt: pausedAt, SlotLimit: slotLimit,
	})
	require.NoError(tb, err)
	return seed.PauseID, seed.RevisionID
}
